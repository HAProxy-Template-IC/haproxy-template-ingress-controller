#!/usr/bin/env bash
set -euo pipefail

mkdir -p .pilot debug-logs
git diff --exit-code "$PILOT_SOURCE_SHA"
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
trap 'kill "$metrics_pid" 2>/dev/null || true; wait "$metrics_pid" 2>/dev/null || true' EXIT

test "$(kind get clusters | wc -l)" -eq 0
chart_version="0.0.0-github.g${PILOT_SOURCE_SHA:0:8}"
sed -i "s|^version:.*|version: ${chart_version}|" charts/haptic/Chart.yaml
yq -i '.cache.varnish.image = strenv(PILOT_VARNISH_IMAGE)' charts/haptic/values.yaml
yq -i '.controller.extraEnv += [{"name": "GOMAXPROCS", "value": "2"}, {"name": "GOGC", "value": strenv(PILOT_GOGC)}]' scripts/dev-env-assets/e2e-values.yaml
make docker-build-test 2>&1 | tee .pilot/build.log
docker image inspect haptic:test > .pilot/controller-image.json
python3 .github/scripts/profile-startup.py > .pilot/startup-profile.log 2>&1 &
profile_pid=$!
go test -mod=readonly -tags=e2e -list '^Test' ./tests/e2e/... > debug-logs/e2e-test-inventory.txt
TEST_RUN_PATTERN=$(python3 scripts/shard-go-tests.py --weights tests/e2e/shard-weights.json 1 3 < debug-logs/e2e-test-inventory.txt)
export TEST_RUN_PATTERN
printf '%s\n' "$TEST_RUN_PATTERN" > .pilot/e2e-selection.txt
started=$(date +%s)
set +e
KEEP_CLUSTER=true SKIP_DOCKER_BUILD=1 make test-e2e 2>&1 | tee .pilot/test.log
result=${PIPESTATUS[0]}
set -e
elapsed=$(( $(date +%s) - started ))
printf '{"exit_code":%s,"test_seconds":%s}\n' "$result" "$elapsed" > .pilot/result.json
wait "$profile_pid" || true
docker exec haptic-e2e-control-plane kubectl --kubeconfig=/etc/kubernetes/admin.conf describe pods --all-namespaces > .pilot/pod-details.txt 2>&1 || true
for previous in current previous; do
    flags=()
    if [[ "$previous" == previous ]]; then flags+=(--previous); fi
    docker exec haptic-e2e-control-plane kubectl --kubeconfig=/etc/kubernetes/admin.conf \
        logs --namespace haptic --selector app.kubernetes.io/component=controller \
        --container controller --prefix --tail=-1 --max-log-requests=20 "${flags[@]}" \
        > ".pilot/$previous-controller-logs.txt" 2>&1 || true
done
sudo chown -R "$(id -u):$(id -g)" .pilot debug-logs
printf 'Native ARM E2E exit: **%s**; duration: **%s seconds**\n' "$result" "$elapsed" >> "$GITHUB_STEP_SUMMARY"
exit "$result"
