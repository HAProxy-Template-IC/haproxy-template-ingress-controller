#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
: "${BENCH_BASE_REF:?Set BENCH_BASE_REF to the baseline commit}"
comparison_base=$(git rev-parse --verify "${BENCH_BASE_REF}^{commit}")
comparison_candidate=$(git rev-parse --verify HEAD)
[[ "$comparison_base" != "$comparison_candidate" ]] || {
    echo "Baseline and candidate must be different commits" >&2
    exit 1
}
[[ -z "$(git status --porcelain)" ]] || {
    echo "Commit changes before comparing rendered revisions" >&2
    exit 1
}
comparison_output=${BENCH_COMPARISON_OUTPUT:-build/render-comparison}
mkdir -p "$(dirname "$comparison_output")"
mkdir "$comparison_output"
comparison_output=$(realpath "$comparison_output")
comparison_work=$(mktemp -d /tmp/haptic-render-comparison.XXXXXX)
trap 'rm -rf -- "$comparison_work"' EXIT

while IFS= read -r variable; do
    unset "$variable"
done < <(compgen -v HAPTIC_BENCHMARK_)
export GOMAXPROCS=8 GOMEMLIMIT=2GiB GOFLAGS='-mod=readonly -p=2'
export HAPTIC_BENCHMARK_BARE_ENGINE=1
comparison_benchmark='^BenchmarkBundledChartHTTPRouteIncrementalRenderService$/routes=(1000|3000)$/plain/add-one$'

for revision in baseline candidate; do
    commit=$comparison_base
    [[ "$revision" != candidate ]] || commit=$comparison_candidate
    mkdir "$comparison_work/$revision"
    git archive --format=tar "$commit" > "$comparison_work/$revision.tar"
    sha256sum "$comparison_work/$revision.tar" | cut -d ' ' -f 1 > "$comparison_output/$revision-archive.sha256"
    tar -xf "$comparison_work/$revision.tar" -C "$comparison_work/$revision"
done
jq -n \
    --arg baseline "$comparison_base" --arg candidate "$comparison_candidate" \
    --arg baseline_archive "$(cat "$comparison_output/baseline-archive.sha256")" \
    --arg candidate_archive "$(cat "$comparison_output/candidate-archive.sha256")" \
    --arg go_version "$(go version)" --arg host "$(uname -sm)" \
    --arg benchmark "$comparison_benchmark" \
    --arg cpu "$(awk -F ": " '/^model name/ {print $2; exit}' /proc/cpuinfo)" '
    {schema_version: 1, baseline: $baseline, candidate: $candidate,
     archives: {baseline: $baseline_archive, candidate: $candidate_archive},
     go_version: $go_version, host: $host, cpu: $cpu,
     benchmark: $benchmark, order: ["baseline", "candidate", "candidate", "baseline"],
     iterations: 5, samples_per_process: 3, process_timeout: "5m", gomaxprocs: 8, gomemlimit: "2GiB",
     goflags: "-mod=readonly -p=2", bare_engine: true, cold_oracle: true}
' > "$comparison_output/provenance.json"

for arm in baseline-1 candidate-1 candidate-2 baseline-2; do
    revision=${arm%-*}
    date -u +%FT%TZ > "$comparison_output/$arm.started"
    result=0
    PKG=./cmd/haptic COUNT=3 BENCH="$comparison_benchmark" \
        BENCHFLAGS='-benchtime=5x' TIMEOUT=5m \
        make -C "$comparison_work/$revision" bench > "$comparison_output/$arm.log" 2>&1 || result=$?
    printf '%s\n' "$result" > "$comparison_output/$arm.exit"
    date -u +%FT%TZ > "$comparison_output/$arm.finished"
    cat "$comparison_output/$arm.log"
    [[ "$result" -eq 0 ]] || exit "$result"
done
