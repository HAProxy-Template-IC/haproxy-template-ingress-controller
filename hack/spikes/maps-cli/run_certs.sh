#!/usr/bin/env bash
cd "$(dirname "$0")" || exit 1
for v in 3.4 3.0; do
  bash test_c.sh "$v" > "out/C-$v.log" 2>&1; echo "C-$v=$?"
done
echo CERTS_DONE
