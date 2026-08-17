#!/bin/bash
# Question A, part 3: response<->command pairing inside one '@@1' + ';' line.
# A2/A2b showed extra "Server ... is going DOWN"/"has no server available" lines
# in the stream. Where exactly do they belong?
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-a.cfg" "$W/a.cfg"
VER="${1:-3.4}"
start hapA3 "$VER" a.cfg || exit 1
echo "############ HAProxy $VER ############"

hr "A15. one command per connection, so pairing is unambiguous"
mc "@1 add backend dynP from be-http mode http"
mc "@1 add server dynP/s1 127.0.0.1:9000 check init-state up"
mc "@1 enable health dynP/s1"
mc "@1 enable server dynP/s1"
mc "@1 publish backend dynP"
echo "--- 'disable server dynP/s1'  <<< does IT print the 'going DOWN' lines?"
mc "@1 disable server dynP/s1" | cat -A
echo "--- 'shutdown sessions server dynP/s1'"
mc "@1 shutdown sessions server dynP/s1" | cat -A
echo "--- 'wait 3s srv-removable dynP/s1'"
MC_T=10 mc "@1 wait 3s srv-removable dynP/s1" | cat -A
echo "--- 'del server dynP/s1'"
mc "@1 del server dynP/s1" | cat -A
echo "--- 'unpublish backend dynP'"
mc "@1 unpublish backend dynP" | cat -A
echo "--- 'wait 3s be-removable dynP'"
MC_T=10 mc "@1 wait 3s be-removable dynP" | cat -A
echo "--- 'del backend dynP'"
mc "@1 del backend dynP" | cat -A

hr "A15b. same steps as ONE '@@1' ';' line with distinct echo markers, cat -A"
MC_T=20 mc "@@1
add backend dynQ from be-http mode http; add server dynQ/s1 127.0.0.1:9000 check init-state up; enable health dynQ/s1; enable server dynQ/s1; publish backend dynQ; echo M0" | cat -A
echo "--- teardown:"
MC_T=20 mc "@@1
echo M1; unpublish backend dynQ; echo M2; disable server dynQ/s1; echo M3; shutdown sessions server dynQ/s1; echo M4; wait 3s srv-removable dynQ/s1; echo M5; del server dynQ/s1; echo M6; wait 3s be-removable dynQ; echo M7; del backend dynQ; echo M8" | cat -A

hr "A15c. WITHOUT 'shutdown sessions', after a real request (idle server conn in the pool)"
MC_T=20 mc "@@1
add backend dynR from be-http mode http; add server dynR/s1 127.0.0.1:9000 check init-state up; enable health dynR/s1; enable server dynR/s1; publish backend dynR; echo M0"
cx "curl -s -o /dev/null -H 'x-be: dynR' http://127.0.0.1:8080/"
MC_T=20 mc "@@1
echo M1; unpublish backend dynR; echo M2; disable server dynR/s1; echo M3; wait 3s srv-removable dynR/s1; echo M4; del server dynR/s1; echo M5; wait 3s be-removable dynR; echo M6; del backend dynR; echo M7" | cat -A

hr "A16. an '@@1' sequence issued while a reload is in flight (fixed quoting)"
cat > "$W/mkseq.sh" <<'EOS'
#!/bin/sh
i="$1"
printf '@@1\nadd backend r%s from be-http mode http; publish backend r%s; echo ==ok-%s\n' "$i" "$i" "$i" > /tmp/c$i.txt
socat -t5 stdio unix-connect:/etc/haproxy/haproxy-master.sock < /tmp/c$i.txt 2>&1 | tr '\n' '|'
EOS
cx "chmod +x /cfg/mkseq.sh
( echo reload | socat -t30 stdio unix-connect:$MSOCK > /tmp/rl.log 2>&1 ) &
for i in \$(seq 1 15); do echo \"[\$i] \$(/cfg/mkseq.sh \$i)\"; sleep 0.01; done
wait
echo '--- backends after reload+adds:'
echo '@1 show backend' | socat -t3 stdio unix-connect:$MSOCK"

hr "A17. how long is the master socket unavailable during a reload? (connect attempts every ~2ms)"
cx "( sleep 0.05; echo reload | socat -t30 stdio unix-connect:$MSOCK > /tmp/rl2.log 2>&1 ) &
s=\$(date +%s%3N); fails=0; firstfail=; lastfail=
for i in \$(seq 1 400); do
  if echo 'show version' | socat -t1 stdio unix-connect:$MSOCK >/dev/null 2>&1; then :; else
    fails=\$((fails+1)); t=\$(date +%s%3N); [ -z \"\$firstfail\" ] && firstfail=\$t; lastfail=\$t
  fi
done
e=\$(date +%s%3N)
echo \"attempts=400 window=\$((e-s))ms failed=\$fails\"
[ -n \"\$firstfail\" ] && echo \"unavailable window ~ \$((lastfail-firstfail))ms\"
wait"

stop
