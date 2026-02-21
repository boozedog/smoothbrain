package core

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/boozedog/smoothbrain/internal/config"
	"github.com/boozedog/smoothbrain/internal/plugin"
	"github.com/boozedog/smoothbrain/internal/store"
)

const healthCheckTimeout = 5 * time.Second

//go:embed all:web
var webFS embed.FS

type Server struct {
	mux      *http.ServeMux
	store    *store.Store
	log      *slog.Logger
	registry *plugin.Registry
	routes   []config.RouteConfig
	logBuf   *LogBuffer
}

func NewServer(s *store.Store, log *slog.Logger, hub *Hub, registry *plugin.Registry, routes []config.RouteConfig, logBuf *LogBuffer) *Server {
	srv := &Server{
		mux:      http.NewServeMux(),
		store:    s,
		log:      log,
		registry: registry,
		routes:   routes,
		logBuf:   logBuf,
	}
	srv.mux.HandleFunc("GET /api/health", srv.handleHealth)
	srv.mux.HandleFunc("GET /api/health/html", srv.handleHealthHTML)
	srv.mux.HandleFunc("GET /api/events", srv.handleEvents)
	srv.mux.HandleFunc("GET /api/events/html", srv.handleEventsHTML)
	srv.mux.HandleFunc("GET /api/events/{id}/runs", srv.handleEventRuns)
	srv.mux.HandleFunc("GET /api/status/html", srv.handleStatusHTML)
	srv.mux.HandleFunc("GET /api/log/html", srv.handleLogHTML)
	srv.mux.Handle("GET /ws", hub)

	// Serve embedded static files at root.
	webRoot, err := fs.Sub(webFS, "web")
	if err != nil {
		panic("embedded web assets missing: " + err.Error())
	}
	srv.mux.Handle("GET /", http.FileServer(http.FS(webRoot)))

	return srv
}

// Handler returns the http.Handler for use with http.Server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Mux returns the underlying ServeMux for registering additional routes.
func (s *Server) Mux() *http.ServeMux {
	return s.mux
}

// RegisterWebhook registers a POST handler at /hooks/{name}.
func (s *Server) RegisterWebhook(name string, handler http.HandlerFunc) {
	s.mux.HandleFunc("POST /hooks/"+name, handler)
	s.log.Info("webhook registered", "path", "/hooks/"+name)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	agg, results := s.registry.AggregateHealth(r.Context(), healthCheckTimeout)
	w.Header().Set("Content-Type", "application/json")
	if agg.Status == plugin.StatusError {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status":  agg.Status,
		"message": agg.Message,
		"plugins": results,
	}); err != nil {
		s.log.Error("failed to encode health response", "error", err)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	events := queryEvents(s.store, s.log)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(events); err != nil {
		s.log.Error("failed to encode events response", "error", err)
	}
}

func (s *Server) handleEventsHTML(w http.ResponseWriter, r *http.Request) {
	events := queryEvents(s.store, s.log)
	views := toEventViews(events, s.store, s.log)
	w.Header().Set("Content-Type", "text/html")
	if err := EventsTable(views).Render(r.Context(), w); err != nil {
		s.log.Error("render events table", "error", err)
	}
}

func (s *Server) handleEventRuns(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	runs := queryPipelineRuns(s.store, s.log, eventID)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(runs); err != nil {
		s.log.Error("failed to encode event runs response", "error", err)
	}
}

func (s *Server) handleHealthHTML(w http.ResponseWriter, r *http.Request) {
	agg, _ := s.registry.AggregateHealth(r.Context(), healthCheckTimeout)
	w.Header().Set("Content-Type", "text/html")
	if err := HealthBadge(string(agg.Status)).Render(r.Context(), w); err != nil {
		s.log.Error("render health badge", "error", err)
	}
}

func (s *Server) handleStatusHTML(w http.ResponseWriter, r *http.Request) {
	info := buildStatusInfo(r.Context(), s.registry, s.routes)
	w.Header().Set("Content-Type", "text/html")
	if err := StatusTab(info).Render(r.Context(), w); err != nil {
		s.log.Error("render status tab", "error", err)
	}
}

func (s *Server) handleLogHTML(w http.ResponseWriter, r *http.Request) {
	entries := s.logBuf.Entries()
	w.Header().Set("Content-Type", "text/html")
	if err := SystemLog(entries).Render(r.Context(), w); err != nil {
		s.log.Error("render system log", "error", err)
	}
}

func queryEvents(s *store.Store, log *slog.Logger) []map[string]any {
	rows, err := s.DB().Query(
		`SELECT id, source, type, payload, timestamp, COALESCE(route, '') FROM events ORDER BY created_at DESC LIMIT 50`,
	)
	if err != nil {
		log.Error("query events failed", "error", err)
		return []map[string]any{}
	}
	defer func() { _ = rows.Close() }()

	var events []map[string]any
	for rows.Next() {
		var id, source, typ, payload, ts, route string
		if err := rows.Scan(&id, &source, &typ, &payload, &ts, &route); err != nil {
			continue
		}
		events = append(events, map[string]any{
			"id":        id,
			"source":    source,
			"type":      typ,
			"payload":   json.RawMessage(payload),
			"timestamp": ts,
			"route":     route,
		})
	}
	if err := rows.Err(); err != nil {
		log.Error("rows iteration error", "error", err)
	}
	if events == nil {
		events = []map[string]any{}
	}
	return events
}

type pipelineRun struct {
	ID         int64  `json:"id"`
	EventID    string `json:"event_id"`
	Route      string `json:"route"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	DurationMs *int64 `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
	Steps      string `json:"steps,omitempty"`
}

func queryPipelineRuns(s *store.Store, log *slog.Logger, eventID string) []pipelineRun {
	rows, err := s.DB().Query(
		`SELECT id, event_id, route, status, started_at, COALESCE(finished_at, ''), COALESCE(duration_ms, 0), COALESCE(error, ''), COALESCE(steps, '[]')
		 FROM pipeline_runs WHERE event_id = ? ORDER BY id DESC`,
		eventID,
	)
	if err != nil {
		log.Error("query pipeline runs failed", "error", err)
		return []pipelineRun{}
	}
	defer func() { _ = rows.Close() }()

	var runs []pipelineRun
	for rows.Next() {
		var r pipelineRun
		var dur int64
		if err := rows.Scan(&r.ID, &r.EventID, &r.Route, &r.Status, &r.StartedAt, &r.FinishedAt, &dur, &r.Error, &r.Steps); err != nil {
			continue
		}
		if dur > 0 {
			r.DurationMs = &dur
		}
		runs = append(runs, r)
	}
	if runs == nil {
		runs = []pipelineRun{}
	}
	return runs
}
