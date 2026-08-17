#!/usr/bin/env bash
# Background load: run `haproxy -dr -c` on a 3000-route config in a tight loop
# until /tmp/rc-stop appears.
set -uo pipefail
dir="$1"
cd "$dir"
n=0
while [[ ! -f /tmp/rc-stop ]]; do
  haproxy -dr -c -f haproxy.cfg > /dev/null 2>&1
  n=$((n+1))
done
echo "haproxy -c loop iterations: $n"
