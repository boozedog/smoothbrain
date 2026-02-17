package core

import (
	"context"
	"log/slog"
	"maps"
	"time"

	"github.com/dmarx/smoothbrain/internal/config"
	"github.com/dmarx/smoothbrain/internal/plugin"
	"github.com/dmarx/smoothbrain/internal/store"
)

type Router struct {
	routes   []config.RouteConfig
	registry *plugin.Registry
	store    *store.Store
	log      *slog.Logger
}

func NewRouter(routes []config.RouteConfig, registry *plugin.Registry, s *store.Store, log *slog.Logger) *Router {
	return &Router{
		routes:   routes,
		registry: registry,
		store:    s,
		log:      log,
	}
}

func (r *Router) HandleEvent(event plugin.Event) {
	for _, route := range r.routes {
		if route.Source != event.Source {
			continue
		}
		if route.Event != "" && route.Event != event.Type {
			continue
		}
		go r.executeRoute(route, event)
	}
}

func (r *Router) executeRoute(route config.RouteConfig, event plugin.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r.log.Info("route matched", "route", route.Name, "event_id", event.ID)

	// Deep-copy payload to avoid data races when multiple routes match the same event.
	current := event
	current.Payload = make(map[string]any, len(event.Payload))
	maps.Copy(current.Payload, event.Payload)

	for _, step := range route.Pipeline {
		t, ok := r.registry.GetTransform(step.Plugin)
		if !ok {
			r.log.Error("transform plugin not found", "plugin", step.Plugin, "route", route.Name)
			return
		}
		var err error
		current, err = t.Transform(ctx, current, step.Action, step.Params)
		if err != nil {
			r.log.Error("transform failed", "plugin", step.Plugin, "route", route.Name, "error", err)
			return
		}
	}

	sink, ok := r.registry.GetSink(route.Sink.Plugin)
	if !ok {
		r.log.Error("sink plugin not found", "plugin", route.Sink.Plugin, "route", route.Name)
		return
	}

	// Merge sink params into event payload so sinks know delivery details.
	maps.Copy(current.Payload, route.Sink.Params)

	if err := sink.HandleEvent(ctx, current); err != nil {
		r.log.Error("sink delivery failed", "plugin", route.Sink.Plugin, "route", route.Name, "error", err)
		return
	}

	// Update the event row with the route name (bus already inserted it).
	_, err := r.store.DB().Exec(`UPDATE events SET route = ? WHERE id = ?`, route.Name, event.ID)
	if err != nil {
		r.log.Error("failed to update event route", "error", err)
	}
	r.log.Info("route completed", "route", route.Name, "event_id", event.ID)
}
