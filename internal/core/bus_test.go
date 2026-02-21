package core

import (
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/boozedog/smoothbrain/internal/plugin"
	"github.com/boozedog/smoothbrain/internal/store"
)

func newTestBus(t *testing.T) *Bus {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewBus(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func testEvent(id string) plugin.Event {
	return plugin.Event{
		ID:        id,
		Source:    "test",
		Type:      "test.event",
		Payload:   map[string]any{"key": "value"},
		Timestamp: time.Now(),
	}
}

func TestBus_EmitNoSubscribers(t *testing.T) {
	bus := newTestBus(t)
	// Should not panic
	bus.Emit(testEvent("evt-1"))
}

func TestBus_SubscribeReceives(t *testing.T) {
	bus := newTestBus(t)
	var got plugin.Event
	bus.Subscribe(func(event plugin.Event) {
		got = event
	})
	bus.Emit(testEvent("evt-2"))
	if got.ID != "evt-2" {
		t.Errorf("subscriber got ID = %q, want %q", got.ID, "evt-2")
	}
}

func TestBus_MultipleSubscribers(t *testing.T) {
	bus := newTestBus(t)
	var count atomic.Int32
	bus.Subscribe(func(event plugin.Event) { count.Add(1) })
	bus.Subscribe(func(event plugin.Event) { count.Add(1) })
	bus.Emit(testEvent("evt-3"))
	if got := count.Load(); got != 2 {
		t.Errorf("subscriber count = %d, want 2", got)
	}
}

func TestBus_PanicRecovery(t *testing.T) {
	bus := newTestBus(t)
	var called atomic.Bool
	bus.Subscribe(func(event plugin.Event) {
		panic("boom")
	})
	bus.Subscribe(func(event plugin.Event) {
		called.Store(true)
	})
	// Should not panic
	bus.Emit(testEvent("evt-4"))
	if !called.Load() {
		t.Error("second subscriber was not called after first panicked")
	}
}

func TestBus_DBLogging(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bus := NewBus(st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	bus.Emit(testEvent("evt-5"))

	var count int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM events WHERE id = ?", "evt-5").Scan(&count); err != nil {
		t.Fatalf("query events: %v", err)
	}
	if count != 1 {
		t.Errorf("event row count = %d, want 1", count)
	}
}

func TestBus_ConcurrentEmit(t *testing.T) {
	bus := newTestBus(t)
	var received atomic.Int32
	bus.Subscribe(func(event plugin.Event) {
		received.Add(1)
	})

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			bus.Emit(testEvent(time.Now().Format(time.RFC3339Nano)))
			_ = n
		}(i)
	}
	wg.Wait()

	if got := received.Load(); got != 50 {
		t.Errorf("received = %d, want 50", got)
	}
}

func TestBus_SubscribeDuringEmit(t *testing.T) {
	bus := newTestBus(t)
	var wg sync.WaitGroup

	// Emit from one goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 50 {
			bus.Emit(testEvent(time.Now().Format(time.RFC3339Nano)))
			_ = i
		}
	}()

	// Subscribe from another goroutine concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 50 {
			bus.Subscribe(func(event plugin.Event) {})
		}
	}()

	wg.Wait()
	// If we get here without a race detector complaint, the test passes
}
