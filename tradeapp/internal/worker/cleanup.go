package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/babafem99/vfs/tradeapp/internal/service"
)

// RunCleanup, every interval until ctx is cancelled: releases zombie
// idempotency keys (started, never finished), deletes expired ones, and
// deletes webhook nonces too old to be replayed and webhook log rows past
// their retention.
func RunCleanup(ctx context.Context, log *slog.Logger, idempo *service.IdempotencyService,
	webhooks *service.WebhookService, webhookTolerance, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		if err := idempo.SweepZombies(ctx); err != nil && ctx.Err() == nil {
			log.Warn("idempotency zombie sweep failed", "err", err)
		}
		if err := idempo.CleanupExpired(ctx); err != nil && ctx.Err() == nil {
			log.Warn("idempotency cleanup failed", "err", err)
		}
		if err := webhooks.DeleteExpired(ctx, webhookTolerance); err != nil && ctx.Err() == nil {
			log.Warn("webhook cleanup failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
