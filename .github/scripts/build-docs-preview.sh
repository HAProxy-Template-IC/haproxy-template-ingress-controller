#!/usr/bin/env bash
set -euo pipefail

preview_scratch=$(mktemp -d)
trap 'rm -rf -- "$preview_scratch"' EXIT
mkdir -p .pilot
make restore-current-github-website PAGES_OUTPUT="$preview_scratch/public"
python3 .github/scripts/verify-docs-preservation.py record "$preview_scratch/public"
export CI_COMMIT_SHA="${PILOT_SOURCE_SHA:-$(git rev-parse HEAD)}"
export CI_PIPELINE_ID="${GITHUB_RUN_ID:-0}"
export CI_JOB_ID="${GITHUB_JOB:-local}"
test "$(git rev-parse HEAD)" = "$CI_COMMIT_SHA"
cp docs/shared/colors.css docs/landing/docs/stylesheets/
cp docs/shared/colors.css docs/site/docs/stylesheets/
canonical=dev
if [[ -s "$preview_scratch/public/docs/latest/index.html" ]]; then canonical=latest; fi
DOCS_SITE_URL="https://haproxy-haptic.org/docs/$canonical/" \
    mkdocs build --strict -f docs/site/mkdocs.yml -d "$preview_scratch/docs-dev"
jq --null-input --arg version dev --arg commit "$CI_COMMIT_SHA" \
    --arg pipeline "$CI_PIPELINE_ID" --arg job "$CI_JOB_ID" \
    '{version:$version, commit:$commit, pipeline:$pipeline, job:$job}' \
    > "$preview_scratch/docs-dev/release-identity.json"
mkdocs build --strict -f docs/landing/mkdocs.yml -d "$preview_scratch/landing"
du -sb "$preview_scratch/docs-dev" "$preview_scratch/landing" > .pilot/docs-sizes.txt
cp -a --remove-destination "$preview_scratch/landing/." "$preview_scratch/public/"
cp -a --remove-destination docs/shared/. "$preview_scratch/public/shared/"
rm -rf -- "$preview_scratch/public/docs/dev"
mv "$preview_scratch/docs-dev" "$preview_scratch/public/docs/dev"
python3 .github/scripts/verify-docs-preservation.py verify "$preview_scratch/public"
test -s "$preview_scratch/public/index.html"
test -s "$preview_scratch/public/docs/dev/index.html"
jq --exit-status --arg sha "$CI_COMMIT_SHA" --arg run "$CI_PIPELINE_ID" --arg job "$CI_JOB_ID" \
    '.version == "dev" and .commit == $sha and .pipeline == $run and .job == $job' \
    "$preview_scratch/public/docs/dev/release-identity.json"
cp "$preview_scratch/public/docs/dev/release-identity.json" .pilot/docs-preview-identity.json
jq '{preview_base:"github-version-catalog", baseline_url:.url, baseline_sha256:.sha256}' \
    .github/pages-baseline.json > .pilot/docs-preview-provenance.json
tar -C "$preview_scratch/public" -czf .pilot/docs-preview.tar.gz .
sha256sum .pilot/docs-preview.tar.gz > .pilot/docs-preview-sha256.txt
