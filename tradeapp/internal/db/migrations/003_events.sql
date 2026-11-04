-- Phase 3: webhook inbox, event jobs, breaks.

-- Every webhook is stored here before any processing; event_id dedupes
-- provider retries and duplicates.
CREATE TABLE provider_events (
    event_id          TEXT PRIMARY KEY,
    type              TEXT NOT NULL,
    provider_order_id TEXT NULL,
    client_order_id   TEXT NULL,
    payload           JSONB NOT NULL,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at      TIMESTAMPTZ NULL,
    process_error     TEXT NULL
);
CREATE INDEX provider_events_unprocessed_idx ON provider_events (received_at) WHERE processed_at IS NULL;

-- process_event jobs point at an inbox row.
ALTER TABLE jobs ADD COLUMN event_id TEXT NULL REFERENCES provider_events (event_id);

-- Contradictions between us and the provider, kept for reconciliation.
CREATE TABLE breaks (
    id         BIGSERIAL PRIMARY KEY,
    order_id   TEXT NULL REFERENCES orders (id),
    kind       TEXT NOT NULL,
    detail     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The sweeper looks for active orders that have gone quiet.
CREATE INDEX orders_active_updated_idx ON orders (updated_at)
    WHERE status NOT IN ('FILLED', 'CANCELLED', 'REJECTED', 'EXPIRED');
