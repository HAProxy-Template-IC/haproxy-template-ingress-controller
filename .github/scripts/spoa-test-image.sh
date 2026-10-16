#!/usr/bin/env bash
set -euo pipefail

case "${1:?usage: spoa-test-image.sh select|build}" in
    select) yq -i '.spoaHub.image.repository = "haptic-spoa" | .spoaHub.image.tag = "test"' charts/haptic/values.yaml ;;
    build)
        image="$(bash scripts/chart-spoa-image.sh)"
        make spoa-hub-image SPOA_DEV_IMAGE="$image" SPOA_DEV_PLATFORM="linux/$(docker version --format '{{.Server.Arch}}')"
        docker image inspect "$image" > .pilot/spoa-image.json
        ;;
    *) exit 2 ;;
esac
