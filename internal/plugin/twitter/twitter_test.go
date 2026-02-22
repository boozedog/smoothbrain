package twitter

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/boozedog/smoothbrain/internal/plugin"
)

func newTestPlugin(t *testing.T, bearerToken string) *Plugin {
	t.Helper()
	p := New(slog.Default())
	p.bearerToken = bearerToken
	p.cfg.ListID = ""
	return p
}

func testEvent(payload map[string]any) plugin.Event {
	return plugin.Event{
		ID:        "test-1",
		Source:    "test",
		Type:      "link",
		Timestamp: time.Now(),
		Payload:   payload,
	}
}

// validTweetResponse returns a realistic single-tweet API response body.
func validTweetResponse() tweetResponse {
	return tweetResponse{
		Data: tweetData{
			ID:        "1234567890",
			Text:      "Hello world from X!",
			AuthorID:  "user-42",
			CreatedAt: "2026-02-20T12:00:00.000Z",
			PublicMetrics: publicMetrics{
				LikeCount:       10,
				RetweetCount:    3,
				ReplyCount:      1,
				ImpressionCount: 500,
			},
			Entities: &tweetEntities{
				URLs: []tweetURLEntity{
					{URL: "https://t.co/abc", ExpandedURL: "https://example.com/article", DisplayURL: "example.com/article"},
				},
			},
		},
		Includes: includes{
			Users: []user{
				{ID: "user-42", Username: "testuser", Name: "Test User"},
			},
		},
	}
}

func TestFetchTweet_Success(t *testing.T) {
	resp := validTweetResponse()
	body, _ := json.Marshal(resp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	p := newTestPlugin(t, "test-token")
	p.client = srv.Client()

	// Patch the fetchTweet to use our test server URL.
	// We'll test via Transform to also cover the action routing.
	event := testEvent(map[string]any{
		"tweet_id": "1234567890",
		"url":      srv.URL, // not used since tweet_id is present
	})

	// We need to override the API URL — use a custom transport.
	p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		// Rewrite the request to point at our test server.
		req.URL.Scheme = "http"
		req.URL.Host = srv.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(req)
	})

	result, err := p.Transform(context.Background(), event, "fetch_tweet", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := result.Payload["tweet_text"]; got != "Hello world from X!" {
		t.Errorf("tweet_text = %q, want %q", got, "Hello world from X!")
	}
	if got := result.Payload["author_username"]; got != "testuser" {
		t.Errorf("author_username = %q, want %q", got, "testuser")
	}
	if got := result.Payload["author_name"]; got != "Test User" {
		t.Errorf("author_name = %q, want %q", got, "Test User")
	}
	if got := result.Payload["tweet_url"]; got != "https://x.com/testuser/status/1234567890" {
		t.Errorf("tweet_url = %q, want %q", got, "https://x.com/testuser/status/1234567890")
	}
	if got := result.Payload["tweet_created_at"]; got != "2026-02-20T12:00:00.000Z" {
		t.Errorf("tweet_created_at = %q, want %q", got, "2026-02-20T12:00:00.000Z")
	}
	if got := result.Payload["response"]; got != "@testuser: Hello world from X!" {
		t.Errorf("response = %q, want %q", got, "@testuser: Hello world from X!")
	}

	// Check metrics.
	metrics, ok := result.Payload["tweet_metrics"].(map[string]any)
	if !ok {
		t.Fatal("tweet_metrics is not map[string]any")
	}
	if metrics["like_count"] != 10 {
		t.Errorf("like_count = %v, want 10", metrics["like_count"])
	}

	// Check embedded URLs.
	urls, ok := result.Payload["embedded_urls"].([]string)
	if !ok {
		t.Fatalf("embedded_urls has type %T, want []string", result.Payload["embedded_urls"])
	}
	if len(urls) != 1 || urls[0] != "https://example.com/article" {
		t.Errorf("embedded_urls = %v, want [https://example.com/article]", urls)
	}
}

func TestFetchTweet_MissingTweetID(t *testing.T) {
	p := newTestPlugin(t, "test-token")

	event := testEvent(map[string]any{
		"some_other_field": "value",
	})

	_, err := p.Transform(context.Background(), event, "fetch_tweet", nil)
	if err == nil {
		t.Fatal("expected error for missing tweet_id, got nil")
	}
}

func TestFetchTweet_NoBearerToken(t *testing.T) {
	p := newTestPlugin(t, "")

	event := testEvent(map[string]any{
		"tweet_id": "1234567890",
	})

	result, err := p.Transform(context.Background(), event, "fetch_tweet", nil)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	// Should return event unchanged — no tweet_text added.
	if _, exists := result.Payload["tweet_text"]; exists {
		t.Error("expected event to be returned unchanged when no bearer token")
	}
}

func TestFetchTweet_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p := newTestPlugin(t, "test-token")
	p.client = srv.Client()
	p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = srv.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(req)
	})

	event := testEvent(map[string]any{
		"tweet_id": "1234567890",
	})

	result, err := p.Transform(context.Background(), event, "fetch_tweet", nil)
	if err != nil {
		t.Fatalf("expected no error on rate limit, got: %v", err)
	}
	if _, exists := result.Payload["tweet_text"]; exists {
		t.Error("expected event unchanged on rate limit")
	}
}

func TestFetchTweet_URLFallback(t *testing.T) {
	resp := validTweetResponse()
	body, _ := json.Marshal(resp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	p := newTestPlugin(t, "test-token")
	p.client = srv.Client()
	p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = srv.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(req)
	})

	event := testEvent(map[string]any{
		"url": "https://x.com/someuser/status/1234567890",
	})

	result, err := p.Transform(context.Background(), event, "fetch_tweet", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := result.Payload["tweet_text"]; got != "Hello world from X!" {
		t.Errorf("tweet_text = %q, want %q", got, "Hello world from X!")
	}
}

func TestFetchTweet_URLFallbackTwitterDomain(t *testing.T) {
	resp := validTweetResponse()
	body, _ := json.Marshal(resp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	p := newTestPlugin(t, "test-token")
	p.client = srv.Client()
	p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = srv.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(req)
	})

	event := testEvent(map[string]any{
		"url": "https://twitter.com/someuser/status/1234567890",
	})

	result, err := p.Transform(context.Background(), event, "fetch_tweet", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := result.Payload["tweet_text"]; got != "Hello world from X!" {
		t.Errorf("tweet_text = %q, want %q", got, "Hello world from X!")
	}
}

func TestTransform_UnknownAction(t *testing.T) {
	p := newTestPlugin(t, "test-token")
	event := testEvent(map[string]any{})

	_, err := p.Transform(context.Background(), event, "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestInit_BearerTokenOnly(t *testing.T) {
	p := New(slog.Default())
	cfg := `{"bearer_token": "tok123"}`
	if err := p.Init(json.RawMessage(cfg)); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	// Should not have warned — but we can at least verify it didn't error.
	if p.bearerToken != "tok123" {
		t.Errorf("bearerToken = %q, want %q", p.bearerToken, "tok123")
	}
}

func TestHealthCheck_TransformOnlyMode(t *testing.T) {
	p := newTestPlugin(t, "test-token")

	status := p.HealthCheck(context.Background())
	if status.Status != plugin.StatusOK {
		t.Errorf("status = %q, want %q", status.Status, plugin.StatusOK)
	}
	if status.Message != "transform-only mode" {
		t.Errorf("message = %q, want %q", status.Message, "transform-only mode")
	}
}

func TestHealthCheck_NotConfigured(t *testing.T) {
	p := newTestPlugin(t, "")

	status := p.HealthCheck(context.Background())
	if status.Status != plugin.StatusOK {
		t.Errorf("status = %q, want %q", status.Status, plugin.StatusOK)
	}
	if status.Message != "not configured" {
		t.Errorf("message = %q, want %q", status.Message, "not configured")
	}
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
