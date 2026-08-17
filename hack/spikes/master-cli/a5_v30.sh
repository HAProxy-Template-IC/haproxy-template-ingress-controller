#!/bin/bash
# Question A on HAProxy 3.0 (no dynamic backends there — file-defined backends,
# runtime servers only). Same framing questions, same message-string harvest.
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-a30.cfg" "$W/a30.cfg"
VER="${1:-3.0}"
now() { date +%s%3N; }
start hapA5 "$VER" a30.cfg || exit 1
echo "############ HAProxy $VER — master socket only ############"
cx "haproxy -v | head -1"

hr "A20. master CLI command set on $VER"
mc "help"
echo "--- master 'show proc':"; mc "show proc"
echo "--- master 'show info':"; mc "show info" | head -3

hr "A20b. does the worker have add/del/publish backend on $VER?"
mc "@1 help" > "$OUT/help-worker-$VER.txt"
grep -E "^  (add backend|del backend|publish|unpublish|add server|del server|wait|experimental-mode|set severity-output)" "$OUT/help-worker-$VER.txt"
echo "--- try them anyway:"
for c in "add backend dynX from be-http" "publish backend dynA" "unpublish backend dynA" "del backend dynA"; do
  echo ">>> @1 $c"; mc "@1 $c" | head -4
done
echo ">>> @@1 experimental-mode on; add backend dynX from be-http"
MC_T=10 mc "@@1
experimental-mode on; add backend dynX from be-http" | head -6

hr "A21. FRAMING on $VER"
echo "--- framing 1: one connection per command, '@1' prefix"
for c in "add server dynA/s1 127.0.0.1:9000 check init-state up" "enable health dynA/s1" "enable server dynA/s1"; do
  printf '>>> @1 %s\n' "$c"; mc "@1 $c"
done
cx "curl -s -o /dev/null -w 'curl dynA -> %{http_code}\n' -H 'x-be: dynA' http://127.0.0.1:8080/"
echo "--- framing 3: '@1 c1; c2' (one line)"
MC_T=10 mc "@1 echo M1; echo M2; add server dynB/s1 127.0.0.1:9000 check init-state up" | head -12
echo "--- framing 2: '@@1' + one ';' line"
MC_T=10 mc "@@1
echo M1; add server dynB/s1 127.0.0.1:9000 check init-state up; enable health dynB/s1; enable server dynB/s1; echo M2"
cx "curl -s -o /dev/null -w 'curl dynB -> %{http_code}\n' -H 'x-be: dynB' http://127.0.0.1:8080/"
echo "--- framing: '@@1' + NEWLINE-separated (how many run?)"
MC_T=10 mc "@@1
echo LINE-1
echo LINE-2
echo LINE-3"
echo "--- framing: '@@1' + 'prompt' + NEWLINE-separated"
MC_T=10 mc "@@1
prompt
echo LINE-1
echo LINE-2
echo LINE-3
quit"
echo "--- framing 4: '@1' per line in one connection"
MC_T=10 mc "@1 echo LINE-1
@1 echo LINE-2
@1 echo LINE-3"

hr "A22. session scope of experimental-mode / severity-output on $VER"
echo "--- separate connections:"; mc "@1 experimental-mode on"; mc "@1 experimental-mode"
echo "--- '@1' per line, one connection:"; MC_T=10 mc "@1 experimental-mode on
@1 experimental-mode"
echo "--- '@@1' + ';' line:"; MC_T=10 mc "@@1
experimental-mode on; experimental-mode"
echo "--- severity-output BEFORE:"
MC_T=10 mc "@@1
add server dynA/x1 127.0.0.1:9000; add server dynA/x1 127.0.0.1:9000; echo plain" | cat -A
echo "--- severity-output number:"
MC_T=10 mc "@@1
set severity-output number; add server dynA/x2 127.0.0.1:9000; add server dynA/x2 127.0.0.1:9000; echo plain" | cat -A
echo "--- severity-output string:"
MC_T=10 mc "@@1
set severity-output string; add server dynA/x3 127.0.0.1:9000; add server dynA/x3 127.0.0.1:9000; echo plain" | cat -A

hr "A23. delete sequence strings on $VER (per connection, unambiguous)"
for c in "disable server dynA/s1" "shutdown sessions server dynA/s1" "wait 3s srv-removable dynA/s1" "del server dynA/s1"; do
  echo "--- @1 $c"; MC_T=10 mc "@1 $c" | cat -A
done
echo "--- @1 wait 3s be-removable dynA (does the condition exist on $VER?)"
MC_T=10 mc "@1 wait 3s be-removable dynA" | cat -A
echo "--- @1 wait -h (list of conditions)"
mc "@1 wait -h"

hr "A24. ';'-line length limit on $VER"
for n in 100 200 400; do
  s=""; for i in $(seq 1 $n); do s="${s}add server dynB/n${n}_$i 127.0.0.1:9000; "; done
  s="${s}echo ==DONE-$n"
  o=$(MC_T=30 mc "@@1
$s")
  printf 'N=%-5s bytes=%-7s registered=%-5s other=%s\n' "$n" "${#s}" "$(printf '%s' "$o" | grep -c 'New server registered')" "$(printf '%s' "$o" | grep -v 'New server registered' | grep -v '^$' | head -1)"
done

hr "A25. reload on $VER: master socket availability + @1 right after"
echo "--- reload:"; T0=$(now); MC_T=30 mc "reload" | head -3; echo ">>> elapsed $(( $(now) - T0 ))ms"
for i in 1 2 3; do printf '[%s] ' $i; MC_T=5 mc "@1 show info" | grep -E '^(Pid|Uptime_sec)' | tr '\n' ' '; echo; done
echo "--- concurrent reload vs master connects:"
cx "( sleep 0.05; echo reload | socat -t30 stdio unix-connect:$MSOCK >/dev/null 2>&1 ) &
fails=0; ff=; lf=
for i in \$(seq 1 400); do
  if echo 'show version' | socat -t1 stdio unix-connect:$MSOCK >/dev/null 2>&1; then :; else
    fails=\$((fails+1)); t=\$(date +%s%3N); [ -z \"\$ff\" ] && ff=\$t; lf=\$t; fi
done
echo \"failed_connects=\$fails\"; [ -n \"\$ff\" ] && echo \"unavailable window ~\$((lf-ff))ms\"
wait"
echo "--- runtime-added servers survive the reload?"
mc "@1 show servers state dynB" | awk 'NR>2{c++} END{print (c+0)" servers in dynB after reload"}'

stop
