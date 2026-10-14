#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
phase="${1:?usage: publish-github-snapshot.sh spoa|controller|chart}"
[[ "$phase" == spoa || "$phase" == controller || "$phase" == chart ]] || exit 2
python3 -m scripts.ci.github_snapshot --output .publication/snapshot-plan.json
repository="$(jq -er .repository .publication/snapshot-plan.json)"
sha="$(jq -er .sha .publication/snapshot-plan.json)"
app_version="$(jq -er .app_version .publication/snapshot-plan.json)"
export SNAPSHOT_CHART_VERSION SNAPSHOT_APP_VERSION SNAPSHOT_VARNISH_IMAGE
SNAPSHOT_CHART_VERSION="$(jq -er .chart_version .publication/snapshot-plan.json)"
SNAPSHOT_APP_VERSION="$app_version"
SNAPSHOT_VARNISH_IMAGE="${repository}/varnish:${app_version}"
yq -i '.version = strenv(SNAPSHOT_CHART_VERSION) | .appVersion = strenv(SNAPSHOT_APP_VERSION)' \
    charts/haptic/Chart.yaml
yq -i '.cache.varnish.image = strenv(SNAPSHOT_VARNISH_IMAGE)' charts/haptic/values.yaml

# shellcheck source=../versions.env
source versions.env
platforms=linux/amd64,linux/arm64,linux/arm/v7
labels=(--label "org.opencontainers.image.source=https://github.com/${GITHUB_REPOSITORY}"
        --label "org.opencontainers.image.revision=${sha}"
        --label "org.opencontainers.image.version=${app_version}")

verify_image() {
    python3 -m scripts.ci.github_snapshot --verify-image "$1" --platforms "$2" \
        --output ".publication/snapshot-${3}.json"
}

verify_controller_images() {
    local series
    for series in $HAPROXY_VERSIONS; do
        verify_image "${repository}:${app_version}-haproxy${series}" "$platforms" "haproxy${series}"
    done
    verify_image "$SNAPSHOT_VARNISH_IMAGE" linux/amd64,linux/arm64 varnish
}

case "$phase" in
    spoa)
        make spoa-prep
        # shellcheck source=../versions-spoa.env
        source versions-spoa.env
        upstream="registry.gitlab.com/haproxy-haptic/haproxy-spoa-hub:${SPOA_HUB_VERSION#v}"
        digest="$(docker buildx imagetools inspect "$upstream" --format '{{json .Manifest.Digest}}' |
            jq -er 'select(test("^sha256:[0-9a-f]{64}$"))')"
        docker buildx build --platform "$platforms" \
            --build-arg "SPOA_HUB_VERSION=${SPOA_HUB_VERSION#v}@${digest}" \
            --build-context plugins=plugins "${labels[@]}" \
            --tag "${repository}/spoa-hub:${app_version}" --metadata-file .publication/spoa-build.json \
            --provenance=false --push -f Dockerfile.spoa-hub .
        verify_image "${repository}/spoa-hub:${app_version}" "$platforms" spoa
        jq -er .reference .publication/snapshot-spoa.json > .publication/spoa-reference.txt
        ;;
    controller)
        make prepare-github-release-config GITHUB_SNAPSHOT_SHA="$sha"
        cp .pilot/goreleaser.github.yml .publication/goreleaser.snapshot.yml
        SOURCE_HASH="$(bash scripts/source-hash.sh)"
        export SOURCE_HASH
        bash scripts/verify-source-hash.sh
        goreleaser build --snapshot --clean --config .publication/goreleaser.snapshot.yml
        for target in amd64 arm64 arm/v7; do
            case "$target" in
                amd64) source=controller_linux_amd64_v1 ;;
                arm64) source=controller_linux_arm64_v8.0 ;;
                arm/v7) source=controller_linux_arm_7 ;;
            esac
            mkdir -p "binary-context/linux/${target}"
            cp "dist/${source}/haptic" "binary-context/linux/${target}/haptic"
        done
        binary-context/linux/amd64/haptic version > .publication/controller-version.txt
        awk -v version="$app_version" -v commit="$sha" -v hash="$SOURCE_HASH" '
            $1 == "Version:" { v = ($2 == version) }
            $1 == "Commit:" { c = ($2 == commit) }
            $1 == "Source" && $2 == "Hash:" { h = ($3 == hash) }
            END { exit !(v && c && h) }
        ' .publication/controller-version.txt
        for series in $HAPROXY_VERSIONS; do
            image="$(bash scripts/chart-haproxy-image.sh "$series")"
            docker buildx build --platform "$platforms" --build-arg "HAPROXY_IMAGE=${image}" \
                --build-context binary=binary-context --target runtime "${labels[@]}" \
                --tag "${repository}:${app_version}-haproxy${series}" \
                --metadata-file ".publication/haproxy${series}-build.json" --provenance=false --push .
        done
        docker buildx build --platform linux/amd64,linux/arm64 "${labels[@]}" \
            --tag "$SNAPSHOT_VARNISH_IMAGE" --metadata-file .publication/varnish-build.json \
            --provenance=false --push -f Dockerfile.varnish .
        verify_controller_images
        ;;
    chart)
        verify_controller_images
        verify_image "${repository}/spoa-hub:${app_version}" "$platforms" spoa
        helm package charts/haptic --destination .publication
        archive=".publication/haptic-${SNAPSHOT_CHART_VERSION}.tgz"
        helm push "$archive" "oci://${repository}/charts"
        mkdir -p .publication/chart-retrieved
        helm pull "oci://${repository}/charts/haptic" --version "$SNAPSHOT_CHART_VERSION" \
            --destination .publication/chart-retrieved
        cmp "$archive" ".publication/chart-retrieved/haptic-${SNAPSHOT_CHART_VERSION}.tgz"
        sha256sum "$archive" > .publication/chart-sha256.txt
        reference="$(jq -er .reference ".publication/snapshot-haproxy${DEFAULT_HAPROXY}.json")"
        docker buildx imagetools create --tag "${repository}:${app_version}" "$reference"
        verify_image "${repository}:${app_version}" "$platforms" default
        ;;
esac
