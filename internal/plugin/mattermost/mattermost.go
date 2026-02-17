package mattermost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/dmarx/smoothbrain/internal/plugin"
)

type Config struct {
	URL       string `json:"url"`
	TokenFile string `json:"token_file"`
}

type Plugin struct {
	cfg    Config
	token  string
	client *http.Client
	log    *slog.Logger
}

func New(log *slog.Logger) *Plugin {
	return &Plugin{
		client: &http.Client{},
		log:    log,
	}
}

func (p *Plugin) Name() string { return "mattermost" }

func (p *Plugin) Init(cfg json.RawMessage) error {
	if err := json.Unmarshal(cfg, &p.cfg); err != nil {
		return fmt.Errorf("mattermost config: %w", err)
	}

	if p.cfg.TokenFile != "" {
		token, err := os.ReadFile(p.cfg.TokenFile)
		if err != nil {
			return fmt.Errorf("reading mattermost token: %w", err)
		}
		p.token = strings.TrimSpace(string(token))
	}
	return nil
}

func (p *Plugin) Start(ctx context.Context, bus plugin.EventBus) error {
	return nil
}

func (p *Plugin) Stop() error { return nil }

func (p *Plugin) HandleEvent(ctx context.Context, event plugin.Event) error {
	channel, _ := event.Payload["channel"].(string)
	if channel == "" {
		return fmt.Errorf("mattermost: no channel in event payload")
	}

	// Use summary if available, otherwise format payload.
	message := formatMessage(event)

	body, err := json.Marshal(map[string]string{
		"channel_id": channel,
		"message":    message,
	})
	if err != nil {
		return fmt.Errorf("mattermost: marshal post: %w", err)
	}

	postURL, err := url.JoinPath(p.cfg.URL, "/api/v4/posts")
	if err != nil {
		return fmt.Errorf("mattermost: build url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", postURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mattermost request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("mattermost api call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mattermost api error %d: %s", resp.StatusCode, string(respBody))
	}

	p.log.Info("mattermost message sent", "channel", channel, "event_id", event.ID)
	return nil
}

func formatMessage(event plugin.Event) string {
	if summary, ok := event.Payload["summary"].(string); ok {
		return fmt.Sprintf("**[%s]** %s", event.Source, summary)
	}

	payload, err := json.MarshalIndent(event.Payload, "", "  ")
	if err != nil {
		return fmt.Sprintf("**[%s/%s]** (failed to format payload)", event.Source, event.Type)
	}
	return fmt.Sprintf("**[%s/%s]**\n```json\n%s\n```", event.Source, event.Type, string(payload))
}
