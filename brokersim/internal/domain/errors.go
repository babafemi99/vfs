package domain

import (
	"errors"
	"fmt"
)

// Code is the machine-readable error code returned to API clients.
type Code string

const (
	CodeValidation           Code = "validation_error"
	CodeNotFound             Code = "not_found"
	CodeQuoteExpired         Code = "quote_expired"
	CodeQuoteAlreadyAccepted Code = "quote_already_accepted"
	CodeIdempotencyConflict  Code = "idempotency_conflict"
	CodeMarketClosed         Code = "market_closed"
	CodeInvalidTransition    Code = "invalid_transition"
	CodeOrderFinal           Code = "order_final"
)

// Error is a domain error. Two Errors match with errors.Is when their codes match.
type Error struct {
	Code    Code
	Message string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// Sentinels for errors.Is.
var (
	ErrValidation           = &Error{Code: CodeValidation}
	ErrNotFound             = &Error{Code: CodeNotFound}
	ErrQuoteExpired         = &Error{Code: CodeQuoteExpired}
	ErrQuoteAlreadyAccepted = &Error{Code: CodeQuoteAlreadyAccepted}
	ErrIdempotencyConflict  = &Error{Code: CodeIdempotencyConflict}
	ErrMarketClosed         = &Error{Code: CodeMarketClosed}
	ErrInvalidTransition    = &Error{Code: CodeInvalidTransition}
	ErrOrderFinal           = &Error{Code: CodeOrderFinal}
)

// Errorf builds a domain error with the given code.
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// CodeOf returns the domain code of err, or "" if err is not a domain error.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
