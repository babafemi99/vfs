# tradeapp — the trade lifecycle, end to end

A small, correctness-first trading app that sits on top of **brokersim** (the fake provider). One demo user, no auth. You can:

1. **Fund** your account (a button that adds cash).
2. **Get a quote** and **place an order** from it (buy or sell).
3. Watch the provider **fill** or **partially fill** it, live.
4. **Cancel** an open order and see the honest outcome.
5. See **balances, held funds, positions and P&L** that always add up.

Same spirit as 2Remit: explicit state machine, idempotent APIs, signed webhooks into an inbox, durable activity, live updates over SSE, tests as acceptance criteria. The difference is money: here a small **double-entry ledger** holds and spends cash, and positions track shares.

---

## Instructions for Claude Code

- Build in the **milestone order** at the bottom. After each milestone run the tests (`go test -race ./...`, and `npm test` / `npm run build` for the web app) and make them pass before moving on.
- **Backend:** Go 1.22+, `net/http` with Go 1.22 routing, `log/slog`, PostgreSQL 17 via `github.com/jackc/pgx/v5`. That is the only Go dependency allowed. Plain SQL migrations embedded with `embed` and applied at startup.
- **Frontend:** Vite + React + TypeScript, TanStack Query for server state, plain CSS (no UI kit). Money formatted from integer minor units; no floats in money maths.
- **Provider:** brokersim, running separately (see its README). Talk to it only over HTTP. Never share its database or code.
- All money and prices are `BIGINT` minor units. One currency for the demo (display as `$`, value in cents). Quantities are whole shares.
- Every state change goes through the order state machine and the ledger service. No handler writes balances directly.
- If anything here is ambiguous, choose the simplest option and record it in `DECISIONS.md`.
- Write every file in full. No zip archives.

---

## Scope

**In:** fund account, prices, quotes, place order (buy and sell), provider submission with retries, signed webhooks into an inbox, fills and partial fills, cancel with pending-cancel race, reject, expire, ledger with holds, positions with average cost and P&L, stuck-order sweeper, reconciliation report, SSE live updates, a single-page web UI, Docker Compose.

**Out:** auth and multiple users (there is one user, `user_id = 1`), KYC, FX, withdrawals, real providers, margin, short selling, order types beyond "accept this quote".

---

## Layout

```
tradeapp/
├── backend/
│   ├── cmd/api/main.go
│   ├── internal/
│   │   ├── config/config.go
│   │   ├── db/
│   │   │   ├── db.go                 # pgx pool, tx helper
│   │   │   └── migrations/*.sql      # embedded, applied in order at startup
│   │   ├── ledger/ledger.go          # post balanced journal entries, holds, balances
│   │   ├── positions/positions.go    # share holdings, avg cost, realized P&L
│   │   ├── orders/
│   │   │   ├── states.go             # local order state machine
│   │   │   ├── service.go            # quote, place, cancel, apply provider snapshot
│   │   │   └── idempotency.go
│   │   ├── provider/client.go        # brokersim HTTP client
│   │   ├── outbox/relay.go           # submits orders and cancels to the provider
│   │   ├── inbox/
│   │   │   ├── webhook.go            # verify signature, store raw event, 200
│   │   │   └── processor.go          # apply inbox events in transactions
│   │   ├── sweeper/sweeper.go        # poll provider for stuck orders
│   │   ├── recon/recon.go            # compare local vs provider, report breaks
│   │   ├── stream/sse.go             # SSE fan-out of activity
│   │   └── api/                      # handlers, JSON errors, router
│   └── go.mod
├── web/                              # Vite + React + TS
│   └── src/
│       ├── api.ts                    # fetch wrappers, idempotency keys
│       ├── money.ts                  # format/parse minor units
│       ├── useStream.ts              # SSE → invalidate queries
│       └── components/               # BalanceCard, FundDialog, OrderTicket, OpenOrders,
│                                     # OrderHistory, Positions, Activity, PricesTable
├── compose.yaml                      # postgres + brokersim + api + web
├── Makefile
├── .env.example
├── DECISIONS.md
└── README.md
```

---

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `API_ADDR` | `:8091` | Backend address (brokersim's default webhook URL points here) |
| `DATABASE_URL` | `postgres://trade:trade@localhost:5432/trade?sslmode=disable` | Postgres |
| `PROVIDER_URL` | `http://localhost:8090` | brokersim |
| `PROVIDER_API_KEY` | `sim_test_key` | Sent as `X-Api-Key` |
| `PROVIDER_WEBHOOK_SECRET` | `whsec_test` | Must match brokersim |
| `WEBHOOK_TOLERANCE` | `5m` | Max signature age |
| `SWEEP_INTERVAL` | `15s` | Stuck-order sweeper |
| `STUCK_AFTER` | `30s` | Order considered stuck if not updated for this long in a non-final state |
| `WEB_ORIGIN` | `http://localhost:5173` | CORS allow-origin for the web app |

Webhook route: `POST /webhooks/provider` (matches brokersim's default `SIM_WEBHOOK_URL`).

---

## Data model (Postgres)

```sql
-- ledger: accounts and balanced journal entries
accounts(id BIGSERIAL PK, code TEXT UNIQUE NOT NULL, owner_id BIGINT NULL,
         balance BIGINT NOT NULL DEFAULT 0)
-- codes: 'user:1:cash_available', 'user:1:cash_held',
--        'system:funding', 'system:broker_clearing', 'system:fees'
-- user accounts carry CHECK-equivalent guard in code: balance >= 0

journal_entries(id BIGSERIAL PK, kind TEXT NOT NULL,          -- fund | hold | release | fill_buy | fill_sell | fee
                source_type TEXT NOT NULL, source_id TEXT NOT NULL,
                created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                UNIQUE (kind, source_type, source_id))        -- one posting per business event

postings(id BIGSERIAL PK, entry_id BIGINT REFERENCES journal_entries,
         account_id BIGINT REFERENCES accounts, amount BIGINT NOT NULL) -- signed; sum per entry = 0

-- shares
positions(user_id BIGINT, symbol TEXT, qty BIGINT NOT NULL DEFAULT 0,
          held_qty BIGINT NOT NULL DEFAULT 0,                 -- reserved by open SELL orders
          avg_cost BIGINT NOT NULL DEFAULT 0,                 -- per share, minor units
          realized_pnl BIGINT NOT NULL DEFAULT 0,
          PRIMARY KEY (user_id, symbol),
          CHECK (qty >= 0), CHECK (held_qty >= 0), CHECK (held_qty <= qty))

position_movements(id BIGSERIAL PK, user_id BIGINT, symbol TEXT, fill_id TEXT UNIQUE,
                   qty_delta BIGINT, price BIGINT, realized_pnl BIGINT, created_at TIMESTAMPTZ)

-- quotes cached from the provider
quotes(id TEXT PK,                     -- provider quote id
       symbol TEXT, side TEXT, qty BIGINT, price BIGINT, fee BIGINT, total BIGINT,
       expires_at TIMESTAMPTZ, created_at TIMESTAMPTZ)

-- orders
orders(id UUID PK,                     -- ALSO sent to provider as client_order_id
       user_id BIGINT, quote_id TEXT UNIQUE REFERENCES quotes,
       provider_order_id TEXT UNIQUE NULL,
       symbol TEXT, side TEXT, qty BIGINT, price BIGINT,
       filled_qty BIGINT NOT NULL DEFAULT 0, avg_fill_price BIGINT NOT NULL DEFAULT 0,
       hold_amount BIGINT NOT NULL,    -- cash held (BUY) or shares held (SELL)
       hold_remaining BIGINT NOT NULL, -- what is still held right now
       status TEXT NOT NULL, reason TEXT NULL,
       provider_version BIGINT NOT NULL DEFAULT 0,
       created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ)
-- index: (status) WHERE status NOT IN ('FILLED','CANCELLED','REJECTED','EXPIRED')

order_fills(fill_id TEXT PK, order_id UUID REFERENCES orders, qty BIGINT, price BIGINT,
            fee BIGINT, created_at TIMESTAMPTZ)

idempotency_keys(user_id BIGINT, key TEXT, request_hash TEXT NOT NULL,
                 response_code INT NULL, response_body JSONB NULL,
                 created_at TIMESTAMPTZ DEFAULT now(), PRIMARY KEY (user_id, key))

outbox(id BIGSERIAL PK, kind TEXT,     -- submit_order | cancel_order
       order_id UUID, attempts INT DEFAULT 0, next_attempt_at TIMESTAMPTZ,
       done_at TIMESTAMPTZ NULL, last_error TEXT NULL)

provider_events(event_id TEXT PK, type TEXT, provider_order_id TEXT NULL,
                payload JSONB, received_at TIMESTAMPTZ, processed_at TIMESTAMPTZ NULL,
                process_error TEXT NULL)

activity(id BIGSERIAL PK, order_id UUID NULL, kind TEXT, message TEXT,
         data JSONB, created_at TIMESTAMPTZ)   -- durable feed; SSE replays from a cursor
```

Seed on startup (idempotent): the five accounts and `user_id = 1`.

---

## Ledger rules

Balance of an account = sum of its postings, also cached in `accounts.balance` and updated in the **same transaction**. Every journal entry sums to zero. User account balances may never go negative: lock the account rows with `SELECT ... FOR UPDATE` (always in `id` order) and check before posting.

| Event | Postings |
| --- | --- |
| Fund `A` | `system:funding −A`, `user:cash_available +A` |
| Place BUY, hold `T` (quote total) | `cash_available −T`, `cash_held +T` |
| BUY fill `q` at `p`, fee `f` | `cash_held −(q·p+f)`, `system:broker_clearing +(q·p)`, `system:fees +f` |
| BUY final (FILLED, CANCELLED, EXPIRED, REJECTED) | release `hold_remaining`: `cash_held −r`, `cash_available +r` |
| SELL fill `q` at `p`, fee `f` | `system:broker_clearing −(q·p)`, `cash_available +(q·p−f)`, `system:fees +f` |

SELL orders hold **shares**, not cash: `positions.held_qty += qty` on place; each fill reduces `qty` and `held_qty`; final state releases what is left of `held_qty`.

If a BUY fill would need more than `hold_remaining` (fee rounding), take the difference from `cash_available`; if that's also short, record a reconciliation break rather than failing the fill. **A fill is a fact; it is always recorded.**

Positions on BUY fill: new `avg_cost = (qty·avg_cost + q·p) / (qty + q)` (integer division, document rounding). On SELL fill: `realized = (p − avg_cost)·q − f`, add to `realized_pnl`; `avg_cost` unchanged.

---

## Order state machine (local)

| From | To | Cause |
| --- | --- | --- |
| (new) | `PENDING_SUBMIT` | `POST /api/orders` committed (funds or shares held, outbox row written) |
| `PENDING_SUBMIT` | `OPEN` | Provider accepted (accept response or `order.accepted`) |
| `PENDING_SUBMIT` | `REJECTED` | Provider says `quote_expired`, `market_closed`, `quote_already_accepted`, or validation error |
| `PENDING_SUBMIT` | `CANCELLED` | User cancels **before** the outbox has sent it (local cancel, guarded update) |
| `OPEN` | `PARTIALLY_FILLED` / `FILLED` | Fill |
| `PARTIALLY_FILLED` | `PARTIALLY_FILLED` / `FILLED` | Fill |
| `OPEN`, `PARTIALLY_FILLED` | `PENDING_CANCEL` | `DELETE /api/orders/{id}` |
| `PENDING_CANCEL` | `PENDING_CANCEL` | Partial fill while cancelling |
| `PENDING_CANCEL` | `FILLED` | Fill completes the order (the cancel will be rejected) |
| `PENDING_CANCEL` | `CANCELLED` | `order.cancelled` |
| `PENDING_CANCEL` | `OPEN` / `PARTIALLY_FILLED` | `order.cancel_rejected` while the order is still working (derive from `filled_qty`) |
| `OPEN`, `PARTIALLY_FILLED` | `REJECTED` / `EXPIRED` | Provider event |

Rules:

- Final states: `FILLED`, `CANCELLED`, `REJECTED`, `EXPIRED`. **First final state wins**; later events for a final order are stored in the inbox, logged in activity as "ignored", and never change state or money. One exception: a **fill** for a final order is still posted (see ledger rules) and raises a reconciliation break.
- Status changes use guarded updates: `UPDATE orders SET status=$new, provider_version=$v ... WHERE id=$id AND status = ANY($allowed) AND provider_version < $v`.
- Fills are applied by `fill_id` (unique), regardless of event order or version. `filled_qty` is then set to the provider's cumulative `filled_qty` when the snapshot's version is newer.
- On reaching a final state, release `hold_remaining` in the same transaction.

---

## API (no auth; JSON errors `{ "error": { "code", "message" } }`)

| Method & path | What it does |
| --- | --- |
| `GET /api/account` | Available cash, held cash, total, positions with market value and unrealized P&L (using provider prices), realized P&L |
| `POST /api/account/fund` `{ "amount": 100000 }` | Adds cash. **Requires `Idempotency-Key`.** Max 10,000,000 per call. |
| `GET /api/prices` | Proxies brokersim `GET /v1/prices` (short cache, 1s) |
| `POST /api/quotes` `{ "symbol", "side", "qty" }` | Calls provider, stores quote, returns it with `expires_at` |
| `POST /api/orders` `{ "quote_id" }` | Places an order from a quote. **Requires `Idempotency-Key`.** See flow below. |
| `GET /api/orders?status=open\|all` | Orders, newest first |
| `GET /api/orders/{id}` | One order with fills and its activity |
| `DELETE /api/orders/{id}` | Cancel. Idempotent. |
| `GET /api/activity?after=<id>` | Activity feed page |
| `GET /api/stream?after=<id>` | **SSE**: replays activity after the cursor, then streams new items |
| `GET /api/recon` | Reconciliation report (see below) |
| `POST /webhooks/provider` | Provider webhooks |

### Idempotency (fund and place order)

- Missing header → `400`.
- In one transaction: `INSERT INTO idempotency_keys ... ON CONFLICT DO NOTHING`.
    - Inserted → do the work in the same transaction, store response code and body, commit.
    - Exists, same `request_hash`, response stored → return the stored response.
    - Exists, same hash, no response yet (concurrent) → `409 request_in_progress`.
    - Exists, different hash → `422 idempotency_conflict`.
- Keys older than 24h are deleted by a periodic job.

### Place order flow

1. Idempotency check (above).
2. Load the quote. Reject with `409 quote_expired` if `expires_at` has passed (saves a round trip). Reject `409 quote_already_used` if an order exists for it.
3. **BUY:** lock cash accounts; require `cash_available >= quote.total`; post the hold. **SELL:** lock the position row; require `qty − held_qty >= quote.qty`; increase `held_qty`.
4. Insert order `PENDING_SUBMIT` with `hold_amount = hold_remaining` = cash total or share qty.
5. Insert outbox `submit_order`. Insert activity. Commit. Return `201` with the order.
6. **Outbox relay** (separate goroutine) calls `POST /v1/quotes/{quote_id}/accept` with `client_order_id = order.id`:
    - `201`/`200` → store `provider_order_id`, move to `OPEN` (guarded; webhooks may have moved it further already).
    - `409 quote_expired | market_closed | quote_already_accepted` (with a different key) or `400` → `REJECTED`, release hold.
    - `500`, timeout, connection error → **unknown**: retry the same call with the same `client_order_id` (backoff 1s, 2s, 4s… max 6). Before each retry, `GET /v1/orders?client_order_id=` — if the provider has it, adopt it instead of retrying. After max attempts, leave `PENDING_SUBMIT` for the sweeper.

### Cancel flow

- `PENDING_SUBMIT` and outbox not yet sent: guarded update to `CANCELLED`, mark outbox row done, release hold. Instant.
- `OPEN` / `PARTIALLY_FILLED`: guarded update to `PENDING_CANCEL`, insert outbox `cancel_order`, return `202`. The relay calls `DELETE /v1/orders/{provider_order_id}`; the result arrives as a webhook. On `500`/timeout, retry; `409 order_final` → fetch the order and apply its snapshot.
- Already `PENDING_CANCEL` → `202`, no new outbox row.
- Final → `409 order_final`.
- **Never release cash or shares on the cancel request.** Only on the final state.

---

## Webhooks in → inbox → processor

**Handler** (`POST /webhooks/provider`):

1. Read the raw body (limit 1 MB).
2. Verify `X-Sim-Signature` = hex(HMAC-SHA512(secret, `X-Sim-Timestamp + "." + body`)) with constant-time compare; reject if older than `WEBHOOK_TOLERANCE` → `401`.
3. Parse minimally, `INSERT INTO provider_events ... ON CONFLICT (event_id) DO NOTHING`.
4. Return `200` (also for duplicates and for unknown orders). Nudge the processor.

**Processor** (goroutine, also polls every second): takes unprocessed events oldest first with `FOR UPDATE SKIP LOCKED`, and for each, in one transaction:

1. Find the order by `provider_order_id`, or by `client_order_id` (= our order id) if the provider id isn't stored yet. Unknown → mark processed with `process_error = 'unknown_order'`, add activity.
2. If the event has a `fill`: insert `order_fills` (skip if `fill_id` exists), post ledger and position movements.
3. If the snapshot's `version` > `orders.provider_version`: apply the status transition if allowed, set `filled_qty`, `avg_fill_price`, `provider_version`. If the transition isn't allowed (e.g. stale or after final), log it as ignored.
4. If the order is now final, release `hold_remaining`.
5. Insert activity, mark the event processed. Commit, then publish to SSE.

`quote.expired` events only update activity.

---

## Sweeper and reconciliation

**Sweeper** every `SWEEP_INTERVAL`: orders in `PENDING_SUBMIT`, `OPEN`, `PARTIALLY_FILLED`, `PENDING_CANCEL` not updated for `STUCK_AFTER` → `GET /v1/orders/{provider_order_id}` (or by `client_order_id`). Apply the returned snapshot **through the same code path as the processor**, including any fills in `fills[]` not yet recorded. This is how dropped webhooks are recovered.

**Recon report** (`GET /api/recon`): for each local order, compare `status`, `filled_qty` and fill ids with the provider's `GET /v1/orders/{id}`. Also check internal invariants. Return a list of breaks (`order_id`, field, local, provider) and invariant results. Show it on a small panel in the UI.

Invariants (also in tests):

- Every journal entry sums to zero; sum of all account balances = 0.
- `cash_held` balance = sum of `hold_remaining` over non-final BUY orders.
- For each symbol, `held_qty` = sum of `hold_remaining` over non-final SELL orders.
- No user balance or position quantity is negative.
- `filled_qty` = sum of `order_fills.qty` for every order.

---

## Live updates (SSE)

`GET /api/stream?after=<activity_id>` sends `event: activity` with the activity row as JSON, replaying anything after the cursor from Postgres, then streaming new rows. Heartbeat comment every 15s. In-process fan-out is fine (single API instance), same trade-off as 2Remit; note `LISTEN/NOTIFY` as the multi-instance upgrade in `DECISIONS.md`.

Web: `useStream` keeps the last activity id, reconnects with backoff, and on each event invalidates the `account`, `orders` and the affected `order` queries.

---

## Web UI (single page)

- **Balance card:** available, held, total cash; positions value; realized and unrealized P&L. A **Fund account** button opens a dialog (amount, quick picks $1,000 / $10,000), generates one idempotency key when the dialog opens, disables the button while submitting.
- **Prices table:** symbol, bid, ask, refreshed every second.
- **Order ticket:** symbol, Buy/Sell toggle (colour plus text), quantity → **Get quote** → shows price, fee, total and a **countdown**. **Confirm** places the order with an idempotency key generated when the quote was shown; button disabled while in flight. Expired quote → "Quote expired, get a new one". Insufficient funds or shares → message plus a Fund button.
- **Open orders:** status pill, progress bar `filled / qty`, avg price, **Cancel** button. While `PENDING_CANCEL` show "Cancelling… 4 of 10 filled".
- **Order history:** final orders with an honest outcome line: "Filled", "6 of 10 bought, 4 cancelled, $X returned", "Filled before it could be cancelled", "Rejected: quote expired", "Expired: 3 of 10 bought".
- **Positions:** symbol, qty, held, avg cost, last price, market value, unrealized P&L.
- **Activity:** live feed from SSE.
- **Recon:** button to run the report, shows breaks and invariant checks.
- A link to brokersim's dev panel (`http://localhost:8090/dev`) to force fills, rejects and chaos.

---

## Required tests

Backend (real Postgres via Compose or `DATABASE_URL`; each test in its own schema or truncated tables):

- **Fund idempotency:** same key twice → one journal entry; same key different amount → `422`.
- **Place idempotency:** same key twice → one order, one hold; 20 concurrent requests with the same key → one order.
- **No double spend:** with $1,000 available, 20 concurrent orders of $100 each from different quotes → at most 10 succeed; available never negative.
- **Fills:** a duplicate fill webhook posts once; a partial then full fill leaves `hold_remaining = 0` and releases any rounding remainder.
- **Out-of-order events:** `order.filled` (version 4) before `order.partially_filled` (version 3) → final `FILLED`, both fills posted once.
- **Cancel race:** fill arrives during `PENDING_CANCEL` → posted; then `order.cancelled` → `CANCELLED` with `filled_qty > 0`, remaining hold released; `cancel_rejected` after `FILLED` → no change.
- **Late fill after final:** fill posted, break recorded.
- **Ambiguous accept:** provider returns 500 after creating the order → relay adopts it via `client_order_id`, no duplicate.
- **Dropped webhooks:** sweeper recovers missing fills from `GET /v1/orders/{id}`.
- **Signature:** bad signature or old timestamp → `401`, nothing stored.
- **Invariants** run after every test above.

Web: Vitest for `money.ts` formatting and the outcome-line function; build passes with strict TypeScript.

End to end (script `make e2e`): fund, quote, place, wait for fill via polling `/api/orders/{id}`, cancel a second order, then turn on brokersim chaos (`duplicate_rate 0.3`, `delay_max_ms 3000`, `drop_rate 0.1`, `race_fill_on_cancel 0.5`, `accept_500_after_rate 0.2`), run 50 random orders and cancels, wait for the sweeper, and assert `/api/recon` shows no breaks and all invariants pass.

---

## Run

```bash
cp .env.example .env
make compose-up      # postgres, brokersim, api, web
# web:        http://localhost:5173
# api:        http://localhost:8091
# brokersim:  http://localhost:8090/dev
```

---

## Milestones

1. **DB + ledger:** migrations, seed, ledger service with holds and releases, positions maths, invariant checker. Tests.
2. **Fund + account API** with idempotency. Tests.
3. **Provider client + quotes + place order** (hold, outbox, relay, ambiguous-accept handling). Tests against brokersim or an `httptest` fake.
4. **Webhook inbox + processor:** signature check, fills, state machine, final-state release. Tests including out-of-order and duplicates.
5. **Cancel flow** (local and provider), race handling. Tests.
6. **Sweeper + recon report.** Tests with dropped webhooks.
7. **Activity + SSE.**
8. **Web UI.**
9. **Compose, Makefile, e2e chaos script, `DECISIONS.md`.**

## Decision log seeds (copy into `DECISIONS.md` and extend)

- Our order id is the provider `client_order_id`, so retries can't create duplicates at the provider and we can always find our order there.
- Cash is held at quote total on placement and only released on a final state, never on a cancel request.
- A fill is a fact: always recorded, even after a final state; contradictions become recon breaks, not rejected events.
- Status transitions use the provider's `version`; fills use `fill_id`. Neither depends on webhook arrival order.
- The inbox stores raw events before any processing so nothing received is ever lost.
