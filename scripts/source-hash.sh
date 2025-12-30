#!/usr/bin/env bash
# Calculate deterministic hash of Go source files
#
# This hash changes whenever any .go file in pkg/ or cmd/ is modified,
# regardless of git commit status. Used to verify the dev environment
# is running the current local code.
#
# Usage:
#   ./scripts/source-hash.sh
#
# Output:
#   12-character hex hash (e.g., "a1b2c3d4e5f6")

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${REPO_ROOT}"

# Find all .go files, sort for determinism, hash contents, then hash the result
# -print0 and -0 handle filenames with spaces/special chars
find pkg cmd -name "*.go" -type f -print0 2>/dev/null | \
    sort -z | \
    xargs -0 sha256sum 2>/dev/null | \
    sha256sum | \
    cut -c1-12
