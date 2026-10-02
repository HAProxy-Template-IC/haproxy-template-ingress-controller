#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT
cp -a charts/haptic "$test_dir/chart"
chart="$test_dir/chart"

yq -i '.cache.varnish.image = ""' "$chart/values.yaml"
yq -i '.appVersion = "test-chart-version"' "$chart/Chart.yaml"
[[ "$(bash scripts/chart-varnish-image.sh "$chart")" == "registry.gitlab.com/haproxy-haptic/haptic/varnish:test-chart-version" ]]
yq -i '.cache.varnish.image = "example.invalid/cache@sha256:abc"' "$chart/values.yaml"
[[ "$(bash scripts/chart-varnish-image.sh "$chart")" == "example.invalid/cache@sha256:abc" ]]
yq -i '.cache.varnish.image = " "' "$chart/values.yaml"
if bash scripts/chart-varnish-image.sh "$chart" >"$test_dir/invalid.log" 2>&1; then
    echo "Varnish image resolver accepted whitespace" >&2
    exit 1
fi
grep -q 'cache.varnish.image must be an image reference' "$test_dir/invalid.log"
echo "Chart Varnish image tests passed"
