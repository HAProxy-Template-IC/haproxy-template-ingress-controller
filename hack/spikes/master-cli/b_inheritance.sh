#!/bin/bash
# Question B: does a RUNTIME-created backend ('add backend dynX from be-http')
# inherit the named defaults' http-request rules?  Verified with curl, not with
# command success.
#   be-http carries:  http-request set-header X-Prof yes
#                     http-request set-timeout server 2s
#                     http-request return status 503 if { var(txn.deny) -m found }
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-b.cfg" "$W/b.cfg"
cp "$SPIKE_DIR/slowup.sh" "$SPIKE_DIR/slowresp.sh" "$W/"; chmod +x "$W/slowup.sh" "$W/slowresp.sh"
VER="${1:-3.4}"
now() { date +%s%3N; }

start hapB "$VER" b.cfg || exit 1
docker exec -d hapB /cfg/slowup.sh
sleep 0.5
echo "############ HAProxy $VER ############"
cx "haproxy -v | head -1"
echo "--- config check:"; cx "haproxy -dr -c -f /cfg/b.cfg"

# probe <backend> : status, x-prof-seen response header, elapsed ms
probe() {
  local be="$1" extra="${2:-}"
  local t0=$(now)
  local o
  o=$(cx "curl -s -D- -o /dev/null --max-time 12 $extra -H 'x-be: $be' http://127.0.0.1:8080/ | tr -d '\r'")
  local t1=$(now)
  local code=$(printf '%s' "$o" | head -1)
  local prof=$(printf '%s' "$o" | grep -i '^x-prof-seen:' )
  printf '  %-10s %-30s x-prof-seen=[%s]  elapsed=%sms\n' "$be" "$code" "${prof#*: }" "$((t1-t0))"
}
body() { cx "curl -s --max-time 12 $2 -H 'x-be: $1' http://127.0.0.1:8080/"; echo; }

hr "B1. FILE-defined backends (the control) — 'from be-http' vs 'from plain'"
echo "fast upstream (echoes X-Prof back as x-prof-seen):"
probe fileFast
probe ctrlFast
echo "slow upstream (sleeps 5s; 'plain' has timeout server 20s, be-http adds set-timeout server 2s):"
probe fileSlow
probe ctrlSlow
echo "conditional 503 (x-deny: 1 sets var(txn.deny) in the frontend):"
probe fileFast "-H 'x-deny: 1'"
probe ctrlFast "-H 'x-deny: 1'"
echo "  body for fileFast with x-deny:"; body fileFast "-H 'x-deny: 1'"

HAS_DYNBE=0; mc "@1 help" | grep -q '^  add backend' && HAS_DYNBE=1
if [ "$HAS_DYNBE" = 0 ]; then
  hr "B2/B3 SKIPPED — '@1 help' has no 'add backend' on $VER"
  mc "@1 add backend dynFast from be-http" | head -2
  stop; exit 0
fi

hr "B2. RUNTIME-created backends 'from be-http'"
MC_T=20 mc "@@1
add backend dynFast from be-http mode http guid be:dynFast; add server dynFast/fast 127.0.0.1:9000 check init-state up; enable health dynFast/fast; enable server dynFast/fast; publish backend dynFast; echo ==dynFast-ok"
MC_T=20 mc "@@1
add backend dynSlow from be-http mode http guid be:dynSlow; add server dynSlow/slow 127.0.0.1:9100 init-state up; enable server dynSlow/slow; publish backend dynSlow; echo ==dynSlow-ok"
echo "fast upstream:"
probe dynFast
echo "slow upstream:"
probe dynSlow
echo "conditional 503:"
probe dynFast "-H 'x-deny: 1'"
echo "  body for dynFast with x-deny:"; body dynFast "-H 'x-deny: 1'"

hr "B3. RUNTIME-created backend WITHOUT 'from' (control)"
MC_T=20 mc "@@1
add backend dynBare mode http; add server dynBare/fast 127.0.0.1:9000 check init-state up; enable health dynBare/fast; enable server dynBare/fast; publish backend dynBare; echo ==dynBare-ok"
probe dynBare
echo "  and against the slow upstream (what timeout does a bare dynamic backend get?):"
MC_T=20 mc "@@1
add backend dynBareSlow mode http; add server dynBareSlow/slow 127.0.0.1:9100 init-state up; enable server dynBareSlow/slow; publish backend dynBareSlow; echo ==ok"
probe dynBareSlow

hr "B4. what does the runtime backend actually report? ('show backend', server state, timeouts)"
mc "@1 show backend"
echo "--- show servers state dynFast:"; mc "@1 show servers state dynFast"
echo "--- show stat for the four backends (scur/status):"
mc "@1 show stat" | awk -F, 'NR==1{next} $2=="BACKEND"{print "  "$1" status="$18}'

hr "B5. is 'http-request set-timeout server' accepted in a FRONTEND? (haproxy -c)"
cat > "$W/fe-timeout.cfg" <<'EOF'
defaults
    mode http
    timeout connect 5s
    timeout client 30s
    timeout server 20s
frontend fe
    bind :8080
    http-request set-timeout server 2s
    default_backend b
backend b
    server s1 127.0.0.1:9000
EOF
cx "haproxy -dr -c -f /cfg/fe-timeout.cfg; echo rc=\$?"

hr "B6. the fallback path: 'set-timeout server' inside a defaults section that a FRONTEND uses"
cat > "$W/fe-prof.cfg" <<'EOF'
global
    log stdout format raw local0 info
defaults plain
    mode http
    timeout connect 5s
    timeout client 30s
    timeout server 20s
defaults fe-prof from plain
    http-request set-timeout server 2s
    http-request set-header X-Prof yes
frontend fe from fe-prof
    bind :8080
    default_backend slowb
backend slowb from plain
    server slow 127.0.0.1:9100
EOF
echo "--- haproxy -c:"; cx "haproxy -dr -c -f /cfg/fe-prof.cfg; echo rc=\$?"
echo "--- does it actually take effect at runtime? (slow upstream sleeps 5s)"
docker rm -f hapB2 >/dev/null 2>&1
docker run -d --name hapB2 -v "$W:/cfg" --entrypoint haproxy "haproxytech/haproxy-debian:$VER" \
  -dr -W -db -S "$MSOCK,level,admin" -- /cfg/fe-prof.cfg >/dev/null
docker exec -d hapB2 /cfg/slowup.sh
sleep 1
T0=$(now)
docker exec hapB2 sh -c "curl -s -o /dev/null -w 'code=%{http_code}\n' --max-time 12 http://127.0.0.1:8080/" 2>&1
echo "  elapsed $(( $(now) - T0 ))ms  (~2s => frontend-side rule works; ~5s => not applied)"
docker rm -f hapB2 >/dev/null 2>&1

stop
docker rm -f hapB2 >/dev/null 2>&1
