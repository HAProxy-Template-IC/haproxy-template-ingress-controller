#!/usr/bin/env bash
# Every kind the chart's ValidatingWebhookConfiguration routes to the controller
# must have a Go handler registered for it.
#
# An unregistered GVK is a SILENT ALLOW: pkg/webhook/server.go returns
# Allowed:true with no log when it has no validator for the incoming kind. So a
# chart rule shipped ahead of its handler — or a Kind-string typo — disables that
# gate invisibly, which is indistinguishable from a gate that is working.
#
# Scope: HAPTIC's own CRDs (haproxy*). Watched-resource kinds like ingresses and
# httproutes are routed through generic per-rule bridges rather than a named
# handler, so a name match would false-positive on them.
#
# Usage: scripts/check-webhook-kinds.sh
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VWC="$REPO/charts/haptic/templates/validatingwebhookconfiguration.yaml"
REGISTRY="$REPO/pkg/controller/webhook/component.go"

[ -f "$VWC" ] || { echo "not found: $VWC" >&2; exit 1; }
[ -f "$REGISTRY" ] || { echo "not found: $REGISTRY" >&2; exit 1; }

# The plural resource names the chart routes, taken from the `resources:` lists
# under the haproxy-haptic.org apiGroup. `|| true` because a template with none
# is a legitimate state, and under `set -o pipefail` an empty grep would abort.
routed="$(grep -oE '^\s+- haproxy[a-z]+$' "$VWC" | tr -d ' -' | sort -u || true)"

missing=""
for resource in $routed; do
  # Registration is by Kind, not plural, so match case-insensitively on the
  # resource name with the trailing plural 's' removed.
  singular="${resource%s}"
  if ! grep -qiE "\"?${singular}" "$REGISTRY"; then
    missing="$missing $resource"
  fi
done

if [ -n "$missing" ]; then
  echo "ERROR: the chart's ValidatingWebhookConfiguration routes kinds with no Go handler:" >&2
  for m in $missing; do echo "  - $m" >&2; done
  echo "" >&2
  echo "An unregistered GVK is admitted with no log (pkg/webhook/server.go), so the" >&2
  echo "rule would look present while validating nothing. Register the handler in" >&2
  echo "$(realpath --relative-to="$REPO" "$REGISTRY") first, then add the chart rule." >&2
  exit 1
fi

echo "✓ every webhook-routed kind has a Go handler ($(echo "$routed" | wc -w) checked)"
