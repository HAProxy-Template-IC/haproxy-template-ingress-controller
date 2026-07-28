#!/usr/bin/env bash
# Prove that `helm upgrade` from the last released chart to this working tree
# succeeds, and that a rejected upgrade leaves the live configuration exactly as
# it was.
#
# This suite owns its own kind cluster. It cannot share the e2e cluster: it
# installs a *released* chart first, whose pre-upgrade hook applies that
# release's CRDs, and CRDs are cluster-scoped — doing that under the e2e suite
# would downgrade the schemas out from under it.
#
# Usage: scripts/test-chart-upgrade.sh [--keep]
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART="$REPO/charts/haptic"
CLUSTER="${UPGRADE_CLUSTER_NAME:-haptic-upgrade}"
CTX="kind-$CLUSTER"
NS="${UPGRADE_NAMESPACE:-haptic}"
RELEASE="${UPGRADE_RELEASE_NAME:-haptic}"
OCI="oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic"
WORK="$(mktemp -d)"
KEEP=false
[ "${1:-}" = "--keep" ] && KEEP=true

# The baseline is the newest released tag, not a hand-maintained constant: a
# release that forgets to update this file would otherwise silently keep
# testing an ever-older upgrade path.
BASELINE="${BASELINE_CHART_VERSION:-$(git -C "$REPO" tag -l 'v*' | sort -V | tail -1 | sed 's/^v//')}"
[ -n "$BASELINE" ] || { echo "FAIL: cannot determine baseline chart version" >&2; exit 1; }

fail() { echo "FAIL: $*" >&2; exit 1; }
info() { echo "==> $*"; }

cleanup() {
  local rc=$?
  rm -rf "$WORK"
  if [ "$KEEP" = false ]; then
    kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
  else
    echo "kept cluster $CLUSTER (--keep)"
  fi
  exit $rc
}
trap cleanup EXIT

k() { kubectl --context "$CTX" -n "$NS" "$@"; }

# Fingerprint of what the fleet is actually configured by: the merged spec the
# controller would load. Compared before and after a rejected upgrade — equality
# is the contract that a failed upgrade changes nothing.
config_fingerprint() {
  kubectl --context "$CTX" -n "$NS" get haproxytemplateconfig -o json 2>/dev/null \
    | python3 -c 'import json,sys,hashlib
d=json.load(sys.stdin)
specs=sorted((i["metadata"]["name"], json.dumps(i.get("spec",{}),sort_keys=True)) for i in d["items"])
print(hashlib.sha256(json.dumps(specs).encode()).hexdigest())'
}

wait_controller_ready() {
  local deadline=$((SECONDS + ${1:-300}))
  while [ $SECONDS -lt $deadline ]; do
    local want got
    want=$(k get deploy -l app.kubernetes.io/component=controller -o jsonpath='{.items[0].spec.replicas}' 2>/dev/null || echo "")
    got=$(k get deploy -l app.kubernetes.io/component=controller -o jsonpath='{.items[0].status.readyReplicas}' 2>/dev/null || echo 0)
    if [ -n "$want" ] && [ "${got:-0}" = "$want" ]; then return 0; fi
    sleep 5
  done
  k get pods
  return 1
}

# ----------------------------------------------------------------- cluster

info "cluster $CLUSTER"
kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
kind create cluster --name "$CLUSTER" >/dev/null
kubectl --context "$CTX" wait --for=condition=Ready node --all --timeout=180s >/dev/null

info "loading haptic:test"
docker image inspect haptic:test >/dev/null 2>&1 \
  || fail "haptic:test not found — run 'make docker-build-test' first"
# `|| true` on both: under `set -o pipefail` a grep that legitimately matches
# nothing fails the pipeline and kills the script with no diagnostic.
HAPROXY_VERSION="$(grep -oP 'DEFAULT_HAPROXY\s*=\s*\K[0-9.]+' "$REPO/Dockerfile" 2>/dev/null | head -1 || true)"
HAPROXY_VERSION="${HAPROXY_VERSION:-$(sed -n 's/^haproxyVersion: *"\?\([0-9.]*\)"\?/\1/p' "$CHART/values.yaml" | head -1 || true)}"
[ -n "$HAPROXY_VERSION" ] || fail "cannot determine haproxyVersion"
docker tag haptic:test "haptic:test-haproxy${HAPROXY_VERSION}" >/dev/null
kind load docker-image "haptic:test-haproxy${HAPROXY_VERSION}" --name "$CLUSTER" >/dev/null
kind load docker-image haptic:test --name "$CLUSTER" >/dev/null

# ------------------------------------------------- phase 1: released baseline

# The released chart enables the gateway library unconditionally (its _helm_load
# predicate is values-only, with no .Capabilities guard), and that library's
# typed-access templates do not compile unless the Gateway API schemas are
# resolvable. Without these CRDs the baseline install never converges — HAProxy
# is left serving a stub — so the upgrade under test could never start.
# Installing them here keeps this suite testing the UPGRADE. The bare-cluster
# case is a separate defect with its own test; do not paper over it here.
GATEWAY_API_VERSION="${GATEWAY_API_VERSION:-v1.2.1}"
info "installing Gateway API $GATEWAY_API_VERSION (baseline requires it to converge)"
kubectl --context "$CTX" apply -f \
  "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml" \
  >/dev/null 2>&1 || fail "could not install Gateway API CRDs"
kubectl --context "$CTX" wait --for=condition=Established \
  crd/gatewayclasses.gateway.networking.k8s.io \
  crd/gateways.gateway.networking.k8s.io \
  crd/httproutes.gateway.networking.k8s.io --timeout=120s >/dev/null \
  || fail "Gateway API CRDs did not establish"

info "installing baseline chart $BASELINE (the version operators upgrade FROM)"
helm install "$RELEASE" "$OCI" --version "$BASELINE" \
  --kube-context "$CTX" --namespace "$NS" --create-namespace \
  --wait --timeout 15m >/dev/null || fail "baseline install failed"

wait_controller_ready 300 || fail "baseline controller never became ready"
BASELINE_FP="$(config_fingerprint)"
info "baseline healthy, config fingerprint ${BASELINE_FP:0:12}"

# ------------------------------------------------- phase 2: the real upgrade

info "upgrading to the working tree chart"
if ! helm upgrade "$RELEASE" "$CHART" \
      --kube-context "$CTX" --namespace "$NS" \
      --set controller.image.repository=haptic \
      --set controller.image.tag=test \
      --set "haproxyVersion=$HAPROXY_VERSION" \
      --wait --timeout 20m > "$WORK/upgrade.log" 2>&1; then
  echo "--- helm output ---"; cat "$WORK/upgrade.log"
  fail "helm upgrade from $BASELINE failed — this is the upgrade every operator runs"
fi

status=$(helm --kube-context "$CTX" -n "$NS" status "$RELEASE" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)["info"]["status"])')
[ "$status" = "deployed" ] || fail "release status is '$status', expected 'deployed'"

wait_controller_ready 420 || fail "controller not ready after upgrade"

# The controller must consider the config it loaded valid. A crash-loop or a
# Validated=False here means the upgrade shipped a config the load gate rejects.
validated=$(k get haproxytemplateconfig -o json \
  | python3 -c 'import json,sys
items=json.load(sys.stdin)["items"]
c=[c for i in items for c in i.get("status",{}).get("conditions",[]) if c["type"]=="Validated"]
print(c[0]["status"] if c else "MISSING")')
[ "$validated" = "True" ] || fail "config Validated=$validated after upgrade"

restarts=$(k get pods -l app.kubernetes.io/component=controller \
  -o jsonpath='{range .items[*]}{.status.containerStatuses[*].restartCount}{"\n"}{end}' | tr -s ' \n' '+' | sed 's/+$//')
[ "$(( ${restarts:-0} ))" -eq 0 ] || fail "controller restarted ${restarts} times after upgrade (crash-loop?)"

info "upgrade OK: release deployed, controller ready, config validated, no restarts"

# ------------------------------- phase 3: a rejected upgrade must change nothing

info "negative case: an upgrade carrying a broken template must be rejected"
cp -r "$CHART" "$WORK/broken-chart"

# base.yaml, named explicitly. A `find ... | head -1` here is filesystem-ordered
# and matches 14 files, including tests/library_loader_test.yaml — a
# helm-unittest fixture that is never rendered — and libraries an operator can
# disable. Corrupting one of those makes the upgrade legitimately succeed and
# this suite report a gate failure that did not happen.
BROKEN_LIB="$WORK/broken-chart/libraries/base.yaml"
[ -f "$BROKEN_LIB" ] || fail "libraries/base.yaml not found — this suite corrupts it deliberately"

# A template that cannot compile. The gate must catch this; if the upgrade
# succeeds, HAPTIC shipped an unrenderable config to a live fleet.
#
# It goes into haproxyConfig.template, NOT a new templateSnippet: an
# unreferenced snippet is never compiled, so a broken one validates clean
# (measured — 354/354 tests passed with `{%- var x = %}` sitting in the
# rendered config). haproxyConfig.template is the one template guaranteed to be
# compiled and rendered, so breaking it is the only injection that actually
# tests the gate rather than the harness.
python3 - "$BROKEN_LIB" <<'PY'
import sys, pathlib, re
p = pathlib.Path(sys.argv[1]); s = p.read_text()
m = re.search(r"^haproxyConfig:\n(\s+)template: \|\n", s, re.M)
if not m:
    sys.exit(f"{p} has no haproxyConfig.template to corrupt — this suite depends on it")
p.write_text(s[:m.end()] + m.group(1) + "  {%- var x = %}\n" + s[m.end():])
PY

PRE_FP="$(config_fingerprint)"
if helm upgrade "$RELEASE" "$WORK/broken-chart" \
      --kube-context "$CTX" --namespace "$NS" \
      --set controller.image.repository=haptic \
      --set controller.image.tag=test \
      --set "haproxyVersion=$HAPROXY_VERSION" \
      --timeout 10m > "$WORK/broken.log" 2>&1; then
  fail "an upgrade carrying an uncompilable template was ACCEPTED — the gate did not hold"
fi
info "rejected, as required"

POST_FP="$(config_fingerprint)"
[ "$PRE_FP" = "$POST_FP" ] \
  || fail "a rejected upgrade mutated the live config (${PRE_FP:0:12} -> ${POST_FP:0:12}); it must be a no-op"

wait_controller_ready 180 || fail "controller unhealthy after the rejected upgrade"

info "ALL CHECKS PASSED"
