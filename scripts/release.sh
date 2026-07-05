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
# 5. Updates the `helm install ... --version` examples in the READMEs and
#    docs, and the landing page's fallback version
# 6. Regenerates the docs-site changelog copies from CHANGELOG.md
# 7. Commits everything (the tag is created automatically by CI after merge)
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

# --- helm install examples (READMEs, docs, landing page) -----------------------
echo "Updating helm install --version examples..."
# Only occurrences of the PREVIOUS release version are rewritten: docs that
# deliberately pin a historical version (upgrade/migration guides) must not
# be clobbered by a global version rewrite.
PREV_VERSION=$(git show HEAD:VERSION 2>/dev/null || cat VERSION)
INSTALL_EXAMPLE_FILES=$(grep -rlF -- "--version $PREV_VERSION" \
    README.md charts/haptic/README.md docs/controller/docs charts/haptic/docs 2>/dev/null || true)
for f in $INSTALL_EXAMPLE_FILES; do
    sed -i "s|--version $PREV_VERSION|--version $VERSION|g" "$f"
done
# Landing page fallback (replaced client-side by the published-versions JS)
sed -i -E "s|(<span id=\"helm-version\" class=\"t-num\">)[^<]*|\1$VERSION|" docs/landing/overrides/home.html

# --- docs-site changelog copies -------------------------------------------------
# Both doc sites carry a copy of the root CHANGELOG. Repo-relative links don't
# resolve on the docs site, so they are rewritten to GitLab source URLs.
sync_changelog_copy() {
    local target=$1
    local with_front_matter=$2
    {
        if [[ "$with_front_matter" == "yes" ]]; then
            printf -- '---\nhide:\n  - navigation\n---\n\n'
        fi
        printf '# Changelog\n\n'
        printf 'All notable changes to HAPTIC — the controller and its Helm chart — are\n'
        printf 'documented in this file. Controller changes are listed first; chart changes\n'
        printf '(values, templates, chart defaults) follow under each release'\''s "Helm chart"\n'
        printf 'subsection.\n\n'
        printf 'The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),\n'
        printf 'and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).\n\n'
        sed -n '/^## \[/,$p' CHANGELOG.md \
            | sed -e 's|](\./docs/|](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/docs/|g' \
                  -e 's|](\./charts/|](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/charts/|g'
    } > "$target"
}
echo "Regenerating docs-site changelog copies..."
sync_changelog_copy docs/controller/docs/changelog.md yes
sync_changelog_copy charts/haptic/docs/changelog.md no

# --- commit --------------------------------------------------------------------
git add CHANGELOG.md VERSION charts/haptic/Chart.yaml \
    docs/controller/docs/changelog.md charts/haptic/docs/changelog.md \
    docs/landing/overrides/home.html $INSTALL_EXAMPLE_FILES

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
