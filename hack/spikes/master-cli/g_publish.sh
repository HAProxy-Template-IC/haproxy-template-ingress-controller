#!/bin/bash
# What 'publish backend' / 'unpublish backend' actually gate, and whether
# 'be-removable' exists although 'wait -h' does not list it.
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-g.cfg" "$W/a.cfg"
VER="${1:-3.4}"
start hapG "$VER" a.cfg || exit 1
echo "############ $VER ############"
c() { cx "curl -s -o /dev/null -w '%{http_code}\n' -H 'x-be: $1' http://127.0.0.1:8080/"; }

hr "G1. traffic before / after 'publish backend'"
MC_T=10 mc "@@1
add backend pubX from be-http mode http; add server pubX/s1 127.0.0.1:9000 check init-state up; enable health pubX/s1; enable server pubX/s1; echo ==created"
echo "  curl before publish: $(c pubX)"
mc "@1 publish backend pubX"
echo "  curl after publish:  $(c pubX)"
mc "@1 unpublish backend pubX"
echo "  curl after unpublish: $(c pubX)"
mc "@1 publish backend pubX" | head -1
echo "  curl after re-publish: $(c pubX)"

hr "G2. does a FILE-defined backend need publishing? (filebe is file-defined with a real server)"
echo "  curl filebe: $(c filebe)"
mc "@1 unpublish backend filebe"
echo "  curl filebe after unpublish: $(c filebe)"
mc "@1 publish backend filebe"
echo "  curl filebe after re-publish: $(c filebe)"

hr "G3. 'wait -h' does not list be-removable — does it work anyway?"
mc "@1 wait -h" | grep -E 'removable'
echo "--- @1 wait 2s be-removable pubX (still published, has a server):"
MC_T=10 mc "@1 wait 2s be-removable pubX"
echo "--- @1 wait 2s be-removable filebe:"
MC_T=10 mc "@1 wait 2s be-removable filebe"
echo "--- @1 wait 2s bogus-condition pubX:"
MC_T=10 mc "@1 wait 2s bogus-condition pubX"

hr "G4. does 'show backend' distinguish published from unpublished?"
mc "@1 unpublish backend pubX"
mc "@1 show backend"
mc "@1 show stat" | awk -F, 'NR==1{next} $2=="BACKEND"{print "  "$1" status="$18}'

stop
