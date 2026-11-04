-- Idempotency records get an explicit lifecycle:
--   STARTED    the request is running (a second request with the key gets 409)
--   COMPLETED  2xx, response stored and replayed
--   FAILED     with a response: a business rejection (4xx), replayed as is
--              without one: released (5xx, or a zombie reaped by the sweeper),
--              so the same key may run again
ALTER TABLE idempotency_keys
    ADD COLUMN status       TEXT        NOT NULL DEFAULT 'STARTED'
        CHECK (status IN ('STARTED', 'COMPLETED', 'FAILED')),
    ADD COLUMN request_path TEXT        NOT NULL DEFAULT '',
    ADD COLUMN action       TEXT        NOT NULL DEFAULT '',
    ADD COLUMN updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN expires_at   TIMESTAMPTZ NOT NULL DEFAULT now() + interval '24 hours';

UPDATE idempotency_keys SET status = 'COMPLETED' WHERE response_code IS NOT NULL;

DROP INDEX idempotency_keys_created_idx;
CREATE INDEX idempotency_keys_expires_idx ON idempotency_keys (expires_at);
CREATE INDEX idempotency_keys_started_idx ON idempotency_keys (updated_at) WHERE status = 'STARTED';
