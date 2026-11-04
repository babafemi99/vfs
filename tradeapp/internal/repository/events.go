package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

type EventRepo struct{}

// CreateEvent stores a received webhook. domain.ErrDuplicate means this
// event id was already received.
func (EventRepo) CreateEvent(ctx context.Context, tx pgx.Tx, e domain.ProviderEvent) error {
	stmt := `INSERT INTO provider_events (event_id, type, provider_order_id, client_order_id, payload)
	         VALUES ($1, $2, $3, $4, $5)
	         ON CONFLICT (event_id) DO NOTHING`

	tag, err := tx.Exec(ctx, stmt, e.EventID, e.Type, e.ProviderOrderID, e.ClientOrderID, e.Payload)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrDuplicate
	}
	return nil
}

func (EventRepo) GetEventForUpdate(ctx context.Context, tx pgx.Tx, eventID string) (domain.ProviderEvent, error) {
	stmt := `
		SELECT event_id, type, provider_order_id, client_order_id, payload,
		       received_at, processed_at, process_error
		FROM provider_events
		WHERE event_id = $1
		FOR UPDATE`

	var e domain.ProviderEvent
	err := tx.QueryRow(ctx, stmt, eventID).Scan(
		&e.EventID, &e.Type, &e.ProviderOrderID, &e.ClientOrderID, &e.Payload,
		&e.ReceivedAt, &e.ProcessedAt, &e.ProcessError,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderEvent{}, domain.ErrNotFound
	}
	return e, err
}

func (EventRepo) CountEvents(ctx context.Context, tx pgx.Tx) (int, error) {
	stmt := `SELECT count(*) FROM provider_events`

	var n int
	err := tx.QueryRow(ctx, stmt).Scan(&n)
	return n, err
}

// UpdateEventProcessed marks an event processed, with an optional note.
func (EventRepo) UpdateEventProcessed(ctx context.Context, tx pgx.Tx, eventID string, processedAt time.Time, processError *string) error {
	stmt := `UPDATE provider_events SET processed_at = $2, process_error = $3 WHERE event_id = $1`

	tag, err := tx.Exec(ctx, stmt, eventID, processedAt, processError)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

type BreakRepo struct{}

func (BreakRepo) CreateBreak(ctx context.Context, tx pgx.Tx, b domain.Break) error {
	stmt := `INSERT INTO breaks (order_id, kind, detail) VALUES ($1, $2, $3)`

	_, err := tx.Exec(ctx, stmt, b.OrderID, b.Kind, b.Detail)
	return err
}

// ListBreaks returns the most recent breaks first.
func (BreakRepo) ListBreaks(ctx context.Context, tx pgx.Tx, limit int) ([]domain.Break, error) {
	stmt := `
		SELECT id, order_id, kind, detail, created_at
		FROM breaks
		ORDER BY id DESC
		LIMIT $1`

	rows, err := tx.Query(ctx, stmt, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	breaks := []domain.Break{}
	for rows.Next() {
		var b domain.Break
		if err := rows.Scan(&b.ID, &b.OrderID, &b.Kind, &b.Detail, &b.CreatedAt); err != nil {
			return nil, err
		}
		breaks = append(breaks, b)
	}
	return breaks, rows.Err()
}
