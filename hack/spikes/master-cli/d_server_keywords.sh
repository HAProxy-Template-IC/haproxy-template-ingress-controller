#!/bin/bash
# Question D: which server keywords does 'add server' accept, per HAProxy version,
# through the MASTER socket; which balance algorithms accept 'add server'; and how
# long a server added WITHOUT init-state takes to serve traffic on 3.0.
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-d.cfg" "$W/d.cfg"
VER="${1:-3.4}"
now() { date +%s%3N; }
start hapD "$VER" d.cfg || exit 1
echo "############ HAProxy $VER ############"
cx "haproxy -v | head -1"

hr "D0. what does 'add server' say about its own syntax?"
for c in "add server" "add server help" "add server kw/h1" "add server kw/h2 127.0.0.1:9000 no-such-keyword-here"; do
  echo "--- @1 $c"; mc "@1 $c"
done
echo "--- @1 help add server"; mc "@1 help add server"

hr "D1. keyword matrix — one 'add server' per keyword, through '@1'"
# name|extra args      (files under /cfg/tls are real, so a rejection means the
#                       keyword is unsupported, not that the file was bad)
KW='
check|check
inter|check inter 1s
fastinter|check inter 1s fastinter 500ms
downinter|check inter 1s downinter 2s
rise|check rise 2
fall|check fall 2
port|port 9000
addr|addr 127.0.0.1
weight|weight 10
maxconn|maxconn 100
maxqueue|maxqueue 10
minconn|minconn 1
backup|backup
cookie|cookie ck1
guid|guid srv:GUIDTOKEN
ssl|ssl verify none
sni|ssl verify none sni str(example.com)
verify|ssl verify none
verifyhost|ssl verify required ca-file /cfg/tls/ca.crt verifyhost example.com
ca-file|ssl verify required ca-file /cfg/tls/ca.crt
crt|ssl verify none crt /cfg/tls/client.pem
crl-file|ssl verify required ca-file /cfg/tls/ca.crt crl-file /cfg/tls/crl.pem
alpn|ssl verify none alpn h2,http/1.1
ciphers|ssl verify none ciphers ECDHE-RSA-AES128-GCM-SHA256
ciphersuites|ssl verify none ciphersuites TLS_AES_128_GCM_SHA256
ssl-min-ver|ssl verify none ssl-min-ver TLSv1.2
ssl-max-ver|ssl verify none ssl-max-ver TLSv1.3
proto|proto h2
send-proxy|send-proxy
send-proxy-v2|send-proxy-v2
slowstart|slowstart 10s
agent-check|agent-check agent-port 9001 agent-inter 1s
agent-port|agent-check agent-port 9001 agent-inter 1s
agent-inter|agent-check agent-port 9001 agent-inter 1s
agent-send|agent-check agent-port 9001 agent-inter 1s agent-send hello
on-marked-down|check on-marked-down shutdown-sessions
observe|check observe layer4
init-state|check init-state up
disabled|disabled
enabled|enabled
no-check|no-check
'
i=0
printf '%-16s %-10s %s\n' KEYWORD VERDICT "HAProxy output"
printf '%-16s %-10s %s\n' ---------------- ---------- --------------
echo "$KW" | while IFS='|' read -r name args; do
  [ -z "$name" ] && continue
  i=$((i+1))
  sname="k$(printf '%s' "$name" | tr -cd 'a-z0-9')$i"
  a=$(printf '%s' "$args" | sed "s/GUIDTOKEN/$sname/")
  o=$(MC_T=8 mc "@1 add server kw/$sname 127.0.0.1:9000 $a" | grep -v '^$' | tr '\n' ' ')
  case "$o" in
    "New server registered."*) v=ACCEPTED ;;
    *"unknown keyword"*)       v=UNKNOWN ;;
    *)                         v=OTHER ;;
  esac
  printf '%-16s %-10s %s\n' "$name" "$v" "$o"
done

hr "D1b. the ssl-file keywords again, now that the SAME files are preloaded by the config"
echo "(backend 'sslpre' in the file already uses ca.crt / crl.pem / client.pem)"
for probe in \
  "pre_cafile|ssl verify required ca-file /cfg/tls/ca.crt" \
  "pre_crt|ssl verify none crt /cfg/tls/client.pem" \
  "pre_crl|ssl verify required ca-file /cfg/tls/ca.crt crl-file /cfg/tls/crl.pem" \
  "pre_verifyhost|ssl verify required ca-file /cfg/tls/ca.crt verifyhost example.com"; do
  n=${probe%%|*}; a=${probe#*|}
  printf '  %-16s -> %s\n' "$n" "$(MC_T=8 mc "@1 add server kw/$n 127.0.0.1:9000 $a" | grep -v '^$' | tr '\n' ' ')"
done

hr "D1c. the ssl-file keywords with files the config never loaded (ca2/client2/crl2)"
for probe in \
  "new_cafile|ssl verify required ca-file /cfg/tls/ca2.crt" \
  "new_crt|ssl verify none crt /cfg/tls/client2.pem" \
  "new_crl|ssl verify required ca-file /cfg/tls/ca2.crt crl-file /cfg/tls/crl2.pem"; do
  n=${probe%%|*}; a=${probe#*|}
  printf '  %-16s -> %s\n' "$n" "$(MC_T=8 mc "@1 add server kw/$n 127.0.0.1:9000 $a" | grep -v '^$' | tr '\n' ' ')"
done

hr "D2. balance algorithms vs 'add server'"
for b in roundrobin static_rr leastconn first random source source_consistent uri uri_consistent hdr hdr_consistent; do
  o=$(MC_T=8 mc "@1 add server bal_$b/x1 127.0.0.1:9000" | grep -v '^$' | tr '\n' ' ')
  printf '  balance %-18s -> %s\n' "$b" "$o"
done

hr "D3. is a hash/map-based backend addable once it already has a file-defined server?"
echo "(the file config has no servers in the bal_* backends; retry after adding one is impossible if the first add fails)"
mc "@1 show backend" | head -20

hr "D4. time-to-traffic for a server added WITHOUT init-state (check inter 300ms rise 1)"
poll() { # poll <backend> <max_ms>
  local be="$1" max="$2" t0=$(now) code
  while :; do
    code=$(cx "curl -s -o /dev/null -w '%{http_code}' --max-time 2 -H 'x-be: $be' http://127.0.0.1:8080/")
    [ "$code" = "200" ] && { echo "  $be served 200 after $(( $(now) - t0 ))ms"; return 0; }
    [ $(( $(now) - t0 )) -gt "$max" ] && { echo "  $be still $code after ${max}ms"; return 1; }
    sleep 0.02
  done
}
srvstate() { mc "@1 show servers state $1" | sed -n '3p' | awk '{print "srv="$4" op_state="$6" admin_state="$7" check_status="$11" check_health="$13}'; }
echo "--- probe1: add (no init-state) + enable health + enable server"
mc "@1 add server probe1/s1 127.0.0.1:9000 check inter 300ms rise 1 fall 1"
echo "  state right after add:    $(srvstate probe1)"
T0=$(now)
mc "@1 enable health probe1/s1" >/dev/null
mc "@1 enable server probe1/s1" >/dev/null
echo "  state right after enable: $(srvstate probe1)"
poll probe1 5000
echo "  state after serving:      $(srvstate probe1)"

echo "--- probe2: add (no init-state) + enable health + enable server + set server health up"
mc "@1 add server probe2/s1 127.0.0.1:9000 check inter 300ms rise 1 fall 1"
T0=$(now)
mc "@1 enable health probe2/s1" >/dev/null
mc "@1 enable server probe2/s1" >/dev/null
mc "@1 set server probe2/s1 health up"
poll probe2 5000

echo "--- probe3: add (no init-state) + set server health up ONLY (no enable server)"
mc "@1 add server probe3/s1 127.0.0.1:9000 check inter 300ms rise 1 fall 1"
mc "@1 set server probe3/s1 health up"
poll probe3 1500
echo "  state: $(srvstate probe3)"

echo "--- probe4: add WITH init-state up (if supported) + enable health + enable server"
mc "@1 add server probe4/s1 127.0.0.1:9000 check inter 300ms rise 1 fall 1 init-state up"
echo "  state right after add:    $(srvstate probe4)"
T0=$(now)
mc "@1 enable health probe4/s1" >/dev/null
mc "@1 enable server probe4/s1" >/dev/null
poll probe4 5000

hr "D5. 'set server <b>/<s> health up' — exact strings"
mc "@1 set server probe1/s1 health up" | cat -A
mc "@1 set server probe1/s1 health down" | cat -A
mc "@1 set server probe1/s1 state ready" | cat -A
mc "@1 set server probe1/s1 state maint" | cat -A

stop
