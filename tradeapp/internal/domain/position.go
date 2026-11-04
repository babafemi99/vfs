package domain

import (
	"fmt"
	"time"
)

// Position is a user's holding in one symbol.
type Position struct {
	UserID      int64  `json:"-"`
	Symbol      string `json:"symbol"`
	Qty         int64  `json:"qty"`
	HeldQty     int64  `json:"held_qty"` // reserved by open SELL orders
	AvgCost     int64  `json:"avg_cost"` // per share, minor units
	RealizedPnL int64  `json:"realized_pnl"`
}

// PositionMovement records one fill's effect on a position. FillID is unique.
type PositionMovement struct {
	UserID      int64
	Symbol      string
	FillID      string
	QtyDelta    int64
	Price       int64
	RealizedPnL int64
	CreatedAt   time.Time
}

// Available is what can still be sold.
func (p Position) Available() int64 { return p.Qty - p.HeldQty }

// NewAvgCost is the average cost after buying q shares at price on top of
// qty shares at avg. Integer division rounds down. Fees are not included.
func NewAvgCost(qty, avg, q, price int64) int64 {
	if qty+q == 0 {
		return 0
	}
	return (qty*avg + q*price) / (qty + q)
}

// Realized is the P&L of selling q shares at price with fee, given avg cost.
func Realized(avg, q, price, fee int64) int64 {
	return (price-avg)*q - fee
}

// Hold reserves q shares for a SELL order.
func (p *Position) Hold(q int64) error {
	if q <= 0 {
		return ErrInvalidArgument
	}
	if p.Available() < q {
		return fmt.Errorf("%w: %d %s available, need %d", ErrInsufficientShares, p.Available(), p.Symbol, q)
	}
	p.HeldQty += q
	return nil
}

// Release un-reserves q shares. Zero is a no-op.
func (p *Position) Release(q int64) error {
	if q < 0 || q > p.HeldQty {
		return fmt.Errorf("%w: release %d %s, %d held", ErrInvalidArgument, q, p.Symbol, p.HeldQty)
	}
	p.HeldQty -= q
	return nil
}

// ApplyBuy adds q shares bought at price.
func (p *Position) ApplyBuy(q, price int64) error {
	if q <= 0 || price <= 0 {
		return ErrInvalidArgument
	}
	p.AvgCost = NewAvgCost(p.Qty, p.AvgCost, q, price)
	p.Qty += q
	return nil
}

// ApplySell removes q shares sold at price with fee, fromHeld of which were
// reserved by the order, and returns the realized P&L. Selling to zero resets
// the average cost.
func (p *Position) ApplySell(q, price, fee, fromHeld int64) (int64, error) {
	if q <= 0 || price <= 0 || fee < 0 || fromHeld < 0 || fromHeld > q {
		return 0, ErrInvalidArgument
	}
	if p.Qty < q || p.HeldQty < fromHeld {
		return 0, fmt.Errorf("%w: sell %d %s, have %d (held %d)", ErrInsufficientShares, q, p.Symbol, p.Qty, p.HeldQty)
	}
	r := Realized(p.AvgCost, q, price, fee)
	p.Qty -= q
	p.HeldQty -= fromHeld
	p.RealizedPnL += r
	if p.Qty == 0 {
		p.AvgCost = 0
	}
	return r, nil
}
