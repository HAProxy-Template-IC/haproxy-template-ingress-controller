#!/usr/bin/env bash
# Validate the recommended pattern: one prepared generation, MANY payload
# chunks each under the 3.0 payload cap, one commit -- still atomic?
source "$(dirname "$0")/lib.sh"
VER="${1:-3.0}"
CHUNK="${2:-600}"     # lines per chunk (600 * 21 B = 12600 B, safe on 3.0)
TOTAL="${3:-3000}"
VERM=/etc/haproxy/maps/ver.map
ENVDIR="$SPIKE_DIR/mapenv6"
rm -rf "$ENVDIR"; mkdir -p "$ENVDIR"
cp -r "$SPIKE_DIR/mapenv/maps" "$ENVDIR/"
cp "$SPIKE_DIR/mapenv/haproxy.cfg" "$ENVDIR/"
trap stop_hap EXIT
start_hap "$VER" "$ENVDIR" || exit 1
echo "=== $(cli 'show info' | grep -m1 ^Version:) (image $VER) ==="
echo "=== $TOTAL entries in chunks of $CHUNK lines ($((CHUNK*21)) B) ==="

entries() { cli "show map" | sed -n 's|.*(/etc/haproxy/maps/ver.map).*entry_cnt=\([0-9]*\).*|\1|p'; }
gv() { curl -sS --max-time 2 "http://127.0.0.1:$HTTP_PORT$1" | sed -n 's/.*ver=\(\[[^]]*\]\).*/\1/p'; }

cli "clear map $VERM" >/dev/null
cli "add map $VERM /v OLD" >/dev/null
echo "-- baseline /v = $(gv /v)"

LOOPLOG="$OUT_DIR/chunk-atomicity-$VER.log"
: > "$LOOPLOG"
(
  end=$((SECONDS+18))
  while [ $SECONDS -lt $end ]; do
    curl -sS --max-time 2 "http://127.0.0.1:$HTTP_PORT/v" | sed -n 's/.*ver=\(\[[^]]*\]\).*/\1/p' >> "$LOOPLOG"
  done
) &
LOOPPID=$!
sleep 2

T0=$(date +%s.%N)
PREP=$(cli "prepare map $VERM")
V=$(echo "$PREP" | grep -oE '[0-9]+' | head -1)
echo "-- $PREP"
nchunks=0
i=0
while [ "$i" -lt "$TOTAL" ]; do
  python3 -c "
import sys
start=int(sys.argv[1]); n=int(sys.argv[2]); total=int(sys.argv[3])
with open(sys.argv[4],'w') as f:
    if start==0: f.write('/v CHUNKGEN\n')
    for k in range(start, min(start+n, total)):
        f.write('/c%07d %s\n' % (k,'x'*10))
" "$i" "$CHUNK" "$TOTAL" "$OUT_DIR/chunk.txt"
  out=$({ printf '@1 add map @%s %s <<\n' "$V" "$VERM"; cat "$OUT_DIR/chunk.txt"; printf '\n'; } | $HAPCLI "$MSOCK" 10 2>&1 | grep -v '^$' | head -1)
  if [ -n "$out" ]; then echo "   chunk at $i -> $out"; fi
  nchunks=$((nchunks+1))
  i=$((i+CHUNK))
done
T1=$(date +%s.%N)
echo "-- $nchunks chunks pushed; /v during the push: $(gv /v)"
cli "commit map @$V $VERM" | sed 's/^/   commit: /'
T2=$(date +%s.%N)
python3 -c "print('-- push %.3fs, commit %.3fs, TOTAL %.3fs'%($T1-$T0,$T2-$T1,$T2-$T0))"
echo "-- entry_cnt: $(entries) (expected $((TOTAL+1)))"
echo "-- probes: /v=$(gv /v) /c0000000=$(gv /c0000000) /c0001500=$(gv /c0001500) /c0002999=$(gv /c0002999)"

wait $LOOPPID
echo "-- values the loop saw:"
sort "$LOOPLOG" | uniq -c | sort -rn | sed 's/^/    /'
echo "-- transitions:"
uniq "$LOOPLOG" | sed 's/^/    /'
echo "-- samples: $(grep -c . "$LOOPLOG")"
