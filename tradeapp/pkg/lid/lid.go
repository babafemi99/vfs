// Package lid makes prefixed ULID identifiers like "order-01JC3V8Q9W...":
// a readable resource prefix, a separator, and a ULID (time-ordered, so ids
// sort by creation time).
package lid

import (
	"errors"
	"fmt"
	"strings"

	"github.com/oklog/ulid/v2"
)

const separator = "-"

type Prefix string

const (
	Order   Prefix = "order" // an order; also sent to the provider as client_order_id
	Funding Prefix = "fund"  // one account funding (the ledger entry's source id)
	Recon   Prefix = "recon" // one reconciliation run
)

var (
	ErrInvalidID     = errors.New("invalid identifier")
	ErrInvalidPrefix = errors.New("invalid identifier prefix")
)

func NewOrder() string   { return newID(Order) }
func NewFunding() string { return newID(Funding) }
func NewRecon() string   { return newID(Recon) }

func newID(prefix Prefix) string {
	return string(prefix) + separator + ulid.Make().String()
}

// Normalize returns the canonical form of an id: lower-case prefix and
// upper-case ULID ("ORDER-01jc…" → "order-01JC…"). It fails like Validate.
func Normalize(value string, expected Prefix) (string, error) {
	prefix, rawULID, found := strings.Cut(value, separator)
	if !found || Prefix(strings.ToLower(prefix)) != expected {
		return "", fmt.Errorf("%w: expected %s prefix", ErrInvalidPrefix, expected)
	}
	if strings.Contains(rawULID, separator) {
		return "", fmt.Errorf("%w: unexpected separator", ErrInvalidID)
	}
	id, err := ulid.ParseStrict(rawULID)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidID, err)
	}
	return string(expected) + separator + id.String(), nil
}

// Validate checks both the resource prefix and canonical ULID representation.
func Validate(value string, expected Prefix) error {
	prefix, rawULID, found := strings.Cut(value, separator)
	if !found || Prefix(prefix) != expected {
		return fmt.Errorf("%w: expected %s prefix", ErrInvalidPrefix, expected)
	}
	if strings.Contains(rawULID, separator) {
		return fmt.Errorf("%w: unexpected separator", ErrInvalidID)
	}
	if _, err := ulid.ParseStrict(rawULID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidID, err)
	}

	return nil
}
