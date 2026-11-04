package domain

import (
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)

func TestFeeAndTotal(t *testing.T) {
	// Example from the spec: 10 x 19012 at 50 bps -> fee 950.6 rounds to 951.
	buy, err := NewQuote("qt_1", "AAPL", Buy, 10, 19012, 50, t0, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if buy.Fee != 951 || buy.Total != 191071 {
		t.Errorf("buy fee/total = %d/%d, want 951/191071", buy.Fee, buy.Total)
	}
	sell, _ := NewQuote("qt_2", "AAPL", Sell, 10, 19012, 50, t0, 15*time.Second)
	if sell.Total != 190120-951 {
		t.Errorf("sell total = %d, want %d", sell.Total, 190120-951)
	}
	if got := FeeFor(100, 50); got != 1 { // 0.5 rounds half-up
		t.Errorf("FeeFor(100,50) = %d, want 1", got)
	}
	if got := FeeFor(99, 50); got != 0 { // 0.495 rounds down
		t.Errorf("FeeFor(99,50) = %d, want 0", got)
	}
}

func TestNewQuoteValidation(t *testing.T) {
	cases := []struct {
		symbol string
		side   Side
		qty    int64
		price  int64
	}{
		{"", Buy, 1, 100},
		{"AAPL", "HOLD", 1, 100},
		{"AAPL", Buy, 0, 100},
		{"AAPL", Buy, -1, 100},
		{"AAPL", Buy, 1, 0},
	}
	for _, c := range cases {
		if _, err := NewQuote("q", c.symbol, c.side, c.qty, c.price, 50, t0, time.Second); !errors.Is(err, ErrValidation) {
			t.Errorf("%+v: err = %v, want validation_error", c, err)
		}
	}
}

func TestQuoteAcceptAndExpiry(t *testing.T) {
	ttl := 15 * time.Second
	q, _ := NewQuote("qt_1", "AAPL", Buy, 1, 100, 0, t0, ttl)

	// Exactly at ExpiresAt the quote is no longer valid.
	if err := q.Accept("ord_1", t0.Add(ttl)); !errors.Is(err, ErrQuoteExpired) {
		t.Fatalf("accept at expiry: %v, want quote_expired", err)
	}
	if q.Status != QuoteActive {
		t.Fatalf("failed accept changed status to %s", q.Status)
	}
	if v := q.View(t0.Add(ttl)); v.Status != QuoteExpired {
		t.Errorf("View after TTL = %s, want EXPIRED", v.Status)
	}

	if _, err := q.Expire(t0); err == nil {
		t.Error("Expire before TTL should fail")
	}
	ev, err := q.Expire(t0.Add(ttl))
	if err != nil || ev.Type != EventQuoteExpired || ev.Quote.Status != QuoteExpired {
		t.Fatalf("Expire: ev=%+v err=%v", ev, err)
	}
	if _, err := q.Expire(t0.Add(ttl)); err == nil {
		t.Error("second Expire should fail (event must fire once)")
	}
}

func TestQuoteAcceptTwice(t *testing.T) {
	q, _ := NewQuote("qt_1", "AAPL", Buy, 1, 100, 0, t0, time.Minute)
	if err := q.Accept("ord_1", t0); err != nil {
		t.Fatal(err)
	}
	if q.Status != QuoteAccepted || q.OrderID != "ord_1" {
		t.Fatalf("quote = %+v", q)
	}
	if err := q.Accept("ord_2", t0); !errors.Is(err, ErrQuoteAlreadyAccepted) {
		t.Errorf("second accept: %v", err)
	}
	if _, err := q.Expire(t0.Add(time.Hour)); err == nil {
		t.Error("accepted quote must not expire")
	}
}
