# vfs

A trading lab: **brokersim**, a fake stock broker (the provider), and **tradeapp**, a trading app built on top of it, with a double-entry ledger, an outbox with retries and a dead-letter queue, signed webhooks, reconciliation and a small web console.

## Run it

Needs Go, Node.js, and Postgres running on `localhost:5432` (no password).

```bash
make up      # builds and starts everything; Ctrl-C stops it all
```

| What | Where |
| --- | --- |
| Trade console | http://localhost:5173 |
| Provider console (brokersim) | http://localhost:8090/dev |
| Trade API | http://localhost:8091 |

**The broker is manual by default.** Place an order in the trade console, then open the provider console and press *Fill 1*, *Fill all*, *Reject*, *Expire*, or *Confirm cancel* / *Reject cancel*, and watch the trade console update live. `AUTO=1 make up` lets brokersim's engine fill orders and resolve cancels by itself.

`make up` starts from an empty `tradeapp` database every time. brokersim keeps its state in memory and forgets it when it stops, so old orders would only show up as reconciliation mismatches. Use `KEEP_DATA=1 make up` to keep the data anyway.

## Other targets

```bash
make          # list targets
make test     # all Go tests (race detector) and the web build
make e2e      # end to end against the real brokersim
make check    # test + e2e
make down     # stop a `make up` running elsewhere
```

Design notes are in `brokersim/DECISIONS.md` and `tradeapp/DECISIONS.md`.
