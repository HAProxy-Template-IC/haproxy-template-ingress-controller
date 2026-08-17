#!/usr/bin/env bash
# Pin the payload cap in the low range and test whether it tracks tune.bufsize.
# Usage: test_b5.sh <version> [bufsize]
source "$(dirname "$0")/lib.sh"
VER="${1:-3.0}"
BUFSIZE="${2:-}"
VERM=/etc/haproxy/maps/ver.map
ENVDIR="$SPIKE_DIR/mapenv5"
rm -rf "$ENVDIR"; mkdir -p "$ENVDIR"
cp -r "$SPIKE_DIR/mapenv/maps" "$ENVDIR/"
if [ -n "$BUFSIZE" ]; then
  sed "s|    maxconn 2048|    maxconn 2048\n    tune.bufsize $BUFSIZE|" "$SPIKE_DIR/mapenv/haproxy.cfg" > "$ENVDIR/haproxy.cfg"
else
  cp "$SPIKE_DIR/mapenv/haproxy.cfg" "$ENVDIR/haproxy.cfg"
fi
trap stop_hap EXIT
start_hap "$VER" "$ENVDIR" || exit 1
echo "=== $(cli 'show info' | grep -m1 ^Version:) (image $VER) tune.bufsize=${BUFSIZE:-default} ==="

entries() { cli "show map" | sed -n 's|.*(/etc/haproxy/maps/ver.map).*entry_cnt=\([0-9]*\).*|\1|p'; }

probe() { # probe <nlines>  -- each body line is exactly 21 bytes
  python3 -c "
import sys
with open(sys.argv[2],'w') as f:
    for i in range(int(sys.argv[1])): f.write('/b%07d %s\n' % (i,'x'*10))
" "$1" "$OUT_DIR/pl5.txt"
  cli "clear map $VERM" >/dev/null
  { printf '@1 add map %s <<\n' "$VERM"; cat "$OUT_DIR/pl5.txt"; printf '\n'; } | $HAPCLI "$MSOCK" 20 >/dev/null 2>&1
  entries
}

say "sanity: 10-line payload lands? -> $(probe 10)"

say "bisect the payload cap on entries landed (range 10..40000 lines, 21 B each)"
lo=10; hi=40000
if [ "$(probe $hi)" = "$hi" ]; then
  echo "  even $hi lines landed; cap is above the search range"
else
  while [ $((hi-lo)) -gt 1 ]; do
    mid=$(( (lo+hi)/2 ))
    got=$(probe "$mid")
    if [ "$got" = "$mid" ]; then lo=$mid; else hi=$mid; fi
  done
  echo "  largest accepted:  $lo lines = $((lo*21)) payload bytes"
  echo "  smallest rejected: $hi lines = $((hi*21)) payload bytes"
fi
echo "  reference: tune.bufsize=${BUFSIZE:-16384}"

say "over-limit response text"
python3 -c "
import sys
with open(sys.argv[1],'w') as f:
    for i in range(40000): f.write('/b%07d %s\n' % (i,'x'*10))
" "$OUT_DIR/pl5.txt"
cli "clear map $VERM" >/dev/null
{ printf '@1 add map %s <<\n' "$VERM"; cat "$OUT_DIR/pl5.txt"; printf '\n'; } | $HAPCLI "$MSOCK" 20 2>&1 | grep -v '^$' | head -3 | sed 's/^/    /'
echo "    entries landed: $(entries)"
