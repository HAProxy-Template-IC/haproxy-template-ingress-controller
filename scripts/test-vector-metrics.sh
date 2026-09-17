#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
container=""
cleanup() {
    if [[ -n "$container" ]]; then
        docker rm -f "$container" >/dev/null
    fi
    rm -rf "$work"
}
trap cleanup EXIT

helm template haptic "$root/charts/haptic" --namespace haptic > "$work/chart.yaml"
"${CONTROLLER_BIN:-$root/bin/haptic}" validate --file "$work/chart.yaml" \
    --schema-dir "$root/tests/schemas" --test test-vector-omit-empty-log-fields \
    --dump-rendered > "$work/rendered.txt"
python3 "$root/scripts/tests/vector_metrics.py" "$work"
container=$(docker create --network none \
    "$(cat "$work/vector-image")" test /tmp/vector.yaml)
docker cp "$work/vector.yaml" "$container:/tmp/vector.yaml"
docker start --attach "$container"
[[ $(docker inspect -f '{{.State.ExitCode}}' "$container") == 0 ]]
