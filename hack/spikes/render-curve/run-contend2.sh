#!/usr/bin/env bash
# Contention study, round-robin over conditions so machine drift hits every
# condition equally. Each round: one warm-up render (discarded) then one
# 3-iteration benchmark process per condition.
#   C0  isolated, whole machine
#   C1  + background `haproxy -dr -c` loop on the same 3000-route config
#   C2  two concurrent benchmark processes
#   C3  isolated, pinned to 2 CPUs (a realistic controller pod budget)
#   C4  pinned to 2 CPUs + `haproxy -dr -c` loop pinned to the same 2 CPUs
set -uo pipefail
SPIKE="${SPIKE:-$(cd "$(dirname "$0")" && pwd)}"
cd "$SPIKE/repo"
KIND="${KIND:-httproute}"
ROUNDS="${ROUNDS:-5}"
CFG="$SPIKE/configs/${KIND}-3000.yaml"
HCDIR="/tmp/rc/${KIND}-3000"
SCHEMAS="$SPIKE/repo/tests/schemas"

run1() { # $1 outfile, rest: optional taskset prefix
  local out="$1"; shift
  "$@" ./bin/haptic benchmark --file "$CFG" --iterations 3 --schema-dir "$SCHEMAS" > "$out" 2>&1
}

rm -f /tmp/rc-stop
# Warm the page cache for binary + config once.
run1 /dev/null

for round in $(seq 1 "$ROUNDS"); do
  echo "===== round $round ====="

  run1 "$SPIKE/raw/c2-${KIND}-C0-r${round}.txt"
  echo -n "C0 "; grep "^TOTAL" "$SPIKE/raw/c2-${KIND}-C0-r${round}.txt"

  bash "$SPIKE/hcloop.sh" "$HCDIR" > /dev/null 2>&1 &
  LOOP=$!
  run1 "$SPIKE/raw/c2-${KIND}-C1-r${round}.txt"
  touch /tmp/rc-stop; wait $LOOP; rm -f /tmp/rc-stop
  echo -n "C1 "; grep "^TOTAL" "$SPIKE/raw/c2-${KIND}-C1-r${round}.txt"

  run1 "$SPIKE/raw/c2-${KIND}-C2a-r${round}.txt" &
  P1=$!
  run1 "$SPIKE/raw/c2-${KIND}-C2b-r${round}.txt" &
  P2=$!
  wait $P1 $P2
  echo -n "C2a "; grep "^TOTAL" "$SPIKE/raw/c2-${KIND}-C2a-r${round}.txt"
  echo -n "C2b "; grep "^TOTAL" "$SPIKE/raw/c2-${KIND}-C2b-r${round}.txt"

  run1 "$SPIKE/raw/c2-${KIND}-C3-r${round}.txt" taskset -c 0,1
  echo -n "C3 "; grep "^TOTAL" "$SPIKE/raw/c2-${KIND}-C3-r${round}.txt"

  taskset -c 0,1 bash "$SPIKE/hcloop.sh" "$HCDIR" > /dev/null 2>&1 &
  LOOP=$!
  run1 "$SPIKE/raw/c2-${KIND}-C4-r${round}.txt" taskset -c 0,1
  touch /tmp/rc-stop; wait $LOOP; rm -f /tmp/rc-stop
  echo -n "C4 "; grep "^TOTAL" "$SPIKE/raw/c2-${KIND}-C4-r${round}.txt"
done
