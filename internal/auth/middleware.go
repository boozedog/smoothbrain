package auth

import (
	"net/http"
	"strings"
)

// Middleware returns an HTTP middleware that enforces session authentication.
// Requests to /auth/, /hooks/, /vendor/, and /api/health are allowed through
// without a valid session.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Bypass authentication for public paths.
		if strings.HasPrefix(path, "/auth/") ||
			strings.HasPrefix(path, "/hooks/") ||
			strings.HasPrefix(path, "/vendor/") ||
			path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Check session cookie.
		cookie, err := r.Cookie("session")
		if err == nil && a.ValidateSession(cookie.Value) {
			next.ServeHTTP(w, r)
			return
		}

		http.Redirect(w, r, "/auth/login", http.StatusFound)
	})
}
