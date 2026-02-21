package core

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/boozedog/smoothbrain/internal/store"
)

func TestShortID_Long(t *testing.T) {
	got := shortID("abcdefgh12345")
	want := "abcdefgh"
	if got != want {
		t.Errorf("shortID(%q) = %q, want %q", "abcdefgh12345", got, want)
	}
}

func TestShortID_Short(t *testing.T) {
	got := shortID("abc")
	want := "abc"
	if got != want {
		t.Errorf("shortID(%q) = %q, want %q", "abc", got, want)
	}
}

func TestStr_String(t *testing.T) {
	got := str("hello")
	want := "hello"
	if got != want {
		t.Errorf("str(%q) = %q, want %q", "hello", got, want)
	}
}

func TestStr_NonString(t *testing.T) {
	got := str(42)
	want := "42"
	if got != want {
		t.Errorf("str(42) = %q, want %q", got, want)
	}
}

func TestStr_Nil(t *testing.T) {
	got := str(nil)
	want := "<nil>"
	if got != want {
		t.Errorf("str(nil) = %q, want %q", got, want)
	}
}

func TestDurationStr(t *testing.T) {
	got := durationStr(123)
	want := "123ms"
	if got != want {
		t.Errorf("durationStr(123) = %q, want %q", got, want)
	}
}

func TestSourceColor_Deterministic(t *testing.T) {
	a := sourceColor("mattermost")
	b := sourceColor("mattermost")
	if a != b {
		t.Errorf("sourceColor not deterministic: %q != %q", a, b)
	}
	// Different name should (almost certainly) produce a different hue.
	c := sourceColor("obsidian")
	if a == c {
		t.Errorf("expected different colors for different names, both got %q", a)
	}
	// Should be a valid hsl string.
	if !strings.HasPrefix(a, "hsl(") || !strings.HasSuffix(a, ")") {
		t.Errorf("expected hsl(...) format, got %q", a)
	}
}

func TestColorizeJSON_Tokens(t *testing.T) {
	input := `{
  "name": "alice",
  "age": 30,
  "active": true,
  "deleted": false,
  "meta": null
}`
	got := colorizeJSON(input)

	// Keys should be wrapped in j-key spans.
	if !strings.Contains(got, `<span class="j-key">`) {
		t.Error("expected j-key span for JSON keys")
	}
	// String values should be wrapped in j-str spans.
	if !strings.Contains(got, `<span class="j-str">`) {
		t.Error("expected j-str span for string values")
	}
	// Numbers should be wrapped in j-num spans.
	if !strings.Contains(got, `<span class="j-num">`) {
		t.Error("expected j-num span for numbers")
	}
	// Booleans should be wrapped in j-bool spans.
	if !strings.Contains(got, `<span class="j-bool">`) {
		t.Error("expected j-bool span for booleans")
	}
	// Null should be wrapped in j-null span.
	if !strings.Contains(got, `<span class="j-null">`) {
		t.Error("expected j-null span for null")
	}
}

func TestRunBadgeClass(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"completed", "uk-label uk-label-primary"},
		{"failed", "uk-label uk-label-destructive"},
		{"running", "uk-label uk-label-secondary"},
		{"unknown", "uk-label"},
	}
	for _, tt := range tests {
		got := runBadgeClass(tt.status)
		if got != tt.want {
			t.Errorf("runBadgeClass(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestParseSteps_Valid(t *testing.T) {
	input := `[{"plugin":"p","action":"a","status":"ok","duration_ms":10}]`
	steps := parseSteps(input)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Plugin != "p" {
		t.Errorf("Plugin = %q, want %q", steps[0].Plugin, "p")
	}
	if steps[0].Action != "a" {
		t.Errorf("Action = %q, want %q", steps[0].Action, "a")
	}
	if steps[0].Status != "ok" {
		t.Errorf("Status = %q, want %q", steps[0].Status, "ok")
	}
	if steps[0].DurationMs != 10 {
		t.Errorf("DurationMs = %d, want 10", steps[0].DurationMs)
	}
}

func TestParseSteps_Invalid(t *testing.T) {
	steps := parseSteps("not json")
	if steps != nil {
		t.Errorf("expected nil for invalid JSON, got %v", steps)
	}
}

func TestParseSteps_Empty(t *testing.T) {
	steps := parseSteps("")
	if steps != nil {
		t.Errorf("expected nil for empty string, got %v", steps)
	}
}

func TestSourceLabelStyle(t *testing.T) {
	style := sourceLabelStyle("mattermost")
	if !strings.Contains(style, "background-color:") {
		t.Errorf("expected style to contain 'background-color:', got %q", style)
	}
}

func TestLogLevelClass(t *testing.T) {
	tests := []struct {
		level string
		want  string
	}{
		{"ERROR", "log-error"},
		{"WARN", "log-warn"},
		{"DEBUG", "log-debug"},
		{"INFO", ""},
	}
	for _, tt := range tests {
		got := logLevelClass(tt.level)
		if got != tt.want {
			t.Errorf("logLevelClass(%q) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestHealthBadgeClass(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"ok", "uk-label uk-label-primary"},
		{"degraded", "uk-label uk-label-secondary"},
		{"error", "uk-label uk-label-destructive"},
		{"unknown", "uk-label"},
	}
	for _, tt := range tests {
		got := healthBadgeClass(tt.status)
		if got != tt.want {
			t.Errorf("healthBadgeClass(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestToEventViews_Empty(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	views := toEventViews(nil, st, log)
	if len(views) != 0 {
		t.Errorf("expected 0 views, got %d", len(views))
	}
}

func TestToEventViews_Populated(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	events := []map[string]any{
		{
			"id":        "evt-500",
			"source":    "webhook",
			"type":      "push",
			"timestamp": "2025-01-01T00:00:00Z",
			"route":     "my-route",
			"payload":   json.RawMessage(`{"ref":"main"}`),
		},
	}
	views := toEventViews(events, st, log)
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if views[0].ID != "evt-500" {
		t.Errorf("ID = %q, want evt-500", views[0].ID)
	}
	if views[0].PrettyPayload == "" {
		t.Error("expected non-empty PrettyPayload")
	}
	if !strings.Contains(views[0].PrettyPayload, "main") {
		t.Errorf("PrettyPayload should contain 'main', got %q", views[0].PrettyPayload)
	}
}
