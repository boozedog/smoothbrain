package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/boozedog/smoothbrain/internal/config"
	"github.com/boozedog/smoothbrain/internal/plugin"
	"github.com/boozedog/smoothbrain/internal/store"
)

// --- stubs ---

type stubTransform struct {
	name   string
	called int
	err    error
	mu     sync.Mutex
}

func (s *stubTransform) Name() string                                 { return s.name }
func (s *stubTransform) Init(json.RawMessage) error                   { return nil }
func (s *stubTransform) Start(context.Context, plugin.EventBus) error { return nil }
func (s *stubTransform) Stop() error                                  { return nil }
func (s *stubTransform) Transform(_ context.Context, e plugin.Event, _ string, _ map[string]any) (plugin.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.called++
	if s.err != nil {
		return e, s.err
	}
	e.Payload["transformed_by_"+s.name] = true
	return e, nil
}

type stubSink struct {
	name   string
	events []plugin.Event
	err    error
	mu     sync.Mutex
}

func (s *stubSink) Name() string                                 { return s.name }
func (s *stubSink) Init(json.RawMessage) error                   { return nil }
func (s *stubSink) Start(context.Context, plugin.EventBus) error { return nil }
func (s *stubSink) Stop() error                                  { return nil }
func (s *stubSink) HandleEvent(_ context.Context, e plugin.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return s.err
}

// newTestRouter builds a Router with stub transforms/sinks registered.
func newTestRouter(t *testing.T, routes []config.RouteConfig, transforms map[string]*stubTransform, sinks map[string]*stubSink) (*Router, func()) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := plugin.NewRegistry(log, st.DB())
	for _, tr := range transforms {
		reg.Register(tr)
	}
	for _, sk := range sinks {
		reg.Register(sk)
	}
	if err := reg.InitAll(nil); err != nil {
		t.Fatal(err)
	}
	r := NewRouter(routes, reg, st, log)
	return r, func() { _ = st.Close() }
}

// waitRoute sets up a notify channel and returns a wait function.
func waitRoute(r *Router) func() {
	done := make(chan struct{}, 1)
	r.SetNotifyFn(func() { done <- struct{}{} })
	return func() {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			panic("timeout waiting for route completion")
		}
	}
}

func makeEvent(source, typ string) plugin.Event {
	return plugin.Event{
		ID:        "evt-001",
		Source:    source,
		Type:      typ,
		Payload:   map[string]any{"key": "value"},
		Timestamp: time.Now(),
	}
}

// --- tests ---

func TestRouter_MatchBySource(t *testing.T) {
	sink := &stubSink{name: "out"}
	routes := []config.RouteConfig{{
		Name:   "match-source",
		Source: "webhook",
		Sink:   config.SinkConfig{Plugin: "out"},
	}}
	r, cleanup := newTestRouter(t, routes, nil, map[string]*stubSink{"out": sink})
	defer cleanup()

	wait := waitRoute(r)
	r.HandleEvent(makeEvent("webhook", "push"))
	wait()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) != 1 {
		t.Fatalf("expected 1 event delivered, got %d", len(sink.events))
	}
	if sink.events[0].Source != "webhook" {
		t.Errorf("expected source %q, got %q", "webhook", sink.events[0].Source)
	}
}

func TestRouter_NoMatch(t *testing.T) {
	sink := &stubSink{name: "out"}
	routes := []config.RouteConfig{{
		Name:   "match-source",
		Source: "webhook",
		Sink:   config.SinkConfig{Plugin: "out"},
	}}
	r, cleanup := newTestRouter(t, routes, nil, map[string]*stubSink{"out": sink})
	defer cleanup()

	// Event from a different source — should not match.
	r.HandleEvent(makeEvent("other-source", "push"))

	// Give goroutines a moment to run (nothing should fire).
	time.Sleep(50 * time.Millisecond)

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(sink.events))
	}
}

func TestRouter_MatchByEvent(t *testing.T) {
	sink := &stubSink{name: "out"}
	routes := []config.RouteConfig{{
		Name:   "event-filter",
		Source: "webhook",
		Event:  "push",
		Sink:   config.SinkConfig{Plugin: "out"},
	}}
	r, cleanup := newTestRouter(t, routes, nil, map[string]*stubSink{"out": sink})
	defer cleanup()

	// Non-matching event type.
	r.HandleEvent(makeEvent("webhook", "pull_request"))
	time.Sleep(50 * time.Millisecond)

	sink.mu.Lock()
	if len(sink.events) != 0 {
		sink.mu.Unlock()
		t.Fatalf("expected 0 events for non-matching type, got %d", len(sink.events))
	}
	sink.mu.Unlock()

	// Matching event type.
	wait := waitRoute(r)
	r.HandleEvent(makeEvent("webhook", "push"))
	wait()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sink.events))
	}
}

func TestRouter_TransformChain(t *testing.T) {
	t1 := &stubTransform{name: "t1"}
	t2 := &stubTransform{name: "t2"}
	sink := &stubSink{name: "out"}
	routes := []config.RouteConfig{{
		Name:   "chain",
		Source: "src",
		Pipeline: []config.StepConfig{
			{Plugin: "t1", Action: "enrich"},
			{Plugin: "t2", Action: "format"},
		},
		Sink: config.SinkConfig{Plugin: "out"},
	}}
	r, cleanup := newTestRouter(t, routes,
		map[string]*stubTransform{"t1": t1, "t2": t2},
		map[string]*stubSink{"out": sink},
	)
	defer cleanup()

	wait := waitRoute(r)
	r.HandleEvent(makeEvent("src", "any"))
	wait()

	t1.mu.Lock()
	t1Called := t1.called
	t1.mu.Unlock()
	t2.mu.Lock()
	t2Called := t2.called
	t2.mu.Unlock()

	if t1Called != 1 {
		t.Errorf("expected t1 called once, got %d", t1Called)
	}
	if t2Called != 1 {
		t.Errorf("expected t2 called once, got %d", t2Called)
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sink.events))
	}
	payload := sink.events[0].Payload
	if payload["transformed_by_t1"] != true {
		t.Error("payload missing t1 transform marker")
	}
	if payload["transformed_by_t2"] != true {
		t.Error("payload missing t2 transform marker")
	}
}

func TestRouter_TransformError(t *testing.T) {
	tr := &stubTransform{name: "bad", err: errors.New("transform broke")}
	sink := &stubSink{name: "out"}
	routes := []config.RouteConfig{{
		Name:     "err-route",
		Source:   "src",
		Pipeline: []config.StepConfig{{Plugin: "bad", Action: "do"}},
		Sink:     config.SinkConfig{Plugin: "out"},
	}}
	r, cleanup := newTestRouter(t, routes,
		map[string]*stubTransform{"bad": tr},
		map[string]*stubSink{"out": sink},
	)
	defer cleanup()

	wait := waitRoute(r)
	r.HandleEvent(makeEvent("src", "any"))
	wait()

	// deliverError sends the error to the sink.
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) != 1 {
		t.Fatalf("expected 1 error event delivered via deliverError, got %d", len(sink.events))
	}
	summary, _ := sink.events[0].Payload["summary"].(string)
	if summary == "" {
		t.Error("expected error summary in payload")
	}
}

func TestRouter_SinkNotFound(t *testing.T) {
	routes := []config.RouteConfig{{
		Name:   "no-sink",
		Source: "src",
		Sink:   config.SinkConfig{Plugin: "missing"},
	}}
	// No sinks registered — the route should fail gracefully, no panic.
	r, cleanup := newTestRouter(t, routes, nil, nil)
	defer cleanup()

	wait := waitRoute(r)
	r.HandleEvent(makeEvent("src", "any"))
	wait()
	// If we get here without panicking, the test passes.
}

func TestRouter_TransformNotFound(t *testing.T) {
	sink := &stubSink{name: "out"}
	routes := []config.RouteConfig{{
		Name:     "missing-transform",
		Source:   "src",
		Pipeline: []config.StepConfig{{Plugin: "ghost", Action: "do"}},
		Sink:     config.SinkConfig{Plugin: "out"},
	}}
	r, cleanup := newTestRouter(t, routes, nil, map[string]*stubSink{"out": sink})
	defer cleanup()

	wait := waitRoute(r)
	r.HandleEvent(makeEvent("src", "any"))
	wait()

	// The sink should receive the error notification via deliverError.
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) != 1 {
		t.Fatalf("expected 1 error event via deliverError, got %d", len(sink.events))
	}
}

func TestRouter_NotifyFnCalled(t *testing.T) {
	sink := &stubSink{name: "out"}
	routes := []config.RouteConfig{{
		Name:   "notify",
		Source: "src",
		Sink:   config.SinkConfig{Plugin: "out"},
	}}
	r, cleanup := newTestRouter(t, routes, nil, map[string]*stubSink{"out": sink})
	defer cleanup()

	called := make(chan struct{}, 1)
	r.SetNotifyFn(func() { called <- struct{}{} })

	r.HandleEvent(makeEvent("src", "any"))

	select {
	case <-called:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("notifyFn was not called")
	}
}

func TestRouter_SinkParams(t *testing.T) {
	sink := &stubSink{name: "out"}
	routes := []config.RouteConfig{{
		Name:   "params",
		Source: "src",
		Sink: config.SinkConfig{
			Plugin: "out",
			Params: map[string]any{"channel": "general", "mention": true},
		},
	}}
	r, cleanup := newTestRouter(t, routes, nil, map[string]*stubSink{"out": sink})
	defer cleanup()

	wait := waitRoute(r)
	r.HandleEvent(makeEvent("src", "any"))
	wait()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sink.events))
	}
	payload := sink.events[0].Payload
	if payload["channel"] != "general" {
		t.Errorf("expected channel=general, got %v", payload["channel"])
	}
	if payload["mention"] != true {
		t.Errorf("expected mention=true, got %v", payload["mention"])
	}
}
