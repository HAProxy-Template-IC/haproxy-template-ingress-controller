#!/usr/bin/env bash
set -euo pipefail
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"
mkdir -p bin
env -u GOROOT go build -o bin/test-infra ./tests/runner
exec "$REPO/bin/test-infra" "$@"
