package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/dmarx/smoothbrain/internal/config"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

type challengeEntry struct {
	data      *webauthn.SessionData
	expiresAt time.Time
}

// Auth provides WebAuthn-based passkey authentication.
type Auth struct {
	wa              *webauthn.WebAuthn
	db              *sql.DB
	log             *slog.Logger
	sessionDuration time.Duration

	mu         sync.Mutex
	challenges map[string]challengeEntry
}

// New creates an Auth instance, configures WebAuthn, and ensures the required
// database tables exist.
func New(cfg config.AuthConfig, db *sql.DB, log *slog.Logger) (*Auth, error) {
	waCfg := &webauthn.Config{
		RPDisplayName: cfg.RPDisplayName,
		RPID:          cfg.RPID,
		RPOrigins:     cfg.RPOrigins,
	}
	wa, err := webauthn.New(waCfg)
	if err != nil {
		return nil, fmt.Errorf("auth: webauthn init: %w", err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS webauthn_credentials (
		id               INTEGER PRIMARY KEY,
		credential_id    BLOB UNIQUE,
		public_key       BLOB,
		attestation_type TEXT,
		aaguid           BLOB,
		sign_count       INTEGER,
		backup_eligible  BOOLEAN NOT NULL DEFAULT 0,
		backup_state     BOOLEAN NOT NULL DEFAULT 0,
		transport        TEXT NOT NULL DEFAULT '',
		created_at       DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return nil, fmt.Errorf("auth: create credentials table: %w", err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		token      TEXT PRIMARY KEY,
		expires_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return nil, fmt.Errorf("auth: create sessions table: %w", err)
	}

	// Add expires_at column for existing databases (ignore error if already exists).
	_, _ = db.Exec(`ALTER TABLE sessions ADD COLUMN expires_at DATETIME`)

	sessionDuration := cfg.SessionDuration
	if sessionDuration == 0 {
		sessionDuration = 24 * time.Hour
	}

	return &Auth{
		wa:              wa,
		db:              db,
		log:             log,
		sessionDuration: sessionDuration,
		challenges:      make(map[string]challengeEntry),
	}, nil
}

// User implements the webauthn.User interface for a single-owner model.
type User struct {
	credentials []webauthn.Credential
}

func (u *User) WebAuthnID() []byte                         { return []byte("owner") }
func (u *User) WebAuthnName() string                       { return "owner" }
func (u *User) WebAuthnDisplayName() string                { return "owner" }
func (u *User) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// HasCredential returns true if at least one passkey credential is registered.
func (a *Auth) HasCredential() bool {
	var count int
	err := a.db.QueryRow(`SELECT COUNT(*) FROM webauthn_credentials`).Scan(&count)
	if err != nil {
		a.log.Error("auth: count credentials", "error", err)
		return false
	}
	return count > 0
}

func (a *Auth) loadCredentials() []webauthn.Credential {
	rows, err := a.db.Query(`SELECT credential_id, public_key, attestation_type, aaguid, sign_count, backup_eligible, backup_state, transport FROM webauthn_credentials`)
	if err != nil {
		a.log.Error("auth: load credentials", "error", err)
		return nil
	}
	defer func() { _ = rows.Close() }()

	var creds []webauthn.Credential
	for rows.Next() {
		var c webauthn.Credential
		var aaguid []byte
		var transportJSON string
		if err := rows.Scan(&c.ID, &c.PublicKey, &c.AttestationType, &aaguid, &c.Authenticator.SignCount, &c.Flags.BackupEligible, &c.Flags.BackupState, &transportJSON); err != nil {
			a.log.Error("auth: scan credential", "error", err)
			continue
		}
		if len(aaguid) == 16 {
			copy(c.Authenticator.AAGUID[:], aaguid)
		}
		if transportJSON != "" {
			if err := json.Unmarshal([]byte(transportJSON), &c.Transport); err != nil {
				a.log.Error("auth: unmarshal transport", "error", err, "credential_id", c.ID)
			}
		}
		creds = append(creds, c)
	}
	if err := rows.Err(); err != nil {
		a.log.Error("auth: iterate credentials", "error", err)
		return nil
	}
	return creds
}

// BeginRegistration starts a WebAuthn registration ceremony.
func (a *Auth) BeginRegistration() (*protocol.CredentialCreation, string, error) {
	user := &User{credentials: nil}
	creation, session, err := a.wa.BeginRegistration(user)
	if err != nil {
		return nil, "", fmt.Errorf("auth: begin registration: %w", err)
	}
	challengeID := a.storeChallenge(session)
	return creation, challengeID, nil
}

// FinishRegistration completes a WebAuthn registration ceremony and stores
// the new credential in the database.
func (a *Auth) FinishRegistration(challengeID string, r *http.Request) error {
	session, ok := a.getChallenge(challengeID)
	if !ok {
		return fmt.Errorf("auth: no registration challenge found")
	}

	user := &User{credentials: nil}
	cred, err := a.wa.FinishRegistration(user, *session, r)
	if err != nil {
		return fmt.Errorf("auth: finish registration: %w", err)
	}

	transportJSON, _ := json.Marshal(cred.Transport)
	_, err = a.db.Exec(
		`INSERT INTO webauthn_credentials (credential_id, public_key, attestation_type, aaguid, sign_count, backup_eligible, backup_state, transport) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		cred.ID, cred.PublicKey, cred.AttestationType, cred.Authenticator.AAGUID[:], cred.Authenticator.SignCount, cred.Flags.BackupEligible, cred.Flags.BackupState, string(transportJSON),
	)
	if err != nil {
		return fmt.Errorf("auth: store credential: %w", err)
	}
	return nil
}

// BeginLogin starts a WebAuthn login ceremony.
func (a *Auth) BeginLogin() (*protocol.CredentialAssertion, string, error) {
	creds := a.loadCredentials()
	if len(creds) == 0 {
		return nil, "", fmt.Errorf("auth: no credentials registered")
	}

	user := &User{credentials: creds}
	assertion, session, err := a.wa.BeginLogin(user)
	if err != nil {
		return nil, "", fmt.Errorf("auth: begin login: %w", err)
	}
	challengeID := a.storeChallenge(session)
	return assertion, challengeID, nil
}

// FinishLogin completes a WebAuthn login ceremony and returns a new session
// token.
func (a *Auth) FinishLogin(challengeID string, r *http.Request) (string, error) {
	session, ok := a.getChallenge(challengeID)
	if !ok {
		return "", fmt.Errorf("auth: no login challenge found")
	}

	creds := a.loadCredentials()
	user := &User{credentials: creds}
	cred, err := a.wa.FinishLogin(user, *session, r)
	if err != nil {
		return "", fmt.Errorf("auth: finish login: %w", err)
	}

	// Update sign count (non-fatal).
	if _, err := a.db.Exec(`UPDATE webauthn_credentials SET sign_count = ? WHERE credential_id = ?`, cred.Authenticator.SignCount, cred.ID); err != nil {
		a.log.Error("auth: update sign count", "error", err)
	}

	// Generate session token.
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("auth: generate token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)

	expiresAt := time.Now().Add(a.sessionDuration)
	_, err = a.db.Exec(`INSERT INTO sessions (token, expires_at) VALUES (?, ?)`, token, expiresAt)
	if err != nil {
		return "", fmt.Errorf("auth: store session: %w", err)
	}
	return token, nil
}

// ValidateSession returns true if the given token exists in the sessions table.
func (a *Auth) ValidateSession(token string) bool {
	var count int
	err := a.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token = ? AND expires_at > ?`, token, time.Now()).Scan(&count)
	if err != nil {
		a.log.Error("auth: validate session", "error", err)
		return false
	}
	return count > 0
}

// DeleteSession removes a session token from the database.
func (a *Auth) DeleteSession(token string) {
	_, err := a.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	if err != nil {
		a.log.Error("auth: delete session", "error", err)
	}
}

func (a *Auth) cleanupExpiredSessions() {
	result, err := a.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, time.Now())
	if err != nil {
		a.log.Error("auth: cleanup expired sessions", "error", err)
		return
	}
	if n, _ := result.RowsAffected(); n > 0 {
		a.log.Info("auth: cleaned up expired sessions", "count", n)
	}
}

// StartCleanup runs periodic cleanup of expired sessions.
func (a *Auth) StartCleanup(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.cleanupExpiredSessions()
			}
		}
	}()
}

func (a *Auth) storeChallenge(data *webauthn.SessionData) string {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	challengeID := hex.EncodeToString(id)

	a.mu.Lock()
	defer a.mu.Unlock()
	a.challenges[challengeID] = challengeEntry{
		data:      data,
		expiresAt: time.Now().Add(60 * time.Second),
	}
	return challengeID
}

func (a *Auth) getChallenge(challengeID string) (*webauthn.SessionData, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Cleanup expired entries.
	now := time.Now()
	for k, v := range a.challenges {
		if now.After(v.expiresAt) {
			delete(a.challenges, k)
		}
	}

	entry, ok := a.challenges[challengeID]
	if !ok {
		return nil, false
	}
	delete(a.challenges, challengeID)
	return entry.data, true
}
