#!/usr/bin/env bash
# Prove that `helm upgrade` from the last released chart to this working tree is
# ACCEPTED, and that a rejected upgrade leaves the live configuration exactly as
# it was.
#
# Scope, deliberately narrow: the risks this covers are (1) the pre-rollout
# preflight hook aborting a broken release BEFORE any manifest object is
# applied (ADR-0016 — the successor of the per-object config webhook, which
# could not judge a multi-object config change), (2) the apply-crds hook
# stripping the legacy config-webhook entries during the same upgrade that
# removes their server, and (3) helm/helmfile completing — including `helm
# diff` on a CRD its own pre-upgrade hook has not installed yet, which broke
# once before.
#
# It does NOT wait for the fleet to converge. Demanding convergence only drags
# in every runtime dependency (a default-ssl-cert Secret, Gateway API CRDs, …),
# and each prop makes the cluster less like the one an operator has.
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

# Every HAPTIC config object as one JSON document, tolerating a kind whose CRD
# is not installed yet: the baseline chart predates HAProxyTemplateLibrary, so
# `kubectl get` on it errors there. A missing kind must read as "no objects" —
# letting it error feeds empty stdin to the parsers below and fails the suite
# for the wrong reason.
config_objects() {
  local kind out merged=""
  for kind in haproxytemplateconfig haproxytemplatelibrary; do
    out=$(kubectl --context "$CTX" -n "$NS" get "$kind" -o json 2>/dev/null) || out='{"items":[]}'
    merged="${merged}${out}"$'\n'
  done
  printf '%s' "$merged" | python3 -c '
import json, sys

decoder = json.JSONDecoder()
buf = sys.stdin.read()
items = []
i = 0
while i < len(buf):
    while i < len(buf) and buf[i].isspace():
        i += 1
    if i >= len(buf):
        break
    doc, i = decoder.raw_decode(buf, i)
    items.extend(doc.get("items", []))
json.dump({"items": items}, sys.stdout)
'
}

# Fingerprint of what the fleet is actually configured by: the merged spec the
# controller would load. Compared before and after a rejected upgrade — equality
# is the contract that a failed upgrade changes nothing.
#
# Covers BOTH kinds: template content lives in HAProxyTemplateLibrary objects,
# so a config-only fingerprint would be blind to every template change and this
# suite's whole premise — "a rejected upgrade mutated nothing" — would pass
# without testing anything. Kind is part of the key so two objects sharing a
# name cannot collide.
config_fingerprint() {
  config_objects \
    | python3 -c 'import json,sys,hashlib
d=json.load(sys.stdin)
specs=sorted((i["kind"], i["metadata"]["name"], json.dumps(i.get("spec",{}),sort_keys=True)) for i in d["items"])
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

# The upgrade itself needs nothing from an operator: the crd-upgrade-hook Job
# (pre-install/pre-upgrade, weight -5) runs `apply-crds` before Helm applies the
# release, so `helm upgrade` installs a newly added CRD kind on its own.
#
# `helm diff` runs BEFORE hooks, so at diff time that kind is not registered yet
# and helm cannot map it. Applying the CRDs here puts the cluster in exactly the
# state the hook produces moments later, so the diff leg measures the upgrade
# rather than the ordering of helm's own phases. It stays strict afterwards and
# still catches every other mapping or rendering break.
info "applying the target chart's CRDs (the state crd-upgrade-hook reaches before helm applies)"
kubectl --context "$CTX" apply --server-side --force-conflicts -f "$CHART/crds/" >/dev/null \
  || fail "could not apply the target chart's CRDs; the crd-upgrade-hook would hit the same failure"

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

# The baseline's ValidatingWebhookConfiguration carried per-object
# haproxytemplateconfig entries; the apply-crds hook must have deleted them
# during THIS upgrade — the old controller is gone, so a surviving entry is a
# fail-open hole (failurePolicy: Ignore) at best and a hard 443-refused at
# worst. Watched-resource entries (Ingress etc.) must survive.
LEGACY_ENTRIES="$(kubectl --context "$CTX" get validatingwebhookconfigurations -o json 2>/dev/null \
  | python3 -c 'import json,sys
n=0
for vwc in json.load(sys.stdin).get("items", []):
    for wh in vwc.get("webhooks", []):
        if wh.get("name", "").startswith("haproxytemplateconfig."):
            n += 1
print(n)')"
[ "$LEGACY_ENTRIES" = "0" ] \
  || fail "$LEGACY_ENTRIES legacy config-webhook entries survived the upgrade; apply-crds must strip them"
info "legacy config-webhook entries stripped by the upgrade, as required"

# ------------------------------- phase 3: a rejected upgrade must change nothing

# The gate under test is the pre-rollout preflight hook (ADR-0016). Hooks run
# deterministically BEFORE helm applies the first manifest object, so unlike
# the removed webhook there is no enforcement window to synchronise on — but
# the gate must demonstrably be wired into the release, or the negative case
# below proves nothing.
helm --kube-context "$CTX" -n "$NS" get hooks "$RELEASE" | grep -q "pre-rollout" \
  || fail "the release carries no pre-rollout validation hook; the negative case below would prove nothing"

# The broken artifact is the chart AND the image: they ship together, the
# hook's HAPTIC_EXPECT_CHART_VERSION guard pins them to one version, and the
# hook validates the IMAGE-embedded chart — so "HAPTIC released a rendering
# bug" means both carry it. A chart-dir-only corruption models a hand-modified
# chart instead; that path has no pre-apply gate by design (the hook cannot
# see files it does not ship) and is contained by the live-change gate and
# recovered by phase 4.
info "negative case: a release that cannot compile its own templates must abort before any object is applied"
cp -r "$CHART" "$WORK/broken-chart"

# base.yaml, named explicitly. A `find ... | head -1` here is filesystem-ordered
# and matches 14 files, including tests/library_loader_test.yaml — a
# helm-unittest fixture that is never rendered — and libraries an operator can
# disable. Corrupting one of those makes the upgrade legitimately succeed and
# this suite report a gate failure that did not happen.
BROKEN_LIB="$WORK/broken-chart/charts/base/library.yaml"
[ -f "$BROKEN_LIB" ] || fail "charts/base/library.yaml not found — this suite corrupts it deliberately"

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

info "building the matching broken image (a real release bug ships in chart AND image)"
docker build -t "haptic:test-broken-haproxy${HAPROXY_VERSION}" \
    -f - "$(dirname "$BROKEN_LIB")" > "$WORK/broken-image.log" 2>&1 <<'EOF' \
  || { cat "$WORK/broken-image.log"; fail "could not build the broken image"; }
FROM haptic:test
COPY library.yaml /usr/share/haptic/chart/charts/base/library.yaml
EOF
kind load docker-image "haptic:test-broken-haproxy${HAPROXY_VERSION}" --name "$CLUSTER" >/dev/null

PRE_FP="$(config_fingerprint)"
if helm upgrade "$RELEASE" "$WORK/broken-chart" \
      --kube-context "$CTX" --namespace "$NS" \
      --set controller.image.repository=haptic \
      --set controller.image.tag=test-broken \
      --set "haproxyVersion=$HAPROXY_VERSION" \
      --timeout 5m > "$WORK/broken.log" 2>&1; then
  echo "--- helm output (the upgrade that should have been aborted) ---"; cat "$WORK/broken.log"
  # Did the config actually change? If the fingerprint is unmoved, helm reported
  # success without mutating the config and the gate DID hold — the assertion is
  # then wrong, not the product.
  echo "--- config fingerprint: before=${PRE_FP:0:12} after=$(config_fingerprint | cut -c1-12) ---"
  echo "--- is the corruption actually in the live config? ---"
  config_objects | grep -c 'var x = ' || echo "  0 (the broken template never landed)"
  echo "--- preflight hook state ---"
  k get jobs -o wide 2>/dev/null
  k get pods -l app.kubernetes.io/component=pre-rollout-validation -o wide 2>/dev/null
  fail "an upgrade carrying an uncompilable template was ACCEPTED — the pre-rollout gate did not hold"
fi

# The abort must come from the PREFLIGHT hook, before the manifest. A later
# failure (rollout stall on the load gate) also fails helm, but only after the
# broken config reached etcd. hook-delete-policy keeps a FAILED hook job
# around, so a failed pre-rollout job is the discriminator.
PREFLIGHT_JOB="$(k get jobs -o name 2>/dev/null | grep -- "-pre-rollout" | head -1 || true)"
[ -n "$PREFLIGHT_JOB" ] \
  || { cat "$WORK/broken.log"; fail "no failed pre-rollout job left behind — the abort did not come from the preflight gate"; }
FAILED_COUNT="$(k get "$PREFLIGHT_JOB" -o jsonpath='{.status.failed}' 2>/dev/null)"
[ "${FAILED_COUNT:-0}" -ge 1 ] \
  || fail "the pre-rollout job did not fail; the upgrade aborted for some other reason"
echo "--- preflight verdict (what an operator sees) ---"
k logs "$PREFLIGHT_JOB" --tail=15 2>/dev/null | sed 's/^/  /' || true
info "aborted by the pre-rollout hook, as required"

POST_FP="$(config_fingerprint)"
[ "$PRE_FP" = "$POST_FP" ] \
  || fail "a rejected upgrade mutated the live config (${PRE_FP:0:12} -> ${POST_FP:0:12}); it must be a no-op"

# The hook aborts before the first manifest object, so the broken template
# must never have reached etcd and the running fleet must be untouched.
# The corruption goes into a library file, so it lands in a
# HAProxyTemplateLibrary — checking only the config would find nothing and
# report success no matter what the gate did.
LIVE_CFG="$(config_objects)"
case "$LIVE_CFG" in
  *'{%- var x = %}'*) fail "the broken template reached the live config; the gate held only after the damage" ;;
esac
restarts=$(k get pods -l app.kubernetes.io/component=controller \
  -o jsonpath='{range .items[*]}{.status.containerStatuses[*].restartCount}{"\n"}{end}' | tr -s ' \n' '+' | sed 's/+$//')
[ "$(( ${restarts:-0} ))" -eq 0 ] \
  || fail "controller restarted during a hook-aborted upgrade; the abort was not pre-apply"

wait_controller_ready 180 || fail "controller unhealthy after the rejected upgrade"

# ------------------------------ phase 4: recovery from a broken live deployment

# Phase 3 covers the case where the gate holds. This covers the case where a
# config no controller can load is already in etcd — a hand-edited CR, or a
# chart-image skew that slipped a bad template past the embedded-chart
# preflight (the hook cannot see files it does not ship). Every fleet that
# hits such a state arrives here, so `helm upgrade` must get out of it with no
# manual step; otherwise each such bug is an outage an operator cannot end.
#
# Staging uses the skew path directly: the corrupted chart DIRECTORY with the
# pristine image passes the preflight hook, so the broken config lands in
# etcd. That is exactly the write the retired admission webhook used to catch
# only while a controller happened to be up — CR content has no admission
# gate anymore by design (ADR-0016).

info "phase 4: breaking the live deployment the way a shipped bug does"
DEPLOY="$(controller_deploy)"
[ -n "$DEPLOY" ] || fail "cannot find the controller deployment"
ORIG_REPLICAS="$(k get "deploy/$DEPLOY" -o jsonpath='{.spec.replicas}' 2>/dev/null)"
[ -n "$ORIG_REPLICAS" ] && [ "$ORIG_REPLICAS" != "0" ] \
  || fail "controller deployment reports $ORIG_REPLICAS replicas before the break; nothing to scale down"
k scale "deploy/$DEPLOY" --replicas=0 >/dev/null || fail "could not scale the controller down"

# Every pod gone is the precondition, not a nicety: a surviving pod takes the
# broken CR as a LIVE change, which the scatter-gather gate rejects while the
# pod stays healthy — the negative control below would then report a fleet
# that "recovered" without ever being broken, so this must be loud rather
# than best-effort.
if ! k wait --for=delete pod -l app.kubernetes.io/component=controller --timeout=120s >/dev/null 2>&1; then
  k get pods -l app.kubernetes.io/component=controller
  fail "controller pods did not terminate after scaling to 0; cannot stage the broken state"
fi

# replicaCount=0 keeps the fleet down THROUGH the upgrade. Without it helm
# restores the replicas as part of this same upgrade, and the new pods race the
# CR write: a pod that starts first loads the OLD, good config and goes Ready,
# and the broken CR then arrives as a LIVE change — which the scatter-gather
# gate rejects while the controller keeps serving the old config, never
# crash-looping. Only the STARTUP load gate crash-loops, so the pods must not
# exist until the broken CR is in place. Observed as a phase-4 failure that
# reproduced in CI and not locally, purely on timing.
if ! helm upgrade "$RELEASE" "$WORK/broken-chart" \
      --kube-context "$CTX" --namespace "$NS" \
      --set controller.image.repository=haptic \
      --set controller.image.tag=test \
      --set controller.replicaCount=0 \
      --set "haproxyVersion=$HAPROXY_VERSION" \
      --timeout 10m > "$WORK/break.log" 2>&1; then
  echo "--- helm output ---"; cat "$WORK/break.log"
  fail "could not stage the broken state: the preflight hook validates the embedded chart, not the chart directory, so this upgrade must be admitted"
fi

# The staging is only real if the broken template actually reached the live CR.
# If it did not, the check below would pass for the wrong reason — a healthy
# controller loading a perfectly good config — and report a recovery that was
# never tested.
# Not `| grep -q`: under pipefail it SIGPIPEs kubectl on the first match and the
# pipeline reports failure despite finding the marker. `-o json` because -o yaml
# folds long scalars across lines and splits it.
LIVE_CFG="$(config_objects)"
case "$LIVE_CFG" in
  *'{%- var x = %}'*) : ;;
  *) fail "the broken template never reached the live HAProxyTemplateConfig; phase 4 would prove nothing" ;;
esac

k scale "deploy/$DEPLOY" --replicas="$ORIG_REPLICAS" >/dev/null || fail "could not scale the controller back up"

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
