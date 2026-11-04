package service

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

// postEntry writes a balanced journal entry inside tx. It returns false and
// changes nothing if the same business event (kind, source) was already
// posted. User accounts may never go negative.
func (s *Service) postEntry(ctx context.Context, tx pgx.Tx, e domain.Entry) (bool, error) {
	if err := e.Validate(); err != nil {
		return false, err
	}

	// 1. Claim the business event; a duplicate means it was already posted.
	err := s.ledgerRepo.CreateJournalEntry(ctx, tx, e)
	if errors.Is(err, domain.ErrDuplicate) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	entryID, err := s.ledgerRepo.GetJournalEntryID(ctx, tx, e.Kind, e.SourceType, e.SourceID)
	if err != nil {
		return false, err
	}

	// 2. Lock every account the entry touches (in id order, so concurrent
	//    postings can't deadlock) and work out the new balances.
	deltas := e.Deltas()
	codes := make([]string, 0, len(deltas))
	for code := range deltas {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	accounts, err := s.ledgerRepo.GetAccountsForUpdate(ctx, tx, codes)
	if err != nil {
		return false, err
	}
	if len(accounts) != len(codes) {
		return false, fmt.Errorf("%w: unknown account in %v", domain.ErrInvalidEntry, codes)
	}

	accountIDs := make(map[string]int64, len(accounts))
	for _, a := range accounts {
		newBalance := a.Balance + deltas[a.Code]
		if a.IsUser() && newBalance < 0 {
			return false, fmt.Errorf("%w: %s has %d, needs %d", domain.ErrInsufficientFunds, a.Code, a.Balance, -deltas[a.Code])
		}
		accountIDs[a.Code] = a.ID
	}

	// 3. Write the postings and the new cached balances.
	for _, p := range e.Postings {
		if err := s.ledgerRepo.CreatePosting(ctx, tx, entryID, accountIDs[p.Account], p.Amount); err != nil {
			return false, err
		}
	}
	for _, a := range accounts {
		if err := s.ledgerRepo.UpdateAccountBalance(ctx, tx, a.ID, a.Balance+deltas[a.Code]); err != nil {
			return false, err
		}
	}
	return true, nil
}

// fundCash moves amount from system:funding into the user's available cash.
func (s *Service) fundCash(ctx context.Context, tx pgx.Tx, userID, amount int64, fundingID string) (bool, error) {
	return s.postEntry(ctx, tx, domain.Entry{
		Kind:       domain.EntryFund,
		SourceType: domain.SourceFunding,
		SourceID:   fundingID,
		Postings: []domain.Posting{
			{Account: domain.AcctFunding, Amount: -amount},
			{Account: domain.CashAvailable(userID), Amount: amount},
		},
	})
}

// holdCash reserves amount of available cash for a BUY order.
func (s *Service) holdCash(ctx context.Context, tx pgx.Tx, userID, amount int64, orderID string) (bool, error) {
	return s.postEntry(ctx, tx, domain.Entry{
		Kind:       domain.EntryHold,
		SourceType: domain.SourceOrder,
		SourceID:   orderID,
		Postings: []domain.Posting{
			{Account: domain.CashAvailable(userID), Amount: -amount},
			{Account: domain.CashHeld(userID), Amount: amount},
		},
	})
}

// releaseCash returns what is left of a BUY order's hold to available cash.
// It posts once per order, when the order reaches a final state. Zero posts
// nothing.
func (s *Service) releaseCash(ctx context.Context, tx pgx.Tx, userID, amount int64, orderID string) (bool, error) {
	if amount == 0 {
		return false, nil
	}
	return s.postEntry(ctx, tx, domain.Entry{
		Kind:       domain.EntryRelease,
		SourceType: domain.SourceOrder,
		SourceID:   orderID,
		Postings: []domain.Posting{
			{Account: domain.CashHeld(userID), Amount: -amount},
			{Account: domain.CashAvailable(userID), Amount: amount},
		},
	})
}

// payBuyFill pays for a BUY fill: gross (qty*price) to broker clearing and
// fee to system:fees. fromHeld comes out of the order's hold; anything above
// it (fee rounding, or no hold left) comes from available cash.
func (s *Service) payBuyFill(ctx context.Context, tx pgx.Tx, userID int64, fillID string, gross, fee, fromHeld int64) (bool, error) {
	cost := gross + fee
	if gross <= 0 || fee < 0 || fromHeld < 0 || fromHeld > cost {
		return false, domain.ErrInvalidEntry
	}

	postings := []domain.Posting{{Account: domain.AcctBrokerClearing, Amount: gross}}
	if fee > 0 {
		postings = append(postings, domain.Posting{Account: domain.AcctFees, Amount: fee})
	}
	if fromHeld > 0 {
		postings = append(postings, domain.Posting{Account: domain.CashHeld(userID), Amount: -fromHeld})
	}
	if fromAvailable := cost - fromHeld; fromAvailable > 0 {
		postings = append(postings, domain.Posting{Account: domain.CashAvailable(userID), Amount: -fromAvailable})
	}

	return s.postEntry(ctx, tx, domain.Entry{
		Kind:       domain.EntryFillBuy,
		SourceType: domain.SourceFill,
		SourceID:   fillID,
		Postings:   postings,
	})
}

// creditSellFill credits a SELL fill: gross leaves broker clearing, the user
// gets gross minus fee, and the fee goes to system:fees.
func (s *Service) creditSellFill(ctx context.Context, tx pgx.Tx, userID int64, fillID string, gross, fee int64) (bool, error) {
	if gross <= 0 || fee < 0 || fee > gross {
		return false, domain.ErrInvalidEntry
	}

	postings := []domain.Posting{{Account: domain.AcctBrokerClearing, Amount: -gross}}
	if net := gross - fee; net > 0 {
		postings = append(postings, domain.Posting{Account: domain.CashAvailable(userID), Amount: net})
	}
	if fee > 0 {
		postings = append(postings, domain.Posting{Account: domain.AcctFees, Amount: fee})
	}

	return s.postEntry(ctx, tx, domain.Entry{
		Kind:       domain.EntryFillSell,
		SourceType: domain.SourceFill,
		SourceID:   fillID,
		Postings:   postings,
	})
}

// getCash reads a user's available and held cash.
func (s *Service) getCash(ctx context.Context, tx pgx.Tx, userID int64) (domain.Cash, error) {
	available, err := s.ledgerRepo.GetAccountBalance(ctx, tx, domain.CashAvailable(userID))
	if err != nil {
		return domain.Cash{}, err
	}
	held, err := s.ledgerRepo.GetAccountBalance(ctx, tx, domain.CashHeld(userID))
	if err != nil {
		return domain.Cash{}, err
	}
	return domain.Cash{Available: available, Held: held, Total: available + held}, nil
}
