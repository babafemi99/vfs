// Package dbtest gives each test its own migrated Postgres schema in the test
// database, so packages and tests can run in parallel without interfering.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/babafem99/vfs/tradeapp/internal/db"
	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/repository"
	"github.com/babafem99/vfs/tradeapp/pkg/lid"
)

const defaultURL = "postgres://localhost:5432/tradeapp_test?sslmode=disable"

// URL is TEST_DATABASE_URL or the local default.
func URL() string {
	if u := os.Getenv("TEST_DATABASE_URL"); u != "" {
		return u
	}
	return defaultURL
}

// New creates a fresh schema, migrates it and returns a pool bound to it.
// The schema is dropped when the test ends.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	schema := "t_" + randomHex(6)

	admin, err := pgx.Connect(ctx, URL())
	if err != nil {
		t.Fatalf("connect to test database (%s): %v", URL(), err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close(ctx)
		t.Fatalf("create schema: %v", err)
	}
	admin.Close(ctx)

	cfg, err := pgxpool.ParseConfig(URL())
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 30
	pool, err := db.OpenConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if c, err := pgx.Connect(ctx, URL()); err == nil {
			_, _ = c.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
			c.Close(ctx)
		}
	})
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

// AssertInvariants fails the test if any invariant reported by check is
// broken. Pass the service's CheckInvariants.
func AssertInvariants(t testing.TB, check func(context.Context) ([]domain.InvariantResult, error)) {
	t.Helper()
	results, err := check(context.Background())
	if err != nil {
		t.Fatalf("invariants: %v", err)
	}
	for _, r := range results {
		if !r.OK {
			t.Errorf("invariant %s broken: %s", r.Name, r.Detail)
		}
	}
}

// InsertOrder writes a quote and an order that holds hold, standing in for
// order placement until it exists. It returns the order id, and an error
// instead of failing the test so goroutines can call it.
func InsertOrder(ctx context.Context, tx pgx.Tx, userID int64, symbol, side string, qty, price, fee, hold int64, status string) (string, error) {
	quote := domain.Quote{
		ID:        "qt_" + randomHex(8),
		Symbol:    symbol,
		Side:      side,
		Qty:       qty,
		Price:     price,
		Fee:       fee,
		Total:     qty*price + fee,
		ExpiresAt: time.Now().Add(time.Minute),
	}
	if side == domain.SideSell {
		quote.Total = qty*price - fee
	}
	if err := (repository.QuoteRepo{}).CreateQuote(ctx, tx, quote); err != nil {
		return "", fmt.Errorf("insert quote: %w", err)
	}

	order := domain.Order{
		ID:            lid.NewOrder(),
		UserID:        userID,
		QuoteID:       quote.ID,
		Symbol:        symbol,
		Side:          side,
		Qty:           qty,
		Price:         price,
		HoldAmount:    hold,
		HoldRemaining: hold,
		Status:        status,
	}
	if err := (repository.OrderRepo{}).CreateOrder(ctx, tx, order); err != nil {
		return "", fmt.Errorf("insert order: %w", err)
	}
	return order.ID, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
