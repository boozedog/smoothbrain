package uptimekuma

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/boozedog/smoothbrain/internal/plugin"
	"github.com/google/uuid"
)

const maxBodySize = 1 << 20 // 1 MB

type Config struct {
	WebhookToken     string `json:"webhook_token"`
	WebhookTokenFile string `json:"webhook_token_file"`
}

type Plugin struct {
	cfg Config
	log *slog.Logger
	bus plugin.EventBus
}

func New(log *slog.Logger) *Plugin {
	return &Plugin{log: log}
}

func (p *Plugin) Name() string { return "uptime-kuma" }

func (p *Plugin) Init(cfg json.RawMessage) error {
	if err := json.Unmarshal(cfg, &p.cfg); err != nil {
		return fmt.Errorf("uptime-kuma config: %w", err)
	}

	if p.cfg.WebhookTokenFile != "" {
		token, err := os.ReadFile(p.cfg.WebhookTokenFile)
		if err != nil {
			return fmt.Errorf("reading uptime-kuma webhook token: %w", err)
		}
		p.cfg.WebhookToken = strings.TrimSpace(string(token))
	}
	return nil
}

func (p *Plugin) Start(ctx context.Context, bus plugin.EventBus) error {
	p.bus = bus
	return nil
}

func (p *Plugin) Stop() error { return nil }

// RegisterWebhook sets up the POST /hooks/uptime-kuma endpoint.
func (p *Plugin) RegisterWebhook(reg plugin.WebhookRegistrar) {
	reg.RegisterWebhook("uptime-kuma", p.handleWebhook)
}

func (p *Plugin) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if p.cfg.WebhookToken != "" {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Webhook-Token")), []byte(p.cfg.WebhookToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		// If not JSON, wrap raw body.
		payload = map[string]any{"raw": string(body)}
	}

	event := plugin.Event{
		ID:        uuid.NewString(),
		Source:    "uptime-kuma",
		Type:      "alert",
		Payload:   payload,
		Timestamp: time.Now(),
	}

	p.log.Info("uptime-kuma webhook received", "event_id", event.ID)
	p.bus.Emit(event)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "accepted", "event_id": event.ID}); err != nil {
		p.log.Error("uptime-kuma: encode response", "error", err)
	}
}
