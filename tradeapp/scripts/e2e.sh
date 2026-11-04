#!/usr/bin/env bash
# End-to-end test: the real brokersim (auto-fill on, signed webhooks) and the
# trade API, each built fresh, on their own ports and database. Places buys,
# cancels one mid-flight, sells, waits until every order is final, runs
# reconciliation, and fails on anything that doesn't add up.
#
#   scripts/e2e.sh                   # webhooks delivered normally
#   DROP_WEBHOOKS=1 scripts/e2e.sh   # every webhook lost: the sweeper must recover
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"   # tradeapp/
SIM_DIR="$ROOT/../brokersim"
WORK="$(mktemp -d)"
DB=tradeapp_e2e
API_PORT=18091
SIM_PORT=18090
API="http://localhost:$API_PORT"
SIM="http://localhost:$SIM_PORT"

fail() { echo "FAIL: $*" >&2; echo "--- api log tail:" >&2; tail -20 "$WORK/api.log" >&2 || true; exit 1; }
json() { jq -r "$1"; }

echo "== build"
(cd "$ROOT" && go build -o "$WORK/tradeapi" ./cmd/api)
(cd "$SIM_DIR" && go build -o "$WORK/brokersim" ./cmd/brokersim)

echo "== fresh database $DB"
dropdb -h localhost --if-exists "$DB"
createdb -h localhost "$DB"

WEBHOOK_URL="$API/webhooks/provider"
if [[ "${DROP_WEBHOOKS:-0}" == "1" ]]; then
  WEBHOOK_URL="http://localhost:9/nowhere"   # nothing listens: every delivery fails
  echo "== webhooks are being dropped"
fi

# brokersim's ids come from a seeded RNG: a fresh seed every run.
SIM_ADDR=":$SIM_PORT" SIM_WEBHOOK_URL="$WEBHOOK_URL" SIM_SEED="$RANDOM$RANDOM" SIM_FILL_PROB=0.5 \
  "$WORK/brokersim" > "$WORK/brokersim.log" 2>&1 &
SIM_PID=$!
API_ADDR=":$API_PORT" PROVIDER_URL="$SIM" DATABASE_URL="postgres://localhost:5432/$DB?sslmode=disable" \
  SWEEP_INTERVAL=1s STUCK_AFTER=2s RECON_INTERVAL=1h \
  "$WORK/tradeapi" > "$WORK/api.log" 2>&1 &
API_PID=$!
trap 'kill $API_PID $SIM_PID 2>/dev/null || true; rm -rf "$WORK"' EXIT

for _ in $(seq 100); do
  curl -sf "$API/healthz" >/dev/null && curl -sf -H 'X-Api-Key: sim_test_key' "$SIM/v1/prices" >/dev/null && break
  sleep 0.1
done
curl -sf "$API/healthz" >/dev/null || fail "api did not start"

place() { # symbol side qty -> order id
  local quote order
  quote=$(curl -sf -X POST "$API/api/quotes" -d "{\"symbol\":\"$1\",\"side\":\"$2\",\"qty\":$3}" | json .id)
  order=$(curl -sf -X POST "$API/api/orders" -H "Idempotency-Key: e2e-$RANDOM-$RANDOM" -d "{\"quote_id\":\"$quote\"}" | json .id)
  [[ "$order" == order-* ]] || fail "placing $1 $2 $3 returned $order"
  echo "$order"
}

open_orders() { curl -sf "$API/api/orders?status=open" | json '.orders | length'; }

wait_until_final() {
  for _ in $(seq 600); do
    [[ "$(open_orders)" == "0" ]] && return 0
    sleep 0.1
  done
  fail "orders still open after 60s: $(curl -sf "$API/api/orders?status=open")"
}

echo "== scenario"
curl -sf -X POST "$API/api/account/fund" -H 'Idempotency-Key: e2e-fund' -d '{"amount":10000000}' >/dev/null
for sym in AAPL MSFT TSLA AAPL MTNN; do place "$sym" BUY 20 >/dev/null; done
CANCEL_ID=$(place AAPL BUY 50)
sleep 1
code=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$API/api/orders/$CANCEL_ID")
[[ "$code" == "202" || "$code" == "409" ]] || fail "cancel returned $code"   # 409: it filled first
wait_until_final
SELL_ID=$(place AAPL SELL 5)
wait_until_final
sleep 3   # let everything settle past STUCK_AFTER so recon judges every order

echo "== results"
curl -sf "$API/api/orders?status=all" | json '.orders[] | "  \(.side) \(.symbol) \(.status) \(.filled_qty)/\(.qty) hold_left=\(.hold_remaining)"'

echo "== assertions"
[[ "$(curl -sf "$API/api/orders/$SELL_ID" | json .status)" == "FILLED" ]] || fail "sell not filled"
[[ "$(curl -sf "$API/api/admin/invariants" | json .ok)" == "true" ]] || fail "invariants: $(curl -sf "$API/api/admin/invariants")"
[[ "$(curl -sf "$API/api/breaks" | json '.breaks | length')" == "0" ]] || fail "breaks: $(curl -sf "$API/api/breaks")"
[[ "$(curl -sf "$API/api/admin/jobs?status=dead" | json '.jobs | length')" == "0" ]] || fail "dead jobs: $(curl -sf "$API/api/admin/jobs")"
[[ "$(curl -sf "$API/api/account" | json .cash.held)" == "0" ]] || fail "cash still held"

recon=$(curl -sf -X POST "$API/api/recon/run")
[[ "$(echo "$recon" | json .ok)" == "true" ]] || fail "reconciliation: $recon"
checked=$(echo "$recon" | json .orders_checked)
[[ "$checked" -ge 7 ]] || fail "reconciliation checked only $checked orders: $recon"

[[ "$(curl -sf "$API/api/orders/$CANCEL_ID/compare" | json .agree)" == "true" ]] || fail "cancelled order disagrees with the broker"
if grep -q "level=ERROR" "$WORK/api.log"; then fail "errors in the api log"; fi

delivered=$(curl -sf "$SIM/dev/deliveries" | jq '[.. | objects | select(has("state")) | .state] | group_by(.) | map({(.[0]): length}) | add')
echo "  webhooks: $delivered"
echo "  reconciliation: checked $checked orders, ok"
echo "PASS"
