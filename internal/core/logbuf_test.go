package core

import (
	"context"
	"log/slog"
	"testing"
)

// captureHandler records all slog.Records passed to Handle.
type captureHandler struct {
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func TestLogBuffer_Empty(t *testing.T) {
	buf := NewLogBuffer(10)
	entries := buf.Entries()
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}

func TestLogBuffer_AddOne(t *testing.T) {
	buf := NewLogBuffer(5)
	inner := &captureHandler{}
	h := NewLogHandler(inner, buf)
	logger := slog.New(h)

	logger.Info("hello")

	entries := buf.Entries()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Message != "hello" {
		t.Errorf("message = %q, want %q", entries[0].Message, "hello")
	}
	if entries[0].Level != "INFO" {
		t.Errorf("level = %q, want %q", entries[0].Level, "INFO")
	}
}

func TestLogBuffer_FillToCapacity(t *testing.T) {
	const max = 5
	buf := NewLogBuffer(max)
	inner := &captureHandler{}
	h := NewLogHandler(inner, buf)
	logger := slog.New(h)

	for i := range max {
		logger.Info("msg", "i", i)
	}

	entries := buf.Entries()
	if len(entries) != max {
		t.Fatalf("got %d entries, want %d", len(entries), max)
	}
	// Verify order: oldest first.
	if entries[0].Message != "msg" {
		t.Errorf("first entry message = %q, want %q", entries[0].Message, "msg")
	}
}

func TestLogBuffer_OverflowEvicts(t *testing.T) {
	const max = 3
	buf := NewLogBuffer(max)
	inner := &captureHandler{}
	h := NewLogHandler(inner, buf)
	logger := slog.New(h)

	messages := []string{"a", "b", "c", "d"}
	for _, m := range messages {
		logger.Info(m)
	}

	entries := buf.Entries()
	if len(entries) != max {
		t.Fatalf("got %d entries, want %d", len(entries), max)
	}
	// "a" should be evicted.
	if entries[0].Message != "b" {
		t.Errorf("entries[0].Message = %q, want %q", entries[0].Message, "b")
	}
	if entries[1].Message != "c" {
		t.Errorf("entries[1].Message = %q, want %q", entries[1].Message, "c")
	}
	if entries[2].Message != "d" {
		t.Errorf("entries[2].Message = %q, want %q", entries[2].Message, "d")
	}
}

func TestLogBuffer_OverflowMultiple(t *testing.T) {
	const max = 3
	buf := NewLogBuffer(max)
	inner := &captureHandler{}
	h := NewLogHandler(inner, buf)
	logger := slog.New(h)

	// Add 2*max entries.
	msgs := []string{"a", "b", "c", "d", "e", "f"}
	for _, m := range msgs {
		logger.Info(m)
	}

	entries := buf.Entries()
	if len(entries) != max {
		t.Fatalf("got %d entries, want %d", len(entries), max)
	}
	// Only last 3 should remain.
	want := []string{"d", "e", "f"}
	for i, w := range want {
		if entries[i].Message != w {
			t.Errorf("entries[%d].Message = %q, want %q", i, entries[i].Message, w)
		}
	}
}

func TestLogBuffer_EntriesCopy(t *testing.T) {
	buf := NewLogBuffer(5)
	inner := &captureHandler{}
	h := NewLogHandler(inner, buf)
	logger := slog.New(h)

	logger.Info("original")

	entries := buf.Entries()
	entries[0].Message = "modified"

	// Buffer should be unaffected.
	fresh := buf.Entries()
	if fresh[0].Message != "original" {
		t.Errorf("buffer was modified via returned slice: got %q, want %q", fresh[0].Message, "original")
	}
}

func TestLogHandler_CapturesRecord(t *testing.T) {
	buf := NewLogBuffer(10)
	inner := &captureHandler{}
	h := NewLogHandler(inner, buf)
	logger := slog.New(h)

	logger.Warn("warning msg")

	entries := buf.Entries()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Message != "warning msg" {
		t.Errorf("message = %q, want %q", entries[0].Message, "warning msg")
	}
	if entries[0].Level != "WARN" {
		t.Errorf("level = %q, want %q", entries[0].Level, "WARN")
	}
}

func TestLogHandler_DelegatesToInner(t *testing.T) {
	buf := NewLogBuffer(10)
	inner := &captureHandler{}
	h := NewLogHandler(inner, buf)
	logger := slog.New(h)

	logger.Info("delegated")

	if len(inner.records) != 1 {
		t.Fatalf("inner got %d records, want 1", len(inner.records))
	}
	if inner.records[0].Message != "delegated" {
		t.Errorf("inner message = %q, want %q", inner.records[0].Message, "delegated")
	}
}

func TestLogHandler_WithAttrs(t *testing.T) {
	buf := NewLogBuffer(10)
	inner := &captureHandler{}
	h := NewLogHandler(inner, buf)

	h2 := h.WithAttrs([]slog.Attr{slog.String("key", "val")})

	lh, ok := h2.(*LogHandler)
	if !ok {
		t.Fatal("WithAttrs did not return a *LogHandler")
	}
	// Must share the same buffer.
	if lh.buf != buf {
		t.Error("WithAttrs returned handler with different buffer")
	}
}

func TestLogHandler_WithGroup(t *testing.T) {
	buf := NewLogBuffer(10)
	inner := &captureHandler{}
	h := NewLogHandler(inner, buf)

	h2 := h.WithGroup("grp")

	lh, ok := h2.(*LogHandler)
	if !ok {
		t.Fatal("WithGroup did not return a *LogHandler")
	}
	// Must share the same buffer.
	if lh.buf != buf {
		t.Error("WithGroup returned handler with different buffer")
	}
}
