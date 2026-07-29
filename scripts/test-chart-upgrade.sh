#!/usr/bin/env bash
# Prove that `helm upgrade` from the last released chart to this working tree is
# ACCEPTED, and that a rejected upgrade leaves the live configuration exactly as
# it was.
#
# Scope, deliberately narrow: the risk this covers is the OLD controller's
# admission webhook judging the NEW chart's content, plus helm/helmfile
# completing. That is what broke twice — per-library fragments denied
# standalone, and `helm diff` failing on a CRD its own pre-upgrade hook had not
# installed yet.
#
# It does NOT wait for the fleet to converge. The webhook serves as soon as the
# controller pod is Ready, which needs neither a rendered config nor a healthy
# data plane — observed directly: the baseline controller sat 2/2 and denied an
# upgrade while HAProxy was still on its bootstrap stub. Demanding convergence
# only drags in every runtime dependency (a default-ssl-cert Secret, Gateway API
# CRDs, …), and each prop makes the cluster less like the one an operator has.
# Convergence is what tests/e2e is for.
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

# Selected by NAME, not by label. The component label is on the pods, not the
# Deployment, so a label-selected `get deploy` returns nothing and the wait
# reports "never became ready" while every replica is plainly Ready — which is
# exactly what this suite did before, sending me after a phantom product bug.
deploy_ready() {
  k get deploy -o json 2>/dev/null | python3 -c '
import json,sys
want = sys.argv[1]
for d in json.load(sys.stdin).get("items", []):
    if d["metadata"]["name"].endswith(want):
        spec = d["spec"].get("replicas", 0)
        got = d.get("status", {}).get("readyReplicas", 0)
        print("ready" if spec and got == spec else "waiting")
        sys.exit(0)
print("absent")' "$1"
}

wait_controller_ready() {
  local deadline=$((SECONDS + ${2:-300}))
  while [ $SECONDS -lt $deadline ]; do
    [ "$(deploy_ready controller)" = "ready" ] && return 0
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

info "installing baseline chart $BASELINE (the version operators upgrade FROM)"
# No --wait: HAProxy's readiness probe only passes once the controller has
# pushed it a config, and --wait blocks on every pod at once, so it deadlocks on
# its own precondition. The e2e harness omits it for the same reason. Readiness
# is polled below instead, which is also what makes the assertion meaningful.
helm install "$RELEASE" "$OCI" --version "$BASELINE" \
  --kube-context "$CTX" --namespace "$NS" --create-namespace \
  --timeout 15m >/dev/null || fail "baseline install failed"

wait_controller_ready controller 420 || fail "baseline controller never became ready"
BASELINE_FP="$(config_fingerprint)"
info "baseline healthy, config fingerprint ${BASELINE_FP:0:12}"

# ------------------------------------------------- phase 2: the real upgrade

# `helm diff` runs BEFORE helm's pre-upgrade hooks, so a chart that introduces a
# new CRD *and* a resource of that kind in one version fails here while plain
# `helm upgrade` succeeds — the hook has not installed the CRD yet. That is the
# path helmfile and most GitOps flows take, and it is how a real upgrade broke
# after this suite passed.
# Installed rather than skipped when absent: a leg that quietly disappears when a
# plugin is missing is worth nothing — this one exists precisely because the
# defect it catches got past a suite that looked green.
if ! helm plugin list 2>/dev/null | grep -q '^diff'; then
  info "installing the helm-diff plugin"
  helm plugin install --verify=false https://github.com/databus23/helm-diff >/dev/null 2>&1 \
    || fail "could not install helm-diff; this suite must exercise the diff path, not skip it"
fi

if helm plugin list 2>/dev/null | grep -q '^diff'; then
  info "diffing the upgrade (the path helmfile takes, before any hook has run)"
  if ! helm diff upgrade "$RELEASE" "$CHART" \
        --kube-context "$CTX" --namespace "$NS" \
        --set controller.image.repository=haptic \
        --set controller.image.tag=test \
        --set "haproxyVersion=$HAPROXY_VERSION" \
        --dry-run=server > "$WORK/diff.log" 2>&1; then
    echo "--- helm diff output ---"; tail -20 "$WORK/diff.log"
    fail "helm diff failed before the upgrade: a GitOps flow would stop here even though helm upgrade alone would succeed"
  fi
else
  fail "helm-diff is still unavailable after install; refusing to skip the diff leg"
fi

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

wait_controller_ready controller 420 || fail "controller not ready after upgrade"

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

wait_controller_ready controller 180 || fail "controller unhealthy after the rejected upgrade"

info "ALL CHECKS PASSED"
