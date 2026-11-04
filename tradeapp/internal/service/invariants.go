package service

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

// maxViolationsShown caps how many problem lines one result carries.
const maxViolationsShown = 10

// CheckInvariants runs every "books add up" check and returns one result per
// check.
func (s *Service) CheckInvariants(ctx context.Context) ([]domain.InvariantResult, error) {
	checks := []struct {
		name string
		run  func(context.Context, pgx.Tx) ([]string, error)
	}{
		{"entries_balance", s.invariantRepo.UnbalancedEntries},
		{"accounts_sum_to_zero", s.invariantRepo.NonZeroBalanceSum},
		{"balance_matches_postings", s.invariantRepo.StaleBalances},
		{"no_negative_user_balances", s.invariantRepo.NegativeUserBalances},
		{"cash_held_matches_open_buys", s.invariantRepo.CashHeldMismatches},
		{"held_qty_matches_open_sells", s.invariantRepo.HeldQtyMismatches},
		{"no_negative_positions", s.invariantRepo.NegativePositions},
		{"position_qty_matches_movements", s.invariantRepo.PositionMovementMismatches},
	}

	results := make([]domain.InvariantResult, 0, len(checks))
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		for _, c := range checks {
			problems, err := c.run(ctx, tx)
			if err != nil {
				return err
			}
			if len(problems) > maxViolationsShown {
				problems = problems[:maxViolationsShown]
			}
			results = append(results, domain.InvariantResult{
				Name:   c.name,
				OK:     len(problems) == 0,
				Detail: strings.Join(problems, "; "),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}
