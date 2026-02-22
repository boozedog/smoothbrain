package twitter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/boozedog/smoothbrain/internal/plugin"
	"github.com/google/uuid"
)

var tweetURLPattern = regexp.MustCompile(`(?:twitter\.com|x\.com)/\w+/status/(\d+)`)

type Config struct {
	BearerToken     string `json:"bearer_token"`
	BearerTokenFile string `json:"bearer_token_file"`
	ListID          string `json:"list_id"`
	QueryFilter     string `json:"query_filter"`
	PollInterval    string `json:"poll_interval"`
}

type Plugin struct {
	cfg           Config
	bearerToken   string
	pollInterval  time.Duration
	client        *http.Client
	log           *slog.Logger
	lastFetchOK   atomic.Bool
	lastFetchTime atomic.Int64
}

func New(log *slog.Logger) *Plugin {
	return &Plugin{
		client: &http.Client{Timeout: 30 * time.Second},
		log:    log,
	}
}

func (p *Plugin) Name() string { return "twitter" }

func (p *Plugin) Init(cfg json.RawMessage) error {
	p.cfg = Config{PollInterval: "60s"}
	if err := json.Unmarshal(cfg, &p.cfg); err != nil {
		return fmt.Errorf("twitter config: %w", err)
	}

	// Resolve bearer token.
	p.bearerToken = p.cfg.BearerToken
	if p.cfg.BearerTokenFile != "" {
		token, err := os.ReadFile(p.cfg.BearerTokenFile)
		if err != nil {
			return fmt.Errorf("reading twitter bearer token: %w", err)
		}
		p.bearerToken = strings.TrimSpace(string(token))
	}

	if p.cfg.ListID == "" && p.bearerToken == "" {
		p.log.Warn("twitter: no list_id or bearer_token configured, plugin will be idle")
	}

	dur, err := time.ParseDuration(p.cfg.PollInterval)
	if err != nil {
		return fmt.Errorf("twitter: invalid poll_interval %q: %w", p.cfg.PollInterval, err)
	}
	p.pollInterval = dur
	return nil
}

func (p *Plugin) Start(ctx context.Context, bus plugin.EventBus) error {
	if p.bearerToken == "" || p.cfg.ListID == "" {
		p.log.Warn("twitter: missing bearer_token or list_id, not starting poller")
		return nil
	}
	go p.poll(ctx, bus)
	return nil
}

func (p *Plugin) Stop() error { return nil }

// poll runs the ticker loop, fetching new tweets and emitting events.
func (p *Plugin) poll(ctx context.Context, bus plugin.EventBus) {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	var sinceID string

	// Do an initial poll immediately.
	p.log.Debug("twitter: starting poller", "list_id", p.cfg.ListID, "interval", p.pollInterval)
	sinceID = p.fetch(ctx, bus, sinceID)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sinceID = p.fetch(ctx, bus, sinceID)
		}
	}
}

// fetch calls the X API and emits events for each tweet. Returns the updated sinceID.
func (p *Plugin) fetch(ctx context.Context, bus plugin.EventBus, sinceID string) string {
	query := fmt.Sprintf("list:%s", p.cfg.ListID)
	if p.cfg.QueryFilter != "" {
		query += " " + p.cfg.QueryFilter
	}

	nextToken := ""
	newestID := sinceID

	for {
		params := url.Values{
			"query":        {query},
			"tweet.fields": {"created_at,public_metrics,author_id"},
			"user.fields":  {"username,name"},
			"expansions":   {"author_id"},
			"max_results":  {"100"},
		}
		if sinceID != "" {
			params.Set("since_id", sinceID)
		}
		if nextToken != "" {
			params.Set("next_token", nextToken)
		}

		reqURL := "https://api.x.com/2/tweets/search/recent?" + params.Encode()
		p.log.Debug("twitter: fetching", "query", query, "since_id", sinceID, "next_token", nextToken)
		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			p.log.Error("twitter: build request", "error", err)
			p.lastFetchOK.Store(false)
			p.lastFetchTime.Store(time.Now().UnixNano())
			return newestID
		}
		req.Header.Set("Authorization", "Bearer "+p.bearerToken)

		resp, err := p.client.Do(req) //nolint:gosec // URL is constructed from config, not user input
		if err != nil {
			p.log.Error("twitter: api request", "error", err)
			p.lastFetchOK.Store(false)
			p.lastFetchTime.Store(time.Now().UnixNano())
			return newestID
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			p.log.Error("twitter: api error", "status", resp.StatusCode, "body", string(body))
			p.lastFetchOK.Store(false)
			p.lastFetchTime.Store(time.Now().UnixNano())
			return newestID
		}

		p.log.Debug("twitter: api response", "status", resp.StatusCode, "bytes", len(body))

		var result searchResponse
		if err := json.Unmarshal(body, &result); err != nil {
			p.log.Error("twitter: parse response", "error", err)
			p.lastFetchOK.Store(false)
			p.lastFetchTime.Store(time.Now().UnixNano())
			return newestID
		}

		// Build author lookup map.
		users := make(map[string]user, len(result.Includes.Users))
		for _, u := range result.Includes.Users {
			users[u.ID] = u
		}

		for _, tw := range result.Data {
			author := users[tw.AuthorID]
			event := plugin.Event{
				ID:        uuid.NewString(),
				Source:    "twitter",
				Type:      "tweet",
				Timestamp: time.Now(),
				Payload: map[string]any{
					"tweet_id":         tw.ID,
					"text":             tw.Text,
					"author_id":        tw.AuthorID,
					"author_username":  author.Username,
					"author_name":      author.Name,
					"created_at":       tw.CreatedAt,
					"like_count":       tw.PublicMetrics.LikeCount,
					"retweet_count":    tw.PublicMetrics.RetweetCount,
					"reply_count":      tw.PublicMetrics.ReplyCount,
					"impression_count": tw.PublicMetrics.ImpressionCount,
					"url":              fmt.Sprintf("https://x.com/%s/status/%s", author.Username, tw.ID),
				},
			}
			p.log.Info("twitter: new tweet", "tweet_id", tw.ID, "author", author.Username)
			bus.Emit(event)
		}

		p.log.Debug("twitter: page results", "count", result.Meta.ResultCount, "newest_id", result.Meta.NewestID)

		// Track newest ID from the first page (API returns newest first).
		if result.Meta.NewestID != "" && newestID == sinceID {
			newestID = result.Meta.NewestID
		}

		if result.Meta.NextToken == "" {
			break
		}
		nextToken = result.Meta.NextToken
	}

	p.lastFetchOK.Store(true)
	p.lastFetchTime.Store(time.Now().UnixNano())
	return newestID
}

func (p *Plugin) HealthCheck(_ context.Context) plugin.HealthStatus {
	if p.bearerToken == "" {
		return plugin.HealthStatus{Status: plugin.StatusOK, Message: "not configured"}
	}
	if p.cfg.ListID == "" {
		return plugin.HealthStatus{Status: plugin.StatusOK, Message: "transform-only mode"}
	}
	lastNano := p.lastFetchTime.Load()
	if lastNano == 0 {
		return plugin.HealthStatus{Status: plugin.StatusOK, Message: "no polls yet"}
	}
	if !p.lastFetchOK.Load() {
		return plugin.HealthStatus{Status: plugin.StatusDegraded, Message: "last poll failed"}
	}
	lastTime := time.Unix(0, lastNano)
	if time.Since(lastTime) > 3*p.pollInterval {
		return plugin.HealthStatus{Status: plugin.StatusDegraded, Message: "no successful poll in 3x interval"}
	}
	return plugin.HealthStatus{Status: plugin.StatusOK}
}

// X API v2 response types.

type searchResponse struct {
	Data     []tweet  `json:"data"`
	Includes includes `json:"includes"`
	Meta     meta     `json:"meta"`
}

type tweet struct {
	ID            string        `json:"id"`
	Text          string        `json:"text"`
	AuthorID      string        `json:"author_id"`
	CreatedAt     string        `json:"created_at"`
	PublicMetrics publicMetrics `json:"public_metrics"`
}

type publicMetrics struct {
	LikeCount       int `json:"like_count"`
	RetweetCount    int `json:"retweet_count"`
	ReplyCount      int `json:"reply_count"`
	ImpressionCount int `json:"impression_count"`
}

type includes struct {
	Users []user `json:"users"`
}

type user struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

type meta struct {
	NewestID    string `json:"newest_id"`
	OldestID    string `json:"oldest_id"`
	ResultCount int    `json:"result_count"`
	NextToken   string `json:"next_token"`
}

// Transform enriches an event by fetching tweet data from the X API.
func (p *Plugin) Transform(ctx context.Context, event plugin.Event, action string, params map[string]any) (plugin.Event, error) {
	switch action {
	case "fetch_tweet":
		return p.fetchTweet(ctx, event)
	default:
		return event, fmt.Errorf("twitter: unknown action %q", action)
	}
}

func (p *Plugin) fetchTweet(ctx context.Context, event plugin.Event) (plugin.Event, error) {
	if p.bearerToken == "" {
		p.log.Warn("twitter: no bearer_token configured, skipping fetch_tweet")
		return event, nil
	}

	// Extract tweet ID from payload.
	tweetID, _ := event.Payload["tweet_id"].(string)
	if tweetID == "" {
		if rawURL, _ := event.Payload["url"].(string); rawURL != "" {
			if m := tweetURLPattern.FindStringSubmatch(rawURL); len(m) > 1 {
				tweetID = m[1]
			}
		}
	}
	if tweetID == "" {
		return event, fmt.Errorf("twitter: no tweet_id or recognizable tweet url in event payload")
	}

	params := url.Values{
		"tweet.fields": {"created_at,public_metrics,entities"},
		"user.fields":  {"username,name"},
		"expansions":   {"author_id"},
		"media.fields": {"url,preview_image_url"},
	}
	reqURL := fmt.Sprintf("https://api.x.com/2/tweets/%s?%s", tweetID, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return event, fmt.Errorf("twitter: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.bearerToken)

	resp, err := p.client.Do(req)
	if err != nil {
		return event, fmt.Errorf("twitter: api request: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		p.log.Warn("twitter: rate limited on fetch_tweet, skipping", "tweet_id", tweetID)
		return event, nil
	}
	if resp.StatusCode != http.StatusOK {
		return event, fmt.Errorf("twitter: api error %d: %s", resp.StatusCode, string(body))
	}

	var result tweetResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return event, fmt.Errorf("twitter: parse response: %w", err)
	}

	// Resolve author from includes.
	var author user
	for _, u := range result.Includes.Users {
		if u.ID == result.Data.AuthorID {
			author = u
			break
		}
	}

	// Collect embedded URLs from entities.
	var embeddedURLs []string
	if result.Data.Entities != nil {
		for _, e := range result.Data.Entities.URLs {
			if e.ExpandedURL != "" {
				embeddedURLs = append(embeddedURLs, e.ExpandedURL)
			}
		}
	}

	event.Payload["tweet_text"] = result.Data.Text
	event.Payload["tweet_created_at"] = result.Data.CreatedAt
	event.Payload["tweet_metrics"] = map[string]any{
		"like_count":       result.Data.PublicMetrics.LikeCount,
		"retweet_count":    result.Data.PublicMetrics.RetweetCount,
		"reply_count":      result.Data.PublicMetrics.ReplyCount,
		"impression_count": result.Data.PublicMetrics.ImpressionCount,
	}
	event.Payload["author_name"] = author.Name
	event.Payload["author_username"] = author.Username
	event.Payload["tweet_url"] = fmt.Sprintf("https://x.com/%s/status/%s", author.Username, tweetID)
	event.Payload["embedded_urls"] = embeddedURLs
	event.Payload["response"] = fmt.Sprintf("@%s: %s", author.Username, result.Data.Text)

	return event, nil
}

// Single-tweet API response types.

type tweetResponse struct {
	Data     tweetData `json:"data"`
	Includes includes  `json:"includes"`
}

type tweetData struct {
	ID            string         `json:"id"`
	Text          string         `json:"text"`
	AuthorID      string         `json:"author_id"`
	CreatedAt     string         `json:"created_at"`
	PublicMetrics publicMetrics  `json:"public_metrics"`
	Entities      *tweetEntities `json:"entities"`
}

type tweetEntities struct {
	URLs []tweetURLEntity `json:"urls"`
}

type tweetURLEntity struct {
	URL         string `json:"url"`
	ExpandedURL string `json:"expanded_url"`
	DisplayURL  string `json:"display_url"`
}
