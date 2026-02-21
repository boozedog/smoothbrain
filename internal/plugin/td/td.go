package td

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dmarx/smoothbrain/internal/plugin"
	"github.com/google/uuid"
)

const maxBodySize = 1 << 20 // 1 MB

type Config struct {
	WebhookSecret     string `json:"webhook_secret"`
	WebhookSecretFile string `json:"webhook_secret_file"`
}

type Plugin struct {
	cfg Config
	log *slog.Logger
	bus plugin.EventBus

	nonceMu sync.Mutex
	nonces  map[string]time.Time // signature -> time seen
}

func New(log *slog.Logger) *Plugin {
	return &Plugin{
		log:    log,
		nonces: make(map[string]time.Time),
	}
}

func (p *Plugin) Name() string { return "td" }

func (p *Plugin) Init(cfg json.RawMessage) error {
	if err := json.Unmarshal(cfg, &p.cfg); err != nil {
		return fmt.Errorf("td config: %w", err)
	}

	if p.cfg.WebhookSecretFile != "" {
		secret, err := os.ReadFile(p.cfg.WebhookSecretFile)
		if err != nil {
			return fmt.Errorf("reading td webhook secret: %w", err)
		}
		p.cfg.WebhookSecret = strings.TrimSpace(string(secret))
	}
	return nil
}

func (p *Plugin) Start(ctx context.Context, bus plugin.EventBus) error {
	p.bus = bus
	return nil
}

func (p *Plugin) Stop() error { return nil }

// RegisterWebhook sets up the POST /hooks/td endpoint.
func (p *Plugin) RegisterWebhook(reg plugin.WebhookRegistrar) {
	reg.RegisterWebhook("td", p.handleWebhook)
}

func (p *Plugin) handleWebhook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if p.cfg.WebhookSecret != "" {
		ts := r.Header.Get("X-TD-Timestamp")
		sig := r.Header.Get("X-TD-Signature")
		if !verifySignature(p.cfg.WebhookSecret, ts, body, sig) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !isTimestampFresh(ts, 5*time.Minute) {
			http.Error(w, "unauthorized: timestamp too old", http.StatusUnauthorized)
			return
		}
		if p.isReplayedNonce(sig) {
			http.Error(w, "unauthorized: replayed request", http.StatusUnauthorized)
			return
		}
	}

	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "bad request: invalid JSON", http.StatusBadRequest)
		return
	}

	eventType := "unknown"
	if len(payload.Actions) > 0 {
		eventType = payload.Actions[0].ActionType
	}

	// Build the event payload as a generic map.
	var payloadMap map[string]any
	if err := json.Unmarshal(body, &payloadMap); err != nil {
		payloadMap = map[string]any{"raw": string(body)}
	}
	payloadMap["summary"] = buildSummary(payload)

	event := plugin.Event{
		ID:        uuid.NewString(),
		Source:    "td",
		Type:      eventType,
		Payload:   payloadMap,
		Timestamp: time.Now(),
	}

	p.log.Info("td webhook received", "event_id", event.ID, "type", eventType, "actions", len(payload.Actions))
	p.bus.Emit(event)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "accepted", "event_id": event.ID}); err != nil {
		p.log.Error("td: encode response", "error", err)
	}
}

// verifySignature checks the HMAC-SHA256 signature: HMAC(secret, timestamp + "." + body).
func verifySignature(secret, timestamp string, body []byte, signature string) bool {
	if signature == "" || timestamp == "" {
		return false
	}

	sig := strings.TrimPrefix(signature, "sha256=")
	expected, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)

	return hmac.Equal(mac.Sum(nil), expected)
}

// isTimestampFresh returns true if the timestamp is within maxAge of now.
func isTimestampFresh(ts string, maxAge time.Duration) bool {
	// Try RFC3339 first.
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		// Try Unix epoch (seconds as string).
		sec, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			return false
		}
		t = time.Unix(sec, 0)
	}
	diff := time.Since(t)
	if diff < 0 {
		diff = -diff
	}
	return diff <= maxAge
}

const nonceWindow = 5*time.Minute + 30*time.Second

// isReplayedNonce returns true if the signature was already seen within the
// replay window. It evicts expired entries on each call.
func (p *Plugin) isReplayedNonce(sig string) bool {
	now := time.Now()

	p.nonceMu.Lock()
	defer p.nonceMu.Unlock()

	// Evict expired nonces.
	for k, seen := range p.nonces {
		if now.Sub(seen) > nonceWindow {
			delete(p.nonces, k)
		}
	}

	if _, exists := p.nonces[sig]; exists {
		return true
	}
	p.nonces[sig] = now
	return false
}

type webhookPayload struct {
	ProjectDir string   `json:"project_dir"`
	Timestamp  string   `json:"timestamp"`
	Actions    []action `json:"actions"`
}

type action struct {
	ID           string `json:"id"`
	SessionID    string `json:"session_id"`
	ActionType   string `json:"action_type"`
	EntityType   string `json:"entity_type"`
	EntityID     string `json:"entity_id"`
	PreviousData string `json:"previous_data"`
	NewData      string `json:"new_data"`
	Timestamp    string `json:"timestamp"`
}

func buildSummary(p webhookPayload) string {
	project := filepath.Base(p.ProjectDir)
	if project == "" || project == "." {
		project = "unknown"
	}

	var lines []string
	for _, a := range p.Actions {
		line := formatAction(project, a)
		if line != "" {
			lines = append(lines, line)
		}
	}

	if len(lines) == 0 {
		return fmt.Sprintf("[%s] td event", project)
	}
	return strings.Join(lines, "\n")
}

func formatAction(project string, a action) string {
	title, priority, kind := parseEntityData(a.NewData)
	shortID := a.EntityID

	switch a.ActionType {
	case "create":
		desc := formatTitle(title, kind, priority)
		return fmt.Sprintf("[%s] created %s %s%s", project, a.EntityType, shortID, desc)
	case "close":
		desc := formatTitle(title, "", "")
		return fmt.Sprintf("[%s] closed %s %s%s", project, a.EntityType, shortID, desc)
	case "update":
		desc := formatTitle(title, "", "")
		return fmt.Sprintf("[%s] updated %s %s%s", project, a.EntityType, shortID, desc)
	case "handoff":
		return fmt.Sprintf("[%s] handoff on %s", project, shortID)
	default:
		return fmt.Sprintf("[%s] %s %s %s", project, a.ActionType, a.EntityType, shortID)
	}
}

func formatTitle(title, kind, priority string) string {
	if title == "" {
		return ""
	}
	s := fmt.Sprintf(": %q", title)
	var tags []string
	if kind != "" {
		tags = append(tags, kind)
	}
	if priority != "" {
		tags = append(tags, priority)
	}
	if len(tags) > 0 {
		s += " (" + strings.Join(tags, ", ") + ")"
	}
	return s
}

// parseEntityData extracts title, priority, and kind from the JSON snapshot in new_data.
func parseEntityData(data string) (title, priority, kind string) {
	if data == "" {
		return "", "", ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return "", "", ""
	}
	if v, ok := m["title"].(string); ok {
		title = v
	}
	if v, ok := m["priority"].(string); ok {
		priority = v
	}
	if v, ok := m["kind"].(string); ok {
		kind = v
	}
	return title, priority, kind
}
