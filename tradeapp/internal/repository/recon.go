package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

type ReconRepo struct{}

// CreateRun stores a finished run with its report (JSON).
func (ReconRepo) CreateRun(ctx context.Context, tx pgx.Tx, r domain.ReconReport, report []byte) error {
	stmt := `INSERT INTO recon_runs (id, started_at, finished_at, orders_checked, mismatches, invariants_ok, report)
	         VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := tx.Exec(ctx, stmt, r.RunID, r.StartedAt, r.FinishedAt, r.OrdersChecked, len(r.Mismatches), r.InvariantsOK, report)
	return err
}

// GetLatestRunReport returns the most recent run's report (JSON).
func (ReconRepo) GetLatestRunReport(ctx context.Context, tx pgx.Tx) ([]byte, error) {
	stmt := `SELECT report FROM recon_runs ORDER BY finished_at DESC LIMIT 1`

	var report []byte
	err := tx.QueryRow(ctx, stmt).Scan(&report)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return report, err
}

// ListOrdersUpdatedBetween returns every order (all users, all statuses)
// last changed in [from, to).
func (ReconRepo) ListOrdersUpdatedBetween(ctx context.Context, tx pgx.Tx, from, to time.Time) ([]domain.Order, error) {
	stmt := `
		SELECT id, user_id, quote_id, provider_order_id, symbol, side, qty, price,
		       filled_qty, avg_fill_price, hold_amount, hold_remaining, status, reason,
		       provider_version, created_at, updated_at
		FROM orders
		WHERE updated_at >= $1 AND updated_at < $2
		ORDER BY updated_at`

	return queryOrders(ctx, tx, stmt, from, to)
}

func (ReconRepo) CountBreaks(ctx context.Context, tx pgx.Tx) (int, error) {
	stmt := `SELECT count(*) FROM breaks`

	var n int
	err := tx.QueryRow(ctx, stmt).Scan(&n)
	return n, err
}
