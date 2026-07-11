#!/bin/bash
# Release script for HAPTIC. The controller and the Helm chart are released
# together: one version, one tag, one changelog.
#
# Usage: ./scripts/release.sh <version>
# Example: ./scripts/release.sh 0.2.0
#          ./scripts/release.sh 0.2.0-alpha.1
#
# This script:
# 1. Validates the version format (SemVer)
# 2. Promotes the CHANGELOG.md [Unreleased] section to [<version>] - <date>
#    (skipped when a [<version>] section already exists)
# 3. Writes <version> to the VERSION file
# 4. Updates charts/haptic/Chart.yaml: version, appVersion, and the
#    artifacthub.io/images annotation (controller + spoa-hub image tags)
# 5. Rewrites every current-version reference across the documentation in one
#    pass (helm install --version examples, the pinned controller image tag in
#    migrate-check's docker one-liner) and the landing page's fallback version
# 6. Commits everything (the tag is created automatically by CI after merge)
#
# After running this script:
#   1. Review the commit — especially the promoted changelog section and the
#      hand-curated artifacthub.io/changes annotation in Chart.yaml
#   2. Push to a release branch and create an MR
#   3. Merge the MR to main
#   4. CI creates the v<version> tag and the release pipeline publishes
#      binaries, container images, the Helm chart, and versioned docs

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Print error message and exit
error() {
    echo -e "${RED}Error: $1${NC}" >&2
    exit 1
}

# Print success message
success() {
    echo -e "${GREEN}$1${NC}"
}

# Print warning message
warn() {
    echo -e "${YELLOW}$1${NC}"
}

usage() {
    echo "Usage: $0 <version>"
    echo "Example: $0 0.2.0"
    echo "         $0 0.2.0-alpha.1"
}

# Check if we're in the repository root
if [[ ! -f "go.mod" ]] || [[ ! -d "charts/haptic" ]]; then
    error "This script must be run from the repository root"
fi

# Validate arguments
if [[ $# -ne 1 ]]; then
    usage
    exit 1
fi

VERSION=$1

# Validate SemVer format (with optional pre-release suffix)
# Matches: 0.1.0, 1.0.0, 1.2.3-alpha.1, 1.2.3-beta.2, 1.2.3-rc.1
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[a-z]+\.[0-9]+)?$ ]]; then
    error "Invalid version format. Use: X.Y.Z or X.Y.Z-suffix.N (e.g., 0.2.0-alpha.1)"
fi

# Check if working directory is clean
if [[ -n $(git status --porcelain) ]]; then
    warn "Warning: Working directory is not clean"
    git status --short
    read -p "Continue anyway? [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
fi

# --- CHANGELOG: promote [Unreleased] to [<version>] - <date> -----------------
if grep -q "^## \[$VERSION\]" CHANGELOG.md; then
    warn "CHANGELOG.md already has a [$VERSION] section — skipping the [Unreleased] promotion"
else
    grep -q "^## \[Unreleased\]" CHANGELOG.md \
        || error "CHANGELOG.md has no [Unreleased] section to promote"
    echo "Promoting CHANGELOG.md [Unreleased] to [$VERSION]..."
    # Insert the version heading right below [Unreleased]: the unreleased
    # content ends up under the new version, and [Unreleased] stays empty
    # at the top.
    # awk instead of sed: GNU sed renders \n in the replacement as a newline,
    # BSD/macOS sed emits a literal "n" — silently corrupting the changelog.
    awk -v ver="$VERSION" -v today="$(date +%Y-%m-%d)" \
        '{ print; if ($0 == "## [Unreleased]") { print ""; print "## [" ver "] - " today } }' \
        CHANGELOG.md > CHANGELOG.md.tmp && mv CHANGELOG.md.tmp CHANGELOG.md
fi

# --- VERSION file -------------------------------------------------------------
echo "Updating VERSION file..."
echo "$VERSION" > VERSION

# --- Chart.yaml: version, appVersion, image annotations ------------------------
echo "Updating Chart.yaml version and appVersion..."
sed -i "s/^version:.*/version: $VERSION/" charts/haptic/Chart.yaml
sed -i "s/^appVersion:.*/appVersion: \"$VERSION\"/" charts/haptic/Chart.yaml

echo "Updating Chart.yaml artifacthub.io/images annotation..."
# shellcheck source=../versions.env
source versions.env
sed -i "s|haptic:[0-9a-z.-]*|haptic:$VERSION-haproxy$DEFAULT_HAPROXY|" charts/haptic/Chart.yaml
sed -i "s|spoa-hub:[0-9a-z.-]*|spoa-hub:$VERSION|" charts/haptic/Chart.yaml
sed -i "s|most recently shipped release ([^)]*)|most recently shipped release ($VERSION)|" charts/haptic/Chart.yaml

# --- documentation version references (single pass) ----------------------------
echo "Updating documentation version references..."
# One rewrite pass for the whole documentation — no per-kind duplication. The
# current release appears in exactly two syntactic forms across the docs:
#   1. helm `... --version X.Y.Z` install examples
#   2. the pinned controller image tag in migrate-check's docker one-liner,
#      `haptic:X.Y.Z-haproxy<series>`
# Both are rewritten below from the PREVIOUS release's version. The patterns are
# ANCHORED to those two contexts rather than a bare global substring, so that
# deliberately-fixed illustrative version strings elsewhere are never clobbered:
# Prometheus `version="0.1.0"` examples, changelog `## [X.Y.Z]` headings, and the
# version-scheme tables in releasing.md. The `\b` / `-haproxy` boundaries also
# stop a version that is a prefix of a longer one (0.2.0-alpha.1 vs .10) from
# being partially rewritten. The image tag's `-haproxy<series>` suffix is
# regenerated from versions.env's DEFAULT_HAPROXY (sourced above), so it tracks
# the default HAProxy series and stays a published tag instead of a stale
# hardcoded one.
PREV_VERSION=$(git show HEAD:VERSION 2>/dev/null || cat VERSION)
# Escape the version for a Basic-Regexp (BRE) sed pattern so it matches
# literally. BRE, not -E: in BRE the only metacharacters are . [ ] \ * ^ $
# (escaped below), while + ? ( ) { } | are literal — so any future version
# scheme (e.g. semver build metadata `1.0.0+build.5`) is still matched
# literally instead of `+` acting as a quantifier.
PREV_ESC=$(printf '%s' "$PREV_VERSION" | sed 's/[][\.^$*]/\\&/g')
VERSION_DOC_FILES=$(grep -rlF -- "$PREV_VERSION" \
    README.md charts/haptic/README.md docs/site/docs 2>/dev/null || true)
for f in $VERSION_DOC_FILES; do
    sed -i \
        -e "s|\\(--version \\)$PREV_ESC\\b|\\1$VERSION|g" \
        -e "s|\\(haptic:\\)$PREV_ESC-haproxy[0-9.]*|\\1$VERSION-haproxy$DEFAULT_HAPROXY|g" \
        "$f"
done
# These files ship inside the released chart (Artifact Hub renders README and
# values.yaml; NOTES.txt prints after install; Chart.yaml carries the Artifact
# Hub Documentation link), so their hosted-docs links must point at the version
# being released, not the moving dev docs — and repo-blob links (e.g. the ADR
# rationale pointer in values.yaml) must pin to the release tag, not the moving
# main branch. Two patterns per link kind keep this idempotent across releases:
# the first release rewrites the initial dev/main links; every later release
# rewrites the previous version's links (same PREV->CURRENT keying as the
# version-pin rewrites above). The v$VERSION tag doesn't exist yet when this
# runs (CI creates it after the release MR merges), but the chart is only
# published by that tag's pipeline, so shipped links always resolve.
sed -i \
    -e "s|haproxy-haptic.org/docs/dev/|haproxy-haptic.org/docs/$VERSION/|g" \
    -e "s|haproxy-haptic.org/docs/$PREV_ESC/|haproxy-haptic.org/docs/$VERSION/|g" \
    -e "s|gitlab.com/haproxy-haptic/haptic/-/blob/main/|gitlab.com/haproxy-haptic/haptic/-/blob/v$VERSION/|g" \
    -e "s|gitlab.com/haproxy-haptic/haptic/-/blob/v$PREV_ESC/|gitlab.com/haproxy-haptic/haptic/-/blob/v$VERSION/|g" \
    charts/haptic/README.md \
    charts/haptic/values.yaml \
    charts/haptic/Chart.yaml \
    charts/haptic/templates/NOTES.txt
# Landing page fallback (replaced client-side by the published-versions JS)
sed -i -E "s|(<span id=\"helm-version\" class=\"t-num\">)[^<]*|\1$VERSION|" docs/landing/overrides/home.html

# The docs-site changelog page is generated at mkdocs build time from
# CHANGELOG.md (docs/site/hooks/changelog.py) — no release-time sync needed.

# --- commit --------------------------------------------------------------------
git add CHANGELOG.md VERSION charts/haptic/Chart.yaml \
    docs/landing/overrides/home.html $VERSION_DOC_FILES

if git diff --cached --quiet; then
    warn "Nothing to do — all release files already carry $VERSION"
    exit 0
fi

echo ""
echo "Changes to be committed:"
git diff --cached --stat

echo ""
echo "Creating commit..."
git commit -m "release: haptic v$VERSION"

success ""
success "Release commit created for v$VERSION"
success ""
echo "Next steps:"
echo "  1. Review the commit: git show HEAD"
echo "     - check the promoted [$VERSION] changelog section reads as release notes"
echo "     - update the hand-curated artifacthub.io/changes annotation in"
echo "       charts/haptic/Chart.yaml (summary bullets for the new release)"
echo "  2. Create release branch: git checkout -b release/v$VERSION"
echo "  3. Push and create MR: git push -u origin release/v$VERSION"
echo "  4. Merge the MR to main"
echo ""
echo "After merge, CI will automatically:"
echo "  - Create tag v$VERSION (after the full post-merge pipeline passes)"
echo "  - Build binaries and Docker images (controller + spoa-hub)"
echo "  - Package and push the Helm chart to the OCI registry"
echo "  - Create the GitLab release with the [$VERSION] changelog section"
echo "  - Publish versioned documentation"
