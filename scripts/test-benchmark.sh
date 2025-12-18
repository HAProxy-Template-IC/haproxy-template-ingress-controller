#!/usr/bin/env bash
set -euo pipefail

# test-benchmark.sh - Benchmark HAProxy template rendering performance
#
# This script measures template rendering time at scale by:
# 1. Extracting HAProxyTemplateConfig from Helm chart
# 2. Generating a single config with all benchmark tests
# 3. Running controller benchmark (compiles templates once, runs all tests)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
CHART_DIR="${PROJECT_ROOT}/charts/haproxy-template-ic"
CONTROLLER_BIN="${PROJECT_ROOT}/bin/controller"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default configuration
DEFAULT_STEPS="10,100,1000,10000"
STEPS=""
INGRESS_ONLY=false
HTTPROUTE_ONLY=false
ITERATIONS=2
PROFILE_INCLUDES=false
EXTRA_ARGS=""

# Temp directory (global for cleanup trap)
TEMP_DIR=""

# Print error message
error() {
    echo -e "${RED}Error: $1${NC}" >&2
}

# Print warning message
warn() {
    echo -e "${YELLOW}Warning: $1${NC}" >&2
}

# Print info message
info() {
    echo -e "${BLUE}$1${NC}" >&2
}

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Benchmark HAProxy template rendering performance at scale.

This script generates N services with corresponding Ingresses and HTTPRoutes,
then measures how long template rendering takes at each scale step.

OPTIONS:
  --steps STEPS       Comma-separated list of N values
                      (default: ${DEFAULT_STEPS})
  --ingress-only      Run only Ingress benchmarks
  --httproute-only    Run only HTTPRoute benchmarks
  --iterations N      Number of render iterations per test (default: ${ITERATIONS})
  --profile-includes  Show include timing statistics (top 20 slowest)
  --help              Show this help message

EXAMPLES:
  # Run default benchmarks (10, 100, 1000, 10000)
  $(basename "$0")

  # Run with custom steps
  $(basename "$0") --steps 10,50,100,500

  # Benchmark only Ingress resources
  $(basename "$0") --ingress-only

  # Benchmark only HTTPRoute resources
  $(basename "$0") --httproute-only

  # Profile include timing to find slow template snippets
  $(basename "$0") --profile-includes

OUTPUT:
  Displays detailed benchmark results including:
  - Compilation time
  - Per-iteration, per-file render times
  - Statistics (min, max, avg, median, stddev)
  - Renders per second

NOTES:
  - Large N values (10000+) may require significant memory
  - 100000 resources may take several minutes to render
  - Results show render-only time (no HAProxy binary validation)

EOF
    exit 0
}

# Parse command line arguments
parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --steps)
                STEPS="$2"
                shift 2
                ;;
            --ingress-only)
                INGRESS_ONLY=true
                shift
                ;;
            --httproute-only)
                HTTPROUTE_ONLY=true
                shift
                ;;
            --iterations)
                ITERATIONS="$2"
                shift 2
                ;;
            --profile-includes)
                PROFILE_INCLUDES=true
                shift
                ;;
            --extra-args)
                EXTRA_ARGS="$2"
                shift 2
                ;;
            --help|-h)
                usage
                ;;
            *)
                error "Unknown option: $1"
                usage
                ;;
        esac
    done

    # Set default steps if not provided
    if [[ -z "$STEPS" ]]; then
        STEPS="$DEFAULT_STEPS"
    fi

    # Validate conflicting options
    if [[ "$INGRESS_ONLY" == "true" && "$HTTPROUTE_ONLY" == "true" ]]; then
        error "Cannot use both --ingress-only and --httproute-only"
        exit 1
    fi
}

# Check for required tools
check_prerequisites() {
    local missing_tools=()

    if [[ ! -x "$CONTROLLER_BIN" ]]; then
        error "Controller binary not found at $CONTROLLER_BIN"
        echo "Run 'make build' first to build the controller" >&2
        exit 1
    fi

    if ! command -v helm &> /dev/null; then
        missing_tools+=("helm")
    fi

    if ! command -v yq &> /dev/null; then
        missing_tools+=("yq")
    fi

    if [[ ${#missing_tools[@]} -gt 0 ]]; then
        error "Missing required tools: ${missing_tools[*]}"
        echo "Please install them before running this script." >&2
        exit 1
    fi

    # Check if controller binary is outdated
    if find cmd/controller pkg go.mod go.sum VERSION -newer "$CONTROLLER_BIN" 2>/dev/null | grep -q .; then
        warn "Controller binary may be outdated (source files modified since build)"
        warn "Run 'make build' to rebuild the controller binary"
        echo >&2
    fi

    if [[ ! -d "$CHART_DIR" ]]; then
        error "Chart directory not found at $CHART_DIR"
        exit 1
    fi
}

# Extract HAProxyTemplateConfig from Helm chart
extract_base_config() {
    local output_file="$1"

    info "Extracting HAProxyTemplateConfig from Helm chart..."

    if ! helm template "$CHART_DIR" \
        --api-versions=gateway.networking.k8s.io/v1/GatewayClass \
        --set controller.templateLibraries.gateway.enabled=true \
        2>/dev/null | yq 'select(.kind == "HAProxyTemplateConfig")' \
        > "$output_file"; then
        error "Failed to render Helm chart"
        return 1
    fi

    if [[ ! -s "$output_file" ]]; then
        error "Rendered HAProxyTemplateConfig is empty"
        return 1
    fi
}

# Generate a single Service fixture (outputs indented YAML array item)
generate_service() {
    local i="$1"
    local port=$((8080 + (i % 100)))

    cat <<EOF
          - apiVersion: v1
            kind: Service
            metadata:
              name: bench-svc-${i}
              namespace: default
            spec:
              ports:
                - port: ${port}
                  targetPort: ${port}
                  protocol: TCP
              selector:
                app: bench-app-${i}
EOF
}

# Generate a single Ingress fixture with realistic annotations
generate_ingress() {
    local i="$1"
    local port=$((8080 + (i % 100)))

    # Vary load-balance algorithm
    local lb_modes=("roundrobin" "leastconn" "source")
    local lb_mode="${lb_modes[$((i % 3))]}"

    # Vary timeout
    local timeouts=("30s" "60s" "120s")
    local timeout="${timeouts[$((i % 3))]}"

    cat <<EOF
          - apiVersion: networking.k8s.io/v1
            kind: Ingress
            metadata:
              name: bench-ing-${i}
              namespace: default
              annotations:
                haproxy.org/load-balance: "${lb_mode}"
                haproxy.org/timeout-server: "${timeout}"
            spec:
              ingressClassName: haproxy
              rules:
                - host: bench-${i}.example.com
                  http:
                    paths:
                      - path: /
                        pathType: Prefix
                        backend:
                          service:
                            name: bench-svc-${i}
                            port:
                              number: ${port}
EOF
}

# Generate a single HTTPRoute fixture with complex rules
generate_httproute() {
    local i="$1"
    local port=$((8080 + (i % 100)))
    local version=$((i % 3))
    local weight=$((70 + (i % 30)))

    cat <<EOF
          - apiVersion: gateway.networking.k8s.io/v1
            kind: HTTPRoute
            metadata:
              name: bench-route-${i}
              namespace: default
            spec:
              parentRefs:
                - name: main-gateway
                  namespace: default
              hostnames:
                - "bench-${i}.example.com"
              rules:
                - matches:
                    - path:
                        type: PathPrefix
                        value: /api
                      headers:
                        - name: X-Version
                          value: "v${version}"
                      queryParams:
                        - name: debug
                          value: "true"
                  backendRefs:
                    - name: bench-svc-${i}
                      port: ${port}
                      weight: ${weight}
EOF
}

# Generate Gateway fixture (required for HTTPRoutes)
generate_gateway() {
    cat <<EOF
          - apiVersion: gateway.networking.k8s.io/v1
            kind: Gateway
            metadata:
              name: main-gateway
              namespace: default
            spec:
              gatewayClassName: haproxy
              listeners:
                - name: http
                  port: 80
                  protocol: HTTP
EOF
}

# Generate default SSL certificate secret fixture
# Uses same test certificate as ssl.yaml library
generate_ssl_secret() {
    cat <<'EOF'
          - apiVersion: v1
            kind: Secret
            type: kubernetes.io/tls
            metadata:
              name: default-ssl-cert
              namespace: haproxy-template-ic
            data:
              tls.crt: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURDekNDQWZPZ0F3SUJBZ0lVT2FGRWhyWlRXZ1JpbEorSFJCOWhJQnlzT2xNd0RRWUpLb1pJaHZjTkFRRUwKQlFBd0ZERVNNQkFHQTFVRUF3d0piRzlqWVd4b2IzTjBNQ0FYRFRJMU1URXhPVEU0TWpVMU1Wb1lEekl4TWpVeApNREkyTVRneU5UVXhXakFVTVJJd0VBWURWUVFEREFsc2IyTmhiR2h2YzNRd2dnRWlNQTBHQ1NxR1NJYjNEUUVCCkFRVUFBNElCRHdBd2dnRUtBb0lCQVFDWDJIQnA0V00rbFIvSXljLzRMR01qUHk0bUdFcUtVYm5xaFdRbU9nMXgKNHlnRkkzMkcyNWZYR1djZ0ZtVmg4YkZSZVFxdTB0Z1k2SUo5UE9nWmd5eHNhaUgzeldBMi8wbkdEejVvN2dYeQp5Z0VLaTdZY3M3bHNFNng0Y3lLK2tZaFdvZkNNaDJDck5LNWVCVzlRUnh3U3pieDNWREZqVS9HdVVaQnRBVWxICkRLd2ZCb0ZHNEhzaEZDUHl2Y3BuTlNLbUdwS0wwZ0UyczNBTHN5NWpqYjRpMnpyQng4Mng0Y3hQZUlCOEt6ckMKMDZvQXVwbzJDdUxaQTFMMkMrZVB5UlQ0QWxRUGNOL2l3WmdqMyt3eG8vWkFJaUxQK2NXbUY1dUdXeTZUREoybAo2K2FLYWFOajFCVVBWSkxTdW92WFhCMmc2akYxdWxIM1VWNVhPMlVMQ1ZzUkFnTUJBQUdqVXpCUk1CMEdBMVVkCkRnUVdCQlRHdi9VWG5nZ2tKaytpTkN0WnFwMStTSS9JaGpBZkJnTlZIU01FR0RBV2dCVEd2L1VYbmdna0prK2kKTkN0WnFwMStTSS9JaGpBUEJnTlZIUk1CQWY4RUJUQURBUUgvTUEwR0NTcUdTSWIzRFFFQkN3VUFBNElCQVFBSwozb3QyQTRyR05DWEpuZHB5OWlBa0dUUnk2SWxCcExKUlNJZ1JOWmlZQkU4SEtlWnUyR3JnUGI3V0ZUL1RyWkI2Cm5jMjhzVjdsYUNDNXlyZVUxeGVpL2ZjTllPSHlscWdBR2lFYmt2Um1za1hTd24wSlFaanpUNWl1a2hObVBkV0QKSWx0bWhCU3FsZlRsWFRYaytjMVlzeUJrVkN5TVNOdUZMd3pkODlLanJlU2xsZzE5clRVUmlLRzZGU0o2azBPSQprVE1lbWczRGZabldZSytNZnBxdjlVSDJSblBhMVJlbHJ4aTNLTWZoV0h2b2wxeUlVSGdnT0g3MkE4Wkd6eFZMClh4L09rSS8weXozUFhvM0xtUkRqczMzWUpXSzRTMmRwaFpyS3RMS2hqcEFIZzI1Nk8zUlN1bWd2d2NpYzZVRWMKaEpvWitneWZacUdBcnJ6M1dWTTkKLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo=
              tls.key: LS0tLS1CRUdJTiBQUklWQVRFIEtFWS0tLS0tCk1JSUV1d0lCQURBTkJna3Foa2lHOXcwQkFRRUZBQVNDQktVd2dnU2hBZ0VBQW9JQkFRQ1gySEJwNFdNK2xSL0kKeWMvNExHTWpQeTRtR0VxS1VibnFoV1FtT2cxeDR5Z0ZJMzJHMjVmWEdXY2dGbVZoOGJGUmVRcXUwdGdZNklKOQpQT2daZ3l4c2FpSDN6V0EyLzBuR0R6NW83Z1h5eWdFS2k3WWNzN2xzRTZ4NGN5SytrWWhXb2ZDTWgyQ3JOSzVlCkJXOVFSeHdTemJ4M1ZERmpVL0d1VVpCdEFVbEhES3dmQm9GRzRIc2hGQ1B5dmNwbk5TS21HcEtMMGdFMnMzQUwKc3k1ampiNGkyenJCeDgyeDRjeFBlSUI4S3pyQzA2b0F1cG8yQ3VMWkExTDJDK2VQeVJUNEFsUVBjTi9pd1pnagozK3d4by9aQUlpTFArY1dtRjV1R1d5NlRESjJsNithS2FhTmoxQlVQVkpMU3VvdlhYQjJnNmpGMXVsSDNVVjVYCk8yVUxDVnNSQWdNQkFBRUNnZjhKSHBhaHhVZVFtcVF1Q3ZEU2x0ZmRaZzMvZTdYK1dLb3h5NUVZT3FSVUVyQjAKbm8wTGJHVFNKbFJyT08wZDFNWXhmbk9GekdQdUd3aTdQTTB6dXcwUDljL1VjaUUxTEYvaDVVaDZSTkZXbzRzcwpkdmVaQWJKQksyMVFUcG5ubUJYNEhnRzBidXovVzBxZG12WDBmRkRUVUVmaFlzMFVpaFlad2d4S2Y2bEcrd1FzClZRclNiTzlMKzRJaHN2cnNSQlRGYU1nV0FMNGp0a3VpcTdSaVFkamRpejRkMmZJd1FtcVR5VEFvWlh5ZW9WRmEKM2JlRXh3aWtJME5pcXBNTWlaUjlXeTYzZkw2TlowZ0F5T0dKL09FaVU2Q3kyV2JCQzZrbVhnRlpOVW1Va1B5dAp3TXlqNTg5ZG1taDNaTGw1VEloNzVOc1dTNlVvTm1abzV3MmxLWUVDZ1lFQXh5N3dJeWxFbDlLaURvT1Q5V3gyCjFOcnp3bjQzLzVVbFAxakVFM0NiMXBiRDJCQjRUNXM4TmxCSEFOa0tFeFI1RUJnNTNYakw3aVlvTU1rWWUwL1YKRFVOdWFLRzlaaHhUSklualdPNURNUDE2NVFwNlBzdklObmlVZ1JLcW1Bc1hSNkNkN0NXeVIzcEZVOUZPcEJpWgo2aWt1eFVNYnZFWW8xVHNUWjB2Umw5RUNnWUVBd3lpNVNORWpEM2x6UHozcm5OVllRTEJBL2V1NS8wS1JXc2hQCmJVcit0YUszN1l4ZEJMV1JHOVpjOHd2ZU9pVWZTUHhWNmwyWnFRdVQ0VVVFdmYrbTlJWEZGZXZxUE1IanNZUVQKVVlRQVRxMkgwelF3Mm5kS2thdDlWSk81UVZKV252N0YxaWY5S3VPRFozbEhiUk96aEJjZjZXUWlBS0RNQWQ3cwpQNDNYbjBFQ2dZRUFxNmFacjlONmwxUWY4RjRYL2lLdzdaS2JDdnQzQ3J6ZlVvNE91Nm9Kd280K3pFNjFQL1ZKCm1JenFBNk1HK1pabEZpZXFobC81Ym94WGltTml3N0h5cXZGM2pwZ0QvcUZlVFZpL0lmNkN6UTlFLzJsZUhBdkYKeUp0MWJ4NUZBYTVkSzQ4UlNWYmJJcG9PY01NcUFHUnJEODdaelltZHQwekhGNnRIZDNkeGNtRUNnWUFzRWJNZApWVlNVZHZsbVM0WTc2UlUvcmsxT3lYODd1LzEwd1l6bUFpeFlPY0ZNM0FoWk91TGtwVmhoN2NrbDJpSWhhaEhBCmxaaFFTdlAreDRZVm5YaEcrVG9UQkMzbHdHYTVQRGpjakhGQlV3QTcyaW81K3Z3VXZ1UFRTSFJwNHJ6NnRFOWEKVjdkY2l2bXVVUDJuRE83Wm9oc3JxZGZmeW0rbThIN3FydzRFd1FLQmdEV1RqME82TXpyUUVhQVlrUVVucERVLwpYOFd5OGlPcStFYU5ZejNWWUtKSm5xVkJTeldLZDIrRFRQVmlvVHZqOVhVYkhGK1p3MW9ieXljcklDcVgrd0RCCkl4ZXBoMlBNNERKUHVlRnFJVzRqY3lQSkJuVGt4OVBuNUNKem1XSUJybE1vZjZON2tvWFIzMFBMbjlDRlRpNFoKa1dhTnhjR3BROVNldjVQeEoxL2QKLS0tLS1FTkQgUFJJVkFURSBLRVktLS0tLQo=
EOF
}

# Generate all fixtures for a given count and type (outputs complete fixtures section)
generate_fixtures() {
    local count="$1"
    local type="$2"  # "ingress" or "httproute"

    # SSL secret is required for default certificate
    echo "        secrets:"
    generate_ssl_secret

    echo "        services:"
    for ((i=0; i<count; i++)); do
        generate_service "$i"
    done

    if [[ "$type" == "ingress" ]]; then
        echo "        ingresses:"
        for ((i=0; i<count; i++)); do
            generate_ingress "$i"
        done
    else
        echo "        gateways:"
        generate_gateway
        echo "        httproutes:"
        for ((i=0; i<count; i++)); do
            generate_httproute "$i"
        done
    fi
}

# Add a single test to the config
add_test_to_config() {
    local config_file="$1"
    local count="$2"
    local type="$3"
    local test_name="benchmark-${type}-${count}"

    info "  Generating test: ${test_name}..."

    # Create fixtures file
    local fixtures_file
    fixtures_file=$(mktemp)
    generate_fixtures "$count" "$type" > "$fixtures_file"

    # Create the benchmark test YAML
    local test_yaml
    test_yaml=$(mktemp)
    cat > "$test_yaml" <<EOF
spec:
  validationTests:
    ${test_name}:
      description: "Benchmark test with ${count} ${type} resources"
      fixtures:
$(cat "$fixtures_file")
      assertions:
        - type: contains
          target: haproxy.cfg
          pattern: "global"
          description: "Config contains global section"
EOF

    # Merge the test into the config
    yq eval-all 'select(fileIndex == 0) * select(fileIndex == 1)' \
        "$config_file" "$test_yaml" > "${config_file}.merged"
    mv "${config_file}.merged" "$config_file"

    # Cleanup
    rm -f "$fixtures_file" "$test_yaml"
}

# Create config with all benchmark tests
create_all_tests_config() {
    local base_config="$1"
    local output_config="$2"
    local -a steps_array
    IFS=',' read -ra steps_array <<< "$STEPS"

    # Start with base config without existing tests
    yq eval 'del(.spec.validationTests)' "$base_config" > "$output_config"

    info "Generating benchmark tests..."

    # Generate all tests
    for step in "${steps_array[@]}"; do
        if [[ "$HTTPROUTE_ONLY" != "true" ]]; then
            add_test_to_config "$output_config" "$step" "ingress"
        fi
        if [[ "$INGRESS_ONLY" != "true" ]]; then
            add_test_to_config "$output_config" "$step" "httproute"
        fi
    done
}

# Main execution
main() {
    parse_args "$@"
    check_prerequisites

    echo
    echo "================================================================================"
    echo " HAProxy Template Benchmark"
    echo "================================================================================"
    echo

    # Parse steps into array
    local -a steps_array
    IFS=',' read -ra steps_array <<< "$STEPS"

    # Warn about large benchmarks
    for step in "${steps_array[@]}"; do
        if [[ "$step" -ge 100000 ]]; then
            warn "Running benchmark with ${step} resources - this may take several minutes and require significant memory"
            echo >&2
            break
        fi
    done

    # Create temp directory for configs (use global for trap cleanup)
    TEMP_DIR=$(mktemp -d)
    trap 'rm -rf "$TEMP_DIR"' EXIT

    # Extract base config
    local base_config="${TEMP_DIR}/base-config.yaml"
    extract_base_config "$base_config"

    # Create config with ALL tests
    local benchmark_config="${TEMP_DIR}/benchmark-all.yaml"
    create_all_tests_config "$base_config" "$benchmark_config"

    # Build command arguments
    local args=("--file" "$benchmark_config" "--iterations" "$ITERATIONS")
    if [[ "$PROFILE_INCLUDES" == "true" ]]; then
        args+=("--profile-includes")
    fi

    # Run single benchmark invocation (compiles once, runs all tests)
    info "Running benchmark..."
    echo
    # shellcheck disable=SC2086
    "$CONTROLLER_BIN" benchmark "${args[@]}" $EXTRA_ARGS
}

main "$@"
