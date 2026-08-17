#!/usr/bin/env bash
# The plan's probe point: ~1500 routes. Render, artifact sizes, haproxy -c and
# the full validate cost, so the admission estimate at 1500 is measured, not
# interpolated.
set -uo pipefail
SPIKE="${SPIKE:-$(cd "$(dirname "$0")" && pwd)}"
cd "$SPIKE/repo"
SCHEMAS="$SPIKE/repo/tests/schemas"
n=1500

for kind in httproute ingress; do
  cfg="$SPIKE/configs/${kind}-${n}.yaml"
  if [[ ! -f "$cfg" ]]; then
    flag="--${kind}-only"
    GEN_ONLY=1 KEEP_CONFIG="$cfg" ./scripts/bench-spike.sh "$flag" --steps "$n" > /dev/null 2>&1
  fi
  test="benchmark-${kind}-${n}"
  echo "########## ${kind}-${n} ##########"

  for r in 1 2 3; do
    python3 "$SPIKE/runmax.py" ./bin/haptic benchmark --file "$cfg" --iterations 3 \
      --schema-dir "$SCHEMAS" > "$SPIKE/raw/runD-${kind}-${n}-r${r}.txt" \
      2> "$SPIKE/raw/runD-${kind}-${n}-r${r}.rss"
    grep __RUNMAX__ "$SPIKE/raw/runD-${kind}-${n}-r${r}.rss"
    grep "^TOTAL" "$SPIKE/raw/runD-${kind}-${n}-r${r}.txt"
  done

  ./bin/haptic validate -f "$cfg" --test "$test" --schema-dir "$SCHEMAS" --dump-rendered \
    > "$SPIKE/raw/dump-${kind}-${n}.txt" 2>&1
  rm -rf "/tmp/rc/${kind}-${n}"
  python3 "$SPIKE/extract.py" "$SPIKE/raw/dump-${kind}-${n}.txt" "/tmp/rc/${kind}-${n}" \
    > "$SPIKE/raw/sizes-${kind}-${n}.json"
  bash "$SPIKE/fixcrt.sh" > /dev/null
  bash "$SPIKE/hc.sh" "/tmp/rc/${kind}-${n}" 5 | tee "$SPIKE/raw/hc-${kind}-${n}.txt"

  aug="$SPIKE/configs/${kind}-${n}-hv.yaml"
  yq "(select(.kind == \"HAProxyTemplateConfig\") | .spec.validationTests.\"${test}\".assertions) += [{\"type\":\"haproxy_valid\"}]" \
    "$cfg" > "$aug"
  for r in 1 2 3; do
    ./bin/haptic validate -f "$cfg" --test "$test" --schema-dir "$SCHEMAS" \
      > "$SPIKE/raw/val-${kind}-${n}-plain-r${r}.txt" 2>&1
    ./bin/haptic validate -f "$aug" --test "$test" --schema-dir "$SCHEMAS" \
      > "$SPIKE/raw/val-${kind}-${n}-hv-r${r}.txt" 2>&1
  done
  echo -n "  plain : "; grep -h "^Tests:" "$SPIKE/raw/val-${kind}-${n}-plain-r"*.txt | tr '\n' ' '; echo
  echo -n "  hvalid: "; grep -h "^Tests:" "$SPIKE/raw/val-${kind}-${n}-hv-r"*.txt | tr '\n' ' '; echo
done
