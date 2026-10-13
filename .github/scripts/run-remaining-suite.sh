#!/usr/bin/env bash
set -euo pipefail

git config --global --add safe.directory "$PWD"
test "$(kind get clusters | wc -l)" -eq 0
go version
haproxy -v

case "$PILOT_SUITE" in
    gateway-benchmark)
        BENCH_CI_OUTPUT="$PWD/.pilot/gateway-benchmark" make bench-gateway-api-ci
        exit
        ;;
    integration) KEEP_CLUSTER=false make test-integration; exit ;;
    integration-coverage) KEEP_CLUSTER=false make test-integration-coverage; cp -r coverage .pilot/; exit ;;
    unit-shard|unit-cache)
        go list ./... > .pilot/unit-packages-all.txt
        python3 .github/scripts/select-unit-packages.py
        PKG=$(tr '\n' ' ' < .pilot/unit-packages-selected.txt)
        export PKG
        export GOFLAGS=-p=4
        if [[ "$PILOT_SUITE" == unit-cache ]]; then
            export TEST_RUN_PATTERN='^$'
        elif [[ "$SHARD_ID" == command ]]; then
            go test -list . ./cmd/haptic > .pilot/unit-command-inventory.txt
            TEST_RUN_PATTERN=$(python3 scripts/shard-go-tests.py --kinds Test Example Fuzz --weights .github/unit-command-weights.json "$SHARD_INDEX" 3 < .pilot/unit-command-inventory.txt)
            export TEST_RUN_PATTERN
            printf '%s\n' "$TEST_RUN_PATTERN" > .pilot/unit-command-selection.txt
        fi
        make test-unit COUNT=1
        cp report.xml .pilot/unit-report.xml
        exit
        ;;
    coverage-shard)
        go list ./... > .pilot/unit-packages-all.txt
        python3 .github/scripts/select-coverage-packages.py
        PKG=$(tr '\n' ' ' < .pilot/unit-packages-selected.txt)
        export PKG
        GOFLAGS=-p=4 make test-coverage-unit
        cp coverage.out coverage.txt .pilot/
        exit
        ;;
    test-support)
        go mod download sigs.k8s.io/gateway-api/conformance
        SHARD_CONFORMANCE_TESTS_REQUIRED=1 make test-support test-github-verification
        exit
        ;;
    agent) make test-agent-docker; exit ;;
    lint) make lint audit; exit ;;
    unit) make test; exit ;;
    unit-renderer) GOFLAGS=-p=1 PKG=./pkg/controller/renderer TEST_RUN_PATTERN=TestGatewayFeaturePublicationsScaleByChangedGateway make test-unit; exit ;;
    integration-diagnosis)
        set +e
        KEEP_CLUSTER=true TEST_RUN_PATTERN=TestSyncBackends make test-integration
        result=$?
        set -e
        docker exec haproxy-test-control-plane kubectl --kubeconfig=/etc/kubernetes/admin.conf get pods -A -o json > .pilot/integration-pods.json
        python3 .github/scripts/collect-integration-startup.py
        exit "$result"
        ;;
    chart) make lint-chart-ci chart-schema-check; exit ;;
    vector) make test-vector-spans test-vector-metrics; exit ;;
esac

chart_version="0.0.0-github.g${PILOT_SOURCE_SHA:0:8}"
sed -i "s|^version:.*|version: ${chart_version}|" charts/haptic/Chart.yaml
yq -i '.cache.varnish.image = strenv(PILOT_VARNISH_IMAGE)' charts/haptic/values.yaml
make docker-build-test
docker image inspect haptic:test > .pilot/controller-image.json

case "$PILOT_SUITE" in
    upgrade) SKIP_DOCKER_BUILD=1 make test-chart-upgrade ;;
    plain) SKIP_DOCKER_BUILD=1 make test-install-without-gateway-api ;;
    gitops) GITOPS_IMAGE=haptic:test make test-gitops-lifecycle ;;
    acceptance) make test-acceptance-parallel ;;
    defaults) HELM_DEFAULTS_IMAGE=haptic:test make test-helm-defaults ;;
    ingress)
        export HAPTIC_E2E_PROFILE=conformance
        export HAPTIC_E2E_CONTROLLER_STARTUP=staged
        export HAPTIC_E2E_EXTRA_SET='controller.resources.limits.cpu=4;controller.resources.limits.memory=2Gi'
        yq -i '.controller.extraEnv += [{"name": "GOMAXPROCS", "value": "4"}, {"name": "GOGC", "value": "300"}]' scripts/dev-env-assets/conformance-values.yaml
        KEEP_CLUSTER=true SKIP_DOCKER_BUILD=1 TEST_RUN_PATTERN='^$' make test-e2e
        make test-ingress-conformance
        ;;
    *) printf 'Unknown suite: %s\n' "$PILOT_SUITE" >&2; exit 2 ;;
esac
