#!/usr/bin/env bash
# Definitive payload-size sweep: count the entries that ACTUALLY land, per
# transport. An absent error string is not proof of acceptance, so every row
# reports entry_cnt as well as the response.
source "$(dirname "$0")/lib.sh"
VER="${1:-3.4}"
VERM=/etc/haproxy/maps/ver.map
trap stop_hap EXIT
start_hap "$VER" "$SPIKE_DIR/mapenv3" || exit 1
echo "=== $(cli 'show info' | grep -m1 ^Version:) (image $VER) ==="

entries() { cli "show map" | sed -n 's|.*(/etc/haproxy/maps/ver.map).*entry_cnt=\([0-9]*\).*|\1|p'; }

# each line is exactly 21 bytes: "/bNNNNNNN xxxxxxxxxx\n"
mkbody() { python3 -c "
import sys
n=int(sys.argv[1])
with open(sys.argv[2],'w') as f:
    for i in range(n): f.write('/b%07d %s\n' % (i,'x'*10))
" "$1" "$2"; }

sweep() { # sweep <label> <sockvar> <prefix-printf>
  local label="$1" sock="$2" pre="$3"
  echo "-- $label"
  for lines in 4000 10000 20000 25000 25200 25400 30000 50000 100000; do
    mkbody "$lines" "$OUT_DIR/pl3.txt"
    local bytes; bytes=$(wc -c < "$OUT_DIR/pl3.txt")
    cli "clear map $VERM" >/dev/null
    local t0 t1 resp
    t0=$(date +%s.%N)
    resp=$({ printf "$pre" "$VERM"; cat "$OUT_DIR/pl3.txt"; printf '\n'; } | $HAPCLI "$sock" 25 2>&1)
    t1=$(date +%s.%N)
    printf '   lines=%-7s bytes=%-9s landed=%-8s %s  resp=%q\n' \
      "$lines" "$bytes" "$(entries)" \
      "$(python3 -c "print('t=%.2fs'%($t1-$t0))")" "$(echo "$resp" | grep -v '^$' | head -1 | head -c 90)"
  done
}

say "B2z payload sweep -- entries actually landed (default config)"
sweep "master, '@1 add map <path> <<'" "$MSOCK" '@1 add map %s <<\n'
sweep "master, '@@1' session (3.1+)"   "$MSOCK" '@@1\nadd map %s <<\n'
sweep "worker socket direct"           "$WSOCK" 'add map %s <<\n'

say "B3z payload command combined with other commands on one line"
probe3() { # probe3 <label> <raw printf fmt>
  cli "clear map $VERM" >/dev/null
  echo "-- $1"
  printf "$2" "$VERM" | send 2>&1 | grep -v '^$' | head -6 | sed 's/^/      /'
  echo "      entry_cnt=$(entries)  show map:"
  cli "show map $VERM" | sed 's/^/      /'
}
probe3 "'@1 add map <p> <<; show version' + body"  '@1 add map %s <<; show version\n/x3 X3\n\n'
probe3 "'@1 show version; add map <p> <<' + body"  '@1 show version; add map %s <<\n/x4 X4\n\n'
probe3 "'@1' session, 'add map <p> <<; show version' + body" '@1\nadd map %s <<; show version\n/x5 X5\n\n'
probe3 "'@1' session, 'show version; add map <p> <<' + body" '@1\nshow version; add map %s <<\n/x6 X6\n\n'
probe3 "worker socket, 'add map <p> <<; show version' + body" 'IGNORED'
cli "clear map $VERM" >/dev/null
printf 'add map %s <<; show version\n/x7 X7\n\n' "$VERM" | wsend 2>&1 | grep -v '^$' | head -6 | sed 's/^/      /'
echo "      entry_cnt=$(entries)"
cli "show map $VERM" | sed 's/^/      /'
