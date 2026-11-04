package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
	"github.com/babafem99/vfs/tradeapp/pkg/lid"
)

// RunReconciliation compares our orders with the provider's, checks the
// books' invariants, stores the report and returns it.
//
//   - Window: orders changed in the last ReconLookback.
//   - Orders that changed (on either side) within StuckAfter are skipped as
//     still settling: a webhook may be in flight.
//   - Each checked order is compared on status, filled_qty and fill ids.
//   - Orders we sent but the provider doesn't have, and provider orders we
//     don't have, are mismatches too.
func (s *Service) RunReconciliation(ctx context.Context) (*domain.ReconReport, error) {
	started := s.now()
	settledBefore := started.Add(-s.cfg.StuckAfter)
	from := started.Add(-s.cfg.ReconLookback)

	// 1. The provider's side.
	theirs, err := s.broker.ListOrders(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("%w: list orders: %v", domain.ErrProviderUnavailable, err)
	}
	byClientID := make(map[string]provider.Order, len(theirs))
	for _, p := range theirs {
		byClientID[p.ClientOrderID] = p
	}

	report := domain.ReconReport{RunID: lid.NewRecon(), StartedAt: started, Mismatches: []domain.Mismatch{}}

	// 2. Our side, order by order.
	err = s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		ours, err := s.reconRepo.ListOrdersUpdatedBetween(ctx, tx, from, started)
		if err != nil {
			return err
		}
		known := make(map[string]bool, len(ours))

		for _, o := range ours {
			known[o.ID] = true
			p, atProvider := byClientID[o.ID]

			if !atProvider {
				if o.ProviderOrderID == nil {
					continue // never reached the provider (not yet sent, or refused at submit)
				}
				if o.UpdatedAt.After(settledBefore) {
					report.OrdersSkipped++
					continue
				}
				report.OrdersChecked++
				report.Mismatches = append(report.Mismatches, domain.Mismatch{
					OrderID: o.ID, ProviderOrderID: *o.ProviderOrderID, Kind: domain.MismatchMissingAtProvider,
					Local: o.Status, Provider: "",
				})
				continue
			}

			if o.UpdatedAt.After(settledBefore) || p.UpdatedAt.After(settledBefore) {
				report.OrdersSkipped++
				continue
			}
			fills, err := s.orderRepo.ListFills(ctx, tx, o.ID)
			if err != nil {
				return err
			}
			report.OrdersChecked++
			report.Mismatches = append(report.Mismatches, domain.CompareOrder(o, fills, toSnapshot(p))...)
		}

		// 3. Provider orders we don't know (outside our window, or not at all).
		for _, p := range theirs {
			if known[p.ClientOrderID] || p.UpdatedAt.After(settledBefore) {
				continue
			}
			if id, err := lid.Normalize(p.ClientOrderID, lid.Order); err == nil {
				_, err := s.orderRepo.GetOrder(ctx, tx, id)
				if err == nil {
					continue // ours, just changed before the window
				}
				if !errors.Is(err, domain.ErrNotFound) {
					return err
				}
			}
			report.Mismatches = append(report.Mismatches, domain.Mismatch{
				ProviderOrderID: p.ID, Kind: domain.MismatchUnknownToUs,
				Local: "", Provider: fmt.Sprintf("%s %s (client_order_id %q)", p.Status, p.Symbol, p.ClientOrderID),
			})
		}

		report.OpenBreaks, err = s.reconRepo.CountBreaks(ctx, tx)
		return err
	})
	if err != nil {
		return nil, err
	}

	// 4. The books themselves.
	if report.Invariants, err = s.CheckInvariants(ctx); err != nil {
		return nil, err
	}
	report.InvariantsOK = true
	for _, inv := range report.Invariants {
		report.InvariantsOK = report.InvariantsOK && inv.OK
	}
	report.FinishedAt = s.now()
	report.OK = len(report.Mismatches) == 0 && report.InvariantsOK

	// 5. Keep the report.
	body, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	err = s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		return s.reconRepo.CreateRun(ctx, tx, report, body)
	})
	if err != nil {
		return nil, err
	}
	return &report, nil
}

// LatestReconciliation returns the most recent stored report.
func (s *Service) LatestReconciliation(ctx context.Context) (*domain.ReconReport, error) {
	var body []byte
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		body, err = s.reconRepo.GetLatestRunReport(ctx, tx)
		return err
	})
	if errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("%w: no reconciliation has run yet", domain.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}

	var report domain.ReconReport
	if err := json.Unmarshal(body, &report); err != nil {
		return nil, err
	}
	return &report, nil
}

// CompareOrder puts one of the user's orders next to the provider's live
// view of it and lists the differences. Unlike the scheduled run it has no
// settling window: it shows exactly what each side says right now.
func (s *Service) CompareOrder(ctx context.Context, userID int64, orderID string) (*domain.OrderComparison, error) {
	id, ok := normalizeOrderID(orderID)
	if !ok {
		return nil, fmt.Errorf("%w: order %s", domain.ErrNotFound, orderID)
	}

	cmp := domain.OrderComparison{Mismatches: []domain.Mismatch{}}
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		if cmp.Order, err = s.orderRepo.GetOrder(ctx, tx, id); err != nil {
			return err
		}
		if cmp.Order.UserID != userID {
			return domain.ErrNotFound
		}
		cmp.Fills, err = s.orderRepo.ListFills(ctx, tx, id)
		return err
	})
	if errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("%w: order %s", domain.ErrNotFound, orderID)
	}
	if err != nil {
		return nil, err
	}

	var snap provider.Order
	if cmp.Order.ProviderOrderID != nil {
		snap, err = s.broker.GetOrder(ctx, *cmp.Order.ProviderOrderID)
	} else {
		snap, err = s.broker.GetOrderByClientOrderID(ctx, id)
	}

	var apiErr *provider.APIError
	switch {
	case err == nil:
		view := toSnapshot(snap)
		cmp.Provider = &view
		cmp.Mismatches = append(cmp.Mismatches, domain.CompareOrder(cmp.Order, cmp.Fills, view)...)
	case errors.As(err, &apiErr) && apiErr.Status == 404:
		if cmp.Order.ProviderOrderID != nil {
			cmp.Mismatches = append(cmp.Mismatches, domain.Mismatch{
				OrderID: id, ProviderOrderID: *cmp.Order.ProviderOrderID,
				Kind: domain.MismatchMissingAtProvider, Local: cmp.Order.Status,
			})
		}
		// Never sent: the provider not knowing it is expected.
	default:
		return nil, fmt.Errorf("%w: %v", domain.ErrProviderUnavailable, err)
	}

	cmp.Agree = len(cmp.Mismatches) == 0
	return &cmp, nil
}

func toSnapshot(p provider.Order) domain.ProviderSnapshot {
	ids := make([]string, 0, len(p.Fills))
	for _, f := range p.Fills {
		ids = append(ids, f.ID)
	}
	return domain.ProviderSnapshot{
		ID: p.ID, ClientOrderID: p.ClientOrderID, Status: p.Status, Qty: p.Qty,
		FilledQty: p.FilledQty, FillIDs: ids, Version: p.Version, UpdatedAt: p.UpdatedAt,
	}
}
