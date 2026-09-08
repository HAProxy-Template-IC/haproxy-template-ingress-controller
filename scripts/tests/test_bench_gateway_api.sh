#!/usr/bin/env bash
set -euo pipefail

runner="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/bench-gateway-api.sh"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/bench-gateway-api-test.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

assert_eq() {
    local expected="$1"
    local actual="$2"
    [[ "$actual" == "$expected" ]] || {
        printf 'expected %q, got %q\n' "$expected" "$actual" >&2
        exit 1
    }
}

assert_eq 1 "$(bash -c 'source "$1"; duration_seconds 1s' bash "$runner")"
assert_eq 120 "$(bash -c 'source "$1"; duration_seconds 2m' bash "$runner")"
assert_eq 3600 "$(bash -c 'source "$1"; duration_seconds 1h' bash "$runner")"
if bash -c 'source "$1"; duration_seconds 1d' bash "$runner" >/dev/null 2>&1; then
    echo "duration_seconds accepted an invalid suffix" >&2
    exit 1
fi

assert_eq standard "$(BENCH_GATEWAY_API_CHANNEL=standard bash -c 'source "$1"; printf "%s" "$BENCH_GATEWAY_API_CHANNEL"' bash "$runner")"

# pilot-load is stopped by signal at the end of the steady-churn interval; a
# signal exit after that stop is the stop working, before it a crash.
touch "$tmp/stop-issued"
workload_exit() {
    bash -c 'source "$1"; workload_exit_acceptable "$2" "$3" && echo ok || echo fail' bash "$runner" "$1" "$2"
}
assert_eq ok "$(workload_exit 0 "$tmp/missing")"
assert_eq ok "$(workload_exit 0 "$tmp/stop-issued")"
assert_eq ok "$(workload_exit 130 "$tmp/stop-issued")"
assert_eq ok "$(workload_exit 143 "$tmp/stop-issued")"
assert_eq fail "$(workload_exit 143 "$tmp/missing")"
assert_eq fail "$(workload_exit 137 "$tmp/stop-issued")"
assert_eq fail "$(workload_exit 1 "$tmp/stop-issued")"

mkdir "$tmp/bin"
cat > "$tmp/bin/kind" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == get && "$2" == clusters ]]; then
    printf '%s\n' haptic-gwbench-test haptic-dev
    exit 0
fi
exit 1
EOF
chmod 0755 "$tmp/bin/kind"
assert_eq present "$(PATH="$tmp/bin:$PATH" bash -c 'source "$1"; kind_cluster_state haptic-gwbench-test' bash "$runner")"
assert_eq absent "$(PATH="$tmp/bin:$PATH" bash -c 'source "$1"; kind_cluster_state missing' bash "$runner")"

cat > "$tmp/values.json" <<'EOF'
{"credentials":{"dataplane":{"password":"secret"}},"controller":{"webhook":{"caBundle":"certificate"}},"kept":"value"}
EOF
BENCH_GATEWAY_API_CHANNEL=standard bash -c 'source "$1"; redact_helm_values "$2" "$3"' bash "$runner" "$tmp/values.json" "$tmp/redacted.json"
jq -e '
    .credentials.dataplane.password == "<redacted>" and
    .controller.webhook.caBundle == "<redacted>" and
    .kept == "value"
' "$tmp/redacted.json" >/dev/null

# assert_no_limit_workloads takes the controller requests from the chart
# defaults, so a chart sizing change cannot fail every run.
cat > "$tmp/defaults.json" <<'EOF'
{"controller":{"resources":{"limits":{"memory":"1Gi"},"requests":{"cpu":"100m","memory":"1Gi"}}}}
EOF
write_manifest() {
    local controller_resources="$1"
    cat > "$tmp/manifest.yaml" <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: haptic-controller
spec:
  template:
    spec:
      containers:
      - name: controller
        resources: $controller_resources
        livenessProbe: {httpGet: {path: /healthz}}
        readinessProbe: {httpGet: {path: /healthz}}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: haptic-haproxy
spec:
  template:
    spec:
      containers:
      - name: haproxy
        resources: {requests: {cpu: 250m, memory: 1Gi}}
EOF
}
no_limit_workloads() {
    bash -c 'source "$1"; assert_no_limit_workloads "$2" "$3"' bash "$runner" "$tmp/manifest.yaml" "$tmp/defaults.json"
}
write_manifest '{requests: {cpu: 100m, memory: 1Gi}}'
no_limit_workloads
write_manifest '{requests: {cpu: 100m, memory: 1Gi}, limits: {memory: 1Gi}}'
if no_limit_workloads; then
    echo "assert_no_limit_workloads accepted a controller memory limit" >&2
    exit 1
fi
write_manifest '{requests: {cpu: 100m, memory: 512Mi}}'
if no_limit_workloads; then
    echo "assert_no_limit_workloads accepted controller requests that differ from the chart defaults" >&2
    exit 1
fi

# extract_upstream_backend_manifest must lift the program's backendTemplate
# constant byte-for-byte and accept only the Deployment + Service pair.
mkdir -p "$tmp/upstream/tests/probe"
cat > "$tmp/upstream/tests/probe/probe.go" <<'EOF'
package main

const backendTemplate = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: backend
spec:
  selector:
    matchLabels:
      app: backend
---
apiVersion: v1
kind: Service
metadata:
  name: backend
spec:
  ports:
  - name: http
    port: 80
`

const other = "unrelated"
EOF
bash -c 'source "$1"; UPSTREAM_DIR="$3"; extract_upstream_backend_manifest probe "$2"' bash "$runner" "$tmp/backend.yaml" "$tmp/upstream"
assert_eq "$(sed -n '/^const backendTemplate = `/,/^`/p' "$tmp/upstream/tests/probe/probe.go" | sed '1d;$d')" "$(cat "$tmp/backend.yaml")"
cat > "$tmp/upstream/tests/probe/probe.go" <<'EOF'
package main

const backendTemplate = `
apiVersion: v1
kind: Service
metadata:
  name: backend
`
EOF
if bash -c 'source "$1"; UPSTREAM_DIR="$3"; extract_upstream_backend_manifest probe "$2"' bash "$runner" "$tmp/backend.yaml" "$tmp/upstream" >/dev/null 2>&1; then
    echo "extract_upstream_backend_manifest accepted a manifest without the Deployment" >&2
    exit 1
fi

# The readiness-timeout evidence carries the HAProxyCfg snapshot, which is
# megabytes at scale; as a jq argument it exceeded the argument list and the
# run lost its evidence.
scenario="$tmp/scale"
mkdir -p "$scenario"
printf '{"metadata":{"generation":3}}\n' > "$scenario/haproxycfg-baseline.json"
printf 'abc123\n' > "$scenario/haproxycfg-baseline-checksum.txt"
printf '{"attempts":2,"reason_code":"exact-current-timeout","outcome":"deadline","evidence_valid":true,"pass":false,"deadline_reached":true}\n' > "$tmp/readiness.json"
python3 - "$tmp/haproxycfg.json" <<'EOF'
import json, sys
cfg = {"metadata": {"generation": 9}, "spec": {"checksum": "big", "config": "x" * 3_000_000},
       "status": {"deployedToPods": [{"checksum": "big"}]}}
json.dump(cfg, open(sys.argv[1], "w"))
EOF
bash -c 'source "$1"; write_scale_readiness_timeout "$2" 5000 initial-exact-current "$3" "$4"' bash "$runner" "$scenario" "$tmp/haproxycfg.json" "$tmp/readiness.json"
assert_eq big "$(jq -r '.at_scale.checksum' "$scenario/scale-readiness.json")"
assert_eq exact-current-timeout "$(jq -r '.reason_code' "$scenario/scale-readiness.json")"

# The stop grace grows with the routes pilot-load has to delete on INT.
assert_eq 10 "$(bash -c 'source "$1"; workload_stop_grace_seconds 0' bash "$runner")"
assert_eq 60 "$(bash -c 'source "$1"; workload_stop_grace_seconds 5000' bash "$runner")"

printf 'bench-gateway-api shell tests: OK\n'
