wait_upgrade_traffic() {
  local phase="$1" deployment pods pod
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
    probe_upgrade_pod "$pod" || return 1
    info "$phase: $pod serves the existing HTTP and HTTPS routes"
  done
  k get pods -o json > "$ARTIFACTS/$phase-pods.json" || return 1
  k logs -l app.kubernetes.io/component=controller --all-containers --prefix \
    --tail=-1 > "$ARTIFACTS/$phase-controller.log" || return 1
  if grep -E 'Rendered output rejected|content differs from its plan file|ArtifactContentMismatch' "$ARTIFACTS/$phase-controller.log"; then
    fail "$phase: controller rejected inconsistent rendered output"
  fi
}

probe_upgrade_pod() (
  local pod="$1" forward_log forward_pid deadline http_port https_port targets http_target https_target
  targets="$(k get pod "$pod" -o json | python3 -c '
import json, sys
ports = [port for container in json.load(sys.stdin)["spec"]["containers"] for port in container.get("ports", [])]
targets = []
for name in ("http", "https"):
    matches = [port["containerPort"] for port in ports if port.get("name") == name]
    if len(matches) != 1 or not 0 < matches[0] < 65536:
        sys.exit("expected one valid " + name + " container port")
    targets.append(matches[0])
print(*targets)
')" || return 1
  read -r http_target https_target <<< "$targets"
  forward_log="$(mktemp "$WORK/forward.XXXXXX.log")" || return 1
  kubectl --context "$CTX" -n "$NS" port-forward --address=127.0.0.1 \
    "pod/$pod" :http :https > "$forward_log" 2>&1 &
  forward_pid=$!
  trap 'kill "$forward_pid" 2>/dev/null || true; wait "$forward_pid" 2>/dev/null || true' EXIT
  deadline=$((SECONDS + 30))
  while :; do
    if ! kill -0 "$forward_pid" 2>/dev/null || [ "$SECONDS" -ge "$deadline" ]; then
      cat "$forward_log"
      return 1
    fi
    http_port="$(sed -n "s/^Forwarding from 127\.0\.0\.1:\([0-9][0-9]*\) -> $http_target\$/\1/p" "$forward_log")"
    https_port="$(sed -n "s/^Forwarding from 127\.0\.0\.1:\([0-9][0-9]*\) -> $https_target\$/\1/p" "$forward_log")"
    [ -n "$http_port" ] && [ -n "$https_port" ] && break
    sleep 1
  done
  deadline=$((SECONDS + 120))
  until probe_upgrade_route http http.upgrade.test "$http_port" && probe_upgrade_route https tls.upgrade.test "$https_port"; do
    if ! kill -0 "$forward_pid" 2>/dev/null || [ "$SECONDS" -ge "$deadline" ]; then
      cat "$forward_log" "$WORK/response.json" "$WORK/probe.log"
      return 1
    fi
    sleep 1
  done
)

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
