#!/usr/bin/env bash
# Contention study at 3000 routes.
#   C0  isolated, whole machine
#   C1  + background `haproxy -dr -c` loop on the same 3000-route config
#   C2  two concurrent benchmark processes
#   C3  isolated, pinned to 2 CPUs (a realistic controller pod budget)
#   C4  pinned to 2 CPUs + `haproxy -dr -c` loop pinned to the same 2 CPUs
set -uo pipefail
SPIKE="${SPIKE:-$(cd "$(dirname "$0")" && pwd)}"
cd "$SPIKE/repo"
KIND="${KIND:-httproute}"
CFG="$SPIKE/configs/${KIND}-3000.yaml"
HCDIR="/tmp/rc/${KIND}-3000"
SCHEMAS="$SPIKE/repo/tests/schemas"

bench() { # $1 = tag, rest = optional prefix (taskset)
  local tag="$1"; shift
  for r in 1 2 3; do
    "$@" ./bin/haptic benchmark --file "$CFG" --iterations 3 --schema-dir "$SCHEMAS" \
      > "$SPIKE/raw/cont-${KIND}-${tag}-r${r}.txt" 2>&1
    grep "^TOTAL" "$SPIKE/raw/cont-${KIND}-${tag}-r${r}.txt"
  done
}

rm -f /tmp/rc-stop

echo "===== C0 isolated ====="
bench C0

echo "===== C1 + haproxy -c loop ====="
bash "$SPIKE/hcloop.sh" "$HCDIR" > "$SPIKE/raw/cont-${KIND}-C1-loop.txt" 2>&1 &
LOOP=$!
sleep 1
bench C1
touch /tmp/rc-stop; wait $LOOP; rm -f /tmp/rc-stop
cat "$SPIKE/raw/cont-${KIND}-C1-loop.txt"

echo "===== C2 two concurrent renders ====="
for r in 1 2 3; do
  ./bin/haptic benchmark --file "$CFG" --iterations 3 --schema-dir "$SCHEMAS" \
    > "$SPIKE/raw/cont-${KIND}-C2a-r${r}.txt" 2>&1 &
  P1=$!
  ./bin/haptic benchmark --file "$CFG" --iterations 3 --schema-dir "$SCHEMAS" \
    > "$SPIKE/raw/cont-${KIND}-C2b-r${r}.txt" 2>&1 &
  P2=$!
  wait $P1 $P2
  grep "^TOTAL" "$SPIKE/raw/cont-${KIND}-C2a-r${r}.txt"
  grep "^TOTAL" "$SPIKE/raw/cont-${KIND}-C2b-r${r}.txt"
done

echo "===== C3 isolated, 2 CPUs ====="
bench C3 taskset -c 0,1

echo "===== C4 2 CPUs + haproxy -c loop on the same 2 CPUs ====="
taskset -c 0,1 bash "$SPIKE/hcloop.sh" "$HCDIR" > "$SPIKE/raw/cont-${KIND}-C4-loop.txt" 2>&1 &
LOOP=$!
sleep 1
bench C4 taskset -c 0,1
touch /tmp/rc-stop; wait $LOOP; rm -f /tmp/rc-stop
cat "$SPIKE/raw/cont-${KIND}-C4-loop.txt"
