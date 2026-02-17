package xai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/dmarx/smoothbrain/internal/plugin"
)

type Config struct {
	Model      string `json:"model"`
	APIKeyFile string `json:"api_key_file"`
}

type Plugin struct {
	cfg    Config
	apiKey string
	client *http.Client
	log    *slog.Logger
}

func New(log *slog.Logger) *Plugin {
	return &Plugin{
		client: &http.Client{},
		log:    log,
	}
}

func (p *Plugin) Name() string { return "xai" }

func (p *Plugin) Init(cfg json.RawMessage) error {
	p.cfg = Config{Model: "grok-3"}
	if err := json.Unmarshal(cfg, &p.cfg); err != nil {
		return fmt.Errorf("xai config: %w", err)
	}

	if p.cfg.APIKeyFile != "" {
		key, err := os.ReadFile(p.cfg.APIKeyFile)
		if err != nil {
			return fmt.Errorf("reading xai api key: %w", err)
		}
		p.apiKey = strings.TrimSpace(string(key))
	}
	return nil
}

func (p *Plugin) Start(ctx context.Context, bus plugin.EventBus) error {
	return nil
}

func (p *Plugin) Stop() error { return nil }

func (p *Plugin) Transform(ctx context.Context, event plugin.Event, action string, params map[string]any) (plugin.Event, error) {
	switch action {
	case "summarize":
		return p.summarize(ctx, event, params)
	default:
		return event, fmt.Errorf("xai: unknown action %q", action)
	}
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

func (p *Plugin) summarize(ctx context.Context, event plugin.Event, params map[string]any) (plugin.Event, error) {
	payloadJSON, err := json.Marshal(event.Payload)
	if err != nil {
		return event, fmt.Errorf("xai: marshal payload: %w", err)
	}

	prompt := "Summarize this alert concisely for a chat notification:"
	if custom, ok := params["prompt"].(string); ok {
		prompt = custom
	}

	reqBody := chatRequest{
		Model: p.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: prompt},
			{Role: "user", Content: string(payloadJSON)},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return event, fmt.Errorf("xai: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.x.ai/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return event, fmt.Errorf("xai request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return event, fmt.Errorf("xai api call: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return event, fmt.Errorf("xai api error %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return event, fmt.Errorf("xai parse response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return event, fmt.Errorf("xai: no choices in response")
	}

	event.Payload["summary"] = chatResp.Choices[0].Message.Content
	p.log.Info("xai summarize complete", "event_id", event.ID)
	return event, nil
}
