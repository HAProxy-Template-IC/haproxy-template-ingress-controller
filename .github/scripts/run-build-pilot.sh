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
    docker version
} > .pilot/host.txt 2>&1
python3 .github/scripts/runner-metrics.py .pilot/metrics.jsonl &
metrics_pid=$!
trap 'kill "$metrics_pid" 2>/dev/null || true; wait "$metrics_pid" 2>/dev/null || true' EXIT
docker pull "$PILOT_CI_IMAGE"
docker image inspect "$PILOT_CI_IMAGE" > .pilot/toolchain-image.json
started=$(date +%s)
set +e
docker run --name haptic-pilot --network host \
    --volume /var/run/docker.sock:/var/run/docker.sock \
    --volume "$PWD:$PWD" --workdir "$PWD" \
    --env PILOT_SUITE --env PILOT_SOURCE_SHA --env PILOT_VARNISH_IMAGE \
    --env GITHUB_RUN_ID --env GITHUB_JOB \
    --env HAPROXY_VERSION --env KIND_NODE_IMAGE --env BASELINE_CHART_VERSION \
    --env GITOPS_PROVIDER --env GITOPS_CERTIFICATES --env SHARD_ID --env SHARD_COUNT \
    --entrypoint bash "$PILOT_CI_IMAGE" .github/scripts/run-build-suite.sh \
    2>&1 | tee .pilot/test.log
result=${PIPESTATUS[0]}
set -e
elapsed=$(( $(date +%s) - started ))
printf '{"exit_code":%s,"test_seconds":%s}\n' "$result" "$elapsed" > .pilot/result.json
docker stats --no-stream > .pilot/docker-stats.txt
docker ps -a > .pilot/docker-ps.txt
sudo chown -R "$(id -u):$(id -g)" .pilot debug-logs
printf 'Suite: **%s**; exit: **%s**; duration: **%s seconds**\n' "$PILOT_SUITE" "$result" "$elapsed" >> "$GITHUB_STEP_SUMMARY"
exit "$result"
