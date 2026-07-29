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
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.16.2}"
WORK="$(mktemp -d)"
KEEP=false
[ "${1:-}" = "--keep" ] && KEEP=true

# The baseline is the newest STABLE chart in the registry — the version an
# operator can actually be running — discovered rather than hand-maintained, so
# a release that forgets this file cannot leave the suite testing an ever-older
# upgrade.
#
# From the registry, NOT from git tags: 0.1.0 is published but was never tagged
# `v0.1.0` (only its alphas were), so a tag-derived baseline silently tested a
# pre-release-to-pre-release upgrade and never the one operators perform.
# Pre-releases are excluded for the same reason.
CHART_REPO_PATH="haproxy-haptic/haptic/charts/haptic"

# EVERY published stable release, oldest first — not just the newest.
#
# An operator upgrades from whatever they are running, which is not necessarily
# the previous release. Testing only the newest one also lets a version-specific
# migration quietly stop being exercised the moment a new release lands: the
# cert-manager Secret adoption below exists for 0.1.0, and pinning the baseline
# to "newest" would have retired its only test on the day 0.2.0 shipped, leaving
# a special case in the product that nothing runs. See the no-can-kicking rule
# in charts/CLAUDE.md.
discover_baselines() {
  local token
  token=$(curl -sf "https://gitlab.com/jwt/auth?service=container_registry&scope=repository:${CHART_REPO_PATH}:pull" 2>/dev/null \
    | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])' 2>/dev/null) || return 1
  [ -n "$token" ] || return 1
  curl -sf -H "Authorization: Bearer $token" \
    "https://registry.gitlab.com/v2/${CHART_REPO_PATH}/tags/list?n=10000" 2>/dev/null \
    | python3 -c '
import json, re, sys
tags = json.load(sys.stdin).get("tags", [])
stable = [t for t in tags if re.match(r"^\d+\.\d+\.\d+$", t)]
stable.sort(key=lambda v: tuple(int(x) for x in v.split(".")))
print("\n".join(stable))'
}

# One baseline per invocation. With none pinned, re-exec once per discovered
# release rather than restructuring the phases into a loop: each pass then gets
# its own fresh cluster for free, which it needs anyway because a baseline's
# pre-upgrade hook installs ITS cluster-scoped CRDs.
if [ -z "${BASELINE_CHART_VERSION:-}" ]; then
  ALL="$(discover_baselines)"
  [ -n "$ALL" ] || { echo "FAIL: could not discover any published stable chart version to upgrade FROM. Set BASELINE_CHART_VERSION to override." >&2; exit 1; }
  echo "==> testing upgrades from every published stable release: $(echo "$ALL" | tr '\n' ' ')"
  rc=0
  for v in $ALL; do
    echo "==> ================ baseline $v ================"
    BASELINE_CHART_VERSION="$v" "$0" "$@" || { rc=1; break; }
  done
  exit $rc
fi
BASELINE="$BASELINE_CHART_VERSION"

fail() { echo "FAIL: $*" >&2; exit 1; }
info() { echo "==> $*"; }

# shellcheck source=scripts/lib/cluster.sh
. "$REPO/scripts/lib/cluster.sh"

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

# Takes a timeout only. The component is fixed because the name says so —
# deploy_ready above is the parameterised seam if another workload ever needs
# one. It previously took a component name it then ignored, which is the shape
# that reads as correct at every call site and silently waits on the wrong thing
# at the first one that passes something else.
wait_controller_ready() {
  local deadline=$((SECONDS + ${1:-300}))
  while [ $SECONDS -lt $deadline ]; do
    [ "$(deploy_ready controller)" = "ready" ] && return 0
    sleep 5
  done
  k get pods
  return 1
}

# ----------------------------------------------------------------- cluster

info "cluster $CLUSTER"
kind_create_cluster "$CLUSTER" || fail "could not create the kind cluster"
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

# 0.1.0 provisions its webhook certificate through cert-manager and renders the
# Certificate only when that API is present; without it the Secret never exists
# and its controller waits on the volume forever. That is a prerequisite of the
# version being upgraded FROM — an operator running it has cert-manager — not a
# prop to get past a failure, and the upgrade therefore also crosses a change of
# webhook-cert provider (the current chart self-signs its own Secret).
#
# Conditional on the baseline actually asking for it, so this retires itself
# once the newest stable release stops needing cert-manager.
# Ask the precise question: rendered for a BARE cluster, does the baseline
# produce its webhook-cert Secret itself? 0.1.0 does not (it leaves that to
# cert-manager); 0.2.0-alpha.1 does. Testing for a Certificate under
# --api-versions instead would over-trigger, since both charts prefer
# cert-manager when it happens to be available.
baseline_needs_cert_manager() {
  ! helm template "$RELEASE" "$OCI" --version "$BASELINE" --namespace "$NS" 2>/dev/null \
    | yq 'select(.kind == "Secret" and (.metadata.name | test("webhook-cert"))) | .metadata.name' 2>/dev/null \
    | grep -q .
}

if baseline_needs_cert_manager; then
  info "installing cert-manager $CERT_MANAGER_VERSION (chart $BASELINE requires it for its webhook cert)"
  kubectl --context "$CTX" apply -f \
    "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml" \
    >/dev/null 2>&1 || fail "could not install cert-manager"
  for d in cert-manager cert-manager-webhook cert-manager-cainjector; do
    kubectl --context "$CTX" -n cert-manager rollout status "deploy/$d" --timeout=5m >/dev/null \
      || fail "cert-manager deployment $d never became ready"
  done
fi

info "installing baseline chart $BASELINE (the version operators upgrade FROM)"
# No --wait: HAProxy's readiness probe only passes once the controller has
# pushed it a config, and --wait blocks on every pod at once, so it deadlocks on
# its own precondition. The e2e harness omits it for the same reason. Readiness
# is polled below instead, which is also what makes the assertion meaningful.
helm install "$RELEASE" "$OCI" --version "$BASELINE" \
  --kube-context "$CTX" --namespace "$NS" --create-namespace \
  --timeout 15m >/dev/null || fail "baseline install failed"

wait_controller_ready 420 || fail "baseline controller never became ready"
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
have_diff_plugin() { helm plugin list 2>/dev/null | grep -q '^diff'; }

if ! have_diff_plugin; then
  info "installing the helm-diff plugin"
  # helm 4 verifies plugin signatures and needs --verify=false for an unsigned
  # one; helm 3 has no such flag and errors on it. Try both rather than parse a
  # version, and judge by whether the plugin is THERE — an attempt that half
  # succeeds makes the next one exit non-zero with "plugin already exists".
  helm plugin install https://github.com/databus23/helm-diff > "$WORK/plugin.log" 2>&1 || true
  if ! have_diff_plugin; then
    helm plugin install --verify=false https://github.com/databus23/helm-diff >> "$WORK/plugin.log" 2>&1 || true
  fi
  if ! have_diff_plugin; then
    echo "--- helm plugin install output ---"; cat "$WORK/plugin.log"
    fail "could not install helm-diff; this suite must exercise the diff path, not skip it"
  fi
fi

if have_diff_plugin; then
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
# No --wait here either, for the reason phase 1 gives: it blocks on every pod at
# once, so the whole fleet's images must land before it returns. On a developer
# machine those are cached and it passes in ~2 minutes; in CI the kind node pulls
# haproxy, vector and spoa-hub fresh and it sat until the 20-minute deadline.
# Nothing is lost — the release status, controller readiness, Validated=True and
# the restart count are all asserted explicitly below, which is both narrower
# than --wait and what this suite actually claims to check.
if ! helm upgrade "$RELEASE" "$CHART" \
      --kube-context "$CTX" --namespace "$NS" \
      --set controller.image.repository=haptic \
      --set controller.image.tag=test \
      --set "haproxyVersion=$HAPROXY_VERSION" \
      --timeout 20m > "$WORK/upgrade.log" 2>&1; then
  echo "--- helm output ---"; cat "$WORK/upgrade.log"
  dump_pod_diagnostics
  fail "helm upgrade from $BASELINE failed — this is the upgrade every operator runs"
fi

status=$(helm --kube-context "$CTX" -n "$NS" status "$RELEASE" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)["info"]["status"])')
[ "$status" = "deployed" ] || fail "release status is '$status', expected 'deployed'"

# Readiness is not enough here: during a rolling update readyReplicas can equal
# replicas while OLD pods are still serving, and the old controller is still the
# leader writing status. It cannot compile the new chart's templates — the whole
# reason this suite exists — so it writes Validated=False, and reading status
# then reports a failed upgrade that is really an in-progress one. Observed in
# CI, where the rollout is slow enough for the window to be wide.
#
# rollout status is the precise wait: it returns only when every replica is the
# NEW revision, so the verdict below is the new controller's.
CONTROLLER_DEPLOY="$(controller_deploy)"
[ -n "$CONTROLLER_DEPLOY" ] || fail "cannot find the controller deployment"
k rollout status "deploy/$CONTROLLER_DEPLOY" --timeout=7m >/dev/null \
  || { dump_pod_diagnostics; dump_config_state; \
       fail "controller rollout did not complete after upgrade"; }

# The controller must consider the config it loaded valid. A crash-loop or a
# Validated=False here means the upgrade shipped a config the load gate rejects.
validated=$(wait_config_validated 180) || fail "config Validated=$validated after upgrade"

restarts=$(k get pods -l app.kubernetes.io/component=controller \
  -o jsonpath='{range .items[*]}{.status.containerStatuses[*].restartCount}{"\n"}{end}' | tr -s ' \n' '+' | sed 's/+$//')
[ "$(( ${restarts:-0} ))" -eq 0 ] || fail "controller restarted ${restarts} times after upgrade (crash-loop?)"

info "upgrade OK: release deployed, controller ready, config validated, no restarts"

# ------------------------------- phase 3: a rejected upgrade must change nothing

# Pod readiness does NOT mean the admission webhook is listening: /healthz is
# answered by the bootstrap health checker from early startup, while the webhook
# server starts at the last startup stage. Asserting a denial before then reads
# as "the gate did not hold" when the gate simply was not up yet, and
# failurePolicy:Ignore admits in that window — observed exactly once here.
#
# So synchronise on the gate ENFORCING, not on the pod being Ready. The probe is
# a server-side dry-run patch of the live object, which is schema-valid by
# construction: only the webhook can reject it, so a rejection cannot be
# confused with CRD schema validation. It doubles as a positive control — if
# this never denies, the negative case below would have been vacuous.
# One denial is NOT enough. The API server load-balances across every ready
# webhook endpoint, and a controller pod is Ready well before its admission
# server starts (that is the last startup stage). So a single probe can be
# answered by a pod that enforces while a sibling still refuses the connection —
# and failurePolicy:Ignore admits whatever lands on the sibling. Measured: the
# probe was denied at 12:48:05 and the broken upgrade was admitted at 12:48:13.
#
# So require every endpoint to be ready AND a run of consecutive denials, which
# samples across them.
webhook_endpoints_all_ready() {
  k get endpointslices -o json 2>/dev/null | python3 -c '
import json, sys
ready = notready = 0
for es in json.load(sys.stdin).get("items", []):
    if not any(p.get("name") == "webhook" or p.get("port") == 9443 for p in es.get("ports", [])):
        continue
    for ep in es.get("endpoints", []):
        if ep.get("conditions", {}).get("ready"):
            ready += 1
        else:
            notready += 1
print("ok" if ready and not notready else "wait")'
}

wait_webhook_enforcing() {
  local cfg deadline=$((SECONDS + ${1:-180})) streak=0
  cfg=$(k get haproxytemplateconfig -o name 2>/dev/null | head -1)
  [ -n "$cfg" ] || return 1
  while [ $SECONDS -lt $deadline ]; do
    if [ "$(webhook_endpoints_all_ready)" = "ok" ] \
       && ! k patch "$cfg" --type=merge --dry-run=server \
            -p '{"spec":{"haproxyConfig":{"template":"{%- var x = %}"}}}' >/dev/null 2>&1; then
      streak=$((streak + 1))
      [ "$streak" -ge 8 ] && return 0
    else
      streak=0
    fi
    sleep 2
  done
  return 1
}

info "waiting for the admission webhook to actually enforce"
wait_webhook_enforcing 180 \
  || fail "the config webhook never denied a known-bad template; the negative case below would prove nothing"

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
  echo "--- helm output (the upgrade that should have been denied) ---"; cat "$WORK/broken.log"
  echo "--- webhook endpoints ---"; k get endpointslices -o wide
  fail "an upgrade carrying an uncompilable template was ACCEPTED — the gate did not hold"
fi
info "rejected, as required"

POST_FP="$(config_fingerprint)"
[ "$PRE_FP" = "$POST_FP" ] \
  || fail "a rejected upgrade mutated the live config (${PRE_FP:0:12} -> ${POST_FP:0:12}); it must be a no-op"

wait_controller_ready 180 || fail "controller unhealthy after the rejected upgrade"

# ------------------------------ phase 4: recovery from a broken live deployment

# Phase 3 covers the case where the gate holds. This covers the case where it
# didn't — HAPTIC shipped a bug, and a config no controller can load is already
# in etcd. Every fleet that hits such a bug arrives here, so `helm upgrade` must
# get out of it with no manual step; otherwise each bug we ship is an outage an
# operator cannot end.
#
# Getting into that state uses the product's own fail-open path rather than a
# synthetic hack: the config webhook is served BY the controller, so with the
# controller down it is unreachable, failurePolicy:Ignore admits the broken
# config, and the pods that come back cannot load it. That is exactly the
# sequence a shipped rendering bug produces.

info "phase 4: breaking the live deployment the way a shipped bug does"
DEPLOY="$(controller_deploy)"
[ -n "$DEPLOY" ] || fail "cannot find the controller deployment"
k scale "deploy/$DEPLOY" --replicas=0 >/dev/null || fail "could not scale the controller down"

# Every pod gone is the precondition, not a nicety: one surviving pod still
# serves the webhook, which would DENY the broken chart instead of failing open.
# The staging upgrade would then fail for a reason that looks nothing like the
# real one, so this must be loud rather than best-effort.
if ! k wait --for=delete pod -l app.kubernetes.io/component=controller --timeout=120s >/dev/null 2>&1; then
  k get pods -l app.kubernetes.io/component=controller
  fail "controller pods did not terminate after scaling to 0; cannot stage the broken state"
fi

if ! helm upgrade "$RELEASE" "$WORK/broken-chart" \
      --kube-context "$CTX" --namespace "$NS" \
      --set controller.image.repository=haptic \
      --set controller.image.tag=test \
      --set "haproxyVersion=$HAPROXY_VERSION" \
      --timeout 10m > "$WORK/break.log" 2>&1; then
  echo "--- helm output ---"; cat "$WORK/break.log"
  fail "could not stage the broken state: with the controller down the config webhook must fail open"
fi

# Negative control for this phase. If the fleet is healthy here, the recovery
# below proves nothing — it would just be a second successful upgrade.
if wait_controller_ready 150; then
  fail "the deliberately broken deployment came up healthy — phase 4 is not testing recovery"
fi
info "deployment is broken as intended (controller cannot become ready)"

info "recovering with a plain helm upgrade — no manual intervention allowed"
if ! helm upgrade "$RELEASE" "$CHART" \
      --kube-context "$CTX" --namespace "$NS" \
      --set controller.image.repository=haptic \
      --set controller.image.tag=test \
      --set "haproxyVersion=$HAPROXY_VERSION" \
      --timeout 20m > "$WORK/recover.log" 2>&1; then
  echo "--- helm output ---"; cat "$WORK/recover.log"
  fail "helm upgrade could NOT recover a broken deployment — an operator hitting a HAPTIC bug would be stuck"
fi

# Same reason as after the upgrade: wait for the NEW revision, not merely for
# some replica to be Ready, so the Validated verdict below is the recovered
# controller's and not a leftover.
k rollout status "deploy/$DEPLOY" --timeout=7m >/dev/null \
  || { dump_pod_diagnostics; fail "controller rollout did not complete after recovery"; }

validated=$(wait_config_validated 180) || fail "config Validated=$validated after recovery"

info "recovery OK: a broken fleet was restored by an ordinary helm upgrade"

info "ALL CHECKS PASSED"
