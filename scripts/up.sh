#!/usr/bin/env bash
# Start the whole lab in one terminal: brokersim (:8090), the trade API
# (:8091) and the web console (:5173). Ctrl-C stops all three.
#
#   KEEP_DATA=1   keep the tradeapp database (default: start fresh, because
#                 brokersim forgets everything when it stops)
#   AUTO=1        let brokersim's engine fill orders and resolve cancels by
#                 itself (default: manual, you drive it from the provider
#                 console at http://localhost:5173/#/provider)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RUN="$ROOT/.run"
BIN="$RUN/bin"
PIDS="$RUN/pids"
SIM_PORT=8090 API_PORT=8091 WEB_PORT=5173

say()  { printf '\033[1m%s\033[0m\n' "$*"; }
fail() { printf '\033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

# label every log line: [sim] ..., in a colour per service
prefix() { awk -v tag="$1" -v c="$2" '{ printf "\033[%sm%-6s\033[0m %s\n", c, tag, $0; fflush() }'; }

# ---- prerequisites --------------------------------------------------------
command -v go   >/dev/null || fail "Go is not installed"
command -v node >/dev/null || fail "Node.js is not installed"
command -v npm  >/dev/null || fail "npm is not installed"
pg_isready -h localhost -q 2>/dev/null || fail "Postgres is not running on localhost:5432 (try: brew services start postgresql@18)"

for port in $SIM_PORT $API_PORT $WEB_PORT; do
  if lsof -nP -iTCP:$port -sTCP:LISTEN >/dev/null 2>&1; then
    fail "port $port is already in use (make down, or stop whatever is on it)"
  fi
done

# ---- databases ------------------------------------------------------------
if [[ "${KEEP_DATA:-0}" == "1" ]]; then
  createdb -h localhost tradeapp 2>/dev/null && say "· created database tradeapp" || true
else
  dropdb -h localhost --if-exists tradeapp >/dev/null 2>&1
  createdb -h localhost tradeapp
  say "· fresh database tradeapp (KEEP_DATA=1 to keep it)"
fi
createdb -h localhost tradeapp_test 2>/dev/null || true

# ---- build ----------------------------------------------------------------
mkdir -p "$BIN"
say "· building brokersim and the API"
(cd "$ROOT/brokersim" && go build -o "$BIN/brokersim" ./cmd/brokersim)
(cd "$ROOT/tradeapp" && go build -o "$BIN/tradeapi" ./cmd/api)
if [[ ! -d "$ROOT/web/node_modules" ]]; then
  say "· installing web dependencies"
  (cd "$ROOT/web" && npm install --no-audit --no-fund --silent)
fi

# ---- start ----------------------------------------------------------------
: > "$PIDS"
stop() {
  local code=${1:-0}
  trap - INT TERM EXIT
  echo
  say "· stopping"
  if [[ -f "$PIDS" ]]; then
    while read -r pid; do kill "$pid" 2>/dev/null || true; done < "$PIDS"
    rm -f "$PIDS"
  fi
  wait 2>/dev/null || true
  exit "$code"
}
trap 'stop 0' INT TERM
trap 'stop $?' EXIT

# Manual by default: nothing fills or resolves until you press a button in
# the provider console, and orders stay working for a day instead of 10m.
if [[ "${AUTO:-0}" == "1" ]]; then
  SIM_AUTO_FILL=true SIM_ORDER_TTL=10m MODE="auto (the engine fills and cancels by itself)"
else
  SIM_AUTO_FILL=false SIM_ORDER_TTL=24h MODE="manual (drive fills and cancels from the provider console; AUTO=1 for automatic)"
fi

# brokersim's ids come from a seeded RNG: a fresh seed every start.
SIM_AUTO_FILL="$SIM_AUTO_FILL" SIM_ORDER_TTL="$SIM_ORDER_TTL" \
SIM_ADDR=":$SIM_PORT" SIM_SEED="$RANDOM$RANDOM" \
  SIM_WEBHOOK_URL="http://localhost:$API_PORT/webhooks/provider" \
  "$BIN/brokersim" > >(prefix "[sim]" "35") 2>&1 &
echo $! >> "$PIDS"

API_ADDR=":$API_PORT" PROVIDER_URL="http://localhost:$SIM_PORT" \
  DATABASE_URL="postgres://localhost:5432/tradeapp?sslmode=disable" \
  "$BIN/tradeapi" > >(prefix "[api]" "34") 2>&1 &
echo $! >> "$PIDS"

(cd "$ROOT/web" && exec node node_modules/vite/bin/vite.js --port "$WEB_PORT" --strictPort) \
  > >(prefix "[web]" "32") 2>&1 &
echo $! >> "$PIDS"

# ---- wait until healthy ---------------------------------------------------
for _ in $(seq 120); do
  if curl -sf "http://localhost:$API_PORT/healthz" >/dev/null \
    && curl -sf "http://localhost:$SIM_PORT/dev/engine" >/dev/null \
    && curl -sf "http://localhost:$WEB_PORT" >/dev/null; then
    # Cancels have no env setting: set the engine switch through the dev API.
    if [[ "${AUTO:-0}" != "1" ]]; then
      curl -sf -X PUT "http://localhost:$SIM_PORT/dev/engine" -d '{"auto_fill":false,"auto_resolve_cancels":false}' >/dev/null \
        || fail "could not switch brokersim to manual mode"
    fi
    echo
    say "✓ up"
    echo "  Broker mode       $MODE"
    echo "  Trade console     http://localhost:$WEB_PORT"
    echo "  Provider console  http://localhost:$WEB_PORT/#/provider"
    echo "  API               http://localhost:$API_PORT   (health: /healthz)"
    echo "  Ctrl-C to stop everything"
    echo
    # Keep running while all three are alive; if one exits, stop the rest.
    # (A polling loop rather than `wait -n`, which macOS's bash 3.2 lacks.)
    while true; do
      if [[ ! -f "$PIDS" ]]; then   # `make down` stopped us from outside
        say "· stopped by make down"
        exit 0
      fi
      while read -r pid; do
        kill -0 "$pid" 2>/dev/null || fail "a service stopped; see its log above"
      done < "$PIDS"
      sleep 1
    done
  fi
  sleep 0.25
done
fail "services did not become healthy in 30s; see the logs above"
