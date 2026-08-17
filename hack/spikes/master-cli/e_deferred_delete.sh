#!/bin/bash
# Question E: with a client holding an idle keep-alive connection (http-reuse safe,
# default pool-purge-delay), does 'wait 2s srv-removable' succeed after
#   (a) 'disable server' alone, and (b) 'disable server' + 'shutdown sessions server'?
# Also the case that actually differs: a request still in flight to a slow upstream.
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-e.cfg" "$W/e.cfg"
cp "$SPIKE_DIR/ein.sh" "$W/ein.sh"; chmod +x "$W/ein.sh"
cp "$SPIKE_DIR/slowup.sh" "$SPIKE_DIR/slowresp.sh" "$W/"; chmod +x "$W/slowup.sh" "$W/slowresp.sh"
VER="${1:-3.4}"
start hapE "$VER" e.cfg || exit 1
docker exec -d hapE /cfg/slowup.sh
sleep 0.5
echo "############ HAProxy $VER  (http-reuse safe, default pool-purge-delay) ############"
cx "haproxy -v | head -1"
echo "--- 'show servers conn' header (purge_delay column = pool-purge-delay in ms):"
mc "@1 show servers conn nope"

hr "E1. idle keep-alive CLIENT connection held open;  'disable server' ONLY"
cx "/cfg/ein.sh e1 disable idle"

hr "E2. idle keep-alive CLIENT connection held open;  'disable server' + 'shutdown sessions server'"
cx "/cfg/ein.sh e2 disable_shutsess idle"

hr "E3. idle keep-alive CLIENT connection held open;  NO teardown at all (control)"
cx "/cfg/ein.sh e3 nothing idle"

hr "E4. NO client connection, only a pooled idle SERVER connection;  'disable server' ONLY"
cx "/cfg/ein.sh e4 disable none"

hr "E5. request IN FLIGHT to a 5s upstream;  'disable server' ONLY"
cx "/cfg/ein.sh e5 disable inflight 9100"

hr "E6. request IN FLIGHT to a 5s upstream;  'disable server' + 'shutdown sessions server'"
cx "/cfg/ein.sh e6 disable_shutsess inflight 9100"

hr "E7. raw 'show sess' while an idle keep-alive client is attached"
cx "( printf 'GET / HTTP/1.1\r\nHost: h\r\nx-be: nope\r\nConnection: keep-alive\r\n\r\n'; sleep 5 ) | socat -t10 - TCP:127.0.0.1:8080 >/dev/null 2>&1 &
sleep 1
echo '@1 show sess' | socat -t5 stdio unix-connect:$MSOCK"

stop
