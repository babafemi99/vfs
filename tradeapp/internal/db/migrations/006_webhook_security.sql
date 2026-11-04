-- Replay protection and the webhook log.

-- Every nonce seen in a verified webhook. A nonce seen again within the
-- timestamp tolerance is a replay. Older rows are deleted: the timestamp
-- check already refuses anything that old.
CREATE TABLE webhook_nonces (
    nonce   TEXT PRIMARY KEY,
    seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX webhook_nonces_seen_idx ON webhook_nonces (seen_at);

-- Every webhook request, accepted or not, for the console's inspector.
-- body is what arrived (capped); for rejected requests it is unverified.
CREATE TABLE webhook_log (
    id                BIGSERIAL PRIMARY KEY,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    outcome           TEXT NOT NULL,      -- accepted | duplicate | rejected_* | invalid_body | error
    detail            TEXT NULL,
    verified          BOOLEAN NOT NULL,   -- signature, timestamp and nonce all checked out
    event_id          TEXT NULL,
    event_type        TEXT NULL,
    provider_order_id TEXT NULL,
    timestamp_header  TEXT NULL,
    nonce             TEXT NULL,
    signature_prefix  TEXT NULL,          -- first characters only; never the whole signature
    remote_addr       TEXT NULL,
    body              BYTEA NOT NULL,
    body_size         INT NOT NULL
);
CREATE INDEX webhook_log_received_idx ON webhook_log (received_at);
