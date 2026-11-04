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

func (s *Service) buyShares(t *testing.T, fillID string, q, price int64) bool {
	t.Helper()
	var applied bool
	s.inTx(t, func(tx pgx.Tx) error {
		var err error
		applied, err = s.applyBuyFill(ctx, tx, user, "AAPL", fillID, q, price)
		return err
	})
	return applied
}

// placeSell records a SELL order and holds its shares, like order placement will.
func (s *Service) placeSell(tx pgx.Tx, qty int64) (string, error) {
	id, err := dbtest.InsertOrder(ctx, tx, user, "AAPL", domain.SideSell, qty, 1_200, 0, qty, domain.StatusOpen)
	if err != nil {
		return "", err
	}
	return id, s.holdShares(ctx, tx, user, "AAPL", qty)
}

func TestBuyFillsAverageCostOnce(t *testing.T) {
	s := setup(t)
	s.buyShares(t, "f1", 10, 1_000)
	s.buyShares(t, "f2", 10, 1_100)
	if s.buyShares(t, "f2", 10, 1_100) {
		t.Fatal("duplicate fill applied twice")
	}
	if p := s.positionNow(t, "AAPL"); p.Qty != 20 || p.AvgCost != 1_050 {
		t.Fatalf("position = %+v, want 20 @ 1050", p)
	}
	s.assertInvariants(t)
}

func TestHoldNeedsAvailableShares(t *testing.T) {
	s := setup(t)
	hold := func(qty int64) error {
		return s.repo.RunInTx(ctx, func(tx pgx.Tx) error { return s.holdShares(ctx, tx, user, "AAPL", qty) })
	}
	if err := hold(1); !errors.Is(err, domain.ErrInsufficientShares) {
		t.Fatalf("no position: got %v", err)
	}
	s.buyShares(t, "f1", 5, 1_000)
	if err := hold(6); !errors.Is(err, domain.ErrInsufficientShares) {
		t.Fatalf("too many: got %v", err)
	}
}

func TestSellFillRealizesAndReleases(t *testing.T) {
	s := setup(t)
	s.buyShares(t, "b1", 10, 1_000)

	var orderID string
	s.inTx(t, func(tx pgx.Tx) error {
		var err error
		orderID, err = s.placeSell(tx, 6)
		return err
	})
	if p := s.positionNow(t, "AAPL"); p.HeldQty != 6 || p.Available() != 4 {
		t.Fatalf("after hold: %+v", p)
	}

	// Sell 4 of the 6 at $12.00 with a $0.10 fee.
	s.inTx(t, func(tx pgx.Tx) error {
		realized, applied, err := s.applySellFill(ctx, tx, user, "AAPL", "s1", 4, 1_200, 10, 4)
		if err != nil {
			return err
		}
		if !applied || realized != 790 {
			t.Errorf("realized = %d (applied %v), want 790", realized, applied)
		}
		return s.orderRepo.UpdateOrderHoldRemaining(ctx, tx, orderID, 2)
	})
	s.assertInvariants(t)

	// Cancelled: release the 2 still held.
	s.inTx(t, func(tx pgx.Tx) error {
		if err := s.releaseShares(ctx, tx, user, "AAPL", 2); err != nil {
			return err
		}
		if err := s.orderRepo.UpdateOrderHoldRemaining(ctx, tx, orderID, 0); err != nil {
			return err
		}
		return s.orderRepo.UpdateOrderStatus(ctx, tx, orderID, domain.StatusCancelled)
	})
	p := s.positionNow(t, "AAPL")
	if p.Qty != 6 || p.HeldQty != 0 || p.AvgCost != 1_000 || p.RealizedPnL != 790 {
		t.Fatalf("final position = %+v", p)
	}
	s.assertInvariants(t)
}

func TestSellFillAppliedOnce(t *testing.T) {
	s := setup(t)
	s.buyShares(t, "b1", 5, 1_000)
	for i, want := range []bool{true, false} {
		s.inTx(t, func(tx pgx.Tx) error {
			_, applied, err := s.applySellFill(ctx, tx, user, "AAPL", "s1", 2, 1_100, 0, 0)
			if applied != want {
				t.Errorf("call %d: applied = %v, want %v", i, applied, want)
			}
			return err
		})
	}
	if p := s.positionNow(t, "AAPL"); p.Qty != 3 || p.RealizedPnL != 200 {
		t.Fatalf("position = %+v, want 3 left with 200 realized", p)
	}
	s.assertInvariants(t)
}

// 10 shares, 20 concurrent single-share SELL orders: exactly 10 hold.
func TestNoDoubleSellConcurrent(t *testing.T) {
	s := setup(t)
	s.buyShares(t, "b1", 10, 1_000)

	var ok, short atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
				_, err := s.placeSell(tx, 1)
				return err
			})
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, domain.ErrInsufficientShares):
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
		t.Fatalf("held %d, refused %d; want 10 and 10", ok.Load(), short.Load())
	}
	s.assertInvariants(t)
}
