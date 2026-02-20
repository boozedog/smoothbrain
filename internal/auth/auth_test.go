package auth

import (
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/dmarx/smoothbrain/internal/config"
	_ "modernc.org/sqlite"
)

func newTestAuth(t *testing.T, sessionDuration time.Duration) *Auth {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := config.AuthConfig{
		RPDisplayName:   "Test",
		RPID:            "localhost",
		RPOrigins:       []string{"http://localhost:8080"},
		SessionDuration: sessionDuration,
	}
	auth, err := New(cfg, db, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func TestSessionExpiry(t *testing.T) {
	a := newTestAuth(t, 100*time.Millisecond)

	// Insert a session that expires soon.
	_, err := a.db.Exec(`INSERT INTO sessions (token, expires_at) VALUES (?, ?)`, "test-token", time.Now().Add(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	// Should be valid now.
	if !a.ValidateSession("test-token") {
		t.Error("session should be valid before expiry")
	}

	// Wait for expiry.
	time.Sleep(150 * time.Millisecond)

	// Should be expired now.
	if a.ValidateSession("test-token") {
		t.Error("session should be expired")
	}
}

func TestCleanupExpiredSessions(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	// Insert expired and valid sessions.
	_, _ = a.db.Exec(`INSERT INTO sessions (token, expires_at) VALUES (?, ?)`, "expired", time.Now().Add(-1*time.Hour))
	_, _ = a.db.Exec(`INSERT INTO sessions (token, expires_at) VALUES (?, ?)`, "valid", time.Now().Add(1*time.Hour))

	a.cleanupExpiredSessions()

	if a.ValidateSession("expired") {
		t.Error("expired session should have been cleaned up")
	}
	if !a.ValidateSession("valid") {
		t.Error("valid session should still exist")
	}
}

func TestLoadCredentialsInvalidTransport(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	// Insert a credential with invalid transport JSON.
	_, err := a.db.Exec(`INSERT INTO webauthn_credentials (credential_id, public_key, attestation_type, aaguid, sign_count, backup_eligible, backup_state, transport) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		[]byte("cred1"), []byte("key1"), "none", make([]byte, 16), 0, false, false, "not-valid-json")
	if err != nil {
		t.Fatal(err)
	}

	creds := a.loadCredentials()
	if len(creds) != 1 {
		t.Fatalf("expected 1 credential, got %d", len(creds))
	}
}

func TestValidateSessionNonexistent(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	if a.ValidateSession("does-not-exist") {
		t.Error("nonexistent token should not validate")
	}
}

func TestDeleteSession(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	_, _ = a.db.Exec(`INSERT INTO sessions (token, expires_at) VALUES (?, ?)`, "to-delete", time.Now().Add(1*time.Hour))
	if !a.ValidateSession("to-delete") {
		t.Fatal("session should exist before deletion")
	}

	a.DeleteSession("to-delete")

	if a.ValidateSession("to-delete") {
		t.Error("session should not exist after deletion")
	}
}
