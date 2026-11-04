package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

// holdShares reserves qty shares for a SELL order.
func (s *Service) holdShares(ctx context.Context, tx pgx.Tx, userID int64, symbol string, qty int64) error {
	position, err := s.positionRepo.GetPositionForUpdate(ctx, tx, userID, symbol)
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: no %s position", domain.ErrInsufficientShares, symbol)
	}
	if err != nil {
		return err
	}

	if err := position.Hold(qty); err != nil {
		return err
	}
	return s.positionRepo.UpdatePosition(ctx, tx, position)
}

// releaseShares un-reserves qty shares when a SELL order reaches a final
// state. Zero is a no-op.
func (s *Service) releaseShares(ctx context.Context, tx pgx.Tx, userID int64, symbol string, qty int64) error {
	if qty == 0 {
		return nil
	}

	position, err := s.positionRepo.GetPositionForUpdate(ctx, tx, userID, symbol)
	if err != nil {
		return err
	}

	if err := position.Release(qty); err != nil {
		return err
	}
	return s.positionRepo.UpdatePosition(ctx, tx, position)
}

// applyBuyFill adds q shares bought at price. It returns false and changes
// nothing if fillID was already applied.
func (s *Service) applyBuyFill(ctx context.Context, tx pgx.Tx, userID int64, symbol, fillID string, q, price int64) (bool, error) {
	if fillID == "" {
		return false, domain.ErrInvalidArgument
	}

	position, err := s.lockOrCreatePosition(ctx, tx, userID, symbol)
	if err != nil {
		return false, err
	}
	if err := position.ApplyBuy(q, price); err != nil {
		return false, err
	}

	// The movement row is keyed by fill id: a duplicate means already applied.
	err = s.positionRepo.CreateMovement(ctx, tx, domain.PositionMovement{
		UserID:   userID,
		Symbol:   symbol,
		FillID:   fillID,
		QtyDelta: q,
		Price:    price,
	})
	if errors.Is(err, domain.ErrDuplicate) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	return true, s.positionRepo.UpdatePosition(ctx, tx, position)
}

// applySellFill removes q shares sold at price with fee, fromHeld of which
// were reserved by the order (normally q). It returns the realized P&L, and
// false if fillID was already applied.
func (s *Service) applySellFill(ctx context.Context, tx pgx.Tx, userID int64, symbol, fillID string, q, price, fee, fromHeld int64) (int64, bool, error) {
	if fillID == "" {
		return 0, false, domain.ErrInvalidArgument
	}

	position, err := s.lockOrCreatePosition(ctx, tx, userID, symbol)
	if err != nil {
		return 0, false, err
	}
	realized, err := position.ApplySell(q, price, fee, fromHeld)
	if err != nil {
		return 0, false, err
	}

	err = s.positionRepo.CreateMovement(ctx, tx, domain.PositionMovement{
		UserID:      userID,
		Symbol:      symbol,
		FillID:      fillID,
		QtyDelta:    -q,
		Price:       price,
		RealizedPnL: realized,
	})
	if errors.Is(err, domain.ErrDuplicate) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}

	return realized, true, s.positionRepo.UpdatePosition(ctx, tx, position)
}

// lockOrCreatePosition makes sure the position row exists, then locks it.
func (s *Service) lockOrCreatePosition(ctx context.Context, tx pgx.Tx, userID int64, symbol string) (domain.Position, error) {
	err := s.positionRepo.CreatePosition(ctx, tx, domain.Position{UserID: userID, Symbol: symbol})
	if err != nil && !errors.Is(err, domain.ErrDuplicate) {
		return domain.Position{}, err
	}
	return s.positionRepo.GetPositionForUpdate(ctx, tx, userID, symbol)
}
