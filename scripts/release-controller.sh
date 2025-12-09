#!/bin/bash
# Release script for the HAProxy Template Ingress Controller
#
# Usage: ./scripts/release-controller.sh <version>
# Example: ./scripts/release-controller.sh 0.1.0-beta.1
#
# This script:
# 1. Validates the version format (SemVer)
# 2. Checks that CHANGELOG.md has an entry for the version
# 3. Updates the VERSION file
# 4. Updates Chart.yaml appVersion
# 5. Commits changes and creates a git tag
#
# After running this script, push the tag to trigger CI:
#   git push origin main controller-v<version>

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

# Check if we're in the repository root
if [[ ! -f "go.mod" ]] || [[ ! -d "charts/haproxy-template-ic" ]]; then
    error "This script must be run from the repository root"
fi

# Validate arguments
if [[ $# -ne 1 ]]; then
    echo "Usage: $0 <version>"
    echo "Example: $0 0.1.0-beta.1"
    exit 1
fi

VERSION=$1

# Validate SemVer format (with optional pre-release suffix)
# Matches: 0.1.0, 1.0.0, 1.2.3-alpha.1, 1.2.3-beta.2, 1.2.3-rc.1
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[a-z]+\.[0-9]+)?$ ]]; then
    error "Invalid version format. Use: X.Y.Z or X.Y.Z-suffix.N (e.g., 0.1.0-beta.1)"
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

# Check CHANGELOG.md has entry for this version
if ! grep -q "## \[$VERSION\]" CHANGELOG.md; then
    error "CHANGELOG.md has no entry for version $VERSION

Please update CHANGELOG.md before releasing:
1. Rename [Unreleased] section to [$VERSION] - $(date +%Y-%m-%d)
2. Add a new empty [Unreleased] section at the top
3. Run this script again"
fi

# Update VERSION file
echo "Updating VERSION file..."
echo "$VERSION" > VERSION

# Update Chart.yaml appVersion
echo "Updating Chart.yaml appVersion..."
sed -i "s/^appVersion:.*/appVersion: \"$VERSION\"/" charts/haproxy-template-ic/Chart.yaml

# Show changes
echo ""
echo "Changes to be committed:"
git diff --stat

# Commit and tag
echo ""
echo "Creating commit and tag..."
git add VERSION charts/haproxy-template-ic/Chart.yaml
git commit -m "release: controller v$VERSION"
git tag -a "controller-v$VERSION" -m "Controller release v$VERSION"

success ""
success "Created tag: controller-v$VERSION"
success ""
echo "Next steps:"
echo "  1. Review the commit: git show HEAD"
echo "  2. Push to trigger CI: git push origin main controller-v$VERSION"
