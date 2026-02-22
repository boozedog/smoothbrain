package obsidian

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boozedog/smoothbrain/internal/plugin"
	_ "modernc.org/sqlite"
)

func newTestObsidian(t *testing.T) *Plugin {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.cfg.VaultPath = dir
	p.db = db
	if err := p.initSchema(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTransform_UnknownAction(t *testing.T) {
	p := newTestObsidian(t)
	ev := plugin.Event{Payload: map[string]any{}}
	_, err := p.Transform(context.Background(), ev, "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("error %q should contain %q", err.Error(), "unknown action")
	}
}

func TestSearch_MissingMessage(t *testing.T) {
	p := newTestObsidian(t)
	ev := plugin.Event{Payload: map[string]any{}}
	_, err := p.search(context.Background(), ev, nil)
	if err == nil {
		t.Fatal("expected error for missing message")
	}
	if !strings.Contains(err.Error(), "missing message") {
		t.Errorf("error %q should contain %q", err.Error(), "missing message")
	}
}

func TestSearch_EmptyIndex(t *testing.T) {
	p := newTestObsidian(t)
	ev := plugin.Event{Payload: map[string]any{"message": "something"}}
	result, err := p.search(context.Background(), ev, nil)
	if err != nil {
		t.Fatal(err)
	}
	summary, _ := result.Payload["response"].(string)
	if !strings.Contains(summary, "No results") {
		t.Errorf("summary %q should contain %q", summary, "No results")
	}
}

func TestRead_MissingPath(t *testing.T) {
	p := newTestObsidian(t)
	ev := plugin.Event{Payload: map[string]any{}}
	_, err := p.read(context.Background(), ev, nil)
	if err == nil {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(err.Error(), "missing path") {
		t.Errorf("error %q should contain %q", err.Error(), "missing path")
	}
}

func TestRead_PathEscapesVault(t *testing.T) {
	p := newTestObsidian(t)
	ev := plugin.Event{Payload: map[string]any{}}
	_, err := p.read(context.Background(), ev, map[string]any{"path": "../../etc/passwd"})
	if err == nil {
		t.Fatal("expected error for path escaping vault")
	}
	if !strings.Contains(err.Error(), "escapes vault") {
		t.Errorf("error %q should contain %q", err.Error(), "escapes vault")
	}
}

func TestRead_ValidFile(t *testing.T) {
	p := newTestObsidian(t)
	if err := os.WriteFile(filepath.Join(p.cfg.VaultPath, "test.md"), []byte("# Hello\nContent"), 0o600); err != nil {
		t.Fatal(err)
	}
	ev := plugin.Event{Payload: map[string]any{}}
	result, err := p.read(context.Background(), ev, map[string]any{"path": "test.md"})
	if err != nil {
		t.Fatal(err)
	}
	summary, _ := result.Payload["response"].(string)
	if !strings.Contains(summary, "# Hello") {
		t.Errorf("summary %q should contain file content", summary)
	}
	if !strings.Contains(summary, "Content") {
		t.Errorf("summary %q should contain %q", summary, "Content")
	}
}

func TestRead_AutoAppendsMd(t *testing.T) {
	p := newTestObsidian(t)
	if err := os.WriteFile(filepath.Join(p.cfg.VaultPath, "test.md"), []byte("# Hello\nContent"), 0o600); err != nil {
		t.Fatal(err)
	}
	ev := plugin.Event{Payload: map[string]any{}}
	result, err := p.read(context.Background(), ev, map[string]any{"path": "test"})
	if err != nil {
		t.Fatal(err)
	}
	summary, _ := result.Payload["response"].(string)
	if !strings.Contains(summary, "# Hello") {
		t.Errorf("summary %q should contain file content after auto-appending .md", summary)
	}
}

func TestQuery_MissingDir(t *testing.T) {
	p := newTestObsidian(t)
	ev := plugin.Event{Payload: map[string]any{}}
	_, err := p.query(context.Background(), ev, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing dir")
	}
	if !strings.Contains(err.Error(), "missing dir") {
		t.Errorf("error %q should contain %q", err.Error(), "missing dir")
	}
}

func TestQuery_DirEscapesVault(t *testing.T) {
	p := newTestObsidian(t)
	ev := plugin.Event{Payload: map[string]any{}}
	_, err := p.query(context.Background(), ev, map[string]any{"dir": "../../etc"})
	if err == nil {
		t.Fatal("expected error for dir escaping vault")
	}
	if !strings.Contains(err.Error(), "escapes vault") {
		t.Errorf("error %q should contain %q", err.Error(), "escapes vault")
	}
}
