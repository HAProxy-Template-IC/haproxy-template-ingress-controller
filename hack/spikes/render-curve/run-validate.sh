#!/usr/bin/env bash
# Full in-controller validation cost: run each benchmark test through
# `haptic validate` with and without a haproxy_valid assertion. The difference
# is the 3-phase cost (client-native syntax + OpenAPI schema + `haproxy -dr -c`)
# the admission webhook pays on top of the render.
set -uo pipefail
SPIKE="${SPIKE:-$(cd "$(dirname "$0")" && pwd)}"
cd "$SPIKE/repo"
SCHEMAS="$SPIKE/repo/tests/schemas"

for kind in httproute ingress; do
  for n in 300 1000 3000; do
    src="$SPIKE/configs/${kind}-${n}.yaml"
    aug="$SPIKE/configs/${kind}-${n}-hv.yaml"
    test="benchmark-${kind}-${n}"
    if [[ ! -f "$aug" ]]; then
      yq "(select(.kind == \"HAProxyTemplateConfig\") | .spec.validationTests.\"${test}\".assertions) += [{\"type\":\"haproxy_valid\"}]" \
        "$src" > "$aug"
    fi
    echo "########## ${kind}-${n} ##########"
    for r in 1 2 3; do
      ./bin/haptic validate -f "$src" --test "$test" --schema-dir "$SCHEMAS" \
        > "$SPIKE/raw/val-${kind}-${n}-plain-r${r}.txt" 2>&1
      ./bin/haptic validate -f "$aug" --test "$test" --schema-dir "$SCHEMAS" \
        > "$SPIKE/raw/val-${kind}-${n}-hv-r${r}.txt" 2>&1
    done
    echo -n "  plain : "; grep -h "^Tests:" "$SPIKE/raw/val-${kind}-${n}-plain-r"*.txt | tr '\n' ' '; echo
    echo -n "  hvalid: "; grep -h "^Tests:" "$SPIKE/raw/val-${kind}-${n}-hv-r"*.txt | tr '\n' ' '; echo
    grep -h "haproxy_valid" "$SPIKE/raw/val-${kind}-${n}-hv-r1.txt" | head -2
  done
done
