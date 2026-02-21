package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMiddlewareNoLocalhostBypass(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	handler := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("localhost should not bypass auth, got status %d", rec.Code)
	}
}

func TestMiddlewarePublicPaths(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)
	handler := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	publicPaths := []string{"/auth/login", "/hooks/td", "/vendor/js/app.js", "/api/health"}
	for _, path := range publicPaths {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("path %s should be public, got status %d", path, rec.Code)
		}
	}
}

func TestMiddlewareValidSession(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	// Insert a valid session.
	_, _ = a.db.Exec(`INSERT INTO sessions (token, expires_at) VALUES (?, ?)`, "valid-token", time.Now().Add(1*time.Hour))

	handler := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "valid-token"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("valid session should pass through, got status %d", rec.Code)
	}
}

func TestMiddlewareExpiredSession(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	// Insert an expired session.
	_, _ = a.db.Exec(`INSERT INTO sessions (token, expires_at) VALUES (?, ?)`, "expired-token", time.Now().Add(-1*time.Hour))

	handler := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "expired-token"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("expired session should redirect, got status %d", rec.Code)
	}
}

func TestMiddlewareNoCookie(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	handler := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/dashboard", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("missing cookie should redirect, got status %d", rec.Code)
	}
}
