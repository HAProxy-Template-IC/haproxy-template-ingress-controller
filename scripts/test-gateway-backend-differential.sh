#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
project_root="$(cd "${script_dir}/.." && pwd)"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/haptic-gateway-backend-diff.XXXXXX")"
baseline="${scratch}/head"

cleanup() {
    git -C "$project_root" worktree remove --force "$baseline" >/dev/null 2>&1 || true
    rm -rf "$scratch"
}
trap cleanup EXIT

baseline_rev="${HAPTIC_DIFFERENTIAL_BASELINE_REV:-01c67ed481fbda49891bc27189c49e18a38dee84}"
git -C "$project_root" worktree add --quiet --detach "$baseline" "$baseline_rev"
baseline_generator="${baseline}/charts/haptic/charts/gateway/30-backends.yaml"

HAPTIC_GATEWAY_BACKEND_BASELINE="$baseline_generator" \
    go test ./pkg/controller/renderer \
        -run '^TestGatewayBackendChartColdMatchesDetachedHEADGenerator$' \
        -count=1
