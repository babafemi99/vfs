package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

type ActivityRepo struct{}

func (ActivityRepo) CreateActivity(ctx context.Context, tx pgx.Tx, a domain.Activity) error {
	stmt := `INSERT INTO activity (user_id, order_id, kind, message, data)
	         VALUES ($1, $2, $3, $4, $5)`

	_, err := tx.Exec(ctx, stmt, a.UserID, a.OrderID, a.Kind, a.Message, a.Data)
	return err
}

// ListActivity returns up to limit items with id > afterID, oldest first.
func (ActivityRepo) ListActivity(ctx context.Context, tx pgx.Tx, userID, afterID int64, limit int) ([]domain.Activity, error) {
	stmt := `
		SELECT id, user_id, order_id, kind, message, data, created_at
		FROM activity
		WHERE user_id = $1 AND id > $2
		ORDER BY id
		LIMIT $3`

	rows, err := tx.Query(ctx, stmt, userID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.Activity{}
	for rows.Next() {
		var a domain.Activity
		if err := rows.Scan(&a.ID, &a.UserID, &a.OrderID, &a.Kind, &a.Message, &a.Data, &a.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

// ListOrderActivity returns an order's activity, oldest first.
func (ActivityRepo) ListOrderActivity(ctx context.Context, tx pgx.Tx, orderID string) ([]domain.Activity, error) {
	stmt := `
		SELECT id, user_id, order_id, kind, message, data, created_at
		FROM activity
		WHERE order_id = $1
		ORDER BY id`

	rows, err := tx.Query(ctx, stmt, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.Activity{}
	for rows.Next() {
		var a domain.Activity
		if err := rows.Scan(&a.ID, &a.UserID, &a.OrderID, &a.Kind, &a.Message, &a.Data, &a.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}
