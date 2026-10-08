#!/usr/bin/env bash
set -euo pipefail
exec "$(dirname "$0")/test-infra.sh" helm-defaults "$@"
