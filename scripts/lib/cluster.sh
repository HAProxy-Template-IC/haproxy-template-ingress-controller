# Create a kind cluster that is reachable both locally and under GitLab's
# docker:dind service.
#
# Under dind the Docker daemon lives in a separate container, so kind's default
# kubeconfig points at 127.0.0.1 on that daemon's network namespace and every
# kubectl call from the job container gets "connection refused". The fix is the
# same one .gitlab/ci/kind-config-dind.yaml and test-helm-defaults.sh already
# use: bind the API server to all interfaces, add "docker" to its certificate
# SANs, then rewrite the kubeconfig to reach it by that name.
#
# Source this and call: kind_create_cluster <cluster-name>
#
# wait_config_validated below calls the caller's `k()` wrapper (kubectl pinned to
# the suite's context and namespace), which every consumer defines.

# Both expansions need the default: callers run under `set -u`, where a bare
# ${DOCKER_HOST#tcp://} aborts the script when the variable is unset.
kind_in_dind() { local h="${DOCKER_HOST:-}"; [ "$h" != "${h#tcp://}" ]; }

kind_create_cluster() {
  local name="$1"
  local repo config
  repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

  kind delete cluster --name "$name" >/dev/null 2>&1 || true

  if kind_in_dind; then
    config="$repo/.gitlab/ci/kind-config-dind.yaml"
    [ -f "$config" ] || { echo "FAIL: $config not found" >&2; return 1; }
    kind create cluster --name "$name" --config "$config" >/dev/null || return 1
    # kind writes the bind address it was given; only the client needs the name.
    sed -i 's|https://0\.0\.0\.0:|https://docker:|g' "${KUBECONFIG:-$HOME/.kube/config}"
  else
    kind create cluster --name "$name" >/dev/null || return 1
  fi

  kubectl --context "kind-$name" cluster-info >/dev/null 2>&1 \
    || { echo "FAIL: kind cluster $name is not reachable after creation" >&2; return 1; }
}

# The Validated condition is written asynchronously by the leader's status
# applier, so it is absent for a while after the config is loaded and serving.
# Reading it once races that write and reports Validated=MISSING for a config
# that is fine — seen in CI on a bare cluster while HAProxy was already serving
# a real 77-line render.
#
# Polls until the condition reads True, then echoes it. On timeout it echoes the
# last value seen (MISSING / False / a reason), so the caller's failure names
# what it actually found rather than just "not True".
config_validated_status() {
  k get haproxytemplateconfig -o json 2>/dev/null | python3 -c '
import json,sys
try:
    items = json.load(sys.stdin).get("items", [])
except Exception:
    print("UNREADABLE"); sys.exit(0)
c = [c for i in items for c in i.get("status", {}).get("conditions", []) if c["type"] == "Validated"]
print(c[0]["status"] if c else "MISSING")'
}

wait_config_validated() {
  local deadline=$((SECONDS + ${1:-180})) status=MISSING
  while [ $SECONDS -lt $deadline ]; do
    status="$(config_validated_status)"
    [ "$status" = "True" ] && { echo "True"; return 0; }
    sleep 5
  done
  echo "$status"
  return 1
}

# The controller Deployment, by name suffix: the component label lives on the
# pods, so a label-selected `get deploy` matches nothing.
controller_deploy() {
  k get deploy -o json 2>/dev/null | python3 -c '
import json,sys
for d in json.load(sys.stdin).get("items", []):
    if d["metadata"]["name"].endswith("controller"):
        print(d["metadata"]["name"]); break'
}

# Why a pod is not ready, at the only zoom level that distinguishes causes:
# which container, what state, and — for a restart — how the previous instance
# died. An OOMKill under rollout surge and a genuine crash look identical from
# `get pods` alone, and telling them apart is usually the whole question.
dump_pod_diagnostics() {
  echo "--- pods ---"; k get pods -o wide
  echo "--- pod revisions (old vs new is the whole question during an upgrade) ---"
  k get pods -o json 2>/dev/null | python3 -c '
import json, sys
for pod in json.load(sys.stdin).get("items", []):
    imgs = [c.get("image", "") for c in pod["spec"].get("containers", [])]
    owner = ",".join(o.get("name", "") for o in pod["metadata"].get("ownerReferences", []))
    ready = sum(1 for c in pod.get("status", {}).get("containerStatuses", []) if c.get("ready"))
    total = len(pod["spec"].get("containers", []))
    print("  {} rs={} {}/{} images={}".format(pod["metadata"]["name"], owner, ready, total, " ".join(imgs)))'
  echo "--- not-ready containers ---"
  k get pods -o json 2>/dev/null | python3 -c '
import json, sys
for pod in json.load(sys.stdin).get("items", []):
    pname = pod["metadata"]["name"]
    for cs in pod.get("status", {}).get("containerStatuses", []):
        if cs.get("ready"):
            continue
        state = cs.get("state", {})
        which = next(iter(state), "unknown")
        detail = state.get(which, {})
        last = cs.get("lastState", {}).get("terminated", {})
        print("  {}/{} state={} reason={} msg={} restarts={} lastExit={} lastReason={}".format(
            pname, cs["name"], which,
            detail.get("reason", ""), detail.get("message", "")[:120],
            cs.get("restartCount", 0),
            last.get("exitCode", "-"), last.get("reason", "-")))'
  echo "--- logs of every not-ready container (current and previous instance) ---"
  k get pods -o json 2>/dev/null | python3 -c '
import json, sys
for pod in json.load(sys.stdin).get("items", []):
    for cs in pod.get("status", {}).get("containerStatuses", []):
        if not cs.get("ready"):
            print(pod["metadata"]["name"], cs["name"])' | while read -r pod ctr; do
    echo "  === $pod/$ctr (current) ==="
    k logs "$pod" -c "$ctr" --tail=25 2>&1 | sed 's/^/    /'
    echo "  === $pod/$ctr (previous instance) ==="
    k logs "$pod" -c "$ctr" --previous --tail=25 2>&1 | sed 's/^/    /'
  done
  echo "--- recent warning events ---"
  k get events --field-selector type=Warning --sort-by=.lastTimestamp 2>/dev/null | tail -15
}

# The config as the controller actually loaded it. A validationTest that passes
# from a file and fails in-cluster differs by exactly this, so print it rather
# than inferring it from the test's failure text.
dump_config_state() {
  echo "--- stored validationTests object ---"
  k get haproxyvalidationtests -o json 2>/dev/null | python3 -c '
import json, sys
try:
    items = json.load(sys.stdin).get("items", [])
except Exception:
    print("  <unreadable>"); raise SystemExit
if not items:
    print("  <none found — check validationTestsSelector>"); raise SystemExit
for obj in items:
    tests = obj.get("spec", {}).get("validationTests", {})
    print("  {} tests={}".format(obj["metadata"]["name"], len(tests)))
    t = tests.get("test-status-frontend-prometheus-exporter")
    if t is not None:
        print("    test-status-frontend-prometheus-exporter.extraContext = {!r}".format(t.get("extraContext", "<absent>")))'
  echo "--- operator extraContext.vector ---"
  k get haproxytemplateconfig -o json 2>/dev/null | python3 -c '
import json, sys
for obj in json.load(sys.stdin).get("items", []):
    ec = obj.get("spec", {}).get("templatingSettings", {}).get("extraContext", {}) or {}
    print("  {}: vector={}".format(obj["metadata"]["name"],
          "PRESENT" if ec.get("vector") is not None else "absent/null"))'
}
