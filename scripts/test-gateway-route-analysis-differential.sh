#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
project_root="$(cd "${script_dir}/.." && pwd)"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/haptic-gateway-route-analysis-diff.XXXXXX")"
baseline="${scratch}/head"

cleanup() {
    git -C "$project_root" worktree remove --force "$baseline" >/dev/null 2>&1 || true
    rm -rf "$scratch"
}
trap cleanup EXIT

baseline_rev="${HAPTIC_DIFFERENTIAL_BASELINE_REV:-01c67ed481fbda49891bc27189c49e18a38dee84}"
git -C "$project_root" worktree add --quiet --detach "$baseline" "$baseline_rev"

HAPTIC_GATEWAY_ROUTE_ANALYSIS_BASELINE="$baseline" \
    go test ./pkg/controller/renderer \
        -run '^TestGatewayRouteAnalysisColdMatchesDetachedHEAD$' \
        -count=1
