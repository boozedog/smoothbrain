package claudecode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/boozedog/smoothbrain/internal/plugin"
	"github.com/boozedog/smoothbrain/pkg/claudecode"
)

// SourceConfig holds per-source access control settings.
type SourceConfig struct {
	AllowedUsers      []string          `json:"allowed_users,omitempty"`
	ChannelWorkspaces map[string]string `json:"channel_workspaces,omitempty"`
}

// WorkspaceConfig holds per-workspace settings.
type WorkspaceConfig struct {
	Path               string `json:"path"`
	Tools              string `json:"tools,omitempty"`
	AppendSystemPrompt string `json:"append_system_prompt,omitempty"`
}

// Config holds the plugin configuration.
type Config struct {
	Binary         string                     `json:"binary,omitempty"`
	Model          string                     `json:"model,omitempty"`
	PermissionMode string                     `json:"permission_mode,omitempty"`
	SessionTTL     string                     `json:"session_ttl,omitempty"`
	WireLog        bool                       `json:"wire_log,omitempty"`
	Workspaces     map[string]WorkspaceConfig `json:"workspaces,omitempty"`
	Sources        map[string]SourceConfig    `json:"sources,omitempty"`
	MaxTurns       int                        `json:"max_turns,omitempty"`
}

// Stats tracks cumulative usage across requests.
type Stats struct {
	TotalRequests int     `json:"total_requests"`
	TotalTokens   int     `json:"total_tokens"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
}

type sessionEntry struct {
	SessionID string
	LastUsed  time.Time
}

// Plugin implements Transform, HealthChecker, and StoreAware.
type Plugin struct {
	cfg        Config
	log        *slog.Logger
	bus        plugin.EventBus
	db         *sql.DB
	sessions   map[string]sessionEntry
	mu         sync.Mutex
	stats      Stats
	sessionTTL time.Duration
}

// New creates a new claudecode plugin instance.
func New(log *slog.Logger) *Plugin {
	return &Plugin{
		log:      log,
		sessions: make(map[string]sessionEntry),
	}
}

func (p *Plugin) Name() string { return "claudecode" }

func (p *Plugin) Init(cfg json.RawMessage) error {
	if cfg != nil {
		if err := json.Unmarshal(cfg, &p.cfg); err != nil {
			return fmt.Errorf("claudecode config: %w", err)
		}
	}

	// Parse session TTL (default 1h).
	ttlStr := p.cfg.SessionTTL
	if ttlStr == "" {
		ttlStr = "1h"
	}
	ttl, err := time.ParseDuration(ttlStr)
	if err != nil {
		return fmt.Errorf("claudecode: invalid session_ttl %q: %w", ttlStr, err)
	}
	p.sessionTTL = ttl

	// Load persisted state from store if available.
	if p.db != nil {
		p.loadStats()
		p.loadSessions()
	}

	return nil
}

func (p *Plugin) Start(_ context.Context, bus plugin.EventBus) error {
	p.bus = bus
	if p.cfg.WireLog {
		claudecode.SetWireLogEnabled(true)
	}
	return nil
}

func (p *Plugin) Stop() error {
	if p.db != nil {
		p.persistStats()
	}
	return nil
}

// SetStore implements plugin.StoreAware.
func (p *Plugin) SetStore(db *sql.DB) {
	p.db = db
}

// HealthCheck implements plugin.HealthChecker.
func (p *Plugin) HealthCheck(_ context.Context) plugin.HealthStatus {
	p.mu.Lock()
	sessionCount := len(p.sessions)
	stats := p.stats
	p.mu.Unlock()

	msg := fmt.Sprintf("$%.2f | %s tokens | %d reqs | %d sessions",
		stats.TotalCostUSD,
		claudecode.FormatTokens(stats.TotalTokens),
		stats.TotalRequests,
		sessionCount,
	)
	return plugin.HealthStatus{Status: plugin.StatusOK, Message: msg}
}

func (p *Plugin) Transform(ctx context.Context, event plugin.Event, action string, params map[string]any) (plugin.Event, error) {
	if err := p.checkAccess(event); err != nil {
		return event, err
	}
	switch action {
	case "ask":
		return p.ask(ctx, event, params)
	case "chat":
		return p.chat(ctx, event, params)
	default:
		return event, fmt.Errorf("claudecode: unknown action %q", action)
	}
}

func (p *Plugin) checkAccess(event plugin.Event) error {
	// Source firewall: if sources is configured, the source must have a key.
	if len(p.cfg.Sources) > 0 {
		src, ok := p.cfg.Sources[event.Source]
		if !ok {
			return &plugin.AccessDeniedError{Reason: fmt.Sprintf("claudecode: source %q not allowed", event.Source)}
		}
		// User firewall: per-source allowed_users.
		if len(src.AllowedUsers) > 0 {
			userID, _ := event.Payload["user_id"].(string)
			if !slices.Contains(src.AllowedUsers, userID) {
				return &plugin.AccessDeniedError{Reason: fmt.Sprintf("claudecode: user %q not allowed", userID)}
			}
		}
		// Channel firewall: per-source channel_workspaces.
		if len(src.ChannelWorkspaces) > 0 {
			if chID, ok := event.Payload["channel_id"].(string); ok && chID != "" {
				if _, mapped := src.ChannelWorkspaces[chID]; !mapped {
					return &plugin.AccessDeniedError{Reason: fmt.Sprintf("claudecode: channel %q not in allowed channels", chID)}
				}
			}
		}
	}
	return nil
}

func (p *Plugin) ask(ctx context.Context, event plugin.Event, params map[string]any) (plugin.Event, error) {
	message, _ := event.Payload["message"].(string)
	if message == "" {
		return event, fmt.Errorf("claudecode: no message in payload")
	}

	opts, err := p.buildOpts(event, params)
	if err != nil {
		return event, err
	}

	p.log.Info("claudecode: running ask", "message", message)

	ch, _, err := claudecode.Stream(message, opts)
	if err != nil {
		return event, fmt.Errorf("claudecode: stream: %w", err)
	}

	resp, err := p.drainStream(ctx, ch, event)
	if err != nil {
		return event, err
	}

	p.updateStats(resp.Result)
	event.Payload["response"] = resp.AssistantText()
	p.log.Info("claudecode: ask complete", "event_id", event.ID)
	return event, nil
}

func (p *Plugin) chat(ctx context.Context, event plugin.Event, params map[string]any) (plugin.Event, error) {
	message, _ := event.Payload["message"].(string)
	if message == "" {
		return event, fmt.Errorf("claudecode: no message in payload")
	}

	// Derive session key from thread context:
	//   - Thread reply (root_id set): use root_id (all replies share one session)
	//   - New top-level post: use post_id (becomes root_id for future replies)
	//   - Fallback: explicit param > session_key_field > event source
	rootID, _ := event.Payload["root_id"].(string)
	postID, _ := event.Payload["post_id"].(string)

	var sessionKey string
	if rootID != "" {
		sessionKey = rootID
	} else if postID != "" {
		sessionKey = postID
	} else {
		sessionKey, _ = params["session_key"].(string)
		if sessionKey == "" {
			if field, ok := params["session_key_field"].(string); ok && field != "" {
				if val, ok := event.Payload[field].(string); ok && val != "" {
					sessionKey = val
				}
			}
		}
		if sessionKey == "" {
			sessionKey = event.Source
		}
	}

	opts, err := p.buildOpts(event, params)
	if err != nil {
		return event, err
	}

	// Only resume a session for thread replies (root_id present).
	// New top-level posts always start a fresh conversation.
	if rootID != "" {
		p.mu.Lock()
		if entry, ok := p.sessions[sessionKey]; ok {
			if time.Since(entry.LastUsed) < p.sessionTTL {
				opts.SessionID = entry.SessionID
			} else {
				delete(p.sessions, sessionKey)
			}
		}
		p.mu.Unlock()
	}

	p.log.Info("claudecode: running chat", "message", message, "session_key", sessionKey, "resume", opts.SessionID != "")

	ch, _, err := claudecode.Stream(message, opts)
	if err != nil {
		return event, fmt.Errorf("claudecode: stream: %w", err)
	}

	resp, err := p.drainStream(ctx, ch, event)
	if err != nil {
		return event, err
	}

	p.updateStats(resp.Result)

	// Cache session for reuse.
	if resp.Result.SessionID != "" {
		p.mu.Lock()
		p.sessions[sessionKey] = sessionEntry{
			SessionID: resp.Result.SessionID,
			LastUsed:  time.Now(),
		}
		p.mu.Unlock()

		if p.db != nil {
			p.persistSession(sessionKey, resp.Result.SessionID)
		}
	}

	event.Payload["response"] = resp.AssistantText()
	p.log.Info("claudecode: chat complete", "event_id", event.ID, "session_key", sessionKey)
	return event, nil
}

// buildOpts constructs claudecode.Options from config and per-request params.
func (p *Plugin) buildOpts(event plugin.Event, params map[string]any) (claudecode.Options, error) {
	opts := claudecode.Options{
		Binary: p.cfg.Binary,
		Model:  p.cfg.Model,
	}

	// Resolve workspace: explicit param > channel_workspaces mapping.
	var ws WorkspaceConfig
	if wsName, ok := params["workspace"].(string); ok && wsName != "" {
		found, exists := p.cfg.Workspaces[wsName]
		if !exists {
			return opts, fmt.Errorf("claudecode: unknown workspace %q", wsName)
		}
		ws = found
	} else if channelID, ok := event.Payload["channel_id"].(string); ok && channelID != "" {
		if src, ok := p.cfg.Sources[event.Source]; ok {
			if wsName, ok := src.ChannelWorkspaces[channelID]; ok {
				if found, exists := p.cfg.Workspaces[wsName]; exists {
					ws = found
				}
			}
		}
	}
	opts.CWD = ws.Path
	opts.Tools = ws.Tools

	// Permission mode: param > config > default "plan".
	if pm, ok := params["permission_mode"].(string); ok && pm != "" {
		opts.PermissionMode = pm
	} else if p.cfg.PermissionMode != "" {
		opts.PermissionMode = p.cfg.PermissionMode
	} else {
		opts.PermissionMode = "plan"
	}

	// Hard-coded flags: always set in this application.
	opts.DisableSlashCommands = true
	opts.NoChrome = true
	opts.SystemPrompt = "Do not read or modify anything outside the current directory and its subdirectories."

	// Config-driven flags.
	opts.MaxTurns = p.cfg.MaxTurns

	// Append system prompt: route param > workspace default.
	if sp, ok := params["system_prompt"].(string); ok && sp != "" {
		opts.AppendSystemPrompt = sp
	} else if ws.AppendSystemPrompt != "" {
		opts.AppendSystemPrompt = ws.AppendSystemPrompt
	}

	return opts, nil
}

// drainStream reads all messages from the stream channel, emitting deltas to
// the event bus. Returns the final Response or an error.
func (p *Plugin) drainStream(_ context.Context, ch <-chan claudecode.StreamMsg, event plugin.Event) (*claudecode.Response, error) {
	for msg := range ch {
		if msg.Event != nil {
			delta := claudecode.ExtractDeltas(msg.Event.Raw)
			if delta.Text != "" {
				p.bus.Emit(plugin.Event{
					Source: "claudecode",
					Type:   "stream",
					Payload: map[string]any{
						"text_delta": delta.Text,
						"event_id":   event.ID,
					},
				})
			}
		}
		if msg.Done {
			if msg.Err != nil {
				return nil, fmt.Errorf("claudecode: %w", msg.Err)
			}
			return msg.Response, nil
		}
	}
	return nil, fmt.Errorf("claudecode: stream closed without done message")
}

// updateStats adds result metrics to cumulative stats.
func (p *Plugin) updateStats(result claudecode.Result) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stats.TotalRequests++
	p.stats.TotalTokens += result.Usage.InputTokens + result.Usage.OutputTokens
	p.stats.TotalCostUSD += result.CostUSD
}

// persistStats writes cumulative stats to plugin_state.
func (p *Plugin) persistStats() {
	p.mu.Lock()
	data, err := json.Marshal(p.stats)
	p.mu.Unlock()
	if err != nil {
		p.log.Warn("claudecode: failed to marshal stats", "error", err)
		return
	}
	_, err = p.db.Exec(
		`INSERT OR REPLACE INTO plugin_state (plugin, key, value, updated_at) VALUES ('claudecode', 'stats', ?, CURRENT_TIMESTAMP)`,
		string(data),
	)
	if err != nil {
		p.log.Warn("claudecode: failed to persist stats", "error", err)
	}
}

// loadStats restores cumulative stats from plugin_state.
func (p *Plugin) loadStats() {
	var raw string
	err := p.db.QueryRow(`SELECT value FROM plugin_state WHERE plugin = 'claudecode' AND key = 'stats'`).Scan(&raw)
	if err != nil {
		return // no saved stats
	}
	if err := json.Unmarshal([]byte(raw), &p.stats); err != nil {
		p.log.Warn("claudecode: failed to parse saved stats", "error", err)
	}
}

// loadSessions restores persisted sessions from plugin_state.
func (p *Plugin) loadSessions() {
	rows, err := p.db.Query(`SELECT key, value FROM plugin_state WHERE plugin = 'claudecode' AND key LIKE 'session:%'`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var key, sessionID string
		if err := rows.Scan(&key, &sessionID); err != nil {
			continue
		}
		// key is "session:<sessionKey>", strip prefix.
		sessionKey := key[len("session:"):]
		p.sessions[sessionKey] = sessionEntry{
			SessionID: sessionID,
			LastUsed:  time.Now(), // treat loaded sessions as fresh
		}
	}
}

// persistSession writes a session mapping to plugin_state.
func (p *Plugin) persistSession(sessionKey, sessionID string) {
	_, err := p.db.Exec(
		`INSERT OR REPLACE INTO plugin_state (plugin, key, value, updated_at) VALUES ('claudecode', ?, ?, CURRENT_TIMESTAMP)`,
		"session:"+sessionKey, sessionID,
	)
	if err != nil {
		p.log.Warn("claudecode: failed to persist session", "session_key", sessionKey, "error", err)
	}
}

// WorkspaceChannels implements plugin.WorkspaceChannelProvider.
func (p *Plugin) WorkspaceChannels() []string {
	var channels []string
	for _, src := range p.cfg.Sources {
		for ch := range src.ChannelWorkspaces {
			channels = append(channels, ch)
		}
	}
	return channels
}
