#!/usr/bin/env bash
set -euo pipefail

image=registry.gitlab.com/haproxy-haptic/haptic/ci@sha256:b27b3629fe0597aa0e7f9222e02f3b95ca4edcbb7af12877fd7d34a833b9d3ad
mkdir -p .publication
trap 'sudo chown -R "$(id -u):$(id -g)" .publication' EXIT
image=$(python3 -m scripts.ci.github_toolchain "$image")
docker run --rm --network host \
    --volume /var/run/docker.sock:/var/run/docker.sock \
    --volume "$HOME/.docker:/root/.docker" \
    --volume "$PWD:$PWD" --workdir "$PWD" \
    --env GITHUB_REPOSITORY --env PUBLICATION_PHASE --entrypoint bash "$image" -euc '
        git config --global --add safe.directory "$PWD"
        docker buildx create --driver docker-container --use --name snapshot-builder --bootstrap
        make publish-github-snapshot PUBLICATION_PHASE="$PUBLICATION_PHASE"
    '
