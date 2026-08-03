#!/usr/bin/env bash
# check-library-yaml.sh — every template-library file must be parseable YAML.
#
# Helm reads these with Files.Get + fromYaml, and fromYaml on a malformed
# document does not fail the render: it yields whatever it managed to parse. A
# library that stops being valid YAML therefore renders a chart that installs
# cleanly and is quietly missing templateSnippets and validationTests — the
# suite goes green with fewer tests than it had, which is the failure the
# test-inventory gate exists to prevent, arriving through a different door.
#
# Provenance: a Scriggo `{#- ... -#}` comment written inside a validationTests
# block (valid only inside a `template:` string) silently dropped 22 tests from
# the run while every gate stayed green.
set -euo pipefail
cd "$(dirname "$0")/.."

fail=0

while IFS= read -r f; do
  if ! err=$(python3 -c '
import sys, yaml
try:
    yaml.safe_load(open(sys.argv[1]))
except Exception as e:
    print(e)
    sys.exit(1)
' "$f" 2>&1); then
    echo "✗ $f is not valid YAML — Helm would drop its snippets and tests silently:"
    echo "$err" | sed 's/^/    /'
    fail=1
  fi
done < <(find charts/haptic/libraries charts/haptic/charts -name '*.yaml' -type f | sort)

if [ "$fail" -ne 0 ]; then
  echo ""
  echo "Scriggo comment syntax ({# ... #}) is only valid INSIDE a template: string."
  echo "Elsewhere in a library file, use a YAML comment (#)."
  exit 1
fi

echo "All template-library files parse as YAML"
