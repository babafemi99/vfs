package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/babafem99/vfs/lab/brokersim/internal/domain"
)

const (
	codeUnauthorized domain.Code = "unauthorized"
	codeForbidden    domain.Code = "forbidden"
	codeInternal     domain.Code = "internal"
)

var statusByCode = map[domain.Code]int{
	codeUnauthorized:                401,
	codeForbidden:                   403,
	domain.CodeValidation:           400,
	domain.CodeNotFound:             404,
	domain.CodeQuoteExpired:         409,
	domain.CodeQuoteAlreadyAccepted: 409,
	domain.CodeIdempotencyConflict:  422,
	domain.CodeMarketClosed:         409,
	domain.CodeInvalidTransition:    409,
	domain.CodeOrderFinal:           409,
	codeInternal:                    500,
}

type errorBody struct {
	Error struct {
		Code    domain.Code `json:"code"`
		Message string      `json:"message"`
	} `json:"error"`
	Order *domain.Order `json:"order,omitempty"`
}

// writeError maps err to the JSON error shape. order, if non-nil, is included
// in the body (used for order_final on cancel).
func writeError(w http.ResponseWriter, log *slog.Logger, err error, order *domain.Order) {
	var body errorBody
	body.Order = order
	var de *domain.Error
	status, known := 0, false
	if errors.As(err, &de) {
		status, known = statusByCode[de.Code]
	}
	if known {
		body.Error.Code, body.Error.Message = de.Code, de.Message
	} else {
		log.Error("internal error", "err", err)
		body.Error.Code, body.Error.Message = codeInternal, "internal error"
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
