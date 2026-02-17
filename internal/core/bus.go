package core

import (
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/dmarx/smoothbrain/internal/plugin"
	"github.com/dmarx/smoothbrain/internal/store"
)

type subscriber func(event plugin.Event)

type Bus struct {
	mu          sync.RWMutex
	subscribers []subscriber
	store       *store.Store
	log         *slog.Logger
}

func NewBus(s *store.Store, log *slog.Logger) *Bus {
	return &Bus{store: s, log: log}
}

func (b *Bus) Subscribe(fn subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribers = append(b.subscribers, fn)
}

func (b *Bus) Emit(event plugin.Event) {
	b.mu.RLock()
	subs := b.subscribers
	b.mu.RUnlock()

	b.log.Debug("event emitted", "source", event.Source, "type", event.Type, "id", event.ID)
	b.logEvent(event)

	for _, fn := range subs {
		fn(event)
	}
}

func (b *Bus) logEvent(event plugin.Event) {
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		b.log.Error("failed to marshal event payload", "error", err)
		return
	}
	_, err = b.store.DB().Exec(
		`INSERT OR IGNORE INTO events (id, source, type, payload, timestamp) VALUES (?, ?, ?, ?, ?)`,
		event.ID, event.Source, event.Type, string(payload), event.Timestamp,
	)
	if err != nil {
		b.log.Error("failed to log event", "error", err)
	}
}
