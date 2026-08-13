#!/usr/bin/env bash
# Calculate a deterministic hash of controller build inputs
#
# This hash changes whenever controller Go source, module selection, or the PGO
# profile changes, regardless of git commit status.
#
# Usage:
#   ./scripts/source-hash.sh
#
# Output:
#   12-character hex hash (e.g., "a1b2c3d4e5f6")

set -euo pipefail
export LC_ALL=C

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${REPO_ROOT}"

# Find all inputs, sort for determinism, hash contents, then hash the result.
# -print0 and -0 handle filenames with spaces/special chars
{
    find pkg cmd -type f \( -name "*.go" -o -name "default.pgo" \) -print0 || exit $?
    printf '%s\0' go.mod go.sum
} | \
    sort -z | \
    xargs -0 sha256sum | \
    sha256sum | \
    cut -c1-12
