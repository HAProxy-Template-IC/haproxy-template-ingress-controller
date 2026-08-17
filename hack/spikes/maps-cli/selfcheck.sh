#!/usr/bin/env bash
# Parse-check every script in the spike so a stale edit cannot ship broken.
cd "$(dirname "$0")" || exit 1
rc=0
for f in *.sh; do
  bash -n "$f" || { echo "SYNTAX ERROR in $f"; rc=1; }
done
python3 -c "import ast,sys; ast.parse(open('hapcli.py').read())" \
  || { echo "SYNTAX ERROR in hapcli.py"; rc=1; }
[ $rc -eq 0 ] && echo "all scripts parse OK"
exit $rc
