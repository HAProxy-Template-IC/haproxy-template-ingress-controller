#!/usr/bin/env bash
# Generate the browser-playground's runtime assets (schema bundle + preset
# configs/resources) into an output directory. These are build artifacts, not
# committed — the shell (cmd/playground/web/) references them by relative path,
# and CI regenerates them per release (see the browser-wasm-template-playground
# OpenSpec change, Milestones 4/5).
#
# Each preset is one `helm template` render with a specific set of template
# libraries enabled, so the config shown demonstrates exactly that library's
# templates/snippets. Resources are committed static kubectl `kind: List`s
# (cmd/playground/web/presets/*.resources.yaml); the per-vendor ones are derived
# from the ingress example by annotating its "shop" Ingress so the vendor's
# directives actually fire.
#
# Usage: scripts/gen-playground-assets.sh <output-dir>
set -euo pipefail

OUT="${1:?usage: gen-playground-assets.sh <output-dir>}"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART="$REPO/charts/haptic"
WEB="$REPO/cmd/playground/web"
SCHEMA_DIR="$REPO/tests/schemas"

command -v helm >/dev/null || { echo "helm not found" >&2; exit 1; }
command -v yq   >/dev/null || { echo "yq not found" >&2; exit 1; }

mkdir -p "$OUT/presets"

echo "==> schema bundle -> $OUT/schemas.json"
(cd "$REPO" && go run scripts/gen_playground_schema_bundle.go "$SCHEMA_DIR") > "$OUT/schemas.json"

# render_config <preset-id> <api-versions:yes|no> <overrides…>
# Each override is `key=value`, prefixed with `controller.templateLibraries.`
# unless it carries a `raw:` prefix (then it is passed to --set verbatim, for
# non-templateLibraries values like spoaHub.*).
render_config() {
  local id="$1" apiver="$2"; shift 2
  local sets=() av=()
  local kv
  for kv in "$@"; do
    if [[ "$kv" == raw:* ]]; then sets+=(--set "${kv#raw:}")
    else sets+=(--set "controller.templateLibraries.$kv"); fi
  done
  [ "$apiver" = yes ] && av=(--api-versions=gateway.networking.k8s.io/v1/GatewayClass)
  # The playground is the only consumer; the chart omits it by default (!1492).
  sets+=(--set controller.config.includeMigrationCoverage=true)
  echo "==> $id config -> $OUT/presets/$id.config.yaml"
  # The chart renders one HAProxyTemplateConfig per enabled template library
  # (ADR-0014), but the playground shows and renders a single config. Merge the
  # set through the controller's own merge rather than a yq-based one, so the
  # preset is exactly what the controller would assemble.
  local rendered="$OUT/presets/.$id.multi.yaml"
  helm template "$CHART" --namespace default "${av[@]}" "${sets[@]}" \
    | yq 'select(.kind == "HAProxyTemplateConfig" or .kind == "HAProxyValidationTests")' > "$rendered"
  (cd "$REPO" && go run ./cmd/controller validate -f "$rendered" --dump-merged) \
    > "$OUT/presets/$id.config.yaml"
  rm -f "$rendered"
}

# Defaults (no --set): ingress + gateway + haproxytech + haproxy-ingress on,
# nginx-ingress off. Each preset narrows to the one library it demonstrates.
render_config ingress         no  gateway.enabled=false haproxyIngress.enabled=false nginxIngress.enabled=false haproxytech.enabled=false
render_config haproxytech      no  gateway.enabled=false haproxyIngress.enabled=false nginxIngress.enabled=false
render_config haproxy-ingress  no  gateway.enabled=false haproxytech.enabled=false    nginxIngress.enabled=false
render_config nginx-ingress    no  gateway.enabled=false haproxytech.enabled=false    haproxyIngress.enabled=false nginxIngress.enabled=true
# HAPTIC-native's headline features are SPOE-powered (shared rate limiting, SPOE
# auth, traffic mirroring), so the library imports macros that only exist when
# the spoa-hub library is loaded. Enable spoa-hub (and the mirror plugin, which
# otherwise only turns on with the Gateway library) so the preset renders the
# native library as it is meant to run and its validationTests all pass.
render_config haptic-annotations no gateway.enabled=false haproxytech.enabled=false    haproxyIngress.enabled=false nginxIngress.enabled=false raw:spoaHub.enabled=true raw:spoaHub.plugins.mirror.enabled=true
render_config gateway          yes haproxyIngress.enabled=false nginxIngress.enabled=false haproxytech.enabled=false
render_config all              yes nginxIngress.enabled=true haproxytech.enabled=true haproxyIngress.enabled=true

# Static example resources (committed) for ingress / gateway / all.
for id in ingress gateway all; do
  echo "==> $id resources -> $OUT/presets/$id.resources.yaml"
  cp "$WEB/presets/$id.resources.yaml" "$OUT/presets/$id.resources.yaml"
done

# Per-vendor resources = the ingress example with the "shop" Ingress annotated so
# the vendor's snippets fire (annotation keys mirror each upstream controller).
gen_vendor_resources() {
  local id="$1" annotations="$2"
  echo "==> $id resources ($id-annotated) -> $OUT/presets/$id.resources.yaml"
  yq "(.items[] | select(.kind == \"Ingress\" and .metadata.name == \"shop\").metadata.annotations) += $annotations" \
    "$WEB/presets/ingress.resources.yaml" > "$OUT/presets/$id.resources.yaml"
}
gen_vendor_resources haproxytech     '{"haproxy.org/timeout-connect":"5s","haproxy.org/timeout-server":"45s","haproxy.org/load-balance":"leastconn","haproxy.org/check-http":"/healthz","haproxy.org/check-interval":"2s","haproxy.org/scale-server-slots":"10","haproxy.org/server-proto":"h2","haproxy.org/request-set-header":"X-Request-Source haproxy-edge","haproxy.org/response-set-header":"X-Frame-Options DENY","haproxy.org/rate-limit-requests":"100","haproxy.org/rate-limit-period":"10s","haproxy.org/cookie-persistence-no-dynamic":"SERVERID","haproxy.org/cors-enable":"true","haproxy.org/cors-allow-methods":"GET, POST, OPTIONS","haproxy.org/ssl-redirect":"true","haproxy.org/allow-list":"10.0.0.0/8, 192.168.0.0/16"}'
gen_vendor_resources haproxy-ingress '{"haproxy-ingress.github.io/balance-algorithm":"leastconn","haproxy-ingress.github.io/timeout-server":"60s","haproxy-ingress.github.io/timeout-connect":"5s","haproxy-ingress.github.io/maxconn-server":"256","haproxy-ingress.github.io/backend-check-interval":"2s","haproxy-ingress.github.io/health-check-uri":"/healthz","haproxy-ingress.github.io/affinity":"cookie","haproxy-ingress.github.io/session-cookie-name":"SHOPSESSION","haproxy-ingress.github.io/forwardfor":"add","haproxy-ingress.github.io/headers":"X-Env:production|X-Frontend:haptic","haproxy-ingress.github.io/cors-enable":"true","haproxy-ingress.github.io/cors-allow-origin":"https://shop.example.com","haproxy-ingress.github.io/backend-protocol":"h2","haproxy-ingress.github.io/limit-rps":"10","haproxy-ingress.github.io/allowlist-source-range":"10.0.0.0/8,192.168.0.0/16","haproxy-ingress.github.io/app-root":"/web"}'
# nginx: two supported directives (fire in haproxy.cfg) plus two annotations the
# migration tab flags — hsts (different: emitted only when explicitly true) and
# server-snippet (dropped: no HAProxy equivalent) — so the migration report
# demonstrates a real mixed verdict rather than an all-clear.
gen_vendor_resources haptic-annotations '{"haproxy-haptic.org/load-balance":"leastconn","haproxy-haptic.org/timeout-connect":"5s","haproxy-haptic.org/timeout-server":"45s","haproxy-haptic.org/health-check-uri":"/healthz","haproxy-haptic.org/health-check-interval":"2s","haproxy-haptic.org/maxconn-server":"256","haproxy-haptic.org/pod-maxconn":"2000","haproxy-haptic.org/affinity":"cookie","haproxy-haptic.org/session-cookie-name":"SHOPSESSION","haproxy-haptic.org/forwardfor":"add","haproxy-haptic.org/cors-enable":"true","haproxy-haptic.org/cors-allow-origin":"https://shop.example.com","haproxy-haptic.org/allowlist-source-range":"10.0.0.0/8,192.168.0.0/16","haproxy-haptic.org/https-redirect":"true","haproxy-haptic.org/root-redirect":"/web"}'
gen_vendor_resources nginx-ingress   '{"nginx.ingress.kubernetes.io/proxy-connect-timeout":"10","nginx.ingress.kubernetes.io/proxy-read-timeout":"60","nginx.ingress.kubernetes.io/load-balance":"ewma","nginx.ingress.kubernetes.io/backend-protocol":"HTTPS","nginx.ingress.kubernetes.io/limit-rps":"10","nginx.ingress.kubernetes.io/app-root":"/dashboard","nginx.ingress.kubernetes.io/whitelist-source-range":"10.0.0.0/8,192.168.0.0/16","nginx.ingress.kubernetes.io/denylist-source-range":"203.0.113.0/24","nginx.ingress.kubernetes.io/custom-response-headers":"X-Frame-Options:SAMEORIGIN|X-Content-Type-Options:nosniff","nginx.ingress.kubernetes.io/upstream-vhost":"internal.shop.svc","nginx.ingress.kubernetes.io/proxy-next-upstream-tries":"3","nginx.ingress.kubernetes.io/affinity":"cookie","nginx.ingress.kubernetes.io/session-cookie-name":"SHOPSESSIONID","nginx.ingress.kubernetes.io/enable-cors":"true","nginx.ingress.kubernetes.io/cors-allow-origin":"https://shop.example.com","nginx.ingress.kubernetes.io/hsts":"true"}'

# extend = the ingress preset + the landing's request-id snippet, shown against an
# Ingress that opts in via the example.com/request-id-header annotation. render_glob
# "frontend-filters-*" in haproxyConfig.template auto-invokes the merged snippet in
# name order (after -250-request-body-size), so no config-template edit is needed;
# only the "shop" Ingress carries the annotation, so the header line is emitted once
# per frontend for it.
echo "==> extend config -> $OUT/presets/extend.config.yaml"
yq ".spec.templateSnippets += load(\"$WEB/presets/extend.snippet.yaml\")" \
  "$OUT/presets/ingress.config.yaml" > "$OUT/presets/extend.config.yaml"
gen_vendor_resources extend '{"example.com/request-id-header":"X-Request-ID"}'

echo "==> done. $(wc -c < "$OUT/schemas.json") bytes of schema, $(ls "$OUT/presets"/*.config.yaml | wc -l) preset configs"
