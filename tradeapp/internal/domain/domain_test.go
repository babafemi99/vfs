package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

func TestNewAvgCost(t *testing.T) {
	cases := []struct{ qty, avg, q, p, want int64 }{
		{0, 0, 10, 1_000, 1_000},
		{10, 1_000, 10, 1_100, 1_050},
		{20, 1_050, 3, 1_001, 1_043}, // 24003/23 = 1043.6 → rounds down
		{0, 0, 0, 0, 0},
	}
	for _, c := range cases {
		if got := domain.NewAvgCost(c.qty, c.avg, c.q, c.p); got != c.want {
			t.Errorf("NewAvgCost(%d, %d, %d, %d) = %d, want %d", c.qty, c.avg, c.q, c.p, got, c.want)
		}
	}
}

func TestRealized(t *testing.T) {
	if got := domain.Realized(1_000, 4, 1_200, 10); got != 790 {
		t.Errorf("gain: got %d, want 790", got)
	}
	if got := domain.Realized(1_000, 4, 900, 10); got != -410 {
		t.Errorf("loss: got %d, want -410", got)
	}
}

func TestPositionLifecycle(t *testing.T) {
	p := domain.Position{Symbol: "AAPL"}
	if err := p.Hold(1); !errors.Is(err, domain.ErrInsufficientShares) {
		t.Fatalf("hold on empty: %v", err)
	}
	_ = p.ApplyBuy(10, 1_000)
	_ = p.ApplyBuy(10, 1_100)
	if p.Qty != 20 || p.AvgCost != 1_050 {
		t.Fatalf("after buys: %+v", p)
	}
	if err := p.Hold(6); err != nil {
		t.Fatal(err)
	}
	if err := p.Hold(15); !errors.Is(err, domain.ErrInsufficientShares) {
		t.Fatalf("over-hold: %v", err)
	}
	r, err := p.ApplySell(4, 1_200, 10, 4)
	if err != nil || r != 590 { // (1200-1050)*4 - 10
		t.Fatalf("sell: r=%d err=%v", r, err)
	}
	if err := p.Release(2); err != nil {
		t.Fatal(err)
	}
	if p.Qty != 16 || p.HeldQty != 0 || p.AvgCost != 1_050 || p.RealizedPnL != 590 {
		t.Fatalf("after sell: %+v", p)
	}
	if err := p.Release(1); err == nil {
		t.Fatal("released more than held")
	}
	if _, err := p.ApplySell(17, 900, 0, 0); !errors.Is(err, domain.ErrInsufficientShares) {
		t.Fatalf("oversell: %v", err)
	}
	if _, err := p.ApplySell(16, 1_000, 0, 0); err != nil {
		t.Fatal(err)
	}
	if p.Qty != 0 || p.AvgCost != 0 {
		t.Fatalf("flat position keeps avg cost: %+v", p)
	}
}

func TestEntryValidate(t *testing.T) {
	ok := domain.Entry{Kind: "k", SourceType: "s", SourceID: "1", Postings: []domain.Posting{{"a", -5}, {"b", 5}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid entry: %v", err)
	}
	bad := map[string]domain.Entry{
		"unbalanced":   {Kind: "k", SourceType: "s", SourceID: "1", Postings: []domain.Posting{{"a", -5}, {"b", 4}}},
		"one posting":  {Kind: "k", SourceType: "s", SourceID: "1", Postings: []domain.Posting{{"a", 0}}},
		"zero amount":  {Kind: "k", SourceType: "s", SourceID: "1", Postings: []domain.Posting{{"a", 0}, {"b", 0}}},
		"no source id": {Kind: "k", SourceType: "s", Postings: []domain.Posting{{"a", -5}, {"b", 5}}},
	}
	for name, e := range bad {
		if err := e.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFormatMoney(t *testing.T) {
	cases := map[int64]string{0: "$0.00", 5: "$0.05", 123456: "$1,234.56", 100000000: "$1,000,000.00", -250: "-$2.50"}
	for in, want := range cases {
		if got := domain.FormatMoney(in); got != want {
			t.Errorf("FormatMoney(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestBackoff(t *testing.T) {
	base, max := time.Second, 10*time.Second
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
	for i, w := range want {
		if got := domain.Backoff(i+1, base, max); got != w {
			t.Errorf("Backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}

func TestValidateQuoteRequest(t *testing.T) {
	if err := domain.ValidateQuoteRequest("AAPL", "BUY", 10); err != nil {
		t.Fatalf("valid: %v", err)
	}
	bad := [][3]any{{"", "BUY", int64(1)}, {"aapl", "BUY", int64(1)}, {"AAPL", "HOLD", int64(1)},
		{"AAPL", "SELL", int64(0)}, {"AAPL", "SELL", int64(domain.MaxOrderQty + 1)}, {"TOOLONGSYMBOLNAME1", "BUY", int64(1)}}
	for _, b := range bad {
		if err := domain.ValidateQuoteRequest(b[0].(string), b[1].(string), b[2].(int64)); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("%v accepted", b)
		}
	}
}

func TestNextStatus(t *testing.T) {
	cases := []struct {
		local, provider string
		cancelRejected  bool
		want            string
		changed         bool
	}{
		{domain.StatusPendingSubmit, domain.StatusOpen, false, domain.StatusOpen, true},
		{domain.StatusPendingSubmit, domain.StatusFilled, false, domain.StatusFilled, true},
		{domain.StatusOpen, domain.StatusPartiallyFilled, false, domain.StatusPartiallyFilled, true},
		{domain.StatusOpen, domain.StatusOpen, false, domain.StatusOpen, false},
		{domain.StatusPendingCancel, domain.StatusPartiallyFilled, false, domain.StatusPendingCancel, false},
		{domain.StatusPendingCancel, domain.StatusOpen, false, domain.StatusPendingCancel, false},
		{domain.StatusPendingCancel, domain.StatusPartiallyFilled, true, domain.StatusPartiallyFilled, true},
		{domain.StatusPendingCancel, domain.StatusFilled, false, domain.StatusFilled, true},
		{domain.StatusPendingCancel, domain.StatusCancelled, false, domain.StatusCancelled, true},
		{domain.StatusFilled, domain.StatusCancelled, false, domain.StatusFilled, false},
		{domain.StatusCancelled, domain.StatusOpen, false, domain.StatusCancelled, false},
		{domain.StatusOpen, "WEIRD", false, domain.StatusOpen, false},
	}
	for _, c := range cases {
		got, changed := domain.NextStatus(c.local, c.provider, c.cancelRejected)
		if got != c.want || changed != c.changed {
			t.Errorf("NextStatus(%s, %s, %v) = %s, %v; want %s, %v", c.local, c.provider, c.cancelRejected, got, changed, c.want, c.changed)
		}
	}
}

func TestOutcomeLine(t *testing.T) {
	expired := "quote_expired"
	cases := []struct {
		o    domain.Order
		want string
	}{
		{domain.Order{Side: "BUY", Status: domain.StatusFilled, Qty: 10, FilledQty: 10}, "Filled"},
		{domain.Order{Side: "BUY", Status: domain.StatusCancelled, Qty: 10, FilledQty: 6}, "6 of 10 bought, 4 cancelled"},
		{domain.Order{Side: "SELL", Status: domain.StatusCancelled, Qty: 10}, "Cancelled"},
		{domain.Order{Side: "BUY", Status: domain.StatusExpired, Qty: 10, FilledQty: 3}, "Expired: 3 of 10 bought"},
		{domain.Order{Side: "SELL", Status: domain.StatusRejected, Reason: &expired}, "Rejected: quote_expired"},
		{domain.Order{Side: "BUY", Status: domain.StatusOpen, Qty: 10}, ""},
	}
	for _, c := range cases {
		if got := domain.OutcomeLine(c.o); got != c.want {
			t.Errorf("OutcomeLine(%s %s %d/%d) = %q, want %q", c.o.Side, c.o.Status, c.o.FilledQty, c.o.Qty, got, c.want)
		}
	}
}

func TestCompareOrder(t *testing.T) {
	local := domain.Order{ID: "order-1", Status: domain.StatusFilled, Qty: 10, FilledQty: 10}
	fills := []domain.Fill{{ID: "f1"}, {ID: "f2"}}
	agree := domain.ProviderSnapshot{ID: "ord_1", Status: domain.StatusFilled, FilledQty: 10, FillIDs: []string{"f2", "f1"}}
	if m := domain.CompareOrder(local, fills, agree); len(m) != 0 {
		t.Fatalf("agreeing order: %+v", m)
	}

	differ := domain.ProviderSnapshot{ID: "ord_1", Status: domain.StatusCancelled, FilledQty: 6, FillIDs: []string{"f1", "f3"}}
	got := domain.CompareOrder(local, fills, differ)
	want := []string{domain.MismatchStatus, domain.MismatchFilledQty, domain.MismatchFillMissingLocal, domain.MismatchFillUnknown}
	if len(got) != len(want) {
		t.Fatalf("mismatches = %+v", got)
	}
	for i, k := range want {
		if got[i].Kind != k {
			t.Errorf("mismatch %d = %s, want %s", i, got[i].Kind, k)
		}
	}
	if got[2].Provider != "f3" || got[3].Local != "f2" {
		t.Errorf("fill ids: %+v", got[2:])
	}
}

func TestJobAlerts(t *testing.T) {
	if a := domain.JobAlerts(0, time.Minute, 5*time.Minute, false); len(a) != 0 {
		t.Fatalf("healthy: %v", a)
	}
	a := domain.JobAlerts(2, 6*time.Minute, 5*time.Minute, true)
	if len(a) != 3 || a[0] != domain.AlertDeadLetters || a[1] != domain.AlertQueueLag || a[2] != domain.AlertCircuitOpen {
		t.Fatalf("alerts = %v", a)
	}
}
