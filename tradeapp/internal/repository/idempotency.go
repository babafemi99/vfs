package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

type IdempotencyRepo struct{}

// CreateKey inserts a new idempotency key with no response yet.
// domain.ErrDuplicate means the key already exists.
func (IdempotencyRepo) CreateKey(ctx context.Context, tx pgx.Tx, rec domain.IdempotencyRecord) error {
	stmt := `INSERT INTO idempotency_keys (user_id, key, request_hash)
	         VALUES ($1, $2, $3)
	         ON CONFLICT (user_id, key) DO NOTHING`

	tag, err := tx.Exec(ctx, stmt, rec.UserID, rec.Key, rec.RequestHash)
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
		SELECT user_id, key, request_hash, response_code, response_body, created_at
		FROM idempotency_keys
		WHERE user_id = $1 AND key = $2`

	var rec domain.IdempotencyRecord
	err := tx.QueryRow(ctx, stmt, userID, key).
		Scan(&rec.UserID, &rec.Key, &rec.RequestHash, &rec.ResponseCode, &rec.ResponseBody, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.IdempotencyRecord{}, domain.ErrNotFound
	}
	return rec, err
}

func (IdempotencyRepo) UpdateKeyResponse(ctx context.Context, tx pgx.Tx, userID int64, key string, code int, body []byte) error {
	stmt := `UPDATE idempotency_keys
	         SET response_code = $3, response_body = $4
	         WHERE user_id = $1 AND key = $2`

	tag, err := tx.Exec(ctx, stmt, userID, key, code, body)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (IdempotencyRepo) DeleteKey(ctx context.Context, tx pgx.Tx, userID int64, key string) error {
	stmt := `DELETE FROM idempotency_keys WHERE user_id = $1 AND key = $2`

	_, err := tx.Exec(ctx, stmt, userID, key)
	return err
}

func (IdempotencyRepo) DeleteKeysCreatedBefore(ctx context.Context, tx pgx.Tx, before time.Time) error {
	stmt := `DELETE FROM idempotency_keys WHERE created_at < $1`

	_, err := tx.Exec(ctx, stmt, before)
	return err
}
