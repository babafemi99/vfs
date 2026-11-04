// Package repository is the Postgres data access layer. It only stores and
// fetches data: one SQL statement per method, no business rules.
//
//   - Every method takes the transaction it runs in (tx pgx.Tx).
//   - Writes return only an error. Reads return the data and an error.
//   - No row found is domain.ErrNotFound. An insert that hits an existing row
//     (ON CONFLICT DO NOTHING) is domain.ErrDuplicate.
package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) Repository {
	return Repository{pool: pool}
}

// RunInTx runs fn in a transaction: commit if fn returns nil, roll back if it
// returns an error or panics.
//
// Isolation is Read Committed. Money is protected by row locks
// (SELECT ... FOR UPDATE), and idempotency relies on a second request with the
// same key waiting for the first and then reading its committed result.
func (r Repository) RunInTx(ctx context.Context, fn func(tx pgx.Tx) error) (err error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}

	defer func() {
		if panicValue := recover(); panicValue != nil {
			_ = tx.Rollback(ctx)
			err = fmt.Errorf("panic in transaction: %v", panicValue)
			return
		}
		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				err = fmt.Errorf("rollback failed: %v (original error: %w)", rbErr, err)
			}
			return
		}
		if cmErr := tx.Commit(ctx); cmErr != nil {
			err = fmt.Errorf("commit failed: %w", cmErr)
		}
	}()

	return fn(tx)
}

// Ping checks the database is reachable.
func (r Repository) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}
