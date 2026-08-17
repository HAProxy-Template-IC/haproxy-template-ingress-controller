#!/bin/bash
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-e2.cfg" "$W/e2.cfg"
start hapP3 3.0 e2.cfg || exit 1
echo "--- add server a/s1 (one shot)"; mc "@1 add server a/s1 127.0.0.1:9000 check inter 300ms rise 1 fall 1"
echo "--- enable health";  mc "@1 enable health a/s1"
echo "--- enable server";  mc "@1 enable server a/s1"
sleep 0.5
echo "--- curl:"; cx "curl -s -o /dev/null -w '%{http_code}\n' -H 'x-be: a' http://127.0.0.1:8080/"
echo "--- show servers conn a:"; mc "@1 show servers conn a"
echo "--- show servers state a:"; mc "@1 show servers state a"
stop
