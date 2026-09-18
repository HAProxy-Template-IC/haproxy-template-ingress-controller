wait_upgrade_traffic() {
  local phase="$1" deployment pods pod forward_pid deadline http_port https_port
  deployment="$(k get deployments -o json | python3 -c '
import json, sys
items = [item["metadata"]["name"] for item in json.load(sys.stdin)["items"]
         if item["metadata"]["name"].endswith("-haproxy")]
if len(items) != 1:
    sys.exit("expected one HAProxy deployment")
print(items[0])')" || return 1
  k rollout status "deployment/$deployment" --timeout=7m || return 1
  pods="$(k get pods -l app.kubernetes.io/component=loadbalancer -o json | python3 -c '
import json, sys
print("\n".join(item["metadata"]["name"] for item in json.load(sys.stdin)["items"]
                if not item["metadata"].get("deletionTimestamp")))')" || return 1
  [ "$(wc -w <<<"$pods")" -eq 2 ] || fail "$phase: expected both HAProxy replicas"
  k get secret upgrade-default-tls -o jsonpath='{.data.tls\.crt}' | base64 -d > "$WORK/upgrade.crt" || return 1
  for pod in $pods; do
    k wait "pod/$pod" --for=condition=Ready --timeout=180s || return 1
    k port-forward --address=127.0.0.1 "pod/$pod" :http :https > "$WORK/forward.log" 2>&1 &
    forward_pid=$!
    deadline=$((SECONDS + 30))
    while [ "$(grep -c '^Forwarding from' "$WORK/forward.log")" -lt 2 ]; do
      if ! kill -0 "$forward_pid" 2>/dev/null || [ "$SECONDS" -ge "$deadline" ]; then
        kill "$forward_pid" 2>/dev/null || true
        wait "$forward_pid" 2>/dev/null || true
        cat "$WORK/forward.log"
        return 1
      fi
      sleep 1
    done
    http_port="$(sed -n '1s/.*127\.0\.0\.1:\([0-9]*\).*/\1/p' "$WORK/forward.log")"
    https_port="$(sed -n '2s/.*127\.0\.0\.1:\([0-9]*\).*/\1/p' "$WORK/forward.log")"
    deadline=$((SECONDS + 120))
    until probe_upgrade_route http http.upgrade.test "$http_port" && probe_upgrade_route https tls.upgrade.test "$https_port"; do
      if [ "$SECONDS" -ge "$deadline" ]; then
        kill "$forward_pid" 2>/dev/null || true
        wait "$forward_pid" 2>/dev/null || true
        cat "$WORK/response.json" "$WORK/probe.log"
        return 1
      fi
      sleep 1
    done
    kill "$forward_pid" 2>/dev/null || true
    wait "$forward_pid" 2>/dev/null || true
    info "$phase: $pod serves the existing HTTP and HTTPS routes"
  done
  k get pods -o json > "$ARTIFACTS/$phase-pods.json" || return 1
  k logs -l app.kubernetes.io/component=controller --all-containers --prefix \
    --tail=-1 > "$ARTIFACTS/$phase-controller.log" || return 1
  if grep -E 'Rendered output rejected|content differs from its plan file|ArtifactContentMismatch' "$ARTIFACTS/$phase-controller.log"; then
    fail "$phase: controller rejected inconsistent rendered output"
  fi
}

probe_upgrade_route() {
  local scheme="$1" host="$2" port="$3"
  curl --silent --show-error --fail --noproxy '*' --max-time 5 \
    --cacert "$WORK/upgrade.crt" --resolve "$host:$port:127.0.0.1" \
    "$scheme://$host:$port/upgrade-check" > "$WORK/response.json" 2> "$WORK/probe.log" || return 1
  python3 - "$WORK/response.json" <<'PY' 2>> "$WORK/probe.log"
import json, sys
response = json.load(open(sys.argv[1]))
if not response.get("environment", {}).get("HOSTNAME", "").startswith("upgrade-backend-"):
    sys.exit("request did not reach the upgrade backend")
if response.get("http", {}).get("originalUrl") != "/upgrade-check":
    sys.exit("request path changed during the upgrade")
PY
}
