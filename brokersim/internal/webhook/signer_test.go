package webhook

import (
	"errors"
	"testing"
	"time"
)

func TestSignVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ts := now.Unix()
	body := []byte(`{"id":"evt_1"}`)
	nonce := "0123456789abcdef0123456789abcdef"
	sig := Sign("whsec_test", ts, nonce, body)
	tol := 5 * time.Minute

	if err := VerifyAt(now, "whsec_test", ts, nonce, body, sig, tol); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	cases := []struct {
		name   string
		now    time.Time
		secret string
		nonce  string
		body   []byte
		sig    string
		want   error
	}{
		{"tampered body", now, "whsec_test", nonce, []byte(`{"id":"evt_2"}`), sig, ErrBadSignature},
		{"swapped nonce", now, "whsec_test", "ffffffffffffffffffffffffffffffff", body, sig, ErrBadSignature},
		{"missing nonce", now, "whsec_test", "", body, sig, ErrBadNonce},
		{"wrong secret", now, "other", nonce, body, sig, ErrBadSignature},
		{"not hex", now, "whsec_test", nonce, body, "zz", ErrBadSignature},
		{"empty sig", now, "whsec_test", nonce, body, "", ErrBadSignature},
		{"old timestamp", now.Add(tol + time.Second), "whsec_test", nonce, body, sig, ErrBadTimestamp},
		{"future timestamp", now.Add(-tol - time.Second), "whsec_test", nonce, body, sig, ErrBadTimestamp},
	}
	for _, c := range cases {
		if err := VerifyAt(c.now, c.secret, ts, c.nonce, c.body, c.sig, tol); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

func TestNonceIsFreshEachTime(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		n := NewNonce()
		if len(n) != 32 || seen[n] {
			t.Fatalf("nonce %q: wrong length or repeated", n)
		}
		seen[n] = true
	}
}
