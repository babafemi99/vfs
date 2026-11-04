// Package worker runs background jobs. It is a driver like the HTTP
// transport: it only loops and calls the service, which owns the logic.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/metrics"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
	"github.com/babafem99/vfs/tradeapp/internal/reqid"
	"github.com/babafem99/vfs/tradeapp/internal/service"
)

type Worker struct {
	svc        *service.Service
	log        *slog.Logger
	metrics    *metrics.App
	poll       time.Duration // how often to look for due jobs
	sweepEvery time.Duration // how often to check for stuck orders
	reconEvery time.Duration // how often to reconcile with the provider
	batchSize  int
}

func New(svc *service.Service, log *slog.Logger, m *metrics.App, poll, sweepEvery, reconEvery time.Duration) *Worker {
	return &Worker{svc: svc, log: log, metrics: m, poll: poll, sweepEvery: sweepEvery, reconEvery: reconEvery, batchSize: 10}
}

// Run processes jobs until ctx is cancelled. It wakes up every poll
// interval, immediately when the service queues new jobs, runs the
// stuck-order sweeper every sweepEvery and reconciliation every reconEvery.
func (w *Worker) Run(ctx context.Context) {
	poll := time.NewTicker(w.poll)
	defer poll.Stop()
	sweep := time.NewTicker(w.sweepEvery)
	defer sweep.Stop()
	recon := time.NewTicker(w.reconEvery)
	defer recon.Stop()

	for {
		w.drain(ctx)

		select {
		case <-ctx.Done():
			return
		case <-poll.C:
		case <-w.svc.JobsReady():
		case <-sweep.C:
			w.sweep(ctx)
		case <-recon.C:
			w.reconcile(ctx)
		}
	}
}

// sweep polls the provider for orders that have gone quiet.
func (w *Worker) sweep(ctx context.Context) {
	checked, err := w.svc.SweepStuckOrders(ctx)
	if err != nil {
		w.log.WarnContext(ctx, "sweep", "checked", checked, "err", err)
		return
	}
	if checked > 0 {
		w.log.InfoContext(ctx, "sweep", "checked", checked)
	}
}

// reconcile compares our books with the provider's and logs the outcome;
// the full report is stored and served by GET /api/recon/latest.
func (w *Worker) reconcile(ctx context.Context) {
	report, err := w.svc.RunReconciliation(ctx)
	if err != nil {
		w.log.WarnContext(ctx, "reconciliation", "err", err)
		return
	}
	level := slog.LevelInfo
	if !report.OK {
		level = slog.LevelWarn
	}
	w.log.Log(ctx, level, "reconciliation", "run_id", report.RunID, "ok", report.OK,
		"checked", report.OrdersChecked, "skipped", report.OrdersSkipped,
		"mismatches", len(report.Mismatches), "invariants_ok", report.InvariantsOK, "open_breaks", report.OpenBreaks)
}

// drain claims and runs batches of due jobs until none are left.
func (w *Worker) drain(ctx context.Context) {
	for ctx.Err() == nil {
		jobs, err := w.svc.ClaimJobs(ctx, w.batchSize)
		if err != nil {
			w.log.ErrorContext(ctx, "claim jobs", "err", err)
			return
		}
		if len(jobs) == 0 {
			return
		}

		for _, job := range jobs {
			w.run(ctx, job)
		}
	}
}

// run runs one job with the id of the request that queued it in the
// context, so every log line about it (here and in the service) carries it.
func (w *Worker) run(ctx context.Context, job domain.Job) {
	if job.RequestID != nil {
		ctx = reqid.With(ctx, *job.RequestID)
	}
	outcome, err := w.svc.RunJob(ctx, job)
	switch {
	case outcome == domain.JobDead:
		// The alert hook: a job gave up and needs a human (see /api/admin/jobs/stats).
		w.metrics.Jobs.Inc(job.Kind, "dead")
		w.log.WarnContext(ctx, "job dead-lettered", "job_id", job.ID, "kind", job.Kind, "attempts", job.Attempts,
			"order_id", deref(job.OrderID), "err", err)
	case errors.Is(err, provider.ErrCircuitOpen):
		w.metrics.Jobs.Inc(job.Kind, "deferred")
		w.log.InfoContext(ctx, "job deferred: provider circuit open", "job_id", job.ID, "kind", job.Kind)
	case err != nil:
		w.metrics.Jobs.Inc(job.Kind, "failed")
		w.log.WarnContext(ctx, "job failed", "job_id", job.ID, "kind", job.Kind, "attempt", job.Attempts, "err", err)
	default:
		w.metrics.Jobs.Inc(job.Kind, "done")
		w.log.InfoContext(ctx, "job done", "job_id", job.ID, "kind", job.Kind, "attempt", job.Attempts)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
