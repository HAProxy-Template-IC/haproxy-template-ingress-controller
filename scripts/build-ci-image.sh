#!/usr/bin/env bash

set -euo pipefail

cd "$(dirname "$0")/.."
./scripts/check-image-pins.sh

declare -A ci_build_pins
while IFS=$'\t' read -r name value; do
    ci_build_pins["$name"]="$value"
done < <(yq -r '.variables | to_entries[] | [.key, .value] | @tsv' .gitlab/ci/build-pins.yml)

haproxy_pin="HAPROXY_IMAGE_${HAPROXY_VERSION//./}"
ci_build_pins[HAPROXY_IMAGE]="${ci_build_pins[$haproxy_pin]:-}"
build_hash="$(scripts/ci-image-input-hash.sh)"
image="haptic-ci:${build_hash}-hp${HAPROXY_VERSION}"
dockerfile=.gitlab/ci/images/ci/Dockerfile
args=(build --file "$dockerfile" --tag "$image")
for pin in $(awk '/^ARG / { split($2, value, "="); print value[1] }' "$dockerfile"); do
    value="${ci_build_pins[$pin]:-}"
    if [[ -z "$value" ]]; then
        echo "Missing CI build pin $pin for HAProxy $HAPROXY_VERSION" >&2
        exit 1
    fi
    args+=(--build-arg "$pin=$value")
done
args+=(--label "haptic.ci.build-input-hash=$build_hash")
args+=(--label "haptic.ci.haproxy-image=${ci_build_pins[HAPROXY_IMAGE]}")
exec docker "${args[@]}" .gitlab/ci/images/ci/
