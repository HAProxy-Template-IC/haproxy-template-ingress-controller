#!/usr/bin/env bash
set -euo pipefail

image=registry.gitlab.com/haproxy-haptic/haptic/ci@sha256:b27b3629fe0597aa0e7f9222e02f3b95ca4edcbb7af12877fd7d34a833b9d3ad
mkdir -p build/gateway-conformance debug-logs
trap 'sudo chown -R "$(id -u):$(id -g)" build/gateway-conformance debug-logs' EXIT
image=$(python3 -m scripts.ci.github_toolchain "$image")
docker run --rm --network host \
    --volume /var/run/docker.sock:/var/run/docker.sock \
    --volume "$HOME/.docker:/root/.docker" \
    --volume "$PWD:$PWD" --workdir "$PWD" \
    --env GITHUB_REPOSITORY --env GITHUB_SHA --env GITHUB_REF_TYPE --env GITHUB_REF_NAME \
    --env GITHUB_RUN_ID --env GITHUB_RUN_ATTEMPT --env CONFORMANCE_SOURCE_SHA \
    --env CONFORMANCE_MODE --env KIND_NODE_IMAGE --entrypoint bash "$image" -euc '
        git config --global --add safe.directory "$PWD"
        make github-conformance-report
    '
