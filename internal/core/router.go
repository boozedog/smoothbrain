package core

import (
	"context"
	"encoding/json"
	"fmt"
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
	notifyFn func()
}

func NewRouter(routes []config.RouteConfig, registry *plugin.Registry, s *store.Store, log *slog.Logger) *Router {
	return &Router{
		routes:   routes,
		registry: registry,
		store:    s,
		log:      log,
	}
}

// SetNotifyFn sets the callback invoked after each pipeline run completes.
func (r *Router) SetNotifyFn(fn func()) {
	r.notifyFn = fn
}

type stepResult struct {
	Plugin     string `json:"plugin"`
	Action     string `json:"action"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
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
	timeout := 30 * time.Second
	if route.Timeout != "" {
		if d, err := time.ParseDuration(route.Timeout); err == nil {
			timeout = d
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	r.log.Info("route matched", "route", route.Name, "event_id", event.ID)

	startedAt := time.Now().UTC()

	// Insert a running pipeline_runs row.
	res, err := r.store.DB().Exec(
		`INSERT INTO pipeline_runs (event_id, route, status, started_at) VALUES (?, ?, 'running', ?)`,
		event.ID, route.Name, startedAt,
	)
	if err != nil {
		r.log.Error("failed to insert pipeline run", "error", err)
		return
	}
	runID, _ := res.LastInsertId()

	// Deep-copy payload to avoid data races when multiple routes match the same event.
	current := event
	current.Payload = make(map[string]any, len(event.Payload))
	maps.Copy(current.Payload, event.Payload)

	var steps []stepResult

	for _, step := range route.Pipeline {
		stepStart := time.Now()
		t, ok := r.registry.GetTransform(step.Plugin)
		if !ok {
			errMsg := "transform plugin not found"
			r.log.Error(errMsg, "plugin", step.Plugin, "route", route.Name)
			steps = append(steps, stepResult{
				Plugin:     step.Plugin,
				Action:     step.Action,
				Status:     "failed",
				DurationMs: time.Since(stepStart).Milliseconds(),
				Error:      errMsg,
			})
			r.deliverError(ctx, route, current, errMsg)
			r.finishRun(runID, startedAt, "failed", errMsg, steps)
			return
		}

		current, err = t.Transform(ctx, current, step.Action, step.Params)
		elapsed := time.Since(stepStart).Milliseconds()

		if err != nil {
			r.log.Error("transform failed", "plugin", step.Plugin, "route", route.Name, "error", err)
			steps = append(steps, stepResult{
				Plugin:     step.Plugin,
				Action:     step.Action,
				Status:     "failed",
				DurationMs: elapsed,
				Error:      err.Error(),
			})
			r.deliverError(ctx, route, current, err.Error())
			r.finishRun(runID, startedAt, "failed", err.Error(), steps)
			return
		}

		steps = append(steps, stepResult{
			Plugin:     step.Plugin,
			Action:     step.Action,
			Status:     "completed",
			DurationMs: elapsed,
		})
	}

	// Sink delivery step.
	sinkStart := time.Now()
	sink, ok := r.registry.GetSink(route.Sink.Plugin)
	if !ok {
		errMsg := "sink plugin not found"
		r.log.Error(errMsg, "plugin", route.Sink.Plugin, "route", route.Name)
		steps = append(steps, stepResult{
			Plugin:     route.Sink.Plugin,
			Action:     "sink",
			Status:     "failed",
			DurationMs: time.Since(sinkStart).Milliseconds(),
			Error:      errMsg,
		})
		r.finishRun(runID, startedAt, "failed", errMsg, steps)
		return
	}

	maps.Copy(current.Payload, route.Sink.Params)

	if err := sink.HandleEvent(ctx, current); err != nil {
		r.log.Error("sink delivery failed", "plugin", route.Sink.Plugin, "route", route.Name, "error", err)
		steps = append(steps, stepResult{
			Plugin:     route.Sink.Plugin,
			Action:     "sink",
			Status:     "failed",
			DurationMs: time.Since(sinkStart).Milliseconds(),
			Error:      err.Error(),
		})
		r.finishRun(runID, startedAt, "failed", err.Error(), steps)
		return
	}

	steps = append(steps, stepResult{
		Plugin:     route.Sink.Plugin,
		Action:     "sink",
		Status:     "completed",
		DurationMs: time.Since(sinkStart).Milliseconds(),
	})

	// Update the event row with the route name (bus already inserted it).
	if _, err := r.store.DB().Exec(`UPDATE events SET route = ? WHERE id = ?`, route.Name, event.ID); err != nil {
		r.log.Error("failed to update event route", "error", err)
	}

	r.finishRun(runID, startedAt, "completed", "", steps)
	r.log.Info("route completed", "route", route.Name, "event_id", event.ID)
}

// deliverError attempts to send an error message through the route's sink.
func (r *Router) deliverError(ctx context.Context, route config.RouteConfig, event plugin.Event, errMsg string) {
	sink, ok := r.registry.GetSink(route.Sink.Plugin)
	if !ok {
		return
	}
	event.Payload["summary"] = fmt.Sprintf("**Error:** %s", errMsg)
	maps.Copy(event.Payload, route.Sink.Params)
	if err := sink.HandleEvent(ctx, event); err != nil {
		r.log.Error("failed to deliver error to sink", "plugin", route.Sink.Plugin, "error", err)
	}
}

func (r *Router) finishRun(runID int64, startedAt time.Time, status, errMsg string, steps []stepResult) {
	finishedAt := time.Now().UTC()
	durationMs := time.Since(startedAt).Milliseconds()

	stepsJSON, _ := json.Marshal(steps)

	_, err := r.store.DB().Exec(
		`UPDATE pipeline_runs SET status = ?, finished_at = ?, duration_ms = ?, error = ?, steps = ? WHERE id = ?`,
		status, finishedAt, durationMs, errMsg, string(stepsJSON), runID,
	)
	if err != nil {
		r.log.Error("failed to update pipeline run", "error", err)
	}

	if r.notifyFn != nil {
		r.notifyFn()
	}
}
