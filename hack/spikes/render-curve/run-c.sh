#!/usr/bin/env bash
# Run C: per-step isolated benchmark processes with peak RSS, plus haproxy -c
# timing on every reconstructed artifact tree.
set -uo pipefail
SPIKE="${SPIKE:-$(cd "$(dirname "$0")" && pwd)}"
cd "$SPIKE/repo"

for kind in httproute ingress; do
  for n in 300 1000 3000; do
    test="benchmark-${kind}-${n}"
    echo "########## $test ##########"
    for r in 1 2 3; do
      python3 "$SPIKE/runmax.py" ./bin/haptic benchmark \
        --file "$SPIKE/configs/${kind}.yaml" \
        --test "$test" \
        --iterations 3 \
        --schema-dir "$SPIKE/repo/tests/schemas" \
        > "$SPIKE/raw/runC-${kind}-${n}-r${r}.txt" \
        2> "$SPIKE/raw/runC-${kind}-${n}-r${r}.rss"
      grep __RUNMAX__ "$SPIKE/raw/runC-${kind}-${n}-r${r}.rss"
      tail -1 "$SPIKE/raw/runC-${kind}-${n}-r${r}.txt"
    done
    bash "$SPIKE/hc.sh" "/tmp/rc/${kind}-${n}" 5 | tee "$SPIKE/raw/hc-${kind}-${n}.txt"
  done
done
