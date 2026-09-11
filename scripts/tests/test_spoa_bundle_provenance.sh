#!/usr/bin/env bash
set -euo pipefail

runner="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/bench-gateway-api.sh"
source "$runner"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT
BENCH_OUTPUT_DIR="$test_dir"
mkdir "$test_dir/cluster"
test_mode=valid
chart_image="$(bash "${PROJECT_ROOT}/scripts/chart-spoa-image.sh")"
test_image_id="sha256:$(printf 'a%.0s' {1..64})"

checksums() {
    printf 'hub-sha  /usr/local/bin/haproxy-spoa-hub\nplugin-sha  /etc/haproxy-spoa-hub/plugins/libmirror_plugin.so\n'
}

docker() {
    if [[ "$1" == image ]]; then
        [[ "$3" == "$chart_image" ]] || return 1
        if [[ "$test_mode" == missing-image-id ]]; then
            printf '[]\n'
        else
            jq -n --arg id "$test_image_id" '[{Id: $id}]'
        fi
    else
        [[ "$*" == *"--entrypoint sh $test_image_id "* ]] || return 1
        if [[ "$test_mode" != empty-checksums ]]; then
            checksums
        fi
    fi
}

kubectl() {
    if [[ "$1" == get ]]; then
        jq -n --arg mode "$test_mode" --arg image "$chart_image" '{items: ["spoa-hub", "validators"] | map(. as $name |
          {metadata: {name: $name},
           spec: {containers: [{name: $name, image: (if $mode == "wrong-image" then "old:release" else $image end), imagePullPolicy: "IfNotPresent"}]},
           status: {containerStatuses: (if $mode == "missing-status" then [] else
             [{name: $name, ready: ($mode != "not-ready"), imageID: "sha256:test"}] end)}})} |
          if $mode == "missing-validators" then .items |= map(select(.metadata.name != "validators")) else . end'
    elif [[ "$test_mode" == wrong-hub ]]; then
        printf 'old-hub-sha  /usr/local/bin/haproxy-spoa-hub\nplugin-sha  /etc/haproxy-spoa-hub/plugins/libmirror_plugin.so\n'
    elif [[ "$test_mode" == wrong-plugin ]]; then
        printf 'hub-sha  /usr/local/bin/haproxy-spoa-hub\nold-plugin-sha  /etc/haproxy-spoa-hub/plugins/libmirror_plugin.so\n'
    else
        checksums
    fi
}

verify_spoa_bundle
verify_spoa_bundle after
for test_mode in missing-image-id empty-checksums wrong-image missing-status missing-validators not-ready wrong-hub wrong-plugin; do
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
test_mode=valid
verify_spoa_bundle
printf '%s\n' old:release > "$test_dir/cluster/spoa-bundle-image-reference.txt"
if (verify_spoa_bundle after) > "$test_dir/reference-drift.log" 2>&1; then
    printf 'SPOA bundle gate accepted chart image reference drift\n' >&2
    exit 1
fi

jq -n '{spoaHub: {image: {repository: "example.test/hub", tag: "1.2.3", pullPolicy: "IfNotPresent"}}}' > "$test_dir/defaults.json"
assert_effective_profile "$test_dir/defaults.json" "$test_dir/defaults.json" "$test_dir/profile.json"
for field in repository tag pullPolicy; do
    jq --arg field "$field" '.spoaHub.image[$field] = "changed"' "$test_dir/defaults.json" > "$test_dir/effective.json"
    if (assert_effective_profile "$test_dir/defaults.json" "$test_dir/effective.json" "$test_dir/profile.json") > "$test_dir/profile-${field}.log" 2>&1; then
        printf 'Benchmark accepted a SPOA image %s override\n' "$field" >&2
        exit 1
    fi
done
printf 'SPOA bundle provenance tests passed\n'
