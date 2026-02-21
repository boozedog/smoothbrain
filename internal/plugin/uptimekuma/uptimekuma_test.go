package uptimekuma

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boozedog/smoothbrain/internal/plugin"
)

type mockBus struct{}

func (m *mockBus) Emit(e plugin.Event) {}

func TestWebhookTokenValid(t *testing.T) {
	p := New(slog.Default())
	p.cfg.WebhookToken = "secret-token"
	p.bus = &mockBus{}

	req := httptest.NewRequest("POST", "/hooks/uptime-kuma", strings.NewReader(`{"test":true}`))
	req.Header.Set("X-Webhook-Token", "secret-token")
	rec := httptest.NewRecorder()
	p.handleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("valid token should succeed, got %d", rec.Code)
	}
}

func TestWebhookTokenInvalid(t *testing.T) {
	p := New(slog.Default())
	p.cfg.WebhookToken = "secret-token"
	p.bus = &mockBus{}

	req := httptest.NewRequest("POST", "/hooks/uptime-kuma", strings.NewReader(`{"test":true}`))
	req.Header.Set("X-Webhook-Token", "wrong-token")
	rec := httptest.NewRecorder()
	p.handleWebhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("invalid token should be unauthorized, got %d", rec.Code)
	}
}

func TestWebhookTokenMissing(t *testing.T) {
	p := New(slog.Default())
	p.cfg.WebhookToken = "secret-token"
	p.bus = &mockBus{}

	req := httptest.NewRequest("POST", "/hooks/uptime-kuma", strings.NewReader(`{"test":true}`))
	rec := httptest.NewRecorder()
	p.handleWebhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token should be unauthorized, got %d", rec.Code)
	}
}

func TestWebhookNoTokenConfigured(t *testing.T) {
	p := New(slog.Default())
	// No token configured — should allow all requests.
	p.bus = &mockBus{}

	req := httptest.NewRequest("POST", "/hooks/uptime-kuma", strings.NewReader(`{"test":true}`))
	rec := httptest.NewRecorder()
	p.handleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("no token configured should allow request, got %d", rec.Code)
	}
}
