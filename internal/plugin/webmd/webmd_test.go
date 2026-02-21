package webmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boozedog/smoothbrain/internal/plugin"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWebmd_Name(t *testing.T) {
	p := New(discardLogger())
	if got := p.Name(); got != "webmd" {
		t.Errorf("Name() = %q, want %q", got, "webmd")
	}
}

func TestWebmd_Init_Default(t *testing.T) {
	p := New(discardLogger())
	if err := p.Init(nil); err != nil {
		t.Fatalf("Init(nil) error: %v", err)
	}
	if p.cfg.Endpoint != defaultEndpoint {
		t.Errorf("endpoint = %q, want %q", p.cfg.Endpoint, defaultEndpoint)
	}
}

func TestWebmd_Init_Custom(t *testing.T) {
	p := New(discardLogger())
	cfg := json.RawMessage(`{"endpoint":"http://custom"}`)
	if err := p.Init(cfg); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	if p.cfg.Endpoint != "http://custom" {
		t.Errorf("endpoint = %q, want %q", p.cfg.Endpoint, "http://custom")
	}
}

func TestWebmd_Transform_UnknownAction(t *testing.T) {
	p := New(discardLogger())
	_ = p.Init(nil)
	ev := plugin.Event{Payload: map[string]any{}}
	_, err := p.Transform(context.Background(), ev, "foo", nil)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("error = %q, want it to contain %q", err, "unknown action")
	}
}

func TestWebmd_Fetch_NoURL(t *testing.T) {
	p := New(discardLogger())
	_ = p.Init(nil)
	ev := plugin.Event{Payload: map[string]any{}}
	_, err := p.Transform(context.Background(), ev, "fetch", nil)
	if err == nil {
		t.Fatal("expected error for empty payload")
	}
	if !strings.Contains(err.Error(), "no URL provided") {
		t.Errorf("error = %q, want it to contain %q", err, "no URL provided")
	}
}

func TestWebmd_Fetch_AddScheme(t *testing.T) {
	var gotURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.Query().Get("url")
		_, _ = fmt.Fprint(w, "# OK")
	}))
	defer ts.Close()

	p := New(discardLogger())
	_ = p.Init(nil)
	p.cfg.Endpoint = ts.URL + "/"

	ev := plugin.Event{Payload: map[string]any{"message": "example.com"}}
	_, err := p.Transform(context.Background(), ev, "fetch", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotURL != "https://example.com" {
		t.Errorf("url sent to endpoint = %q, want %q", gotURL, "https://example.com")
	}
}

func TestWebmd_Fetch_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "# Hello")
	}))
	defer ts.Close()

	p := New(discardLogger())
	_ = p.Init(nil)
	p.cfg.Endpoint = ts.URL + "/"

	ev := plugin.Event{Payload: map[string]any{"message": "https://example.com"}}
	result, err := p.Transform(context.Background(), ev, "fetch", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, ok := result.Payload["file_content"].(string); !ok || got != "# Hello" {
		t.Errorf("file_content = %q, want %q", got, "# Hello")
	}
	if got, ok := result.Payload["summary"].(string); !ok || !strings.Contains(got, "example.com") {
		t.Errorf("summary = %q, want it to contain %q", got, "example.com")
	}
}

func TestWebmd_Fetch_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, "server error")
	}))
	defer ts.Close()

	p := New(discardLogger())
	_ = p.Init(nil)
	p.cfg.Endpoint = ts.URL + "/"

	ev := plugin.Event{Payload: map[string]any{"message": "https://example.com"}}
	_, err := p.Transform(context.Background(), ev, "fetch", nil)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("error = %q, want it to contain %q", err, "HTTP 500")
	}
}

func TestWebmd_Fetch_InvalidURL(t *testing.T) {
	p := New(discardLogger())
	_ = p.Init(nil)

	ev := plugin.Event{Payload: map[string]any{"message": "   "}}
	_, err := p.Transform(context.Background(), ev, "fetch", nil)
	if err == nil {
		t.Fatal("expected error for whitespace-only URL")
	}
}
