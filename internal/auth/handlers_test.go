package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLogoutCSRFValidOrigin(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	// Insert a session.
	_, _ = a.db.Exec(`INSERT INTO sessions (token, expires_at) VALUES (?, ?)`, "logout-token", time.Now().Add(1*time.Hour))

	req := httptest.NewRequest("POST", "/auth/logout", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	req.AddCookie(&http.Cookie{Name: "session", Value: "logout-token"})
	rec := httptest.NewRecorder()

	a.handleLogout(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Error("valid origin should not be forbidden")
	}
}

func TestLogoutCSRFInvalidOrigin(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	req := httptest.NewRequest("POST", "/auth/logout", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	rec := httptest.NewRecorder()

	a.handleLogout(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("invalid origin should be forbidden, got %d", rec.Code)
	}
}

func TestLogoutCSRFMissingOrigin(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	req := httptest.NewRequest("POST", "/auth/logout", nil)
	// No Origin or Referer header.
	rec := httptest.NewRecorder()

	a.handleLogout(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("missing origin should be forbidden, got %d", rec.Code)
	}
}

func TestLogoutCSRFRefererFallback(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	_, _ = a.db.Exec(`INSERT INTO sessions (token, expires_at) VALUES (?, ?)`, "ref-token", time.Now().Add(1*time.Hour))

	req := httptest.NewRequest("POST", "/auth/logout", nil)
	req.Header.Set("Referer", "http://localhost:8080/dashboard")
	req.AddCookie(&http.Cookie{Name: "session", Value: "ref-token"})
	rec := httptest.NewRecorder()

	a.handleLogout(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Error("valid referer should not be forbidden")
	}
}

func TestLogoutDeletesSession(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	_, _ = a.db.Exec(`INSERT INTO sessions (token, expires_at) VALUES (?, ?)`, "del-token", time.Now().Add(1*time.Hour))

	req := httptest.NewRequest("POST", "/auth/logout", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	req.AddCookie(&http.Cookie{Name: "session", Value: "del-token"})
	rec := httptest.NewRecorder()

	a.handleLogout(rec, req)

	if a.ValidateSession("del-token") {
		t.Error("session should be deleted after logout")
	}
}
