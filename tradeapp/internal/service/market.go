package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
)

// ListPrices returns provider prices, reusing them for PriceCacheTTL.
func (s *Service) ListPrices(ctx context.Context) ([]domain.Price, error) {
	s.pricesMu.Lock()
	defer s.pricesMu.Unlock()

	if s.prices != nil && s.now().Sub(s.pricesFetched) < s.cfg.PriceCacheTTL {
		return s.prices, nil
	}

	fetched, err := s.broker.ListPrices(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrProviderUnavailable, err)
	}

	prices := make([]domain.Price, 0, len(fetched))
	for _, p := range fetched {
		prices = append(prices, domain.Price{Symbol: p.Symbol, Bid: p.Bid, Mid: p.Mid, Ask: p.Ask})
	}
	s.prices, s.pricesFetched = prices, s.now()
	return prices, nil
}

// CreateQuote asks the provider for a quote and stores it, so an order can
// later be placed from it.
func (s *Service) CreateQuote(ctx context.Context, symbol, side string, qty int64) (*domain.Quote, error) {
	if err := domain.ValidateQuoteRequest(symbol, side, qty); err != nil {
		return nil, err
	}

	pq, err := s.broker.CreateQuote(ctx, symbol, side, qty)
	if err != nil {
		var apiErr *provider.APIError
		if errors.As(err, &apiErr) && (apiErr.Status == 400 || apiErr.Status == 404) {
			return nil, fmt.Errorf("%w: %s", domain.ErrInvalidArgument, apiErr.Message)
		}
		return nil, fmt.Errorf("%w: %v", domain.ErrProviderUnavailable, err)
	}

	quote := domain.Quote{
		ID:        pq.ID,
		Symbol:    pq.Symbol,
		Side:      pq.Side,
		Qty:       pq.Qty,
		Price:     pq.Price,
		Fee:       pq.Fee,
		Total:     pq.Total,
		ExpiresAt: pq.ExpiresAt,
	}

	err = s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		if err := s.quoteRepo.CreateQuote(ctx, tx, quote); err != nil {
			return err
		}
		quote, err = s.quoteRepo.GetQuote(ctx, tx, quote.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &quote, nil
}
