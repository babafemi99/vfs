package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
)

const sweepBatchSize = 50

// SweepStuckOrders polls the provider for active orders that have not
// changed for StuckAfter and applies what it says, through the same code as
// webhooks. This is how dropped webhooks are recovered. It returns how many
// orders it checked; one order's failure doesn't stop the others.
func (s *Service) SweepStuckOrders(ctx context.Context) (int, error) {
	before := s.now().Add(-s.cfg.StuckAfter)

	var stuck []domain.Order
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		stuck, err = s.orderRepo.ListOrdersUpdatedBefore(ctx, tx, domain.FinalStatuses, before, sweepBatchSize)
		return err
	})
	if err != nil {
		return 0, err
	}

	var errs []error
	for _, o := range stuck {
		if err := s.sweepOrder(ctx, o); err != nil {
			errs = append(errs, fmt.Errorf("order %s: %w", o.ID, err))
		}
	}
	return len(stuck), errors.Join(errs...)
}

func (s *Service) sweepOrder(ctx context.Context, o domain.Order) error {
	if o.Status == domain.StatusPendingSubmit {
		return s.sweepUnsubmitted(ctx, o)
	}

	var snap provider.Order
	var err error
	if o.ProviderOrderID != nil {
		snap, err = s.broker.GetOrder(ctx, *o.ProviderOrderID)
	} else {
		snap, err = s.broker.GetOrderByClientOrderID(ctx, o.ID)
	}
	if err != nil {
		return err
	}
	if err := s.applyPolledSnapshot(ctx, o.ID, snap); err != nil {
		return err
	}
	if o.Status == domain.StatusPendingCancel {
		return s.requeueDeadCancel(ctx, o.ID)
	}
	return nil
}

// requeueDeadCancel gives a cancel another go when its job died (the
// provider was unreachable for every attempt) and the order is still
// PENDING_CANCEL after polling the provider.
func (s *Service) requeueDeadCancel(ctx context.Context, orderID string) error {
	requeued := false
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		order, err := s.orderRepo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if order.Status != domain.StatusPendingCancel {
			return nil
		}
		job, err := s.jobRepo.GetLatestOrderJobForUpdate(ctx, tx, orderID, domain.JobCancelOrder)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		}
		if err != nil || job.Status != domain.JobDead {
			return err
		}

		job.Status = domain.JobPending
		job.RunAt = s.now()
		job.LockedUntil = nil
		s.resetRetryBudget(&job, job.RunAt)
		requeued = true
		return s.jobRepo.UpdateJob(ctx, tx, job)
	})
	if requeued {
		s.notifyJobsReady()
	}
	return err
}

// sweepUnsubmitted handles an order still PENDING_SUBMIT. While its submit
// job is queued or running, the worker owns it. Once the job has given up
// (dead), ask the provider: if it has the order, adopt it; if it doesn't and
// the quote has expired (so no retry can ever succeed), reject the order
// and release its hold.
func (s *Service) sweepUnsubmitted(ctx context.Context, o domain.Order) error {
	var job domain.Job
	var quote domain.Quote
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		if job, err = s.jobRepo.GetLatestOrderJobForUpdate(ctx, tx, o.ID, domain.JobSubmitOrder); err != nil {
			return err
		}
		quote, err = s.quoteRepo.GetQuote(ctx, tx, o.QuoteID)
		return err
	})
	if err != nil {
		return err
	}
	if job.Status == domain.JobPending || job.Status == domain.JobRunning {
		return nil
	}

	snap, err := s.broker.GetOrderByClientOrderID(ctx, o.ID)
	if err == nil {
		return s.applyPolledSnapshot(ctx, o.ID, snap)
	}
	var apiErr *provider.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 404 {
		return err
	}

	if s.now().Before(quote.ExpiresAt) {
		return nil // a manual retry of the job could still succeed
	}

	return s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		order, err := s.orderRepo.GetOrderForUpdate(ctx, tx, o.ID)
		if err != nil {
			return err
		}
		if order.Status != domain.StatusPendingSubmit {
			return nil
		}
		if err := s.recordBreak(ctx, tx, order.ID, domain.BreakSubmitNeverAccepted,
			fmt.Sprintf("submit job gave up and the provider has no order for client id %s", order.ID)); err != nil {
			return err
		}
		if err := s.releaseOrderHold(ctx, tx, order); err != nil {
			return err
		}
		if err := s.orderRepo.UpdateOrderStatus(ctx, tx, order.ID, domain.StatusRejected); err != nil {
			return err
		}
		if err := s.orderRepo.UpdateOrderReason(ctx, tx, order.ID, "submit_failed"); err != nil {
			return err
		}
		order.Status = domain.StatusRejected
		return s.addOrderActivity(ctx, tx, order, "order.status", "Rejected: could not be sent to the broker")
	})
}
