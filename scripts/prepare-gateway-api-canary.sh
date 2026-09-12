#!/usr/bin/env bash
set -euo pipefail

module_revision() {
    local metadata
    metadata="$(go list -mod=mod -m -json "$1")"
    jq -er '.Origin.Hash | select(test("^[0-9a-f]{40}$"))' <<<"$metadata"
}

prepare_modules() {
    local revision="$1" module metadata expected actual index
    if [[ ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
        echo "Gateway API canary needs an exact Git commit." >&2
        return 1
    fi
    local modules=(sigs.k8s.io/gateway-api sigs.k8s.io/gateway-api/conformance)
    local versions=()
    for module in "${modules[@]}"; do
        metadata="$(go list -mod=mod -m -json "$module@$revision")"
        if [[ "$(jq -er '.Origin.Hash' <<<"$metadata")" != "$revision" ]]; then
            echo "Gateway API module $module doesn't resolve to $revision." >&2
            return 1
        fi
        expected="$(jq -er '.Version | select(length > 0)' <<<"$metadata")"
        versions+=("$expected")
    done
    go get "${modules[0]}@$revision" "${modules[1]}@$revision" >&2
    go mod tidy >&2
    for index in "${!modules[@]}"; do
        module="${modules[$index]}"
        expected="${versions[$index]}"
        actual="$(go list -mod=mod -m -f '{{.Version}}' "$module")"
        if [[ "$actual" != "$expected" ]]; then
            echo "Gateway API module $module changed to $actual; expected $expected. Fix its imports before running the canary." >&2
            return 1
        fi
    done
}

case "${1:-}" in
    resolve)
        [[ $# == 1 ]] || exit 1
        module_revision sigs.k8s.io/gateway-api@main
        ;;
    prepare)
        [[ $# == 2 ]] || exit 1
        prepare_modules "$2"
        ;;
    *)
        echo "Usage: prepare-gateway-api-canary.sh resolve | prepare COMMIT" >&2
        exit 1
        ;;
esac
