package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
)

// applySnapshot brings a locked order up to date with what the provider
// says. It is the one code path for webhooks, cancel answers and the
// sweeper, so they can never disagree.
//
//   - fills: the fills to record (a webhook's single fill, or every fill of
//     a polled order). Each is applied once, by fill id, whatever the order
//     of arrival.
//   - status, filled_qty, avg price: taken only from a snapshot whose version
//     is newer than the one we have (domain.NextStatus decides the status).
//   - reaching a final state releases whatever the order still holds.
func (s *Service) applySnapshot(ctx context.Context, tx pgx.Tx, order domain.Order, snap provider.Order, fills []provider.Fill, cancelRejected bool) error {
	// 1. Remember the provider's id (a webhook can arrive before the accept answer).
	if order.ProviderOrderID == nil && snap.ID != "" {
		if err := s.orderRepo.UpdateOrderProviderID(ctx, tx, order.ID, snap.ID); err != nil {
			return err
		}
		order.ProviderOrderID = &snap.ID
	}

	// 2. Record fills. A fill is a fact: always recorded, even after a final state.
	for _, f := range fills {
		if err := s.applyFill(ctx, tx, &order, f); err != nil {
			return err
		}
	}

	// 3. Move the status forward, newer snapshots only.
	if snap.Version > order.ProviderVersion && !domain.IsFinal(order.Status) {
		next, changed := domain.NextStatus(order.Status, snap.Status, cancelRejected)
		if err := s.orderRepo.UpdateOrderSnapshot(ctx, tx, order.ID, next, snap.FilledQty, snap.AvgFillPrice, snap.Version); err != nil {
			return err
		}
		order.Status, order.FilledQty, order.AvgFillPrice, order.ProviderVersion = next, snap.FilledQty, snap.AvgFillPrice, snap.Version

		if snap.Reason != "" && (next == domain.StatusRejected || next == domain.StatusExpired) {
			if err := s.orderRepo.UpdateOrderReason(ctx, tx, order.ID, snap.Reason); err != nil {
				return err
			}
			order.Reason = &snap.Reason
		}
		if changed {
			if err := s.addOrderActivity(ctx, tx, order, "order.status", statusMessage(order)); err != nil {
				return err
			}
		}
	}

	// 4. A final order holds nothing: give back the rest of its hold.
	if domain.IsFinal(order.Status) && order.HoldRemaining > 0 {
		returned := order.HoldRemaining
		if err := s.releaseOrderHold(ctx, tx, order); err != nil {
			return err
		}
		msg := fmt.Sprintf("Returned %s to your available cash", domain.FormatMoney(returned))
		if order.Side == domain.SideSell {
			msg = fmt.Sprintf("Released %d unsold %s shares", returned, order.Symbol)
		}
		if err := s.addOrderActivity(ctx, tx, order, "order.hold_released", msg); err != nil {
			return err
		}
	}
	return nil
}

// applyFill records one provider fill against a locked order: the fill row,
// the cash, the shares, and the order's remaining hold. A fill already
// recorded is skipped. Contradictions become breaks, never errors.
func (s *Service) applyFill(ctx context.Context, tx pgx.Tx, order *domain.Order, f provider.Fill) error {
	err := s.orderRepo.CreateFill(ctx, tx, domain.Fill{ID: f.ID, OrderID: order.ID, Qty: f.Qty, Price: f.Price, Fee: f.Fee})
	if errors.Is(err, domain.ErrDuplicate) {
		return nil
	}
	if err != nil {
		return err
	}

	// A fill beyond what a final order says was filled is a late fill.
	if domain.IsFinal(order.Status) {
		recorded, err := s.orderRepo.ListFills(ctx, tx, order.ID)
		if err != nil {
			return err
		}
		var total int64
		for _, r := range recorded {
			total += r.Qty
		}
		if total > order.FilledQty {
			if err := s.recordBreak(ctx, tx, order.ID, domain.BreakFillAfterFinal,
				fmt.Sprintf("fill %s (%d @ %d) arrived after the order was %s with %d filled", f.ID, f.Qty, f.Price, order.Status, order.FilledQty)); err != nil {
				return err
			}
		}
	}

	gross := f.Qty * f.Price
	var fromHeld int64
	var verb string

	if order.Side == domain.SideBuy {
		verb = "Bought"
		fromHeld = min(gross+f.Fee, order.HoldRemaining)
		paid, err := s.payBuyFillIfAffordable(ctx, tx, order.UserID, f, fromHeld)
		if err != nil {
			return err
		}
		if !paid {
			fromHeld = 0 // nothing moved; the hold stays for the final release
			if err := s.recordBreak(ctx, tx, order.ID, domain.BreakFillUnpaid,
				fmt.Sprintf("fill %s costs %d but the hold and available cash could not cover it", f.ID, gross+f.Fee)); err != nil {
				return err
			}
		}
		if _, err := s.applyBuyFill(ctx, tx, order.UserID, order.Symbol, f.ID, f.Qty, f.Price); err != nil {
			return err
		}
	} else {
		verb = "Sold"
		fromHeld = min(f.Qty, order.HoldRemaining)
		_, _, err := s.applySellFill(ctx, tx, order.UserID, order.Symbol, f.ID, f.Qty, f.Price, f.Fee, fromHeld)
		if errors.Is(err, domain.ErrInsufficientShares) {
			// Nothing moved: no shares, no cash. The hold stays for the final release.
			return s.recordBreak(ctx, tx, order.ID, domain.BreakFillSharesMissing,
				fmt.Sprintf("sell fill %s for %d %s, but those shares are not held", f.ID, f.Qty, order.Symbol))
		}
		if err != nil {
			return err
		}
		if _, err := s.creditSellFill(ctx, tx, order.UserID, f.ID, gross, f.Fee); err != nil {
			return err
		}
	}

	if fromHeld > 0 {
		order.HoldRemaining -= fromHeld
		if err := s.orderRepo.UpdateOrderHoldRemaining(ctx, tx, order.ID, order.HoldRemaining); err != nil {
			return err
		}
	}
	return s.addOrderActivity(ctx, tx, *order, "order.fill",
		fmt.Sprintf("%s %d %s at %s (fee %s)", verb, f.Qty, order.Symbol, domain.FormatMoney(f.Price), domain.FormatMoney(f.Fee)))
}

// payBuyFillIfAffordable pays a BUY fill when the hold plus available cash
// covers it, and reports false (paying nothing) when it doesn't. Checking
// first keeps the transaction usable: a refused posting would abort it.
func (s *Service) payBuyFillIfAffordable(ctx context.Context, tx pgx.Tx, userID int64, f provider.Fill, fromHeld int64) (bool, error) {
	cost := f.Qty*f.Price + f.Fee
	if need := cost - fromHeld; need > 0 {
		accounts, err := s.ledgerRepo.GetAccountsForUpdate(ctx, tx, []string{domain.CashAvailable(userID)})
		if err != nil {
			return false, err
		}
		if len(accounts) != 1 || accounts[0].Balance < need {
			return false, nil
		}
	}
	_, err := s.payBuyFill(ctx, tx, userID, f.ID, f.Qty*f.Price, f.Fee, fromHeld)
	return err == nil, err
}

func (s *Service) recordBreak(ctx context.Context, tx pgx.Tx, orderID, kind, detail string) error {
	s.log.WarnContext(ctx, "break recorded", "order_id", orderID, "kind", kind, "detail", detail)
	return s.breakRepo.CreateBreak(ctx, tx, domain.Break{OrderID: &orderID, Kind: kind, Detail: detail})
}

// statusMessage is the activity line for an order's new status.
func statusMessage(o domain.Order) string {
	if line := domain.OutcomeLine(o); line != "" {
		return line
	}
	switch o.Status {
	case domain.StatusOpen:
		return "Accepted by the broker"
	case domain.StatusPartiallyFilled:
		return fmt.Sprintf("%d of %d filled", o.FilledQty, o.Qty)
	case domain.StatusPendingCancel:
		return fmt.Sprintf("Cancelling… %d of %d filled", o.FilledQty, o.Qty)
	}
	return "Status " + o.Status
}

// ListBreaks returns the most recent recorded breaks.
func (s *Service) ListBreaks(ctx context.Context) ([]domain.Break, error) {
	var breaks []domain.Break
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		breaks, err = s.breakRepo.ListBreaks(ctx, tx, 200)
		return err
	})
	if err != nil {
		return nil, err
	}
	return breaks, nil
}
