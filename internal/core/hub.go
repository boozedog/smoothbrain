package core

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/dmarx/smoothbrain/internal/plugin"
	"github.com/dmarx/smoothbrain/internal/store"
)

type client struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
}

type Hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
	notify  chan struct{}
	store   *store.Store
	log     *slog.Logger
}

func NewHub(s *store.Store, log *slog.Logger) *Hub {
	return &Hub{
		clients: make(map[*client]struct{}),
		notify:  make(chan struct{}, 1),
		store:   s,
		log:     log,
	}
}

// HandleEvent is a bus subscriber. Non-blocking send coalesces bursts.
func (h *Hub) HandleEvent(e plugin.Event) {
	select {
	case h.notify <- struct{}{}:
	default:
	}
}

// Notify triggers a broadcast to all connected WebSocket clients.
func (h *Hub) Notify() {
	select {
	case h.notify <- struct{}{}:
	default:
	}
}

// Run processes notifications and broadcasts to all clients.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.notify:
			h.broadcast()
		}
	}
}

func (h *Hub) broadcast() {
	events := queryEvents(h.store, h.log)

	var buf bytes.Buffer
	buf.WriteString(`<div id="events-table">`)
	renderEventsHTML(&buf, events, h.store, h.log)
	buf.WriteString(`</div>`)
	msg := buf.Bytes()

	h.mu.Lock()
	defer h.mu.Unlock()

	for c := range h.clients {
		writeCtx, writeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := c.conn.Write(writeCtx, websocket.MessageText, msg)
		writeCancel()
		if err != nil {
			h.log.Debug("removing dead ws client", "error", err)
			c.cancel()
			delete(h.clients, c)
		}
	}
}

// ServeHTTP upgrades the connection to WebSocket.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		h.log.Error("ws accept failed", "error", err)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	c := &client{conn: conn, cancel: cancel}

	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()

	h.log.Info("ws client connected", "clients", len(h.clients))

	// Send initial state.
	events := queryEvents(h.store, h.log)
	var buf bytes.Buffer
	fmt.Fprint(&buf, `<div id="events-table">`)
	renderEventsHTML(&buf, events, h.store, h.log)
	fmt.Fprint(&buf, `</div>`)
	conn.Write(ctx, websocket.MessageText, buf.Bytes())

	// Read loop keeps connection alive; exits on disconnect.
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			break
		}
	}

	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	cancel()
	h.log.Info("ws client disconnected")
}
