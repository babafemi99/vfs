package domain

import (
	"fmt"
	"sort"
	"time"
)

// Mismatch kinds found by reconciliation.
const (
	MismatchStatus            = "status"              // local status differs from the provider's
	MismatchFilledQty         = "filled_qty"          // local filled_qty differs
	MismatchFillMissingLocal  = "fill_missing_local"  // the provider has a fill we never recorded
	MismatchFillUnknown       = "fill_unknown"        // we recorded a fill the provider doesn't have
	MismatchMissingAtProvider = "missing_at_provider" // we sent the order but the provider has no such order
	MismatchUnknownToUs       = "unknown_to_us"       // the provider has an order we don't
)

// ProviderSnapshot is what reconciliation needs from a provider order.
type ProviderSnapshot struct {
	ID            string    `json:"id"`
	ClientOrderID string    `json:"client_order_id"`
	Status        string    `json:"status"`
	Qty           int64     `json:"qty"`
	FilledQty     int64     `json:"filled_qty"`
	FillIDs       []string  `json:"fill_ids"`
	Version       int64     `json:"version"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Mismatch is one difference between our books and the provider's.
type Mismatch struct {
	OrderID         string `json:"order_id,omitempty"`
	ProviderOrderID string `json:"provider_order_id,omitempty"`
	Kind            string `json:"kind"`
	Local           string `json:"local"`
	Provider        string `json:"provider"`
}

// CompareOrder lists every difference between our order (with the fills we
// recorded) and the provider's snapshot of it. No differences means the two
// agree.
func CompareOrder(local Order, localFills []Fill, p ProviderSnapshot) []Mismatch {
	var out []Mismatch
	add := func(kind, l, pv string) {
		out = append(out, Mismatch{OrderID: local.ID, ProviderOrderID: p.ID, Kind: kind, Local: l, Provider: pv})
	}

	if local.Status != p.Status {
		add(MismatchStatus, local.Status, p.Status)
	}
	if local.FilledQty != p.FilledQty {
		add(MismatchFilledQty, fmt.Sprint(local.FilledQty), fmt.Sprint(p.FilledQty))
	}

	ours := map[string]bool{}
	for _, f := range localFills {
		ours[f.ID] = true
	}
	theirs := map[string]bool{}
	for _, id := range p.FillIDs {
		theirs[id] = true
	}
	for _, id := range sortedKeys(theirs) {
		if !ours[id] {
			add(MismatchFillMissingLocal, "", id)
		}
	}
	for _, id := range sortedKeys(ours) {
		if !theirs[id] {
			add(MismatchFillUnknown, id, "")
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ReconReport is the result of one reconciliation run.
type ReconReport struct {
	RunID         string            `json:"run_id"`
	StartedAt     time.Time         `json:"started_at"`
	FinishedAt    time.Time         `json:"finished_at"`
	OrdersChecked int               `json:"orders_checked"`
	OrdersSkipped int               `json:"orders_skipped"` // changed too recently to judge
	Mismatches    []Mismatch        `json:"mismatches"`
	InvariantsOK  bool              `json:"invariants_ok"`
	Invariants    []InvariantResult `json:"invariants"`
	OpenBreaks    int               `json:"open_breaks"` // contradictions recorded while processing
	OK            bool              `json:"ok"`          // no mismatches and all invariants hold
}

// OrderComparison is one order side by side with the provider's live view.
type OrderComparison struct {
	Order      Order             `json:"order"`
	Fills      []Fill            `json:"fills"`
	Provider   *ProviderSnapshot `json:"provider"` // nil if the provider has no such order
	Mismatches []Mismatch        `json:"mismatches"`
	Agree      bool              `json:"agree"`
}
