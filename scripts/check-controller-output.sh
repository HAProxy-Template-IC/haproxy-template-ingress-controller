#!/usr/bin/env bash
set -euo pipefail

cluster="${HAPTIC_E2E_CLUSTER_NAME:-haptic-e2e}"
namespace="${CTRL_NAMESPACE:-haptic}"
output="${CONTROLLER_OUTPUT_DIR:-debug-logs/controller-output}"
mkdir -p "$output"

kubectl --context "kind-$cluster" -n "$namespace" get pods \
  -l app.kubernetes.io/component=controller -o json > "$output/pods.json"
python3 - "$output/pods.json" <<'PY'
import json, sys
if not json.load(open(sys.argv[1]))["items"]:
    sys.exit("No controller pods found; output consistency was not checked.")
PY

nodes="$(kind get nodes --name "$cluster")"
[ -n "$nodes" ] || { echo "No cluster nodes found; output consistency was not checked." >&2; exit 1; }
: > "$output/controller.log"
for node in $nodes; do
  docker exec "$node" sh -eu -c '
    for directory in /var/log/pods/"$1"_*/controller; do
      [ -d "$directory" ] || continue
      for log in "$directory"/*.log*; do
        [ -f "$log" ] || continue
        printf "=== %s ===\n" "$log" >&2
        case "$log" in
          *.gz) gzip -cd "$log" ;;
          *) cat "$log" ;;
        esac
      done
    done
  ' sh "$namespace" >> "$output/controller.log" 2>> "$output/sources.log"
done

[ -s "$output/controller.log" ] || { echo "Controller logs are empty; output consistency was not checked." >&2; exit 1; }
if grep -E 'Rendered output rejected|content differs from its plan file|ArtifactContentMismatch' "$output/controller.log"; then
  echo "Controller rejected inconsistent output; inspect $output/controller.log." >&2
  exit 1
fi
echo "Controller output consistency: no rejection in the retained cluster logs."
