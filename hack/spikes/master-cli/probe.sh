#!/bin/bash
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-a.cfg" "$W/a.cfg"
start hapP "${1:-3.4}" a.cfg || exit 1
mc "@1 help" > "$OUT/help-worker-${1:-3.4}.txt"
echo "--- experimental mentions:"; grep -in "experimental" "$OUT/help-worker-${1:-3.4}.txt"
echo "--- add/del/publish/wait lines:"; grep -E "^  (add|del|publish|unpublish|wait|experimental|expert|set severity)" "$OUT/help-worker-${1:-3.4}.txt"
echo "--- echo on worker:"; mc "@1 echo HELLO"
echo "--- experimental-mode state query:"; mc "@1 experimental-mode"
echo "--- expert-mode state query:"; mc "@1 expert-mode"
stop
