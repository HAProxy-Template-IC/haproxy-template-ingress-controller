#!/usr/bin/env bash
# Which master-CLI form carries a PAYLOAD command to the worker, per version?
source "$(dirname "$0")/lib.sh"
VER="${1:-3.0}"
VERM=/etc/haproxy/maps/ver.map
trap stop_hap EXIT
start_hap "$VER" "$SPIKE_DIR/mapenv" || exit 1
echo "=== $(cli 'show info' | grep -m1 ^Version:) (image $VER) ==="

entries() { cli "show map $VERM" | grep -c .; }
clr() { cli "clear map $VERM" >/dev/null; }

say "master CLI help (what prefixes exist)"
printf 'help\n' | send | sed 's/^/    /'

try() { # try <label> <raw-bytes>
  clr
  local out
  out=$(printf '%b' "$2" | send 2>&1)
  printf '  %-34s entries=%-4s resp=%q\n' "$1" "$(entries)" "$(echo "$out" | grep -v '^$' | head -2 | tr '\n' '|' | head -c 130)"
}

say "payload delivery attempts"
try "@@1 session"          "@@1\nadd map $VERM <<\n/a A\n\n"
try "@1 <cmd> << prefix"   "@1 add map $VERM <<\n/a A\n\n"
try "@1 alone then cmd"    "@1\nadd map $VERM <<\n/a A\n\n"
try "@1 alone; cmd on next" "@1\nadd map $VERM /a A\n"
try "worker socket direct" ""
clr
printf 'add map %s <<\n/a A\n\n' "$VERM" | wsend >/dev/null
printf '  %-34s entries=%-4s\n' "worker socket direct" "$(entries)"

say "does a plain '@1' line switch the session? follow it with two commands"
printf '@1\nshow version\nshow version\n' | send | sed 's/^/    /' | head -8

say "prompt/interactive mode"
printf '@1\nprompt\nshow version\n' | send | head -8 | sed 's/^/    /'
