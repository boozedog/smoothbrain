package claudecode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boozedog/smoothbrain/internal/plugin"
	"github.com/boozedog/smoothbrain/pkg/claudecode"
	_ "modernc.org/sqlite"
)

// --- Helpers ---

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type mockBus struct {
	mu     sync.Mutex
	events []plugin.Event
}

func (b *mockBus) Emit(ev plugin.Event) {
	b.mu.Lock()
	b.events = append(b.events, ev)
	b.mu.Unlock()
}

func (b *mockBus) getEvents() []plugin.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := make([]plugin.Event, len(b.events))
	copy(cp, b.events)
	return cp
}

// mockBinaryScript creates a temp shell script that outputs valid NDJSON for
// claude CLI streaming. It writes received args to an "args" file in the same
// directory so tests can verify what flags were passed.
func mockBinaryScript(t *testing.T, resultText, sessionID string, costUSD float64, inputTokens, outputTokens int) (binaryPath, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > '%s'
echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"%s"}}}'
echo '{"type":"assistant","message":{"model":"test-model","content":[{"type":"text","text":"%s"}]}}'
echo '{"type":"result","subtype":"success","result":"%s","session_id":"%s","total_cost_usd":%f,"usage":{"input_tokens":%d,"output_tokens":%d},"duration_ms":100}'
`, argsFile, resultText, resultText, resultText, sessionID, costUSD, inputTokens, outputTokens)
	binaryPath = filepath.Join(dir, "mock-claude")
	if err := os.WriteFile(binaryPath, []byte(script), 0o755); err != nil { //nolint:gosec // test mock binary needs to be executable
		t.Fatal(err)
	}
	return binaryPath, argsFile
}

// testDB creates an in-memory SQLite database with the plugin_state table.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS plugin_state (
		plugin TEXT NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (plugin, key)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// newTestPlugin creates a plugin with a mock binary and bus, ready for ask/chat tests.
func newTestPlugin(t *testing.T, resultText, sessionID string, costUSD float64, inputTokens, outputTokens int) (*Plugin, *mockBus, string) {
	t.Helper()
	bin, argsFile := mockBinaryScript(t, resultText, sessionID, costUSD, inputTokens, outputTokens)
	bus := &mockBus{}
	p := New(discardLogger())
	p.cfg.Binary = bin
	if err := p.Init(nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(context.Background(), bus); err != nil {
		t.Fatal(err)
	}
	return p, bus, argsFile
}

// --- Init tests ---

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
	if p.cfg.PermissionMode != "" {
		t.Errorf("permission_mode = %q, want empty", p.cfg.PermissionMode)
	}
	if p.cfg.SessionTTL != "" {
		t.Errorf("session_ttl raw = %q, want empty", p.cfg.SessionTTL)
	}
	if p.sessionTTL != time.Hour {
		t.Errorf("sessionTTL = %v, want %v", p.sessionTTL, time.Hour)
	}
	if p.cfg.WireLog {
		t.Error("wire_log = true, want false")
	}
	if p.cfg.Workspaces != nil {
		t.Errorf("workspaces = %v, want nil", p.cfg.Workspaces)
	}
	if p.cfg.MaxTurns != 0 {
		t.Errorf("max_turns = %d, want 0", p.cfg.MaxTurns)
	}
}

func TestClaudeCode_Init_Config(t *testing.T) {
	p := New(discardLogger())
	cfg := json.RawMessage(`{
		"binary": "/usr/bin/claude",
		"model": "opus",
		"permission_mode": "bypassPermissions",
		"session_ttl": "30m",
		"wire_log": true,
		"workspaces": {
			"default": {"path": "/home/user/projects", "tools": "Bash,Read,Edit", "append_system_prompt": "Be concise."},
			"docs": {"path": "/home/user/docs"}
		},
		"max_turns": 25
	}`)
	if err := p.Init(cfg); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	if p.cfg.Binary != "/usr/bin/claude" {
		t.Errorf("binary = %q, want %q", p.cfg.Binary, "/usr/bin/claude")
	}
	if p.cfg.Model != "opus" {
		t.Errorf("model = %q, want %q", p.cfg.Model, "opus")
	}
	if p.cfg.PermissionMode != "bypassPermissions" {
		t.Errorf("permission_mode = %q, want %q", p.cfg.PermissionMode, "bypassPermissions")
	}
	if p.sessionTTL != 30*time.Minute {
		t.Errorf("sessionTTL = %v, want %v", p.sessionTTL, 30*time.Minute)
	}
	if !p.cfg.WireLog {
		t.Error("wire_log = false, want true")
	}
	if len(p.cfg.Workspaces) != 2 {
		t.Fatalf("workspaces count = %d, want 2", len(p.cfg.Workspaces))
	}
	if p.cfg.Workspaces["default"].Path != "/home/user/projects" {
		t.Errorf("workspaces[default].Path = %q, want %q", p.cfg.Workspaces["default"].Path, "/home/user/projects")
	}
	if p.cfg.Workspaces["default"].Tools != "Bash,Read,Edit" {
		t.Errorf("workspaces[default].Tools = %q, want %q", p.cfg.Workspaces["default"].Tools, "Bash,Read,Edit")
	}
	if p.cfg.Workspaces["default"].AppendSystemPrompt != "Be concise." {
		t.Errorf("workspaces[default].AppendSystemPrompt = %q, want %q", p.cfg.Workspaces["default"].AppendSystemPrompt, "Be concise.")
	}
	if p.cfg.Workspaces["docs"].Path != "/home/user/docs" {
		t.Errorf("workspaces[docs].Path = %q, want %q", p.cfg.Workspaces["docs"].Path, "/home/user/docs")
	}
	if p.cfg.MaxTurns != 25 {
		t.Errorf("max_turns = %d, want 25", p.cfg.MaxTurns)
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

func TestClaudeCode_Init_InvalidSessionTTL(t *testing.T) {
	p := New(discardLogger())
	err := p.Init(json.RawMessage(`{"session_ttl": "not-a-duration"}`))
	if err == nil {
		t.Fatal("expected error for invalid session_ttl")
	}
	if !strings.Contains(err.Error(), "invalid session_ttl") {
		t.Errorf("error = %q, want it to contain %q", err, "invalid session_ttl")
	}
}

// --- Transform routing ---

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

// --- Ask tests ---

func TestAsk_NoMessage(t *testing.T) {
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
	p, bus, _ := newTestPlugin(t, "test response", "sess-123", 0.05, 100, 50)

	ev := plugin.Event{
		ID:      "test-1",
		Payload: map[string]any{"message": "hello world"},
	}
	result, err := p.Transform(context.Background(), ev, "ask", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify summary is set from result text.
	summary, ok := result.Payload["response"].(string)
	if !ok || summary == "" {
		t.Fatal("expected non-empty summary")
	}
	if summary != "test response" {
		t.Errorf("summary = %q, want %q", summary, "test response")
	}

	// Verify stats were updated.
	p.mu.Lock()
	stats := p.stats
	p.mu.Unlock()
	if stats.TotalRequests != 1 {
		t.Errorf("total_requests = %d, want 1", stats.TotalRequests)
	}
	if stats.TotalTokens != 150 {
		t.Errorf("total_tokens = %d, want 150 (100+50)", stats.TotalTokens)
	}
	if stats.TotalCostUSD != 0.05 {
		t.Errorf("total_cost_usd = %f, want 0.05", stats.TotalCostUSD)
	}

	// Verify stream delta was emitted to bus.
	events := bus.getEvents()
	var foundDelta bool
	for _, ev := range events {
		if ev.Type == "stream" {
			if td, ok := ev.Payload["text_delta"].(string); ok && td == "test response" {
				foundDelta = true
			}
		}
	}
	if !foundDelta {
		t.Error("expected stream event with text_delta emitted to bus")
	}
}

func TestAsk_WorkspaceResolution(t *testing.T) {
	p := New(discardLogger())
	p.cfg.Workspaces = map[string]WorkspaceConfig{
		"main": {Path: "/home/user/projects"},
	}
	_ = p.Init(nil)

	ev := plugin.Event{Payload: map[string]any{}}

	// Valid workspace maps to CWD.
	opts, err := p.buildOpts(ev, map[string]any{"workspace": "main"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.CWD != "/home/user/projects" {
		t.Errorf("CWD = %q, want %q", opts.CWD, "/home/user/projects")
	}

	// Unknown workspace returns error.
	_, err = p.buildOpts(ev, map[string]any{"workspace": "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown workspace")
	}
	if !strings.Contains(err.Error(), "unknown workspace") {
		t.Errorf("error = %q, want it to contain %q", err, "unknown workspace")
	}

	// Channel workspace: channel_id in payload maps via sources config.
	p.cfg.Sources = map[string]SourceConfig{"mattermost": {ChannelWorkspaces: map[string]string{"chan-abc": "main"}}}
	evWithChannel := plugin.Event{Source: "mattermost", Payload: map[string]any{"channel_id": "chan-abc"}}
	opts, err = p.buildOpts(evWithChannel, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.CWD != "/home/user/projects" {
		t.Errorf("channel workspace CWD = %q, want %q", opts.CWD, "/home/user/projects")
	}

	// Explicit workspace param overrides channel workspace.
	opts, err = p.buildOpts(evWithChannel, map[string]any{"workspace": "main"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.CWD != "/home/user/projects" {
		t.Errorf("explicit workspace CWD = %q, want %q", opts.CWD, "/home/user/projects")
	}

	// Unknown channel_id has no mapping — CWD stays empty.
	evUnknownChan := plugin.Event{Source: "mattermost", Payload: map[string]any{"channel_id": "chan-xyz"}}
	opts, err = p.buildOpts(evUnknownChan, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.CWD != "" {
		t.Errorf("unmapped channel CWD = %q, want empty", opts.CWD)
	}
}

func TestAsk_PermissionModeOverride(t *testing.T) {
	p := New(discardLogger())
	_ = p.Init(nil)
	ev := plugin.Event{Payload: map[string]any{}}

	// Default: "plan".
	opts, err := p.buildOpts(ev, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.PermissionMode != "plan" {
		t.Errorf("default permission_mode = %q, want %q", opts.PermissionMode, "plan")
	}

	// Config overrides default.
	p.cfg.PermissionMode = "bypassPermissions"
	opts, err = p.buildOpts(ev, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.PermissionMode != "bypassPermissions" {
		t.Errorf("config permission_mode = %q, want %q", opts.PermissionMode, "bypassPermissions")
	}

	// Param overrides config.
	opts, err = p.buildOpts(ev, map[string]any{"permission_mode": "default"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.PermissionMode != "default" {
		t.Errorf("param permission_mode = %q, want %q", opts.PermissionMode, "default")
	}
}

func TestAsk_SystemPrompt(t *testing.T) {
	p := New(discardLogger())
	_ = p.Init(nil)
	ev := plugin.Event{Payload: map[string]any{}}

	// SystemPrompt is always hard-coded.
	wantSP := "Do not read or modify anything outside the current directory and its subdirectories."

	// Route param system_prompt maps to AppendSystemPrompt.
	opts, err := p.buildOpts(ev, map[string]any{"system_prompt": "you are helpful"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.SystemPrompt != wantSP {
		t.Errorf("SystemPrompt = %q, want %q", opts.SystemPrompt, wantSP)
	}
	if opts.AppendSystemPrompt != "you are helpful" {
		t.Errorf("AppendSystemPrompt = %q, want %q", opts.AppendSystemPrompt, "you are helpful")
	}
}

func TestAsk_BinaryNotFound(t *testing.T) {
	bus := &mockBus{}
	p := New(discardLogger())
	p.cfg.Binary = "/nonexistent/path/to/binary"
	_ = p.Init(nil)
	_ = p.Start(context.Background(), bus)

	ev := plugin.Event{Payload: map[string]any{"message": "test"}}
	_, err := p.Transform(context.Background(), ev, "ask", nil)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

// --- Chat tests ---

func TestChat_NewSession(t *testing.T) {
	p, _, _ := newTestPlugin(t, "chat response", "new-sess-456", 0.03, 80, 40)

	ev := plugin.Event{
		ID:      "chat-1",
		Source:  "test-source",
		Payload: map[string]any{"message": "hello"},
	}
	result, err := p.Transform(context.Background(), ev, "chat", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	summary, _ := result.Payload["response"].(string)
	if summary != "chat response" {
		t.Errorf("summary = %q, want %q", summary, "chat response")
	}

	// Verify session was stored (keyed by Source since no session_key param).
	p.mu.Lock()
	entry, ok := p.sessions["test-source"]
	p.mu.Unlock()
	if !ok {
		t.Fatal("expected session to be stored for key 'test-source'")
	}
	if entry.SessionID != "new-sess-456" {
		t.Errorf("session_id = %q, want %q", entry.SessionID, "new-sess-456")
	}
}

func TestChat_ResumeSession(t *testing.T) {
	p, _, argsFile := newTestPlugin(t, "resumed response", "existing-sess", 0.02, 60, 30)

	// Pre-populate with a valid session.
	p.mu.Lock()
	p.sessions["test-source"] = sessionEntry{
		SessionID: "existing-sess",
		LastUsed:  time.Now(),
	}
	p.mu.Unlock()

	ev := plugin.Event{
		ID:      "chat-2",
		Source:  "test-source",
		Payload: map[string]any{"message": "continue"},
	}
	_, err := p.Transform(context.Background(), ev, "chat", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify --resume was passed to the binary.
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read args file: %v", err)
	}
	argStr := string(args)
	if !strings.Contains(argStr, "--resume") {
		t.Error("expected --resume flag to be passed to binary")
	}
	if !strings.Contains(argStr, "existing-sess") {
		t.Errorf("expected session ID in args, got:\n%s", argStr)
	}
}

func TestChat_SessionTTLExpiry(t *testing.T) {
	p, _, argsFile := newTestPlugin(t, "fresh response", "fresh-sess", 0.01, 40, 20)

	// Pre-populate with an expired session (2h ago, default TTL is 1h).
	p.mu.Lock()
	p.sessions["test-source"] = sessionEntry{
		SessionID: "old-sess",
		LastUsed:  time.Now().Add(-2 * time.Hour),
	}
	p.mu.Unlock()

	ev := plugin.Event{
		ID:      "chat-3",
		Source:  "test-source",
		Payload: map[string]any{"message": "hello"},
	}
	_, err := p.Transform(context.Background(), ev, "chat", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify --resume was NOT passed (session expired).
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read args file: %v", err)
	}
	if strings.Contains(string(args), "--resume") {
		t.Error("expected --resume NOT to be passed for expired session")
	}

	// Verify new session replaced the expired one.
	p.mu.Lock()
	entry, ok := p.sessions["test-source"]
	p.mu.Unlock()
	if !ok {
		t.Fatal("expected new session to be stored")
	}
	if entry.SessionID != "fresh-sess" {
		t.Errorf("session_id = %q, want %q", entry.SessionID, "fresh-sess")
	}
}

func TestChat_SessionKeyField(t *testing.T) {
	p, _, _ := newTestPlugin(t, "threaded response", "thread-sess", 0.01, 30, 10)

	ev := plugin.Event{
		ID:      "chat-keyfield",
		Source:  "mattermost",
		Payload: map[string]any{"message": "hello", "root_id": "post-abc123"},
	}
	params := map[string]any{"session_key_field": "root_id"}
	_, err := p.Transform(context.Background(), ev, "chat", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Session should be stored under "post-abc123", not "mattermost".
	p.mu.Lock()
	_, byField := p.sessions["post-abc123"]
	_, bySource := p.sessions["mattermost"]
	p.mu.Unlock()
	if !byField {
		t.Error("expected session stored under payload field value 'post-abc123'")
	}
	if bySource {
		t.Error("session should NOT be stored under event.Source 'mattermost'")
	}
}

func TestChat_SessionKeyFieldEmpty(t *testing.T) {
	p, _, _ := newTestPlugin(t, "fallback response", "fallback-sess", 0.01, 30, 10)

	ev := plugin.Event{
		ID:      "chat-keyfield-empty",
		Source:  "mattermost",
		Payload: map[string]any{"message": "hello", "root_id": ""},
	}
	params := map[string]any{"session_key_field": "root_id"}
	_, err := p.Transform(context.Background(), ev, "chat", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Empty field value should fall back to event.Source.
	p.mu.Lock()
	_, bySource := p.sessions["mattermost"]
	p.mu.Unlock()
	if !bySource {
		t.Error("expected session to fall back to event.Source 'mattermost'")
	}
}

// --- Health check ---

func TestHealthCheck_Format(t *testing.T) {
	p := New(discardLogger())
	_ = p.Init(nil)

	p.mu.Lock()
	p.stats = Stats{
		TotalRequests: 5,
		TotalTokens:   12500,
		TotalCostUSD:  1.23,
	}
	p.sessions["s1"] = sessionEntry{SessionID: "a"}
	p.sessions["s2"] = sessionEntry{SessionID: "b"}
	p.mu.Unlock()

	status := p.HealthCheck(context.Background())
	if status.Status != plugin.StatusOK {
		t.Errorf("status = %q, want %q", status.Status, plugin.StatusOK)
	}
	want := "$1.23 | 12.5k tokens | 5 reqs | 2 sessions"
	if status.Message != want {
		t.Errorf("message = %q, want %q", status.Message, want)
	}
}

// --- Store ---

func TestSetStore(t *testing.T) {
	db := testDB(t)
	p := New(discardLogger())
	p.SetStore(db)
	if p.db != db {
		t.Error("expected db reference to be stored")
	}
}

// --- Stats ---

func TestStatsAccumulation(t *testing.T) {
	p := New(discardLogger())
	_ = p.Init(nil)

	p.updateStats(claudecode.Result{
		CostUSD: 0.10,
		Usage:   claudecode.TokenUsage{InputTokens: 100, OutputTokens: 50},
	})
	p.updateStats(claudecode.Result{
		CostUSD: 0.20,
		Usage:   claudecode.TokenUsage{InputTokens: 200, OutputTokens: 100},
	})
	p.updateStats(claudecode.Result{
		CostUSD: 0.05,
		Usage:   claudecode.TokenUsage{InputTokens: 50, OutputTokens: 25},
	})

	p.mu.Lock()
	stats := p.stats
	p.mu.Unlock()

	if stats.TotalRequests != 3 {
		t.Errorf("total_requests = %d, want 3", stats.TotalRequests)
	}
	if stats.TotalTokens != 525 {
		t.Errorf("total_tokens = %d, want 525", stats.TotalTokens)
	}
	if stats.TotalCostUSD < 0.349 || stats.TotalCostUSD > 0.351 {
		t.Errorf("total_cost_usd = %f, want ~0.35", stats.TotalCostUSD)
	}
}

// --- Access control ---

func TestTransform_SourceFirewall(t *testing.T) {
	p := New(discardLogger())
	p.cfg.Sources = map[string]SourceConfig{"mattermost": {}}
	_ = p.Init(nil)

	ev := plugin.Event{
		Source:  "webhook",
		Payload: map[string]any{"message": "hello"},
	}
	_, err := p.Transform(context.Background(), ev, "ask", nil)
	if err == nil {
		t.Fatal("expected error for disallowed source")
	}
	if !strings.Contains(err.Error(), "source \"webhook\" not allowed") {
		t.Errorf("error = %q, want it to contain source not allowed", err)
	}
}

func TestTransform_SourceFirewall_AllowedPasses(t *testing.T) {
	p, _, _ := newTestPlugin(t, "ok", "sess-1", 0.01, 10, 5)
	p.cfg.Sources = map[string]SourceConfig{"mattermost": {}}

	ev := plugin.Event{
		ID:      "test-allowed",
		Source:  "mattermost",
		Payload: map[string]any{"message": "hello"},
	}
	_, err := p.Transform(context.Background(), ev, "ask", nil)
	if err != nil {
		t.Fatalf("unexpected error for allowed source: %v", err)
	}
}

func TestTransform_SourceFirewall_NoConfig(t *testing.T) {
	p, _, _ := newTestPlugin(t, "ok", "sess-1", 0.01, 10, 5)
	// No Sources configured — all sources pass.

	ev := plugin.Event{
		ID:      "test-no-config",
		Source:  "anything",
		Payload: map[string]any{"message": "hello"},
	}
	_, err := p.Transform(context.Background(), ev, "ask", nil)
	if err != nil {
		t.Fatalf("unexpected error with no source config: %v", err)
	}
}

func TestTransform_ChannelFirewall(t *testing.T) {
	p := New(discardLogger())
	p.cfg.Sources = map[string]SourceConfig{"mattermost": {ChannelWorkspaces: map[string]string{"chan-abc": "main"}}}
	_ = p.Init(nil)

	ev := plugin.Event{
		Source:  "mattermost",
		Payload: map[string]any{"message": "hello", "channel_id": "chan-xyz"},
	}
	_, err := p.Transform(context.Background(), ev, "ask", nil)
	if err == nil {
		t.Fatal("expected error for unmapped channel")
	}
	if !strings.Contains(err.Error(), "not in allowed channels") {
		t.Errorf("error = %q, want it to contain not in allowed channels", err)
	}
}

func TestTransform_ChannelFirewall_MappedPasses(t *testing.T) {
	p, _, _ := newTestPlugin(t, "ok", "sess-1", 0.01, 10, 5)
	p.cfg.Sources = map[string]SourceConfig{"mattermost": {ChannelWorkspaces: map[string]string{"chan-abc": "main"}}}
	p.cfg.Workspaces = map[string]WorkspaceConfig{"main": {Path: "/tmp"}}

	ev := plugin.Event{
		ID:      "test-mapped",
		Source:  "mattermost",
		Payload: map[string]any{"message": "hello", "channel_id": "chan-abc"},
	}
	_, err := p.Transform(context.Background(), ev, "ask", nil)
	if err != nil {
		t.Fatalf("unexpected error for mapped channel: %v", err)
	}
}

func TestTransform_AllowedAccess(t *testing.T) {
	p, _, _ := newTestPlugin(t, "ok", "sess-1", 0.01, 10, 5)
	p.cfg.Sources = map[string]SourceConfig{
		"mattermost": {
			AllowedUsers:      []string{"user-abc"},
			ChannelWorkspaces: map[string]string{"chan-abc": "main"},
		},
	}
	p.cfg.Workspaces = map[string]WorkspaceConfig{"main": {Path: "/tmp"}}

	ev := plugin.Event{
		ID:      "test-full-access",
		Source:  "mattermost",
		Payload: map[string]any{"message": "hello", "channel_id": "chan-abc", "user_id": "user-abc"},
	}
	_, err := p.Transform(context.Background(), ev, "ask", nil)
	if err != nil {
		t.Fatalf("unexpected error for fully allowed access: %v", err)
	}
}

func TestTransform_UserFirewall(t *testing.T) {
	p := New(discardLogger())
	p.cfg.Sources = map[string]SourceConfig{"mattermost": {AllowedUsers: []string{"user-abc"}}}
	_ = p.Init(nil)

	ev := plugin.Event{
		Source:  "mattermost",
		Payload: map[string]any{"message": "hello", "user_id": "user-xyz"},
	}
	_, err := p.Transform(context.Background(), ev, "ask", nil)
	if err == nil {
		t.Fatal("expected error for disallowed user")
	}
	if !strings.Contains(err.Error(), "user \"user-xyz\" not allowed") {
		t.Errorf("error = %q, want it to contain user not allowed", err)
	}
}

func TestTransform_AccessDenied_ErrorType(t *testing.T) {
	p := New(discardLogger())
	p.cfg.Sources = map[string]SourceConfig{"mattermost": {}}
	_ = p.Init(nil)

	ev := plugin.Event{
		Source:  "webhook",
		Payload: map[string]any{"message": "hello"},
	}
	_, err := p.Transform(context.Background(), ev, "ask", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var accessErr *plugin.AccessDeniedError
	if !errors.As(err, &accessErr) {
		t.Errorf("expected *plugin.AccessDeniedError, got %T", err)
	}
}

func TestTransform_UserFirewall_NoUserID(t *testing.T) {
	p := New(discardLogger())
	p.cfg.Sources = map[string]SourceConfig{"mattermost": {AllowedUsers: []string{"user-abc"}}}
	_ = p.Init(nil)

	// No user_id in payload — should be rejected (empty string not in allowlist).
	ev := plugin.Event{
		Source:  "mattermost",
		Payload: map[string]any{"message": "hello"},
	}
	_, err := p.Transform(context.Background(), ev, "ask", nil)
	if err == nil {
		t.Fatal("expected error for missing user_id")
	}
}

// --- WorkspaceChannels ---

func TestWorkspaceChannels(t *testing.T) {
	p := New(discardLogger())
	p.cfg.Sources = map[string]SourceConfig{
		"mattermost": {
			ChannelWorkspaces: map[string]string{
				"chan-1": "ws1",
				"chan-2": "ws2",
			},
		},
		"other": {
			ChannelWorkspaces: map[string]string{
				"chan-3": "ws3",
			},
		},
	}

	channels := p.WorkspaceChannels()
	if len(channels) != 3 {
		t.Fatalf("WorkspaceChannels() returned %d channels, want 3", len(channels))
	}
	// Check all channels are present (order not guaranteed with maps).
	chanSet := make(map[string]bool)
	for _, ch := range channels {
		chanSet[ch] = true
	}
	for _, want := range []string{"chan-1", "chan-2", "chan-3"} {
		if !chanSet[want] {
			t.Errorf("WorkspaceChannels() missing %q", want)
		}
	}
}

func TestWorkspaceChannels_Empty(t *testing.T) {
	p := New(discardLogger())
	channels := p.WorkspaceChannels()
	if len(channels) != 0 {
		t.Errorf("WorkspaceChannels() returned %d channels, want 0", len(channels))
	}
}

// --- buildOpts ---

func TestBuildOpts_HardCodedFlags(t *testing.T) {
	p := New(discardLogger())
	_ = p.Init(nil)
	ev := plugin.Event{Payload: map[string]any{}}

	opts, err := p.buildOpts(ev, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !opts.DisableSlashCommands {
		t.Error("DisableSlashCommands = false, want true")
	}
	if !opts.NoChrome {
		t.Error("NoChrome = false, want true")
	}
	wantSP := "Do not read or modify anything outside the current directory and its subdirectories."
	if opts.SystemPrompt != wantSP {
		t.Errorf("SystemPrompt = %q, want %q", opts.SystemPrompt, wantSP)
	}
}

func TestBuildOpts_ConfigFlags(t *testing.T) {
	p := New(discardLogger())
	cfg := json.RawMessage(`{
		"max_turns": 10,
		"workspaces": {
			"main": {"path": "/tmp/project", "tools": "Bash,Read", "append_system_prompt": "Be brief."}
		}
	}`)
	if err := p.Init(cfg); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	ev := plugin.Event{Payload: map[string]any{}}

	// Without workspace, tools and append_system_prompt are empty.
	opts, err := p.buildOpts(ev, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.MaxTurns != 10 {
		t.Errorf("MaxTurns = %d, want 10", opts.MaxTurns)
	}
	if opts.Tools != "" {
		t.Errorf("Tools = %q, want empty (no workspace)", opts.Tools)
	}
	if opts.AppendSystemPrompt != "" {
		t.Errorf("AppendSystemPrompt = %q, want empty (no workspace)", opts.AppendSystemPrompt)
	}

	// With workspace, tools and append_system_prompt come from workspace config.
	opts, err = p.buildOpts(ev, map[string]any{"workspace": "main"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Tools != "Bash,Read" {
		t.Errorf("Tools = %q, want %q", opts.Tools, "Bash,Read")
	}
	if opts.AppendSystemPrompt != "Be brief." {
		t.Errorf("AppendSystemPrompt = %q, want %q", opts.AppendSystemPrompt, "Be brief.")
	}

	// Route param overrides workspace for AppendSystemPrompt.
	opts, err = p.buildOpts(ev, map[string]any{"workspace": "main", "system_prompt": "override prompt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.AppendSystemPrompt != "override prompt" {
		t.Errorf("AppendSystemPrompt = %q, want %q", opts.AppendSystemPrompt, "override prompt")
	}
}

// --- Lifecycle ---

func TestStartStop(t *testing.T) {
	p := New(discardLogger())
	_ = p.Init(nil)

	bus := &mockBus{}
	if err := p.Start(context.Background(), bus); err != nil {
		t.Errorf("Start() error = %v, want nil", err)
	}
	if p.bus != bus {
		t.Error("expected bus to be set after Start")
	}
	if err := p.Stop(); err != nil {
		t.Errorf("Stop() error = %v, want nil", err)
	}
}
