package claudecode

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/boozedog/smoothbrain/internal/plugin"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestClaudeCode_Name(t *testing.T) {
	p := New(discardLogger())
	if got := p.Name(); got != "claudecode" {
		t.Errorf("Name() = %q, want %q", got, "claudecode")
	}
}

func TestClaudeCode_Init_Default(t *testing.T) {
	p := New(discardLogger())
	if err := p.Init(nil); err != nil {
		t.Fatalf("Init(nil) error: %v", err)
	}
	if p.cfg.Binary != "" {
		t.Errorf("binary = %q, want empty", p.cfg.Binary)
	}
	if p.cfg.Model != "" {
		t.Errorf("model = %q, want empty", p.cfg.Model)
	}
}

func TestClaudeCode_Init_Config(t *testing.T) {
	p := New(discardLogger())
	cfg := json.RawMessage(`{"binary":"/usr/bin/claude","model":"opus"}`)
	if err := p.Init(cfg); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	if p.cfg.Binary != "/usr/bin/claude" {
		t.Errorf("binary = %q, want %q", p.cfg.Binary, "/usr/bin/claude")
	}
	if p.cfg.Model != "opus" {
		t.Errorf("model = %q, want %q", p.cfg.Model, "opus")
	}
}

func TestClaudeCode_Transform_UnknownAction(t *testing.T) {
	p := New(discardLogger())
	_ = p.Init(nil)
	ev := plugin.Event{Payload: map[string]any{}}
	_, err := p.Transform(context.Background(), ev, "foo", nil)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("error = %q, want it to contain %q", err, "unknown action")
	}
}

func TestClaudeCode_Ask_NoMessage(t *testing.T) {
	p := New(discardLogger())
	_ = p.Init(nil)
	ev := plugin.Event{Payload: map[string]any{}}
	_, err := p.Transform(context.Background(), ev, "ask", nil)
	if err == nil {
		t.Fatal("expected error for missing message")
	}
	if !strings.Contains(err.Error(), "no message") {
		t.Errorf("error = %q, want it to contain %q", err, "no message")
	}
}

func TestAsk_Success(t *testing.T) {
	p := New(discardLogger())
	p.cfg.Binary = "echo"
	_ = p.Init(nil)

	ev := plugin.Event{
		ID:      "test-1",
		Payload: map[string]any{"message": "hello world"},
	}
	result, err := p.Transform(context.Background(), ev, "ask", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	summary, _ := result.Payload["summary"].(string)
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if !strings.Contains(summary, "hello world") {
		t.Errorf("summary = %q, should contain 'hello world'", summary)
	}
}

func TestAsk_ModelFlag(t *testing.T) {
	p := New(discardLogger())
	p.cfg.Binary = "echo"
	p.cfg.Model = "opus"
	_ = p.Init(nil)

	ev := plugin.Event{
		ID:      "test-2",
		Payload: map[string]any{"message": "test prompt"},
	}
	result, err := p.Transform(context.Background(), ev, "ask", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	summary, _ := result.Payload["summary"].(string)
	if !strings.Contains(summary, "--model opus") {
		t.Errorf("summary = %q, should contain '--model opus'", summary)
	}
}

func TestAsk_SystemPrompt(t *testing.T) {
	p := New(discardLogger())
	p.cfg.Binary = "echo"
	_ = p.Init(nil)

	ev := plugin.Event{
		ID:      "test-3",
		Payload: map[string]any{"message": "test prompt"},
	}
	params := map[string]any{"system_prompt": "you are helpful"}
	result, err := p.Transform(context.Background(), ev, "ask", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	summary, _ := result.Payload["summary"].(string)
	if !strings.Contains(summary, "--system-prompt") {
		t.Errorf("summary = %q, should contain '--system-prompt'", summary)
	}
	if !strings.Contains(summary, "you are helpful") {
		t.Errorf("summary = %q, should contain 'you are helpful'", summary)
	}
}

func TestAsk_BinaryNotFound(t *testing.T) {
	p := New(discardLogger())
	p.cfg.Binary = "/nonexistent/path/to/binary"
	_ = p.Init(nil)

	ev := plugin.Event{Payload: map[string]any{"message": "test"}}
	_, err := p.Transform(context.Background(), ev, "ask", nil)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestClaudeCode_StartStop(t *testing.T) {
	p := New(discardLogger())
	_ = p.Init(nil)

	if err := p.Start(context.Background(), nil); err != nil {
		t.Errorf("Start() error = %v, want nil", err)
	}
	if err := p.Stop(); err != nil {
		t.Errorf("Stop() error = %v, want nil", err)
	}
}

func TestClaudeCode_Init_InvalidJSON(t *testing.T) {
	p := New(discardLogger())
	err := p.Init(json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "claudecode config") {
		t.Errorf("error = %q, want it to contain %q", err, "claudecode config")
	}
}

func TestAsk_ContextCancelled(t *testing.T) {
	p := New(discardLogger())
	// Use "sleep" as the binary — it will block until cancelled.
	p.cfg.Binary = "sleep"
	_ = p.Init(nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	ev := plugin.Event{Payload: map[string]any{"message": "10"}}
	_, err := p.Transform(ctx, ev, "ask", nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestAsk_DefaultBinary(t *testing.T) {
	p := New(discardLogger())
	_ = p.Init(nil)
	// Binary should default to empty (will become "claude" at runtime).
	if p.cfg.Binary != "" {
		t.Errorf("default binary = %q, want empty", p.cfg.Binary)
	}
}
