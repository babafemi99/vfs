package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

const maxBody = 1 << 20

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var b errorBody
	b.Error.Code, b.Error.Message = code, msg
	writeJSON(w, status, b)
}

// writeErr maps domain errors to HTTP responses; anything else is a 500.
func (s *Server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidArgument), errors.Is(err, domain.ErrInvalidAmount):
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, domain.ErrInsufficientFunds):
		writeError(w, http.StatusUnprocessableEntity, "insufficient_funds", "not enough available cash")
	case errors.Is(err, domain.ErrInsufficientShares):
		writeError(w, http.StatusUnprocessableEntity, "insufficient_shares", "not enough shares available")
	case errors.Is(err, domain.ErrQuoteExpired):
		writeError(w, http.StatusConflict, "quote_expired", "quote expired, get a new one")
	case errors.Is(err, domain.ErrQuoteAlreadyUsed):
		writeError(w, http.StatusConflict, "quote_already_used", "an order was already placed from this quote")
	case errors.Is(err, domain.ErrOrderFinal):
		writeError(w, http.StatusConflict, "order_final", err.Error())
	case errors.Is(err, domain.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, domain.ErrProviderUnavailable):
		s.log.Warn("provider unavailable", "method", r.Method, "path", r.URL.Path, "err", err)
		writeError(w, http.StatusBadGateway, "provider_unavailable", "the broker is unavailable, try again")
	default:
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
	}
}

// decode reads a strict JSON body (unknown fields and trailing data refused).
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", "invalid JSON body: "+err.Error())
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "validation_error", "body must be a single JSON object")
		return false
	}
	return true
}
