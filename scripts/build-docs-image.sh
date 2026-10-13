#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
declare -A pins
while IFS=$'\t' read -r name value; do
    pins["$name"]="$value"
done < <(yq -r '.variables | to_entries[] | [.key, .value] | @tsv' .gitlab/ci/build-pins.yml)
build_hash=$(scripts/ci-image-input-hash.sh)
args=(build --tag "haptic-docs:$build_hash" --label "haptic.ci.build-input-hash=$build_hash")
for pin in $(awk '/^ARG / { split($2, value, "="); print value[1] }' .gitlab/ci/images/python-docs/Dockerfile); do
    if [[ -z "${pins[$pin]:-}" ]]; then
        printf 'Missing docs image build pin %s\n' "$pin" >&2
        exit 1
    fi
    args+=(--build-arg "$pin=${pins[$pin]}")
done
exec docker "${args[@]}" .gitlab/ci/images/python-docs
