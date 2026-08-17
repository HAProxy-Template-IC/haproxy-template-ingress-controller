#!/usr/bin/env bash
# Run D: per-step config, per-step process -> attributable peak RSS.
set -uo pipefail
SPIKE="${SPIKE:-$(cd "$(dirname "$0")" && pwd)}"
cd "$SPIKE/repo"

for kind in httproute ingress; do
  for n in 300 1000 3000; do
    echo "########## ${kind}-${n} ##########"
    for r in 1 2 3; do
      python3 "$SPIKE/runmax.py" ./bin/haptic benchmark \
        --file "$SPIKE/configs/${kind}-${n}.yaml" \
        --iterations 3 \
        --schema-dir "$SPIKE/repo/tests/schemas" \
        > "$SPIKE/raw/runD-${kind}-${n}-r${r}.txt" \
        2> "$SPIKE/raw/runD-${kind}-${n}-r${r}.rss"
      grep __RUNMAX__ "$SPIKE/raw/runD-${kind}-${n}-r${r}.rss"
      grep "^TOTAL" "$SPIKE/raw/runD-${kind}-${n}-r${r}.txt"
    done
  done
done
