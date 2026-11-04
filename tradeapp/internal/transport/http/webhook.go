package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
)

// providerWebhook receives a provider event.
//
//  1. Verify: timestamp within tolerance, nonce present, HMAC signature over
//     "<ts>.<nonce>.<body>" (constant-time). Anything else → 401.
//  2. Replay check: the nonce must be new. A nonce seen before is the same
//     request sent again → 409, nothing stored. (The provider uses a fresh
//     nonce for each of its own retries, so retries still get through.)
//  3. Store the event in the inbox and queue it → 200. A duplicate event id
//     is also 200: there is nothing for the provider to retry.
//
// Every request, whatever happened to it, is written to the webhook log for
// the console's inspector.
func (s *Server) providerWebhook(w http.ResponseWriter, r *http.Request) {
	entry := domain.WebhookLog{
		RemoteAddr:      strPtr(r.RemoteAddr),
		Timestamp:       strPtr(r.Header.Get(provider.HeaderTimestamp)),
		Nonce:           strPtr(r.Header.Get(provider.HeaderNonce)),
		SignaturePrefix: strPtr(prefix(r.Header.Get(provider.HeaderSignature), 16)),
		EventID:         strPtr(r.Header.Get(provider.HeaderEventID)),
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		s.recordWebhook(r, entry, body, domain.WebhookInvalidBody, "body too large or unreadable")
		writeError(w, http.StatusBadRequest, "validation_error", "body too large or unreadable")
		return
	}
	describeEvent(&entry, body)

	// 1. Signature, timestamp, nonce.
	err = provider.VerifySignature(s.cfg.WebhookSecret, r.Header.Get(provider.HeaderTimestamp),
		r.Header.Get(provider.HeaderNonce), body, r.Header.Get(provider.HeaderSignature),
		s.cfg.WebhookTolerance, time.Now())
	if err != nil {
		outcome := domain.WebhookRejectedSignature
		switch {
		case errors.Is(err, provider.ErrBadTimestamp):
			outcome = domain.WebhookRejectedTimestamp
		case errors.Is(err, provider.ErrBadNonce):
			outcome = domain.WebhookRejectedNonce
		}
		s.recordWebhook(r, entry, body, outcome, err.Error())
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}
	entry.Verified = true

	// 2. Replay: a nonce may be used once.
	err = s.repo.RunInTx(r.Context(), func(tx pgx.Tx) error {
		return s.webhookRepo.CreateNonce(r.Context(), tx, r.Header.Get(provider.HeaderNonce))
	})
	if errors.Is(err, domain.ErrDuplicate) {
		s.recordWebhook(r, entry, body, domain.WebhookRejectedReplay, "nonce already used: this exact request was received before")
		writeError(w, http.StatusConflict, "replay_detected", "nonce already used")
		return
	}
	if err != nil {
		s.recordWebhook(r, entry, body, domain.WebhookError, err.Error())
		s.writeErr(w, r, err)
		return
	}

	// 3. Into the inbox.
	duplicate, err := s.svc.ReceiveProviderEvent(r.Context(), body)
	if err != nil {
		outcome := domain.WebhookError
		if errors.Is(err, domain.ErrInvalidArgument) {
			outcome = domain.WebhookInvalidBody
		}
		s.recordWebhook(r, entry, body, outcome, err.Error())
		s.writeErr(w, r, err)
		return
	}

	outcome := domain.WebhookAccepted
	if duplicate {
		outcome = domain.WebhookDuplicate
	}
	s.recordWebhook(r, entry, body, outcome, "")
	writeJSON(w, http.StatusOK, map[string]bool{"received": true})
}

// recordWebhook logs the request (slog) and writes it to the webhook log.
// It uses a context that survives the client hanging up.
func (s *Server) recordWebhook(r *http.Request, entry domain.WebhookLog, body []byte, outcome, detail string) {
	entry.Outcome = outcome
	if detail != "" {
		entry.Detail = &detail
	}
	entry.BodySize = len(body)
	if len(body) > domain.MaxLoggedBody {
		body = body[:domain.MaxLoggedBody]
	}
	if body == nil {
		body = []byte{}
	}

	attrs := []any{"outcome", outcome, "event_id", deref(entry.EventID), "type", deref(entry.EventType),
		"provider_order_id", deref(entry.ProviderOrderID), "nonce", deref(entry.Nonce), "remote", r.RemoteAddr}
	if detail != "" {
		attrs = append(attrs, "detail", detail)
	}
	if entry.Verified && (outcome == domain.WebhookAccepted || outcome == domain.WebhookDuplicate) {
		s.log.Info("webhook received", attrs...)
	} else {
		s.log.Warn("webhook refused", attrs...)
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		return s.webhookRepo.CreateLog(ctx, tx, entry, body)
	})
	if err != nil {
		s.log.Error("webhook log write failed", "err", err)
	}
}

// describeEvent fills in the event id, type and order from the body, for
// display only (it may be unverified at this point).
func describeEvent(entry *domain.WebhookLog, body []byte) {
	var e provider.Event
	if json.Unmarshal(body, &e) != nil {
		return
	}
	if e.ID != "" {
		entry.EventID = &e.ID
	}
	if e.Type != "" {
		entry.EventType = &e.Type
	}
	if e.Data.Order != nil && e.Data.Order.ID != "" {
		entry.ProviderOrderID = &e.Data.Order.ID
	}
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func prefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
