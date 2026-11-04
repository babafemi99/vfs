package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/repository"
)

const (
	// WebhookLogRetention is how long the webhook log keeps requests.
	WebhookLogRetention = 7 * 24 * time.Hour

	DefaultWebhookLogLimit = 200
	MaxWebhookLogLimit     = 1000
)

// WebhookService owns the webhook replay cache (nonces) and the webhook log
// the console's terminal and inspector read. Turning a verified event into
// order updates stays with the trading Service (ReceiveProviderEvent).
type WebhookService struct {
	repo        repository.Repository
	webhookRepo repository.WebhookRepo
	now         func() time.Time
}

func NewWebhookService(repo repository.Repository) *WebhookService {
	return &WebhookService{repo: repo, now: time.Now}
}

// ClaimNonce records a nonce as used. A nonce may be used once: seeing it
// again means the same signed request was sent again (ErrReplayDetected).
func (s *WebhookService) ClaimNonce(ctx context.Context, nonce string) error {
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		return s.webhookRepo.CreateNonce(ctx, tx, nonce)
	})
	if errors.Is(err, domain.ErrDuplicate) {
		return domain.ErrReplayDetected
	}
	return err
}

// RecordLog writes one webhook request to the log. Only the first
// MaxLoggedBody bytes of the body are kept; BodySize keeps the real size.
func (s *WebhookService) RecordLog(ctx context.Context, entry domain.WebhookLog, body []byte) error {
	entry.BodySize = len(body)
	if len(body) > domain.MaxLoggedBody {
		body = body[:domain.MaxLoggedBody]
	}
	if body == nil {
		body = []byte{}
	}
	return s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		return s.webhookRepo.CreateLog(ctx, tx, entry, body)
	})
}

// ListLatestLogs returns the newest limit requests, oldest first (ready to
// print top to bottom like a terminal).
func (s *WebhookService) ListLatestLogs(ctx context.Context, limit int) ([]domain.WebhookLog, error) {
	if limit < 1 || limit > MaxWebhookLogLimit {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", domain.ErrInvalidArgument, MaxWebhookLogLimit)
	}
	var logs []domain.WebhookLog
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		logs, err = s.webhookRepo.ListLatestLogs(ctx, tx, limit)
		return err
	})
	slices.Reverse(logs)
	return logs, err
}

// ListLogsAfter returns up to limit requests after id afterID, oldest first.
func (s *WebhookService) ListLogsAfter(ctx context.Context, afterID int64, limit int) ([]domain.WebhookLog, error) {
	var logs []domain.WebhookLog
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		logs, err = s.webhookRepo.ListLogsAfter(ctx, tx, afterID, limit)
		return err
	})
	return logs, err
}

// GetLog returns one request with its body and processing state.
func (s *WebhookService) GetLog(ctx context.Context, id int64) (domain.WebhookLog, error) {
	var log domain.WebhookLog
	err := s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		var err error
		log, err = s.webhookRepo.GetLog(ctx, tx, id)
		return err
	})
	return log, err
}

// DeleteExpired removes nonces older than twice the signature tolerance (the
// timestamp check already refuses anything that old, so they can't be
// replayed) and log rows past their retention.
func (s *WebhookService) DeleteExpired(ctx context.Context, tolerance time.Duration) error {
	now := s.now()
	return s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		if err := s.webhookRepo.DeleteNoncesSeenBefore(ctx, tx, now.Add(-2*tolerance)); err != nil {
			return err
		}
		return s.webhookRepo.DeleteLogsReceivedBefore(ctx, tx, now.Add(-WebhookLogRetention))
	})
}
