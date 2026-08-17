#!/bin/bash
# Question D, second half: after 'add server' WITHOUT init-state, how long until the
# server takes traffic?  Measured inside the container (no docker-exec overhead).
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-d2.cfg" "$W/d2.cfg"
cp "$SPIKE_DIR/d4in.sh" "$W/d4in.sh"; chmod +x "$W/d4in.sh"
VER="${1:-3.0}"
start hapD2 "$VER" d2.cfg || exit 1
echo "############ HAProxy $VER ############"
cx "haproxy -v | head -1"

hr "baseline: cost of one curl round trip against an always-up file-defined backend"
cx "for i in 1 2 3; do curl -s -o /dev/null -w '  %{http_code} in %{time_total}s\n' -H 'x-be: alwaysup' http://127.0.0.1:8080/; done"

run() { echo; echo "--- $1"; shift; cx "/cfg/d4in.sh $*"; }

run "p1: check inter 300ms rise 1, NO init-state, then enable health + enable server" \
    p1 enable "check inter 300ms rise 1 fall 1"
run "p2: same + 'set server health up' right after the enable pair" \
    p2 enable_health "check inter 300ms rise 1 fall 1"
run "p3: 'set server health up' ONLY (never 'enable server')" \
    p3 health_only "check inter 300ms rise 1 fall 1"
run "p4: check inter 5s rise 1 (slow checks) — does it wait for the first check?" \
    p4 enable "check inter 5s rise 1 fall 1"
run "p5: check inter 5s rise 2 — two checks needed" \
    p5 enable "check inter 5s rise 2 fall 1"
run "p6: NO 'check' at all, just enable server" \
    p6 enable "no-such"
run "p7: NO 'check' at all (valid), enable health + enable server" \
    p7 enable ""
run "p8: init-state up (3.1+), enable health + enable server" \
    p8 enable "check inter 5s rise 2 fall 1 init-state up"
run "p9: added and NOTHING else (no enable) — proves the default admin state" \
    p9 none "check inter 300ms rise 1 fall 1"
run "p10: 'enable server' ONLY (no 'enable health')" \
    p10 server_only "check inter 300ms rise 1 fall 1"
run "p11: 'enable health' ONLY (no 'enable server')" \
    p11 health_first "check inter 300ms rise 1 fall 1"

stop
