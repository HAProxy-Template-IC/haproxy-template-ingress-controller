#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
mode="${1:?usage: spoa-release-image.sh prepare|verify}"
[[ "$mode" == prepare || "$mode" == verify ]] || exit 2
version="$(<VERSION)"
repository="${CI_REGISTRY_IMAGE:-registry.gitlab.com/haproxy-haptic/haptic}/spoa-hub"
release_image="${repository}:${version}"
input_label=org.haproxy-haptic.spoa.inputs-sha256
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

fail() { printf '%s\n' "$*" >&2; exit 1; }

[[ "$(yq -r '.version' charts/haptic/Chart.yaml)" == "$version" &&
   "$(yq -r '.appVersion' charts/haptic/Chart.yaml)" == "$version" ]] ||
    fail 'Release versions differ; run make release before preparing the SPOA image'
[[ "$(yq -r '.spoaHub.image.repository' charts/haptic/values.yaml)" == "$repository" &&
   -z "$(yq -r '.spoaHub.image.tag' charts/haptic/values.yaml)" ]] ||
    fail 'Chart SPOA image overrides the release image; remove the override before preparing it'
if [[ "$mode" == prepare ]]; then
    [[ "${CI_MERGE_REQUEST_SOURCE_BRANCH_NAME:-${CI_COMMIT_BRANCH:-}}" == "release/v${version}" &&
       "${CI_JOB_ID:-}" =~ ^[0-9]+$ ]] ||
        fail 'SPOA preparation requires the matching release branch CI job'
fi

# shellcheck source=../versions-spoa.env
source versions-spoa.env
hub_image="registry.gitlab.com/haproxy-haptic/haproxy-spoa-hub:${SPOA_HUB_VERSION#v}"
hub_digest="$(docker buildx imagetools inspect "$hub_image" --format '{{json .Manifest.Digest}}' | jq -er '.')"
[[ "$hub_digest" =~ ^sha256:[0-9a-f]{64}$ ]] || fail 'Upstream SPOA image has no valid digest'
for arch in amd64 arm64 armv7; do
    compgen -G "plugins/${arch}/*.so" >/dev/null || fail "SPOA plugins/${arch} is empty; run make spoa-prep"
done
{
    printf '%s\n' "$hub_digest"
    sha256sum Dockerfile.spoa-hub .dockerignore versions-spoa.env
    find plugins -type f -name '*.so' -print0 | LC_ALL=C sort -z | xargs -0 sha256sum
} > "$work_dir/inputs"
inputs_hash="$(sha256sum "$work_dir/inputs" | awk '{print $1}')"

image_digest() {
    docker buildx imagetools inspect "$1" --format '{{json .Manifest.Digest}}' |
        jq -er 'select(test("^sha256:[0-9a-f]{64}$"))'
}

verify_bundle() {
    local digest="$1" platform child config
    docker buildx imagetools inspect "${repository}@${digest}" --raw > "$work_dir/index.json"
    jq -e '
        [.manifests[].platform | .os + "/" + .architecture +
          (if .variant then "/" + .variant else "" end)] | sort ==
        ["linux/amd64", "linux/arm/v7", "linux/arm64"]
    ' "$work_dir/index.json" >/dev/null || fail "SPOA ${release_image} must contain exactly amd64, arm64, and arm/v7"
    while IFS=$'\t' read -r platform child; do
        [[ "$child" =~ ^sha256:[0-9a-f]{64}$ ]] || fail "SPOA ${platform} manifest has no valid digest"
        config="$(docker buildx imagetools inspect "${repository}@${child}" --format '{{json .Image}}')"
        jq -e --arg key "$input_label" --arg hash "$inputs_hash" --arg version "$version" '
            .config.Labels[$key] == $hash and
            .config.Labels["org.opencontainers.image.version"] == $version
        ' <<< "$config" >/dev/null ||
            fail "SPOA ${release_image} ${platform} has different build inputs; use a new release version"
    done < <(jq -r '.manifests[] | [.platform.architecture, .digest] | @tsv' "$work_dir/index.json")
}

record_bundle() {
    jq -n --arg digest "$1" '{"containerimage.digest": $digest}' > metadata.json
    printf 'Verified %s@%s (inputs %s)\n' "$repository" "$1" "$inputs_hash"
}

release_is_missing() {
    [[ "$(<"$work_dir/inspect-error")" == "ERROR: ${release_image}: not found" ]]
}

if digest="$(image_digest "$release_image" 2> "$work_dir/inspect-error")"; then
    verify_bundle "$digest"
    record_bundle "$digest"
    exit 0
fi
[[ "$mode" == prepare ]] || fail "SPOA ${release_image} is unavailable; run the prepare-spoa-release CI job first"
release_is_missing || {
    sed -n '1,8p' "$work_dir/inspect-error" >&2
    fail "Cannot inspect SPOA ${release_image}; fix registry access before preparing it"
}

candidate="${repository}:ci-spoa-prepare-${version}-${CI_JOB_ID}"
docker buildx build \
    --platform linux/amd64,linux/arm64,linux/arm/v7 \
    --build-arg "SPOA_HUB_VERSION=${SPOA_HUB_VERSION#v}@${hub_digest}" \
    --build-context plugins=plugins \
    --label "${input_label}=${inputs_hash}" \
    --label "org.opencontainers.image.source=${CI_PROJECT_URL}" \
    --label "org.opencontainers.image.revision=${CI_COMMIT_SHA}" \
    --label "org.opencontainers.image.version=${version}" \
    --tag "$candidate" --provenance=false --push -f Dockerfile.spoa-hub .
candidate_digest="$(image_digest "$candidate")"
verify_bundle "$candidate_digest"

# CI serializes publishers; never replace a release that appeared during the build.
if digest="$(image_digest "$release_image" 2> "$work_dir/inspect-error")"; then
    verify_bundle "$digest"
else
    release_is_missing ||
        fail "Cannot recheck SPOA ${release_image}; fix registry access before publishing it"
    docker buildx imagetools create --tag "$release_image" "${repository}@${candidate_digest}"
    digest="$(image_digest "$release_image")"
    [[ "$digest" == "$candidate_digest" ]] || fail "SPOA ${release_image} changed while publishing; inspect the registry"
fi
record_bundle "$digest"
