package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// --- stub types ---

type stubPlugin struct {
	name    string
	initErr error
	started bool
	stopped bool
	initSeq *[]string // shared slice to track init order
	stopSeq *[]string // shared slice to track stop order
}

func (s *stubPlugin) Name() string { return s.name }
func (s *stubPlugin) Init(json.RawMessage) error {
	if s.initSeq != nil {
		*s.initSeq = append(*s.initSeq, s.name)
	}
	return s.initErr
}
func (s *stubPlugin) Start(context.Context, EventBus) error { s.started = true; return nil }
func (s *stubPlugin) Stop() error {
	s.stopped = true
	if s.stopSeq != nil {
		*s.stopSeq = append(*s.stopSeq, s.name)
	}
	return nil
}

type stubSinkPlugin struct {
	stubPlugin
	handled []Event
}

func (s *stubSinkPlugin) HandleEvent(_ context.Context, e Event) error {
	s.handled = append(s.handled, e)
	return nil
}

type stubTransformPlugin struct {
	stubPlugin
}

func (s *stubTransformPlugin) Transform(_ context.Context, e Event, _ string, _ map[string]any) (Event, error) {
	return e, nil
}

type stubHealthPlugin struct {
	stubPlugin
	status HealthStatus
}

func (s *stubHealthPlugin) HealthCheck(context.Context) HealthStatus { return s.status }

type stubStoreAwarePlugin struct {
	stubPlugin
	db *sql.DB
}

func (s *stubStoreAwarePlugin) SetStore(db *sql.DB) { s.db = db }

// --- helpers ---

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewRegistry(slog.New(slog.NewTextHandler(io.Discard, nil)), db)
}

// --- tests ---

func TestRegistry_Register_Get(t *testing.T) {
	r := newTestRegistry(t)
	p := &stubPlugin{name: "alpha"}
	r.Register(p)

	got, ok := r.Get("alpha")
	if !ok {
		t.Fatal("expected plugin to be found")
	}
	if got.Name() != "alpha" {
		t.Errorf("got name %q, want %q", got.Name(), "alpha")
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	r := newTestRegistry(t)
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected ok=false for unknown plugin")
	}
}

func TestRegistry_GetSink_Found(t *testing.T) {
	r := newTestRegistry(t)
	p := &stubSinkPlugin{stubPlugin: stubPlugin{name: "mysink"}}
	r.Register(p)

	s, ok := r.GetSink("mysink")
	if !ok {
		t.Fatal("expected sink to be found")
	}
	if s == nil {
		t.Fatal("expected non-nil sink")
	}
}

func TestRegistry_GetSink_NotSink(t *testing.T) {
	r := newTestRegistry(t)
	p := &stubPlugin{name: "plain"}
	r.Register(p)

	_, ok := r.GetSink("plain")
	if ok {
		t.Error("expected ok=false for non-sink plugin")
	}
}

func TestRegistry_GetTransform_Found(t *testing.T) {
	r := newTestRegistry(t)
	p := &stubTransformPlugin{stubPlugin: stubPlugin{name: "mytx"}}
	r.Register(p)

	tx, ok := r.GetTransform("mytx")
	if !ok {
		t.Fatal("expected transform to be found")
	}
	if tx == nil {
		t.Fatal("expected non-nil transform")
	}
}

func TestRegistry_GetTransform_NotTransform(t *testing.T) {
	r := newTestRegistry(t)
	p := &stubPlugin{name: "plain"}
	r.Register(p)

	_, ok := r.GetTransform("plain")
	if ok {
		t.Error("expected ok=false for non-transform plugin")
	}
}

func TestRegistry_InitAll_Order(t *testing.T) {
	r := newTestRegistry(t)
	var seq []string
	a := &stubPlugin{name: "alpha", initSeq: &seq}
	b := &stubPlugin{name: "bravo", initSeq: &seq}
	r.Register(a)
	r.Register(b)

	if err := r.InitAll(nil); err != nil {
		t.Fatal(err)
	}
	if len(seq) != 2 || seq[0] != "alpha" || seq[1] != "bravo" {
		t.Errorf("init order = %v, want [alpha bravo]", seq)
	}
}

func TestRegistry_InitAll_Error(t *testing.T) {
	r := newTestRegistry(t)
	p := &stubPlugin{name: "bad", initErr: errors.New("kaboom")}
	r.Register(p)

	err := r.InitAll(nil)
	if err == nil {
		t.Fatal("expected error from InitAll")
	}
	if !errors.Is(err, p.initErr) {
		t.Errorf("got %v, want wrapped kaboom", err)
	}
}

func TestRegistry_InitAll_StoreAware(t *testing.T) {
	r := newTestRegistry(t)
	p := &stubStoreAwarePlugin{stubPlugin: stubPlugin{name: "dbaware"}}
	r.Register(p)

	if err := r.InitAll(nil); err != nil {
		t.Fatal(err)
	}
	if p.db == nil {
		t.Error("expected StoreAware plugin to receive non-nil DB")
	}
}

func TestRegistry_StartAll(t *testing.T) {
	r := newTestRegistry(t)
	a := &stubPlugin{name: "alpha"}
	b := &stubPlugin{name: "bravo"}
	r.Register(a)
	r.Register(b)

	if err := r.StartAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !a.started {
		t.Error("expected alpha to be started")
	}
	if !b.started {
		t.Error("expected bravo to be started")
	}
}

func TestRegistry_StopAll_ReverseOrder(t *testing.T) {
	r := newTestRegistry(t)
	var seq []string
	a := &stubPlugin{name: "alpha", stopSeq: &seq}
	b := &stubPlugin{name: "bravo", stopSeq: &seq}
	r.Register(a)
	r.Register(b)

	r.StopAll()

	if len(seq) != 2 || seq[0] != "bravo" || seq[1] != "alpha" {
		t.Errorf("stop order = %v, want [bravo alpha]", seq)
	}
	if !a.stopped || !b.stopped {
		t.Error("expected both plugins to be stopped")
	}
}

func TestRegistry_All_Types(t *testing.T) {
	r := newTestRegistry(t)
	r.Register(&stubPlugin{name: "plain"})
	r.Register(&stubSinkPlugin{stubPlugin: stubPlugin{name: "mysink"}})
	r.Register(&stubTransformPlugin{stubPlugin: stubPlugin{name: "mytx"}})

	infos := r.All()
	if len(infos) != 3 {
		t.Fatalf("got %d infos, want 3", len(infos))
	}

	// plain plugin defaults to "source"
	if len(infos[0].Types) != 1 || infos[0].Types[0] != "source" {
		t.Errorf("plain types = %v, want [source]", infos[0].Types)
	}
	// sink plugin
	found := false
	for _, ty := range infos[1].Types {
		if ty == "sink" {
			found = true
		}
	}
	if !found {
		t.Errorf("sink types = %v, want to contain 'sink'", infos[1].Types)
	}
	// transform plugin
	found = false
	for _, ty := range infos[2].Types {
		if ty == "transform" {
			found = true
		}
	}
	if !found {
		t.Errorf("transform types = %v, want to contain 'transform'", infos[2].Types)
	}
}

func TestRegistry_CheckHealth_Default(t *testing.T) {
	r := newTestRegistry(t)
	r.Register(&stubPlugin{name: "plain"})

	results := r.CheckHealth(context.Background(), time.Second)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Status.Status != StatusOK {
		t.Errorf("got status %q, want %q", results[0].Status.Status, StatusOK)
	}
}

func TestRegistry_CheckHealth_Custom(t *testing.T) {
	r := newTestRegistry(t)
	p := &stubHealthPlugin{
		stubPlugin: stubPlugin{name: "hc"},
		status:     HealthStatus{Status: StatusDegraded, Message: "warming up"},
	}
	r.Register(p)

	results := r.CheckHealth(context.Background(), time.Second)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Status.Status != StatusDegraded {
		t.Errorf("got status %q, want %q", results[0].Status.Status, StatusDegraded)
	}
	if results[0].Status.Message != "warming up" {
		t.Errorf("got message %q, want %q", results[0].Status.Message, "warming up")
	}
}

func TestRegistry_AggregateHealth_AllOK(t *testing.T) {
	r := newTestRegistry(t)
	r.Register(&stubPlugin{name: "a"})
	r.Register(&stubPlugin{name: "b"})

	agg, results := r.AggregateHealth(context.Background(), time.Second)
	if agg.Status != StatusOK {
		t.Errorf("aggregate status = %q, want %q", agg.Status, StatusOK)
	}
	if len(results) != 2 {
		t.Errorf("got %d results, want 2", len(results))
	}
}

func TestRegistry_AggregateHealth_Degraded(t *testing.T) {
	r := newTestRegistry(t)
	r.Register(&stubPlugin{name: "ok-one"})
	r.Register(&stubHealthPlugin{
		stubPlugin: stubPlugin{name: "sick"},
		status:     HealthStatus{Status: StatusDegraded, Message: "slow"},
	})

	agg, _ := r.AggregateHealth(context.Background(), time.Second)
	if agg.Status != StatusDegraded {
		t.Errorf("aggregate status = %q, want %q", agg.Status, StatusDegraded)
	}
}
