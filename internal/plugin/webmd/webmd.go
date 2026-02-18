package webmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/dmarx/smoothbrain/internal/plugin"
)

const defaultEndpoint = "https://webmd.booze.dog/"

type Config struct {
	Endpoint string `json:"endpoint"`
}

type Plugin struct {
	cfg    Config
	client *http.Client
	log    *slog.Logger
}

func New(log *slog.Logger) *Plugin {
	return &Plugin{
		client: &http.Client{},
		log:    log,
	}
}

func (p *Plugin) Name() string { return "webmd" }

func (p *Plugin) Init(cfg json.RawMessage) error {
	p.cfg.Endpoint = defaultEndpoint
	if cfg != nil {
		if err := json.Unmarshal(cfg, &p.cfg); err != nil {
			return fmt.Errorf("webmd config: %w", err)
		}
	}
	return nil
}

func (p *Plugin) Start(ctx context.Context, bus plugin.EventBus) error { return nil }
func (p *Plugin) Stop() error                                          { return nil }

func (p *Plugin) Transform(ctx context.Context, event plugin.Event, action string, params map[string]any) (plugin.Event, error) {
	switch action {
	case "fetch":
		return p.fetch(ctx, event)
	default:
		return event, fmt.Errorf("webmd: unknown action %q", action)
	}
}

func (p *Plugin) fetch(ctx context.Context, event plugin.Event) (plugin.Event, error) {
	rawURL, _ := event.Payload["message"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return event, fmt.Errorf("webmd: no URL provided")
	}

	// Ensure the URL has a scheme.
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return event, fmt.Errorf("webmd: invalid URL: %w", err)
	}
	if parsed.Host == "" {
		return event, fmt.Errorf("webmd: invalid URL %q: no host", rawURL)
	}

	endpoint := fmt.Sprintf("%s?url=%s", strings.TrimRight(p.cfg.Endpoint, "/"), url.QueryEscape(rawURL))
	p.log.Info("webmd: fetching", "url", rawURL, "endpoint", endpoint)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return event, fmt.Errorf("webmd: build request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return event, fmt.Errorf("webmd: fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return event, fmt.Errorf("webmd: HTTP %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return event, fmt.Errorf("webmd: read body: %w", err)
	}

	md := strings.TrimSpace(string(body))

	// Derive a filename from the URL.
	filename := parsed.Host + ".md"
	if filename == ".md" {
		filename = "page.md"
	}

	event.Payload["summary"] = fmt.Sprintf("Fetched [%s](%s)", rawURL, rawURL)
	event.Payload["file_content"] = md
	event.Payload["file_name"] = filename

	p.log.Info("webmd: fetched", "url", rawURL, "bytes", len(md))
	return event, nil
}
