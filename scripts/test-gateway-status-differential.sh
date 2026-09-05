#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
project_root="$(cd "${script_dir}/.." && pwd)"
controller_bin="${project_root}/bin/haptic"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/haptic-gateway-status-diff.XXXXXX")"
baseline="${scratch}/head"

cleanup() {
    git -C "$project_root" worktree remove --force "$baseline" >/dev/null 2>&1 || true
    rm -rf "$scratch"
}
trap cleanup EXIT

# The candidate engine's statusPatch signature rejects the pre-migration
# chart, so the baseline runs on its own standalone-built binary. The
# merge-base with main is the newest revision that builds without go.work.
baseline_rev="${HAPTIC_DIFFERENTIAL_BASELINE_REV:-$(git -C "$project_root" merge-base origin/main HEAD)}"
git -C "$project_root" worktree add --quiet --detach "$baseline" "$baseline_rev"
(cd "$baseline" && env -u GOROOT GOWORK=off go build -o "${scratch}/haptic-baseline" ./cmd/haptic)
baseline_bin="${scratch}/haptic-baseline"

render_config() {
    local root=$1
    local output=$2
    helm template "${root}/charts/haptic" \
        --namespace default \
        --api-versions=gateway.networking.k8s.io/v1/GatewayClass \
        --set controller.templateLibraries.gateway.enabled=true \
        --set controller.templateLibraries.gateway.experimentalChannel=true \
        | yq 'select(.kind == "HAProxyTemplateConfig" or .kind == "HAProxyTemplateLibrary")' \
        > "$output"
}

normalise_status() {
    local input=$1
    local output=$2
    python3 - "$input" "$output" <<'PY'
import json
import re
import sys

content = open(sys.argv[1], encoding="utf-8").read()
marker = "\n### Status Patches\n"
if marker not in content:
    raise SystemExit("status patch section is missing")

patches = {}
pattern = re.compile(r"\n#### ([^\n]+)\n-+\n(.*?)\n-+(?=\n|$)", re.S)
for name, payload in pattern.findall(content.split(marker, 1)[1]):
    value = json.loads(payload)

    def normalise(node):
        if isinstance(node, dict):
            return {
                key: "<transition-time>" if key == "lastTransitionTime" else normalise(child)
                for key, child in node.items()
            }
        if isinstance(node, list):
            return [normalise(child) for child in node]
        return node

    patches[name] = normalise(value)

if not patches:
    raise SystemExit("status patch section is empty")
with open(sys.argv[2], "w", encoding="utf-8") as output_file:
    json.dump(patches, output_file, sort_keys=True, separators=(",", ":"))
    output_file.write("\n")
PY
}

render_config "$project_root" "${scratch}/current.yaml"
render_config "$baseline" "${scratch}/baseline.yaml"

for test_name in \
    test-gateway-status-patches \
    test-listenerset-host-gateway-status \
    test-tlsroute-mixed-termination-protocol-conflict; do
    for version in current baseline; do
        bin="$controller_bin"
        schemas="${project_root}/tests/schemas"
        if [ "$version" = baseline ]; then
            bin="$baseline_bin"
            schemas="${baseline}/tests/schemas"
        fi
        "$bin" validate \
            --file "${scratch}/${version}.yaml" \
            --schema-dir "$schemas" \
            --workers 1 \
            --test "$test_name" \
            --dump-rendered \
            > "${scratch}/${version}-${test_name}.dump"
        normalise_status \
            "${scratch}/${version}-${test_name}.dump" \
            "${scratch}/${version}-${test_name}.json"
    done
    diff -u \
        "${scratch}/baseline-${test_name}.json" \
        "${scratch}/current-${test_name}.json"
done
