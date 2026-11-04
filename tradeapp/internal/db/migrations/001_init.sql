-- Phase 1: users, ledger, positions, orders, idempotency, activity.
-- Money and prices are BIGINT minor units (cents). Quantities are whole shares.

CREATE TABLE users (
    id         BIGINT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Ledger ------------------------------------------------------------------

CREATE TABLE accounts (
    id       BIGSERIAL PRIMARY KEY,
    code     TEXT NOT NULL UNIQUE,                -- user:1:cash_available, system:fees, ...
    owner_id BIGINT NULL REFERENCES users (id),   -- NULL for system accounts
    balance  BIGINT NOT NULL DEFAULT 0,           -- cache of SUM(postings.amount)
    CHECK (owner_id IS NULL OR balance >= 0)      -- user accounts never go negative
);

CREATE TABLE journal_entries (
    id          BIGSERIAL PRIMARY KEY,
    kind        TEXT NOT NULL,                    -- fund | hold | release | fill_buy | fill_sell
    source_type TEXT NOT NULL,
    source_id   TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, source_type, source_id)         -- one posting per business event
);

CREATE TABLE postings (
    id         BIGSERIAL PRIMARY KEY,
    entry_id   BIGINT NOT NULL REFERENCES journal_entries (id),
    account_id BIGINT NOT NULL REFERENCES accounts (id),
    amount     BIGINT NOT NULL CHECK (amount <> 0) -- signed; sum per entry = 0
);
CREATE INDEX postings_entry_idx ON postings (entry_id);
CREATE INDEX postings_account_idx ON postings (account_id);

-- Shares ------------------------------------------------------------------

CREATE TABLE positions (
    user_id      BIGINT NOT NULL REFERENCES users (id),
    symbol       TEXT NOT NULL,
    qty          BIGINT NOT NULL DEFAULT 0,
    held_qty     BIGINT NOT NULL DEFAULT 0,       -- reserved by open SELL orders
    avg_cost     BIGINT NOT NULL DEFAULT 0,       -- per share, minor units
    realized_pnl BIGINT NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, symbol),
    CHECK (qty >= 0), CHECK (held_qty >= 0), CHECK (held_qty <= qty), CHECK (avg_cost >= 0)
);

CREATE TABLE position_movements (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT NOT NULL,
    symbol       TEXT NOT NULL,
    fill_id      TEXT NOT NULL UNIQUE,            -- a fill moves a position once
    qty_delta    BIGINT NOT NULL,
    price        BIGINT NOT NULL,
    realized_pnl BIGINT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Quotes and orders --------------------------------------------------------

CREATE TABLE quotes (
    id         TEXT PRIMARY KEY,                  -- provider quote id
    symbol     TEXT NOT NULL,
    side       TEXT NOT NULL CHECK (side IN ('BUY', 'SELL')),
    qty        BIGINT NOT NULL CHECK (qty > 0),
    price      BIGINT NOT NULL CHECK (price > 0),
    fee        BIGINT NOT NULL CHECK (fee >= 0),
    total      BIGINT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE orders (
    id                TEXT PRIMARY KEY,             -- order-<ULID>; also the provider client_order_id
    user_id           BIGINT NOT NULL REFERENCES users (id),
    quote_id          TEXT NOT NULL UNIQUE REFERENCES quotes (id),
    provider_order_id TEXT NULL UNIQUE,
    symbol            TEXT NOT NULL,
    side              TEXT NOT NULL CHECK (side IN ('BUY', 'SELL')),
    qty               BIGINT NOT NULL CHECK (qty > 0),
    price             BIGINT NOT NULL CHECK (price > 0),
    filled_qty        BIGINT NOT NULL DEFAULT 0 CHECK (filled_qty >= 0 AND filled_qty <= qty),
    avg_fill_price    BIGINT NOT NULL DEFAULT 0,
    hold_amount       BIGINT NOT NULL CHECK (hold_amount >= 0),    -- cash (BUY) or shares (SELL)
    hold_remaining    BIGINT NOT NULL CHECK (hold_remaining >= 0), -- still held right now
    status            TEXT NOT NULL,
    reason            TEXT NULL,
    provider_version  BIGINT NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX orders_active_idx ON orders (status)
    WHERE status NOT IN ('FILLED', 'CANCELLED', 'REJECTED', 'EXPIRED');
CREATE INDEX orders_user_created_idx ON orders (user_id, created_at DESC);

CREATE TABLE order_fills (
    fill_id    TEXT PRIMARY KEY,                  -- provider fill id
    order_id   TEXT NOT NULL REFERENCES orders (id),
    qty        BIGINT NOT NULL CHECK (qty > 0),
    price      BIGINT NOT NULL,
    fee        BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX order_fills_order_idx ON order_fills (order_id);

-- Idempotency and activity -------------------------------------------------

CREATE TABLE idempotency_keys (
    user_id       BIGINT NOT NULL,
    key           TEXT NOT NULL,
    request_hash  TEXT NOT NULL,
    response_code INT NULL,                       -- NULL while the request is in progress
    response_body BYTEA NULL,                     -- exact bytes, so replays are identical
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);
CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at);

CREATE TABLE activity (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL,
    order_id   TEXT NULL,
    kind       TEXT NOT NULL,
    message    TEXT NOT NULL,
    data       JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX activity_order_idx ON activity (order_id) WHERE order_id IS NOT NULL;

-- Seed ---------------------------------------------------------------------

INSERT INTO users (id, name) VALUES (1, 'Demo User') ON CONFLICT DO NOTHING;

INSERT INTO accounts (code, owner_id) VALUES
    ('system:funding', NULL),
    ('system:broker_clearing', NULL),
    ('system:fees', NULL),
    ('user:1:cash_available', 1),
    ('user:1:cash_held', 1)
ON CONFLICT (code) DO NOTHING;
