package tailscale

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/boozedog/smoothbrain/internal/plugin"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestTailscale_Name(t *testing.T) {
	p := New(discardLogger())
	if got := p.Name(); got != "tailscale" {
		t.Errorf("Name() = %q, want %q", got, "tailscale")
	}
}

func TestTailscale_HealthCheck_NilServer(t *testing.T) {
	p := New(discardLogger())
	status := p.HealthCheck(context.Background())
	if status.Status != plugin.StatusOK {
		t.Errorf("Status = %q, want %q", status.Status, plugin.StatusOK)
	}
	if status.Message != "tsnet not enabled" {
		t.Errorf("Message = %q, want %q", status.Message, "tsnet not enabled")
	}
}

func TestTailscale_NoOps(t *testing.T) {
	p := New(discardLogger())
	if err := p.Init(nil); err != nil {
		t.Errorf("Init() error: %v", err)
	}
	if err := p.Start(context.Background(), nil); err != nil {
		t.Errorf("Start() error: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Errorf("Stop() error: %v", err)
	}
}
