#!/usr/bin/env bash
set -euo pipefail

runner="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/bench-gateway-api.sh"
source "$runner"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT
BENCH_OUTPUT_DIR="$test_dir"
mkdir "$test_dir/cluster"
test_mode=valid

docker() {
    if [[ "$1" == image ]]; then
        printf '[]\n'
    elif [[ "$*" == *'--entrypoint cat'* ]]; then
        if [[ "$test_mode" == stale-pins ]]; then
            printf 'SPOA_HUB_VERSION=v0.7.3\n'
        else
            command cat "${PROJECT_ROOT}/versions-spoa.env"
        fi
    else
        printf 'hub-sha  /usr/local/bin/haproxy-spoa-hub\nplugin-sha  /etc/haproxy-spoa-hub/plugins/libmirror_plugin.so\n'
    fi
}

kubectl() {
    if [[ "$1" == get ]]; then
        jq -n --arg mode "$test_mode" '{items: ["spoa-hub", "validators"] | map(. as $name |
          {metadata: {name: $name},
           spec: {containers: [{name: $name, image: (if $mode == "wrong-image" then "old:release" else "spoa-hub:dev" end), imagePullPolicy: "Never"}]},
           status: {containerStatuses: (if $mode == "missing-status" then [] else
             [{name: $name, ready: ($mode != "not-ready"), imageID: "sha256:test"}] end)}})} |
          if $mode == "missing-validators" then .items |= map(select(.metadata.name != "validators")) else . end'
    elif [[ "$test_mode" == wrong-hub ]]; then
        printf 'old-hub-sha  /usr/local/bin/haproxy-spoa-hub\nplugin-sha  /etc/haproxy-spoa-hub/plugins/libmirror_plugin.so\n'
    elif [[ "$test_mode" == wrong-plugin ]]; then
        printf 'hub-sha  /usr/local/bin/haproxy-spoa-hub\nold-plugin-sha  /etc/haproxy-spoa-hub/plugins/libmirror_plugin.so\n'
    else
        docker checksums
    fi
}

verify_spoa_bundle
verify_spoa_bundle after
for test_mode in stale-pins wrong-image missing-status missing-validators not-ready wrong-hub wrong-plugin; do
    if (verify_spoa_bundle) > "$test_dir/${test_mode}.log" 2>&1; then
        printf 'SPOA bundle gate accepted %s\n' "$test_mode" >&2
        exit 1
    fi
done
test_mode=valid
verify_spoa_bundle
test_mode=wrong-plugin
if (verify_spoa_bundle after) > "$test_dir/after-drift.log" 2>&1; then
    printf 'SPOA bundle gate accepted post-measurement drift\n' >&2
    exit 1
fi
printf 'SPOA bundle provenance tests passed\n'
