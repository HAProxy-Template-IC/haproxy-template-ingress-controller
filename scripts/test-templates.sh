#!/usr/bin/env bash
set -euo pipefail

# test-templates.sh - Test HAProxy template libraries
#
# This script wraps the correct workflow for testing template libraries:
# 1. Render merged HAProxyTemplateConfig using helm template
# 2. Extract the HAProxyTemplateConfig resource with yq
# 3. Pass to controller validate for testing
#
# This ensures you don't forget the helm template step when testing library changes.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
CHART_DIR="${PROJECT_ROOT}/charts/haptic"
CONTROLLER_BIN="${CONTROLLER_BIN:-${PROJECT_ROOT}/bin/haptic-controller}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Print warning message
warn() {
    echo -e "${YELLOW}Warning: $1${NC}" >&2
}

# Assert that one Helm values combination is rejected with the expected text.
# Some ownership-migration checks deliberately add a key absent from
# values.yaml; helm-unittest cannot preserve those unknown descendants through
# its per-leaf `set` merger, while Helm itself does. Keep these checks on the
# real `helm template --set` path operators use.
run_helm_failure_guard() {
    local label=$1
    local expected=$2
    shift 2
    local guard_err
    guard_err=$(mktemp /tmp/haptic-helm-guard-XXXXXX.log)
    echo -e "${YELLOW}${label}...${NC}" >&2
    if helm template "$CHART_DIR" --namespace default $HAPROXY_VERSION_ARG "$@" > /dev/null 2> "$guard_err"; then
        echo -e "${RED}Error: ${label} did not fail${NC}" >&2
        rm -f "$guard_err"
        exit 1
    fi
    if ! grep -Fq "$expected" "$guard_err"; then
        echo -e "${RED}Error: ${label} returned unexpected error:${NC}" >&2
        cat "$guard_err" >&2
        rm -f "$guard_err"
        exit 1
    fi
    rm -f "$guard_err"
}

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Test HAProxy template libraries by rendering the merged Helm chart and running
validation tests.

This script automates the correct workflow:
  1. helm template (with --api-versions for Gateway API)
  2. yq to extract HAProxyTemplateConfig
  3. controller validate to run tests

OPTIONS:
  --test NAME           Run specific test by name
  --workers N           Number of parallel test workers (0=auto-detect CPUs, 1=sequential, default: 0)
  --dump-rendered       Dump all rendered content
  --verbose             Show rendered content preview for failed assertions
  --trace-templates     Show template execution trace (top-level; use with --profile-includes for full call tree)
  --profile-includes    Show include timing statistics (top 20 slowest)
  --output FORMAT       Output format: summary, json, yaml (default: summary)
  --help                Show this help message

EXAMPLES:
  # Run all validation tests
  $(basename "$0")

  # Run specific test
  $(basename "$0") --test test-httproute-method-matching

  # Run test with debugging output
  $(basename "$0") --test test-httproute-method-matching --dump-rendered

  # Show all available tests
  $(basename "$0") --output yaml | yq '.tests[].name'

  # Verbose output for failed assertions
  $(basename "$0") --test test-httproute-method-matching --verbose

  # Run with 8 parallel workers
  $(basename "$0") --workers 8

  # Run sequentially (for debugging)
  $(basename "$0") --workers 1

  # Profile include execution times
  $(basename "$0") --profile-includes

NOTES:
  - Gateway API tests require --api-versions flag (automatically included)
  - This is the recommended way to test template changes
  - Do NOT test library files directly - always test the merged output

EOF
    exit 0
}

# Check for help flag first
for arg in "$@"; do
    if [[ "$arg" == "--help" || "$arg" == "-h" ]]; then
        usage
    fi
done

# Check if controller binary exists
if [[ ! -x "$CONTROLLER_BIN" ]]; then
    echo -e "${RED}Error: Controller binary not found at $CONTROLLER_BIN${NC}" >&2
    echo "Run 'make build' first to build the controller" >&2
    exit 1
fi

# Check if controller binary is outdated
if find cmd/controller pkg go.mod go.sum VERSION -newer "$CONTROLLER_BIN" 2>/dev/null | grep -q .; then
    warn "Controller binary may be outdated (source files modified since build)"
    warn "Run 'make build' to rebuild the controller binary"
    echo >&2  # Add blank line for readability
fi

# Check if helm is installed
if ! command -v helm &> /dev/null; then
    echo -e "${RED}Error: helm command not found${NC}" >&2
    echo "Install helm: https://helm.sh/docs/intro/install/" >&2
    exit 1
fi

# Check if yq is installed
if ! command -v yq &> /dev/null; then
    echo -e "${RED}Error: yq command not found${NC}" >&2
    echo "Install yq: https://github.com/mikefarah/yq" >&2
    exit 1
fi

# Check if chart directory exists
if [[ ! -d "$CHART_DIR" ]]; then
    echo -e "${RED}Error: Chart directory not found at $CHART_DIR${NC}" >&2
    exit 1
fi

# Create temporary file for merged config
TEMP_CONFIG=$(mktemp)
trap 'rm -f "$TEMP_CONFIG"' EXIT

# Render Helm chart with Gateway API support and extract HAProxyTemplateConfig.
# Use --namespace default for consistent behavior regardless of HELM_NAMESPACE
# env var (matches the _global fixtures which provide SSL certs in 'default').
#
# HAPROXY_VERSION env var is honored so CI's per-version matrix
# (.validate-helm-libraries-base in .gitlab-ci.yml) can render with the
# matching haproxyVersion value. When unset, the chart's values.yaml
# default applies.
HAPROXY_VERSION_ARG=""
if [[ -n "${HAPROXY_VERSION:-}" ]]; then
    HAPROXY_VERSION_ARG="--set haproxyVersion=${HAPROXY_VERSION}"
fi
echo -e "${YELLOW}Rendering Helm chart...${NC}" >&2
if ! helm template "$CHART_DIR" \
    --namespace default \
    $HAPROXY_VERSION_ARG \
    --set controller.templateLibraries.gateway.enabled=true \
    --set controller.templateLibraries.gateway.experimentalChannel=true \
    --set controller.templateLibraries.hapticAnnotations.enabled=true \
    --set controller.templateLibraries.haproxytech.enabled=true \
    --set controller.templateLibraries.haproxyIngress.enabled=true \
    --set controller.templateLibraries.nginxIngress.enabled=true \
    | yq 'select(.kind == "HAProxyTemplateConfig")' \
    > "$TEMP_CONFIG"; then
    echo -e "${RED}Error: Failed to render Helm chart${NC}" >&2
    exit 1
fi

# Guard the CHART-DEFAULT coraza directive ordering. This must live HERE
# (not in the chart's validationTests): validationTests ship inside the CR
# and run at config load under WHATEVER values the deployment uses — an
# assertion about values.yaml defaults would reject perfectly valid configs
# whose values override the directives (dev-values does). This script always
# renders chart defaults for the coraza directives, so the check is exact:
# SecRuleEngine On must come AFTER the includes, because
# @coraza.conf-recommended itself sets DetectionOnly and a reorder would
# silently ship a detection-only WAF.
if ! python3 -c '
import re, sys
s = open(sys.argv[1]).read()
if "@coraza.conf-recommended" in s:
    if not re.search(r"Include @owasp_crs/\*\.conf\n\s*SecRuleEngine On", s):
        sys.exit(1)
sys.exit(0)
' "$TEMP_CONFIG"; then
    echo -e "${RED}Error: chart-default coraza directives are mis-ordered:${NC}" >&2
    echo "  'SecRuleEngine On' must come AFTER 'Include @coraza.conf-recommended'" >&2
    echo "  (the include sets SecRuleEngine DetectionOnly; a later On is required" >&2
    echo "  or the default WAF silently becomes detection-only)." >&2
    exit 1
fi

# Verify the config file is not empty
if [[ ! -s "$TEMP_CONFIG" ]]; then
    echo -e "${RED}Error: Rendered HAProxyTemplateConfig is empty${NC}" >&2
    echo "This usually means the Helm template didn't output a HAProxyTemplateConfig resource" >&2
    exit 1
fi

# Run controller validate with all provided arguments.
#
# `--schema-dir=tests/schemas` is auto-wired so typed-access in chart
# templates (the `gateways`, `httproutes`, ... top-level globals) compiles
# offline. Operators passing their own `--schema-dir` explicitly via
# `$@` override this default — the loop below skips the auto-wiring if
# either `--schema-dir` or `--schema-dir=...` appears in the forwarded
# args. The HAPTIC_SCHEMA_DIR env var (read by the validate CLI's flag
# default) is also a valid override and similarly skips the auto-wiring.
#
# An array (not a plain string) is used so an empty value contributes
# zero arguments without relying on word-splitting behaviour — and so a
# non-empty value passes through unchanged even if PROJECT_ROOT
# contains spaces. shellcheck SC2086 / SC2128 stay clean.
SCHEMA_DIR_ARGS=()
SCHEMA_DIR="${PROJECT_ROOT}/tests/schemas"
if [[ -d "$SCHEMA_DIR" && -z "${HAPTIC_SCHEMA_DIR:-}" ]] \
    && ! printf '%s\n' "$@" | grep -qE -- '^--schema-dir(=|$)'; then
    SCHEMA_DIR_ARGS=("--schema-dir=$SCHEMA_DIR")
fi

echo -e "${YELLOW}Running validation tests...${NC}" >&2
FULL_RC=0
"$CONTROLLER_BIN" validate --file "$TEMP_CONFIG" "${SCHEMA_DIR_ARGS[@]}" "$@" || FULL_RC=$?

# Optional shared-rate-limit profile. The normal render above must keep the
# feature off so validationTests can assert that using
# haproxy-haptic.org/rate-limit-requests without the opt-in fails loudly.
# These tests need the opt-in because they assert the active map + SPOE
# dispatch path.
if [[ $FULL_RC -eq 0 && "$*" != *"--test"* ]]; then
    RATE_LIMIT_CONFIG=$(mktemp /tmp/haptic-rate-limit-config-XXXXXX.yaml)
    trap 'rm -f "$TEMP_CONFIG" "$RATE_LIMIT_CONFIG"' EXIT
    echo -e "${YELLOW}Rendering shared rate-limit profile...${NC}" >&2
    if ! helm template "$CHART_DIR" \
        --namespace default \
        $HAPROXY_VERSION_ARG \
        --set controller.templateLibraries.gateway.enabled=true \
        --set controller.templateLibraries.gateway.experimentalChannel=true \
        --set controller.templateLibraries.hapticAnnotations.enabled=true \
        --set controller.templateLibraries.haproxytech.enabled=true \
        --set controller.templateLibraries.haproxyIngress.enabled=true \
        --set controller.templateLibraries.nginxIngress.enabled=true \
        --set cache.varnish.enabled=true \
        --set rateLimit.shared.enabled=true \
        --set rateLimit.shared.managedStore.enabled=true \
        --set spoaHub.plugins.coraza.enabled=true \
        | yq 'select(.kind == "HAProxyTemplateConfig")' \
        > "$RATE_LIMIT_CONFIG"; then
        echo -e "${RED}Error: Failed to render shared rate-limit Helm profile${NC}" >&2
        exit 1
    fi
    for TEST in \
        test-haptic-rate-limit-shared-ip \
        test-haptic-rate-limit-shared-exact-consumer \
        test-haptic-cache-shared-rate-limit-loopback \
        test-haptic-cache-autoscaling \
        test-haptic-rate-limit-shared-invalid-requests \
        test-haptic-rate-limit-shared-invalid-period-zero; do
        echo -e "${YELLOW}Shared rate-limit profile: ${TEST}...${NC}" >&2
        "$CONTROLLER_BIN" validate --file "$RATE_LIMIT_CONFIG" "${SCHEMA_DIR_ARGS[@]}" --test "$TEST" "$@" || FULL_RC=$?
        if [[ $FULL_RC -ne 0 ]]; then
            break
        fi
    done
fi

# Optional API-gateway request-validation profile. The normal render keeps the
# feature off so validationTests can assert that request-schema annotations fail
# loudly without the opt-in. These tests assert the enabled map + SPOE dispatch
# path and render-time guardrails.
if [[ $FULL_RC -eq 0 && "$*" != *"--test"* ]]; then
    REQUEST_VALIDATION_CONFIG=$(mktemp /tmp/haptic-request-validation-config-XXXXXX.yaml)
    trap 'rm -f "$TEMP_CONFIG" "${RATE_LIMIT_CONFIG:-}" "$REQUEST_VALIDATION_CONFIG"' EXIT
    echo -e "${YELLOW}Rendering request-validation profile...${NC}" >&2
    if ! helm template "$CHART_DIR" \
        --namespace default \
        $HAPROXY_VERSION_ARG \
        --set controller.templateLibraries.gateway.enabled=true \
        --set controller.templateLibraries.gateway.experimentalChannel=true \
        --set controller.templateLibraries.hapticAnnotations.enabled=true \
        --set controller.templateLibraries.haproxytech.enabled=true \
        --set controller.templateLibraries.haproxyIngress.enabled=true \
        --set controller.templateLibraries.nginxIngress.enabled=true \
        --set controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.enabled=true \
        | yq 'select(.kind == "HAProxyTemplateConfig")' \
        > "$REQUEST_VALIDATION_CONFIG"; then
        echo -e "${RED}Error: Failed to render request-validation Helm profile${NC}" >&2
        exit 1
    fi
    for TEST in \
        test-haptic-request-validation-configmap \
        test-haptic-request-validation-secret \
        test-haptic-request-validation-rejects-two-sources \
        test-haptic-request-validation-missing-key \
        test-haptic-request-validation-max-body-bounded \
        test-haptic-request-validation-max-body-fits-buffer; do
        echo -e "${YELLOW}Request-validation profile: ${TEST}...${NC}" >&2
        "$CONTROLLER_BIN" validate --file "$REQUEST_VALIDATION_CONFIG" "${SCHEMA_DIR_ARGS[@]}" --test "$TEST" "$@" || FULL_RC=$?
        if [[ $FULL_RC -ne 0 ]]; then
            break
        fi
    done
    if [[ $FULL_RC -eq 0 ]]; then
        echo -e "${YELLOW}Request-validation Helm guard: requestBody.defaultMaxBytes hard cap...${NC}" >&2
        GUARD_ERR=$(mktemp /tmp/haptic-request-validation-guard-XXXXXX.log)
        if helm template "$CHART_DIR" \
            --namespace default \
            $HAPROXY_VERSION_ARG \
            --set controller.templateLibraries.hapticAnnotations.enabled=true \
            --set controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.enabled=true \
            --set controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.requestBody.defaultMaxBytes=1048577 \
            > /dev/null 2> "$GUARD_ERR"; then
            echo -e "${RED}Error: request-validation requestBody.defaultMaxBytes hard-cap guard did not fail${NC}" >&2
            rm -f "$GUARD_ERR"
            exit 1
        fi
        if ! grep -q "apiGateway.requestSchemaValidation.requestBody.defaultMaxBytes must be between 1 and 1048576 bytes" "$GUARD_ERR"; then
            echo -e "${RED}Error: request-validation requestBody.defaultMaxBytes hard-cap guard returned unexpected error:${NC}" >&2
            cat "$GUARD_ERR" >&2
            rm -f "$GUARD_ERR"
            exit 1
        fi
        rm -f "$GUARD_ERR"

        echo -e "${YELLOW}Request-body inspection Helm guard: haproxyBuffer.sizeBytes lower bound...${NC}" >&2
        GUARD_ERR=$(mktemp /tmp/haptic-request-validation-guard-XXXXXX.log)
        if helm template "$CHART_DIR" \
            --namespace default \
            $HAPROXY_VERSION_ARG \
            --set controller.templateLibraries.hapticAnnotations.enabled=true \
            --set controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.enabled=true \
            --set controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer.sizeBytes=8192 \
            > /dev/null 2> "$GUARD_ERR"; then
            echo -e "${RED}Error: request-body inspection haproxyBuffer.sizeBytes lower-bound guard did not fail${NC}" >&2
            rm -f "$GUARD_ERR"
            exit 1
        fi
        if ! grep -q "requestBodyInspection.haproxyBuffer.sizeBytes must be between 16384 and 2097152 bytes" "$GUARD_ERR"; then
            echo -e "${RED}Error: request-body inspection haproxyBuffer.sizeBytes lower-bound guard returned unexpected error:${NC}" >&2
            cat "$GUARD_ERR" >&2
            rm -f "$GUARD_ERR"
            exit 1
        fi
        rm -f "$GUARD_ERR"

        echo -e "${YELLOW}Request-body inspection Helm guard: reservedBytes fits sizeBytes...${NC}" >&2
        GUARD_ERR=$(mktemp /tmp/haptic-request-validation-guard-XXXXXX.log)
        if helm template "$CHART_DIR" \
            --namespace default \
            $HAPROXY_VERSION_ARG \
            --set controller.templateLibraries.hapticAnnotations.enabled=true \
            --set controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.enabled=true \
            --set controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer.sizeBytes=16384 \
            --set controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer.reservedBytes=16384 \
            > /dev/null 2> "$GUARD_ERR"; then
            echo -e "${RED}Error: request-validation reservedBytes guard did not fail${NC}" >&2
            rm -f "$GUARD_ERR"
            exit 1
        fi
        if ! grep -q "haproxyBuffer.reservedBytes must be a positive integer smaller than requestBodyInspection.haproxyBuffer.sizeBytes" "$GUARD_ERR"; then
            echo -e "${RED}Error: request-validation reservedBytes guard returned unexpected error:${NC}" >&2
            cat "$GUARD_ERR" >&2
            rm -f "$GUARD_ERR"
            exit 1
        fi
        rm -f "$GUARD_ERR"

        echo -e "${YELLOW}Request-validation Helm guard: requestBody.defaultMaxBytes leaves reserved buffer capacity...${NC}" >&2
        GUARD_ERR=$(mktemp /tmp/haptic-request-validation-guard-XXXXXX.log)
        if helm template "$CHART_DIR" \
            --namespace default \
            $HAPROXY_VERSION_ARG \
            --set controller.templateLibraries.hapticAnnotations.enabled=true \
            --set controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.enabled=true \
            --set controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.requestBody.defaultMaxBytes=9000 \
            --set controller.config.templatingSettings.extraContext.requestBodyInspection.haproxyBuffer.sizeBytes=16384 \
            > /dev/null 2> "$GUARD_ERR"; then
            echo -e "${RED}Error: request-validation buffer-headroom guard did not fail${NC}" >&2
            rm -f "$GUARD_ERR"
            exit 1
        fi
        if ! grep -q "requestBody.defaultMaxBytes must not exceed requestBodyInspection.haproxyBuffer.sizeBytes minus requestBodyInspection.haproxyBuffer.reservedBytes" "$GUARD_ERR"; then
            echo -e "${RED}Error: request-validation buffer-headroom guard returned unexpected error:${NC}" >&2
            cat "$GUARD_ERR" >&2
            rm -f "$GUARD_ERR"
            exit 1
        fi
        rm -f "$GUARD_ERR"

        echo -e "${YELLOW}Request-validation Helm guard: requestBody.waitTimeout duration...${NC}" >&2
        GUARD_ERR=$(mktemp /tmp/haptic-request-validation-guard-XXXXXX.log)
        if helm template "$CHART_DIR" \
            --namespace default \
            $HAPROXY_VERSION_ARG \
            --set controller.templateLibraries.hapticAnnotations.enabled=true \
            --set controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.enabled=true \
            --set-string 'controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.requestBody.waitTimeout=100ms http-request deny' \
            > /dev/null 2> "$GUARD_ERR"; then
            echo -e "${RED}Error: request-validation requestBody.waitTimeout guard did not fail${NC}" >&2
            rm -f "$GUARD_ERR"
            exit 1
        fi
        if ! grep -q "requestBody.waitTimeout must be a positive HAProxy duration" "$GUARD_ERR"; then
            echo -e "${RED}Error: request-validation requestBody.waitTimeout guard returned unexpected error:${NC}" >&2
            cat "$GUARD_ERR" >&2
            rm -f "$GUARD_ERR"
            exit 1
        fi
        rm -f "$GUARD_ERR"
    fi
fi

# Assert that one Helm values combination renders successfully. The
# values-surface counterpart of run_helm_failure_guard: exercises fields the
# Helm-level pre-validation must accept, so the haproxytemplateconfig.yaml
# allowlist can't silently drift behind the Scriggo-side registerPolicy
# allowlist (that drift shipped once: allowedMethods rejected at helm
# template time while every chart test passed).
run_helm_success_guard() {
    local label=$1
    shift 1
    local guard_err
    guard_err=$(mktemp /tmp/haptic-helm-guard-XXXXXX.log)
    echo -e "${YELLOW}${label}...${NC}" >&2
    if ! helm template "$CHART_DIR" --namespace default $HAPROXY_VERSION_ARG "$@" > /dev/null 2> "$guard_err"; then
        echo -e "${RED}Error: ${label} failed to render:${NC}" >&2
        cat "$guard_err" >&2
        rm -f "$guard_err"
        exit 1
    fi
    rm -f "$guard_err"
}

if [[ $FULL_RC -eq 0 && "$*" != *"--test"* ]]; then
    run_helm_success_guard \
        "WAF policy Helm guard: accept the full inline-policy field surface" \
        --set-json 'controller.config.templatingSettings.extraContext.waf.policies.inline={"full-surface":{"description":"every valid field","enforcement":"deny","allowedMethods":["GET","HEAD","POST","OPTIONS","PUT","PATCH","DELETE"],"paranoiaLevel":2,"anomalyThreshold":{"inbound":10,"outbound":8},"ruleExclusions":[{"rules":[930130],"onPathContains":".git/"},{"rules":[941320],"excludeTarget":"ARGS:wp_post"},{"tags":["attack-sqli"],"excludeTarget":"ARGS:q"}],"secLang":"SecRuleRemoveById 999999","requestBody":{"mode":"json","maxBytes":2048}}}'
    run_helm_failure_guard \
        "WAF policy Helm guard: reject unknown inline policy fields" \
        'waf.policies.inline.bad contains unknown field "allowedMethodz"' \
        --set-json 'controller.config.templatingSettings.extraContext.waf.policies.inline={"bad":{"allowedMethodz":["GET"]}}'
    run_helm_failure_guard \
        "WAF policy Helm guard: reject unknown ConfigMap reference fields" \
        'waf.policies.configMapRefs[0] contains unknown field "namespce". Valid fields: namespace, name, key.' \
        --set-string 'controller.config.templatingSettings.extraContext.waf.policies.configMapRefs[0].name=policies' \
        --set-string 'controller.config.templatingSettings.extraContext.waf.policies.configMapRefs[0].namespce=security'
    run_helm_failure_guard \
        "Reusable-WAF Helm guard: immutable policy requires a default" \
        "waf.policies.defaultPolicy is required when waf.ingressPermissions.allowPolicySelection=false" \
        --set controller.config.templatingSettings.extraContext.waf.ingressPermissions.allowPolicySelection=false \
        --set controller.config.templatingSettings.extraContext.waf.policies.inline.baseline.enforcement=deny
    run_helm_failure_guard \
        "Shared rate-limit Helm guard: reject ambiguous legacy store ownership" \
        'rateLimit.shared contains unknown field "store". Valid fields: enabled, managedStore, externalStore.' \
        --set rateLimit.shared.store.enabled=false
    run_helm_failure_guard \
        "Shared rate-limit Helm guard: reject Redis CLI spelling in public values" \
        "rateLimit.shared.managedStore.maxmemory and maxmemoryPolicy were renamed to maxMemory and maxMemoryPolicy" \
        --set-string rateLimit.shared.managedStore.maxmemory=64mb
    run_helm_failure_guard \
        "Managed Valkey Helm guard: reject invalid image pull policy" \
        "rateLimit.shared.managedStore.imagePullPolicy must be one of: Always, IfNotPresent, Never." \
        --set-string rateLimit.shared.managedStore.imagePullPolicy=Sometimes
    run_helm_failure_guard \
        "SPOA Hub Helm guard: reject zero HAProxy processing margin" \
        "spoaHub.haproxy.timeoutProcessingMarginMs must be between 1 and 60000 milliseconds." \
        --set-string spoaHub.haproxy.timeoutProcessingMarginMs=0
    run_helm_failure_guard \
        "Controller Helm guard: reject CRD terminology for config object name" \
        "controller.crdName was renamed to controller.configName" \
        --set-string controller.crdName=legacy-config
    run_helm_failure_guard \
        "Controller Helm guard: reject duplicate debug listener owner" \
        "controller.debugPort was removed; controller.ports.healthz is now the single source of truth" \
        --set-string controller.debugPort=8081
    run_helm_failure_guard \
        "Controller Helm guard: reject no-op metrics CR field" \
        "controller.config.controller.metricsPort was a no-op and has been removed" \
        --set-string controller.config.controller.metricsPort=9191
    run_helm_failure_guard \
        "Controller Helm guard: reject duplicate Dataplane API port owner" \
        "controller.config.dataplane.port was removed; haproxy.ports.dataplane is now the single source of truth" \
        --set-string controller.config.dataplane.port=6666
    run_helm_failure_guard \
        "Controller Helm guard: reject chart-only routing field beside CR fields" \
        "controller.config.routing moved to controller.config.templatingSettings.extraContext.routing" \
        --set-string controller.config.routing.regexMatchOrder=last
    run_helm_failure_guard \
        "Controller Helm guard: reject ambiguous root workload values" \
        "image moved to controller.image so every workload setting has an explicit component owner" \
        --set-string image.repository=example.invalid/haptic
    run_helm_failure_guard \
        "Controller Helm guard: reject misplaced status-patch policy" \
        "controller.statusPatches moved to controller.config.templatingSettings.extraContext.statusPatches" \
        --set controller.statusPatches.enabled=false
    run_helm_failure_guard \
        "Controller Helm guard: reject broad legacy debug toggle" \
        "extraContext.debug uses a removed flat value" \
        --set controller.config.templatingSettings.extraContext.debug=true
    run_helm_failure_guard \
        "Controller Helm guard: reject direct metrics environment override" \
        "controller.extraEnv must not override METRICS_PORT; use controller.ports.metrics" \
        --set-string controller.extraEnv[0].name=METRICS_PORT \
        --set-string controller.extraEnv[0].value=9191
    run_helm_failure_guard \
        "HAProxy Helm guard: reject duplicate Enterprise series owner" \
        "haproxy.enterprise.version was removed; haproxyVersion now selects" \
        --set-string haproxy.enterprise.version=3.2
    # Enterprise enablement must fail only for series without a tested image
    # pin (haproxyEnterprisePatchVersions maps them to ""). The effective
    # series is the CI matrix's HAPROXY_VERSION or the chart default.
    EFFECTIVE_HAPROXY_SERIES="${HAPROXY_VERSION:-$(yq '.haproxyVersion' "$CHART_DIR/values.yaml")}"
    ENTERPRISE_PIN="$(yq ".haproxyEnterprisePatchVersions.\"${EFFECTIVE_HAPROXY_SERIES}\"" "$CHART_DIR/values.yaml")"
    if [[ -z "$ENTERPRISE_PIN" || "$ENTERPRISE_PIN" == "null" ]]; then
        run_helm_failure_guard \
            "HAProxy Helm guard: reject unpinned Enterprise series" \
            "haproxy.enterprise.enabled=true has no tested image pin for haproxyVersion \"${EFFECTIVE_HAPROXY_SERIES}\"" \
            --set haproxy.enterprise.enabled=true
    fi
fi

# The reusable-WAF validationTests are self-contained: each pins the exact
# extraContext.waf values it needs (per-test extraContext deep-merges over the
# global context at test run time), so they run in the main validation pass
# above under the standard render — no per-profile helm renders are needed.

# ---------------------------------------------------------------------------
# Degraded Gateway API profiles (skipped when a single --test is requested or
# a custom schema dir is in play). For each committed old-release CRD bundle
# (tests/schemas-ga-*), render the STANDARD-channel chart, resolve against the
# bundle (the controller strips features whose CRDs the release doesn't serve
# AND tests whose requiresFields name schema fields the release's generation
# lacks), and require:
#   (a) ZERO failing tests — with field-level stripping in place, every test
#       that would fail on that schema generation must have been stripped
#       instead (a failure here is exactly the load-gate crash-loop of
#       issue #59); and
#   (b) the set of STRIPPED tests (the validate CLI's "⊘ <name> stripped:"
#       lines) to EXACTLY match the bundle's allowlist
#       (tests/schemas-ga-<rel>/expected-stripped.txt). A newly-stripped
#       test OR a stale allowlist entry both fail the run.
# ---------------------------------------------------------------------------
if [[ $FULL_RC -eq 0 && "$*" != *"--test"* && ${#SCHEMA_DIR_ARGS[@]} -gt 0 ]]; then
    STD_CONFIG=$(mktemp /tmp/haptic-std-config-XXXXXX.yaml)
    trap 'rm -f "$TEMP_CONFIG" "${RATE_LIMIT_CONFIG:-}" "${REQUEST_VALIDATION_CONFIG:-}" "$STD_CONFIG"' EXIT
    helm template "$CHART_DIR" --namespace default $HAPROXY_VERSION_ARG \
        --set controller.templateLibraries.gateway.enabled=true \
        --set controller.templateLibraries.hapticAnnotations.enabled=true \
        --set controller.templateLibraries.haproxytech.enabled=true \
        --set controller.templateLibraries.haproxyIngress.enabled=true \
        --set controller.templateLibraries.nginxIngress.enabled=true \
        | yq 'select(.kind == "HAProxyTemplateConfig")' > "$STD_CONFIG"
    for BUNDLE in "$PROJECT_ROOT"/tests/schemas-ga-*; do
        [[ -d "$BUNDLE" ]] || continue
        REL=$(basename "$BUNDLE")
        MERGED=$(mktemp -d /tmp/haptic-schemas-XXXXXX)
        cp "$PROJECT_ROOT"/tests/schemas/core_v1_*.yaml \
           "$PROJECT_ROOT"/tests/schemas/discovery_*.yaml \
           "$PROJECT_ROOT"/tests/schemas/haproxy-haptic.org_*.yaml \
           "$PROJECT_ROOT"/tests/schemas/networking_*.yaml "$MERGED/"
        # The "none" profile (no Gateway API at all) has no CRDs to add.
        find "$BUNDLE" -name '*.gateway.networking.k8s.io.yaml' -exec cp {} "$MERGED/" \;
        echo -e "${YELLOW}Degraded profile ${REL}...${NC}" >&2
        DEGRADED_RC=0
        DEGRADED_OUT=$("$CONTROLLER_BIN" validate --file "$STD_CONFIG" --schema-dir "$MERGED" 2>&1) || DEGRADED_RC=$?
        rm -rf "$MERGED"
        FAILED=$(printf '%s\n' "$DEGRADED_OUT" | grep '^✗' | awk '{print $2}' | sort || true)
        if [[ $DEGRADED_RC -ne 0 || -n "$FAILED" ]]; then
            echo -e "${RED}Degraded profile ${REL}: expected ZERO failing tests (rc=${DEGRADED_RC})${NC}" >&2
            echo -e "${RED}Failing tests must be stripped via requires/requiresFields, not fail the run:${NC}" >&2
            printf '%s\n' "$FAILED" >&2
            printf '%s\n' "$DEGRADED_OUT" | tail -30 >&2
            exit 1
        fi
        # Match only schema-strip lines ("⊘ <name> stripped: <reason>").
        # The test runner separately prints "⊘ <name> [skipped]" for
        # minHAProxyVersion skips, which vary with the HAProxy version of
        # the validating binary (this script runs per version in CI) and
        # must not perturb the schema-generation allowlist.
        ACTUAL=$(printf '%s\n' "$DEGRADED_OUT" | grep '^⊘ .* stripped: ' | awk '{print $2}' | sort || true)
        EXPECTED=$(grep -v '^#' "$BUNDLE/expected-stripped.txt" 2>/dev/null | grep -v '^$' | sort || true)
        if [[ "$ACTUAL" != "$EXPECTED" ]]; then
            echo -e "${RED}Degraded profile ${REL}: stripped-test set does not match ${BUNDLE}/expected-stripped.txt${NC}" >&2
            diff <(echo "$EXPECTED") <(echo "$ACTUAL") >&2 || true
            exit 1
        fi
        echo -e "${GREEN}Degraded profile ${REL}: zero failures, stripped set matches allowlist${NC}" >&2
    done
fi
exit $FULL_RC
