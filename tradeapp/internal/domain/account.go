package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

const MaxFund = 10_000_000

func ValidateFundAmount(amount int64) error {
	if amount < 1 || amount > MaxFund {
		return ErrInvalidAmount
	}
	return nil
}

// FundResult is the outcome of funding an account.
type FundResult struct {
	Amount int64 `json:"amount"`
	Cash   Cash  `json:"cash"`
}

// AccountSummary is what GET /api/account shows. The market fields are nil
// when provider prices are unavailable.
type AccountSummary struct {
	UserID         int64          `json:"user_id"`
	Cash           Cash           `json:"cash"`
	Positions      []PositionView `json:"positions"`
	PositionsValue *int64         `json:"positions_value"`
	RealizedPnL    int64          `json:"realized_pnl"`
	UnrealizedPnL  *int64         `json:"unrealized_pnl"`
}

// PositionView is a position valued at the current bid (what selling now
// would fetch, before fees).
type PositionView struct {
	Position
	LastPrice     *int64 `json:"last_price"`
	MarketValue   *int64 `json:"market_value"`
	UnrealizedPnL *int64 `json:"unrealized_pnl"`
}

// Activity is one item of the durable, user-visible feed.
type Activity struct {
	ID        int64           `json:"id"`
	UserID    int64           `json:"-"`
	OrderID   *string         `json:"order_id,omitempty"`
	Kind      string          `json:"kind"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// IdempotencyStatus is where a request with an Idempotency-Key is.
type IdempotencyStatus string

const (
	IdempotencyStarted   IdempotencyStatus = "STARTED"   // running
	IdempotencyCompleted IdempotencyStatus = "COMPLETED" // 2xx, response stored
	IdempotencyFailed    IdempotencyStatus = "FAILED"    // 4xx stored, or released (no response)
)

// IdempotencyRecord is a stored idempotency key. A FAILED record with a
// response is a business rejection to replay; without one it was released
// (5xx, or reaped as a zombie) and the key may run again.
type IdempotencyRecord struct {
	UserID       int64
	Key          string
	RequestHash  string
	RequestPath  string
	Action       string
	Status       IdempotencyStatus
	ResponseCode *int
	ResponseBody []byte
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ExpiresAt    time.Time
}

// Replayable reports whether a retry with this key gets the stored response.
func (r IdempotencyRecord) Replayable() bool {
	return r.Status == IdempotencyCompleted || (r.Status == IdempotencyFailed && r.ResponseCode != nil)
}

// InvariantResult is the outcome of one books-add-up check.
type InvariantResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// FormatMoney renders minor units as $1,234.56.
func FormatMoney(m int64) string {
	sign := ""
	if m < 0 {
		sign, m = "-", -m
	}
	whole := fmt.Sprint(m / 100)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	return fmt.Sprintf("%s$%s.%02d", sign, whole, m%100)
}
