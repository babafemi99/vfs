-- Phase 4: reconciliation runs. Each run's full report is kept as JSON.

CREATE TABLE recon_runs (
    id             TEXT PRIMARY KEY,         -- recon-<ULID>
    started_at     TIMESTAMPTZ NOT NULL,
    finished_at    TIMESTAMPTZ NOT NULL,
    orders_checked INT NOT NULL,
    mismatches     INT NOT NULL,
    invariants_ok  BOOLEAN NOT NULL,
    report         JSONB NOT NULL
);
