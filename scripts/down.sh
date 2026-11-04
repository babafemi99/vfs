#!/usr/bin/env bash
# Stop services started by `make up` (for when it was backgrounded or its
# terminal was closed).
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PIDS="$ROOT/.run/pids"
if [[ ! -f "$PIDS" ]]; then
  echo "nothing running (no $PIDS)"
  exit 0
fi
while read -r pid; do kill "$pid" 2>/dev/null && echo "stopped $pid"; done < "$PIDS"
rm -f "$PIDS"
