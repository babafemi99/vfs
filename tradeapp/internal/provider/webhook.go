package provider

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

// Webhook headers sent by the provider.
const (
	HeaderEventID   = "X-Sim-Event-Id"
	HeaderTimestamp = "X-Sim-Timestamp"
	HeaderNonce     = "X-Sim-Nonce"
	HeaderSignature = "X-Sim-Signature"
)

var (
	ErrBadSignature = errors.New("webhook signature mismatch")
	ErrBadTimestamp = errors.New("webhook timestamp missing or outside tolerance")
	ErrBadNonce     = errors.New("webhook nonce missing or malformed")
)

// MaxNonceLength bounds the nonce we accept (the provider sends 32 hex chars).
const MaxNonceLength = 64

// Event is a provider webhook body. Order is the full snapshot after the
// change; Fill is present only on fill events.
type Event struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Data      struct {
		Order  *Order `json:"order,omitempty"`
		Fill   *Fill  `json:"fill,omitempty"`
		Reason string `json:"reason,omitempty"`
	} `json:"data"`
}

// Sign returns hex(HMAC-SHA512(secret, "<ts>.<nonce>.<body>")) with ts in
// Unix seconds. Signing the timestamp and nonce means neither can be changed
// without breaking the signature.
func Sign(secret string, ts int64, nonce string, body []byte) string {
	m := hmac.New(sha512.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10) + "." + nonce + "."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// VerifySignature checks a webhook: the timestamp is within tolerance of now
// (either direction), the nonce is present, and the signature matches
// (constant-time). Refusing a nonce seen before (a replay) is the caller's
// job, because it needs storage.
func VerifySignature(secret, timestamp, nonce string, body []byte, signature string, tolerance time.Duration, now time.Time) error {
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrBadTimestamp
	}
	if d := now.Sub(time.Unix(ts, 0)); d > tolerance || d < -tolerance {
		return ErrBadTimestamp
	}
	if nonce == "" || len(nonce) > MaxNonceLength {
		return ErrBadNonce
	}
	got, err := hex.DecodeString(signature)
	if err != nil {
		return ErrBadSignature
	}
	want, _ := hex.DecodeString(Sign(secret, ts, nonce, body))
	if !hmac.Equal(got, want) {
		return ErrBadSignature
	}
	return nil
}
