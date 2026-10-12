#!/usr/bin/env bash
set -euo pipefail

git config --global --add safe.directory "$PWD"
test "$(kind get clusters | wc -l)" -eq 0
go version
haproxy -v

case "$PILOT_SUITE" in
    integration) make test-integration; exit ;;
    agent) make test-agent-docker; exit ;;
    lint) make lint audit; exit ;;
    unit) make test; exit ;;
    chart) make lint-chart-ci chart-schema-check validate-helm-libraries; exit ;;
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
