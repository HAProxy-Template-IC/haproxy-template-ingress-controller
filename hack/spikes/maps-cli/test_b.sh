#!/usr/bin/env bash
# Section B -- CLI limits. Usage: test_b.sh <haproxy-version>
source "$(dirname "$0")/lib.sh"
VER="${1:-3.4}"
VERM=/etc/haproxy/maps/ver.map
SPEC=/etc/haproxy/maps/spec.map
trap stop_hap EXIT
start_hap "$VER" "$SPIKE_DIR/mapenv" || exit 1
echo "=== $(cli 'show info' | grep -m1 ^Version:) (image $VER) ==="

entries() { cli "show map" | sed -n 's|.*(/etc/haproxy/maps/ver.map).*entry_cnt=\([0-9]*\).*|\1|p'; }
clr() { cli "clear map $VERM" >/dev/null; }

say "B1  compiled-in defaults (haproxy -vv)"
docker exec "$CNAME" haproxy -vv 2>&1 | grep -A2 'Default settings'

say "B1  single COMMAND LINE length: 'add map <path> <key> <VALUE-of-N-bytes>' (no payload)"
# The line form stops the value at the first space, so pad with a single long token.
for n in 1000 8192 15000 16000 16300 16384 17000 20000 65536; do
  clr
  key="/len$n"
  resp=$(python3 -c "
import sys
n=int(sys.argv[1]); key=sys.argv[2]
sys.stdout.write('@1 add map /etc/haproxy/maps/ver.map %s %s\n' % (key,'v'*n))
" "$n" "$key" | $HAPCLI "$MSOCK" 5 2>&1)
  got=$(cli "get map $VERM $key" | sed -n 's/.*value="\(.*\)", type.*/\1/p' | wc -c)
  printf '  valuebytes=%-7s totalline=%-7s landed_entries=%-3s readback_valuelen=%-7s resp=%q\n' \
    "$n" "$((n+45))" "$(entries)" "$((got-1))" "$(echo "$resp" | head -c 100 | tr '\n' '|')"
done

say "B1b same, straight to the WORKER socket (no @1 relay)"
for n in 8192 15000 16000 16384 17000 65536; do
  clr
  key="/wlen$n"
  resp=$(python3 -c "
import sys
n=int(sys.argv[1]); key=sys.argv[2]
sys.stdout.write('add map /etc/haproxy/maps/ver.map %s %s\n' % (key,'v'*n))
" "$n" "$key" | $HAPCLI "$WSOCK" 5 2>&1)
  got=$(wcli "get map $VERM $key" | sed -n 's/.*value="\(.*\)", type.*/\1/p' | wc -c)
  printf '  valuebytes=%-7s landed_entries=%-3s readback_valuelen=%-7s resp=%q\n' \
    "$n" "$(entries)" "$((got-1))" "$(echo "$resp" | head -c 100 | tr '\n' '|')"
done

say "B2  payload cap: measured by test_b5.sh, which bisects on entries landed rather than on the reply text"

say "B2b oversize payload: is it rejected wholesale or partially applied?"
clr
python3 -c "
import sys
for i in range(60000): sys.stdout.write('/big%06d v%06d\n' % (i,i))
" > "$OUT_DIR/oversize.txt"
echo "  payload bytes: $(wc -c < "$OUT_DIR/oversize.txt")"
resp=$({ printf '@1 add map %s <<\n' "$VERM"; cat "$OUT_DIR/oversize.txt"; printf '\n'; } | $HAPCLI "$MSOCK" 5 2>&1)
echo "  response: $resp"
echo "  entries landed: $(entries)"
echo "  is the CLI session still usable afterwards? -> $(cli 'show info' | grep -c ^Version:) (1 = yes)"

say "B2c raising tune.cli.max-payload-size in the config"
stop_hap
mkdir -p "$SPIKE_DIR/mapenv2"
cp -r "$SPIKE_DIR/mapenv/maps" "$SPIKE_DIR/mapenv2/"
sed 's|    maxconn 2048|    maxconn 2048\n    tune.cli.max-payload-size 2097152|' \
  "$SPIKE_DIR/mapenv/haproxy.cfg" > "$SPIKE_DIR/mapenv2/haproxy.cfg"
if start_hap "$VER" "$SPIKE_DIR/mapenv2"; then
  echo "  restarted with tune.cli.max-payload-size 2097152"
  clr
  resp=$({ printf '@1 add map %s <<\n' "$VERM"; cat "$OUT_DIR/oversize.txt"; printf '\n'; } | $HAPCLI "$MSOCK" 20 2>&1)
  echo "  response: $(echo "$resp" | head -c 120)"
  echo "  entries landed: $(entries)   (payload was $(wc -c < "$OUT_DIR/oversize.txt") bytes / 60000 lines)"
else
  echo "  ^^ this version does NOT accept the 'tune.cli.max-payload-size' keyword"
fi
stop_hap
start_hap "$VER" "$SPIKE_DIR/mapenv" || exit 1

say "B3  payload command must be LAST on its line: 'add map <p> <<; show info'"
clr
echo "-- (a) 'add map <p> <<; show info' then a payload body:"
printf '@1 add map %s <<; show info\n/x3 X3\n\n' "$VERM" | $HAPCLI "$MSOCK" 5 2>&1 | head -12 | sed 's/^/    /'
echo "    entries: $(entries)  get /x3: $(cli "get map $VERM /x3" | head -c 120)"
clr
echo "-- (b) 'show info; add map <p> <<' then a payload body:"
printf '@1 show info; add map %s <<\n/x4 X4\n\n' "$VERM" | $HAPCLI "$MSOCK" 5 2>&1 | head -6 | sed 's/^/    /'
echo "    entries: $(entries)  get /x4: $(cli "get map $VERM /x4" | head -c 120)"
clr
echo "-- (c) payload with a custom terminator: 'add map <p> <<EOFMARK'"
printf '@1 add map %s <<EOFMARK\n/x5 X5\n\n/x6 X6 has a blank line above\nEOFMARK\n' "$VERM" | $HAPCLI "$MSOCK" 5 2>&1 | head -6 | sed 's/^/    /'
echo "    entries: $(entries)"
cli "show map $VERM"

say "B4  multi-command lines: how many ';'-separated commands fit on one line?"
for n in 2 10 100 500 1000 2000 4000; do
  clr
  # each command is 'set server dummy/sN weight 1' (~30 bytes)
  line=$(python3 -c "
import sys
n=int(sys.argv[1])
cmds=['set server dummy/s%d weight %d' % ((i%8)+1, (i%250)+1) for i in range(n)]
sys.stdout.write('@1 ' + '; '.join(cmds) + '\n')
" "$n")
  bytes=$(printf '%s' "$line" | wc -c)
  resp=$(printf '%s\n' "$line" | $HAPCLI "$MSOCK" 5 2>&1)
  errs=$(echo "$resp" | grep -c 'Unknown command\|too large\|truncated' )
  printf '  cmds=%-6s linebytes=%-7s errlines=%-4s resp_head=%q\n' \
    "$n" "$bytes" "$errs" "$(echo "$resp" | grep -v '^$' | head -1 | head -c 110)"
done

say "B4b the same long line, direct to the worker socket"
for n in 500 1000 2000 4000; do
  line=$(python3 -c "
import sys
n=int(sys.argv[1])
cmds=['set server dummy/s%d weight %d' % ((i%8)+1, (i%250)+1) for i in range(n)]
sys.stdout.write('; '.join(cmds) + '\n')
" "$n")
  bytes=$(printf '%s' "$line" | wc -c)
  resp=$(printf '%s\n' "$line" | $HAPCLI "$WSOCK" 5 2>&1)
  printf '  cmds=%-6s linebytes=%-7s resp_head=%q\n' \
    "$n" "$bytes" "$(echo "$resp" | grep -v '^$' | head -1 | head -c 110)"
done

say "B5  does the master '@1' relay change any limit? side-by-side"
for n in 15000 16000 16384 17000; do
  clr
  m=$(python3 -c "
import sys; n=int(sys.argv[1])
sys.stdout.write('@1 add map /etc/haproxy/maps/ver.map /m %s\n' % ('v'*n))" "$n" | $HAPCLI "$MSOCK" 5 2>&1 | head -c 60)
  mlen=$(cli "get map $VERM /m" | sed -n 's/.*value="\(.*\)", type.*/\1/p' | wc -c)
  clr
  w=$(python3 -c "
import sys; n=int(sys.argv[1])
sys.stdout.write('add map /etc/haproxy/maps/ver.map /w %s\n' % ('v'*n))" "$n" | $HAPCLI "$WSOCK" 5 2>&1 | head -c 60)
  wlen=$(wcli "get map $VERM /w" | sed -n 's/.*value="\(.*\)", type.*/\1/p' | wc -c)
  printf '  n=%-6s  master:len=%-7s resp=%q   worker:len=%-7s resp=%q\n' \
    "$n" "$((mlen-1))" "$(echo "$m" | tr '\n' '|')" "$((wlen-1))" "$(echo "$w" | tr '\n' '|')"
done

say "B5b '@1 <cmd>' vs '@@1' session vs worker socket for a PAYLOAD command"
clr
echo "-- pay1 ('@1 add map ... <<'):"
pay "add map $VERM" "/p1 P1" | head -5 | sed 's/^/    /'
echo "    entries: $(entries)  get /p1: $(cli "get map $VERM /p1" | head -c 110)"
clr
echo "-- paysess ("@@1" then 'add map ... <<'):"
paysess "add map $VERM" "/p2 P2" | head -5 | sed 's/^/    /'
echo "    entries: $(entries)  get /p2: $(cli "get map $VERM /p2" | head -c 110)"
clr
echo "-- wpay (worker socket directly):"
wpay "add map $VERM" "/p3 P3" | head -5 | sed 's/^/    /'
echo "    entries: $(entries)  get /p3: $(wcli "get map $VERM /p3" | head -c 110)"
