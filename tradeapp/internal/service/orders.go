package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
	"github.com/babafem99/vfs/tradeapp/pkg/lid"
)

const maxOrdersListed = 200

// normalizeOrderID returns an order id in its canonical form
// ("ORDER-01jc…" → "order-01JC…"), and false if it isn't an order id.
func normalizeOrderID(id string) (string, bool) {
	canonical, err := lid.Normalize(id, lid.Order)
	return canonical, err == nil
}

// PlaceOrder places an order from a stored quote. In one transaction it
// holds the cash (BUY) or shares (SELL), creates the order as PENDING_SUBMIT
// and queues a submit_order job. The worker then sends it to the provider.
func (s *Service) PlaceOrder(ctx context.Context, userID int64, quoteID string) (*domain.Order, error) {
	if quoteID == "" {
		return nil, fmt.Errorf("%w: quote_id is required", domain.ErrInvalidArgument)
	}

	orderID := lid.NewOrder()
	now := s.now()
	var order domain.Order

	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		// 1. The quote must exist and still be valid.
		quote, err := s.quoteRepo.GetQuote(ctx, tx, quoteID)
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: quote %s", domain.ErrNotFound, quoteID)
		}
		if err != nil {
			return err
		}
		if !now.Before(quote.ExpiresAt) {
			return fmt.Errorf("%w: quote %s expired at %s", domain.ErrQuoteExpired, quoteID, quote.ExpiresAt.UTC().Format("15:04:05"))
		}

		// 2. Create the order. One order per quote: a duplicate means it was used.
		holdAmount := quote.Total // BUY holds cash
		if quote.Side == domain.SideSell {
			holdAmount = quote.Qty // SELL holds shares
		}
		order = domain.Order{
			ID:            orderID,
			UserID:        userID,
			QuoteID:       quote.ID,
			Symbol:        quote.Symbol,
			Side:          quote.Side,
			Qty:           quote.Qty,
			Price:         quote.Price,
			HoldAmount:    holdAmount,
			HoldRemaining: holdAmount,
			Status:        domain.StatusPendingSubmit,
		}
		err = s.orderRepo.CreateOrder(ctx, tx, order)
		if errors.Is(err, domain.ErrDuplicate) {
			return fmt.Errorf("%w: quote %s", domain.ErrQuoteAlreadyUsed, quoteID)
		}
		if err != nil {
			return err
		}

		// 3. Hold what the order needs.
		if order.Side == domain.SideBuy {
			if _, err := s.holdCash(ctx, tx, userID, holdAmount, orderID); err != nil {
				return err
			}
		} else {
			if err := s.holdShares(ctx, tx, userID, order.Symbol, holdAmount); err != nil {
				return err
			}
		}

		// 4. Queue the submission to the provider.
		if err := s.jobRepo.CreateJob(ctx, tx, s.newJob(ctx, domain.JobSubmitOrder, &orderID, nil)); err != nil {
			return err
		}

		// 5. Record it in the activity feed.
		if err := s.addOrderActivity(ctx, tx, order, "order.placed", fmt.Sprintf("Placed %s %d %s at %s",
			order.Side, order.Qty, order.Symbol, domain.FormatMoney(order.Price))); err != nil {
			return err
		}

		order, err = s.orderRepo.GetOrder(ctx, tx, orderID)
		return err
	})
	if err != nil {
		return nil, err
	}

	s.log.InfoContext(ctx, "order placed", "order_id", order.ID, "side", order.Side, "qty", order.Qty,
		"symbol", order.Symbol, "price", order.Price, "hold", order.HoldAmount)
	s.notifyJobsReady()
	return &order, nil
}

// ListOrders returns the user's orders, newest first. status is "open"
// (not final) or "all".
func (s *Service) ListOrders(ctx context.Context, userID int64, status string) ([]domain.Order, error) {
	if status != "open" && status != "all" {
		return nil, fmt.Errorf("%w: status must be open or all", domain.ErrInvalidArgument)
	}

	var orders []domain.Order
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		if status == "open" {
			orders, err = s.orderRepo.ListOrdersNotInStatus(ctx, tx, userID, domain.FinalStatuses, maxOrdersListed)
		} else {
			orders, err = s.orderRepo.ListOrders(ctx, tx, userID, maxOrdersListed)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return orders, nil
}

// GetOrder returns one of the user's orders with its fills and activity.
func (s *Service) GetOrder(ctx context.Context, userID int64, orderID string) (*domain.OrderDetail, error) {
	id, ok := normalizeOrderID(orderID)
	if !ok {
		return nil, fmt.Errorf("%w: order %s", domain.ErrNotFound, orderID)
	}
	orderID = id

	var detail domain.OrderDetail
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		if detail.Order, err = s.orderRepo.GetOrder(ctx, tx, orderID); err != nil {
			return err
		}
		if detail.UserID != userID {
			return domain.ErrNotFound
		}
		if detail.Fills, err = s.orderRepo.ListFills(ctx, tx, orderID); err != nil {
			return err
		}
		detail.Activity, err = s.activityRepo.ListOrderActivity(ctx, tx, orderID)
		return err
	})
	if errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("%w: order %s", domain.ErrNotFound, orderID)
	}
	if err != nil {
		return nil, err
	}
	return &detail, nil
}

// submitOrder is the submit_order job: send a PENDING_SUBMIT order to the
// provider and record the answer. A returned error means "outcome unknown,
// try again later".
func (s *Service) submitOrder(ctx context.Context, orderID string, attempt int) error {
	var order domain.Order
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		order, err = s.orderRepo.GetOrder(ctx, tx, orderID)
		return err
	})
	if err != nil {
		return err
	}

	// Already resolved (accepted, rejected, or cancelled before submission).
	if order.Status != domain.StatusPendingSubmit {
		return nil
	}

	// A retry may follow an accept that reached the provider but whose answer
	// was lost (a 500 or a timeout). If the provider has the order, adopt it
	// instead of accepting again.
	if attempt > 1 {
		existing, err := s.broker.GetOrderByClientOrderID(ctx, order.ID)
		if err == nil {
			return s.markOrderSubmitted(ctx, order.ID, existing)
		}
		var apiErr *provider.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 404 {
			return err
		}
	}

	accepted, err := s.broker.AcceptQuote(ctx, order.QuoteID, order.ID)
	if err == nil {
		return s.markOrderSubmitted(ctx, order.ID, accepted)
	}

	// A definite "no" from the provider rejects the order. Anything else
	// (5xx, timeout, network, auth) leaves the outcome unknown: retry.
	var apiErr *provider.APIError
	if errors.As(err, &apiErr) && isRejection(apiErr) {
		return s.rejectOrder(ctx, order.ID, apiErr.Code)
	}
	return err
}

// isRejection reports whether the provider definitely refused the accept.
func isRejection(e *provider.APIError) bool {
	switch e.Status {
	case 400, 404, 409, 422:
		return true
	}
	return false
}

// markOrderSubmitted records that the provider has the order: store its id
// and move PENDING_SUBMIT → OPEN. The provider's later events (fills,
// cancels) are applied from its snapshots by the webhook processor, so the
// provider version is not recorded here.
func (s *Service) markOrderSubmitted(ctx context.Context, orderID string, po provider.Order) error {
	return s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		order, err := s.orderRepo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}

		if order.ProviderOrderID == nil {
			if err := s.orderRepo.UpdateOrderProviderID(ctx, tx, orderID, po.ID); err != nil {
				return err
			}
		}

		// Something else (a webhook, a local cancel) already moved it on.
		if order.Status != domain.StatusPendingSubmit {
			return nil
		}

		if err := s.orderRepo.UpdateOrderStatus(ctx, tx, orderID, domain.StatusOpen); err != nil {
			return err
		}
		s.log.InfoContext(ctx, "order submitted", "order_id", orderID, "provider_order_id", po.ID)
		return s.addOrderActivity(ctx, tx, order, "order.submitted",
			fmt.Sprintf("%s %d %s sent to the broker", order.Side, order.Qty, order.Symbol))
	})
}

// rejectOrder moves a PENDING_SUBMIT order to REJECTED and releases its hold.
func (s *Service) rejectOrder(ctx context.Context, orderID, reason string) error {
	return s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		order, err := s.orderRepo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if order.Status != domain.StatusPendingSubmit {
			return nil
		}

		if err := s.releaseOrderHold(ctx, tx, order); err != nil {
			return err
		}
		if err := s.orderRepo.UpdateOrderStatus(ctx, tx, orderID, domain.StatusRejected); err != nil {
			return err
		}
		if err := s.orderRepo.UpdateOrderReason(ctx, tx, orderID, reason); err != nil {
			return err
		}
		s.log.WarnContext(ctx, "order rejected by provider", "order_id", orderID, "reason", reason)
		return s.addOrderActivity(ctx, tx, order, "order.rejected",
			fmt.Sprintf("Rejected: %s", reason))
	})
}

// releaseOrderHold gives back whatever the order still holds (cash for a
// BUY, shares for a SELL) and sets hold_remaining to 0.
func (s *Service) releaseOrderHold(ctx context.Context, tx pgx.Tx, order domain.Order) error {
	if order.HoldRemaining == 0 {
		return nil
	}
	if order.Side == domain.SideBuy {
		if _, err := s.releaseCash(ctx, tx, order.UserID, order.HoldRemaining, order.ID); err != nil {
			return err
		}
	} else {
		if err := s.releaseShares(ctx, tx, order.UserID, order.Symbol, order.HoldRemaining); err != nil {
			return err
		}
	}
	return s.orderRepo.UpdateOrderHoldRemaining(ctx, tx, order.ID, 0)
}

// addOrderActivity records an activity item about an order.
func (s *Service) addOrderActivity(ctx context.Context, tx pgx.Tx, order domain.Order, kind, message string) error {
	data, err := json.Marshal(map[string]any{"status": order.Status, "symbol": order.Symbol, "side": order.Side, "qty": order.Qty})
	if err != nil {
		return err
	}
	return s.activityRepo.CreateActivity(ctx, tx, domain.Activity{
		UserID:  order.UserID,
		OrderID: &order.ID,
		Kind:    kind,
		Message: message,
		Data:    data,
	})
}
