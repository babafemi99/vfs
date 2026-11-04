package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
)

type WebhookRepo struct{}

// CreateNonce records a webhook nonce. domain.ErrDuplicate means it was seen
// before: a replay.
func (WebhookRepo) CreateNonce(ctx context.Context, tx pgx.Tx, nonce string) error {
	stmt := `INSERT INTO webhook_nonces (nonce) VALUES ($1) ON CONFLICT (nonce) DO NOTHING`

	tag, err := tx.Exec(ctx, stmt, nonce)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrDuplicate
	}
	return nil
}

func (WebhookRepo) DeleteNoncesSeenBefore(ctx context.Context, tx pgx.Tx, before time.Time) error {
	stmt := `DELETE FROM webhook_nonces WHERE seen_at < $1`

	_, err := tx.Exec(ctx, stmt, before)
	return err
}

func (WebhookRepo) CreateLog(ctx context.Context, tx pgx.Tx, l domain.WebhookLog, body []byte) error {
	stmt := `INSERT INTO webhook_log (
	             outcome, detail, verified, event_id, event_type, provider_order_id,
	             timestamp_header, nonce, signature_prefix, remote_addr, body, body_size
	         ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

	_, err := tx.Exec(ctx, stmt,
		l.Outcome, l.Detail, l.Verified, l.EventID, l.EventType, l.ProviderOrderID,
		l.Timestamp, l.Nonce, l.SignaturePrefix, l.RemoteAddr, body, l.BodySize,
	)
	return err
}

// ListLogsAfter returns up to limit log rows with id > afterID, oldest
// first, without bodies, joined with their inbox processing state.
func (WebhookRepo) ListLogsAfter(ctx context.Context, tx pgx.Tx, afterID int64, limit int) ([]domain.WebhookLog, error) {
	stmt := `
		SELECT w.id, w.received_at, w.outcome, w.detail, w.verified, w.event_id, w.event_type,
		       w.provider_order_id, w.timestamp_header, w.nonce, w.signature_prefix, w.remote_addr,
		       w.body_size, e.client_order_id, e.processed_at, e.process_error
		FROM webhook_log w
		LEFT JOIN provider_events e ON e.event_id = w.event_id AND w.outcome = 'accepted'
		WHERE w.id > $1
		ORDER BY w.id
		LIMIT $2`

	return queryWebhookLogs(ctx, tx, stmt, afterID, limit)
}

// ListLatestLogs returns the newest limit log rows, newest first.
func (WebhookRepo) ListLatestLogs(ctx context.Context, tx pgx.Tx, limit int) ([]domain.WebhookLog, error) {
	stmt := `
		SELECT w.id, w.received_at, w.outcome, w.detail, w.verified, w.event_id, w.event_type,
		       w.provider_order_id, w.timestamp_header, w.nonce, w.signature_prefix, w.remote_addr,
		       w.body_size, e.client_order_id, e.processed_at, e.process_error
		FROM webhook_log w
		LEFT JOIN provider_events e ON e.event_id = w.event_id AND w.outcome = 'accepted'
		ORDER BY w.id DESC
		LIMIT $1`

	return queryWebhookLogs(ctx, tx, stmt, limit)
}

// GetLog returns one log row with its body.
func (WebhookRepo) GetLog(ctx context.Context, tx pgx.Tx, id int64) (domain.WebhookLog, error) {
	stmt := `
		SELECT w.id, w.received_at, w.outcome, w.detail, w.verified, w.event_id, w.event_type,
		       w.provider_order_id, w.timestamp_header, w.nonce, w.signature_prefix, w.remote_addr,
		       w.body_size, e.client_order_id, e.processed_at, e.process_error, w.body
		FROM webhook_log w
		LEFT JOIN provider_events e ON e.event_id = w.event_id AND w.outcome = 'accepted'
		WHERE w.id = $1`

	var l domain.WebhookLog
	var body []byte
	err := tx.QueryRow(ctx, stmt, id).Scan(
		&l.ID, &l.ReceivedAt, &l.Outcome, &l.Detail, &l.Verified, &l.EventID, &l.EventType,
		&l.ProviderOrderID, &l.Timestamp, &l.Nonce, &l.SignaturePrefix, &l.RemoteAddr,
		&l.BodySize, &l.OrderID, &l.ProcessedAt, &l.ProcessError, &body,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WebhookLog{}, domain.ErrNotFound
	}
	text := strings.ToValidUTF8(string(body), "�")
	l.Body = &text
	return l, err
}

func (WebhookRepo) DeleteLogsReceivedBefore(ctx context.Context, tx pgx.Tx, before time.Time) error {
	stmt := `DELETE FROM webhook_log WHERE received_at < $1`

	_, err := tx.Exec(ctx, stmt, before)
	return err
}

func queryWebhookLogs(ctx context.Context, tx pgx.Tx, stmt string, args ...any) ([]domain.WebhookLog, error) {
	rows, err := tx.Query(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	logs := []domain.WebhookLog{}
	for rows.Next() {
		var l domain.WebhookLog
		if err := rows.Scan(
			&l.ID, &l.ReceivedAt, &l.Outcome, &l.Detail, &l.Verified, &l.EventID, &l.EventType,
			&l.ProviderOrderID, &l.Timestamp, &l.Nonce, &l.SignaturePrefix, &l.RemoteAddr,
			&l.BodySize, &l.OrderID, &l.ProcessedAt, &l.ProcessError,
		); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}
