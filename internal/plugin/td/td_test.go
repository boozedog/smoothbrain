package td

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"
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
