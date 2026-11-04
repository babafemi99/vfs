// Package domain holds the trade app's models and the rules that need no
// database or network: validation, position maths, order statuses, money
// formatting. Every other layer depends on it; it depends on nothing.
package domain

import "errors"

// Business errors. The transport layer maps these to HTTP responses.
var (
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrInvalidAmount       = errors.New("amount must be between 1 and 10000000")
	ErrInsufficientFunds   = errors.New("insufficient funds")
	ErrInsufficientShares  = errors.New("insufficient shares")
	ErrUnbalancedEntry     = errors.New("ledger entry does not balance")
	ErrInvalidEntry        = errors.New("invalid ledger entry")
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different request")
	ErrRequestInProgress   = errors.New("request with this idempotency key is in progress")
	ErrUnauthenticated     = errors.New("no authenticated user")
	ErrReplayDetected      = errors.New("nonce already used: this exact request was received before")
	ErrNotFound            = errors.New("not found")
	ErrDuplicate           = errors.New("already exists")
	ErrConflict            = errors.New("conflict")
	ErrQuoteExpired        = errors.New("quote expired")
	ErrQuoteAlreadyUsed    = errors.New("quote already used")
	ErrProviderUnavailable = errors.New("provider unavailable")
	ErrOrderFinal          = errors.New("order is already final")
)
