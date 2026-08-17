#!/bin/bash
# Question A, part 2: the details that part 1 turned up.
#  - '@@1' + NEWLINE-separated commands drops everything after the first line
#  - so: does 'prompt' fix it?  what is the ';'-line length limit?
#  - severity-output before/after with the ONLY framing that keeps session state
#  - master-socket behaviour while a reload is actually in flight (raw error text)
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-a.cfg" "$W/a.cfg"
VER="${1:-3.4}"
now() { date +%s%3N; }
start hapA2 "$VER" a.cfg || exit 1
echo "############ HAProxy $VER ############"

hr "A10. '@@1' + NEWLINE-separated commands — how many lines really run?"
MC_T=10 mc "@@1
echo LINE-1
echo LINE-2
echo LINE-3"

hr "A10b. '@@1' then 'prompt' (interactive mode) then NEWLINE-separated commands"
MC_T=10 mc "@@1
prompt
echo LINE-1
echo LINE-2
echo LINE-3
quit"

hr "A10c. '@@1' with the command on the SAME line"
MC_T=10 mc "@@1 echo SAMELINE"

hr "A10d. '@@1' then ONE line with ';' — the working framing"
MC_T=10 mc "@@1
echo LINE-1; echo LINE-2; echo LINE-3"

hr "A10e. master-level 'prompt' first, then '@1' lines (does interactive master help?)"
MC_T=10 mc "prompt
@1 echo A
@1 echo B
quit"

hr "A11. severity-output BEFORE/AFTER (framing: '@@1' + one ';' line), success + failure"
echo "--- BEFORE (no severity-output):"
MC_T=15 mc "@@1
add backend sevA from be-http; add backend sevA from be-http; add server sevA/s1 127.0.0.1:9000; enable server sevA/s1; echo plain-echo" | cat -A
echo "--- AFTER ('set severity-output number' as the FIRST command of the same session):"
MC_T=15 mc "@@1
set severity-output number; add backend sevB from be-http; add backend sevB from be-http; add server sevB/s1 127.0.0.1:9000; enable server sevB/s1; echo plain-echo" | cat -A
echo "--- AFTER ('set severity-output string'):"
MC_T=15 mc "@@1
set severity-output string; add backend sevC from be-http; add backend sevC from be-http; add server sevC/s1 127.0.0.1:9000; enable server sevC/s1; echo plain-echo" | cat -A
echo "--- multi-line 'show' output with severity-output number (show backend):"
MC_T=15 mc "@@1
set severity-output number; show backend" | cat -A | head -12

hr "A12. FULL happy-path + delete strings, framing '@@1' + ';' line, echo markers"
MC_T=30 mc "@@1
echo ==addbe; add backend dynH from be-http mode http guid be:dynH; echo ==addsrv; add server dynH/s1 127.0.0.1:9000 check init-state up; echo ==enhealth; enable health dynH/s1; echo ==ensrv; enable server dynH/s1; echo ==pub; publish backend dynH; echo ==showbe; show backend; echo ==unpub; unpublish backend dynH; echo ==dissrv; disable server dynH/s1; echo ==shutsess; shutdown sessions server dynH/s1; echo ==waitsrv; wait 3s srv-removable dynH/s1; echo ==delsrv; del server dynH/s1; echo ==waitbe; wait 3s be-removable dynH; echo ==delbe; del backend dynH; echo ==end"

hr "A12b. same sequence, but WITHOUT 'shutdown sessions' — does 'wait srv-removable' still pass?"
MC_T=30 mc "@@1
add backend dynI from be-http mode http; add server dynI/s1 127.0.0.1:9000 check init-state up; enable health dynI/s1; enable server dynI/s1; publish backend dynI; echo ==setup-done"
cx "curl -s -o /dev/null -H 'x-be: dynI' http://127.0.0.1:8080/"
T0=$(now)
MC_T=30 mc "@@1
unpublish backend dynI; disable server dynI/s1; wait 3s srv-removable dynI/s1; del server dynI/s1; wait 3s be-removable dynI; del backend dynI; echo ==end"
echo ">>> elapsed $(( $(now) - T0 ))ms"

hr "A13. how long can the ';' line be? (N x 'add server' in ONE @@1 line)"
MC_T=30 mc "@@1
add backend bulk from be-http mode http; publish backend bulk"
for n in 10 50 100 200 400 800 1600; do
  seq_cmd=""
  for i in $(seq 1 $n); do seq_cmd="${seq_cmd}add server bulk/n${n}_$i 127.0.0.1:9000 init-state up; "; done
  seq_cmd="${seq_cmd}echo ==DONE-$n"
  bytes=${#seq_cmd}
  out=$(MC_T=30 mc "@@1
$seq_cmd")
  ok=$(printf '%s' "$out" | grep -c 'New server registered')
  saw_end=$(printf '%s' "$out" | grep -c "==DONE-$n")
  err=$(printf '%s' "$out" | grep -v 'New server registered' | grep -v '^$' | head -2 | tr '\n' '|')
  printf 'N=%-5s line=%-7s bytes  registered=%-5s saw_end=%s  other=%s\n' "$n" "$bytes" "$ok" "$saw_end" "$err"
done
echo ">>> servers actually present:"; mc "@1 show servers state bulk" | awk 'NR>2{c++} END{print c+0" servers"}'

hr "A14. master socket DURING an in-flight reload — raw output of every attempt"
cx "( echo reload | socat -t30 stdio unix-connect:$MSOCK > /tmp/rl.log 2>&1 ) &
for i in \$(seq 1 25); do
  o=\$( (echo '@1 show info' | socat -t2 stdio unix-connect:$MSOCK) 2>&1 )
  rc=\$?
  first=\$(printf '%s' \"\$o\" | head -1)
  pid=\$(printf '%s' \"\$o\" | grep '^Pid:' )
  echo \"[\$i] rc=\$rc  first-line='\$first'  \$pid\"
  sleep 0.01
done
wait"

hr "A14b. same for master 'show proc'"
cx "( echo reload | socat -t30 stdio unix-connect:$MSOCK > /tmp/rl2.log 2>&1 ) &
for i in \$(seq 1 25); do
  o=\$( (echo 'show proc' | socat -t2 stdio unix-connect:$MSOCK) 2>&1 )
  rc=\$?
  echo \"[\$i] rc=\$rc lines=\$(printf '%s' \"\$o\" | wc -l) first='\$(printf '%s' \"\$o\" | head -1 | cut -c1-40)'\"
  sleep 0.01
done
wait"

hr "A14c. an @@1 sequence issued while a reload is in flight"
cx "( echo reload | socat -t30 stdio unix-connect:$MSOCK > /tmp/rl3.log 2>&1 ) &
for i in \$(seq 1 12); do
  printf '@@1\nadd backend r\$i from be-http; publish backend r\$i; echo ==ok-\$i\n' > /tmp/c.txt
  o=\$( (socat -t5 stdio unix-connect:$MSOCK < /tmp/c.txt) 2>&1 | tr '\n' '|' )
  echo \"[\$i] \$o\"
  sleep 0.01
done
wait
echo '--- backends after:'; echo '@1 show backend' | socat -t3 stdio unix-connect:$MSOCK"

stop
