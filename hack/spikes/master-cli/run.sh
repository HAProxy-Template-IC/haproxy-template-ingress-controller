#!/bin/bash
# run.sh <script.sh> <version> [<out-name>]  -> runs the spike script, tees to out/<name>.txt
set -u
D="$(cd "$(dirname "$0")" && pwd)"
s="$1"; v="${2:-3.4}"; n="${3:-$(basename "$s" .sh)-$v}"
mkdir -p "$D/out"
bash "$D/$s" "$v" > "$D/out/$n.txt" 2>&1
echo "exit=$? -> $D/out/$n.txt ($(wc -l < "$D/out/$n.txt") lines)"
