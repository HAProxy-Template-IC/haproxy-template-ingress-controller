#!/usr/bin/env bash
# Section A -- runtime map behaviour. Usage: test_a.sh <haproxy-version>
source "$(dirname "$0")/lib.sh"
VER="${1:-3.4}"
SPEC=/etc/haproxy/maps/spec.map
STR=/etc/haproxy/maps/str.map
REG=/etc/haproxy/maps/reg.map
BEG=/etc/haproxy/maps/beg.map
SUB=/etc/haproxy/maps/sub.map
DOM=/etc/haproxy/maps/dom.map
VERM=/etc/haproxy/maps/ver.map

trap stop_hap EXIT
start_hap "$VER" "$SPIKE_DIR/mapenv" || exit 1
echo "=== HAProxy $(cli 'show info' | grep -m1 ^Version:) / image tag $VER ==="

cnt() { cli "show map" | sed -n "s|^[0-9]* ($1).*entry_cnt=\([0-9]*\).*|\1|p"; }
gv()  { curl -sS "http://127.0.0.1:$HTTP_PORT$2" ${3:+-H "Host: $3"} | sed -n "s/.*$1=\(\[[^]]*\]\).*/\1/p"; }

# --------------------------------------------------------------------------
say "A1  line form: add map <path> <key> <value> with special characters"
add_line() { printf '@1 add map %s %s %s\n' "$SPEC" "$1" "$2" | send | sed 's/^/    resp: /'; }

echo "-- /L_space  value 'a b'"          ; add_line /L_space  'a b'
echo "-- /L_semi   value 'a;b'"          ; add_line /L_semi   'a;b'
echo "-- /L_pct    value 'a%b'"          ; add_line /L_pct    'a%b'
echo "-- /L_plus   value 'a+b'"          ; add_line /L_plus   'a+b'
echo "-- /L_pipe   value 'a|b'"          ; add_line /L_pipe   'a|b'
echo "-- /L_dq     value 'a\"b'"         ; add_line /L_dq     'a"b'
echo "-- /L_bs     value 'a\\b'"         ; add_line /L_bs     'a\b'
echo "-- /L_bs2    value 'a\\\\b' (doubled backslash)" ; add_line /L_bs2 'a\\b'
echo "-- /L_all    value 'a b;c%d+e|f\"g\\h'" ; add_line /L_all 'a b;c%d+e|f"g\h'
echo "-- /L_hash   value 'a#b'"          ; add_line /L_hash   'a#b'
echo "-- /L_tab    value 'a<TAB>b'"      ; add_line /L_tab    "$(printf 'a\tb')"
echo "-- /L_esc_sp value 'a\\ b' (backslash-escaped space)" ; add_line /L_esc_sp 'a\ b'
echo "-- /L_quoted value '\"a b\"' (double-quoted)" ; add_line /L_quoted '"a b"'

say "A1  show map after line-form adds  (cat -A: \$=EOL, ^I=TAB)"
cli "show map $SPEC" | cat -A

# --------------------------------------------------------------------------
say "A2  payload form: add map <path> << ... with the same values"
padd() { pay "add map $SPEC" "$1" | sed 's/^/    resp: /'; }
padd '/P_space a b'
padd '/P_semi a;b'
padd '/P_pct a%b'
padd '/P_plus a+b'
padd '/P_pipe a|b'
padd '/P_dq a"b'
padd '/P_bs a\b'
padd '/P_all a b;c%d+e|f"g\h'
padd '/P_hash a#b'
padd "$(printf '/P_tab a\tb')"
padd '/P_trailsp trailing   '
padd '/P_esc_sp a\ b'
echo "-- one payload, many lines at once:"
pay "add map $SPEC" "$(printf '/P_multi1 m one\n/P_multi2 m two\n/P_multi3 m;three')" | sed 's/^/    resp: /'

say "A2  show map after payload-form adds  (cat -A)"
cli "show map $SPEC" | cat -A

# --------------------------------------------------------------------------
say "A3  read back through a request (curl)"
for k in /L_space /L_semi /L_pct /L_plus /L_pipe /L_dq /L_bs /L_bs2 /L_all /L_hash /L_tab /L_esc_sp /L_quoted \
         /P_space /P_semi /P_pct /P_plus /P_pipe /P_dq /P_bs /P_all /P_hash /P_tab /P_trailsp /P_esc_sp \
         /P_multi1 /P_multi2 /P_multi3 ; do
  printf '  %-12s body: %-24s hdr: %s\n' "$k" "$(gv spec "$k")" \
    "$(curl -sS -D- -o /dev/null "http://127.0.0.1:$HTTP_PORT$k" | sed -n 's/^x-spec: //Ip' | tr -d '\r')"
done

say "A3b payload form: is a ';' inside the value part of the value, or a 2nd command?"
echo "-- payload line: '/P_semicmd VAL; add map $SPEC /P_injected INJ'"
pay "add map $SPEC" "/P_semicmd VAL; add map $SPEC /P_injected INJ" | sed 's/^/    resp: /'
echo "-- get /P_semicmd  -> $(cli "get map $SPEC /P_semicmd")"
echo "-- get /P_injected -> $(cli "get map $SPEC /P_injected")"

say "A3c line form: does ';' split the command? 'add map <p> /L_semicmd VAL; show info'"
printf '@1 add map %s /L_semicmd VAL; show info\n' "$SPEC" | send | head -8 | sed 's/^/    /'
echo "-- get /L_semicmd -> $(cli "get map $SPEC /L_semicmd")"

say "A3d payload form: can a payload LINE inject a second map entry via a newline? (it cannot -- newline IS the separator)"
echo "-- can the KEY contain a space in the payload form? 'a b VALUE':"
pay "add map $SPEC" "a b VALUE" | sed 's/^/    resp: /'
echo "-- get 'a' -> $(cli "get map $SPEC a")"

# --------------------------------------------------------------------------
say "A4  del map with DUPLICATE keys (str.map has /dup twice in the file)"
echo "-- before:"
cli "show map $STR"
echo "-- request lookup /dup: $(gv str /dup)"
echo "-- get map $STR /dup -> $(cli "get map $STR /dup")"
echo "-- add a THIRD /dup at runtime, then show:"
cli "add map $STR /dup dupC"
cli "show map $STR"
echo "-- del map $STR /dup   (by KEY):"
cli "del map $STR /dup"
echo "-- after one del:"
cli "show map $STR"
echo "-- request lookup /dup now: $(gv str /dup)"
echo "-- del map $STR /dup again:"
cli "del map $STR /dup"
cli "show map $STR"
echo "-- del map $STR /dup a third time:"
cli "del map $STR /dup"
cli "show map $STR"
echo "-- request lookup /dup now: $(gv str /dup)"

say "A4b del map by reference id: 'del map <path> #<id>'"
cli "add map $STR /dup2 d2A"
cli "add map $STR /dup2 d2B"
cli "show map $STR" | grep dup2
ID=$(cli "show map $STR" | awk '/ \/dup2 d2A$/{print $1}')
echo "   deleting id $ID  ->  del map $STR #$ID"
cli "del map $STR #$ID"
cli "show map $STR" | grep dup2
echo "   (bare id without '#':)"
ID2=$(cli "show map $STR" | awk '/ \/dup2 d2B$/{print $1}')
cli "del map $STR $ID2"

# --------------------------------------------------------------------------
say "A5  ORDER: file order vs runtime-append order; tree vs list index"

echo "--- map_reg: file order = '^/a/b.* B' then '^/a.* A'"
cli "show map $REG"
echo "  get map (shows idx=): $(cli "get map $REG /a/b/c")"
echo "  GET /a/b/c  -> $(gv reg /a/b/c)"
echo "  GET /a/x    -> $(gv reg /a/x)"
echo "  + add map $REG '^/a/b/c.*' C   (MORE specific, appended LAST)"
cli "add map $REG ^/a/b/c.* C"
cli "show map $REG"
echo "  GET /a/b/c  -> $(gv reg /a/b/c)   [B => file entry still wins; C => append wins]"
echo "  + add map $REG '^/zz.*' Z  (non-overlapping, sanity check that adds work)"
cli "add map $REG ^/zz.* Z"
echo "  GET /zz1    -> $(gv reg /zz1)"

echo
echo "--- map_beg: file order = '/p/q BQ' then '/p P'"
cli "show map $BEG"
echo "  get map (idx=): $(cli "get map $BEG /p/q/r")"
echo "  GET /p/q/r  -> $(gv beg /p/q/r)"
echo "  + add map $BEG /p/q/r RUNTIME  (LONGER prefix, appended last)"
cli "add map $BEG /p/q/r RUNTIME"
cli "show map $BEG"
echo "  GET /p/q/r  -> $(gv beg /p/q/r)"
echo "  GET /p/q/rZ -> $(gv beg /p/q/rZ)"
echo "  GET /p/q/z  -> $(gv beg /p/q/z)"
echo "  + add map $BEG /p SHORTRUNTIME  (DUPLICATE of the file's '/p', appended last)"
cli "add map $BEG /p SHORTRUNTIME"
cli "show map $BEG"
echo "  GET /pZ     -> $(gv beg /pZ)   [P => first insert wins for equal-length prefixes]"
echo "  GET /p/q/r  -> $(gv beg /p/q/r)"

echo
echo "--- map_sub: file order = 'foobar FOOBAR' then 'foo FOO'"
cli "show map $SUB"
echo "  get map (idx=): $(cli "get map $SUB /xfoobarx")"
echo "  GET /xfoobarx -> $(gv sub /xfoobarx)"
echo "  + add map $SUB oba RUNTIMESUB (appended last)"
cli "add map $SUB oba RUNTIMESUB"
cli "show map $SUB"
echo "  GET /xfoobarx -> $(gv sub /xfoobarx)"
echo "  GET /xobax    -> $(gv sub /xobax)"

echo
echo "--- map_dom: file order = 'sub.example.com SUB' then 'example.com EX'"
cli "show map $DOM"
echo "  get map (idx=): $(cli "get map $DOM a.sub.example.com")"
echo "  Host a.sub.example.com -> $(gv dom / a.sub.example.com)"
echo "  + add map $DOM a.sub.example.com RUNTIMEDOM (MORE specific, appended last)"
cli "add map $DOM a.sub.example.com RUNTIMEDOM"
cli "show map $DOM"
echo "  Host a.sub.example.com -> $(gv dom / a.sub.example.com)"
echo "  + add map $DOM example.com RUNTIMEEX (duplicate of file entry, appended last)"
cli "add map $DOM example.com RUNTIMEEX"
echo "  Host www.example.com   -> $(gv dom / www.example.com)"

echo
echo "--- map_str: duplicate key -- which value wins?"
cli "add map $STR /ord first"
cli "add map $STR /ord second"
cli "show map $STR" | grep '/ord'
echo "  get map (idx=): $(cli "get map $STR /ord")"
echo "  GET /ord -> $(gv str /ord)"

# --------------------------------------------------------------------------
say "A6  prepare / commit / clear map versions"
echo "-- show map (map list) BEFORE, note curr_ver/next_ver/entry_cnt:"
cli "show map"
echo
echo "-- show map $VERM:"
cli "show map $VERM"
echo "-- prepare map $VERM:"
PREP=$(cli "prepare map $VERM"); echo "   $PREP"
VNUM=$(echo "$PREP" | grep -oE '[0-9]+' | head -1)
echo "-- add map @$VNUM $VERM << (payload, 3 entries):"
pay "add map @$VNUM $VERM" "$(printf '/v NEW\n/v2 NEW2\n/v3 NEW3')" | sed 's/^/    resp: /'
echo "-- show map $VERM while UNCOMMITTED:"
cli "show map $VERM"
echo "-- show map @$VNUM $VERM (ask explicitly for the pending version):"
cli "show map @$VNUM $VERM"
echo "-- map list line while uncommitted:"
cli "show map" | grep ver.map
echo "-- request lookup /v while uncommitted: $(gv ver /v)"
echo "-- commit map @$VNUM $VERM:"
cli "commit map @$VNUM $VERM"
echo "-- show map $VERM after commit:"
cli "show map $VERM"
echo "-- map list line after commit:"
cli "show map" | grep ver.map
echo "-- request /v: $(gv ver /v)  /v2: $(gv ver /v2)"

say "A6b clear map @<ver> on a PREPARED-but-uncommitted version"
PREP2=$(cli "prepare map $VERM"); echo "   $PREP2"
V2=$(echo "$PREP2" | grep -oE '[0-9]+' | head -1)
pay "add map @$V2 $VERM" "$(printf '/v ABANDONED\n/zz ABANDONED2')" | sed 's/^/    resp: /'
echo "-- show map @$V2 $VERM before clear:"
cli "show map @$V2 $VERM"
echo "-- map list line: $(cli "show map" | grep ver.map)"
echo "-- clear map @$V2 $VERM  ->"
cli "clear map @$V2 $VERM"
echo "-- show map @$V2 $VERM after clear:"
cli "show map @$V2 $VERM"
echo "-- live map after clear (must be untouched):"
cli "show map $VERM"
echo "-- request /v: $(gv ver /v)"
echo "-- now commit the CLEARED version @$V2:"
cli "commit map @$V2 $VERM"
echo "-- live map after committing the cleared version:"
cli "show map $VERM"
echo "-- request /v: $(gv ver /v)"
echo "-- map list line: $(cli "show map" | grep ver.map)"

say "A6c commit of a version that was never prepared / already committed"
echo "-- commit map @$V2 $VERM again: $(cli "commit map @$V2 $VERM")"
echo "-- commit map @9999 $VERM: $(cli "commit map @9999 $VERM")"
echo "-- add map @9999 $VERM payload:"
pay "add map @9999 $VERM" "/x X" | sed 's/^/    resp: /'

say "A6d clear map <path> WITHOUT a version"
cli "add map $VERM /v OLD" >/dev/null
cli "add map $VERM /wipeme W" >/dev/null
cli "show map $VERM"
cli "clear map $VERM"
echo "-- after 'clear map <path>':"
cli "show map $VERM"
echo "-- request /v: $(gv ver /v)"
cli "add map $VERM /v OLD" >/dev/null

# --------------------------------------------------------------------------
say "A6e ATOMICITY + COST: prepare/add(3000)/commit under a tight curl loop"
python3 - "$OUT_DIR/big3000.txt" <<'PY'
import sys
with open(sys.argv[1], 'w') as f:
    f.write("/v NEWGEN\n")
    for i in range(2999):
        f.write("/k%d val%d\n" % (i, i))
PY
echo "-- payload file: $(wc -l < "$OUT_DIR/big3000.txt") lines, $(wc -c < "$OUT_DIR/big3000.txt") bytes"

LOOPLOG="$OUT_DIR/atomicity-$VER.log"
: > "$LOOPLOG"
(
  end=$((SECONDS+20))
  while [ $SECONDS -lt $end ]; do
    curl -sS --max-time 2 "http://127.0.0.1:$HTTP_PORT/v" | sed -n 's/.* ver=\(\[[^]]*\]\).*/\1/p' >> "$LOOPLOG"
  done
) &
LOOPPID=$!
sleep 2

T0=$(date +%s.%N)
PREP3=$(cli "prepare map $VERM")
V3=$(echo "$PREP3" | grep -oE '[0-9]+' | head -1)
T1=$(date +%s.%N)
payfile "add map @$V3 $VERM" "$OUT_DIR/big3000.txt" > "$OUT_DIR/bigadd-$VER.out" 2>&1
T2=$(date +%s.%N)
cli "commit map @$V3 $VERM" | sed 's/^/    commit resp: /'
T3=$(date +%s.%N)
python3 -c "print('  prepare:           %.4fs'%($T1-$T0)); print('  add(3000 payload): %.4fs'%($T2-$T1)); print('  commit:            %.4fs'%($T3-$T2)); print('  TOTAL:             %.4fs'%($T3-$T0))"
echo "-- payload add response bytes: $(wc -c < "$OUT_DIR/bigadd-$VER.out"); first lines:"
head -4 "$OUT_DIR/bigadd-$VER.out" | sed 's/^/    /'
echo "-- entry_cnt after commit (from show map list): $(cli "show map" | grep ver.map | grep -oE 'entry_cnt=[0-9]+')"
echo "-- 'show map <path>' line count: $(cli "show map $VERM" | grep -c .)"
echo "-- probe first/middle/last keys: /v=$(gv ver /v) /k1500=$(gv ver /k1500) /k2998=$(gv ver /k2998)"

wait $LOOPPID
echo "-- values the curl loop saw:"
sort "$LOOPLOG" | uniq -c | sort -rn | sed 's/^/    /'
echo "-- transitions (consecutive-unique):"
uniq "$LOOPLOG" | sed 's/^/    /'
echo "-- samples: $(grep -c . "$LOOPLOG")  empty/failed samples: $(grep -c '^$' "$LOOPLOG")"

say "A6f cost comparison: same 3000-entry replace WITHOUT versioning (clear + add)"
python3 - "$OUT_DIR/big3000b.txt" <<'PY'
import sys
with open(sys.argv[1], 'w') as f:
    f.write("/v NAIVEGEN\n")
    for i in range(2999):
        f.write("/n%d val%d\n" % (i, i))
PY
T0=$(date +%s.%N)
cli "clear map $VERM" >/dev/null
payfile "add map $VERM" "$OUT_DIR/big3000b.txt" >/dev/null 2>&1
T1=$(date +%s.%N)
python3 -c "print('  clear+add TOTAL:   %.4fs'%($T1-$T0))"
echo "-- entry_cnt: $(cli "show map" | grep ver.map | grep -oE 'entry_cnt=[0-9]+')"
echo "-- 30k-entry versioned replace timing:"
python3 - "$OUT_DIR/big30000.txt" <<'PY'
import sys
with open(sys.argv[1], 'w') as f:
    f.write("/v BIGGEN\n")
    for i in range(29999):
        f.write("/m%d val%d\n" % (i, i))
PY
echo "   payload bytes: $(wc -c < "$OUT_DIR/big30000.txt")"
T0=$(date +%s.%N)
PREP4=$(cli "prepare map $VERM"); V4=$(echo "$PREP4" | grep -oE '[0-9]+' | head -1)
payfile "add map @$V4 $VERM" "$OUT_DIR/big30000.txt" >/dev/null 2>&1
cli "commit map @$V4 $VERM" >/dev/null
T1=$(date +%s.%N)
python3 -c "print('   TOTAL 30000: %.4fs'%($T1-$T0))"
echo "   entry_cnt: $(cli "show map" | grep ver.map | grep -oE 'entry_cnt=[0-9]+')"
echo "   probe /v=$(gv ver /v) /m29998=$(gv ver /m29998)"

# --------------------------------------------------------------------------
say "A7  set map: in-place value change -- does the entry keep its id/position?"
cli "clear map $STR" >/dev/null
cli "add map $STR /a AAA" >/dev/null
cli "add map $STR /b BBB" >/dev/null
cli "add map $STR /c CCC" >/dev/null
echo "-- before:"
cli "show map $STR"
BID=$(cli "show map $STR" | awk '/ \/b /{print $1}')
echo "-- set map $STR /b BBB-CHANGED   (by key)"
cli "set map $STR /b BBB-CHANGED"
cli "show map $STR"
echo "   (/b id before the set: $BID)"
echo "-- set map $STR #$BID BBB-BYID   (by reference id)"
cli "set map $STR #$BID BBB-BYID"
cli "show map $STR"
echo "-- set map on a MISSING key: $(cli "set map $STR /nope X")"
echo "-- set map value with a space: 'x y'"
cli "set map $STR /a 'x y'"
cli "show map $STR" | cat -A
echo "-- set map via PAYLOAD form:"
pay "set map $STR" "/a x y;z%w" | sed 's/^/    resp: /'
cli "show map $STR" | cat -A
echo "-- request /a: $(gv str /a)"
echo "-- set map on a DUPLICATE key: which of the two changes?"
cli "add map $STR /d D1" >/dev/null
cli "add map $STR /d D2" >/dev/null
cli "show map $STR" | grep ' /d '
cli "set map $STR /d DSET"
cli "show map $STR" | grep ' /d '
echo "-- request /d: $(gv str /d)"

say "A-extra  'show map' with no argument"
cli "show map"
