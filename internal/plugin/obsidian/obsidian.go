package obsidian

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/boozedog/smoothbrain/internal/plugin"
)

type Config struct {
	VaultPath string `json:"vault_path"`
}

type Plugin struct {
	cfg     Config
	db      *sql.DB
	bus     plugin.EventBus
	log     *slog.Logger
	watcher *Watcher
}

func New(log *slog.Logger) *Plugin {
	return &Plugin{log: log}
}

func (p *Plugin) Name() string { return "obsidian" }

func (p *Plugin) SetStore(db *sql.DB) { p.db = db }

func (p *Plugin) Init(cfg json.RawMessage) error {
	p.cfg = Config{VaultPath: "~/obsidian/smoothbrain"}
	if err := json.Unmarshal(cfg, &p.cfg); err != nil {
		return fmt.Errorf("obsidian config: %w", err)
	}

	// Expand ~ in vault path.
	if strings.HasPrefix(p.cfg.VaultPath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("obsidian: resolve home dir: %w", err)
		}
		p.cfg.VaultPath = filepath.Join(home, p.cfg.VaultPath[2:])
	}

	return p.initSchema()
}

func (p *Plugin) Start(ctx context.Context, bus plugin.EventBus) error {
	p.bus = bus
	if err := p.IndexVault(); err != nil {
		p.log.Warn("obsidian: initial vault index failed", "error", err)
	}

	w, err := NewWatcher(p)
	if err != nil {
		p.log.Warn("obsidian: file watcher failed to start", "error", err)
	} else {
		p.watcher = w
		if err := w.Start(ctx); err != nil {
			p.log.Warn("obsidian: watcher start failed", "error", err)
		}
	}
	return nil
}

func (p *Plugin) Stop() error {
	if p.watcher != nil {
		return p.watcher.Stop()
	}
	return nil
}

func (p *Plugin) HealthCheck(_ context.Context) plugin.HealthStatus {
	if _, err := os.Stat(p.cfg.VaultPath); err != nil {
		return plugin.HealthStatus{Status: plugin.StatusError, Message: "vault inaccessible: " + err.Error()}
	}
	if p.watcher == nil {
		return plugin.HealthStatus{Status: plugin.StatusDegraded, Message: "file watcher not running"}
	}
	return plugin.HealthStatus{Status: plugin.StatusOK}
}

func (p *Plugin) Transform(ctx context.Context, event plugin.Event, action string, params map[string]any) (plugin.Event, error) {
	switch action {
	case "search":
		return p.search(ctx, event, params)
	case "read":
		return p.read(ctx, event, params)
	case "query":
		return p.query(ctx, event, params)
	case "write_note":
		return p.writeNote(ctx, event, params)
	case "write_link":
		return p.writeLink(ctx, event, params)
	case "write_log":
		return p.writeLog(ctx, event, params)
	case "save_link":
		return p.saveLink(ctx, event, params)
	default:
		return event, fmt.Errorf("obsidian: unknown action %q", action)
	}
}

func (p *Plugin) search(_ context.Context, event plugin.Event, params map[string]any) (plugin.Event, error) {
	query, _ := event.Payload["message"].(string)
	if query == "" {
		return event, fmt.Errorf("obsidian search: missing message")
	}

	limit := 10
	if l, ok := params["limit"].(float64); ok {
		limit = int(l)
	}

	results, err := p.Search(query, limit)
	if err != nil {
		return event, fmt.Errorf("obsidian search: %w", err)
	}

	if len(results) == 0 {
		event.Payload["summary"] = "No results found."
		return event, nil
	}

	var sb strings.Builder
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. **%s** (`%s`)\n   %s\n", i+1, r.Title, r.Path, r.Excerpt)
	}
	event.Payload["summary"] = sb.String()
	return event, nil
}

func (p *Plugin) read(_ context.Context, event plugin.Event, params map[string]any) (plugin.Event, error) {
	path, _ := params["path"].(string)
	if path == "" {
		path, _ = event.Payload["message"].(string)
	}
	if path == "" {
		return event, fmt.Errorf("obsidian read: missing path")
	}

	// Ensure .md extension.
	if !strings.HasSuffix(path, ".md") {
		path += ".md"
	}

	absPath := filepath.Clean(filepath.Join(p.cfg.VaultPath, path))
	if !strings.HasPrefix(absPath, filepath.Clean(p.cfg.VaultPath)+string(filepath.Separator)) {
		return event, fmt.Errorf("obsidian read: path escapes vault")
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return event, fmt.Errorf("obsidian read: %w", err)
	}

	event.Payload["summary"] = string(data)
	return event, nil
}

func (p *Plugin) query(_ context.Context, event plugin.Event, params map[string]any) (plugin.Event, error) {
	dir, _ := params["dir"].(string)
	field, _ := params["field"].(string)
	withinDays := 0
	if d, ok := params["within_days"].(float64); ok {
		withinDays = int(d)
	}

	if dir == "" {
		return event, fmt.Errorf("obsidian query: missing dir param")
	}

	searchDir := filepath.Clean(filepath.Join(p.cfg.VaultPath, dir))
	if !strings.HasPrefix(searchDir, filepath.Clean(p.cfg.VaultPath)+string(filepath.Separator)) {
		return event, fmt.Errorf("obsidian query: dir escapes vault")
	}
	var matches []string

	err := filepath.WalkDir(searchDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		fields := ParseInlineFields(string(data))
		if field != "" {
			val, ok := fields[field]
			if !ok {
				return nil
			}
			if withinDays > 0 {
				if !isWithinDays(val, withinDays) {
					return nil
				}
			}
		}
		relPath, _ := filepath.Rel(p.cfg.VaultPath, path)
		matches = append(matches, relPath)
		return nil
	})
	if err != nil {
		return event, fmt.Errorf("obsidian query: %w", err)
	}

	if len(matches) == 0 {
		event.Payload["summary"] = "No matching files found."
		return event, nil
	}

	var sb strings.Builder
	for _, m := range matches {
		data, err := os.ReadFile(filepath.Join(p.cfg.VaultPath, m))
		if err != nil {
			continue
		}
		fields := ParseInlineFields(string(data))
		fmt.Fprintf(&sb, "- **%s**", m)
		if field != "" {
			if v, ok := fields[field]; ok {
				fmt.Fprintf(&sb, " (%s: %s)", field, v)
			}
		}
		sb.WriteString("\n")
	}
	event.Payload["summary"] = sb.String()
	return event, nil
}

// isWithinDays checks if a date string (YYYY-MM-DD) is within n days from now.
func isWithinDays(dateStr string, days int) bool {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(dateStr))
	if err != nil {
		return false
	}
	deadline := time.Now().AddDate(0, 0, days)
	return !t.After(deadline)
}
