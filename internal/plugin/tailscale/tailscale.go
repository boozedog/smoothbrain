package tailscale

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/dmarx/smoothbrain/internal/plugin"
	"tailscale.com/tsnet"
)

type Plugin struct {
	log    *slog.Logger
	server *tsnet.Server
}

func New(log *slog.Logger) *Plugin {
	return &Plugin{log: log}
}

func (p *Plugin) Name() string                                     { return "tailscale" }
func (p *Plugin) Init(_ json.RawMessage) error                     { return nil }
func (p *Plugin) Start(_ context.Context, _ plugin.EventBus) error { return nil }
func (p *Plugin) Stop() error                                      { return nil }

func (p *Plugin) SetServer(s *tsnet.Server) { p.server = s }

func (p *Plugin) HealthCheck(ctx context.Context) plugin.HealthStatus {
	if p.server == nil {
		return plugin.HealthStatus{Status: plugin.StatusOK, Message: "tsnet not enabled"}
	}
	lc, err := p.server.LocalClient()
	if err != nil {
		return plugin.HealthStatus{Status: plugin.StatusError, Message: err.Error()}
	}
	st, err := lc.Status(ctx)
	if err != nil {
		return plugin.HealthStatus{Status: plugin.StatusError, Message: err.Error()}
	}
	if st.BackendState != "Running" {
		return plugin.HealthStatus{Status: plugin.StatusDegraded, Message: "backend: " + st.BackendState}
	}
	return plugin.HealthStatus{Status: plugin.StatusOK}
}
