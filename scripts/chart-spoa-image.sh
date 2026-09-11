#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
chart="${1:-charts/haptic}"
images="$(helm template haptic "$chart" --namespace haptic \
    --show-only templates/haproxy-deployment.yaml |
    yq -o=json -I=0 '[.spec.template.spec.containers[] | select(.name == "spoa-hub") | .image]')"
jq -er 'select(length == 1) | .[0] | select(type == "string" and length > 0)' <<< "$images" || {
    echo "Chart $chart has no unique SPOA image; enable spoaHub and configure its image" >&2
    exit 1
}
