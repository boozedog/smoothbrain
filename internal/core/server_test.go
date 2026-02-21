package core

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/boozedog/smoothbrain/internal/plugin"
	"github.com/boozedog/smoothbrain/internal/store"
)

// --- stubs ---

type stubHealthPlugin struct {
	name   string
	status plugin.HealthStatus
}

func (s *stubHealthPlugin) Name() string                                    { return s.name }
func (s *stubHealthPlugin) Init(json.RawMessage) error                      { return nil }
func (s *stubHealthPlugin) Start(context.Context, plugin.EventBus) error    { return nil }
func (s *stubHealthPlugin) Stop() error                                     { return nil }
func (s *stubHealthPlugin) HealthCheck(context.Context) plugin.HealthStatus { return s.status }

// --- helpers ---

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := plugin.NewRegistry(log, st.DB())
	hub := NewHub(st, log)
	srv := NewServer(st, log, hub, reg, nil, NewLogBuffer(10))
	return srv, st
}

// --- queryEvents tests ---

func TestQueryEvents_Empty(t *testing.T) {
	srv, _ := newTestServer(t)
	events := queryEvents(srv.store, srv.log)
	if events == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestQueryEvents_Populated(t *testing.T) {
	srv, st := newTestServer(t)
	_, err := st.DB().Exec(
		`INSERT INTO events (id, source, type, payload, timestamp, route) VALUES (?, ?, ?, ?, ?, ?)`,
		"evt-100", "webhook", "push", `{"ref":"main"}`, "2025-01-01T00:00:00Z", "my-route",
	)
	if err != nil {
		t.Fatal(err)
	}

	events := queryEvents(srv.store, srv.log)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e["id"] != "evt-100" {
		t.Errorf("id = %v, want evt-100", e["id"])
	}
	if e["source"] != "webhook" {
		t.Errorf("source = %v, want webhook", e["source"])
	}
	if e["type"] != "push" {
		t.Errorf("type = %v, want push", e["type"])
	}
	if e["route"] != "my-route" {
		t.Errorf("route = %v, want my-route", e["route"])
	}
	if e["timestamp"] != "2025-01-01T00:00:00Z" {
		t.Errorf("timestamp = %v, want 2025-01-01T00:00:00Z", e["timestamp"])
	}
	// Payload should be json.RawMessage.
	raw, ok := e["payload"].(json.RawMessage)
	if !ok {
		t.Fatalf("payload type = %T, want json.RawMessage", e["payload"])
	}
	if string(raw) != `{"ref":"main"}` {
		t.Errorf("payload = %s, want {\"ref\":\"main\"}", raw)
	}
}

// --- queryPipelineRuns tests ---

func TestQueryPipelineRuns_Empty(t *testing.T) {
	srv, _ := newTestServer(t)
	runs := queryPipelineRuns(srv.store, srv.log, "nonexistent")
	if runs == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}

func TestQueryPipelineRuns_Populated(t *testing.T) {
	srv, st := newTestServer(t)

	// Insert a parent event first.
	_, err := st.DB().Exec(
		`INSERT INTO events (id, source, type, payload, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"evt-200", "src", "test", "{}", "2025-01-01T00:00:00Z",
	)
	if err != nil {
		t.Fatal(err)
	}

	// Insert a pipeline run with a duration.
	_, err = st.DB().Exec(
		`INSERT INTO pipeline_runs (event_id, route, status, started_at, finished_at, duration_ms, steps)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"evt-200", "test-route", "completed", "2025-01-01T00:00:00Z", "2025-01-01T00:00:01Z", 150, `[]`,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Insert a pipeline run without a duration (0 maps to nil).
	_, err = st.DB().Exec(
		`INSERT INTO pipeline_runs (event_id, route, status, started_at)
		 VALUES (?, ?, ?, ?)`,
		"evt-200", "test-route-2", "running", "2025-01-01T00:00:02Z",
	)
	if err != nil {
		t.Fatal(err)
	}

	runs := queryPipelineRuns(srv.store, srv.log, "evt-200")
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}

	// Runs are ORDER BY id DESC, so the second insert comes first.
	r0 := runs[0]
	if r0.Route != "test-route-2" {
		t.Errorf("run[0].Route = %q, want test-route-2", r0.Route)
	}
	if r0.Status != "running" {
		t.Errorf("run[0].Status = %q, want running", r0.Status)
	}
	if r0.DurationMs != nil {
		t.Errorf("run[0].DurationMs = %v, want nil", *r0.DurationMs)
	}

	r1 := runs[1]
	if r1.Route != "test-route" {
		t.Errorf("run[1].Route = %q, want test-route", r1.Route)
	}
	if r1.Status != "completed" {
		t.Errorf("run[1].Status = %q, want completed", r1.Status)
	}
	if r1.DurationMs == nil {
		t.Fatal("run[1].DurationMs = nil, want 150")
	}
	if *r1.DurationMs != 150 {
		t.Errorf("run[1].DurationMs = %d, want 150", *r1.DurationMs)
	}
}

// --- HTTP handler tests ---

func TestHandleHealth_OK(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
}

func TestHandleHealth_Error(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := plugin.NewRegistry(log, st.DB())
	reg.Register(&stubHealthPlugin{
		name:   "bad-plugin",
		status: plugin.HealthStatus{Status: plugin.StatusError, Message: "down"},
	})
	if err := reg.InitAll(nil); err != nil {
		t.Fatal(err)
	}
	hub := NewHub(st, log)
	srv := NewServer(st, log, hub, reg, nil, NewLogBuffer(10))

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "error" {
		t.Errorf("status = %v, want error", body["status"])
	}
}

func TestHandleEvents_JSON(t *testing.T) {
	srv, st := newTestServer(t)
	_, err := st.DB().Exec(
		`INSERT INTO events (id, source, type, payload, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"evt-300", "src", "test", `{"k":"v"}`, "2025-01-01T00:00:00Z",
	)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var events []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&events); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0]["id"] != "evt-300" {
		t.Errorf("id = %v, want evt-300", events[0]["id"])
	}
}

func TestHandleEventRuns_JSON(t *testing.T) {
	srv, st := newTestServer(t)
	_, err := st.DB().Exec(
		`INSERT INTO events (id, source, type, payload, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"evt-400", "src", "test", "{}", "2025-01-01T00:00:00Z",
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.DB().Exec(
		`INSERT INTO pipeline_runs (event_id, route, status, started_at, duration_ms, steps)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"evt-400", "r1", "completed", "2025-01-01T00:00:00Z", 42, `[]`,
	)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/events/evt-400/runs", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var runs []pipelineRun
	if err := json.NewDecoder(rec.Body).Decode(&runs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].EventID != "evt-400" {
		t.Errorf("event_id = %q, want evt-400", runs[0].EventID)
	}
	if runs[0].DurationMs == nil || *runs[0].DurationMs != 42 {
		t.Errorf("duration_ms = %v, want 42", runs[0].DurationMs)
	}
}

func TestRegisterWebhook(t *testing.T) {
	srv, _ := newTestServer(t)
	var called bool
	srv.RegisterWebhook("test-hook", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/hooks/test-hook", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if !called {
		t.Error("webhook handler was not called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestNewServer_HandlerNotNil(t *testing.T) {
	srv, _ := newTestServer(t)
	if srv.Handler() == nil {
		t.Error("Handler() returned nil")
	}
}
