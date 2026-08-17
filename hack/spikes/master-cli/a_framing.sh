#!/bin/bash
# Question A: framing of runtime commands through the MASTER socket only.
# Run: bash a_framing.sh 3.4   /   bash a_framing.sh 3.0
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-a.cfg" "$W/a.cfg"

VER="${1:-3.4}"
now() { date +%s%3N; }
el()  { echo ">>> elapsed ${1}ms"; }

start hapA "$VER" a.cfg || exit 1
echo "############ HAProxy $VER — master socket only (haproxy -dr -W -db -S $MSOCK,level,admin -- cfg) ############"
cx "haproxy -v | head -1"

hr "A0. what the MASTER CLI itself answers (no @ prefix)"
for c in "show version" "show proc" "show info" "show stat" "experimental-mode on" "add server x/y 1.2.3.4:80"; do
  echo "--- master: '$c'"; mc "$c" | head -12
done
echo "--- master: 'help'"; mc "help"

hr "A0b. worker via '@1 show info' (first 8 lines)"
mc "@1 show info" | head -8

hr "A0c. master 'show proc' — readiness probe candidate"
mc "show proc"

hr "A1. FRAMING 1 — one connection per command, each prefixed '@1'"
for c in \
  "experimental-mode on" \
  "add backend dynA from be-http mode http guid be:dynA" \
  "add server dynA/s1 127.0.0.1:9000 check init-state up" \
  "enable health dynA/s1" \
  "enable server dynA/s1" \
  "publish backend dynA"; do
  printf '>>> @1 %s\n' "$c"; mc "@1 $c"
done
echo ">>> @1 show backend"; mc "@1 show backend"
echo ">>> curl x-be: dynA"; cx "curl -s -o /dev/null -w '%{http_code}\n' -H 'x-be: dynA' http://127.0.0.1:8080/"

hr "A2. FRAMING 2 — ONE connection: '@@1' then whole sequence with ';' (echo markers between)"
T0=$(now)
MC_T=20 mc "@@1
echo MARK-experimental; experimental-mode on; echo MARK-addbe; add backend dynB from be-http mode http guid be:dynB; echo MARK-addsrv; add server dynB/s1 127.0.0.1:9000 check init-state up; echo MARK-enhealth; enable health dynB/s1; echo MARK-ensrv; enable server dynB/s1; echo MARK-pub; publish backend dynB; echo MARK-end"
el $(( $(now) - T0 ))
echo ">>> curl x-be: dynB"; cx "curl -s -o /dev/null -w '%{http_code}\n' -H 'x-be: dynB' http://127.0.0.1:8080/"

hr "A2b. FRAMING 2 delete sequence in ONE '@@1' connection (two 'wait' commands inside)"
T0=$(now)
MC_T=30 mc "@@1
echo MARK-unpub; unpublish backend dynB; echo MARK-dissrv; disable server dynB/s1; echo MARK-shutsess; shutdown sessions server dynB/s1; echo MARK-waitsrv; wait 3s srv-removable dynB/s1; echo MARK-delsrv; del server dynB/s1; echo MARK-waitbe; wait 3s be-removable dynB; echo MARK-delbe; del backend dynB; echo MARK-end"
el $(( $(now) - T0 ))
echo ">>> @1 show backend"; mc "@1 show backend"

hr "A3. FRAMING 3 — ONE connection, ONE line: '@1 cmd1; cmd2; ...'  (does '@1' cover the whole line?)"
MC_T=20 mc "@1 echo MARK-1; echo MARK-2; add backend dynC from be-http mode http guid be:dynC" | head -14
echo ">>> @1 show backend"; mc "@1 show backend"

hr "A4. FRAMING 4 — ONE connection, newline-separated, each line prefixed '@1'"
T0=$(now)
MC_T=20 mc "@1 echo MARK-experimental
@1 experimental-mode on
@1 add backend dynD from be-http mode http guid be:dynD
@1 add server dynD/s1 127.0.0.1:9000 check init-state up
@1 enable health dynD/s1
@1 enable server dynD/s1
@1 publish backend dynD
@1 echo MARK-end"
el $(( $(now) - T0 ))
echo ">>> curl x-be: dynD"; cx "curl -s -o /dev/null -w '%{http_code}\n' -H 'x-be: dynD' http://127.0.0.1:8080/"

hr "A5. SESSION SCOPE of experimental-mode (state query is the probe)"
echo "--- (a) two separate connections: 'on' then query"
mc "@1 experimental-mode on"; mc "@1 experimental-mode"
echo "--- (b) ONE connection, framing 4 ('@1' per line): 'on' then query"
MC_T=10 mc "@1 experimental-mode on
@1 experimental-mode"
echo "--- (c) ONE connection, framing 2 ('@@1' session): 'on' then query"
MC_T=10 mc "@@1
experimental-mode on
experimental-mode"
echo "--- (d) ONE connection, framing 2, same LINE with ';'"
MC_T=10 mc "@@1
experimental-mode on; experimental-mode"
echo "--- (e) fresh connection afterwards"
mc "@1 experimental-mode"

hr "A5b. SESSION SCOPE of severity-output (same probes, failing+succeeding command)"
echo "--- (a) no severity-output at all, @@1, success then failure:"
MC_T=10 mc "@@1
add backend sev0 from be-http
add backend sev0 from be-http" | cat -A
echo "--- (b) 'set severity-output number' first, same two commands:"
MC_T=10 mc "@@1
set severity-output number
add backend sev1 from be-http
add backend sev1 from be-http
enable server dynD/s1
echo hello" | cat -A
echo "--- (c) 'set severity-output string':"
MC_T=10 mc "@@1
set severity-output string
add backend sev2 from be-http
add backend sev2 from be-http" | cat -A
echo "--- (d) framing 4 ('@1' per line): does 'set severity-output number' carry to the next '@1' line?"
MC_T=10 mc "@1 set severity-output number
@1 add backend sev3 from be-http
@1 add backend sev3 from be-http" | cat -A
echo "--- (e) fresh connection: is severity-output back to none?"
mc "@1 add backend sev1 from be-http" | cat -A

hr "A6. exact failure / success strings (all through '@@1')"
runq() { echo "### $1"; MC_T=20 mc "@@1
$2"; }
runq "duplicate backend name" "add backend dynA from be-http"
runq "duplicate server name" "add server dynA/s1 127.0.0.1:9000"
runq "del a published backend" "del backend dynA"
runq "del a server that is UP" "del server dynA/s1"
echo "### wait 2s srv-removable on an UP server (timing + string)"
T0=$(now); MC_T=20 mc "@@1
wait 2s srv-removable dynA/s1"; el $(( $(now) - T0 ))
runq "add server into unknown backend" "add server nosuchbe/s1 127.0.0.1:9000"
runq "unknown command on the worker" "frobnicate"
echo "### unknown worker id"; mc "@9 show info"
runq "publish an already published backend" "publish backend dynA"
runq "unpublish + del the FILE-defined backend 'nope'" "unpublish backend nope
wait 2s be-removable nope
del backend nope"
runq "add backend clashing with a FRONTEND name" "add backend fe from be-http"
runq "add backend with a duplicate guid" "add backend dupguid from be-http mode http guid be:dynA"

hr "A7. full happy-path strings, one per line, framing 2"
MC_T=30 mc "@@1
echo == add backend
add backend dynZ from be-http mode http guid be:dynZ
echo == add server
add server dynZ/s1 127.0.0.1:9000 check init-state up
echo == enable health
enable health dynZ/s1
echo == enable server
enable server dynZ/s1
echo == publish
publish backend dynZ
echo == unpublish
unpublish backend dynZ
echo == disable server
disable server dynZ/s1
echo == shutdown sessions
shutdown sessions server dynZ/s1
echo == wait srv-removable
wait 3s srv-removable dynZ/s1
echo == del server
del server dynZ/s1
echo == wait be-removable
wait 3s be-removable dynZ
echo == del backend
del backend dynZ
echo == done"

hr "A8. reload: does the master socket stay usable?"
echo "--- show proc before"; mc "show proc"
echo "--- 'reload' (synchronous, master):"; T0=$(now); MC_T=30 mc "reload"; el $(( $(now) - T0 ))
echo "--- '@1 show info' right after (5 consecutive connections):"
for i in 1 2 3 4 5; do printf '[%s] ' "$i"; MC_T=5 mc "@1 show info" | grep -E '^(Pid|Uptime_sec)' | tr '\n' ' '; echo; done

hr "A8b. 'reload' and '@1 show info' in the SAME connection"
MC_T=30 mc "reload
@1 show info" | grep -E '^(Success|Pid|Uptime_sec|Unknown|\[)' | head -8

hr "A8c. CONCURRENT: reload running in the background while '@1 show info' is hammered"
cx "( echo reload | socat -t30 stdio unix-connect:$MSOCK > /tmp/reload.log 2>&1 ) &
for i in \$(seq 1 60); do
  r=\$( (echo '@1 show info' | socat -t2 stdio unix-connect:$MSOCK) 2>&1 | grep -E '^(Pid|Uptime_sec)' | tr '\n' ' ')
  [ -z \"\$r\" ] && r=\"<<EMPTY/ERROR>>\"
  echo \"[\$i] \$r\"
  sleep 0.02
done
wait
echo '--- reload.log:'; head -3 /tmp/reload.log"

hr "A8d. same, but hammering master 'show proc' (readiness-probe simulation)"
cx "( echo reload | socat -t30 stdio unix-connect:$MSOCK > /tmp/reload2.log 2>&1 ) &
for i in \$(seq 1 60); do
  r=\$( (echo 'show proc' | socat -t2 stdio unix-connect:$MSOCK) 2>&1 | awk '/^# workers/{f=1;next} f&&NF{print \$1\" \"\$2; exit}')
  [ -z \"\$r\" ] && r=\"<<EMPTY/ERROR>>\"
  echo \"[\$i] \$r\"
  sleep 0.02
done
wait"

hr "A9. after all reloads: are the runtime-created backends still there?"
mc "@1 show backend"
mc "show proc"

stop
