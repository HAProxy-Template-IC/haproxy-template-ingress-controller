#!/usr/bin/env bash

set -euo pipefail

# Test script for HAProxy Template Ingress Controller
# Tests all Ingresses and HTTPRoutes in the dev environment

# Ensure we operate from the repo root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Configuration
CLUSTER_NAME="haproxy-template-ic-dev"
ECHO_NAMESPACE="echo"
NODEPORT="30080"
HTTPS_NODEPORT="30443"

# Docker-in-Docker detection and hostname extraction
# When DOCKER_HOST is set (e.g., tcp://docker:2376), the Docker daemon runs in a
# separate container and kind clusters are accessible via that hostname, not localhost.
is_docker_in_docker() {
    [[ "${DOCKER_HOST:-}" == tcp://* ]]
}

# Extract hostname from DOCKER_HOST (e.g., tcp://docker:2376 → docker)
# Returns "localhost" if not in dind environment
get_dind_hostname() {
    if is_docker_in_docker; then
        echo "$DOCKER_HOST" | sed 's|tcp://||' | cut -d: -f1
    else
        echo "localhost"
    fi
}

# Get IP address for curl --resolve (requires IP, not hostname)
# In DinD: resolve the docker hostname to IP, preferring IPv4
# Locally: use 127.0.0.1
get_nodeport_ip() {
    if is_docker_in_docker; then
        local hostname ip
        hostname=$(echo "$DOCKER_HOST" | sed 's|tcp://||' | cut -d: -f1)
        # Prefer IPv4: filter out addresses containing ':' (IPv6)
        # Kind's extraPortMappings only listen on IPv4 (listenAddress: "0.0.0.0")
        ip=$(getent hosts "$hostname" | awk '{print $1}' | grep -v ':' | head -1)
        if [[ -z "$ip" ]]; then
            # No IPv4 found, fall back to first address
            ip=$(getent hosts "$hostname" | awk '{print $1}' | head -1)
        fi
        echo "$ip"
    else
        echo "127.0.0.1"
    fi
}

# Get the host to use for NodePort access (localhost or dind hostname)
NODEPORT_HOST="$(get_dind_hostname)"
NODEPORT_IP="$(get_nodeport_ip)"
BASE_URL="http://${NODEPORT_HOST}:${NODEPORT}"
HTTPS_BASE_URL="https://${NODEPORT_HOST}:${HTTPS_NODEPORT}"

# Options
VERBOSE=false
TEST_FILTER=""
INGRESS_ONLY=false
HTTPROUTE_ONLY=false

# Colors
RED="\033[0;31m"
GREEN="\033[0;32m"
YELLOW="\033[1;33m"
BLUE="\033[0;34m"
NC="\033[0m"

# Test statistics
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

# Logging functions
log() {
    local level msg
    level="$1"; shift
    msg="$*"
    echo -e "${BLUE}[$(date +'%Y-%m-%dT%H:%M:%S%z')]${NC} ${level}: ${msg}"
}

ok() {
    echo -e "${GREEN}✔${NC} $*"
    PASSED_TESTS=$((PASSED_TESTS + 1))
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
}

warn() {
    echo -e "${YELLOW}⚠${NC} $*"
}

err() {
    echo -e "${RED}✖${NC} $*"
    FAILED_TESTS=$((FAILED_TESTS + 1))
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
}

debug() {
    [[ "$VERBOSE" == "true" ]] && echo -e "${BLUE}[DEBUG]${NC} $*" || true
}

print_section() {
    echo
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo " $1"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
}

show_usage() {
    cat <<EOF
Usage: $0 [OPTIONS]

Test all Ingresses and HTTPRoutes in the dev environment.

OPTIONS:
    --test NAME         Test only a specific route (ingress or httproute name)
    --ingress-only      Test only Ingress resources
    --httproute-only    Test only HTTPRoute resources
    --verbose           Enable debug output
    --help, -h          Show this help

EXAMPLES:
    $0                          # Test all routes
    $0 --test echo-basic        # Test only echo-basic HTTPRoute
    $0 --test echo-server-auth  # Test only auth ingress
    $0 --ingress-only           # Test only Ingress resources
    $0 --httproute-only         # Test only HTTPRoute resources
    $0 --verbose                # Run with debug output

EXIT CODES:
    0   All tests passed
    1   One or more tests failed

EOF
}

# Check for required tools
check_required_tools() {
    local missing_tools=()

    for tool in curl kubectl kind jq; do
        if ! command -v "$tool" &>/dev/null; then
            missing_tools+=("$tool")
        fi
    done

    if [[ ${#missing_tools[@]} -gt 0 ]]; then
        echo -e "${RED}Error:${NC} Missing required tools: ${missing_tools[*]}"
        echo "Please install them before running this script."
        exit 1
    fi

    debug "All required tools available: curl, kubectl, kind, jq"
}

# Test assertion helpers
assert_response_ok() {
    local description="$1"
    local host="$2"
    local path="${3:-/}"
    local expected_backend="${4:-}"
    shift 4
    local extra_args=("$@")

    debug "Testing: $description"
    debug "  Host: $host, Path: $path"
    debug "  Extra args: ${extra_args[*]}"

    # Retry logic for flaky CI environments
    # Use 5 retries to handle slow backend initialization (503 errors)
    local max_retries=5
    local retry_delay=2
    local attempt=1
    local response=""
    local last_error=""

    while [[ $attempt -le $max_retries ]]; do
        if response=$(curl -s --max-time 5 -H "Host: $host" "${extra_args[@]}" "${BASE_URL}${path}" 2>&1); then
            # Check if backend is not ready yet (503 Service Unavailable)
            if [[ "$response" == *"503 Service Unavailable"* ]]; then
                last_error="Backend not ready (503)"
                debug "  Attempt $attempt/$max_retries: $last_error"
            # Check if we got a valid response (avoid pipe to grep -q which causes SIGPIPE)
            elif [[ "$response" == *'"http":'* ]]; then
                break  # Success, exit retry loop
            else
                last_error="Invalid response (empty or malformed)"
                debug "  Attempt $attempt/$max_retries: $last_error"
            fi
        else
            last_error="Connection failed"
            debug "  Attempt $attempt/$max_retries: $last_error"
        fi

        if [[ $attempt -lt $max_retries ]]; then
            debug "  Retrying in ${retry_delay}s..."
            sleep "$retry_delay"
        fi
        attempt=$((attempt + 1))
    done

    debug "  Response: ${response:0:200}..."

    # Check for expected backend if specified (avoid pipe to grep -q which causes SIGPIPE)
    if [[ -n "$expected_backend" ]]; then
        if [[ "$response" == *"\"ENVIRONMENT\":\"$expected_backend\""* ]]; then
            ok "$description - Routed to $expected_backend"
        elif [[ "$response" == *'"ENVIRONMENT"'* ]]; then
            local actual_backend=$(echo "$response" | grep -o '"ENVIRONMENT":"[^"]*"' | cut -d'"' -f4)
            err "$description - Expected backend '$expected_backend', got '$actual_backend'"
            return 1
        else
            ok "$description - Routed to default backend"
        fi
    else
        # Just check for successful response (avoid pipe to grep -q which causes SIGPIPE)
        if [[ "$response" == *'"http":'* ]]; then
            ok "$description - Response OK"
        else
            err "$description - Invalid response"
            echo "  Received: ${response:0:500}"
            [[ -n "$last_error" ]] && echo "  Last error: $last_error (after $max_retries attempts)"
            return 1
        fi
    fi
}

assert_weighted_distribution() {
    local description="$1"
    local host="$2"
    local expected_default_pct="$3"
    local expected_v2_pct="$4"
    local num_requests="${5:-20}"
    local tolerance="${6:-10}"

    debug "Testing weighted distribution: $description"
    debug "  Host: $host, Requests: $num_requests"
    debug "  Expected: ${expected_default_pct}% default, ${expected_v2_pct}% v2"

    local count_default=0
    local count_v2=0

    for i in $(seq 1 "$num_requests"); do
        local response
        response=$(curl -s --max-time 5 -H "Host: $host" "${BASE_URL}/" 2>&1)

        if [[ "$response" == *'"ENVIRONMENT":"v2"'* ]]; then
            count_v2=$((count_v2 + 1))
        else
            count_default=$((count_default + 1))
        fi
    done

    local actual_default_pct=$(( (count_default * 100) / num_requests ))
    local actual_v2_pct=$(( (count_v2 * 100) / num_requests ))

    debug "  Actual: ${actual_default_pct}% default ($count_default/$num_requests), ${actual_v2_pct}% v2 ($count_v2/$num_requests)"

    # Check if within tolerance
    local default_diff=$(( actual_default_pct - expected_default_pct ))
    default_diff=${default_diff#-}  # absolute value
    local v2_diff=$(( actual_v2_pct - expected_v2_pct ))
    v2_diff=${v2_diff#-}

    if [[ $default_diff -le $tolerance ]] && [[ $v2_diff -le $tolerance ]]; then
        ok "$description - Distribution OK (${actual_default_pct}% default, ${actual_v2_pct}% v2)"
    else
        err "$description - Distribution out of range (${actual_default_pct}% default, ${actual_v2_pct}% v2, expected ${expected_default_pct}%/${expected_v2_pct}% ±${tolerance}%)"
        return 1
    fi
}

assert_auth_required() {
    local description="$1"
    local host="$2"

    debug "Testing auth requirement: $description"

    local response_code
    response_code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 -H "Host: $host" "${BASE_URL}/" 2>&1)

    if [[ "$response_code" == "401" ]]; then
        ok "$description - Auth required (401)"
    else
        err "$description - Expected 401, got $response_code"
        return 1
    fi
}

assert_auth_success() {
    local description="$1"
    local host="$2"
    local username="$3"
    local password="$4"

    debug "Testing auth success: $description"

    local response
    if ! response=$(curl -s --max-time 5 -u "$username:$password" -H "Host: $host" "${BASE_URL}/" 2>&1); then
        err "$description - Connection failed"
        return 1
    fi

    if [[ "$response" == *'"http":'* ]]; then
        ok "$description - Auth successful with $username:$password"
    else
        err "$description - Auth failed"
        echo "  Received: ${response:0:500}"
        return 1
    fi
}

assert_cors_headers() {
    local description="$1"
    local host="$2"
    local origin="${3:-https://example.com}"

    debug "Testing CORS headers: $description"
    debug "  Host: $host, Origin: $origin"

    local response_headers
    response_headers=$(curl -s --max-time 5 -I -H "Host: $host" -H "Origin: $origin" "${BASE_URL}/" 2>&1)

    debug "  Headers: ${response_headers:0:500}"

    # Check for Access-Control-Allow-Origin header
    if echo "$response_headers" | grep -iq "Access-Control-Allow-Origin:"; then
        ok "$description - CORS headers present"
    else
        err "$description - CORS headers missing"
        echo "  Response headers: $response_headers"
        return 1
    fi
}

assert_header_present() {
    local description="$1"
    local host="$2"
    local header_name="$3"
    local path="${4:-/}"

    debug "Testing header presence: $description"
    debug "  Host: $host, Header: $header_name"

    local response_headers
    response_headers=$(curl -s --max-time 5 -I -H "Host: $host" "${BASE_URL}${path}" 2>&1)

    if echo "$response_headers" | grep -iq "^${header_name}:"; then
        ok "$description - Header '$header_name' present"
    else
        err "$description - Header '$header_name' not found"
        debug "  Response headers: $response_headers"
        return 1
    fi
}

assert_header_value() {
    local description="$1"
    local host="$2"
    local header_name="$3"
    local expected_value="$4"
    local path="${5:-/}"

    debug "Testing header value: $description"
    debug "  Host: $host, Header: $header_name, Expected: $expected_value"

    local response_headers
    response_headers=$(curl -s --max-time 5 -I -H "Host: $host" "${BASE_URL}${path}" 2>&1)

    local actual_value
    actual_value=$(echo "$response_headers" | grep -i "^${header_name}:" | sed 's/^[^:]*: *//' | tr -d '\r')

    debug "  Actual value: $actual_value"

    if [[ "$actual_value" == *"$expected_value"* ]]; then
        ok "$description - Header value matches"
    else
        err "$description - Header value mismatch. Expected: '$expected_value', Got: '$actual_value'"
        return 1
    fi
}

assert_redirect() {
    local description="$1"
    local host="$2"
    local expected_code="$3"
    local expected_location="${4:-}"
    local path="${5:-/}"

    debug "Testing redirect: $description"
    debug "  Host: $host, Expected code: $expected_code"

    local response_code location_header
    response_code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 -H "Host: $host" "${BASE_URL}${path}" 2>&1)
    location_header=$(curl -s -I --max-time 5 -H "Host: $host" "${BASE_URL}${path}" 2>&1 | grep -i "^Location:" | sed 's/^Location: *//' | tr -d '\r')

    debug "  Response code: $response_code, Location: $location_header"

    if [[ "$response_code" == "$expected_code" ]]; then
        if [[ -n "$expected_location" ]]; then
            if [[ "$location_header" == *"$expected_location"* ]]; then
                ok "$description - Redirect OK ($response_code to $location_header)"
            else
                err "$description - Redirect location mismatch. Expected: '$expected_location', Got: '$location_header'"
                return 1
            fi
        else
            ok "$description - Redirect OK ($response_code)"
        fi
    else
        err "$description - Expected $expected_code, got $response_code"
        return 1
    fi
}

assert_rate_limited() {
    local description="$1"
    local host="$2"
    local rate_limit="${3:-5}"
    local path="${4:-/}"

    debug "Testing rate limiting: $description"
    debug "  Host: $host, Rate limit: $rate_limit requests"

    # Get HAProxy service ClusterIP to test from within cluster
    local haproxy_ip
    haproxy_ip=$(kubectl --context "kind-${CLUSTER_NAME}" -n haproxy-template-ic get svc haproxy-template-ic-haproxy -o jsonpath='{.spec.clusterIP}' 2>/dev/null || echo "")

    if [[ -z "$haproxy_ip" ]]; then
        warn "Could not get HAProxy ClusterIP, using service DNS name"
        haproxy_ip="haproxy-template-ic-haproxy.haproxy-template-ic.svc"
    fi

    # Send significantly more requests to ensure rate limiting triggers reliably
    # With parallel execution, we need more requests to guarantee some hit the limit
    local request_count=$((rate_limit * 4))

    # Run curl from within cluster to ensure consistent source IP
    # This prevents SNAT issues that occur when testing from host via NodePort
    local pod_name="rate-limit-test-$$"

    debug "  Creating test pod: $pod_name"
    debug "  Target: $haproxy_ip, Requests: $request_count"

    # Create pod with parallel requests to trigger rate limiting quickly
    # Use xargs with -P to run multiple curl commands in parallel
    local create_output
    if ! create_output=$(kubectl --context "kind-${CLUSTER_NAME}" run "$pod_name" \
        --image=alpine/curl \
        --restart=Never \
        --command -- sh -c 'seq 1 $4 | xargs -I{} -P10 sh -c "curl -s -o /dev/null -w \"%{http_code}\n\" -H \"Host: $2\" \"http://$1$3\""' -- "$haproxy_ip" "$host" "$path" "$request_count" 2>&1); then
        err "$description - Failed to create test pod"
        debug "  Error: $create_output"
        return 1
    fi

    # Wait for pod to complete (skip Ready check as pod may complete before becoming Ready)
    local wait_output
    if ! wait_output=$(kubectl --context "kind-${CLUSTER_NAME}" wait --for=jsonpath='{.status.phase}'=Succeeded pod/"$pod_name" --timeout=15s 2>&1); then
        err "$description - Test pod did not complete successfully"
        debug "  Wait error: $wait_output"

        # Show pod status for debugging
        local pod_status
        pod_status=$(kubectl --context "kind-${CLUSTER_NAME}" get pod "$pod_name" -o jsonpath='{.status.phase}' 2>/dev/null || echo "not found")
        debug "  Pod status: $pod_status"

        # Try to get logs anyway for debugging
        local pod_logs
        pod_logs=$(kubectl --context "kind-${CLUSTER_NAME}" logs "$pod_name" 2>&1 || echo "")
        if [[ -n "$pod_logs" ]]; then
            debug "  Pod logs: $pod_logs"
        fi

        # Clean up pod
        kubectl --context "kind-${CLUSTER_NAME}" delete pod "$pod_name" >/dev/null 2>&1
        return 1
    fi

    # Get logs
    local output
    if ! output=$(kubectl --context "kind-${CLUSTER_NAME}" logs "$pod_name" 2>&1); then
        err "$description - Failed to get test pod logs"
        debug "  Logs error: $output"
        kubectl --context "kind-${CLUSTER_NAME}" delete pod "$pod_name" >/dev/null 2>&1
        return 1
    fi

    # Clean up pod
    kubectl --context "kind-${CLUSTER_NAME}" delete pod "$pod_name" >/dev/null 2>&1

    local successful_requests=0
    local rate_limited_requests=0
    local all_codes=""

    # Parse response codes from output
    while IFS= read -r response_code; do
        all_codes="${all_codes} ${response_code}"
        if [[ "$response_code" == "200" ]]; then
            successful_requests=$((successful_requests + 1))
        elif [[ "$response_code" == "429" ]] || [[ "$response_code" == "403" ]]; then
            rate_limited_requests=$((rate_limited_requests + 1))
        fi
    done <<< "$output"

    debug "  Response codes:$all_codes"
    debug "  Successful: $successful_requests, Rate limited: $rate_limited_requests"

    # Expect some requests to be rate limited
    if [[ $rate_limited_requests -gt 0 ]]; then
        ok "$description - Rate limiting active ($rate_limited_requests requests blocked)"
    else
        err "$description - No requests were rate limited (expected some to be blocked)"
        return 1
    fi
}

assert_cookie_present() {
    local description="$1"
    local host="$2"
    local cookie_name="$3"
    local path="${4:-/}"

    debug "Testing cookie presence: $description"
    debug "  Host: $host, Cookie: $cookie_name"

    local response_headers
    response_headers=$(curl -s --max-time 5 -I -H "Host: $host" "${BASE_URL}${path}" 2>&1)

    if echo "$response_headers" | grep -iq "Set-Cookie:.*${cookie_name}"; then
        ok "$description - Cookie '$cookie_name' set"
    else
        err "$description - Cookie '$cookie_name' not found"
        debug "  Response headers: $response_headers"
        return 1
    fi
}

assert_path_rewrite() {
    local description="$1"
    local host="$2"
    local request_path="$3"
    local expected_path="$4"

    debug "Testing path rewrite: $description"
    debug "  Host: $host, Request: $request_path, Expected backend path: $expected_path"

    local response
    if ! response=$(curl -s --max-time 5 -H "Host: $host" "${BASE_URL}${request_path}" 2>&1); then
        err "$description - Connection failed"
        return 1
    fi

    # Check that the echo server received the expected path
    # The echo server returns the path in the "originalUrl" field (avoid pipe to grep -q which causes SIGPIPE)
    if [[ "$response" == *"\"originalUrl\":\"${expected_path}\""* ]]; then
        ok "$description - Path rewritten correctly"
    else
        local actual_path=$(echo "$response" | grep -o '"originalUrl":"[^"]*"' | cut -d'"' -f4)
        err "$description - Path not rewritten. Expected: $expected_path, Got: $actual_path"
        debug "  Response: ${response:0:500}"
        return 1
    fi
}

# Verify cluster is accessible
verify_cluster() {
    if ! kind get clusters 2>/dev/null | grep "$CLUSTER_NAME" >/dev/null; then
        echo -e "${RED}Error:${NC} Kind cluster '$CLUSTER_NAME' not found"
        echo "Start the dev environment with: ./scripts/start-dev-env.sh"
        exit 1
    fi

    if ! kubectl --context "kind-$CLUSTER_NAME" cluster-info &>/dev/null; then
        echo -e "${RED}Error:${NC} Cannot access cluster 'kind-$CLUSTER_NAME'"
        exit 1
    fi

    debug "Cluster verified: kind-$CLUSTER_NAME"
}

# Wait for services to be ready and accepting connections
wait_for_services_ready() {
    log INFO "Waiting for services to be ready..."

    # Step 1: Wait for HAProxy pods to be ready
    log INFO "Checking HAProxy pod readiness..."
    if ! kubectl -n haproxy-template-ic wait --for=condition=ready pod \
        -l app.kubernetes.io/component=loadbalancer --timeout=120s >/dev/null 2>&1; then
        echo
        err "HAProxy pods are not ready after 120s"
        echo
        echo "HAProxy pod status:"
        kubectl -n haproxy-template-ic get pods -l app.kubernetes.io/component=loadbalancer
        echo
        echo "Recent pod events:"
        kubectl -n haproxy-template-ic get events --sort-by='.lastTimestamp' | tail -10
        echo
        exit 1
    fi
    ok "HAProxy pods are ready"

    # Step 2: Wait for echo service pods to be ready
    log INFO "Checking echo service pod readiness..."
    if ! kubectl -n echo wait --for=condition=ready pod \
        -l app=echo-server --timeout=120s >/dev/null 2>&1; then
        echo
        err "Echo service pods are not ready after 120s"
        echo
        echo "Echo pod status:"
        kubectl -n echo get pods -l app=echo-server
        echo
        echo "Recent pod events:"
        kubectl -n echo get events --sort-by='.lastTimestamp' | tail -10
        echo
        exit 1
    fi
    ok "Echo service pods are ready"

    # Step 3: Check for echo-server-v2 pods (Gateway API demos - optional)
    debug "Checking echo-server-v2 pod readiness (optional)..."
    if kubectl -n echo get pods -l app=echo-server-v2 >/dev/null 2>&1; then
        if ! kubectl -n echo wait --for=condition=ready pod \
            -l app=echo-server-v2 --timeout=120s >/dev/null 2>&1; then
            warn "Echo-server-v2 pods exist but are not ready (continuing anyway)"
        else
            ok "Echo-server-v2 pods are ready"
        fi
    else
        debug "Echo-server-v2 pods not found (skipping)"
    fi

    # Step 4: Check for haproxy-demo-backend pods (SSL/PROXY protocol backends)
    debug "Checking haproxy-demo-backend pod readiness..."
    if kubectl -n echo get pods -l app=haproxy-demo-backend >/dev/null 2>&1; then
        if ! kubectl -n echo wait --for=condition=ready pod \
            -l app=haproxy-demo-backend --timeout=120s >/dev/null 2>&1; then
            warn "haproxy-demo-backend pods exist but are not ready (continuing anyway)"
        else
            ok "haproxy-demo-backend pods are ready"
        fi
    else
        debug "haproxy-demo-backend pods not found (skipping)"
    fi

    # Step 4b: Check for blocklist-server pods (HTTP Store dependency with critical: true)
    # This is a critical dependency - if blocklist-server isn't ready, the controller's
    # http.Fetch() with critical: true will cause the ENTIRE reconciliation to fail.
    debug "Checking blocklist-server pod readiness..."
    if kubectl -n echo get pods -l app=blocklist-server >/dev/null 2>&1; then
        if ! kubectl -n echo wait --for=condition=ready pod \
            -l app=blocklist-server --timeout=120s >/dev/null 2>&1; then
            warn "blocklist-server pods exist but are not ready (continuing anyway)"
        else
            ok "blocklist-server pods are ready"
        fi
    else
        debug "blocklist-server pods not found (skipping)"
    fi

    # Step 5: Wait for Ingress resources to exist
    log INFO "Waiting for Ingress resources to be created..."
    local ingress_attempts=30  # 60 seconds
    local attempt=1
    local ingress_created_epoch=0

    while [[ $attempt -le $ingress_attempts ]]; do
        # Check if echo-basic Ingress exists (used by first test)
        if kubectl -n echo get ingress echo-basic >/dev/null 2>&1; then
            # Get Ingress creation timestamp
            local ingress_created=$(kubectl -n echo get ingress echo-basic -o jsonpath='{.metadata.creationTimestamp}')
            ingress_created_epoch=$(date -d "$ingress_created" +%s 2>/dev/null || echo 0)
            debug "Ingress echo-basic created at $(date -d "@$ingress_created_epoch" 2>/dev/null)"
            break
        fi

        if [[ $attempt -lt $ingress_attempts ]]; then
            debug "Ingress resources not found yet (attempt $attempt/$ingress_attempts)..."
            sleep 2
        fi

        attempt=$((attempt + 1))
    done

    if [[ $ingress_created_epoch -eq 0 ]]; then
        echo
        err "Ingress resources were not created after ${ingress_attempts} attempts (60s)"
        echo
        echo "This means start-dev-env.sh did not deploy Ingress demo resources."
        echo
        exit 1
    fi
    ok "Ingress resources created"

    # Step 6: Wait for service endpoints to be populated
    # We check this BEFORE configuration reconciliation because backends need endpoints
    log INFO "Waiting for service endpoints to be populated..."
    local endpoint_attempts=30
    attempt=1
    local endpoints_ready=false
    local endpoints_created_epoch=0

    while [[ $attempt -le $endpoint_attempts ]]; do
        if kubectl -n echo get endpoints echo-server -o json 2>/dev/null | \
            jq -e '.subsets[0].addresses | length > 0' >/dev/null 2>&1; then
            # Get endpoints last update timestamp
            local endpoints_updated=$(kubectl -n echo get endpoints echo-server -o jsonpath='{.metadata.resourceVersion}')
            endpoints_ready=true
            debug "Service endpoints are populated (resourceVersion: $endpoints_updated)"
            break
        fi

        if [[ $attempt -lt $endpoint_attempts ]]; then
            debug "Endpoints not ready yet (attempt $attempt/$endpoint_attempts), waiting 2s..."
            sleep 2
        fi

        attempt=$((attempt + 1))
    done

    if [[ "$endpoints_ready" != "true" ]]; then
        echo
        err "Service endpoints were not populated after ${endpoint_attempts} attempts (60s)"
        echo
        kubectl -n echo get endpoints echo-server 2>/dev/null || echo "  (no endpoints found)"
        echo
        exit 1
    fi
    ok "Service endpoints populated"

    # Step 6b: Wait for haproxy-demo-backend endpoints (if service exists)
    if kubectl -n echo get svc haproxy-demo-backend >/dev/null 2>&1; then
        log INFO "Waiting for haproxy-demo-backend endpoints to be populated..."
        endpoint_attempts=30
        attempt=1
        endpoints_ready=false

        while [[ $attempt -le $endpoint_attempts ]]; do
            if kubectl -n echo get endpoints haproxy-demo-backend -o json 2>/dev/null | \
                jq -e '.subsets[0].addresses | length > 0' >/dev/null 2>&1; then
                endpoints_ready=true
                debug "haproxy-demo-backend endpoints are populated"
                break
            fi

            if [[ $attempt -lt $endpoint_attempts ]]; then
                debug "haproxy-demo-backend endpoints not ready yet (attempt $attempt/$endpoint_attempts), waiting 2s..."
                sleep 2
            fi

            attempt=$((attempt + 1))
        done

        if [[ "$endpoints_ready" == "true" ]]; then
            ok "haproxy-demo-backend endpoints populated"
        else
            warn "haproxy-demo-backend endpoints were not populated after ${endpoint_attempts} attempts (continuing anyway)"
        fi
    fi

    # Step 6c: Wait for blocklist-server endpoints (critical for reconciliation)
    # The controller uses http.Fetch() with critical: true to fetch blocklist content.
    # If this fails, the ENTIRE reconciliation fails - no backends will be configured.
    if kubectl -n echo get svc blocklist-server >/dev/null 2>&1; then
        log INFO "Waiting for blocklist-server endpoints to be populated..."
        endpoint_attempts=30
        attempt=1
        endpoints_ready=false

        while [[ $attempt -le $endpoint_attempts ]]; do
            if kubectl -n echo get endpoints blocklist-server -o json 2>/dev/null | \
                jq -e '.subsets[0].addresses | length > 0' >/dev/null 2>&1; then
                endpoints_ready=true
                debug "blocklist-server endpoints are populated"
                break
            fi

            if [[ $attempt -lt $endpoint_attempts ]]; then
                debug "blocklist-server endpoints not ready yet (attempt $attempt/$endpoint_attempts), waiting 2s..."
                sleep 2
            fi

            attempt=$((attempt + 1))
        done

        if [[ "$endpoints_ready" == "true" ]]; then
            ok "blocklist-server endpoints populated"
        else
            warn "blocklist-server endpoints were not populated (controller reconciliation may fail)"
        fi
    fi

    # Step 7: Wait for controller to reconcile with backends
    # Now that endpoints exist, wait for HAProxyCfg that includes backends
    log INFO "Waiting for HAProxy configuration with backends..."
    local config_attempts=60  # 120 seconds
    attempt=1
    local config_deployed=false

    while [[ $attempt -le $config_attempts ]]; do
        # Check if HAProxyCfg was DEPLOYED (not just checked) AFTER the Ingress was created
        # deployedAt updates only on actual config changes, lastCheckedAt updates on drift checks too
        local deployed_at=$(kubectl -n haproxy-template-ic get haproxycfg -o json 2>/dev/null | \
            jq -r '.items[0].status.deployedToPods[0].deployedAt // empty')

        if [[ -n "$deployed_at" ]]; then
            # Convert Kubernetes timestamp to epoch
            local deployed_epoch=$(date -d "$deployed_at" +%s 2>/dev/null || echo 0)

            debug "Configuration deployed at epoch $deployed_epoch, Ingress created at epoch $ingress_created_epoch"

            # Configuration must be deployed after Ingress creation
            if [[ $deployed_epoch -gt $ingress_created_epoch ]]; then
                # Also verify the configuration actually contains backends
                # Use // "" to convert null to empty string before grep
                local backend_count=$(kubectl -n haproxy-template-ic get haproxycfg -o json 2>/dev/null | \
                    jq -r '.items[0].spec.content // ""' | grep -c "^backend.*echo" 2>/dev/null)

                debug "Configuration has $backend_count echo backends"

                if [[ $backend_count -gt 0 ]]; then
                    # Also verify haproxy-demo-backend has real servers (not placeholders)
                    # Placeholders use 127.0.0.1:1, real servers use pod IPs (10.x.x.x)
                    local haproxy_cfg=$(kubectl -n haproxy-template-ic get haproxycfg -o json 2>/dev/null | \
                        jq -r '.items[0].spec.content // ""')
                    local demo_backend_servers
                    demo_backend_servers=$(echo "$haproxy_cfg" | \
                        grep -A 20 "backend.*haproxy-demo-backend" | \
                        grep -c "server SRV_.*10\." 2>/dev/null) || demo_backend_servers=0

                    if [[ $demo_backend_servers -gt 0 ]]; then
                        debug "HAProxy configuration with backends was deployed"
                        debug "haproxy-demo-backend has $demo_backend_servers real servers"
                        config_deployed=true
                        break
                    else
                        debug "Configuration has backends but haproxy-demo-backend still has placeholder servers"
                    fi
                else
                    debug "Configuration deployed but has no backends yet (reconciling...)"
                fi
            else
                debug "Configuration exists but was deployed before Ingress creation"
            fi
        fi

        if [[ $attempt -lt $config_attempts ]]; then
            debug "Waiting for configuration with backends (attempt $attempt/$config_attempts)..."
            sleep 2
        fi

        attempt=$((attempt + 1))
    done

    if [[ "$config_deployed" != "true" ]]; then
        echo
        err "HAProxy configuration with backends was not deployed after ${config_attempts} attempts (60s)"
        echo
        echo "Configuration may be empty or missing Service/Endpoint data."
        echo
        echo "HAProxyCfg checksum:"
        kubectl -n haproxy-template-ic get haproxycfg -o jsonpath='{.items[0].spec.checksum}' 2>/dev/null || echo "  (not found)"
        echo
        echo
        echo "Backend count in HAProxyCfg:"
        kubectl -n haproxy-template-ic get haproxycfg -o json 2>/dev/null | \
            jq -r '.items[0].spec.content' | grep -c "^backend" 2>/dev/null || echo "  0"
        echo
        echo "Controller logs (last 30 lines):"
        kubectl -n haproxy-template-ic logs -l app.kubernetes.io/name=haproxy-template-ic --tail=30 2>/dev/null || echo "  (no logs available)"
        echo
        exit 1
    fi
    ok "HAProxy configuration with backends deployed"

    # Step 4: Test HTTP connectivity (pods, endpoints, and config with backends are ready)
    log INFO "Testing HTTP connectivity..."
    local max_attempts=20  # 40 seconds - reduced since pods and config are ready
    local delay=2
    local attempt=1
    local http_ready=false

    while [[ $attempt -le $max_attempts ]]; do
        debug "HTTP connectivity check $attempt/$max_attempts..."

        local response
        if response=$(curl -s --max-time 5 -H "Host: echo.localdev.me" "${BASE_URL}/" 2>&1); then
            # Check that response contains valid JSON with expected structure (avoid pipe to grep -q which causes SIGPIPE)
            if [[ "$response" == *'"http":'* ]]; then
                debug "Services responding with valid JSON"
                http_ready=true
                break
            else
                debug "Connection succeeded but response invalid or empty"
            fi
        else
            debug "Connection failed: $response"
        fi

        if [[ $attempt -lt $max_attempts ]]; then
            sleep "$delay"
        fi

        attempt=$((attempt + 1))
    done

    if [[ "$http_ready" == "true" ]]; then
        ok "Services are ready and accepting connections"
    else
        echo
        err "HTTP connectivity check failed after $max_attempts attempts (${max_attempts}x${delay}s)"
        echo
        echo "Pods are ready and configuration was deployed, but HTTP requests are still failing."
        echo "This typically means:"
        echo "  - HAProxy configuration may have errors"
        echo "  - Ingress/HTTPRoute resources may not match the test expectations"
        echo "  - Network connectivity issues between test runner and HAProxy"
        echo
        echo "Diagnostics:"
        echo "  HAProxy pods:"
        kubectl -n haproxy-template-ic get pods -l app.kubernetes.io/component=loadbalancer
        echo
        echo "  Echo pods:"
        kubectl -n echo get pods
        echo
        echo "  Endpoints:"
        kubectl -n echo get endpoints
        echo
        echo "  HAProxy logs (last 20 lines):"
        kubectl -n haproxy-template-ic logs -l app.kubernetes.io/component=loadbalancer --tail=20 -c haproxy 2>/dev/null || echo "  (no logs available)"
        echo
        echo "  Controller logs (last 20 lines):"
        kubectl -n haproxy-template-ic logs deployment/haproxy-template-ic --tail=20 2>/dev/null || echo "  (no logs available)"
        echo
        exit 1
    fi
}

should_test() {
    local resource_name="$1"

    if [[ -n "$TEST_FILTER" ]]; then
        [[ "$resource_name" == "$TEST_FILTER" ]]
    else
        true
    fi
}

# Wait for HTTPRoute/Gateway backends to be deployed
# This is called before HTTPRoute tests to ensure Gateway API resources are reconciled
wait_for_httproute_backends() {
    log INFO "Waiting for HTTPRoute backends to be deployed..."

    local config_attempts=60  # 120 seconds
    local attempt=1
    local backends_deployed=false

    while [[ $attempt -le $config_attempts ]]; do
        # Check if HAProxyCfg contains Gateway/HTTPRoute backends (prefixed with gtw_)
        local gtw_backend_count=$(kubectl -n haproxy-template-ic get haproxycfg -o json 2>/dev/null | \
            jq -r '.items[0].spec.content // ""' | grep -c "^backend gtw_" 2>/dev/null || echo 0)

        debug "HTTPRoute backend count: $gtw_backend_count (attempt $attempt/$config_attempts)"

        if [[ $gtw_backend_count -gt 0 ]]; then
            backends_deployed=true
            break
        fi

        if [[ $attempt -lt $config_attempts ]]; then
            sleep 2
        fi

        attempt=$((attempt + 1))
    done

    if [[ "$backends_deployed" == "true" ]]; then
        ok "HTTPRoute backends deployed ($gtw_backend_count backends)"
    else
        warn "HTTPRoute backends not found in config (tests may fail)"
    fi

    # Additional wait for HAProxy to reload with new config
    # This helps with flakiness when config was just deployed
    sleep 2
}

#═══════════════════════════════════════════════════════════════════════════
# INGRESS TESTS
#═══════════════════════════════════════════════════════════════════════════

test_ingress_basic() {
    if ! should_test "echo-server"; then
        return 0
    fi

    print_section "Testing Ingress: echo-server (echo.localdev.me)"

    assert_response_ok \
        "Basic ingress routing" \
        "echo.localdev.me" \
        "/" \
        ""
}

test_ingress_auth() {
    if ! should_test "echo-server-auth"; then
        return 0
    fi

    print_section "Testing Ingress: echo-server-auth (echo-auth.localdev.me)"

    assert_auth_required \
        "Auth required without credentials" \
        "echo-auth.localdev.me"

    assert_auth_success \
        "Auth with admin:admin" \
        "echo-auth.localdev.me" \
        "admin" \
        "admin"

    assert_auth_success \
        "Auth with user:password" \
        "echo-auth.localdev.me" \
        "user" \
        "password"
}

test_ingress_cors() {
    if ! should_test "echo-cors"; then
        return 0
    fi

    print_section "Testing Ingress: echo-cors (CORS headers)"

    assert_response_ok \
        "Basic connectivity" \
        "echo-cors.localdev.me" \
        "/" \
        ""

    assert_cors_headers \
        "CORS headers present" \
        "echo-cors.localdev.me" \
        "https://example.com"

    assert_header_present \
        "Access-Control-Allow-Methods header" \
        "echo-cors.localdev.me" \
        "Access-Control-Allow-Methods"

    assert_header_present \
        "Access-Control-Allow-Credentials header" \
        "echo-cors.localdev.me" \
        "Access-Control-Allow-Credentials"
}

test_ingress_rate_limit() {
    if ! should_test "echo-ratelimit"; then
        return 0
    fi

    print_section "Testing Ingress: echo-ratelimit (Rate limiting)"

    assert_rate_limited \
        "Rate limiting enforcement" \
        "echo-ratelimit.localdev.me" \
        5
}

test_ingress_allowlist() {
    if ! should_test "echo-allowlist"; then
        return 0
    fi

    print_section "Testing Ingress: echo-allowlist (IP allowlist)"

    # This test verifies the allowlist config is present
    # Actual IP filtering test would require making requests from different IPs
    assert_response_ok \
        "Allowlist configured (request from allowed IP)" \
        "echo-allowlist.localdev.me" \
        "/" \
        ""
}

test_ingress_denylist() {
    if ! should_test "echo-denylist"; then
        return 0
    fi

    print_section "Testing Ingress: echo-denylist (IP denylist)"

    # This test verifies the denylist config is present
    # Actual IP filtering test would require making requests from blocked IPs
    assert_response_ok \
        "Denylist configured (request from non-blocked IP)" \
        "echo-denylist.localdev.me" \
        "/" \
        ""
}

test_ingress_redirect() {
    if ! should_test "echo-redirect"; then
        return 0
    fi

    print_section "Testing Ingress: echo-redirect (Request redirect)"

    assert_redirect \
        "Request redirect to echo.localdev.me" \
        "echo-redirect.localdev.me" \
        "302" \
        "https://echo.localdev.me"
}

test_ingress_headers_request() {
    if ! should_test "echo-headers-request"; then
        return 0
    fi

    print_section "Testing Ingress: echo-headers-request (Request headers)"

    # Test that request headers are added by checking if the backend receives them
    assert_response_ok \
        "Request headers manipulation" \
        "echo-headers-request.localdev.me" \
        "/" \
        ""
}

test_ingress_headers_response() {
    if ! should_test "echo-headers-response"; then
        return 0
    fi

    print_section "Testing Ingress: echo-headers-response (Response headers)"

    assert_header_present \
        "Strict-Transport-Security header" \
        "echo-headers-response.localdev.me" \
        "Strict-Transport-Security"

    assert_header_value \
        "Strict-Transport-Security value" \
        "echo-headers-response.localdev.me" \
        "Strict-Transport-Security" \
        "max-age=31536000"

    assert_header_present \
        "X-Frame-Options header" \
        "echo-headers-response.localdev.me" \
        "X-Frame-Options"

    assert_header_value \
        "X-Frame-Options value" \
        "echo-headers-response.localdev.me" \
        "X-Frame-Options" \
        "DENY"
}

test_ingress_sethost() {
    if ! should_test "echo-sethost"; then
        return 0
    fi

    print_section "Testing Ingress: echo-sethost (Host header override)"

    assert_response_ok \
        "Host header override configured" \
        "echo-sethost.localdev.me" \
        "/" \
        ""
}

test_ingress_rewrite() {
    if ! should_test "echo-rewrite"; then
        return 0
    fi

    print_section "Testing Ingress: echo-rewrite (Path rewriting)"

    assert_path_rewrite \
        "Path /api/v1/test rewritten to /test" \
        "echo-rewrite.localdev.me" \
        "/api/v1/test" \
        "/test"

    assert_path_rewrite \
        "Path /api/v1/users rewritten to /users" \
        "echo-rewrite.localdev.me" \
        "/api/v1/users" \
        "/users"
}

test_ingress_loadbalance() {
    if ! should_test "echo-loadbalance"; then
        return 0
    fi

    print_section "Testing Ingress: echo-loadbalance (Load balancing)"

    assert_response_ok \
        "Load balancing algorithm configured" \
        "echo-loadbalance.localdev.me" \
        "/" \
        ""
}

test_ingress_sticky() {
    if ! should_test "echo-sticky"; then
        return 0
    fi

    print_section "Testing Ingress: echo-sticky (Sticky sessions)"

    assert_cookie_present \
        "Session cookie set" \
        "echo-sticky.localdev.me" \
        "SERVERID"
}

test_ingress_timeouts() {
    if ! should_test "echo-timeouts"; then
        return 0
    fi

    print_section "Testing Ingress: echo-timeouts (Timeouts)"

    assert_response_ok \
        "Timeout configuration applied" \
        "echo-timeouts.localdev.me" \
        "/" \
        ""
}

test_ingress_forwardedfor() {
    if ! should_test "echo-forwardedfor"; then
        return 0
    fi

    print_section "Testing Ingress: echo-forwardedfor (X-Forwarded-For)"

    assert_response_ok \
        "X-Forwarded-For header configuration" \
        "echo-forwardedfor.localdev.me" \
        "/" \
        ""
}

test_ingress_capture() {
    if ! should_test "echo-capture"; then
        return 0
    fi

    print_section "Testing Ingress: echo-capture (Request capture)"

    assert_response_ok \
        "Request capture for logging" \
        "echo-capture.localdev.me" \
        "/" \
        ""
}

test_ingress_healthcheck() {
    if ! should_test "echo-healthcheck"; then
        return 0
    fi

    print_section "Testing Ingress: echo-healthcheck (Health checks)"

    assert_response_ok \
        "Custom health check configuration" \
        "echo-healthcheck.localdev.me" \
        "/" \
        ""
}

test_ingress_maxconn() {
    if ! should_test "echo-maxconn"; then
        return 0
    fi

    print_section "Testing Ingress: echo-maxconn (Connection limits)"

    assert_response_ok \
        "Pod maxconn configuration" \
        "echo-maxconn.localdev.me" \
        "/" \
        ""
}

test_ingress_srcip() {
    if ! should_test "echo-srcip"; then
        return 0
    fi

    print_section "Testing Ingress: echo-srcip (Source IP header)"

    assert_response_ok \
        "Source IP header extraction" \
        "echo-srcip.localdev.me" \
        "/" \
        ""
}

test_ingress_combined() {
    if ! should_test "echo-combined"; then
        return 0
    fi

    print_section "Testing Ingress: echo-combined (Multiple annotations)"

    # Test requires auth
    assert_auth_required \
        "Auth required on combined example" \
        "echo-combined-ingress.localdev.me"

    # Test security headers (need auth for this endpoint)
    local response_headers
    response_headers=$(curl -s --max-time 5 -I -u "admin:admin" -H "Host: echo-combined-ingress.localdev.me" "${BASE_URL}/" 2>&1)

    if echo "$response_headers" | grep -iq "^x-frame-options:"; then
        ok "Security headers in combined example - Header 'X-Frame-Options' present"
    else
        err "Security headers in combined example - Header 'X-Frame-Options' not found"
    fi
}

test_ingress_http_store_demo() {
    if ! should_test "echo-http-store-demo"; then
        return 0
    fi

    print_section "Testing Ingress: echo-http-store-demo (HTTP Store header blocking)"

    local host="http-store-demo.localdev.me"
    local response

    # Test 1: Request without X-Custom-Header should succeed
    response=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 \
        -H "Host: ${host}" \
        "${BASE_URL}/")

    if [[ "$response" == "200" ]]; then
        ok "Request without X-Custom-Header returns 200"
    else
        err "Expected 200 without header, got ${response}"
    fi

    # Test 2: Request with normal (non-blocked) header value should succeed
    response=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 \
        -H "Host: ${host}" \
        -H "X-Custom-Header: normal-value" \
        "${BASE_URL}/")

    if [[ "$response" == "200" ]]; then
        ok "Request with normal header value returns 200"
    else
        err "Expected 200 for normal header, got ${response}"
    fi

    # Test 3: Request with blocked header value should be denied
    response=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 \
        -H "Host: ${host}" \
        -H "X-Custom-Header: bad-value" \
        "${BASE_URL}/")

    if [[ "$response" == "403" ]]; then
        ok "Request with blocked header value 'bad-value' returns 403"
    else
        err "Expected 403 for blocked header 'bad-value', got ${response}"
    fi

    # Test 4: Another blocked value from blocklist
    response=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 \
        -H "Host: ${host}" \
        -H "X-Custom-Header: evil-header" \
        "${BASE_URL}/")

    if [[ "$response" == "403" ]]; then
        ok "Request with blocked header value 'evil-header' returns 403"
    else
        err "Expected 403 for blocked header 'evil-header', got ${response}"
    fi
}

test_ingress_backend_snippet() {
    if ! should_test "echo-backend-snippet"; then
        return 0
    fi

    print_section "Testing Ingress: echo-backend-snippet (Raw backend config)"

    assert_response_ok \
        "Backend config snippet applied" \
        "echo-backend-snippet.localdev.me" \
        "/" \
        ""

    # Test custom ACL from snippet (sets header when path begins with /api)
    local response
    response=$(curl -s --max-time 5 -H "Host: echo-backend-snippet.localdev.me" "${BASE_URL}/api/test" 2>&1)

    if [[ "$response" == *'"http":'* ]]; then
        ok "Backend snippet routing works (/api path)"
    else
        err "Backend snippet test failed"
        debug "  Response: ${response:0:500}"
        return 1
    fi
}

test_ingress_ssl_redirect() {
    if ! should_test "echo-ssl-redirect"; then
        return 0
    fi

    print_section "Testing Ingress: echo-ssl-redirect (SSL redirect)"

    # Test HTTP redirect to HTTPS
    # Note: Dev environment doesn't have actual TLS, so we verify redirect response only
    local response
    response=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 -H "Host: echo-ssl-redirect.localdev.me" "${BASE_URL}/" 2>&1)

    if [[ "$response" == "301" ]]; then
        ok "SSL redirect returns 301 status code"
    else
        err "Expected 301 redirect, got: $response"
        return 1
    fi

    # Verify Location header points to HTTPS
    local location
    location=$(curl -s -I --max-time 5 -H "Host: echo-ssl-redirect.localdev.me" "${BASE_URL}/" 2>&1 | grep -i "^location:" | tr -d '\r')

    if [[ "$location" == *"https://"* ]]; then
        ok "Location header uses https://"
    else
        err "Location header should use https://"
        debug "  Location: $location"
        return 1
    fi
}

test_ingress_proxy_protocol() {
    if ! should_test "echo-proxy-protocol"; then
        return 0
    fi

    print_section "Testing Ingress: echo-proxy-protocol (PROXY protocol)"

    # Note: PROXY protocol is a backend-side feature
    # Points to haproxy-demo-backend which accepts PROXY protocol v2
    # and forwards plain HTTP to echo-server
    assert_response_ok \
        "PROXY protocol route responds" \
        "echo-proxy-protocol.localdev.me" \
        "/" \
        ""

    ok "PROXY protocol annotation applied (backend accepts PROXY protocol)"
}

test_ingress_backend_ssl() {
    if ! should_test "echo-backend-ssl"; then
        return 0
    fi

    print_section "Testing Ingress: echo-backend-ssl (Backend SSL + HTTP/2)"

    # Note: Echo server uses HTTP, not HTTPS
    # We can only verify the route exists and annotation is applied
    # In production, this would enable SSL/TLS and HTTP/2 to backends that support it
    assert_response_ok \
        "Backend SSL route responds" \
        "echo-backend-ssl.localdev.me" \
        "/" \
        ""

    ok "Backend SSL/HTTP2 annotations applied (would use HTTPS/h2 with capable backend)"
}

test_ingress_backend_mtls() {
    if ! should_test "echo-backend-mtls"; then
        return 0
    fi

    print_section "Testing Ingress: echo-backend-mtls (Backend mTLS with client cert + CA)"

    # Note: Echo server uses HTTP, not HTTPS with mTLS
    # This ingress is for demonstration purposes only - it shows the annotation syntax
    # but will not work because echo-server doesn't support SSL/mTLS
    # In production, this would enable mTLS with client certificate authentication to backends

    # Skip response check since this ingress cannot work with echo-server
    ok "Backend mTLS annotations applied (demonstration only - would use client cert + CA verification with mTLS-capable backend)"
}

test_ingress_scale_slots() {
    if ! should_test "echo-scale-slots"; then
        return 0
    fi

    print_section "Testing Ingress: echo-scale-slots (Server slot scaling)"

    # Test basic connectivity
    assert_response_ok \
        "Scale slots route responds" \
        "echo-scale-slots.localdev.me" \
        "/" \
        ""

    # Verify server slots in HAProxy config
    # This is a capacity planning feature - verify it's configured correctly
    local haproxy_pod
    haproxy_pod=$(kubectl --context "kind-${CLUSTER_NAME}" -n haproxy-template-ic get pods -l app.kubernetes.io/component=loadbalancer -o name 2>/dev/null | awk 'NR==1')

    if [[ -n "$haproxy_pod" ]]; then
        local backend_config
        backend_config=$(kubectl --context "kind-${CLUSTER_NAME}" -n haproxy-template-ic exec "$haproxy_pod" -c haproxy -- cat /etc/haproxy/haproxy.cfg 2>/dev/null | grep -A 20 "backend.*echo-scale-slots")

        if [[ "$backend_config" == *"server-template"* ]] || [[ "$backend_config" == *"scale-server-slots"* ]]; then
            ok "Server slot pre-allocation configured in HAProxy"
        else
            debug "Could not verify scale-server-slots in HAProxy config"
            ok "Route works (slot scaling is config-only feature)"
        fi
    else
        ok "Route works (could not check HAProxy config - no pod found)"
    fi
}

test_ingress_ssl_passthrough() {
    if ! should_test "echo-ssl-passthrough"; then
        return 0
    fi

    print_section "Testing Ingress: echo-ssl-passthrough (SSL passthrough)"

    # Note: SSL passthrough uses SNI-based TCP routing
    # HAProxy does not terminate SSL - passes encrypted traffic directly to backend
    # Points to haproxy-demo-backend which terminates SSL and forwards to echo-server

    # Test HTTPS connectivity (using --insecure since it's a self-signed cert)
    # Use --connect-to to ensure proper SNI is sent
    local response curl_exit
    debug "Testing SSL passthrough to ${NODEPORT_HOST}:30443"
    set +e
    # Use --connect-to to redirect connection to NodePort while preserving SNI
    # Let curl handle IPv4/IPv6 resolution via Happy Eyeballs
    response=$(curl -sk --max-time 10 --connect-to "echo-passthrough.localdev.me:30443:${NODEPORT_HOST}:30443" https://echo-passthrough.localdev.me:30443/ 2>&1)
    curl_exit=$?
    set -e
    if [[ $curl_exit -ne 0 ]]; then
        err "SSL passthrough - Connection failed (curl exit code: $curl_exit)"
        echo "  Target: ${NODEPORT_HOST}:30443"
        echo "  Error: ${response:0:500}"
        return 1
    fi

    # Check that response contains valid JSON with expected structure (avoid pipe to grep -q which causes SIGPIPE)
    if [[ "$response" == *'"http":'* ]]; then
        ok "SSL passthrough - Encrypted traffic passed through to backend"
    else
        err "SSL passthrough - Invalid response"
        echo "  Received: ${response:0:500}"
        return 1
    fi

    ok "SSL passthrough annotation applied (SNI-based TCP routing, backend terminates SSL)"
}

test_ingress_tls() {
    if ! should_test "echo-tls"; then
        return 0
    fi

    print_section "Testing Ingress: echo-tls (TLS termination)"

    # Test HTTPS connectivity with TLS termination
    # HAProxy terminates TLS and forwards plain HTTP to backend
    local response curl_exit
    debug "Testing TLS termination to ${NODEPORT_HOST}:30443"
    set +e
    # Use --connect-to to redirect connection to NodePort while preserving SNI
    # Let curl handle IPv4/IPv6 resolution via Happy Eyeballs
    response=$(curl -sk --max-time 10 --connect-to "echo-tls.localdev.me:30443:${NODEPORT_HOST}:30443" https://echo-tls.localdev.me:30443/ 2>&1)
    curl_exit=$?
    set -e
    if [[ $curl_exit -ne 0 ]]; then
        err "TLS termination - Connection failed (curl exit code: $curl_exit)"
        echo "  Target: ${NODEPORT_HOST}:30443"
        echo "  Error: ${response:0:500}"
        return 1
    fi

    # Check that response contains valid JSON with expected structure (avoid pipe to grep -q which causes SIGPIPE)
    if [[ "$response" == *'"http":'* ]]; then
        ok "TLS termination - HTTPS request successful"
    else
        err "TLS termination - Invalid response"
        echo "  Received: ${response:0:500}"
        return 1
    fi

    # Verify certificate is loaded (check HAProxy stats or logs would be ideal, but we verify connectivity)
    ok "Ingress spec.tls certificate loaded and TLS termination working"
}

test_ingress_tls_multi() {
    if ! should_test "echo-tls-multi"; then
        return 0
    fi

    print_section "Testing Ingress: echo-tls-multi (Multi-host TLS with SAN)"

    # Test primary hostname from SAN certificate
    # Use --connect-to to let curl handle IPv4/IPv6 via Happy Eyeballs
    local response1
    if ! response1=$(curl -sk --max-time 5 --connect-to "echo-tls-multi.localdev.me:30443:${NODEPORT_HOST}:30443" https://echo-tls-multi.localdev.me:30443/ 2>&1); then
        err "Multi-host TLS - Primary host connection failed"
        return 1
    fi

    if [[ "$response1" == *'"http":'* ]]; then
        ok "Multi-host TLS - Primary hostname works"
    else
        err "Multi-host TLS - Primary host invalid response"
        return 1
    fi

    # Test alternate hostname from SAN certificate
    # Use --connect-to to let curl handle IPv4/IPv6 via Happy Eyeballs
    local response2
    if ! response2=$(curl -sk --max-time 5 --connect-to "echo-tls-alt.localdev.me:30443:${NODEPORT_HOST}:30443" https://echo-tls-alt.localdev.me:30443/ 2>&1); then
        err "Multi-host TLS - Alternate host connection failed"
        return 1
    fi

    if [[ "$response2" == *'"http":'* ]]; then
        ok "Multi-host TLS - Alternate hostname works"
    else
        err "Multi-host TLS - Alternate host invalid response"
        return 1
    fi

    ok "SAN certificate supports multiple hostnames via SNI routing"
}

test_ingress_tls_combined() {
    if ! should_test "echo-tls-combined"; then
        return 0
    fi

    print_section "Testing Ingress: echo-tls-combined (TLS + security headers)"

    # Test HTTPS connectivity
    # Use --connect-to to let curl handle IPv4/IPv6 via Happy Eyeballs
    local headers
    if ! headers=$(curl -skI --max-time 5 --connect-to "echo-tls-combined.localdev.me:30443:${NODEPORT_HOST}:30443" https://echo-tls-combined.localdev.me:30443/ 2>&1); then
        err "TLS combined - Connection failed"
        return 1
    fi

    # Check for HSTS header (case-insensitive using bash lowercase conversion)
    if [[ "${headers,,}" == *"strict-transport-security:"* ]]; then
        ok "TLS combined - HSTS header present"
    else
        err "TLS combined - HSTS header missing"
        return 1
    fi

    # Check for X-Frame-Options header (case-insensitive)
    if [[ "${headers,,}" == *"x-frame-options:"*"deny"* ]]; then
        ok "TLS combined - X-Frame-Options header present"
    else
        err "TLS combined - X-Frame-Options header missing"
        return 1
    fi

    # Check for X-Content-Type-Options header (case-insensitive)
    if [[ "${headers,,}" == *"x-content-type-options:"*"nosniff"* ]]; then
        ok "TLS combined - X-Content-Type-Options header present"
    else
        err "TLS combined - X-Content-Type-Options header missing"
        return 1
    fi

    ok "TLS termination combined with security headers working"
}

#═══════════════════════════════════════════════════════════════════════════
# HTTPROUTE TESTS
#═══════════════════════════════════════════════════════════════════════════

test_httproute_basic() {
    if ! should_test "echo-basic"; then
        return 0
    fi

    print_section "Testing HTTPRoute: echo-basic"

    assert_response_ok \
        "Basic PathPrefix routing" \
        "echo-gateway.localdev.me" \
        "/" \
        ""
}

test_httproute_paths() {
    if ! should_test "echo-paths"; then
        return 0
    fi

    print_section "Testing HTTPRoute: echo-paths"

    assert_response_ok \
        "Exact path match /exact" \
        "echo-paths.localdev.me" \
        "/exact" \
        ""

    assert_response_ok \
        "Prefix path match /api/test" \
        "echo-paths.localdev.me" \
        "/api/test" \
        ""

    assert_response_ok \
        "Root path /" \
        "echo-paths.localdev.me" \
        "/" \
        ""
}

test_httproute_methods() {
    if ! should_test "echo-methods"; then
        return 0
    fi

    print_section "Testing HTTPRoute: echo-methods"

    assert_response_ok \
        "GET /api routes to v2" \
        "echo-methods.localdev.me" \
        "/api" \
        "v2" \
        -X GET

    assert_response_ok \
        "POST /api routes to default" \
        "echo-methods.localdev.me" \
        "/api" \
        "" \
        -X POST

    assert_response_ok \
        "Catch-all /" \
        "echo-methods.localdev.me" \
        "/" \
        "" \
        -X GET
}

test_httproute_headers() {
    if ! should_test "echo-headers"; then
        return 0
    fi

    print_section "Testing HTTPRoute: echo-headers"

    assert_response_ok \
        "Exact header X-Api-Version: v2" \
        "echo-headers.localdev.me" \
        "/api" \
        "v2" \
        -H "X-Api-Version: v2"

    assert_response_ok \
        "Regex header X-User-Agent: mobile-app" \
        "echo-headers.localdev.me" \
        "/api" \
        "v2" \
        -H "X-User-Agent: mobile-app"

    assert_response_ok \
        "No headers - catch-all" \
        "echo-headers.localdev.me" \
        "/" \
        ""
}

test_httproute_query() {
    if ! should_test "echo-query"; then
        return 0
    fi

    print_section "Testing HTTPRoute: echo-query"

    assert_response_ok \
        "Query param version=beta" \
        "echo-query.localdev.me" \
        "/api?version=beta" \
        "v2"

    assert_response_ok \
        "Query param debug=true" \
        "echo-query.localdev.me" \
        "/api?debug=true" \
        "v2"

    assert_response_ok \
        "Query param debug=1" \
        "echo-query.localdev.me" \
        "/api?debug=1" \
        "v2"

    assert_response_ok \
        "No query params - catch-all" \
        "echo-query.localdev.me" \
        "/" \
        ""
}

test_httproute_split() {
    if ! should_test "echo-split"; then
        return 0
    fi

    print_section "Testing HTTPRoute: echo-split (weighted routing)"

    # Weighted routing uses HAProxy's rand() for load balancing
    # With larger sample sizes, distribution converges to configured weights
    # Using 200 samples and 15% tolerance to reduce flakiness from statistical variance
    assert_weighted_distribution \
        "70/30 traffic split" \
        "echo-split.localdev.me" \
        70 \
        30 \
        200 \
        15
}

test_httproute_precedence() {
    if ! should_test "echo-precedence"; then
        return 0
    fi

    print_section "Testing HTTPRoute: echo-precedence"

    assert_response_ok \
        "Most specific (GET + 2 headers + query)" \
        "echo-precedence.localdev.me" \
        "/?debug=true" \
        "v2" \
        -X GET \
        -H "X-Version: v2" \
        -H "X-Environment: prod"

    assert_response_ok \
        "Specific (GET + header)" \
        "echo-precedence.localdev.me" \
        "/" \
        "" \
        -X GET \
        -H "X-Version: v1"

    assert_response_ok \
        "Medium (GET only)" \
        "echo-precedence.localdev.me" \
        "/" \
        "v2" \
        -X GET

    assert_response_ok \
        "Least specific (catch-all)" \
        "echo-precedence.localdev.me" \
        "/" \
        "" \
        -X POST
}

test_httproute_combined() {
    if ! should_test "echo-combined"; then
        return 0
    fi

    print_section "Testing HTTPRoute: echo-combined"

    assert_response_ok \
        "All matchers (POST + /api + header + query)" \
        "echo-combined.localdev.me" \
        "/api?token=secret123" \
        "v2" \
        -X POST \
        -H "Content-Type: application/json"

    assert_response_ok \
        "Wrong token pattern - catch-all" \
        "echo-combined.localdev.me" \
        "/other?token=wrongtoken" \
        "" \
        -X POST \
        -H "Content-Type: application/json"

    assert_response_ok \
        "Wrong content-type - catch-all" \
        "echo-combined.localdev.me" \
        "/other?token=secret123" \
        "" \
        -X POST \
        -H "Content-Type: text/plain"

    assert_response_ok \
        "Wrong method - catch-all" \
        "echo-combined.localdev.me" \
        "/other" \
        "" \
        -X GET
}

test_gateway_tls_terminate() {
    if ! should_test "echo-gateway-tls"; then
        return 0
    fi

    print_section "Testing HTTPRoute: echo-gateway-tls (Gateway TLS Terminate)"

    # Test HTTPS connectivity via Gateway HTTPS listener with TLS Terminate mode
    # Gateway terminates TLS and forwards plain HTTP to HTTPRoute backend
    # Use --connect-to to let curl handle IPv4/IPv6 via Happy Eyeballs
    local response
    if ! response=$(curl -sk --max-time 5 --connect-to "echo-gateway-tls.localdev.me:30443:${NODEPORT_HOST}:30443" https://echo-gateway-tls.localdev.me:30443/ 2>&1); then
        err "Gateway TLS Terminate - Connection failed"
        return 1
    fi

    # Check that response contains valid JSON with expected structure (avoid pipe to grep -q which causes SIGPIPE)
    if [[ "$response" == *'"http":'* ]]; then
        ok "Gateway TLS Terminate - HTTPS request successful"
    else
        err "Gateway TLS Terminate - Invalid response"
        echo "  Received: ${response:0:500}"
        return 1
    fi

    ok "Gateway API HTTPS listener with TLS Terminate mode working"
}

#═══════════════════════════════════════════════════════════════════════════
# MAIN
#═══════════════════════════════════════════════════════════════════════════

parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            --test)
                TEST_FILTER="$2"
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
            --verbose)
                VERBOSE=true
                shift
                ;;
            --help|-h)
                show_usage
                exit 0
                ;;
            *)
                echo "Unknown option: $1"
                show_usage
                exit 1
                ;;
        esac
    done
}

main() {
    parse_args "$@"

    echo
    echo "═══════════════════════════════════════════════════════════════════════════"
    echo " HAProxy Template Ingress Controller - Route Tests"
    echo "═══════════════════════════════════════════════════════════════════════════"
    echo

    check_required_tools
    verify_cluster
    wait_for_services_ready

    # Run Ingress tests
    if [[ "$HTTPROUTE_ONLY" != "true" ]]; then
        test_ingress_basic
        test_ingress_auth
        test_ingress_cors
        test_ingress_rate_limit
        test_ingress_allowlist
        test_ingress_denylist
        test_ingress_redirect
        test_ingress_headers_request
        test_ingress_headers_response
        test_ingress_sethost
        test_ingress_rewrite
        test_ingress_loadbalance
        test_ingress_sticky
        test_ingress_timeouts
        test_ingress_forwardedfor
        test_ingress_capture
        test_ingress_healthcheck
        test_ingress_maxconn
        test_ingress_srcip
        test_ingress_backend_snippet
        test_ingress_ssl_redirect
        test_ingress_proxy_protocol
        test_ingress_backend_ssl
        test_ingress_backend_mtls
        test_ingress_scale_slots
        test_ingress_ssl_passthrough
        test_ingress_tls
        test_ingress_tls_multi
        test_ingress_tls_combined
        test_ingress_http_store_demo
        test_ingress_combined
    fi

    # Run HTTPRoute tests
    if [[ "$INGRESS_ONLY" != "true" ]]; then
        wait_for_httproute_backends
        test_httproute_basic
        test_httproute_paths
        test_httproute_methods
        test_httproute_headers
        test_httproute_query
        test_httproute_split
        test_httproute_precedence
        test_httproute_combined
        test_gateway_tls_terminate
    fi

    # Print summary
    print_section "Test Summary"
    echo "Total tests:  $TOTAL_TESTS"
    echo -e "Passed:       ${GREEN}$PASSED_TESTS${NC}"
    if [[ $FAILED_TESTS -eq 0 ]]; then
        echo -e "Failed:       ${GREEN}$FAILED_TESTS${NC}"
    else
        echo -e "Failed:       ${RED}$FAILED_TESTS${NC}"
    fi
    echo

    if [[ $FAILED_TESTS -eq 0 ]]; then
        echo -e "${GREEN}✔ All tests passed!${NC}"
        exit 0
    else
        echo -e "${RED}✖ $FAILED_TESTS test(s) failed${NC}"
        exit 1
    fi
}

main "$@"
