package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

type PositionRepo struct{}

// CreatePosition inserts a position. domain.ErrDuplicate means it already exists.
func (PositionRepo) CreatePosition(ctx context.Context, tx pgx.Tx, p domain.Position) error {
	stmt := `INSERT INTO positions (user_id, symbol, qty, held_qty, avg_cost, realized_pnl)
	         VALUES ($1, $2, $3, $4, $5, $6)
	         ON CONFLICT (user_id, symbol) DO NOTHING`

	tag, err := tx.Exec(ctx, stmt, p.UserID, p.Symbol, p.Qty, p.HeldQty, p.AvgCost, p.RealizedPnL)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrDuplicate
	}
	return nil
}

func (PositionRepo) GetPosition(ctx context.Context, tx pgx.Tx, userID int64, symbol string) (domain.Position, error) {
	stmt := `
		SELECT user_id, symbol, qty, held_qty, avg_cost, realized_pnl
		FROM positions
		WHERE user_id = $1 AND symbol = $2`

	var p domain.Position
	err := tx.QueryRow(ctx, stmt, userID, symbol).
		Scan(&p.UserID, &p.Symbol, &p.Qty, &p.HeldQty, &p.AvgCost, &p.RealizedPnL)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Position{}, domain.ErrNotFound
	}
	return p, err
}

func (PositionRepo) GetPositionForUpdate(ctx context.Context, tx pgx.Tx, userID int64, symbol string) (domain.Position, error) {
	stmt := `
		SELECT user_id, symbol, qty, held_qty, avg_cost, realized_pnl
		FROM positions
		WHERE user_id = $1 AND symbol = $2
		FOR UPDATE`

	var p domain.Position
	err := tx.QueryRow(ctx, stmt, userID, symbol).
		Scan(&p.UserID, &p.Symbol, &p.Qty, &p.HeldQty, &p.AvgCost, &p.RealizedPnL)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Position{}, domain.ErrNotFound
	}
	return p, err
}

// ListPositions returns the user's positions that hold shares or have realized P&L.
func (PositionRepo) ListPositions(ctx context.Context, tx pgx.Tx, userID int64) ([]domain.Position, error) {
	stmt := `
		SELECT user_id, symbol, qty, held_qty, avg_cost, realized_pnl
		FROM positions
		WHERE user_id = $1 AND (qty > 0 OR realized_pnl <> 0)
		ORDER BY symbol`

	rows, err := tx.Query(ctx, stmt, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	positions := []domain.Position{}
	for rows.Next() {
		var p domain.Position
		if err := rows.Scan(&p.UserID, &p.Symbol, &p.Qty, &p.HeldQty, &p.AvgCost, &p.RealizedPnL); err != nil {
			return nil, err
		}
		positions = append(positions, p)
	}
	return positions, rows.Err()
}

func (PositionRepo) UpdatePosition(ctx context.Context, tx pgx.Tx, p domain.Position) error {
	stmt := `
		UPDATE positions
		SET qty = $3, held_qty = $4, avg_cost = $5, realized_pnl = $6, updated_at = now()
		WHERE user_id = $1 AND symbol = $2`

	tag, err := tx.Exec(ctx, stmt, p.UserID, p.Symbol, p.Qty, p.HeldQty, p.AvgCost, p.RealizedPnL)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// CreateMovement records a fill's effect on a position. domain.ErrDuplicate
// means this fill was already recorded.
func (PositionRepo) CreateMovement(ctx context.Context, tx pgx.Tx, m domain.PositionMovement) error {
	stmt := `INSERT INTO position_movements (user_id, symbol, fill_id, qty_delta, price, realized_pnl)
	         VALUES ($1, $2, $3, $4, $5, $6)
	         ON CONFLICT (fill_id) DO NOTHING`

	tag, err := tx.Exec(ctx, stmt, m.UserID, m.Symbol, m.FillID, m.QtyDelta, m.Price, m.RealizedPnL)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrDuplicate
	}
	return nil
}
