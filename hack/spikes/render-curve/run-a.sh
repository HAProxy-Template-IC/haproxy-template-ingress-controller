#!/usr/bin/env bash
# Run A: whole-suite benchmark, exactly what scripts/test-benchmark.sh runs after
# generating the config (same binary, same flags), repeated REPEATS times.
set -euo pipefail
SPIKE="${SPIKE:-$(cd "$(dirname "$0")" && pwd)}"
cd "$SPIKE/repo"
REPEATS="${REPEATS:-3}"

for kind in httproute ingress; do
  for r in $(seq 1 "$REPEATS"); do
    echo "=== $kind repeat $r ==="
    ./bin/haptic benchmark \
      --file "$SPIKE/configs/${kind}.yaml" \
      --iterations 3 \
      --schema-dir "$SPIKE/repo/tests/schemas" \
      > "$SPIKE/raw/runA-${kind}-r${r}.txt" 2>&1 || echo "FAILED"
    tail -3 "$SPIKE/raw/runA-${kind}-r${r}.txt"
  done
done
