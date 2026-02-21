package core

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/boozedog/smoothbrain/internal/plugin"
	"github.com/boozedog/smoothbrain/internal/store"
)

func newTestHub(t *testing.T) *Hub {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHub(st, log)
}

func TestNewHub(t *testing.T) {
	hub := newTestHub(t)

	if hub.clients == nil {
		t.Error("clients map is nil")
	}
	if hub.notify == nil {
		t.Error("notify channel is nil")
	}
	if cap(hub.notify) != 1 {
		t.Errorf("notify channel cap = %d, want 1", cap(hub.notify))
	}
}

func TestHub_HandleEvent_NonBlocking(t *testing.T) {
	hub := newTestHub(t)

	// Call HandleEvent many times rapidly — must not block
	done := make(chan struct{})
	go func() {
		for i := range 100 {
			hub.HandleEvent(plugin.Event{ID: string(rune(i))})
		}
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("HandleEvent blocked")
	}
}

func TestHub_Notify_NonBlocking(t *testing.T) {
	hub := newTestHub(t)

	done := make(chan struct{})
	go func() {
		for range 100 {
			hub.Notify()
		}
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("Notify blocked")
	}
}

func TestHub_Run_ContextCancel(t *testing.T) {
	hub := newTestHub(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		hub.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// Run exited after cancel
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancel")
	}
}

func TestHub_HandleEvent_Coalesces(t *testing.T) {
	hub := newTestHub(t)

	// Fill the notify channel
	hub.notify <- struct{}{}

	// HandleEvent should not block even though channel is full (select default path)
	done := make(chan struct{})
	go func() {
		hub.HandleEvent(plugin.Event{ID: "coalesce-test"})
		close(done)
	}()

	select {
	case <-done:
		// success — the select{default} path worked
	case <-time.After(2 * time.Second):
		t.Fatal("HandleEvent blocked when notify channel was full")
	}

	// Drain and verify exactly one notification was coalesced
	select {
	case <-hub.notify:
	default:
		t.Error("expected one notification in channel")
	}
}
