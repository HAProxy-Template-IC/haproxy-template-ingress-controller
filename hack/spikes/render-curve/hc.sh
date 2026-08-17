#!/usr/bin/env bash
# Time `haproxy -dr -c` on a reconstructed artifact tree, REPS times.
set -uo pipefail
dir="$1"; reps="${2:-5}"
cd "$dir"
haproxy -dr -c -f haproxy.cfg > /tmp/rc-hc.out 2>&1
echo "exit=$? (first run)"
tail -3 /tmp/rc-hc.out
for i in $(seq 1 "$reps"); do
  s=$(date +%s%N)
  haproxy -dr -c -f haproxy.cfg > /dev/null 2>&1
  rc=$?
  e=$(date +%s%N)
  echo "rep$i ms=$(( (e-s)/1000000 )) exit=$rc"
done
