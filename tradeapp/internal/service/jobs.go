package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
	"github.com/babafem99/vfs/tradeapp/internal/reqid"
)

const maxJobsListed = 200

// errPermanent marks a job failure that retrying cannot fix; the job goes
// straight to the dead-letter queue.
var errPermanent = errors.New("permanent failure")

var (
	// allJobKinds is every job kind the worker runs.
	allJobKinds = []string{domain.JobSubmitOrder, domain.JobCancelOrder, domain.JobProcessEvent}
	// localJobKinds need only our database, never the provider, so they keep
	// running while the provider's circuit is open.
	localJobKinds = []string{domain.JobProcessEvent}
)

// newJob is a pending job due now, with the configured attempt cap and
// retry budget.
// newJob builds a pending job. It records the request id in ctx (if any) so
// the worker's logs for this job carry the id of the request that queued it.
func (s *Service) newJob(ctx context.Context, kind string, orderID, eventID *string) domain.Job {
	now := s.now()
	job := domain.Job{
		Kind:        kind,
		OrderID:     orderID,
		EventID:     eventID,
		Status:      domain.JobPending,
		MaxAttempts: s.cfg.JobMaxAttempts,
		RunAt:       now,
		RequestID:   reqid.Ptr(ctx),
	}
	s.resetRetryBudget(&job, now)
	return job
}

// resetRetryBudget gives a job a fresh budget: attempts from zero and a new
// give-up time.
func (s *Service) resetRetryBudget(job *domain.Job, now time.Time) {
	job.Attempts = 0
	job.GiveUpAt = nil
	if s.cfg.JobRetryBudget > 0 {
		giveUp := now.Add(s.cfg.JobRetryBudget)
		job.GiveUpAt = &giveUp
	}
}

// ClaimJobs locks up to limit due jobs for this worker: each becomes
// running, gets one more attempt and a lease of JobLease. A worker that dies
// mid-job loses its lease and another worker picks the job up again.
//
// While the provider's circuit breaker is refusing calls, only jobs that
// don't need the provider are claimed; the rest wait without using attempts.
func (s *Service) ClaimJobs(ctx context.Context, limit int) ([]domain.Job, error) {
	now := s.now()
	leaseEnd := now.Add(s.cfg.JobLease)
	kinds := allJobKinds
	if s.broker.CircuitRejecting() {
		kinds = localJobKinds
	}
	var claimed []domain.Job

	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		jobs, err := s.jobRepo.GetDueJobsForUpdate(ctx, tx, now, kinds, limit)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			job.Status = domain.JobRunning
			job.Attempts++
			job.LockedUntil = &leaseEnd
			if err := s.jobRepo.UpdateJob(ctx, tx, job); err != nil {
				return err
			}
			claimed = append(claimed, job)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// RunJob runs a claimed job and records the outcome:
//
//   - success: done.
//   - permanent failure: dead (the dead-letter queue) straight away.
//   - the circuit was open, so the provider was never called: deferred to
//     when the circuit may close. This uses no attempt, but the retry
//     budget keeps running.
//   - any other failure: pending again after an exponential backoff with
//     jitter, or dead once the retry budget or attempt cap is used up. The
//     last retry is placed exactly at the give-up time.
//
// It returns the job's status afterwards (done, pending, dead; empty if
// another worker had already taken it over) and the job's own error, so the
// worker can log it.
func (s *Service) RunJob(ctx context.Context, job domain.Job) (string, error) {
	jobErr := s.handleJob(ctx, job)
	outcome := ""

	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		current, err := s.jobRepo.GetJobForUpdate(ctx, tx, job.ID)
		if err != nil {
			return err
		}
		// Our lease expired and another worker reclaimed the job: its result wins.
		if current.Status != domain.JobRunning || current.Attempts != job.Attempts {
			return nil
		}

		now := s.now()
		current.LockedUntil = nil
		switch {
		case jobErr == nil:
			current.Status = domain.JobDone
			current.LastError = nil

		case errors.Is(jobErr, errPermanent):
			current.Status = domain.JobDead
			current.LastError = errorText(jobErr)

		case errors.Is(jobErr, provider.ErrCircuitOpen):
			current.Attempts-- // the provider was never called: not a real attempt
			current.LastError = errorText(jobErr)
			if current.GiveUpAt != nil && !now.Before(*current.GiveUpAt) {
				current.Status = domain.JobDead
			} else {
				current.Status = domain.JobPending
				current.RunAt = now.Add(s.cfg.JobBackoffBase)
			}

		case current.OutOfRetries(now):
			current.Status = domain.JobDead
			current.LastError = errorText(jobErr)

		default:
			current.Status = domain.JobPending
			current.RunAt = now.Add(s.retryDelay(current.Attempts))
			if current.GiveUpAt != nil && current.RunAt.After(*current.GiveUpAt) {
				current.RunAt = *current.GiveUpAt // one last try at the deadline
			}
			current.LastError = errorText(jobErr)
		}
		outcome = current.Status
		return s.jobRepo.UpdateJob(ctx, tx, current)
	})
	if err != nil {
		return "", fmt.Errorf("record job %d outcome: %w (job error: %v)", job.ID, err, jobErr)
	}
	return outcome, jobErr
}

// handleJob dispatches a job to the code for its kind.
func (s *Service) handleJob(ctx context.Context, job domain.Job) error {
	switch job.Kind {
	case domain.JobSubmitOrder:
		if job.OrderID == nil {
			return fmt.Errorf("%w: submit_order job %d has no order", errPermanent, job.ID)
		}
		return s.submitOrder(ctx, *job.OrderID, job.Attempts)
	case domain.JobCancelOrder:
		if job.OrderID == nil {
			return fmt.Errorf("%w: cancel_order job %d has no order", errPermanent, job.ID)
		}
		return s.cancelOrderAtProvider(ctx, *job.OrderID)
	case domain.JobProcessEvent:
		if job.EventID == nil {
			return fmt.Errorf("%w: process_event job %d has no event", errPermanent, job.ID)
		}
		return s.processEvent(ctx, *job.EventID)
	default:
		return fmt.Errorf("%w: unknown job kind %q", errPermanent, job.Kind)
	}
}

// retryDelay is the wait before the next attempt: full jitter, a random
// point between the base delay and the exponential backoff for this attempt.
// After an outage, jobs that failed together then retry spread over the
// whole window instead of all at once.
func (s *Service) retryDelay(attempt int) time.Duration {
	base := s.cfg.JobBackoffBase
	ceiling := domain.Backoff(attempt, base, s.cfg.JobBackoffMax)
	return base + time.Duration(rand.Int64N(int64(ceiling-base)+1))
}

// ListJobs returns jobs with this status (pending, running, done or dead),
// most recently updated first.
func (s *Service) ListJobs(ctx context.Context, status string) ([]domain.Job, error) {
	switch status {
	case domain.JobPending, domain.JobRunning, domain.JobDone, domain.JobDead:
	default:
		return nil, fmt.Errorf("%w: status must be pending, running, done or dead", domain.ErrInvalidArgument)
	}

	var jobs []domain.Job
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		jobs, err = s.jobRepo.ListJobs(ctx, tx, status, maxJobsListed)
		return err
	})
	if err != nil {
		return nil, err
	}
	return jobs, nil
}

// RetryJob takes a job out of the dead-letter queue: it becomes pending
// with a fresh set of attempts and runs as soon as a worker is free.
func (s *Service) RetryJob(ctx context.Context, jobID int64) (*domain.Job, error) {
	var job domain.Job
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		job, err = s.jobRepo.GetJobForUpdate(ctx, tx, jobID)
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: job %d", domain.ErrNotFound, jobID)
		}
		if err != nil {
			return err
		}
		if job.Status != domain.JobDead {
			return fmt.Errorf("%w: job %d is %s, only dead jobs can be retried", domain.ErrConflict, jobID, job.Status)
		}

		job.Status = domain.JobPending
		job.RunAt = s.now()
		job.LockedUntil = nil
		s.resetRetryBudget(&job, job.RunAt)
		return s.jobRepo.UpdateJob(ctx, tx, job)
	})
	if err != nil {
		return nil, err
	}

	s.notifyJobsReady()
	return &job, nil
}

func errorText(err error) *string {
	if err == nil {
		return nil
	}
	s := err.Error()
	return &s
}

// JobStats reports the queue's health: counts, the age of the oldest
// waiting job, the dead-letter queue size, the provider circuit, and the
// alerts that apply.
func (s *Service) JobStats(ctx context.Context) (*domain.JobStats, error) {
	stats := domain.JobStats{Circuit: s.broker.CircuitState()}
	var oldest *time.Time

	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		if stats.Counts, err = s.jobRepo.CountJobs(ctx, tx); err != nil {
			return err
		}
		oldest, err = s.jobRepo.GetOldestWaitingCreatedAt(ctx, tx)
		return err
	})
	if err != nil {
		return nil, err
	}

	for _, c := range stats.Counts {
		switch c.Status {
		case domain.JobPending:
			stats.Pending += c.Count
		case domain.JobRunning:
			stats.Running += c.Count
		case domain.JobDead:
			stats.Dead += c.Count
		}
	}
	var waiting time.Duration
	if oldest != nil {
		waiting = s.now().Sub(*oldest)
		stats.OldestWaitingSeconds = int64(waiting.Seconds())
	}
	stats.Alerts = domain.JobAlerts(stats.Dead, waiting, s.cfg.JobLagAlert, stats.Circuit != provider.CircuitClosed)
	stats.OK = len(stats.Alerts) == 0
	return &stats, nil
}

// maxBulkRetry caps how many dead jobs one bulk retry replays.
const maxBulkRetry = 1000

// RetryDeadJobs replays dead jobs in bulk, each with a fresh retry budget:
// all of them, or only one kind and/or those that died in [from, to). It
// returns how many were replayed. Safe because every job can run twice
// without harm.
func (s *Service) RetryDeadJobs(ctx context.Context, kind string, from, to *time.Time) (int, error) {
	kinds := allJobKinds
	if kind != "" {
		known := false
		for _, k := range allJobKinds {
			known = known || k == kind
		}
		if !known {
			return 0, fmt.Errorf("%w: unknown job kind %q", domain.ErrInvalidArgument, kind)
		}
		kinds = []string{kind}
	}
	start, end := time.Unix(0, 0), s.now().Add(time.Second)
	if from != nil {
		start = *from
	}
	if to != nil {
		end = *to
	}
	if !start.Before(end) {
		return 0, fmt.Errorf("%w: from must be before to", domain.ErrInvalidArgument)
	}

	retried := 0
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		dead, err := s.jobRepo.ListJobsUpdatedBetweenForUpdate(ctx, tx, domain.JobDead, kinds, start, end, maxBulkRetry)
		if err != nil {
			return err
		}
		now := s.now()
		for _, job := range dead {
			job.Status = domain.JobPending
			job.RunAt = now
			job.LockedUntil = nil
			s.resetRetryBudget(&job, now)
			if err := s.jobRepo.UpdateJob(ctx, tx, job); err != nil {
				return err
			}
			retried++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if retried > 0 {
		s.notifyJobsReady()
	}
	return retried, nil
}
