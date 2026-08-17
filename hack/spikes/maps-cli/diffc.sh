#!/usr/bin/env bash
# Normalise volatile fields and diff the 3.4 vs 3.0 Section C transcripts.
cd "$(dirname "$0")/out" || exit 1
norm() {
  sed -e 's/[0-9A-F]\{40\}/SERIAL/g' \
      -e 's/3\.[0-9]\.[0-9]*-[0-9a-f]*/VER/g' \
      -e 's/[A-Z][a-z][a-z] *[0-9]* [0-9:]* 20[0-9][0-9] GMT/DATE/g' "$1"
}
norm C-3.4.log > /tmp/c34.norm
norm C-3.0.log > /tmp/c30.norm
diff -u /tmp/c34.norm /tmp/c30.norm
