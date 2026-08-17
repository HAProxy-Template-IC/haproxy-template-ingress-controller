#!/usr/bin/env bash
set -euo pipefail
SPIKE="${SPIKE:-$(cd "$(dirname "$0")" && pwd)}"
cd "$SPIKE/repo"

echo "=== generating httproute config (300,1000,3000) ==="
time GEN_ONLY=1 KEEP_CONFIG="$SPIKE/configs/httproute.yaml" \
  ./scripts/bench-spike.sh --httproute-only --steps 300,1000,3000 > "$SPIKE/raw/gen-httproute.log" 2>&1

echo "=== generating ingress config (300,1000,3000) ==="
time GEN_ONLY=1 KEEP_CONFIG="$SPIKE/configs/ingress.yaml" \
  ./scripts/bench-spike.sh --ingress-only --steps 300,1000,3000 > "$SPIKE/raw/gen-ingress.log" 2>&1

ls -la "$SPIKE/configs/"
