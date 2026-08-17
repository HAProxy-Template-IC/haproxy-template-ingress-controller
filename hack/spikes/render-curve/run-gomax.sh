#!/usr/bin/env bash
# GOMAXPROCS sweep at 3000 routes: separates "fewer CPUs" from "less parallel GC".
set -uo pipefail
SPIKE="${SPIKE:-$(cd "$(dirname "$0")" && pwd)}"
cd "$SPIKE/repo"
SCHEMAS="$SPIKE/repo/tests/schemas"
for kind in httproute ingress; do
  echo "########## $kind-3000 ##########"
  for g in 1 2 4 8 16; do
    for r in 1 2 3; do
      GOMAXPROCS=$g python3 "$SPIKE/runmax.py" ./bin/haptic benchmark \
        --file "$SPIKE/configs/${kind}-3000.yaml" --iterations 3 --schema-dir "$SCHEMAS" \
        > "$SPIKE/raw/gomax-${kind}-${g}-r${r}.txt" 2> "$SPIKE/raw/gomax-${kind}-${g}-r${r}.rss"
    done
    echo -n "GOMAXPROCS=$g  "
    grep -h "^TOTAL" "$SPIKE/raw/gomax-${kind}-${g}-r"*.txt | tr -s ' ' | tr '\n' ' '
    echo
    grep -h __RUNMAX__ "$SPIKE/raw/gomax-${kind}-${g}-r"*.rss | sed 's/.*maxrss_kb=/  maxrss_kb=/' | tr '\n' ' '
    echo
  done
done
uptime
