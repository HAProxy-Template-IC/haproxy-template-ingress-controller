#!/usr/bin/env bash
# Normalise volatile fields and diff the 3.4 vs 3.0 Section A transcripts.
cd "$(dirname "$0")/out" || exit 1
norm() {
  sed -e 's/0x[0-9a-f]\{8,\}/PTR/g' \
      -e 's/3\.[0-9]\.[0-9]*-[0-9a-f]*/VER/g' \
      -e 's/[0-9]\+\.[0-9]\{4\}s/TIME/g' \
      -e 's/^ *[0-9]\+ \[/ N [/' "$1"
}
norm A-3.4.log > /tmp/a34.norm
norm A-3.0.log > /tmp/a30.norm
diff -u /tmp/a34.norm /tmp/a30.norm
