#!/usr/bin/env bash
cd "$(dirname "$0")" || exit 1
for v in 3.4 3.0; do
  bash test_a.sh  "$v" > "out/A-$v.log"  2>&1; echo "A-$v=$?"
  bash test_b.sh  "$v" > "out/B-$v.log"  2>&1; echo "B-$v=$?"
  bash test_line.sh "$v" > "out/LINE-$v.log" 2>&1; echo "LINE-$v=$?"
  bash test_b3.sh  "$v" > "out/B3-$v.log"   2>&1; echo "B3-$v=$?"
done
echo MAPS_DONE
