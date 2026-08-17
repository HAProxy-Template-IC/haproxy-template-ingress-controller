#!/bin/bash
# 'wait' returned "Interrupted." in ~40ms for every delay. Hypothesis: the client
# half-closes (socat reads the command from a file => EOF) and HAProxy aborts the
# wait. Test: keep the write side of the connection open.
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-e.cfg" "$W/e.cfg"
cp "$SPIKE_DIR/slowup.sh" "$SPIKE_DIR/slowresp.sh" "$W/"; chmod +x "$W/slowup.sh" "$W/slowresp.sh"
VER="${1:-3.4}"
start hapE3 "$VER" e.cfg || exit 1
docker exec -d hapE3 /cfg/slowup.sh
sleep 0.5
echo "############ HAProxy $VER ############"

hr "V1. 'wait 3s' with the write side CLOSED right after the command (what every 'printf | socat' does)"
cx "s=\$(date +%s%3N); printf '@1 wait 3s\n' | socat -t20 stdio unix-connect:$MSOCK; echo \"  elapsed \$((\$(date +%s%3N)-s))ms\""

hr "V2. 'wait 3s' with the write side kept OPEN for 10s"
cx "s=\$(date +%s%3N); ( printf '@1 wait 3s\n'; sleep 10 ) | socat -t20 stdio unix-connect:$MSOCK; echo \"  elapsed \$((\$(date +%s%3N)-s))ms\""

hr "V3. same, '@@1' session, write side kept open"
cx "s=\$(date +%s%3N); ( printf '@@1\nwait 3s\n'; sleep 10 ) | socat -t20 stdio unix-connect:$MSOCK; echo \"  elapsed \$((\$(date +%s%3N)-s))ms\""

hr "V4. srv-removable with a request in flight (5s upstream), write side kept OPEN"
cx "echo '@1 add server e5/s1 127.0.0.1:9100 check inter 300ms rise 1 fall 1' | socat -t5 stdio unix-connect:$MSOCK
echo '@1 enable health e5/s1' | socat -t5 stdio unix-connect:$MSOCK
echo '@1 enable server e5/s1' | socat -t5 stdio unix-connect:$MSOCK
sleep 0.4
( printf 'GET / HTTP/1.1\r\nHost: h\r\nx-be: e5\r\nConnection: keep-alive\r\n\r\n'; sleep 30 ) | socat -t30 - TCP:127.0.0.1:8080 >/dev/null 2>&1 &
sleep 0.5
echo '@1 disable server e5/s1' | socat -t5 stdio unix-connect:$MSOCK
s=\$(date +%s%3N)
( printf '@1 wait 10s srv-removable e5/s1\n'; sleep 20 ) | socat -t25 stdio unix-connect:$MSOCK
echo \"  elapsed \$((\$(date +%s%3N)-s))ms  (upstream answers at ~5s)\"
echo '@1 del server e5/s1' | socat -t5 stdio unix-connect:$MSOCK"

hr "V5. same but the write side is CLOSED (the naive form)"
cx "echo '@1 add server e6/s1 127.0.0.1:9100 check inter 300ms rise 1 fall 1' | socat -t5 stdio unix-connect:$MSOCK
echo '@1 enable health e6/s1' | socat -t5 stdio unix-connect:$MSOCK
echo '@1 enable server e6/s1' | socat -t5 stdio unix-connect:$MSOCK
sleep 0.4
( printf 'GET / HTTP/1.1\r\nHost: h\r\nx-be: e6\r\nConnection: keep-alive\r\n\r\n'; sleep 30 ) | socat -t30 - TCP:127.0.0.1:8080 >/dev/null 2>&1 &
sleep 0.5
echo '@1 disable server e6/s1' | socat -t5 stdio unix-connect:$MSOCK
s=\$(date +%s%3N)
printf '@1 wait 10s srv-removable e6/s1\n' | socat -t25 stdio unix-connect:$MSOCK
echo \"  elapsed \$((\$(date +%s%3N)-s))ms\""

hr "V6. 'Wait delay expired.' — can it be produced?  (server never becomes removable, conn kept open)"
cx "echo '@1 add server e4/s1 127.0.0.1:9100 check inter 300ms rise 1 fall 1' | socat -t5 stdio unix-connect:$MSOCK
echo '@1 enable health e4/s1' | socat -t5 stdio unix-connect:$MSOCK
echo '@1 enable server e4/s1' | socat -t5 stdio unix-connect:$MSOCK
sleep 0.4
( printf 'GET / HTTP/1.1\r\nHost: h\r\nx-be: e4\r\nConnection: keep-alive\r\n\r\n'; sleep 60 ) | socat -t60 - TCP:127.0.0.1:8080 >/dev/null 2>&1 &
( printf 'GET /a HTTP/1.1\r\nHost: h\r\nx-be: e4\r\nConnection: keep-alive\r\n\r\n'; sleep 60 ) | socat -t60 - TCP:127.0.0.1:8080 >/dev/null 2>&1 &
sleep 0.5
echo '@1 disable server e4/s1' | socat -t5 stdio unix-connect:$MSOCK
s=\$(date +%s%3N)
( printf '@1 wait 2s srv-removable e4/s1\n'; sleep 8 ) | socat -t12 stdio unix-connect:$MSOCK
echo \"  elapsed \$((\$(date +%s%3N)-s))ms\""

stop
