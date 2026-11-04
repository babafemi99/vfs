package service

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/dbtest"
	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

func TestPostEntryRejectsBadEntries(t *testing.T) {
	s := setup(t)
	cases := map[string]domain.Entry{
		"unbalanced": {Kind: "x", SourceType: "t", SourceID: "1", Postings: []domain.Posting{
			{Account: domain.AcctFunding, Amount: -100}, {Account: domain.CashAvailable(user), Amount: 99}}},
		"unknown account": {Kind: "x", SourceType: "t", SourceID: "2", Postings: []domain.Posting{
			{Account: domain.AcctFunding, Amount: -1}, {Account: "user:99:cash_available", Amount: 1}}},
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
				_, err := s.postEntry(ctx, tx, entry)
				return err
			})
			if !errors.Is(err, domain.ErrUnbalancedEntry) && !errors.Is(err, domain.ErrInvalidEntry) {
				t.Fatalf("got %v, want ErrUnbalancedEntry or ErrInvalidEntry", err)
			}
		})
	}
	s.assertInvariants(t)
}

func TestPostEntryOncePerSource(t *testing.T) {
	s := setup(t)
	for i, want := range []bool{true, false} {
		s.inTx(t, func(tx pgx.Tx) error {
			posted, err := s.fundCash(ctx, tx, user, 50_000, "funding-1")
			if posted != want {
				t.Errorf("call %d: posted = %v, want %v", i, posted, want)
			}
			return err
		})
	}
	if c := s.cashNow(t); c.Available != 50_000 || c.Held != 0 || c.Total != 50_000 {
		t.Fatalf("cash = %+v, want 50000 available", c)
	}
	s.assertInvariants(t)
}

func TestHoldNeedsAvailableCash(t *testing.T) {
	s := setup(t)
	s.mustFund(t, 500)
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		id, err := dbtest.InsertOrder(ctx, tx, user, "AAPL", domain.SideBuy, 1, 600, 0, 600, domain.StatusPendingSubmit)
		if err != nil {
			return err
		}
		_, err = s.holdCash(ctx, tx, user, 600, id)
		return err
	})
	if !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Fatalf("got %v, want ErrInsufficientFunds", err)
	}
	if c := s.cashNow(t); c.Available != 500 || c.Held != 0 {
		t.Fatalf("cash = %+v, want unchanged", c)
	}
	s.assertInvariants(t)
}

// $1,000 available, 20 concurrent $100 orders: exactly 10 get their hold.
func TestNoDoubleSpend(t *testing.T) {
	s := setup(t)
	s.mustFund(t, 100_000)

	var ok, short atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
				id, err := dbtest.InsertOrder(ctx, tx, user, "AAPL", domain.SideBuy, 1, 10_000, 0, 10_000, domain.StatusPendingSubmit)
				if err != nil {
					return err
				}
				_, err = s.holdCash(ctx, tx, user, 10_000, id)
				return err
			})
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, domain.ErrInsufficientFunds):
				short.Add(1)
			default:
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if ok.Load() != 10 || short.Load() != 10 {
		t.Fatalf("succeeded %d, insufficient %d; want 10 and 10", ok.Load(), short.Load())
	}
	if c := s.cashNow(t); c.Available != 0 || c.Held != 100_000 {
		t.Fatalf("cash = %+v, want 0 available, 100000 held", c)
	}
	s.assertInvariants(t)
}

// placeBuy holds cash for a new OPEN BUY order, like order placement will.
func (s *Service) placeBuy(t *testing.T, qty, price, fee int64) string {
	t.Helper()
	var id string
	s.inTx(t, func(tx pgx.Tx) error {
		var err error
		if id, err = dbtest.InsertOrder(ctx, tx, user, "AAPL", domain.SideBuy, qty, price, fee, qty*price+fee, domain.StatusOpen); err != nil {
			return err
		}
		_, err = s.holdCash(ctx, tx, user, qty*price+fee, id)
		return err
	})
	return id
}

// buyFill mimics the order processor: pay from the hold first, then from
// available cash, and track hold_remaining on the order.
func (s *Service) buyFill(t *testing.T, orderID, fillID string, gross, fee int64) bool {
	t.Helper()
	var posted bool
	s.inTx(t, func(tx pgx.Tx) error {
		order, err := s.orderRepo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		fromHeld := min(gross+fee, order.HoldRemaining)
		if posted, err = s.payBuyFill(ctx, tx, user, fillID, gross, fee, fromHeld); err != nil || !posted {
			return err
		}
		return s.orderRepo.UpdateOrderHoldRemaining(ctx, tx, orderID, order.HoldRemaining-fromHeld)
	})
	return posted
}

// finishBuy releases what is left of the hold and sets a final status.
func (s *Service) finishBuy(t *testing.T, orderID, status string) {
	t.Helper()
	s.inTx(t, func(tx pgx.Tx) error {
		order, err := s.orderRepo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if _, err := s.releaseCash(ctx, tx, user, order.HoldRemaining, orderID); err != nil {
			return err
		}
		if err := s.orderRepo.UpdateOrderHoldRemaining(ctx, tx, orderID, 0); err != nil {
			return err
		}
		return s.orderRepo.UpdateOrderStatus(ctx, tx, orderID, status)
	})
}

func TestPartialThenFullFillReleasesRemainder(t *testing.T) {
	s := setup(t)
	s.mustFund(t, 100_000)
	id := s.placeBuy(t, 10, 1_000, 50) // hold $100.50

	// Per-fill fees round down to 49 in total: 1 cent is left on the hold.
	s.buyFill(t, id, "f1", 4_000, 20)
	s.buyFill(t, id, "f2", 6_000, 29)
	s.assertInvariants(t)
	s.finishBuy(t, id, domain.StatusFilled)

	if c := s.cashNow(t); c.Available != 100_000-10_049 || c.Held != 0 {
		t.Fatalf("cash = %+v, want %d available, 0 held", c, 100_000-10_049)
	}
	if b := s.balanceNow(t, domain.AcctFees); b != 49 {
		t.Fatalf("fees = %d, want 49", b)
	}
	if b := s.balanceNow(t, domain.AcctBrokerClearing); b != 10_000 {
		t.Fatalf("clearing = %d, want 10000", b)
	}
	s.assertInvariants(t)
}

func TestFillCostAboveHoldTakesFromAvailable(t *testing.T) {
	s := setup(t)
	s.mustFund(t, 20_000)
	id := s.placeBuy(t, 10, 1_000, 50) // hold 10_050

	// Fees round up to 52 in total: 2 cents more than held.
	s.buyFill(t, id, "f1", 4_000, 21)
	s.buyFill(t, id, "f2", 6_000, 31)
	s.finishBuy(t, id, domain.StatusFilled)

	if c := s.cashNow(t); c.Available != 20_000-10_052 || c.Held != 0 {
		t.Fatalf("cash = %+v, want %d available", c, 20_000-10_052)
	}
	s.assertInvariants(t)
}

func TestDuplicateFillPostsOnce(t *testing.T) {
	s := setup(t)
	s.mustFund(t, 100_000)
	id := s.placeBuy(t, 10, 1_000, 0)

	if !s.buyFill(t, id, "fill_same", 4_000, 0) {
		t.Fatal("first fill not posted")
	}
	if s.buyFill(t, id, "fill_same", 4_000, 0) {
		t.Fatal("duplicate fill posted")
	}
	if c := s.cashNow(t); c.Held != 6_000 {
		t.Fatalf("held = %d, want 6000", c.Held)
	}
	s.assertInvariants(t)
}

func TestCancelledPartialReleasesUnfilledOnce(t *testing.T) {
	s := setup(t)
	s.mustFund(t, 100_000)
	id := s.placeBuy(t, 10, 1_000, 50)
	s.buyFill(t, id, "f1", 6_000, 30)
	s.finishBuy(t, id, domain.StatusCancelled)

	// A second release (a duplicate final event) posts nothing.
	s.inTx(t, func(tx pgx.Tx) error {
		posted, err := s.releaseCash(ctx, tx, user, 4_020, id)
		if posted {
			t.Error("second release posted")
		}
		return err
	})
	if c := s.cashNow(t); c.Available != 100_000-6_030 || c.Held != 0 {
		t.Fatalf("cash = %+v, want %d available", c, 100_000-6_030)
	}
	s.assertInvariants(t)
}

func TestSellFillCreditsNetOfFee(t *testing.T) {
	s := setup(t)
	s.inTx(t, func(tx pgx.Tx) error {
		_, err := s.creditSellFill(ctx, tx, user, "fs1", 5_000, 25)
		return err
	})
	if c := s.cashNow(t); c.Available != 4_975 {
		t.Fatalf("available = %d, want 4975", c.Available)
	}
	if b := s.balanceNow(t, domain.AcctBrokerClearing); b != -5_000 {
		t.Fatalf("clearing = %d, want -5000", b)
	}
	if b := s.balanceNow(t, domain.AcctFees); b != 25 {
		t.Fatalf("fees = %d, want 25", b)
	}
	s.assertInvariants(t)
}
