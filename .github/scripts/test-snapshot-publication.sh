#!/usr/bin/env bash
set -euo pipefail

registry_id=$(docker run --detach --publish 127.0.0.1:5000:5000 \
    registry@sha256:ddf754342cfc8acc51a56d5d0ab6af06826461864460636d8bd5c546dab2a7b8)
trap 'docker rm -f "$registry_id" >/dev/null' EXIT
curl --fail --silent --show-error --retry 30 --retry-delay 1 --retry-all-errors \
    http://127.0.0.1:5000/v2/
python3 .github/scripts/verify-snapshot-publication.py
if [[ "${PILOT_VERIFY_SIGNATURES:-0}" == 1 ]]; then
    python3 .github/scripts/verify-publication-signatures.py
fi
