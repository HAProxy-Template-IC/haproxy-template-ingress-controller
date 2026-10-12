#!/usr/bin/env bash
set -euo pipefail

mkdir -p .pilot
git diff --exit-code "$PILOT_SOURCE_SHA"
{
    git rev-parse HEAD
    printf 'Tested source: %s\n' "$PILOT_SOURCE_SHA"
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
if [[ "$PILOT_SUITE" != build ]]; then
    (cd pilot-image && sha256sum --check controller.tar.gz.sha256)
    docker load --input pilot-image/controller.tar.gz
fi
docker image inspect "$PILOT_CI_IMAGE" > .pilot/toolchain-image.json

started=$(date +%s)
set +e
docker run --name haptic-pilot --network host \
    --volume /var/run/docker.sock:/var/run/docker.sock \
    --volume "$PWD:$PWD" --workdir "$PWD" \
    --env PILOT_SUITE --env BASELINE_CHART_VERSION \
    --env PILOT_SOURCE_SHA --env PILOT_VARNISH_IMAGE \
    --env HAPROXY_VERSION --env KIND_NODE_IMAGE \
    --env SKIP_DOCKER_BUILD=1 --env KEEP_CLUSTER=false \
    --entrypoint bash "$PILOT_CI_IMAGE" -euo pipefail -c '
        git config --global --add safe.directory "$PWD"
        test "$(kind get clusters | wc -l)" -eq 0
        go version
        haproxy -v
        kind version
        helm version
        chart_version="0.0.0-github.g${PILOT_SOURCE_SHA:0:8}"
        sed -i "s|^version:.*|version: ${chart_version}|" charts/haptic/Chart.yaml
        yq -i '\''.cache.varnish.image = strenv(PILOT_VARNISH_IMAGE)'\'' charts/haptic/values.yaml
        if [[ "$PILOT_SUITE" != build ]]; then
            source_hash=$(scripts/source-hash.sh)
            image_hash=$(docker run --rm --entrypoint haptic haptic:test version | awk '\''$1 == "Source" && $2 == "Hash:" {print $3}'\'')
            test "$source_hash" = "$image_hash"
            printf "Verified controller source hash: %s\n" "$source_hash"
        fi
        case "$PILOT_SUITE" in
            build)
                make build-for-docker
                docker buildx build --load --platform linux/amd64 \
                    --build-context binary=dist --target runtime \
                    --build-arg HAPROXY_IMAGE="$(bash scripts/chart-haproxy-image.sh "$HAPROXY_VERSION")" \
                    --tag haptic:test .
                ;;
            integration)
                make test-integration
                ;;
            e2e)
                mkdir -p debug-logs
                go test -mod=readonly -tags=e2e -list "^Test" ./tests/e2e/... > debug-logs/e2e-test-inventory.txt
                TEST_RUN_PATTERN=$(python3 scripts/shard-go-tests.py --weights tests/e2e/shard-weights.json 1 3 < debug-logs/e2e-test-inventory.txt)
                export TEST_RUN_PATTERN
                printf "%s\n" "$TEST_RUN_PATTERN" | tee .pilot/e2e-selection.txt
                KEEP_CLUSTER=true make test-e2e
                ;;
            upgrade)
                env -u KEEP_CLUSTER make test-chart-upgrade
                ;;
            *)
                printf "Unknown pilot suite: %s\n" "$PILOT_SUITE" >&2
                exit 2
                ;;
        esac
    ' 2>&1 | tee .pilot/test.log
result=${PIPESTATUS[0]}
set -e
elapsed=$(( $(date +%s) - started ))
printf '{"exit_code":%s,"test_seconds":%s}\n' "$result" "$elapsed" > .pilot/result.json
if [[ "$PILOT_SUITE" == e2e ]]; then
    for resource in nodes pods events; do
        docker exec haptic-e2e-control-plane kubectl --kubeconfig=/etc/kubernetes/admin.conf \
            get "$resource" --all-namespaces -o wide > ".pilot/$resource.txt" 2>&1 || true
    done
    docker exec haptic-e2e-control-plane kubectl --kubeconfig=/etc/kubernetes/admin.conf \
        describe pods --all-namespaces > .pilot/pod-details.txt 2>&1 || true
    docker exec haptic-e2e-control-plane kubectl --kubeconfig=/etc/kubernetes/admin.conf \
        logs --namespace haptic --selector app.kubernetes.io/instance=haptic \
        --all-containers=true --prefix --tail=500 --max-log-requests=20 \
        > .pilot/pod-logs.txt 2>&1 || true
fi
if [[ "$result" == 0 && "$PILOT_SUITE" == build ]]; then
    mkdir -p pilot-image
    docker save haptic:test | gzip -1 > pilot-image/controller.tar.gz
    (cd pilot-image && sha256sum controller.tar.gz > controller.tar.gz.sha256)
fi
docker image inspect haptic:test > .pilot/controller-image.json
docker inspect haptic-pilot > .pilot/container.json
docker ps -a > .pilot/docker-ps.txt
docker stats --no-stream > .pilot/docker-stats.txt
df -B1 . /var/lib/docker > .pilot/disk-after.txt
sudo dmesg --ctime | tail -100 > .pilot/kernel-tail.txt
for directory in .pilot debug-logs; do
    if [[ -d "$directory" ]]; then
        sudo chown -R "$(id -u):$(id -g)" "$directory"
    fi
done
{
    printf 'Test source: `%s`\n\n' "$PILOT_SOURCE_SHA"
    printf 'Suite: `%s`, baseline: `%s`\n\n' "$PILOT_SUITE" "$BASELINE_CHART_VERSION"
    printf 'Exit code: **%s**; test container runtime: **%s seconds**\n' "$result" "$elapsed"
} >> "$GITHUB_STEP_SUMMARY"
exit "$result"
