package tailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/dmarx/smoothbrain/internal/plugin"
)

type Config struct {
	ServiceName string `json:"service_name"`
	Target      string `json:"target"`
}

type Plugin struct {
	cfg Config
	log *slog.Logger
}

func New(log *slog.Logger) *Plugin {
	return &Plugin{log: log}
}

func (p *Plugin) Name() string { return "tailscale" }

func (p *Plugin) Init(cfg json.RawMessage) error {
	p.cfg = Config{ServiceName: "smoothbrain"}
	if err := json.Unmarshal(cfg, &p.cfg); err != nil {
		return fmt.Errorf("tailscale config: %w", err)
	}
	if p.cfg.Target == "" {
		return fmt.Errorf("tailscale config: target is required")
	}
	return nil
}

func (p *Plugin) Start(_ context.Context, _ plugin.EventBus) error {
	svcName := "svc:" + p.cfg.ServiceName

	// Set up the HTTPS proxy for the service.
	cmd := exec.Command("tailscale", "serve", "--service", svcName, p.cfg.Target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tailscale: serve: %w (%s)", err, out)
	}
	p.log.Info("tailscale: serve started", "service", svcName, "target", p.cfg.Target)
	return nil
}

func (p *Plugin) Stop() error {
	svcName := "svc:" + p.cfg.ServiceName

	// Remove the serve config entirely.
	cmd := exec.Command("tailscale", "serve", "clear", svcName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		p.log.Error("tailscale: clear failed", "error", err, "output", string(out))
		return fmt.Errorf("tailscale: clear: %w", err)
	}
	p.log.Info("tailscale: serve removed", "service", svcName)
	return nil
}

func (p *Plugin) HealthCheck(ctx context.Context) plugin.HealthStatus {
	cmd := exec.CommandContext(ctx, "tailscale", "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		return plugin.HealthStatus{Status: plugin.StatusError, Message: "tailscale status failed: " + err.Error()}
	}
	var status struct {
		BackendState string `json:"BackendState"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return plugin.HealthStatus{Status: plugin.StatusError, Message: "parse status: " + err.Error()}
	}
	if status.BackendState != "Running" {
		return plugin.HealthStatus{Status: plugin.StatusDegraded, Message: "backend state: " + status.BackendState}
	}
	return plugin.HealthStatus{Status: plugin.StatusOK}
}
