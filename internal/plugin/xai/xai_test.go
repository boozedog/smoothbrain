package xai

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/boozedog/smoothbrain/internal/plugin"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestXai_Name(t *testing.T) {
	p := New(discardLogger())
	if got := p.Name(); got != "xai" {
		t.Errorf("Name() = %q, want %q", got, "xai")
	}
}

func TestXai_Init_DefaultModel(t *testing.T) {
	p := New(discardLogger())
	cfg := json.RawMessage(`{}`)
	if err := p.Init(cfg); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	if p.cfg.Model != "grok-3" {
		t.Errorf("model = %q, want %q", p.cfg.Model, "grok-3")
	}
}

func TestXai_Init_CustomModel(t *testing.T) {
	p := New(discardLogger())
	cfg := json.RawMessage(`{"model":"grok-2"}`)
	if err := p.Init(cfg); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	if p.cfg.Model != "grok-2" {
		t.Errorf("model = %q, want %q", p.cfg.Model, "grok-2")
	}
}

func TestXai_Transform_UnknownAction(t *testing.T) {
	p := New(discardLogger())
	_ = p.Init(json.RawMessage(`{}`))
	ev := plugin.Event{Payload: map[string]any{}}
	_, err := p.Transform(context.Background(), ev, "foo", nil)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("error = %q, want it to contain %q", err, "unknown action")
	}
}

func TestXai_Summarize_Success(t *testing.T) {
	ts := newTestXAIServer(t, http.StatusOK, chatResponse{
		Choices: []struct {
			Message chatMessage `json:"message"`
		}{
			{Message: chatMessage{Content: "Test summary"}},
		},
	})
	defer ts.Close()

	p := New(discardLogger())
	_ = p.Init(json.RawMessage(`{}`))
	p.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			r.URL, _ = url.Parse(ts.URL + r.URL.Path)
			return http.DefaultTransport.RoundTrip(r)
		}),
	}

	ev := plugin.Event{Payload: map[string]any{"message": "test alert"}}
	result, err := p.Transform(context.Background(), ev, "summarize", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := result.Payload["response"].(string)
	if !ok || got != "Test summary" {
		t.Errorf("response = %q, want %q", got, "Test summary")
	}
}

func TestXai_Summarize_CustomPrompt(t *testing.T) {
	var gotBody chatRequest
	ts := newTestXAIServer(t, http.StatusOK, chatResponse{
		Choices: []struct {
			Message chatMessage `json:"message"`
		}{
			{Message: chatMessage{Content: "custom result"}},
		},
	})
	defer ts.Close()

	// Override to capture request body
	ts.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chatResponse{
			Choices: []struct {
				Message chatMessage `json:"message"`
			}{
				{Message: chatMessage{Content: "custom result"}},
			},
		})
	})

	p := New(discardLogger())
	_ = p.Init(json.RawMessage(`{}`))
	p.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			r.URL, _ = url.Parse(ts.URL + r.URL.Path)
			return http.DefaultTransport.RoundTrip(r)
		}),
	}

	ev := plugin.Event{Payload: map[string]any{"message": "test"}}
	params := map[string]any{"prompt": "My custom prompt"}
	_, err := p.Transform(context.Background(), ev, "summarize", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotBody.Messages) == 0 {
		t.Fatal("no messages captured in request")
	}
	if gotBody.Messages[0].Content != "My custom prompt" {
		t.Errorf("system prompt = %q, want %q", gotBody.Messages[0].Content, "My custom prompt")
	}
}

func TestXai_Summarize_APIError(t *testing.T) {
	ts := newTestXAIServer(t, http.StatusInternalServerError, nil)
	defer ts.Close()

	p := New(discardLogger())
	_ = p.Init(json.RawMessage(`{}`))
	p.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			r.URL, _ = url.Parse(ts.URL + r.URL.Path)
			return http.DefaultTransport.RoundTrip(r)
		}),
	}

	ev := plugin.Event{Payload: map[string]any{"message": "test"}}
	_, err := p.Transform(context.Background(), ev, "summarize", nil)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want it to contain %q", err, "500")
	}
}

func newTestXAIServer(t *testing.T, status int, resp any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		if resp != nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
}
