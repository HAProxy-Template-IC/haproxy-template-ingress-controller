#!/bin/bash
# Remove every container this spike created.
for c in hapA hapA2 hapA3 hapA4 hapA5 hapB hapB2 hapC hapD hapD2 hapE hapE2 hapE3 hapE4 hapF hapG hapP hapP2 hapP3; do
  docker rm -f "$c" >/dev/null 2>&1
done
echo "remaining spike containers:"
docker ps -a --format '{{.Names}}' | grep -E '^hap(A|A2|A3|A4|A5|B|B2|C|D|D2|E|E2|E3|E4|F|G|P|P2|P3)$' || echo "  (none)"
