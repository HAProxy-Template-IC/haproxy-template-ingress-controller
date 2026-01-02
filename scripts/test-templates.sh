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
CONTROLLER_BIN="${PROJECT_ROOT}/bin/haptic-controller"

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

# Render Helm chart with Gateway API support and extract HAProxyTemplateConfig
# Use --namespace default for consistent behavior regardless of HELM_NAMESPACE env var
# This matches the _global fixtures which provide SSL certs in 'default' namespace
echo -e "${YELLOW}Rendering Helm chart...${NC}" >&2
if ! helm template "$CHART_DIR" \
    --namespace default \
    --api-versions=gateway.networking.k8s.io/v1/GatewayClass \
    --set controller.templateLibraries.gateway.enabled=true \
    --set controller.templateLibraries.haproxyIngress.enabled=true \
    | yq 'select(.kind == "HAProxyTemplateConfig")' \
    > "$TEMP_CONFIG"; then
    echo -e "${RED}Error: Failed to render Helm chart${NC}" >&2
    exit 1
fi

# Verify the config file is not empty
if [[ ! -s "$TEMP_CONFIG" ]]; then
    echo -e "${RED}Error: Rendered HAProxyTemplateConfig is empty${NC}" >&2
    echo "This usually means the Helm template didn't output a HAProxyTemplateConfig resource" >&2
    exit 1
fi

# Run controller validate with all provided arguments
echo -e "${YELLOW}Running validation tests...${NC}" >&2
"$CONTROLLER_BIN" validate --file "$TEMP_CONFIG" "$@"
