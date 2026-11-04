#!/usr/bin/env bash
# Sends forged and replayed webhooks to the trade app, to show its defences.
#
#   scripts/demo-webhook-attack.sh
#
# 1. a correctly signed webhook               → 200 (stored)
# 2. the exact same request again (replay)    → 409 replay_detected
# 3. the body changed after signing           → 401 (signature)
# 4. a signature from 10 minutes ago          → 401 (timestamp)
# 5. the wrong secret                         → 401 (signature)
#
# Each one appears in the trade console's Webhooks terminal with its outcome.
set -euo pipefail

URL="${API_URL:-http://localhost:8091}/webhooks/provider"
SECRET="${WEBHOOK_SECRET:-whsec_test}"

sign() { # secret ts nonce body
  printf '%s.%s.%s' "$2" "$3" "$4" | openssl dgst -sha512 -hmac "$1" -hex | awk '{print $NF}'
}

send() { # label ts nonce body signature
  local code
  code=$(curl -s -o /tmp/vfs-attack.$$ -w '%{http_code}' -X POST "$URL" \
    -H 'Content-Type: application/json' \
    -H "X-Sim-Timestamp: $2" -H "X-Sim-Nonce: $3" -H "X-Sim-Signature: $5" \
    -H "X-Sim-Event-Id: evt_demo_$RUN" -d "$4")
  printf '  %-34s → %s %s\n' "$1" "$code" "$(cat /tmp/vfs-attack.$$)"
  rm -f /tmp/vfs-attack.$$
}

RUN=$(openssl rand -hex 4)
BODY="{\"id\":\"evt_demo_$RUN\",\"type\":\"order.accepted\",\"created_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"data\":{\"order\":{\"id\":\"ord_demo_$RUN\"}}}"
NOW=$(date +%s)
NONCE=$(openssl rand -hex 16)
SIG=$(sign "$SECRET" "$NOW" "$NONCE" "$BODY")

echo "Webhook attacks against $URL"
send "1. signed correctly"             "$NOW" "$NONCE" "$BODY" "$SIG"
send "2. same request again (replay)"  "$NOW" "$NONCE" "$BODY" "$SIG"

N2=$(openssl rand -hex 16)
SIG2=$(sign "$SECRET" "$NOW" "$N2" "$BODY")
send "3. body changed after signing"   "$NOW" "$N2" "${BODY/order.accepted/order.filled}" "$SIG2"

OLD=$((NOW - 600)); N3=$(openssl rand -hex 16)
send "4. signed 10 minutes ago"        "$OLD" "$N3" "$BODY" "$(sign "$SECRET" "$OLD" "$N3" "$BODY")"

N4=$(openssl rand -hex 16)
send "5. wrong secret"                 "$NOW" "$N4" "$BODY" "$(sign "whsec_attacker" "$NOW" "$N4" "$BODY")"
