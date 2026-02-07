#!/bin/bash
# Release script for the HAProxy Template Ingress Controller Helm Chart
#
# Usage: ./scripts/release-chart.sh <version>
# Example: ./scripts/release-chart.sh 0.1.0
#
# This script:
# 1. Validates the version format (SemVer)
# 2. Updates Chart.yaml version
# 3. Updates README helm install version references
# 4. Commits changes (tag is created automatically by CI after merge)
#
# After running this script:
#   1. Push to a release branch and create an MR
#   2. Merge the MR to main
#   3. CI automatically creates the tag and triggers the release pipeline

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
if [[ ! -f "go.mod" ]] || [[ ! -d "charts/haptic" ]]; then
    error "This script must be run from the repository root"
fi

# Validate arguments
if [[ $# -ne 1 ]]; then
    echo "Usage: $0 <version>"
    echo "Example: $0 0.1.0"
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

# Check chart CHANGELOG.md has entry for this version
if ! grep -q "## \[$VERSION\]" charts/haptic/CHANGELOG.md; then
    error "charts/haptic/CHANGELOG.md has no entry for version $VERSION

Please update charts/haptic/CHANGELOG.md before releasing:
1. Rename [Unreleased] section to [$VERSION] - $(date +%Y-%m-%d)
2. Add a new empty [Unreleased] section at the top
3. Run this script again"
fi

# Update Chart.yaml version
echo "Updating Chart.yaml version..."
sed -i "s/^version:.*/version: $VERSION/" charts/haptic/Chart.yaml

# Update README helm install version references
echo "Updating README.md helm install versions..."
sed -i "s|haptic --version [0-9a-z.-]*|haptic --version $VERSION|" README.md
sed -i "s|haptic --version [0-9a-z.-]*|haptic --version $VERSION|" charts/haptic/README.md

# Show changes
echo ""
echo "Changes to be committed:"
git diff --stat

# Commit changes (tag created automatically by CI after merge)
echo ""
echo "Creating commit..."
git add charts/haptic/Chart.yaml README.md charts/haptic/README.md
git commit -m "release: chart v$VERSION"

success ""
success "Release commit created for chart v$VERSION"
success ""
echo "Next steps:"
echo "  1. Review the commit: git show HEAD"
echo "  2. Create release branch: git checkout -b release/haptic-chart-v$VERSION"
echo "  3. Push and create MR: git push -u origin release/haptic-chart-v$VERSION"
echo "  4. Merge the MR to main"
echo ""
echo "After merge, CI will automatically:"
echo "  - Create tag haptic-chart-v$VERSION"
echo "  - Package and push Helm chart to OCI registry"
echo "  - Create GitLab release"
