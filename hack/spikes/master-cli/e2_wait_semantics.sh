#!/bin/bash
# Question E / A follow-up: what does 'wait' actually do?  Which strings can it
# return, and does it ever block for the full delay?
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-e.cfg" "$W/e.cfg"
cp "$SPIKE_DIR/slowup.sh" "$SPIKE_DIR/slowresp.sh" "$W/"; chmod +x "$W/slowup.sh" "$W/slowresp.sh"
VER="${1:-3.4}"
now() { date +%s%3N; }
start hapE2 "$VER" e.cfg || exit 1
docker exec -d hapE2 /cfg/slowup.sh
sleep 0.5
echo "############ HAProxy $VER ############"

t() { local T0=$(now); local r; r=$(MC_T=40 mc "@1 $1"); echo "  [$1] -> [$(printf '%s' "$r" | tr '\n' '|')]  ${*:2} $(( $(now) - T0 ))ms"; }

hr "W0. 'wait -h'"
mc "@1 wait -h"

hr "W1. bare 'wait <delay>' (no condition) — does it block?"
t "wait 500"
t "wait 2s"

hr "W2. srv-removable on a server that is UP (never in maintenance)"
mc "@1 add server e1/s1 127.0.0.1:9000 check inter 300ms rise 1 fall 1" >/dev/null
mc "@1 enable health e1/s1" >/dev/null; mc "@1 enable server e1/s1" >/dev/null
sleep 0.3
t "wait 5s srv-removable e1/s1"

hr "W3. srv-removable right after 'disable server' (no traffic at all)"
mc "@1 disable server e1/s1" >/dev/null
t "wait 5s srv-removable e1/s1"
mc "@1 del server e1/s1" >/dev/null

hr "W4. srv-removable with a request IN FLIGHT (5s upstream), server in maintenance"
mc "@1 add server e2/s1 127.0.0.1:9100 check inter 300ms rise 1 fall 1" >/dev/null
mc "@1 enable health e2/s1" >/dev/null; mc "@1 enable server e2/s1" >/dev/null
sleep 0.3
cx "( printf 'GET / HTTP/1.1\r\nHost: h\r\nx-be: e2\r\nConnection: keep-alive\r\n\r\n'; sleep 30 ) | socat -t30 - TCP:127.0.0.1:8080 >/dev/null 2>&1 &
sleep 0.5"
mc "@1 disable server e2/s1" >/dev/null
t "wait 10s srv-removable e2/s1" "(upstream answers at ~5s; a real wait would return Done at ~4.5s)"
t "wait 10s srv-removable e2/s1" "(second try, after the first returned)"
echo "  del server -> [$(mc "@1 del server e2/s1" | tr -d '\n')]"

hr "W5. be-removable strings (3.1+ only)"
mc "@1 wait 2s be-removable e3" | cat -A
mc "@1 add server e3/s1 127.0.0.1:9000 init-state up" >/dev/null 2>&1
t "wait 2s be-removable e3" "(file-defined backend, has a server, never published/unpublished)"
mc "@1 unpublish backend e3" | cat -A
t "wait 2s be-removable e3" "(after unpublish, still has a server)"
mc "@1 del server e3/s1" >/dev/null 2>&1
t "wait 2s be-removable e3" "(after the server is gone)"

hr "W6. does 'wait' EVER block? (bare wait with a big delay, timed)"
t "wait 3s"
t "wait 1s"

stop
