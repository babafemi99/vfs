package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

type OrderRepo struct{}

// CreateOrder inserts an order. The caller sets o.ID (it is also the
// provider client_order_id). domain.ErrDuplicate means an order already
// exists for this quote.
func (OrderRepo) CreateOrder(ctx context.Context, tx pgx.Tx, o domain.Order) error {
	stmt := `INSERT INTO orders (
	             id, user_id, quote_id, symbol, side, qty, price,
	             hold_amount, hold_remaining, status
	         ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	         ON CONFLICT (quote_id) DO NOTHING`

	tag, err := tx.Exec(ctx, stmt,
		o.ID, o.UserID, o.QuoteID, o.Symbol, o.Side, o.Qty, o.Price,
		o.HoldAmount, o.HoldRemaining, o.Status,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrDuplicate
	}
	return nil
}

func (OrderRepo) GetOrder(ctx context.Context, tx pgx.Tx, id string) (domain.Order, error) {
	stmt := `
		SELECT id, user_id, quote_id, provider_order_id, symbol, side, qty, price,
		       filled_qty, avg_fill_price, hold_amount, hold_remaining, status, reason,
		       provider_version, created_at, updated_at
		FROM orders
		WHERE id = $1`

	o, err := scanOrder(tx.QueryRow(ctx, stmt, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, domain.ErrNotFound
	}
	return o, err
}

func (OrderRepo) GetOrderForUpdate(ctx context.Context, tx pgx.Tx, id string) (domain.Order, error) {
	stmt := `
		SELECT id, user_id, quote_id, provider_order_id, symbol, side, qty, price,
		       filled_qty, avg_fill_price, hold_amount, hold_remaining, status, reason,
		       provider_version, created_at, updated_at
		FROM orders
		WHERE id = $1
		FOR UPDATE`

	o, err := scanOrder(tx.QueryRow(ctx, stmt, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, domain.ErrNotFound
	}
	return o, err
}

func (OrderRepo) GetOrderByProviderIDForUpdate(ctx context.Context, tx pgx.Tx, providerOrderID string) (domain.Order, error) {
	stmt := `
		SELECT id, user_id, quote_id, provider_order_id, symbol, side, qty, price,
		       filled_qty, avg_fill_price, hold_amount, hold_remaining, status, reason,
		       provider_version, created_at, updated_at
		FROM orders
		WHERE provider_order_id = $1
		FOR UPDATE`

	o, err := scanOrder(tx.QueryRow(ctx, stmt, providerOrderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, domain.ErrNotFound
	}
	return o, err
}

// ListOrdersUpdatedBefore returns orders whose status is not one of statuses
// and that have not changed since before, oldest change first.
func (OrderRepo) ListOrdersUpdatedBefore(ctx context.Context, tx pgx.Tx, statuses []string, before time.Time, limit int) ([]domain.Order, error) {
	stmt := `
		SELECT id, user_id, quote_id, provider_order_id, symbol, side, qty, price,
		       filled_qty, avg_fill_price, hold_amount, hold_remaining, status, reason,
		       provider_version, created_at, updated_at
		FROM orders
		WHERE status <> ALL($1) AND updated_at < $2
		ORDER BY updated_at
		LIMIT $3`

	return queryOrders(ctx, tx, stmt, statuses, before, limit)
}

// ListOrders returns a user's orders, newest first.
func (OrderRepo) ListOrders(ctx context.Context, tx pgx.Tx, userID int64, limit int) ([]domain.Order, error) {
	stmt := `
		SELECT id, user_id, quote_id, provider_order_id, symbol, side, qty, price,
		       filled_qty, avg_fill_price, hold_amount, hold_remaining, status, reason,
		       provider_version, created_at, updated_at
		FROM orders
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2`

	return queryOrders(ctx, tx, stmt, userID, limit)
}

// ListOrdersNotInStatus returns a user's orders whose status is not one of
// statuses, newest first.
func (OrderRepo) ListOrdersNotInStatus(ctx context.Context, tx pgx.Tx, userID int64, statuses []string, limit int) ([]domain.Order, error) {
	stmt := `
		SELECT id, user_id, quote_id, provider_order_id, symbol, side, qty, price,
		       filled_qty, avg_fill_price, hold_amount, hold_remaining, status, reason,
		       provider_version, created_at, updated_at
		FROM orders
		WHERE user_id = $1 AND status <> ALL($2)
		ORDER BY created_at DESC
		LIMIT $3`

	return queryOrders(ctx, tx, stmt, userID, statuses, limit)
}

func (OrderRepo) UpdateOrderStatus(ctx context.Context, tx pgx.Tx, id, status string) error {
	stmt := `UPDATE orders SET status = $2, updated_at = now() WHERE id = $1`

	tag, err := tx.Exec(ctx, stmt, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (OrderRepo) UpdateOrderReason(ctx context.Context, tx pgx.Tx, id, reason string) error {
	stmt := `UPDATE orders SET reason = $2, updated_at = now() WHERE id = $1`

	tag, err := tx.Exec(ctx, stmt, id, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (OrderRepo) UpdateOrderHoldRemaining(ctx context.Context, tx pgx.Tx, id string, holdRemaining int64) error {
	stmt := `UPDATE orders SET hold_remaining = $2, updated_at = now() WHERE id = $1`

	tag, err := tx.Exec(ctx, stmt, id, holdRemaining)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (OrderRepo) UpdateOrderProviderID(ctx context.Context, tx pgx.Tx, id, providerOrderID string) error {
	stmt := `UPDATE orders SET provider_order_id = $2, updated_at = now() WHERE id = $1`

	tag, err := tx.Exec(ctx, stmt, id, providerOrderID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// UpdateOrderSnapshot writes what a provider snapshot says about an order.
func (OrderRepo) UpdateOrderSnapshot(ctx context.Context, tx pgx.Tx, id, status string, filledQty, avgFillPrice, providerVersion int64) error {
	stmt := `
		UPDATE orders
		SET status = $2, filled_qty = $3, avg_fill_price = $4, provider_version = $5, updated_at = now()
		WHERE id = $1`

	tag, err := tx.Exec(ctx, stmt, id, status, filledQty, avgFillPrice, providerVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// TouchOrder marks an order as checked now without changing anything else.
func (OrderRepo) TouchOrder(ctx context.Context, tx pgx.Tx, id string) error {
	stmt := `UPDATE orders SET updated_at = now() WHERE id = $1`

	_, err := tx.Exec(ctx, stmt, id)
	return err
}

// CreateFill records a provider fill. domain.ErrDuplicate means this fill id
// was already recorded.
func (OrderRepo) CreateFill(ctx context.Context, tx pgx.Tx, f domain.Fill) error {
	stmt := `INSERT INTO order_fills (fill_id, order_id, qty, price, fee)
	         VALUES ($1, $2, $3, $4, $5)
	         ON CONFLICT (fill_id) DO NOTHING`

	tag, err := tx.Exec(ctx, stmt, f.ID, f.OrderID, f.Qty, f.Price, f.Fee)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrDuplicate
	}
	return nil
}

// ListFills returns an order's fills, oldest first.
func (OrderRepo) ListFills(ctx context.Context, tx pgx.Tx, orderID string) ([]domain.Fill, error) {
	stmt := `
		SELECT fill_id, order_id, qty, price, fee, created_at
		FROM order_fills
		WHERE order_id = $1
		ORDER BY created_at`

	rows, err := tx.Query(ctx, stmt, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fills := []domain.Fill{}
	for rows.Next() {
		var f domain.Fill
		if err := rows.Scan(&f.ID, &f.OrderID, &f.Qty, &f.Price, &f.Fee, &f.CreatedAt); err != nil {
			return nil, err
		}
		fills = append(fills, f)
	}
	return fills, rows.Err()
}

// scanOrder reads one order row in the column order used above.
func scanOrder(row pgx.Row) (domain.Order, error) {
	var o domain.Order
	err := row.Scan(
		&o.ID, &o.UserID, &o.QuoteID, &o.ProviderOrderID, &o.Symbol, &o.Side, &o.Qty, &o.Price,
		&o.FilledQty, &o.AvgFillPrice, &o.HoldAmount, &o.HoldRemaining, &o.Status, &o.Reason,
		&o.ProviderVersion, &o.CreatedAt, &o.UpdatedAt,
	)
	return o, err
}

// queryOrders runs an order list query and scans every row.
func queryOrders(ctx context.Context, tx pgx.Tx, stmt string, args ...any) ([]domain.Order, error) {
	rows, err := tx.Query(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orders := []domain.Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

type QuoteRepo struct{}

func (QuoteRepo) CreateQuote(ctx context.Context, tx pgx.Tx, q domain.Quote) error {
	stmt := `INSERT INTO quotes (id, symbol, side, qty, price, fee, total, expires_at)
	         VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := tx.Exec(ctx, stmt, q.ID, q.Symbol, q.Side, q.Qty, q.Price, q.Fee, q.Total, q.ExpiresAt)
	return err
}

func (QuoteRepo) GetQuote(ctx context.Context, tx pgx.Tx, id string) (domain.Quote, error) {
	stmt := `
		SELECT id, symbol, side, qty, price, fee, total, expires_at, created_at
		FROM quotes
		WHERE id = $1`

	var q domain.Quote
	err := tx.QueryRow(ctx, stmt, id).
		Scan(&q.ID, &q.Symbol, &q.Side, &q.Qty, &q.Price, &q.Fee, &q.Total, &q.ExpiresAt, &q.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Quote{}, domain.ErrNotFound
	}
	return q, err
}
