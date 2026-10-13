#!/usr/bin/env bash
set -euo pipefail

BENCH_RAW_ROOT=$(mktemp -d /tmp/haptic-gateway-api-bench.XXXXXX)
BENCH_RAW_OUTPUT=${BENCH_RAW_ROOT}/result
BENCH_STAGED_OUTPUT=${BENCH_CI_OUTPUT:?Set BENCH_CI_OUTPUT to the artifact destination}

set +e
env \
  HAPROXY_VERSION=3.4 \
  BENCH_REF=e81292ed876472804e0a2245876a7c445ab80881 \
  BENCH_GATEWAY_API_VERSION=v1.4.0 \
  BENCH_GATEWAYS=haptic-bench/haptic \
  BENCH_SCENARIOS=probe,scale,routechange \
  BENCH_PROBE_ROUTES=300 \
  BENCH_PROBE_TIMEOUT=45m \
  BENCH_DEPLOY_INTERVAL=100ms \
  BENCH_WATCH_DEBOUNCE=100ms \
  BENCH_SCALE_NAMESPACES=50 \
  BENCH_SCALE_ROUTES_PER_NAMESPACE=100 \
  BENCH_SCALE_DURATION=10m \
  BENCH_SCALE_STARTUP_TIMEOUT=20m \
  BENCH_ROUTECHANGE_ITERATIONS=20 \
  BENCH_ROUTECHANGE_GRACE_PERIOD=200ms \
  BENCH_ROUTECHANGE_TIMEOUT=10m \
  BENCH_KEEP_CLUSTER=false \
  BENCH_ALLOW_DIRTY=false \
  BENCH_ALLOW_COSCHEDULED_CLUSTERS=false \
  REUSE_CLUSTER=false \
  BUILD_ONLY=false \
  BENCH_OUTPUT_DIR="$BENCH_RAW_OUTPUT" \
  timeout --preserve-status --kill-after=20m 135m ./scripts/bench-gateway-api.sh
runner_rc=$?
set -e

trusted=false
if [ -d "$BENCH_RAW_OUTPUT" ] &&
   [ ! -L "$BENCH_RAW_OUTPUT" ] &&
   [ -f "$BENCH_RAW_OUTPUT/runner-summary.json" ] &&
   [ ! -L "$BENCH_RAW_OUTPUT/runner-summary.json" ] &&
   [ -f "$BENCH_RAW_OUTPUT/runner-exit-code.txt" ] &&
   [ ! -L "$BENCH_RAW_OUTPUT/runner-exit-code.txt" ] &&
   [ -f "$BENCH_RAW_OUTPUT/metadata.json" ] &&
   [ ! -L "$BENCH_RAW_OUTPUT/metadata.json" ] &&
   [ -f "$BENCH_RAW_OUTPUT/cluster/artifact-secret-scan.json" ] &&
   [ ! -L "$BENCH_RAW_OUTPUT/cluster/artifact-secret-scan.json" ] &&
   [ "$(cat "$BENCH_RAW_OUTPUT/runner-exit-code.txt")" = "$runner_rc" ] &&
   [ -z "$(find "$BENCH_RAW_OUTPUT" -mindepth 1 ! -type f ! -type d -print -quit)" ] &&
   jq -e --argjson runner_rc "$runner_rc" '
     .harness.secret_inventory_trusted == true and
     .harness.final_exit_code == $runner_rc and
     (.harness.pass | type) == "boolean" and
     .harness.pass == ($runner_rc == 0) and
     (([.scenarios[].scenario] == ["probe", "scale", "routechange"] and
       all(.scenarios[];
         .measurement_valid == true and (.pass | type) == "boolean" and
         .supervised_child_continuity.evidence_valid == true and
         (.supervised_child_continuity.pass | type) == "boolean") and
       (.measured_result.pass | type) == "boolean") or
      ($runner_rc != 0 and .schema_version == 1 and
       .harness.status == "invalid" and .scenarios == [] and
       .measured_result == null and .public_comparison == "ballpark-only"))
   ' "$BENCH_RAW_OUTPUT/runner-summary.json" >/dev/null &&
   jq -e '
     .schema_version == 1 and .pass == true and .redacted == [] and
     (.scan_count | type) == "number" and
     (.scan_count | floor) == .scan_count and .scan_count >= 2 and
     .method == "bytewise raw-base64 and decoded captured sensitive Secret value scan; HAPTIC SSL path metadata excluded"
   ' "$BENCH_RAW_OUTPUT/cluster/artifact-secret-scan.json" >/dev/null &&
   jq -e '
     .benchmark_requested_ref == "e81292ed876472804e0a2245876a7c445ab80881" and
     .benchmark_commit == "e81292ed876472804e0a2245876a7c445ab80881" and
     .scenarios == ["probe", "scale", "routechange"] and
     .gateway_targets == ["haptic-bench/haptic"] and
     .gateway_api.version == "v1.4.0" and .gateway_api.channel == "experimental" and
     .haproxy_version == "3.4" and
     .probe_routes == 300 and .probe_timeout == "45m" and
     .timings.requested.min_deployment_interval == "100ms" and
     .timings.requested.gateway_and_httproute_debounce == "100ms" and
     .scale.namespaces == 50 and .scale.routes_per_namespace == 100 and
     .scale.duration == "10m" and .scale.startup_timeout == "20m" and
     .routechange.iterations == 20 and .routechange.grace_period == "200ms" and
     .routechange.timeout == "10m" and
     .haptic_dirty_allowed == false and .build_only == false and
     .haptic_source_provenance.classification == "clean" and
     .cluster.reuse_requested == false and .cluster.keep_created == false and
     .cluster.coscheduled_clusters_allowed == false and
     .cluster.provenance_class == "fresh-controlled" and
     .comparison.published_workload_inputs_match == false and
     .comparison.controlled_default_profile == false and
     (.comparison.profile_deviation_reasons | index("probe route count differs from the published workload")) != null and
     (.comparison.profile_deviation_reasons | index("minDeploymentInterval override requested")) != null and
     (.comparison.profile_deviation_reasons | index("watch debounce override requested")) != null
   ' "$BENCH_RAW_OUTPUT/metadata.json" >/dev/null; then
  trusted=true
fi

minimal_invalid=false
expected_minimal=$(printf '%s\n' \
  artifact-security-invalid.json \
  runner-exit-code.txt \
  runner-finished-at.txt \
  runner-original-exit-code.txt \
  runner-summary.json | sort)
actual_minimal=
if [ -d "$BENCH_RAW_OUTPUT" ] && [ ! -L "$BENCH_RAW_OUTPUT" ]; then
  actual_minimal=$(find "$BENCH_RAW_OUTPUT" -mindepth 1 -maxdepth 1 -printf '%f\n' | sort)
fi
if [ -d "$BENCH_RAW_OUTPUT" ] &&
   [ ! -L "$BENCH_RAW_OUTPUT" ] &&
   [ "$actual_minimal" = "$expected_minimal" ] &&
   [ "$runner_rc" -ne 0 ] &&
   [ -f "$BENCH_RAW_OUTPUT/artifact-security-invalid.json" ] &&
   [ ! -L "$BENCH_RAW_OUTPUT/artifact-security-invalid.json" ] &&
   [ -f "$BENCH_RAW_OUTPUT/runner-summary.json" ] &&
   [ ! -L "$BENCH_RAW_OUTPUT/runner-summary.json" ] &&
   [ -f "$BENCH_RAW_OUTPUT/runner-exit-code.txt" ] &&
   [ ! -L "$BENCH_RAW_OUTPUT/runner-exit-code.txt" ] &&
   [ -f "$BENCH_RAW_OUTPUT/runner-original-exit-code.txt" ] &&
   [ ! -L "$BENCH_RAW_OUTPUT/runner-original-exit-code.txt" ] &&
   [ -f "$BENCH_RAW_OUTPUT/runner-finished-at.txt" ] &&
   [ ! -L "$BENCH_RAW_OUTPUT/runner-finished-at.txt" ] &&
   [ "$(cat "$BENCH_RAW_OUTPUT/runner-exit-code.txt")" = "$runner_rc" ] &&
   [ "$(wc -l < "$BENCH_RAW_OUTPUT/runner-original-exit-code.txt")" -eq 1 ] &&
   grep -Eq '^[0-9]+$' "$BENCH_RAW_OUTPUT/runner-original-exit-code.txt" &&
   [ "$(cat "$BENCH_RAW_OUTPUT/runner-original-exit-code.txt")" -le 255 ] &&
   [ "$(wc -l < "$BENCH_RAW_OUTPUT/runner-finished-at.txt")" -eq 1 ] &&
   grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{9}Z$' "$BENCH_RAW_OUTPUT/runner-finished-at.txt" &&
   jq -e '
     keys == ["artifact_scan_failed", "artifacts_retained", "pass", "reason", "reason_code", "schema_version", "secret_capture_failed", "status", "timestamp"] and
     .schema_version == 1 and .pass == false and .status == "invalid" and
     .reason == "live Secret inventory or artifact scan was untrusted" and
     .reason_code == "artifact-security-untrusted" and
     (.secret_capture_failed | type) == "boolean" and
     (.artifact_scan_failed | type) == "boolean" and
     (.secret_capture_failed or .artifact_scan_failed) and .artifacts_retained == false and
     (.timestamp | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\\.[0-9]{9}Z$"))
   ' "$BENCH_RAW_OUTPUT/artifact-security-invalid.json" >/dev/null &&
   jq -e --argjson runner_rc "$runner_rc" '
     keys == ["finished_at", "harness", "measured_result", "public_comparison", "scenarios", "schema_version"] and
     (.harness | keys) == ["final_exit_code", "pass", "process_exit_semantics", "secret_inventory_trusted", "status"] and
     .schema_version == 1 and .harness.pass == false and .harness.status == "invalid" and
     .harness.final_exit_code == $runner_rc and .harness.secret_inventory_trusted == false and
     .harness.process_exit_semantics == "nonzero means harness, provenance, or evidence invalid; measured gaps remain structured results" and
     .public_comparison == "ballpark-only" and .scenarios == [] and .measured_result == null and
     (.finished_at | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\\.[0-9]{9}Z$"))
   ' "$BENCH_RAW_OUTPUT/runner-summary.json" >/dev/null; then
  minimal_invalid=true
fi

install -d -m 0700 "$BENCH_STAGED_OUTPUT"
if [ "$trusted" = true ] || [ "$minimal_invalid" = true ]; then
  cp -a "$BENCH_RAW_OUTPUT/." "$BENCH_STAGED_OUTPUT/"
else
  jq -n --argjson runner_exit_code "$runner_rc" '
    {schema_version: 1, status: "invalid", reason: "benchmark artifacts failed CI staging validation",
     runner_exit_code: $runner_exit_code, artifacts_retained: false}
  ' > "$BENCH_STAGED_OUTPUT/ci-wrapper-invalid.json"
  [ "$runner_rc" -ne 0 ] || runner_rc=1
fi
exit "$runner_rc"

