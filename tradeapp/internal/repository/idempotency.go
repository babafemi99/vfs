package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

type IdempotencyRepo struct{}

// CreateKey inserts a new idempotency record.
// domain.ErrDuplicate means the key already exists.
func (IdempotencyRepo) CreateKey(ctx context.Context, tx pgx.Tx, rec domain.IdempotencyRecord) error {
	stmt := `INSERT INTO idempotency_keys (user_id, key, request_hash, request_path, action, status, created_at, updated_at, expires_at)
	         VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $8)
	         ON CONFLICT (user_id, key) DO NOTHING`

	tag, err := tx.Exec(ctx, stmt, rec.UserID, rec.Key, rec.RequestHash, rec.RequestPath, rec.Action, rec.Status, rec.CreatedAt, rec.ExpiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrDuplicate
	}
	return nil
}

func (IdempotencyRepo) GetKey(ctx context.Context, tx pgx.Tx, userID int64, key string) (domain.IdempotencyRecord, error) {
	stmt := `
		SELECT user_id, key, request_hash, request_path, action, status, response_code, response_body,
		       created_at, updated_at, expires_at
		FROM idempotency_keys
		WHERE user_id = $1 AND key = $2`

	var rec domain.IdempotencyRecord
	err := tx.QueryRow(ctx, stmt, userID, key).
		Scan(&rec.UserID, &rec.Key, &rec.RequestHash, &rec.RequestPath, &rec.Action, &rec.Status,
			&rec.ResponseCode, &rec.ResponseBody, &rec.CreatedAt, &rec.UpdatedAt, &rec.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.IdempotencyRecord{}, domain.ErrNotFound
	}
	return rec, err
}

// ReclaimKey restarts a released key (FAILED with no stored response) for a
// new attempt. domain.ErrNotFound means the key is not released: it is
// running, or has a stored response.
func (IdempotencyRepo) ReclaimKey(ctx context.Context, tx pgx.Tx, rec domain.IdempotencyRecord) error {
	stmt := `UPDATE idempotency_keys
	         SET status = $3, request_hash = $4, request_path = $5, action = $6, updated_at = $7, expires_at = $8
	         WHERE user_id = $1 AND key = $2 AND status = 'FAILED' AND response_code IS NULL`

	tag, err := tx.Exec(ctx, stmt, rec.UserID, rec.Key, rec.Status, rec.RequestHash, rec.RequestPath, rec.Action, rec.UpdatedAt, rec.ExpiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// UpdateKeyFinished sets the outcome of a STARTED key: status plus the
// response (nil code and body to release it). domain.ErrNotFound means the
// key is no longer STARTED (reaped as a zombie meanwhile).
func (IdempotencyRepo) UpdateKeyFinished(ctx context.Context, tx pgx.Tx, userID int64, key string,
	status domain.IdempotencyStatus, code *int, body []byte, at time.Time) error {
	stmt := `UPDATE idempotency_keys
	         SET status = $3, response_code = $4, response_body = $5, updated_at = $6
	         WHERE user_id = $1 AND key = $2 AND status = 'STARTED'`

	tag, err := tx.Exec(ctx, stmt, userID, key, status, code, body, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// UpdateStaleKeysFailed releases keys STARTED before the given time (their
// request crashed or hung): FAILED with no response.
func (IdempotencyRepo) UpdateStaleKeysFailed(ctx context.Context, tx pgx.Tx, startedBefore, at time.Time) error {
	stmt := `UPDATE idempotency_keys
	         SET status = 'FAILED', updated_at = $2
	         WHERE status = 'STARTED' AND updated_at < $1`

	_, err := tx.Exec(ctx, stmt, startedBefore, at)
	return err
}

func (IdempotencyRepo) DeleteKeysExpiredBefore(ctx context.Context, tx pgx.Tx, before time.Time) error {
	stmt := `DELETE FROM idempotency_keys WHERE expires_at < $1`

	_, err := tx.Exec(ctx, stmt, before)
	return err
}
