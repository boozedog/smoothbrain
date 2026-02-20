package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
)

type Registry struct {
	plugins map[string]Plugin
	order   []Plugin
	mu      sync.RWMutex
	db      *sql.DB
	log     *slog.Logger
}

func NewRegistry(log *slog.Logger, db *sql.DB) *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
		db:      db,
		log:     log,
	}
}

func (r *Registry) Register(p Plugin) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins[p.Name()] = p
	r.order = append(r.order, p)
	r.log.Info("plugin registered", "plugin", p.Name())
}

func (r *Registry) Get(name string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[name]
	return p, ok
}

func (r *Registry) GetSink(name string) (Sink, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[name]
	if !ok {
		return nil, false
	}
	s, ok := p.(Sink)
	return s, ok
}

func (r *Registry) GetTransform(name string) (Transform, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[name]
	if !ok {
		return nil, false
	}
	t, ok := p.(Transform)
	return t, ok
}

func (r *Registry) InitAll(configs map[string]json.RawMessage) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.order {
		name := p.Name()
		if sa, ok := p.(StoreAware); ok {
			sa.SetStore(r.db)
		}
		cfg, ok := configs[name]
		if !ok {
			cfg = json.RawMessage("{}")
		}
		if err := p.Init(cfg); err != nil {
			return fmt.Errorf("init plugin %s: %w", name, err)
		}
		r.log.Info("plugin initialized", "plugin", name)
	}
	return nil
}

func (r *Registry) StartAll(ctx context.Context, bus EventBus) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.order {
		name := p.Name()
		if err := p.Start(ctx, bus); err != nil {
			return fmt.Errorf("start plugin %s: %w", name, err)
		}
		r.log.Info("plugin started", "plugin", name)
	}
	return nil
}

// RegisterWebhooks discovers plugins that implement WebhookSource and registers their handlers.
func (r *Registry) RegisterWebhooks(reg WebhookRegistrar) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.order {
		if ws, ok := p.(WebhookSource); ok {
			ws.RegisterWebhook(reg)
		}
	}
}

// PluginInfo describes a registered plugin for the status UI.
type PluginInfo struct {
	Name  string
	Types []string // "source", "transform", "sink"
}

// All returns info about every registered plugin in registration order.
func (r *Registry) All() []PluginInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	infos := make([]PluginInfo, 0, len(r.order))
	for _, p := range r.order {
		info := PluginInfo{Name: p.Name()}
		if _, ok := p.(WebhookSource); ok {
			info.Types = append(info.Types, "source")
		}
		if _, ok := p.(Transform); ok {
			info.Types = append(info.Types, "transform")
		}
		if _, ok := p.(Sink); ok {
			info.Types = append(info.Types, "sink")
		}
		if len(info.Types) == 0 {
			info.Types = []string{"source"}
		}
		infos = append(infos, info)
	}
	return infos
}

func (r *Registry) StopAll() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// Stop in reverse registration order.
	for i := len(r.order) - 1; i >= 0; i-- {
		p := r.order[i]
		name := p.Name()
		if err := p.Stop(); err != nil {
			r.log.Error("plugin stop error", "plugin", name, "error", err)
		} else {
			r.log.Info("plugin stopped", "plugin", name)
		}
	}
}
