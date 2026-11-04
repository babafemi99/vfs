package domain

import (
	"fmt"
	"time"
)

// Journal entry kinds.
const (
	EntryFund     = "fund"
	EntryHold     = "hold"
	EntryRelease  = "release"
	EntryFillBuy  = "fill_buy"
	EntryFillSell = "fill_sell"
)

// Journal entry source types.
const (
	SourceFunding = "funding" // source id: "<user_id>:<idempotency key>"
	SourceOrder   = "order"   // source id: order id
	SourceFill    = "fill"    // source id: provider fill id
)

// System account codes.
const (
	AcctFunding        = "system:funding"
	AcctBrokerClearing = "system:broker_clearing"
	AcctFees           = "system:fees"
)

// CashAvailable is the code of a user's spendable cash account.
func CashAvailable(userID int64) string { return fmt.Sprintf("user:%d:cash_available", userID) }

// CashHeld is the code of a user's cash reserved by open BUY orders.
func CashHeld(userID int64) string { return fmt.Sprintf("user:%d:cash_held", userID) }

// LedgerAccount is a row of the chart of accounts.
type LedgerAccount struct {
	ID      int64
	Code    string
	OwnerID *int64 // nil for system accounts
	Balance int64  // cached sum of postings
}

// IsUser reports whether the account belongs to a user (and so may not go negative).
func (a LedgerAccount) IsUser() bool { return a.OwnerID != nil }

type Posting struct {
	Account string // account code
	Amount  int64  // signed minor units
}

// Entry is one balanced business event. (Kind, SourceType, SourceID) is unique.
type Entry struct {
	ID         int64
	Kind       string
	SourceType string
	SourceID   string
	Postings   []Posting
	CreatedAt  time.Time
}

// Validate checks the entry is complete and sums to zero.
func (e Entry) Validate() error {
	if e.Kind == "" || e.SourceType == "" || e.SourceID == "" || len(e.Postings) < 2 {
		return ErrInvalidEntry
	}
	var sum int64
	for _, p := range e.Postings {
		if p.Amount == 0 || p.Account == "" {
			return ErrInvalidEntry
		}
		sum += p.Amount
	}
	if sum != 0 {
		return ErrUnbalancedEntry
	}
	return nil
}

// Deltas sums the entry's postings per account code.
func (e Entry) Deltas() map[string]int64 {
	d := make(map[string]int64, len(e.Postings))
	for _, p := range e.Postings {
		d[p.Account] += p.Amount
	}
	return d
}

// Cash is a user's cash position.
type Cash struct {
	Available int64 `json:"available"`
	Held      int64 `json:"held"`
	Total     int64 `json:"total"`
}
