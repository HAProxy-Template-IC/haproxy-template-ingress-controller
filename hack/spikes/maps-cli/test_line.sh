#!/usr/bin/env bash
# Narrow sweep of the maximum single CLI command line, master relay vs worker.
# Bounded probe list (no bisect) so the run always terminates.
source "$(dirname "$0")/lib.sh"
VER="${1:-3.4}"
VERM=/etc/haproxy/maps/ver.map
ENVDIR="$SPIKE_DIR/mapenv5"
rm -rf "$ENVDIR"; mkdir -p "$ENVDIR"
cp -r "$SPIKE_DIR/mapenv/maps" "$ENVDIR/"
cp "$SPIKE_DIR/mapenv/haproxy.cfg" "$ENVDIR/"
trap stop_hap EXIT
start_hap "$VER" "$ENVDIR" || exit 1
echo "=== $(cli 'show info' | grep -m1 ^Version:) (image $VER), tune.bufsize=16384 ==="

# One command line of exactly <total> bytes including the trailing LF.
try() { # try <total> <sock> <prefix>
  python3 -c "
import sys
total=int(sys.argv[1]); pre=sys.argv[2]
line = pre + 'v'*(total-len(pre)-1) + '\n'
assert len(line)==total
sys.stdout.write(line)
" "$1" "$3" | $HAPCLI "$2" 5 2>&1 | grep -v '^$' | head -1 | head -c 70
}

say "master relay: '@1 add map <path> /k <value>'"
PRE="@1 add map $VERM /k "
for t in 15000 15300 15340 15350 15360 15370 15400 16000 16384; do
  cli "clear map $VERM" >/dev/null
  r=$(try "$t" "$MSOCK" "$PRE")
  n=$(cli "get map $VERM /k" | grep -c 'found=yes')
  printf '  total=%-6s landed=%s  resp=%q\n' "$t" "$n" "$r"
done

say "worker socket: 'add map <path> /k <value>'"
PREW="add map $VERM /k "
for t in 16000 16300 16340 16350 16360 16370 16380 16384 16400; do
  cli "clear map $VERM" >/dev/null
  r=$(try "$t" "$WSOCK" "$PREW")
  n=$(wcli "get map $VERM /k" | grep -c 'found=yes')
  printf '  total=%-6s landed=%s  resp=%q\n' "$t" "$n" "$r"
done
