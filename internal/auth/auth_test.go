package auth

import (
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/dmarx/smoothbrain/internal/config"
	"github.com/go-webauthn/webauthn/webauthn"
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

func TestStoreChallengeAndRetrieve(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	sd := &webauthn.SessionData{Challenge: "test-challenge-abc"}
	challengeID := a.storeChallenge(sd)

	got, ok := a.getChallenge(challengeID)
	if !ok {
		t.Fatal("expected challenge to be present")
	}
	if got.Challenge != "test-challenge-abc" {
		t.Errorf("challenge mismatch: got %q, want %q", got.Challenge, "test-challenge-abc")
	}
}

func TestChallengeSingleUse(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	sd := &webauthn.SessionData{Challenge: "one-time"}
	challengeID := a.storeChallenge(sd)

	// First retrieval should succeed.
	if _, ok := a.getChallenge(challengeID); !ok {
		t.Fatal("first retrieval should succeed")
	}

	// Second retrieval should fail (challenge was consumed).
	if _, ok := a.getChallenge(challengeID); ok {
		t.Error("second retrieval should fail; challenge is single-use")
	}
}

func TestChallengeExpiry(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	sd := &webauthn.SessionData{Challenge: "will-expire"}
	challengeID := a.storeChallenge(sd)

	// Manually set expiry to the past to simulate TTL elapsing.
	a.mu.Lock()
	entry := a.challenges[challengeID]
	entry.expiresAt = time.Now().Add(-1 * time.Second)
	a.challenges[challengeID] = entry
	a.mu.Unlock()

	if _, ok := a.getChallenge(challengeID); ok {
		t.Error("expired challenge should not be retrievable")
	}
}

func TestChallengeCleanupRemovesExpired(t *testing.T) {
	a := newTestAuth(t, 24*time.Hour)

	// Store two challenges.
	freshID := a.storeChallenge(&webauthn.SessionData{Challenge: "fresh"})
	staleID := a.storeChallenge(&webauthn.SessionData{Challenge: "stale"})

	// Expire only the stale one.
	a.mu.Lock()
	entry := a.challenges[staleID]
	entry.expiresAt = time.Now().Add(-1 * time.Second)
	a.challenges[staleID] = entry
	a.mu.Unlock()

	// Calling getChallenge triggers cleanup of expired entries.
	// Retrieve a non-existent key just to trigger cleanup.
	a.getChallenge("nonexistent")

	a.mu.Lock()
	_, staleExists := a.challenges[staleID]
	_, freshExists := a.challenges[freshID]
	a.mu.Unlock()

	if staleExists {
		t.Error("expired 'stale' challenge should have been cleaned up")
	}
	if !freshExists {
		t.Error("non-expired 'fresh' challenge should still exist")
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
