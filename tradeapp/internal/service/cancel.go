package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
)

// CancelOrder cancels one of the user's orders. It never releases money on
// the request itself, only when the order reaches a final state.
//
//   - Not yet sent to the provider (submit job still pending): cancelled
//     here and now, hold released. pending = false.
//   - Working at the provider (OPEN, PARTIALLY_FILLED): moves to
//     PENDING_CANCEL and queues a cancel_order job; the outcome arrives as a
//     webhook. pending = true.
//   - Already PENDING_CANCEL: nothing new. pending = true.
//   - Final: domain.ErrOrderFinal.
func (s *Service) CancelOrder(ctx context.Context, userID int64, orderID string) (order *domain.Order, pending bool, err error) {
	id, ok := normalizeOrderID(orderID)
	if !ok {
		return nil, false, fmt.Errorf("%w: order %s", domain.ErrNotFound, orderID)
	}
	orderID = id

	queued := false
	var result domain.Order

	err = s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		o, err := s.orderRepo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if o.UserID != userID {
			return domain.ErrNotFound
		}

		switch o.Status {
		case domain.StatusPendingCancel:
			pending = true

		case domain.StatusPendingSubmit:
			if err := s.cancelBeforeSubmit(ctx, tx, o); err != nil {
				return err
			}

		case domain.StatusOpen, domain.StatusPartiallyFilled:
			if err := s.orderRepo.UpdateOrderStatus(ctx, tx, o.ID, domain.StatusPendingCancel); err != nil {
				return err
			}
			if err := s.jobRepo.CreateJob(ctx, tx, s.newJob(domain.JobCancelOrder, &o.ID, nil)); err != nil {
				return err
			}
			o.Status = domain.StatusPendingCancel
			if err := s.addOrderActivity(ctx, tx, o, "order.cancel_requested", statusMessage(o)); err != nil {
				return err
			}
			pending, queued = true, true

		default:
			return fmt.Errorf("%w: order %s is %s", domain.ErrOrderFinal, o.ID, o.Status)
		}

		result, err = s.orderRepo.GetOrder(ctx, tx, orderID)
		return err
	})
	if errors.Is(err, domain.ErrNotFound) {
		return nil, false, fmt.Errorf("%w: order %s", domain.ErrNotFound, orderID)
	}
	if err != nil {
		return nil, false, err
	}

	s.log.Info("cancel requested", "order_id", result.ID, "status", result.Status, "pending", pending)
	if queued {
		s.notifyJobsReady()
	}
	return &result, pending, nil
}

// cancelBeforeSubmit cancels a PENDING_SUBMIT order locally, which is only
// safe while its submit job has not started: locking the job row keeps the
// worker from claiming it meanwhile.
func (s *Service) cancelBeforeSubmit(ctx context.Context, tx pgx.Tx, o domain.Order) error {
	job, err := s.jobRepo.GetLatestOrderJobForUpdate(ctx, tx, o.ID, domain.JobSubmitOrder)
	if err != nil {
		return err
	}
	if job.Status != domain.JobPending {
		return fmt.Errorf("%w: order %s is being sent to the broker (job %s); try again shortly", domain.ErrConflict, o.ID, job.Status)
	}

	job.Status = domain.JobDone
	job.LastError = errorText(errors.New("cancelled before submission"))
	if err := s.jobRepo.UpdateJob(ctx, tx, job); err != nil {
		return err
	}
	if err := s.releaseOrderHold(ctx, tx, o); err != nil {
		return err
	}
	if err := s.orderRepo.UpdateOrderStatus(ctx, tx, o.ID, domain.StatusCancelled); err != nil {
		return err
	}
	o.Status = domain.StatusCancelled
	return s.addOrderActivity(ctx, tx, o, "order.status", "Cancelled before it was sent to the broker")
}

// cancelOrderAtProvider is the cancel_order job: ask the provider to cancel,
// and apply whatever order snapshot comes back. The final outcome normally
// arrives as a webhook.
func (s *Service) cancelOrderAtProvider(ctx context.Context, orderID string) error {
	var order domain.Order
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		order, err = s.orderRepo.GetOrder(ctx, tx, orderID)
		return err
	})
	if err != nil {
		return err
	}
	if order.Status != domain.StatusPendingCancel {
		return nil // already resolved
	}
	if order.ProviderOrderID == nil {
		return fmt.Errorf("%w: order %s has no provider id", errPermanent, orderID)
	}

	snap, err := s.broker.CancelOrder(ctx, *order.ProviderOrderID)
	var apiErr *provider.APIError
	switch {
	case err == nil:
		// 202: the provider is cancelling.
	case errors.As(err, &apiErr) && apiErr.Status == 409:
		// order_final: it finished first. Fetch its final state.
		if snap, err = s.broker.GetOrder(ctx, *order.ProviderOrderID); err != nil {
			return err
		}
	case errors.As(err, &apiErr) && apiErr.Status == 404:
		return fmt.Errorf("%w: provider has no order %s", errPermanent, *order.ProviderOrderID)
	default:
		return err // outcome unknown: retry
	}

	return s.applyPolledSnapshot(ctx, orderID, snap)
}

// applyPolledSnapshot applies a snapshot fetched from the provider (with all
// its fills) to an order, and marks the order as checked.
func (s *Service) applyPolledSnapshot(ctx context.Context, orderID string, snap provider.Order) error {
	return s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		order, err := s.orderRepo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if err := s.applySnapshot(ctx, tx, order, snap, snap.Fills, false); err != nil {
			return err
		}
		return s.orderRepo.TouchOrder(ctx, tx, orderID)
	})
}
