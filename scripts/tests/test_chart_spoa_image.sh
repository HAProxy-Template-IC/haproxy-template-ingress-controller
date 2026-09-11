#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT
cp -a charts/haptic "$test_dir/chart"
resolver=scripts/chart-spoa-image.sh
chart="$test_dir/chart"

repository="$(yq -r '.spoaHub.image.repository' "$chart/values.yaml")"
yq -i '.spoaHub.image.tag = ""' "$chart/values.yaml"
yq -i '.appVersion = "test-chart-version"' "$chart/Chart.yaml"
[[ "$(bash "$resolver" "$chart")" == "$repository:test-chart-version" ]]
yq -i '.spoaHub.image.repository = "example.invalid/hub" | .spoaHub.image.tag = "test-explicit-version"' "$chart/values.yaml"
[[ "$(bash "$resolver" "$chart")" == "example.invalid/hub:test-explicit-version" ]]
yq -i '.spoaHub.enabled = false' "$chart/values.yaml"
if bash "$resolver" "$chart" >/dev/null 2>&1; then
    echo "SPOA image resolver accepted a chart without its SPOA container" >&2
    exit 1
fi

mkdir "$test_dir/bin"
printf '#!/usr/bin/env bash\necho "Unexpected Docker invocation" >&2\nexit 77\n' > "$test_dir/bin/docker"
chmod 0755 "$test_dir/bin/docker"
if PATH="$test_dir/bin:$PATH" make --no-print-directory test-spoa-reload \
    SPOA_RELOAD_IMAGE=example.invalid/hub:not-the-chart > "$test_dir/override.log" 2>&1; then
    echo "SPOA reload test accepted a conflicting image override" >&2
    exit 1
fi
grep -q 'SPOA_RELOAD_IMAGE differs from chart image' "$test_dir/override.log"
if grep -q 'Unexpected Docker invocation' "$test_dir/override.log"; then
    echo "SPOA reload test reached Docker before rejecting the override" >&2
    exit 1
fi
echo "Chart SPOA image tests passed"
