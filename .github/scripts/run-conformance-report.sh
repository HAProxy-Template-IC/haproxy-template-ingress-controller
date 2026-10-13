#!/usr/bin/env bash
set -euo pipefail

image=registry.gitlab.com/haproxy-haptic/haptic/ci@sha256:b27b3629fe0597aa0e7f9222e02f3b95ca4edcbb7af12877fd7d34a833b9d3ad
case "${1:-report}" in
    report) CONFORMANCE_TARGET=github-conformance-report ;;
    startup) CONFORMANCE_TARGET=github-conformance-startup ;;
    *) echo "Unknown conformance operation" >&2; exit 1 ;;
esac
export CONFORMANCE_TARGET
mkdir -p build/gateway-conformance debug-logs/conformance-report
{
    lscpu
    free -b
    df -B1 . /var/lib/docker
} > debug-logs/conformance-report/host.txt
python3 .github/scripts/runner-metrics.py debug-logs/conformance-report/host-metrics.jsonl &
metrics_pid=$!
cleanup() {
    kill "$metrics_pid" 2>/dev/null || true
    wait "$metrics_pid" 2>/dev/null || true
    sudo chown -R "$(id -u):$(id -g)" build/gateway-conformance debug-logs
}
trap cleanup EXIT
image=$(python3 -m scripts.ci.github_toolchain "$image")
docker run --rm --network host \
    --volume /var/run/docker.sock:/var/run/docker.sock \
    --volume "$HOME/.docker:/root/.docker" \
    --volume "$PWD:$PWD" --workdir "$PWD" \
    --env GITHUB_REPOSITORY --env GITHUB_SHA --env GITHUB_REF_TYPE --env GITHUB_REF_NAME \
    --env GITHUB_RUN_ID --env GITHUB_RUN_ATTEMPT --env CONFORMANCE_SOURCE_SHA \
    --env CONFORMANCE_MODE --env CONFORMANCE_TARGET --env KIND_NODE_IMAGE --entrypoint bash "$image" -euc '
        git config --global --add safe.directory "$PWD"
        make "$CONFORMANCE_TARGET"
    '
