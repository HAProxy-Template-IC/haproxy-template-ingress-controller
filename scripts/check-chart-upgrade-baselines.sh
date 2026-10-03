#!/usr/bin/env bash
# Fail when the test-chart-upgrade matrix and the published upgrade baselines
# disagree, so a new release cannot go untested and a typo cannot test nothing.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

published="$("$ROOT/scripts/test-chart-upgrade.sh" --list-baselines | sort -u)"
[ -n "$published" ] || { echo "FAIL: could not list the published upgrade baselines." >&2; exit 1; }

matrix="$(yq '.["test-chart-upgrade"].parallel.matrix[].BASELINE_CHART_VERSION[]' "$ROOT/.gitlab-ci.yml" | sort -u)"

missing="$(comm -23 <(echo "$published") <(echo "$matrix"))"
unknown="$(comm -13 <(echo "$published") <(echo "$matrix"))"

rc=0
if [ -n "$missing" ]; then
    echo "FAIL: published upgrade baselines are not tested: $(echo "$missing" | tr '\n' ' ')" >&2
    echo "      Add them to test-chart-upgrade.parallel.matrix in .gitlab-ci.yml." >&2
    rc=1
fi
if [ -n "$unknown" ]; then
    echo "FAIL: test-chart-upgrade.parallel.matrix lists versions that are unpublished or above VERSION $(cat "$ROOT/VERSION"): $(echo "$unknown" | tr '\n' ' ')" >&2
    echo "      Remove them from .gitlab-ci.yml, or publish them first." >&2
    rc=1
fi
[ "$rc" -eq 0 ] && echo "OK: test-chart-upgrade covers every upgrade baseline: $(echo "$matrix" | tr '\n' ' ')"
exit "$rc"
