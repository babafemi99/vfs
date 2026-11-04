# Decisions

Choices made where `lab/docs/broker.md` was ambiguous, or where we cut scope.

## Scope
- **No chaos and no dev panel.** For this exercise we assume the provider is reliable. Stages: 1 core, 2 API + webhooks, 3 engine, then the `/dev` API (without chaos).
- Lives in `brokersim/` as its own Go module (module path still `github.com/babafem99/vfs/lab/brokersim`).

## Domain
- **Pending cancel is tracked in `Order.CancelDueAt`, separate from `Status`.** A fill can complete the order (`FILLED`) while a cancel is pending; when the cancel comes due it must still emit `order.cancel_rejected` (`too_late_to_cancel`) without changing the order.
- The too-late cancel_rejected does not bump `Version` ("changes nothing"); the reason travels on the event (`Event.Reason`), not on the order.
- `RejectCancel` on a working order sets `Order.Reason` (default `cancel_rejected`) and bumps `Version`.
- `AvgFillPrice` is `0` until the first fill, then the weighted average (always equal to `Price` in v1).
- A quote is valid strictly before `ExpiresAt`; accepting at exactly `ExpiresAt` is `quote_expired`.
- `GET` on a quote past expiry shows `EXPIRED` immediately, but the `quote.expired` event is only emitted once, by the engine (`Store.ExpireQuote`).
- Domain methods return the event they produce; a nil event means "no change" (e.g. a second cancel).

## Store
- One mutex for all state. Events go to the sink **while the lock is held**, so they reach it in exactly the order the changes happened. The sink must not block or call back into the store (stage 2's dispatcher will just append to a queue).
- Accept checks, in order: idempotency replay/conflict on `client_order_id`, quote exists, quote already accepted, market open, quote expired. A replay of an existing order succeeds even if the market has since closed.
- `ListOrders(since)` matches on `UpdatedAt >= since` (better for reconciliation than `CreatedAt`). Newest first = reverse creation order.
- IDs are `prefix_` + 12 hex chars from the seeded RNG, so a seed reproduces ids too.
- Separate seeded RNGs for market and store (`PCG(seed, 1)`, `PCG(seed, 2)`), so price moves don't shift ids or cancel delays.

## API (stage 2)
- `GET /v1/orders?client_order_id=X` returns that single order (404 if none). Without it, `GET /v1/orders` returns `{"orders": [...]}`.
- `GET /v1/prices` returns `{"prices": [...]}`.
- `409 order_final` on `DELETE` carries the order next to the error: `{"error": {...}, "order": {...}}`.
- Accept: body may be empty if `Idempotency-Key` is sent; if both are present and differ → `400 validation_error`; neither → `400 validation_error`.
- Unknown routes (and wrong methods) → JSON `404 not_found`. Every route, including unknown ones, requires `X-Api-Key`.

## Webhooks (stage 2)
- Max attempts = 1 + 6 retries (waits 1s, 2s, 4s, 8s, 16s, 32s), each with up to +20% jitter. Then `failed`.
- Payload `data` has `order` (full snapshot incl. `fills`), `quote` (quote.expired only), `fill` (fill events only), `reason` (cancel_rejected / rejected / expired).
- Body is built once at enqueue; each attempt re-signs it with a fresh timestamp. `X-Sim-Event-Id` and body `id` never change.
- First attempts go out in enqueue order from one worker; retries run on timers and rejoin the queue, so one failing event never blocks the rest. Events can therefore arrive out of order after a retry — clients use `version`.
- `Verify` rejects timestamps more than `tolerance` in the past **or** future.
- Delivery log states: `pending | delivered | failed` (no `dropped` — no chaos).

## Engine (stage 3)
- One goroutine, one `Tick()` per `SIM_TICK`, steps in spec order: prices, quote expiry, auto-fill, due cancels, order expiry. Each step re-reads state, and each change is its own store call (own lock, own domain method, own event).
- Orders and quotes are visited **oldest first** (creation order, not map order) and the engine has its own seeded RNG (`PCG(seed, 4)`), so a seed reproduces the run.
- Fill chunk = uniform in `[1, max(1, remaining*MaxChunkPct/100)]`, drawn only when the `FillProb` roll succeeds.
- Due cancels are found by `CancelDueAt <= now`, not by status, so a cancel whose order was filled meanwhile still gets `cancel_rejected` (`too_late_to_cancel`).
- Order expiry uses `now - CreatedAt >= SIM_ORDER_TTL`. `PENDING_CANCEL` orders don't expire (not in the transition table); their cancel resolves first.
- Expected races (order already final / transition no longer allowed) are silent; anything else is logged as a warning.

## Dependencies
- `github.com/jackc/pgx/v5` is in `go.mod` (marked indirect, so `go.mod` says Go 1.24) for the trade app that will live alongside. brokersim itself stays in-memory and doesn't import it. Don't run `go mod tidy` until something imports pgx, or it will be dropped.
- Local Postgres databases `brokersim` and `brokersim_test` exist (empty) for that work. No Docker.

## Dev API
- `/dev/*` needs no API key but only answers loopback clients (127.0.0.1 / ::1), checked on `RemoteAddr`; others get `403 forbidden`. Same port as `/v1`.
- Every action calls the same store/domain methods as `/v1` and the engine, so the transition table still applies and the normal webhooks go out.
- Added beyond the spec: `POST /dev/orders/{id}/cancel` (same as `DELETE /v1/orders/{id}`) and the `auto_resolve_cancels` engine switch. With it off, a `PENDING_CANCEL` order waits for a manual `cancel/confirm` or `cancel/reject`, so scenarios like "partial fill during a pending cancel" can be driven by hand.
- `fill` with no body or no `qty` fills all remaining in one fill.
- Default reasons: reject `upstream_rejected`, expire `day_order_expired`, cancel reject `cancel_rejected`. All take an optional `{"reason": "..."}`.
- Dev actions return `200` with the order; errors include the current order next to the error when there is one.
- `PUT /dev/engine` is a partial update (`auto_fill`, `auto_resolve_cancels`, `market_open`); `GET /dev/engine` reads them.
- Redeliver works only on `delivered` / `failed` events (pending → `409`), resends the same id and body, and gets a fresh set of retries.
- Reset clears quotes, orders and the delivery log; scheduled retries from before the reset are dropped. Config, engine switches and RNG state stay.
- Not built: chaos endpoints, the HTML panel.

## Webhook replay protection (nonce)
- **Every delivery attempt** carries a fresh random `X-Sim-Nonce` (16 bytes, hex).
- **The signature covers it:** `hex(HMAC-SHA512(secret, "<ts>.<nonce>.<body>"))`, so neither the timestamp nor the nonce can be changed without breaking the signature.
- **Receivers** can refuse a nonce they have already seen (a replay). Our own retries always use a new nonce, so they are never mistaken for replays.
- **Interface change:** `Sign` and `Verify` take the nonce, and `Verify` refuses a missing one (`ErrBadNonce`).

## Provider console moved to the root web app
- **Removed:** brokersim no longer serves an HTML page at `/dev`.
- **Replacement:** the provider console is a page of the shared web app at the repo root (`web/`, route `/#/provider`). It's built with the same shadcn components and Sonner toasts, under its own Brokersim header.
- **How it connects:** the web app reaches the JSON `/dev` API through its Vite proxy (`/sim` → `:8090`). The proxied requests come from localhost, so the `/dev` loopback-only rule still holds.
- **Added:** `GET /dev/prices` (market prices without the API key) for that page.

## Running in a container
- `SIM_DEV_TRUSTED_CIDRS` (comma-separated CIDRs): networks besides loopback that may call `/dev`. Empty by default, so `/dev` stays loopback-only. Docker Compose trusts only its own network, which the web container's proxy uses.
- `SIM_AUTO_RESOLVE_CANCELS` (default true): the engine confirms cancels by itself. Off is manual mode, with `SIM_AUTO_FILL=false`.
- `SIM_SEED=0` picks a random seed at startup. The default stays 42, so tests and local runs are reproducible.
- `brokersim healthcheck` exits 0 when `/dev/engine` answers. The image is distroless, with no curl for a healthcheck.
- Logs are JSON with `service=brokersim`, ready for the log pipeline.

## Signing with a fresh timestamp per attempt
Each delivery attempt is signed with the current time and a new nonce. The receiver's timestamp window (2 minutes in tradeapp) therefore applies to each attempt, not to when the event happened, so retries hours later still verify. The dev receiver (`cmd/receiver`) uses the same 2 minutes.
