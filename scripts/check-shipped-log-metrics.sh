#!/usr/bin/env bash
# Keep vector.logMetrics.deniedBy.values in step with the deny reasons the chart
# actually emits.
#
# The entry is `kind: enum`, so a reason missing from the list is silently NOT
# counted — the metric keeps looking healthy while the newest control's denials
# are invisible. That is the failure this guards: adding a
# `set-var(txn.denied_by) str(<new>)` without touching values.yaml.
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART="$REPO/charts/haptic"

emitted="$(grep -rhoE 'set-var\(txn\.denied_by\) str\([a-z0-9_]+\)' "$CHART" \
  | grep -oE 'str\([a-z0-9_]+\)' | sed 's/str(//; s/)//' | sort -u)"

declared="$(python3 -c '
import sys, yaml
v = yaml.safe_load(open(sys.argv[1]))["vector"]["logMetrics"]["deniedBy"]["values"]
print("\n".join(sorted(set(v))))' "$CHART/values.yaml")"

missing="$(comm -23 <(printf '%s\n' "$emitted") <(printf '%s\n' "$declared"))"
extra="$(comm -13 <(printf '%s\n' "$emitted") <(printf '%s\n' "$declared"))"

rc=0
if [ -n "$missing" ]; then
  echo "FAIL: denied_by reasons emitted by the chart but absent from vector.logMetrics.deniedBy.values:" >&2
  printf '  %s\n' $missing >&2
  echo "  Those denials would not be counted. Add them to charts/haptic/values.yaml." >&2
  rc=1
fi
if [ -n "$extra" ]; then
  echo "FAIL: values declared in vector.logMetrics.deniedBy.values that the chart never emits:" >&2
  printf '  %s\n' $extra >&2
  echo "  Remove them, or the metric advertises reasons that cannot occur." >&2
  rc=1
fi
# The cache counters carry `requires: cache.varnish.enabled`, resolved at
# helm time, so no validationTest can assert them. They were dropped from
# values.yaml once already while the library, the CR template and two docs pages
# still promised them, and nothing caught it.
for entry in cacheStatus cacheAge cacheReason cacheDegraded deniedBy rateLimitDegraded wafDegraded schemaDegraded; do
  if ! python3 -c '
import sys, yaml
lm = yaml.safe_load(open(sys.argv[1]))["vector"]["logMetrics"]
sys.exit(0 if sys.argv[2] in lm else 1)' "$CHART/values.yaml" "$entry"; then
    echo "FAIL: vector.logMetrics.$entry is missing from values.yaml; the docs still promise it." >&2
    rc=1
  fi
done

[ "$rc" -eq 0 ] && echo "OK: denied_by values match the chart ($(printf '%s\n' "$emitted" | wc -l) reasons), shipped log metrics present"
exit $rc
