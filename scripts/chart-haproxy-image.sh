#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
series="${1:-$(yq -r '.haproxyVersion' charts/haptic/values.yaml)}"
if [[ ! "$series" =~ ^[0-9]+\.[0-9]+$ ]]; then
    echo "Invalid HAProxy series: $series" >&2
    exit 1
fi
patch="$(yq -r ".haproxyPatchVersions[\"$series\"]" charts/haptic/values.yaml)"
if [[ -z "$patch" || "$patch" == null ]]; then
    echo "HAProxy series $series has no patch in the Helm chart" >&2
    exit 1
fi
helm template haptic charts/haptic --namespace haptic \
    --set-string "haproxyVersion=$series" \
    --show-only templates/haproxy-deployment.yaml |
    yq -er '.spec.template.spec.containers[] | select(.name == "haproxy") | .image'
