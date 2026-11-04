package sys

import (
	"context"
	"errors"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/metrics"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
	"github.com/babafem99/vfs/tradeapp/internal/service"
)

// RegisterCollectors adds the scrape-time gauges: numbers that live in the
// database or in the breaker, read fresh on each scrape (the same numbers as
// GET /api/admin/jobs/stats, in Prometheus form).
func RegisterCollectors(m *metrics.App, trade *service.Service) {
	m.Registry.AddCollector(func(ctx context.Context, w *metrics.Writer) error {
		stats, err := trade.JobStats(ctx)
		if err != nil {
			return err
		}
		for _, c := range stats.Counts {
			w.Gauge("jobs", "Outbox jobs by kind and status.", float64(c.Count),
				metrics.Labels{"kind": c.Kind, "status": c.Status})
		}
		w.Gauge("jobs_dead", "Jobs in the dead-letter queue (alert when > 0).", float64(stats.Dead), nil)
		w.Gauge("jobs_oldest_waiting_seconds", "Age of the oldest pending or running job (queue lag).",
			float64(stats.OldestWaitingSeconds), nil)
		for _, state := range []string{provider.CircuitClosed, provider.CircuitOpen, provider.CircuitHalfOpen} {
			v := 0.0
			if stats.Circuit == state {
				v = 1
			}
			w.Gauge("provider_circuit_state", "Provider circuit breaker state (1 = current).", v,
				metrics.Labels{"state": state})
		}
		return nil
	})

	// The latest reconciliation run: what to alert on when the books and
	// the broker disagree, or when reconciliation stops running.
	m.Registry.AddCollector(func(ctx context.Context, w *metrics.Writer) error {
		report, err := trade.LatestReconciliation(ctx)
		if errors.Is(err, domain.ErrNotFound) || (err == nil && report == nil) {
			return nil // no run yet
		}
		if err != nil {
			return err
		}
		w.Gauge("recon_mismatches", "Mismatches found by the latest reconciliation run.", float64(len(report.Mismatches)), nil)
		w.Gauge("recon_open_breaks", "Open breaks (contradictions recorded while processing).", float64(report.OpenBreaks), nil)
		w.Gauge("recon_invariants_ok", "1 if every ledger invariant held in the latest run.", boolFloat(report.InvariantsOK), nil)
		w.Gauge("recon_last_run_timestamp_seconds", "When the latest reconciliation run finished (Unix time).",
			float64(report.FinishedAt.Unix()), nil)
		return nil
	})
}

func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
