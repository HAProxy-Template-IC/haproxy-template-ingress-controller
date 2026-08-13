#!/usr/bin/env bash

set -euo pipefail

actual="$("$(dirname "${BASH_SOURCE[0]}")/source-hash.sh")"
if [[ ! ${SOURCE_HASH:-} =~ ^[0-9a-f]{12}$ ]]; then
    echo "SOURCE_HASH is missing or invalid, so the controller build cannot stamp its source identity. Use scripts/source-hash.sh and retry." >&2
    exit 1
fi

if [[ ${actual} != "${SOURCE_HASH}" ]]; then
    echo "Controller source identity is stale (expected ${SOURCE_HASH}, got ${actual}), so the binary would report the wrong inputs. Recompute the hash and retry." >&2
    exit 1
fi
