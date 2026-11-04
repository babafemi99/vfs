-- Phase 2: background jobs (the outbox). status = 'dead' is the dead-letter queue.

CREATE TABLE jobs (
    id           BIGSERIAL PRIMARY KEY,
    kind         TEXT NOT NULL,                 -- submit_order | ...
    order_id     TEXT NULL REFERENCES orders (id),
    status       TEXT NOT NULL CHECK (status IN ('pending', 'running', 'done', 'dead')),
    attempts     INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL CHECK (max_attempts > 0),
    run_at       TIMESTAMPTZ NOT NULL,          -- not before this time
    locked_until TIMESTAMPTZ NULL,              -- lease while running; expired = claimable again
    last_error   TEXT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Claiming looks only at pending and running jobs.
CREATE INDEX jobs_claim_idx ON jobs (run_at) WHERE status IN ('pending', 'running');
CREATE INDEX jobs_status_idx ON jobs (status, updated_at DESC);
CREATE INDEX jobs_order_idx ON jobs (order_id) WHERE order_id IS NOT NULL;
