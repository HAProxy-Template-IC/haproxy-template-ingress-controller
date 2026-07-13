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
    trap 'rm -f "$TEMP_CONFIG" "$STD_CONFIG"' EXIT
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
