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

printf 'bench-gateway-api shell tests: OK\n'
