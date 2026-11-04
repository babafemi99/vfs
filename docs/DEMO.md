# Demo: a 7-minute tour

A script for showing tradeapp to someone. Each step lists what to **do**, what to **point at**, and the one sentence that explains why it matters. Every step was rehearsed against `make up`.

## Setup (before you start)

```bash
make up            # natively, or:
make docker-obs    # in containers with VictoriaLogs, VictoriaMetrics and alerts (web on :8088 instead of :5173)
```

Put two browser windows side by side:

- **Left:** the trade console, http://localhost:5173
- **Right:** the provider console, http://localhost:5173/#/provider (or **Provider ↗** in the trade console)

Keep a terminal open at the repo root. The broker is in manual mode: nothing fills unless you press a button on the right.

---

## 1. Place an order (1 min)

**Do:**
1. *Portfolio* → **Add cash** → $10,000.
2. *Trade* → BUY 5 AAPL → **Get quote** → **Confirm buy**.

**Point at:**
- The order goes to *Submitting*, then *Open* a moment later.
- On the right, the order appears with **v1**.
- In *Webhooks* (left), the `order.accepted` line is marked verified.

**Say:** "Placing the order doesn't call the broker. The order, the cash hold and a job to send it are one database transaction. A worker sends it, and the broker answers with a signed webhook. If the broker is down, the user still gets an instant answer and the job retries."

## 2. Partial fills (45 s)

**Do:** on the right, press **Fill 1**, then **Fill 1** again, then **Fill all**.

**Point at:**
- Each fill shows up live on the left: a toast, the activity feed, the order's progress.
- *Portfolio* shows the position with its average cost. The version on the right goes up with each event.

**Say:** "Every fill is a ledger entry: cash out of the hold, shares in. Fills are deduplicated by fill id, and an older snapshot can never rewind the order because each update carries the broker's version."

## 3. A fill racing a cancel (1 min)

**Do:**
1. Place another order: BUY 4 MSFT.
2. On its order page (left), press **Cancel order** → it shows *Cancelling*.
3. On the right, press **Fill 1** (the fill gets there before the cancel does), then **Confirm cancel**.

**Point at:** the order ends *Cancelled* with **1 of 4 filled**. The share is in the portfolio, and the hold for the other 3 is released.

**Say:** "A cancel is a request, not a fact. Until the broker confirms, fills can still land. The state machine lets the first final state win, and everything that really filled is booked."

## 4. Duplicate webhooks (30 s)

**Do:** on the right, under *Webhook deliveries*, press **Redeliver** on any delivered event.

**Point at:** in *Webhooks* (left) a new line marked **duplicate** appears with the same event id.

**Say:** "Brokers retry webhooks, so the same event arrives twice. The inbox is keyed by event id, so it's stored once and applied once."

## 5. Forged and replayed webhooks (1 min)

**Do:**
```bash
scripts/demo-webhook-attack.sh
```
```
1. signed correctly                → 200 {"received":true}
2. same request again (replay)     → 409 replay_detected
3. body changed after signing      → 401 webhook signature mismatch
4. signed 10 minutes ago           → 401 webhook timestamp missing or outside tolerance
5. wrong secret                    → 401 webhook signature mismatch
```

**Point at:**
- All five show up in *Webhooks* (left). Click one to open the inspector: headers, body, outcome.
- The first one names an order nobody has. It's stored and flagged as "event for an unknown order" in the logs, not an error.

**Say:** "The signature covers the timestamp, a nonce and the body, compared in constant time. The timestamp limits a replay to 2 minutes, and the nonce cache blocks the replay even inside those 2 minutes. A real retry from the broker uses a new nonce, so it still gets through."

## 6. Retries can't charge twice (1 min)

**Do (terminal):**
```bash
KEY=$(uuidgen)
curl -si -XPOST localhost:8091/api/account/fund -H "Idempotency-Key: $KEY" -d '{"amount":100}' | grep -iE "^HTTP|replayed"
curl -si -XPOST localhost:8091/api/account/fund -H "Idempotency-Key: $KEY" -d '{"amount":100}' | grep -iE "^HTTP|replayed"
curl -s  -XPOST localhost:8091/api/account/fund -H "Idempotency-Key: $KEY" -d '{"amount":999}'
```
**Point at:**
- The first call returns `200`.
- The second returns `200` with `Idempotent-Replayed: true`, the same body, and only $1 added in total.
- The third returns `422 idempotency_conflict`.

**Do (browser, optional):**
1. DevTools → Network → **Offline**, then confirm an order. After a few retries it says it couldn't confirm.
2. Go back online and **reload**. A banner offers **Resume**; press it and exactly one order is placed.

**Say:** "Each user action gets one key, kept in sessionStorage until the server gives a definite answer. Timeouts and 5xx are retried with the same key, so 'did my order go through?' is always safe to retry. The server stores 2xx and 4xx responses for replay, and releases the key on a 5xx because nothing was committed."

## 7. Reconciliation catches what slipped through (1 min)

**Do:**
1. Make sure an order is still open (place one if needed).
2. On the right, press **Reset broker** → confirm. The broker forgets every order.
3. Wait about 30 seconds. Reconciliation leaves orders alone while they might still be in flight.
4. *Operations* → *Reconciliation* → **Run now**.

**Point at:**
- A **missing_at_provider** mismatch for the order the broker forgot.
- *Books* still says the ledger is consistent, and *Compare with broker* on that order shows the two sides.

**Say:** "Webhooks, retries and sweeps make drift unlikely, not impossible. Reconciliation compares both systems on a schedule and records every difference as a break for a human, instead of silently 'fixing' money."

## 8. Following one request (30 s)

**Do:**
```bash
curl -s localhost:8091/metrics | grep -E '^(jobs_dead|provider_circuit_state|idempotency_requests_total|webhooks_received_total)'
```
In the `make up` output, find any order's request id (or send one with `-H "X-Request-Id: demo-1"`) and search for it.

**Point at:** one `request_id` on four lines: *order placed* and *http 201* from the API, then *order submitted* and *job done* from the worker, seconds later.

**With `make docker-obs` (stronger):**
1. Take the `X-Request-Id` response header from any order request in the browser's Network tab.
2. In the VictoriaLogs UI (http://localhost:9428/select/vmui), search `request_id:="<that id>"`. You get nginx's access line, the API's lines and the worker's lines for that one request.
3. Search `level:in("WARN","ERROR")`: step 5's attacks show up as `webhook refused`.
4. In the VictoriaMetrics UI (http://localhost:8428/vmui), query `jobs_dead`, `provider_circuit_state` or `sum by (outcome) (webhooks_received_total)`.
5. Open vmalert (http://localhost:8880). After step 5, `WebhookSignatureFailures` is pending or firing; after step 7, `ReconciliationMismatches` is.

**Say:** "The request id goes into the context and onto every job the request queues, so the worker's logs line up with the HTTP request that started them. Metrics are Prometheus format: alert on dead jobs, queue lag and an open circuit."

---

## Outages: what to say, and how to prove it

`make up` stops the whole stack if any one process dies, so take the broker down with tests instead:

- **The broker is down:**
  - **Proof:** `TestCircuitBreakerPausesProviderJobs` and `TestRetryBudget` in `tradeapp/internal/service`.
  - **What happens:** jobs retry with exponential backoff and full jitter. After 5 failures in a row the circuit opens and provider jobs pause (they're deferred, not burned). After a 15-minute retry budget a job moves to the dead-letter queue, where *Operations* can retry it one at a time or all at once (`TestJobStatsAndBulkRetry`).
- **Every webhook is lost:**
  - **Proof:** `make e2e-dropped` runs the real binaries with every webhook dropped.
  - **What happens:** the sweeper polls orders that went quiet, every order still reaches its final state, and reconciliation comes back clean.
- **A fill the broker reported but our side never booked:** reconciliation finds it (`TestReconFindsDroppedFills`).

## Likely questions

- **"Why not call the broker in the request?"** Because then the user waits on a third party, and a crash between "broker accepted" and "we saved it" loses money. The outbox makes the write and the intent to call one transaction.
- **"What if the worker crashes mid-job?"** Jobs are leased. When the lease expires another worker picks the job up (`FOR UPDATE SKIP LOCKED`), and the attempt count fences the old worker's late result.
- **"Why store a 4xx for replay?"** A rejection like *insufficient funds* is an answer. Re-running it after the user funds the account would silently turn an old click into a new order. A new attempt needs a new key.
- **"Why no Redis for the rate limiter?"** One instance. The token bucket is the same logic in Redis with a Lua script once there are several.
- **"How would this scale?"** The API is stateless apart from that in-memory rate limiter. Workers scale out because jobs are claimed with `SKIP LOCKED`. The heavy reads (activity, webhook log) are append-only, indexed by id, and streamed over SSE from a cursor.
