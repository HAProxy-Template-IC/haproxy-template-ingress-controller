#!/usr/bin/env bash
# One config per (kind, step) so peak RSS is attributable to that step alone.
set -euo pipefail
SPIKE="${SPIKE:-$(cd "$(dirname "$0")" && pwd)}"
cd "$SPIKE/repo"
for n in 300 1000 3000; do
  GEN_ONLY=1 KEEP_CONFIG="$SPIKE/configs/httproute-${n}.yaml" \
    ./scripts/bench-spike.sh --httproute-only --steps "$n" > /dev/null 2>&1
  GEN_ONLY=1 KEEP_CONFIG="$SPIKE/configs/ingress-${n}.yaml" \
    ./scripts/bench-spike.sh --ingress-only --steps "$n" > /dev/null 2>&1
  echo "generated step $n"
done
ls -la "$SPIKE/configs/"
