package auth

import (
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
	wa  *webauthn.WebAuthn
	db  *sql.DB
	log *slog.Logger

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
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return nil, fmt.Errorf("auth: create sessions table: %w", err)
	}

	return &Auth{
		wa:         wa,
		db:         db,
		log:        log,
		challenges: make(map[string]challengeEntry),
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
	defer rows.Close()

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
			json.Unmarshal([]byte(transportJSON), &c.Transport)
		}
		creds = append(creds, c)
	}
	return creds
}

// BeginRegistration starts a WebAuthn registration ceremony.
func (a *Auth) BeginRegistration() (*protocol.CredentialCreation, error) {
	user := &User{credentials: nil}
	creation, session, err := a.wa.BeginRegistration(user)
	if err != nil {
		return nil, fmt.Errorf("auth: begin registration: %w", err)
	}
	a.storeChallenge("registration", session)
	return creation, nil
}

// FinishRegistration completes a WebAuthn registration ceremony and stores
// the new credential in the database.
func (a *Auth) FinishRegistration(r *http.Request) error {
	session, ok := a.getChallenge("registration")
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
func (a *Auth) BeginLogin() (*protocol.CredentialAssertion, error) {
	creds := a.loadCredentials()
	if len(creds) == 0 {
		return nil, fmt.Errorf("auth: no credentials registered")
	}

	user := &User{credentials: creds}
	assertion, session, err := a.wa.BeginLogin(user)
	if err != nil {
		return nil, fmt.Errorf("auth: begin login: %w", err)
	}
	a.storeChallenge("login", session)
	return assertion, nil
}

// FinishLogin completes a WebAuthn login ceremony and returns a new session
// token.
func (a *Auth) FinishLogin(r *http.Request) (string, error) {
	session, ok := a.getChallenge("login")
	if !ok {
		return "", fmt.Errorf("auth: no login challenge found")
	}

	creds := a.loadCredentials()
	user := &User{credentials: creds}
	_, err := a.wa.FinishLogin(user, *session, r)
	if err != nil {
		return "", fmt.Errorf("auth: finish login: %w", err)
	}

	// Generate session token.
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("auth: generate token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)

	_, err = a.db.Exec(`INSERT INTO sessions (token) VALUES (?)`, token)
	if err != nil {
		return "", fmt.Errorf("auth: store session: %w", err)
	}
	return token, nil
}

// ValidateSession returns true if the given token exists in the sessions table.
func (a *Auth) ValidateSession(token string) bool {
	var count int
	err := a.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token = ?`, token).Scan(&count)
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

func (a *Auth) storeChallenge(key string, data *webauthn.SessionData) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.challenges[key] = challengeEntry{
		data:      data,
		expiresAt: time.Now().Add(60 * time.Second),
	}
}

func (a *Auth) getChallenge(key string) (*webauthn.SessionData, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Cleanup expired entries.
	now := time.Now()
	for k, v := range a.challenges {
		if now.After(v.expiresAt) {
			delete(a.challenges, k)
		}
	}

	entry, ok := a.challenges[key]
	if !ok {
		return nil, false
	}
	delete(a.challenges, key)
	return entry.data, true
}
