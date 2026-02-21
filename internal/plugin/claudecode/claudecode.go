package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/boozedog/smoothbrain/internal/plugin"
)

type Config struct {
	Binary string `json:"binary,omitempty"` // path to claude binary, default "claude"
	Model  string `json:"model,omitempty"`
}

type Plugin struct {
	cfg Config
	log *slog.Logger
}

func New(log *slog.Logger) *Plugin {
	return &Plugin{log: log}
}

func (p *Plugin) Name() string { return "claudecode" }

func (p *Plugin) Init(cfg json.RawMessage) error {
	if cfg != nil {
		if err := json.Unmarshal(cfg, &p.cfg); err != nil {
			return fmt.Errorf("claudecode config: %w", err)
		}
	}
	return nil
}

func (p *Plugin) Start(ctx context.Context, bus plugin.EventBus) error {
	return nil
}

func (p *Plugin) Stop() error { return nil }

func (p *Plugin) Transform(ctx context.Context, event plugin.Event, action string, params map[string]any) (plugin.Event, error) {
	switch action {
	case "ask":
		return p.ask(ctx, event, params)
	default:
		return event, fmt.Errorf("claudecode: unknown action %q", action)
	}
}

func (p *Plugin) ask(ctx context.Context, event plugin.Event, params map[string]any) (plugin.Event, error) {
	message, _ := event.Payload["message"].(string)
	if message == "" {
		return event, fmt.Errorf("claudecode: no message in payload")
	}

	args := []string{"--print", "--permission-mode", "plan"}
	if p.cfg.Model != "" {
		args = append(args, "--model", p.cfg.Model)
	}
	if sysPrompt, ok := params["system_prompt"].(string); ok {
		args = append(args, "--system-prompt", sysPrompt)
	}
	args = append(args, message)

	binary := p.cfg.Binary
	if binary == "" {
		binary = "claude"
	}

	p.log.Info("claudecode: running", "message", message)
	cmd := exec.CommandContext(ctx, binary, args...) //nolint:gosec // binary path is from trusted config
	output, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		return event, fmt.Errorf("claudecode: %w: %s", err, stderr)
	}

	event.Payload["summary"] = strings.TrimSpace(string(output))
	p.log.Info("claudecode: complete", "event_id", event.ID)
	return event, nil
}
