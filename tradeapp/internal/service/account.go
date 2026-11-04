package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/pkg/lid"
)

const (
	DefaultActivityLimit = 100
	MaxActivityLimit     = 500
)

// FundAccount adds amount to the user's available cash. Retry safety comes
// from the transport layer's idempotency keys.
func (s *Service) FundAccount(ctx context.Context, userID, amount int64) (*domain.FundResult, error) {
	if err := domain.ValidateFundAmount(amount); err != nil {
		return nil, err
	}

	fundingID := lid.NewFunding()
	var cash domain.Cash

	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		// Credit the user's available cash from system:funding.
		posted, err := s.fundCash(ctx, tx, userID, amount, fundingID)
		if err != nil {
			return err
		}
		if !posted {
			return fmt.Errorf("funding %s was already posted", fundingID)
		}

		// Record it in the activity feed.
		data, err := json.Marshal(map[string]int64{"amount": amount})
		if err != nil {
			return err
		}
		if err := s.activityRepo.CreateActivity(ctx, tx, domain.Activity{
			UserID:  userID,
			Kind:    "account.funded",
			Message: fmt.Sprintf("Added %s to your account", domain.FormatMoney(amount)),
			Data:    data,
		}); err != nil {
			return err
		}

		// Read the new balances for the response.
		cash, err = s.getCash(ctx, tx, userID)
		return err
	})
	if err != nil {
		return nil, err
	}

	return &domain.FundResult{Amount: amount, Cash: cash}, nil
}

// GetAccount returns cash, positions and P&L. Positions are valued at the
// provider's current bid; if prices are unavailable the market fields are
// left empty rather than failing the whole request.
func (s *Service) GetAccount(ctx context.Context, userID int64) (*domain.AccountSummary, error) {
	summary := domain.AccountSummary{UserID: userID, Positions: []domain.PositionView{}}
	var positions []domain.Position

	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		if summary.Cash, err = s.getCash(ctx, tx, userID); err != nil {
			return err
		}
		positions, err = s.positionRepo.ListPositions(ctx, tx, userID)
		return err
	})
	if err != nil {
		return nil, err
	}

	bids := map[string]int64{}
	prices, priceErr := s.ListPrices(ctx)
	for _, p := range prices {
		bids[p.Symbol] = p.Bid
	}

	var totalValue, totalUnrealized int64
	allPriced := priceErr == nil
	for _, p := range positions {
		view := domain.PositionView{Position: p}
		summary.RealizedPnL += p.RealizedPnL

		if bid, ok := bids[p.Symbol]; ok {
			value := bid * p.Qty
			unrealized := (bid - p.AvgCost) * p.Qty
			view.LastPrice, view.MarketValue, view.UnrealizedPnL = &bid, &value, &unrealized
			totalValue += value
			totalUnrealized += unrealized
		} else if p.Qty > 0 {
			allPriced = false
		}
		summary.Positions = append(summary.Positions, view)
	}

	if allPriced {
		summary.PositionsValue, summary.UnrealizedPnL = &totalValue, &totalUnrealized
	}
	return &summary, nil
}

// ListActivity returns up to limit feed items after the cursor, oldest first.
func (s *Service) ListActivity(ctx context.Context, userID, afterID int64, limit int) ([]domain.Activity, error) {
	if afterID < 0 {
		return nil, fmt.Errorf("%w: after must be >= 0", domain.ErrInvalidArgument)
	}
	if limit < 1 || limit > MaxActivityLimit {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", domain.ErrInvalidArgument, MaxActivityLimit)
	}

	var items []domain.Activity
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		items, err = s.activityRepo.ListActivity(ctx, tx, userID, afterID, limit)
		return err
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}
