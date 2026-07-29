#!/usr/bin/env bash
# Install the chart with default values on a cluster that has NO Gateway API
# CRDs, and assert HAPTIC actually serves a rendered configuration.
#
# Every other suite installs Gateway API first, which is why this shipped
# broken: in v0.2.0-alpha.1 on a bare cluster the gateway library is merged
# regardless (its _helm_load predicate is values-only), its typed-access
# templates cannot compile without the gateway schemas, the webhook then denies
# every apply of the config, and HAProxy is left serving the bootstrap stub.
#
# The failure is invisible to liveness-shaped checks: controller pods sat 2/2
# Running with 0 restarts for 43 minutes while HAProxy served 30 lines. So this
# asserts the OUTCOME — HAProxy Ready and a config that HAPTIC actually
# rendered — never merely that pods are up.
#
# Usage: scripts/test-install-without-gateway-api.sh [--keep]
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART="$REPO/charts/haptic"
CLUSTER="${PLAIN_CLUSTER_NAME:-haptic-plain}"
CTX="kind-$CLUSTER"
NS=haptic
KEEP=false
[ "${1:-}" = "--keep" ] && KEEP=true

fail() { echo "FAIL: $*" >&2; exit 1; }
info() { echo "==> $*"; }

# shellcheck source=scripts/lib/cluster.sh
. "$REPO/scripts/lib/cluster.sh"

cleanup() {
  local rc=$?
  if [ "$KEEP" = false ]; then kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
  else echo "kept cluster $CLUSTER (--keep)"; fi
  exit $rc
}
trap cleanup EXIT

k() { kubectl --context "$CTX" -n "$NS" "$@"; }

docker image inspect haptic:test >/dev/null 2>&1 \
  || fail "haptic:test not found — run 'make docker-build-test' first"

# `|| true`: under `set -o pipefail` a grep that legitimately matches nothing
# would kill the script with no diagnostic.
HAPROXY_VERSION="$(sh -c '. '"$REPO"'/versions.env && echo $DEFAULT_HAPROXY' 2>/dev/null || true)"
HAPROXY_VERSION="${HAPROXY_VERSION:-$(sed -n 's/^haproxyVersion: *"\?\([0-9.]*\)"\?.*/\1/p' "$CHART/values.yaml" | head -1 || true)}"
[ -n "$HAPROXY_VERSION" ] || fail "cannot determine haproxyVersion"

info "cluster $CLUSTER (deliberately WITHOUT Gateway API)"
kind_create_cluster "$CLUSTER" || fail "could not create the kind cluster"
kubectl --context "$CTX" wait --for=condition=Ready node --all --timeout=180s >/dev/null

# The premise of this test. If something ever installs Gateway API into this
# cluster, the test silently stops testing anything.
if kubectl --context "$CTX" get crd gatewayclasses.gateway.networking.k8s.io >/dev/null 2>&1; then
  fail "Gateway API is installed — this suite must run on a cluster without it"
fi

docker tag haptic:test "haptic:test-haproxy${HAPROXY_VERSION}" >/dev/null
kind load docker-image "haptic:test-haproxy${HAPROXY_VERSION}" --name "$CLUSTER" >/dev/null

info "installing the chart with DEFAULT values"
# No --wait: HAProxy readiness depends on the controller having pushed a config,
# and helm --wait blocks on all pods at once. We poll the outcome ourselves,
# which is also what makes the assertions below meaningful.
helm install haptic "$CHART" \
  --kube-context "$CTX" --namespace "$NS" --create-namespace \
  --set controller.image.repository=haptic \
  --set controller.image.tag=test \
  --set "haproxyVersion=$HAPROXY_VERSION" \
  --timeout 10m >/dev/null || fail "helm install failed"

info "waiting for HAProxy to become Ready (needs a config pushed to it)"
deadline=$((SECONDS + 420))
ready=false
while [ $SECONDS -lt $deadline ]; do
  total=$(k get pods -l app.kubernetes.io/component=loadbalancer --no-headers 2>/dev/null | wc -l)
  up=$(k get pods -l app.kubernetes.io/component=loadbalancer --no-headers 2>/dev/null \
        | awk '{split($2,a,"/"); if (a[1]==a[2] && a[1]!="0") c++} END {print c+0}')
  if [ "${total:-0}" -gt 0 ] && [ "$up" = "$total" ]; then ready=true; break; fi
  sleep 5
done

if [ "$ready" != true ]; then
  echo "--- pods ---"; k get pods
  echo "--- controller log (errors) ---"
  pod=$(k get pods -l app.kubernetes.io/component=controller --no-headers -o custom-columns=N:.metadata.name | head -1)
  [ -n "$pod" ] && k logs "$pod" -c controller --tail=60 2>/dev/null | grep -iE '"level":"(ERROR|WARN)"|error' | tail -15
  fail "HAProxy never became Ready — HAPTIC did not converge on a cluster without Gateway API"
fi

# Ready alone is not the contract: assert HAProxy is serving a config HAPTIC
# rendered, not the bootstrap stub it starts with.
hp=$(k get pods -l app.kubernetes.io/component=loadbalancer --no-headers -o custom-columns=N:.metadata.name | head -1)
[ -n "$hp" ] || fail "no HAProxy pod found"
cfg="$(k exec "$hp" -c haproxy -- cat /etc/haproxy/haproxy.cfg 2>/dev/null || true)"
[ -n "$cfg" ] || fail "could not read haproxy.cfg"

# `default-path origin` is emitted only by the base library's rendered global
# section (charts/haptic/libraries/base.yaml), so its presence distinguishes a
# HAPTIC render from the image's bootstrap config.
grep -q "default-path origin" <<<"$cfg" \
  || { printf '%s\n' "$cfg" | head -40; fail "haproxy.cfg is not a HAPTIC render (no 'default-path origin') — the stub is being served"; }
grep -qE '^frontend ' <<<"$cfg" \
  || { printf '%s\n' "$cfg" | head -40; fail "haproxy.cfg has no frontend — nothing would be routed"; }

lines=$(printf '%s\n' "$cfg" | wc -l)
info "haproxy.cfg is a real render ($lines lines)"

validated=$(wait_config_validated 180) || fail "config condition Validated=$validated (expected True)"

restarts=$(k get pods -l app.kubernetes.io/component=controller \
  -o jsonpath='{range .items[*]}{.status.containerStatuses[*].restartCount}{"\n"}{end}' 2>/dev/null \
  | tr -s ' \n' '+' | sed 's/+$//')
[ "$(( ${restarts:-0} ))" -eq 0 ] || fail "controller restarted ${restarts} times"

info "PASS — default install converges and serves a rendered config with no Gateway API present"
