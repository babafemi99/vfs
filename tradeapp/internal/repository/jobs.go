package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

type JobRepo struct{}

func (JobRepo) CreateJob(ctx context.Context, tx pgx.Tx, j domain.Job) error {
	stmt := `INSERT INTO jobs (kind, order_id, event_id, status, attempts, max_attempts, run_at, give_up_at)
	         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := tx.Exec(ctx, stmt, j.Kind, j.OrderID, j.EventID, j.Status, j.Attempts, j.MaxAttempts, j.RunAt, j.GiveUpAt)
	return err
}

// GetDueJobsForUpdate locks up to limit jobs of these kinds that are ready
// to run at now: pending jobs whose run_at has come, and running jobs whose
// lease expired (their worker died). Rows locked by another worker are skipped.
func (JobRepo) GetDueJobsForUpdate(ctx context.Context, tx pgx.Tx, now time.Time, kinds []string, limit int) ([]domain.Job, error) {
	stmt := `
		SELECT id, kind, order_id, event_id, status, attempts, max_attempts, run_at, give_up_at,
		       locked_until, last_error, created_at, updated_at
		FROM jobs
		WHERE kind = ANY($2)
		  AND ((status = 'pending' AND run_at <= $1)
		    OR (status = 'running' AND locked_until < $1))
		ORDER BY run_at
		LIMIT $3
		FOR UPDATE SKIP LOCKED`

	rows, err := tx.Query(ctx, stmt, now, kinds, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := []domain.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (JobRepo) GetJobForUpdate(ctx context.Context, tx pgx.Tx, id int64) (domain.Job, error) {
	stmt := `
		SELECT id, kind, order_id, event_id, status, attempts, max_attempts, run_at, give_up_at,
		       locked_until, last_error, created_at, updated_at
		FROM jobs
		WHERE id = $1
		FOR UPDATE`

	j, err := scanJob(tx.QueryRow(ctx, stmt, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Job{}, domain.ErrNotFound
	}
	return j, err
}

// GetLatestOrderJobForUpdate locks the most recent job of this kind for an order.
func (JobRepo) GetLatestOrderJobForUpdate(ctx context.Context, tx pgx.Tx, orderID, kind string) (domain.Job, error) {
	stmt := `
		SELECT id, kind, order_id, event_id, status, attempts, max_attempts, run_at, give_up_at,
		       locked_until, last_error, created_at, updated_at
		FROM jobs
		WHERE order_id = $1 AND kind = $2
		ORDER BY id DESC
		LIMIT 1
		FOR UPDATE`

	j, err := scanJob(tx.QueryRow(ctx, stmt, orderID, kind))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Job{}, domain.ErrNotFound
	}
	return j, err
}

// ListJobs returns up to limit jobs with this status, most recently updated first.
func (JobRepo) ListJobs(ctx context.Context, tx pgx.Tx, status string, limit int) ([]domain.Job, error) {
	stmt := `
		SELECT id, kind, order_id, event_id, status, attempts, max_attempts, run_at, give_up_at,
		       locked_until, last_error, created_at, updated_at
		FROM jobs
		WHERE status = $1
		ORDER BY updated_at DESC
		LIMIT $2`

	rows, err := tx.Query(ctx, stmt, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := []domain.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// CountJobs counts jobs by kind and status.
func (JobRepo) CountJobs(ctx context.Context, tx pgx.Tx) ([]domain.JobCount, error) {
	stmt := `
		SELECT kind, status, count(*)
		FROM jobs
		GROUP BY kind, status
		ORDER BY kind, status`

	rows, err := tx.Query(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := []domain.JobCount{}
	for rows.Next() {
		var c domain.JobCount
		if err := rows.Scan(&c.Kind, &c.Status, &c.Count); err != nil {
			return nil, err
		}
		counts = append(counts, c)
	}
	return counts, rows.Err()
}

// GetOldestWaitingCreatedAt returns when the oldest pending or running job
// was created, or nil if there is none.
func (JobRepo) GetOldestWaitingCreatedAt(ctx context.Context, tx pgx.Tx) (*time.Time, error) {
	stmt := `SELECT min(created_at) FROM jobs WHERE status IN ('pending', 'running')`

	var oldest *time.Time
	err := tx.QueryRow(ctx, stmt).Scan(&oldest)
	return oldest, err
}

// ListJobsUpdatedBetweenForUpdate locks up to limit jobs with this status,
// of these kinds, last updated in [from, to), oldest first.
func (JobRepo) ListJobsUpdatedBetweenForUpdate(ctx context.Context, tx pgx.Tx, status string, kinds []string, from, to time.Time, limit int) ([]domain.Job, error) {
	stmt := `
		SELECT id, kind, order_id, event_id, status, attempts, max_attempts, run_at, give_up_at,
		       locked_until, last_error, created_at, updated_at
		FROM jobs
		WHERE status = $1 AND kind = ANY($2) AND updated_at >= $3 AND updated_at < $4
		ORDER BY updated_at
		LIMIT $5
		FOR UPDATE`

	rows, err := tx.Query(ctx, stmt, status, kinds, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := []domain.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// UpdateJob writes a job's status, attempts, schedule, lease and last error.
func (JobRepo) UpdateJob(ctx context.Context, tx pgx.Tx, j domain.Job) error {
	stmt := `
		UPDATE jobs
		SET status = $2, attempts = $3, run_at = $4, locked_until = $5, last_error = $6, give_up_at = $7, updated_at = now()
		WHERE id = $1`

	tag, err := tx.Exec(ctx, stmt, j.ID, j.Status, j.Attempts, j.RunAt, j.LockedUntil, j.LastError, j.GiveUpAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// scanJob reads one job row in the column order used above.
func scanJob(row pgx.Row) (domain.Job, error) {
	var j domain.Job
	err := row.Scan(
		&j.ID, &j.Kind, &j.OrderID, &j.EventID, &j.Status, &j.Attempts, &j.MaxAttempts, &j.RunAt, &j.GiveUpAt,
		&j.LockedUntil, &j.LastError, &j.CreatedAt, &j.UpdatedAt,
	)
	return j, err
}
