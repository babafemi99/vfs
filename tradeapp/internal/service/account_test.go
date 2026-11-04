package service

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/dbtest"
	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

func TestFundAccount(t *testing.T) {
	s := setup(t)
	res, err := s.FundAccount(ctx, user, 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if res.Amount != 100_000 || res.Cash.Available != 100_000 || res.Cash.Total != 100_000 {
		t.Fatalf("result = %+v", res)
	}
	// Each call is a separate funding (retry safety is the transport's job).
	if _, err := s.FundAccount(ctx, user, 100_000); err != nil {
		t.Fatal(err)
	}
	if c := s.cashNow(t); c.Available != 200_000 {
		t.Fatalf("available = %d, want 200000", c.Available)
	}
	s.assertInvariants(t)
}

func TestFundAccountValidatesAmount(t *testing.T) {
	s := setup(t)
	for _, amount := range []int64{0, -1, domain.MaxFund + 1} {
		if _, err := s.FundAccount(ctx, user, amount); !errors.Is(err, domain.ErrInvalidAmount) {
			t.Errorf("amount %d: got %v", amount, err)
		}
	}
}

func TestActivityAndAccountSummary(t *testing.T) {
	s := setup(t)
	s.mustFund(t, 123_456)
	s.buyShares(t, "b1", 2, 1_000)
	s.inTx(t, func(tx pgx.Tx) error {
		_, _, err := s.applySellFill(ctx, tx, user, "AAPL", "s1", 1, 1_500, 0, 0)
		return err
	})

	items, err := s.ListActivity(ctx, user, 0, DefaultActivityLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Kind != "account.funded" || items[0].Message != "Added $1,234.56 to your account" {
		t.Fatalf("activity = %+v", items)
	}
	if after, _ := s.ListActivity(ctx, user, items[0].ID, 10); len(after) != 0 {
		t.Fatalf("after cursor: %+v", after)
	}
	for _, bad := range [][2]int64{{-1, 10}, {0, 0}, {0, MaxActivityLimit + 1}} {
		if _, err := s.ListActivity(ctx, user, bad[0], int(bad[1])); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("after=%d limit=%d: got %v", bad[0], bad[1], err)
		}
	}

	summary, err := s.GetAccount(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Cash.Available != 123_456 || len(summary.Positions) != 1 || summary.Positions[0].Qty != 1 || summary.RealizedPnL != 500 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestCheckInvariantsDetectsBreaks(t *testing.T) {
	s := setup(t)
	failed := func() map[string]bool {
		results, err := s.CheckInvariants(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, r := range results {
			if !r.OK {
				out[r.Name] = true
			}
		}
		return out
	}
	if f := failed(); len(f) != 0 {
		t.Fatalf("clean database fails: %v", f)
	}

	// Orders holding cash and shares with no matching ledger hold or held_qty.
	var buyID, sellID string
	s.inTx(t, func(tx pgx.Tx) error {
		var err error
		if buyID, err = dbtest.InsertOrder(ctx, tx, user, "AAPL", domain.SideBuy, 1, 100, 0, 100, domain.StatusOpen); err != nil {
			return err
		}
		sellID, err = dbtest.InsertOrder(ctx, tx, user, "MSFT", domain.SideSell, 2, 100, 0, 2, domain.StatusOpen)
		return err
	})
	if f := failed(); !f["cash_held_matches_open_buys"] || !f["held_qty_matches_open_sells"] {
		t.Fatalf("hold breaks not reported: %v", f)
	}

	// Final orders hold nothing, so finishing them clears the breaks.
	s.inTx(t, func(tx pgx.Tx) error {
		if err := s.orderRepo.UpdateOrderStatus(ctx, tx, buyID, domain.StatusRejected); err != nil {
			return err
		}
		return s.orderRepo.UpdateOrderStatus(ctx, tx, sellID, domain.StatusRejected)
	})
	if f := failed(); len(f) != 0 {
		t.Fatalf("after finishing orders: %v", f)
	}

	// A corrupted balance cache.
	s.inTx(t, func(tx pgx.Tx) error {
		accounts, err := s.ledgerRepo.GetAccountsForUpdate(ctx, tx, []string{domain.CashAvailable(user)})
		if err != nil {
			return err
		}
		return s.ledgerRepo.UpdateAccountBalance(ctx, tx, accounts[0].ID, 5)
	})
	if f := failed(); !f["balance_matches_postings"] || !f["accounts_sum_to_zero"] {
		t.Fatalf("corrupt balance not reported: %v", f)
	}
}
