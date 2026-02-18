package core

import (
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/dmarx/smoothbrain/internal/store"
)

//go:embed all:web
var webFS embed.FS

type Server struct {
	mux   *http.ServeMux
	store *store.Store
	log   *slog.Logger
}

func NewServer(s *store.Store, log *slog.Logger, hub *Hub) *Server {
	srv := &Server{
		mux:   http.NewServeMux(),
		store: s,
		log:   log,
	}
	srv.mux.HandleFunc("GET /api/health", srv.handleHealth)
	srv.mux.HandleFunc("GET /api/events", srv.handleEvents)
	srv.mux.HandleFunc("GET /api/events/html", srv.handleEventsHTML)
	srv.mux.HandleFunc("GET /api/events/{id}/runs", srv.handleEventRuns)
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

// RegisterWebhook registers a POST handler at /hooks/{name}.
func (s *Server) RegisterWebhook(name string, handler http.HandlerFunc) {
	s.mux.HandleFunc("POST /hooks/"+name, handler)
	s.log.Info("webhook registered", "path", "/hooks/"+name)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	events := queryEvents(s.store, s.log)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

func (s *Server) handleEventsHTML(w http.ResponseWriter, r *http.Request) {
	events := queryEvents(s.store, s.log)
	w.Header().Set("Content-Type", "text/html")
	renderEventsHTML(w, events, s.store, s.log)
}

func (s *Server) handleEventRuns(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	runs := queryPipelineRuns(s.store, s.log, eventID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(runs)
}

func queryEvents(s *store.Store, log *slog.Logger) []map[string]any {
	rows, err := s.DB().Query(
		`SELECT id, source, type, payload, timestamp, COALESCE(route, '') FROM events ORDER BY created_at DESC LIMIT 50`,
	)
	if err != nil {
		log.Error("query events failed", "error", err)
		return []map[string]any{}
	}
	defer rows.Close()

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
	defer rows.Close()

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

func renderEventsHTML(w io.Writer, events []map[string]any, s *store.Store, log *slog.Logger) {
	if len(events) == 0 {
		fmt.Fprint(w, `<div class="empty">No events yet.</div>`)
		return
	}

	var b strings.Builder
	b.WriteString(`<table class="striped"><thead><tr><th>Time</th><th>Source</th><th>Type</th><th>Route</th><th>ID</th></tr></thead><tbody>`)
	for _, e := range events {
		// Summary row (clickable).
		b.WriteString(`<tr class="event-row">`)
		b.WriteString(fmt.Sprintf(`<td class="mono">%s</td>`, html.EscapeString(str(e["timestamp"]))))
		b.WriteString(fmt.Sprintf(`<td><span class="badge badge-source">%s</span></td>`, html.EscapeString(str(e["source"]))))
		b.WriteString(fmt.Sprintf(`<td><span class="badge badge-type">%s</span></td>`, html.EscapeString(str(e["type"]))))
		b.WriteString(fmt.Sprintf(`<td>%s</td>`, html.EscapeString(str(e["route"]))))
		id := str(e["id"])
		if len(id) > 8 {
			id = id[:8]
		}
		b.WriteString(fmt.Sprintf(`<td class="mono">%s</td>`, html.EscapeString(id)))
		b.WriteString("</tr>")

		// Detail row (hidden until toggled): payload + pipeline runs.
		b.WriteString(`<tr class="payload-row"><td colspan="5">`)

		// Payload section.
		var pretty string
		if raw, ok := e["payload"].(json.RawMessage); ok {
			var buf json.RawMessage
			if json.Unmarshal(raw, &buf) == nil {
				if pp, err := json.MarshalIndent(buf, "", "  "); err == nil {
					pretty = string(pp)
				}
			}
		}
		if pretty == "" {
			pretty = str(e["payload"])
		}
		b.WriteString(fmt.Sprintf(`<pre>%s</pre>`, html.EscapeString(pretty)))

		// Pipeline runs section.
		runs := queryPipelineRuns(s, log, str(e["id"]))
		if len(runs) > 0 {
			renderPipelineRunsHTML(&b, runs)
		}

		b.WriteString(`</td></tr>`)
	}
	b.WriteString(`</tbody></table>`)
	fmt.Fprint(w, b.String())
}

func renderPipelineRunsHTML(b *strings.Builder, runs []pipelineRun) {
	b.WriteString(`<div class="pipeline-runs">`)
	for _, r := range runs {
		badgeClass := "badge-run-" + r.Status
		b.WriteString(fmt.Sprintf(`<div class="pipeline-run"><span class="badge %s">%s</span> `, badgeClass, html.EscapeString(r.Status)))
		b.WriteString(fmt.Sprintf(`<strong>%s</strong>`, html.EscapeString(r.Route)))
		if r.DurationMs != nil {
			b.WriteString(fmt.Sprintf(` <span class="mono">%dms</span>`, *r.DurationMs))
		}
		if r.Error != "" {
			b.WriteString(fmt.Sprintf(` <span class="run-error">%s</span>`, html.EscapeString(r.Error)))
		}

		// Render steps.
		var steps []stepResult
		if json.Unmarshal([]byte(r.Steps), &steps) == nil && len(steps) > 0 {
			b.WriteString(`<ul class="pipeline-steps">`)
			for _, step := range steps {
				stepBadge := "badge-run-" + step.Status
				b.WriteString(fmt.Sprintf(`<li><span class="badge %s">%s</span> %s.%s <span class="mono">%dms</span>`,
					stepBadge, html.EscapeString(step.Status),
					html.EscapeString(step.Plugin), html.EscapeString(step.Action),
					step.DurationMs))
				if step.Error != "" {
					b.WriteString(fmt.Sprintf(` <span class="run-error">%s</span>`, html.EscapeString(step.Error)))
				}
				b.WriteString(`</li>`)
			}
			b.WriteString(`</ul>`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div>`)
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
