#!/usr/bin/env bash
# Print config/map sizes for a set of prepared config dirs.
set -euo pipefail
for d in "$@"; do
  cfg="$d/haproxy.cfg"
  [[ -f "$cfg" ]] || { echo "missing $cfg"; continue; }
  lines=$(wc -l < "$cfg")
  bytes=$(stat -c%s "$cfg")
  nmaps=$(ls "$d/maps" 2>/dev/null | wc -l)
  mbytes=$(du -sb "$d/maps" 2>/dev/null | cut -f1)
  ngen=$(ls "$d/general" 2>/dev/null | wc -l)
  ncerts=$(ls "$d/certs" 2>/dev/null | wc -l)
  echo "$d: cfg=${bytes}B ${lines}lines maps=${nmaps}(${mbytes}B) general=${ngen} certs=${ncerts}"
done
