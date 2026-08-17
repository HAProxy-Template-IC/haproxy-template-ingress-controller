#!/usr/bin/env bash
# Dump the rendered artifacts (haproxy.cfg, maps, files, certs) for one benchmark
# test, so their sizes can be measured and haproxy -c timed against them.
set -euo pipefail
SPIKE="${SPIKE:-$(cd "$(dirname "$0")" && pwd)}"
cd "$SPIKE/repo"
kind="$1"; n="$2"
mkdir -p "$SPIKE/raw"
./bin/haptic validate \
  --file "$SPIKE/configs/${kind}.yaml" \
  --test "benchmark-${kind}-${n}" \
  --schema-dir "$SPIKE/repo/tests/schemas" \
  --dump-rendered \
  > "$SPIKE/raw/dump-${kind}-${n}.txt" 2>&1 || echo "validate exit $?"
head -40 "$SPIKE/raw/dump-${kind}-${n}.txt"
ls -la "$SPIKE/raw/dump-${kind}-${n}.txt"
