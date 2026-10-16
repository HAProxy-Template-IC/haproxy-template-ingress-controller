#!/usr/bin/env bash
set -euo pipefail

mkdir -p .pilot debug-logs
git diff --exit-code "$PILOT_SOURCE_SHA"
test "$(kind get clusters | wc -l)" -eq 0
{
    git rev-parse HEAD
    scripts/source-hash.sh
    lscpu
    free -b
    df -B1 . /var/lib/docker
    go version
    docker version
    helm version
    kind version
} > .pilot/host.txt 2>&1
python3 .github/scripts/runner-metrics.py .pilot/metrics.jsonl &
metrics_pid=$!
stop_background() {
    kill "$metrics_pid" ${spoa_build_pid:+-- "-$spoa_build_pid"} 2>/dev/null || true
    wait "$metrics_pid" ${spoa_build_pid:+"$spoa_build_pid"} 2>/dev/null || true
    [[ -z "${spoa_build_pid:-}" ]] || cat .pilot/spoa-build.log
}
trap stop_background EXIT

sed -i "s|^version:.*|version: 0.0.0-github.g${PILOT_SOURCE_SHA:0:8}|" charts/haptic/Chart.yaml
yq -i '.cache.varnish.image = strenv(PILOT_VARNISH_IMAGE)' charts/haptic/values.yaml
bash .github/scripts/spoa-test-image.sh select
git diff > .pilot/test-values.patch
started=$(date +%s)
build_target=docker-build-test
if [[ "${PILOT_NATIVE_BUILD:-0}" == 1 ]]; then
    build_target=docker-build-test-native
fi
setsid bash .github/scripts/spoa-test-image.sh build > .pilot/spoa-build.log 2>&1 &
spoa_build_pid=$!
make "$build_target" 2>&1 | tee .pilot/build.log
wait "$spoa_build_pid" || exit 1
spoa_build_pid=
docker image inspect haptic:test > .pilot/controller-image.json
set +e
case "$PILOT_SUITE" in
    upgrade) SKIP_DOCKER_BUILD=1 make test-chart-upgrade ;;
    gitops) GITOPS_IMAGE=haptic:test make test-gitops-lifecycle ;;
    *) printf 'Unknown lifecycle suite: %s\n' "$PILOT_SUITE" >&2; exit 2 ;;
esac 2>&1 | tee .pilot/test.log
result=${PIPESTATUS[0]}
set -e
elapsed=$(( $(date +%s) - started ))
printf '{"exit_code":%s,"test_seconds":%s}\n' "$result" "$elapsed" > .pilot/result.json
docker stats --no-stream > .pilot/docker-stats.txt
docker ps -a > .pilot/docker-ps.txt
printf 'Suite: **%s**; exit: **%s**; duration: **%s seconds**\n' "$PILOT_SUITE" "$result" "$elapsed" >> "$GITHUB_STEP_SUMMARY"
exit "$result"
