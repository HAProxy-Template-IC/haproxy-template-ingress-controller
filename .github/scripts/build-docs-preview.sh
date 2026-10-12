#!/usr/bin/env bash
set -euo pipefail

preview_scratch=$(mktemp -d)
trap 'rm -rf -- "$preview_scratch"' EXIT
pages_revision=c992b9cbb92761f17dd04473bf2d892caa3d4bf4
pages_api=https://gitlab.com/api/v4/projects/haproxy-haptic%2Fhaproxy-haptic.gitlab.io
mkdir -p .pilot "$preview_scratch/scripts" "$preview_scratch/public"
for file in deploy-docs.sh semver-order.py site_versions.py; do
    curl --fail --silent --show-error --location --retry 3 \
        "$pages_api/repository/files/scripts%2F$file/raw?ref=$pages_revision" \
        --output "$preview_scratch/scripts/$file"
done
(
    cd "$preview_scratch/scripts"
    sha256sum --check <<'HASHES'
d7b1804d028937ebe4dae6a1587d3adab75eeb4f54b4d6cf27fe6dee24d82e92  deploy-docs.sh
fd20c2ee5f58b998d5453bd1a6b8dc79ac23f810d075f39a91b9166107feee19  semver-order.py
51d39bee1b654619919470cdd33fafc288ad82c6e388754f3a2b46e6a1b96648  site_versions.py
HASHES
)
chmod +x "$preview_scratch/scripts/semver-order.py"
status=$(curl --silent --show-error --location --retry 3 --write-out '%{http_code}' \
    "$pages_api/jobs/artifacts/main/download?job=pages" --output "$preview_scratch/pages.zip")
if [[ "$status" == 200 ]] && unzip -qt "$preview_scratch/pages.zip" >/dev/null; then
    unzip -q "$preview_scratch/pages.zip" -d "$preview_scratch"
    preview_base=published
else
    preview_base=standalone
    printf 'Published Pages artifact unavailable (HTTP %s); building the standalone preview.\n' "$status"
fi
export CI_COMMIT_SHA="${PILOT_SOURCE_SHA:-$(git rev-parse HEAD)}"
export CI_PIPELINE_ID="${GITHUB_RUN_ID:-0}"
export CI_JOB_ID="${GITHUB_JOB:-local}"
bash "$preview_scratch/scripts/deploy-docs.sh" dev "$PWD" "$preview_scratch/public" preview
test -s "$preview_scratch/public/index.html"
test -s "$preview_scratch/public/docs/dev/index.html"
jq --exit-status --arg sha "$CI_COMMIT_SHA" --arg run "$CI_PIPELINE_ID" --arg job "$CI_JOB_ID" \
    '.version == "dev" and .commit == $sha and .pipeline == $run and .job == $job' \
    "$preview_scratch/public/docs/dev/release-identity.json"
cp "$preview_scratch/public/docs/dev/release-identity.json" .pilot/docs-preview-identity.json
jq --null-input --arg revision "$pages_revision" --arg base "$preview_base" \
    '{pages_source_revision:$revision, preview_base:$base}' > .pilot/docs-preview-provenance.json
tar -C "$preview_scratch/public" -czf .pilot/docs-preview.tar.gz .
sha256sum .pilot/docs-preview.tar.gz > .pilot/docs-preview-sha256.txt
