package auth

import (
	"encoding/json"
	"net/http"
	"strings"
)

// RegisterRoutes adds all authentication routes to the given ServeMux.
func (a *Auth) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", a.handleLoginPage)
	mux.HandleFunc("GET /auth/register", a.handleRegisterPage)
	mux.HandleFunc("POST /auth/register/begin", a.handleRegisterBegin)
	mux.HandleFunc("POST /auth/register/finish", a.handleRegisterFinish)
	mux.HandleFunc("POST /auth/login/begin", a.handleLoginBegin)
	mux.HandleFunc("POST /auth/login/finish", a.handleLoginFinish)
	mux.HandleFunc("POST /auth/logout", a.handleLogout)
}

func (a *Auth) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if !a.HasCredential() {
		http.Redirect(w, r, "/auth/register", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	LoginPage().Render(r.Context(), w)
}

func (a *Auth) handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if a.HasCredential() {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	RegisterPage().Render(r.Context(), w)
}

func (a *Auth) handleRegisterBegin(w http.ResponseWriter, r *http.Request) {
	creation, err := a.BeginRegistration()
	if err != nil {
		a.log.Error("auth: register begin", "error", err)
		http.Error(w, "Registration failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(creation)
}

func (a *Auth) handleRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if err := a.FinishRegistration(r); err != nil {
		a.log.Error("auth: register finish", "error", err)
		http.Error(w, "Registration failed", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (a *Auth) handleLoginBegin(w http.ResponseWriter, r *http.Request) {
	assertion, err := a.BeginLogin()
	if err != nil {
		a.log.Error("auth: login begin", "error", err)
		http.Error(w, "Login failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(assertion)
}

func (a *Auth) handleLoginFinish(w http.ResponseWriter, r *http.Request) {
	token, err := a.FinishLogin(r)
	if err != nil {
		a.log.Error("auth: login finish", "error", err)
		http.Error(w, "Login failed", http.StatusBadRequest)
		return
	}

	secure := false
	for _, origin := range a.wa.Config.RPOrigins {
		if strings.HasPrefix(origin, "https") {
			secure = true
			break
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// isValidOrigin checks if the request origin matches any configured RP origin.
func isValidOrigin(r *http.Request, allowedOrigins []string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Fall back to Referer header.
		ref := r.Header.Get("Referer")
		if ref == "" {
			return false
		}
		origin = ref
	}
	for _, allowed := range allowedOrigins {
		if strings.HasPrefix(origin, allowed) {
			return true
		}
	}
	return false
}

func (a *Auth) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !isValidOrigin(r, a.wa.Config.RPOrigins) {
		http.Error(w, "forbidden: invalid origin", http.StatusForbidden)
		return
	}
	cookie, err := r.Cookie("session")
	if err == nil {
		a.DeleteSession(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:   "session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	http.Redirect(w, r, "/auth/login", http.StatusFound)
}
