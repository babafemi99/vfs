// Package webhook signs and delivers events to the client's webhook URL.
package webhook

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

const (
	HeaderEventID   = "X-Sim-Event-Id"
	HeaderTimestamp = "X-Sim-Timestamp"
	HeaderNonce     = "X-Sim-Nonce"
	HeaderSignature = "X-Sim-Signature"
)

var (
	ErrBadSignature = errors.New("webhook: signature mismatch")
	ErrBadTimestamp = errors.New("webhook: timestamp outside tolerance")
	ErrBadNonce     = errors.New("webhook: missing nonce")
)

// NewNonce returns a fresh random nonce (32 hex characters). Every delivery
// attempt gets its own, so a receiver can refuse a request it has already
// seen (a replay) while still accepting our retries.
func NewNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Sign returns hex(HMAC-SHA512(secret, "<ts>.<nonce>.<body>")), ts in Unix
// seconds. The timestamp and nonce are signed, so neither can be swapped.
func Sign(secret string, ts int64, nonce string, body []byte) string {
	return hex.EncodeToString(mac(secret, ts, nonce, body))
}

// Verify checks sig against body, rejects timestamps more than tolerance away
// from now, and requires a nonce. The comparison is constant-time.
func Verify(secret string, ts int64, nonce string, body []byte, sig string, tolerance time.Duration) error {
	return VerifyAt(time.Now(), secret, ts, nonce, body, sig, tolerance)
}

// VerifyAt is Verify with an explicit current time.
func VerifyAt(now time.Time, secret string, ts int64, nonce string, body []byte, sig string, tolerance time.Duration) error {
	if d := now.Sub(time.Unix(ts, 0)); d > tolerance || d < -tolerance {
		return ErrBadTimestamp
	}
	if nonce == "" {
		return ErrBadNonce
	}
	got, err := hex.DecodeString(sig)
	if err != nil || !hmac.Equal(got, mac(secret, ts, nonce, body)) {
		return ErrBadSignature
	}
	return nil
}

func mac(secret string, ts int64, nonce string, body []byte) []byte {
	m := hmac.New(sha512.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10)))
	m.Write([]byte("."))
	m.Write([]byte(nonce))
	m.Write([]byte("."))
	m.Write(body)
	return m.Sum(nil)
}
