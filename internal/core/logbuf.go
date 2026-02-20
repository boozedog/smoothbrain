package core

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// LogEntry is a single captured log record for the web UI.
type LogEntry struct {
	Time    string
	Level   string
	Message string
	Attrs   string
}

// LogBuffer is a bounded ring buffer of recent log entries.
type LogBuffer struct {
	mu      sync.Mutex
	entries []LogEntry
	max     int
}

// NewLogBuffer creates a buffer that retains the most recent max entries.
func NewLogBuffer(max int) *LogBuffer {
	return &LogBuffer{max: max, entries: make([]LogEntry, 0, max)}
}

// Entries returns a copy of the buffered entries (oldest first).
func (lb *LogBuffer) Entries() []LogEntry {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	out := make([]LogEntry, len(lb.entries))
	copy(out, lb.entries)
	return out
}

// LogHandler wraps another slog.Handler, forwarding all records while
// also capturing them into a LogBuffer.
type LogHandler struct {
	inner slog.Handler
	buf   *LogBuffer
}

// NewLogHandler creates a handler that captures records into buf and delegates to inner.
func NewLogHandler(inner slog.Handler, buf *LogBuffer) *LogHandler {
	return &LogHandler{inner: inner, buf: buf}
}

func (h *LogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *LogHandler) Handle(ctx context.Context, r slog.Record) error {
	var attrs string
	r.Attrs(func(a slog.Attr) bool {
		if attrs != "" {
			attrs += " "
		}
		attrs += fmt.Sprintf("%s=%v", a.Key, a.Value)
		return true
	})

	entry := LogEntry{
		Time:    r.Time.Format(time.TimeOnly),
		Level:   r.Level.String(),
		Message: r.Message,
		Attrs:   attrs,
	}

	h.buf.mu.Lock()
	if len(h.buf.entries) >= h.buf.max {
		copy(h.buf.entries, h.buf.entries[1:])
		h.buf.entries = h.buf.entries[:h.buf.max-1]
	}
	h.buf.entries = append(h.buf.entries, entry)
	h.buf.mu.Unlock()

	return h.inner.Handle(ctx, r)
}

func (h *LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogHandler{inner: h.inner.WithAttrs(attrs), buf: h.buf}
}

func (h *LogHandler) WithGroup(name string) slog.Handler {
	return &LogHandler{inner: h.inner.WithGroup(name), buf: h.buf}
}
