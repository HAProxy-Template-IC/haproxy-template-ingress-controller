#!/usr/bin/env bash
set -euo pipefail

: "${CONFORMANCE_IMPL_VERSION:?Set the exact controller image version}"
export CONFORMANCE_CONTROLLER_IMAGE=${CONFORMANCE_CONTROLLER_IMAGE:-haptic:test}
export CONFORMANCE_CONTAINER=${CONFORMANCE_CONTAINER:-haptic-conformance-report-${CI_JOB_ID:-$$}}
export CONFORMANCE_IMAGE=${CONFORMANCE_IMAGE:-haptic-conformance-report:${CI_JOB_ID:-$$}}
artifact_dir=${CONFORMANCE_ARTIFACT_DIR:-build/gateway-conformance}
mkdir -p "$artifact_dir"
if [[ -n "${TEST_RUN_PATTERN:-}" ]]; then
    echo "Conformance evidence requires an unfiltered run; unset TEST_RUN_PATTERN." >&2
    exit 1
fi
if [[ -e "$artifact_dir/report.yaml" || -e "$artifact_dir/provenance.json" ]]; then
    echo "Conformance artifact directory contains previous evidence; choose an empty directory." >&2
    exit 1
fi
trap 'docker rm -f "$CONFORMANCE_CONTAINER" >/dev/null 2>&1 || true' EXIT

rc=0
CONFORMANCE_KEEP_CONTAINER=1 CONFORMANCE_REPORT_OUTPUT=/conformance-report.yaml \
    make test-gateway-conformance || rc=$?
report_rc=0
docker cp "$CONFORMANCE_CONTAINER:/conformance-report.yaml" "$artifact_dir/report.yaml" || report_rc=$?
provenance_rc=0
python3 scripts/conformance-provenance.py \
    --report "$artifact_dir/report.yaml" \
    --output "$artifact_dir/provenance.json" \
    --controller-image "$CONFORMANCE_CONTROLLER_IMAGE" \
    --implementation-version "$CONFORMANCE_IMPL_VERSION" \
    --test-exit-code "$rc" "$@" || provenance_rc=$?
(
    cd "$artifact_dir"
    files=(provenance.json)
    if [[ -s report.yaml ]]; then files+=(report.yaml); fi
    sha256sum "${files[@]}" >SHA256SUMS
)
if (( rc != 0 )); then exit "$rc"; fi
if (( report_rc != 0 )); then exit "$report_rc"; fi
exit "$provenance_rc"
