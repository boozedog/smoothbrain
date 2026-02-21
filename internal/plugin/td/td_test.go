package td

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dmarx/smoothbrain/internal/plugin"
)

func TestVerifySignatureValid(t *testing.T) {
	secret := "test-secret"
	ts := "2024-01-01T00:00:00Z"
	body := []byte(`{"test": true}`)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !verifySignature(secret, ts, body, validSig) {
		t.Error("valid signature should verify")
	}
}

func TestVerifySignatureInvalid(t *testing.T) {
	secret := "test-secret"
	ts := "2024-01-01T00:00:00Z"
	body := []byte(`{"test": true}`)

	if verifySignature(secret, ts, body, "sha256=invalid") {
		t.Error("invalid hex should not verify")
	}

	// Wrong HMAC value.
	wrongSig := "sha256=" + hex.EncodeToString([]byte("wrong-signature-value-here-1234"))
	if verifySignature(secret, ts, body, wrongSig) {
		t.Error("wrong signature should not verify")
	}
}

func TestVerifySignatureEmpty(t *testing.T) {
	body := []byte(`{"test": true}`)

	if verifySignature("secret", "ts", body, "") {
		t.Error("empty signature should not verify")
	}

	if verifySignature("secret", "", body, "sha256=abc") {
		t.Error("empty timestamp should not verify")
	}
}

func TestIsTimestampFreshRFC3339(t *testing.T) {
	fresh := time.Now().UTC().Format(time.RFC3339)
	if !isTimestampFresh(fresh, 5*time.Minute) {
		t.Error("current timestamp should be fresh")
	}

	stale := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	if isTimestampFresh(stale, 5*time.Minute) {
		t.Error("old timestamp should be stale")
	}
}

func TestIsTimestampFreshUnix(t *testing.T) {
	unixFresh := strconv.FormatInt(time.Now().Unix(), 10)
	if !isTimestampFresh(unixFresh, 5*time.Minute) {
		t.Error("current unix timestamp should be fresh")
	}

	unixStale := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	if isTimestampFresh(unixStale, 5*time.Minute) {
		t.Error("old unix timestamp should be stale")
	}
}

func TestIsTimestampFreshMalformed(t *testing.T) {
	if isTimestampFresh("not-a-timestamp", 5*time.Minute) {
		t.Error("malformed timestamp should not be fresh")
	}

	if isTimestampFresh("", 5*time.Minute) {
		t.Error("empty timestamp should not be fresh")
	}
}

func TestIsTimestampFreshFuture(t *testing.T) {
	future := time.Now().Add(3 * time.Minute).UTC().Format(time.RFC3339)
	if !isTimestampFresh(future, 5*time.Minute) {
		t.Error("near-future timestamp should be fresh (within tolerance)")
	}

	farFuture := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	if isTimestampFresh(farFuture, 5*time.Minute) {
		t.Error("far-future timestamp should be stale")
	}
}

// --- nonce / replay tests ---

type stubBus struct{}

func (stubBus) Emit(plugin.Event) {}

func signRequest(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func newTestPlugin(secret string) *Plugin {
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.cfg.WebhookSecret = secret
	p.bus = stubBus{}
	return p
}

func doWebhook(p *Plugin, ts string, body []byte, sig string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/hooks/td", strings.NewReader(string(body)))
	r.Header.Set("X-TD-Timestamp", ts)
	r.Header.Set("X-TD-Signature", sig)
	w := httptest.NewRecorder()
	p.handleWebhook(w, r)
	return w
}

func TestNonceRejectReplay(t *testing.T) {
	secret := "replay-secret"
	body := []byte(`{"actions":[{"action_type":"update","entity_type":"ticket","id":"t1","entity_id":"td-001"}]}`)
	ts := time.Now().UTC().Format(time.RFC3339)
	sig := signRequest(secret, ts, body)

	p := newTestPlugin(secret)

	// First request should succeed.
	w := doWebhook(p, ts, body, sig)
	if w.Code != http.StatusOK {
		t.Fatalf("first request: want 200, got %d: %s", w.Code, w.Body.String())
	}

	// Replay with identical signature should be rejected.
	w = doWebhook(p, ts, body, sig)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("replayed request: want 401, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "replayed") {
		t.Errorf("expected 'replayed' in body, got: %s", w.Body.String())
	}
}

func TestNonceDifferentSignaturesAllowed(t *testing.T) {
	secret := "nonce-secret"
	ts := time.Now().UTC().Format(time.RFC3339)
	body1 := []byte(`{"actions":[{"action_type":"create","entity_type":"ticket","id":"a1","entity_id":"td-100"}]}`)
	body2 := []byte(`{"actions":[{"action_type":"create","entity_type":"ticket","id":"a2","entity_id":"td-200"}]}`)

	p := newTestPlugin(secret)

	w := doWebhook(p, ts, body1, signRequest(secret, ts, body1))
	if w.Code != http.StatusOK {
		t.Fatalf("first request: want 200, got %d", w.Code)
	}

	w = doWebhook(p, ts, body2, signRequest(secret, ts, body2))
	if w.Code != http.StatusOK {
		t.Fatalf("second (different) request: want 200, got %d", w.Code)
	}
}

func TestNonceEviction(t *testing.T) {
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Manually insert an old nonce.
	old := time.Now().Add(-10 * time.Minute)
	p.nonces["old-sig"] = old

	// A new check should evict the old entry.
	if p.isReplayedNonce("new-sig") {
		t.Fatal("new signature should not be a replay")
	}

	p.nonceMu.Lock()
	_, oldExists := p.nonces["old-sig"]
	_, newExists := p.nonces["new-sig"]
	p.nonceMu.Unlock()

	if oldExists {
		t.Error("old nonce should have been evicted")
	}
	if !newExists {
		t.Error("new nonce should be stored")
	}
}

func TestNonceNoSecretSkipsCheck(t *testing.T) {
	// When no webhook secret is configured, nonce check is skipped entirely.
	p := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.bus = stubBus{}

	body := []byte(`{"actions":[{"action_type":"update","entity_type":"ticket","id":"t1","entity_id":"td-001"}]}`)

	// Two identical requests without auth should both succeed.
	for i := range 2 {
		r := httptest.NewRequest(http.MethodPost, "/hooks/td", strings.NewReader(string(body)))
		w := httptest.NewRecorder()
		p.handleWebhook(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d without secret: want 200, got %d", i+1, w.Code)
		}
	}
}

func TestWebhookResponseJSON(t *testing.T) {
	p := newTestPlugin("")
	p.cfg.WebhookSecret = "" // no auth

	body := []byte(`{"actions":[{"action_type":"create","entity_type":"ticket","id":"a1","entity_id":"td-999"}]}`)
	r := httptest.NewRequest(http.MethodPost, "/hooks/td", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	p.handleWebhook(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if resp["status"] != "accepted" {
		t.Errorf("want status=accepted, got %q", resp["status"])
	}
}
