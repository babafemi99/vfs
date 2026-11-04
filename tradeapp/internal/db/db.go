package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLock is the advisory lock key that serialises concurrent migrators.
const migrationLock = 7_239_001

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	return OpenConfig(ctx, cfg)
}

func OpenConfig(ctx context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
			return fmt.Errorf("lock migrations: %w", err)
		}
		if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
			return fmt.Errorf("create schema_migrations: %w", err)
		}
		for _, name := range names {
			var done bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, name,
			).Scan(&done); err != nil {
				return err
			}
			if done {
				continue
			}
			sql, err := migrations.ReadFile(name)
			if err != nil {
				return err
			}
			// No arguments, so pgx uses the simple protocol and multi-statement files work.
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return fmt.Errorf("apply %s: %w", name, err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
				return err
			}
		}
		return nil
	})
}
