#!/usr/bin/env bash
# validate --dump-rendered omits the fileRegistry "crt-list" category, so the
# ssl library's certificate-list.txt is missing from the reconstructed tree.
# Recreate the single default-certificate line it contains.
set -uo pipefail
for d in /tmp/rc/*/; do
  cfg="$d/haproxy.cfg"
  [[ -f "$cfg" ]] || continue
  grep -q "crt-list" "$cfg" || continue
  target=$(grep -o "crt-list [^ ]*certificate-list.txt" "$cfg" | head -1 | awk '{print $2}')
  [[ -n "$target" ]] || continue
  mkdir -p "$(dirname "$target")"
  echo "default.pem [ocsp-update on]" > "$target"
  echo "wrote $target"
done
