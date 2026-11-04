package provider_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/provider"
)

var ctx = context.Background()

func TestClientSendsKeyAndDecodes(t *testing.T) {
	var gotKey, gotIdem, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotIdem, gotPath = r.Header.Get("X-Api-Key"), r.Header.Get("Idempotency-Key"), r.URL.RequestURI()
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ord_1","client_order_id":"c1","status":"OPEN","qty":10,"price":19012,"version":1,"fills":[]}`))
	}))
	defer srv.Close()

	c := provider.New(srv.URL, "sim_test_key", time.Second, nil)
	o, err := c.AcceptQuote(ctx, "qt_1", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "sim_test_key" || gotIdem != "c1" || gotPath != "/v1/quotes/qt_1/accept" || gotBody["client_order_id"] != "c1" {
		t.Fatalf("request: key=%q idem=%q path=%q body=%v", gotKey, gotIdem, gotPath, gotBody)
	}
	if o.ID != "ord_1" || o.Status != "OPEN" || o.Version != 1 || o.Price != 19012 {
		t.Fatalf("order = %+v", o)
	}
}

func TestClientReturnsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"quote_expired","message":"quote qt_1 expired"}}`))
	}))
	defer srv.Close()

	_, err := provider.New(srv.URL, "k", time.Second, nil).AcceptQuote(ctx, "qt_1", "c1")
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 409 || apiErr.Code != "quote_expired" {
		t.Fatalf("got %v", err)
	}
}

func TestClientNonJSONErrorAndTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/prices" {
			time.Sleep(200 * time.Millisecond)
			return
		}
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer srv.Close()
	c := provider.New(srv.URL, "k", 50*time.Millisecond, nil)

	_, err := c.GetOrder(ctx, "ord_1")
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 502 || apiErr.Code != "" {
		t.Fatalf("502: got %v", err)
	}

	if _, err := c.ListPrices(ctx); err == nil || errors.As(err, &apiErr) {
		t.Fatalf("timeout: got %v, want a network error", err)
	}
}

func TestVerifySignature(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"id":"evt_1"}`)
	ts, nonce := "1800000000", "0123456789abcdef0123456789abcdef"
	sig := provider.Sign("whsec_test", 1_800_000_000, nonce, body)
	tol := 5 * time.Minute

	if err := provider.VerifySignature("whsec_test", ts, nonce, body, sig, tol, now); err != nil {
		t.Fatalf("valid: %v", err)
	}
	cases := []struct {
		name, secret, ts, nonce, sig string
		body                         []byte
		at                           time.Time
		want                         error
	}{
		{"wrong secret", "other", ts, nonce, sig, body, now, provider.ErrBadSignature},
		{"tampered body", "whsec_test", ts, nonce, sig, []byte(`{"id":"evt_2"}`), now, provider.ErrBadSignature},
		{"swapped nonce", "whsec_test", ts, "ffffffffffffffffffffffffffffffff", sig, body, now, provider.ErrBadSignature},
		{"missing nonce", "whsec_test", ts, "", sig, body, now, provider.ErrBadNonce},
		{"huge nonce", "whsec_test", ts, strings.Repeat("a", 65), sig, body, now, provider.ErrBadNonce},
		{"not hex", "whsec_test", ts, nonce, "zz", body, now, provider.ErrBadSignature},
		{"too old", "whsec_test", ts, nonce, sig, body, now.Add(6 * time.Minute), provider.ErrBadTimestamp},
		{"from the future", "whsec_test", ts, nonce, sig, body, now.Add(-6 * time.Minute), provider.ErrBadTimestamp},
		{"missing timestamp", "whsec_test", "", nonce, sig, body, now, provider.ErrBadTimestamp},
	}
	for _, c := range cases {
		if err := provider.VerifySignature(c.secret, c.ts, c.nonce, c.body, c.sig, tol, c.at); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

// Known answer computed independently (Python hmac) from brokersim's spec:
// hex(HMAC-SHA512(secret, "<ts>.<nonce>.<body>")).
func TestSignKnownAnswer(t *testing.T) {
	want := "28122eba70d3af13b1cc865113e1fdf03277b8f8892e393d4a010e80ed0251083cc02860ec518b2d5bd8670e862bc826f4ee6c90f9489d5b331efa25f5a89db9"
	if got := provider.Sign("whsec_test", 1700000000, "nonce123", []byte("hello")); got != want {
		t.Fatalf("Sign = %s, want %s", got, want)
	}
}
