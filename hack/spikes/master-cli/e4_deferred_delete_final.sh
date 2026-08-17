#!/bin/bash
# Question E, final: 'wait srv-removable' with response timestamps, and with the
# three master-socket framings (the framing decides whether 'wait' waits at all).
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-e2.cfg" "$W/e2.cfg"
cp "$SPIKE_DIR/ein2.sh" "$W/ein2.sh"; chmod +x "$W/ein2.sh"
cp "$SPIKE_DIR/slowup.sh" "$SPIKE_DIR/slowresp.sh" "$W/"; chmod +x "$W/slowup.sh" "$W/slowresp.sh"
VER="${1:-3.4}"
start hapE4 "$VER" e2.cfg || exit 1
docker exec -d hapE4 /cfg/slowup.sh
sleep 0.5
echo "############ HAProxy $VER  (http-reuse safe, pool-purge-delay default 5s) ############"
cx "haproxy -v | head -1"

hr "E-a. idle keep-alive CLIENT held open; 'disable server' ONLY; wait 2s srv-removable"
cx "/cfg/ein2.sh a disable idle 9000 'wait 2s srv-removable a/s1' 6 at2open"

hr "E-b. idle keep-alive CLIENT held open; 'disable server' + 'shutdown sessions'; wait 2s"
cx "/cfg/ein2.sh b disable_shutsess idle 9000 'wait 2s srv-removable b/s1' 6 at2open"

hr "E-c. idle keep-alive CLIENT held open; NO teardown; wait 2s (control)"
cx "/cfg/ein2.sh c nothing idle 9000 'wait 2s srv-removable c/s1' 6 at2open"

hr "E-d. request IN FLIGHT (5s upstream); 'disable server' ONLY; wait 10s srv-removable"
cx "/cfg/ein2.sh d disable inflight 9100 'wait 10s srv-removable d/s1' 12 at2open"

hr "E-e. request IN FLIGHT (5s upstream); 'disable server' ONLY; wait 2s srv-removable"
cx "/cfg/ein2.sh e disable inflight 9100 'wait 2s srv-removable e/s1' 8 at2open"

hr "E-f. request IN FLIGHT (5s upstream); 'disable server' + 'shutdown sessions'; wait 2s"
cx "/cfg/ein2.sh f disable_shutsess inflight 9100 'wait 2s srv-removable f/s1' 6 at2open"

hr "E-g. SAME as E-d but framing '@@1' with the write side CLOSED immediately"
cx "/cfg/ein2.sh g disable inflight 9100 'wait 10s srv-removable g/s1' 12 at2closed"

hr "E-h. SAME as E-d but framing '@1' one-shot"
cx "/cfg/ein2.sh h disable inflight 9100 'wait 10s srv-removable h/s1' 12 at1"

hr "E-i. no client at all, only a pooled idle SERVER connection; 'disable server' ONLY"
cx "/cfg/ein2.sh i disable none 9000 'wait 2s srv-removable i/s1' 5 at2open"

hr "E-j. SAME as E-d but framing '@1' one-shot with the write side HELD OPEN"
cx "/cfg/ein2.sh j disable inflight 9100 'wait 10s srv-removable j/s1' 12 at1open"

hr "E-k. idle keep-alive CLIENT held open; 'disable server' ONLY; wait 2s via '@1' (works on 3.0 too)"
cx "/cfg/ein2.sh k disable idle 9000 'wait 2s srv-removable k/s1' 4 at1"

hr "E-l. idle keep-alive CLIENT held open; 'disable server' + 'shutdown sessions'; wait 2s via '@1'"
cx "/cfg/ein2.sh l disable_shutsess idle 9000 'wait 2s srv-removable l/s1' 4 at1"

stop
