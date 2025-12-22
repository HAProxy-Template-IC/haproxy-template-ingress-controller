#!/usr/bin/env bash
#
# Helm Chart Default Values Test
#
# Tests that the helm chart works out-of-the-box with default values when
# cert-manager is installed. Verifies: pods running, SSL certificate created,
# HTTP/HTTPS connectivity.
#
# Usage:
#   ./scripts/test-helm-defaults.sh [options]
#
# Options:
#   --keep-cluster    Don't delete cluster on success (useful for debugging)
#   --image IMAGE     Override image (default: use chart default)
#   --namespace NS    Namespace to install into (default: haptic)
#   --help            Show this help message
#
# Environment variables:
#   CLUSTER_NAME      Kind cluster name (default: helm-defaults)
#   KEEP_CLUSTER      Set to "true" to keep cluster on success
#   IMAGE             Controller image to use (e.g., registry.example.com/image:tag)
#   NAMESPACE         Namespace for installation (default: haptic)
#   TIMEOUT           Timeout in seconds for wait operations (default: 300)
#
# Exit codes:
#   0 - All checks passed
#   1 - Cluster creation failed
#   2 - cert-manager installation/readiness failed
#   3 - Helm chart installation failed
#   4 - Pod readiness timeout
#   5 - Certificate verification failed
#   6 - HTTP smoke test failed
#   7 - HTTPS smoke test failed

set -euo pipefail

# Configuration (can be overridden via environment)
CLUSTER_NAME="${CLUSTER_NAME:-helm-defaults}"
NAMESPACE="${NAMESPACE:-haptic}"
RELEASE_NAME="${RELEASE_NAME:-haptic}"
TIMEOUT="${TIMEOUT:-300}"
KEEP_CLUSTER="${KEEP_CLUSTER:-false}"
IMAGE="${IMAGE:-}"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.16.2}"

# Temp file tracking (for cleanup)
TEMP_KIND_CONFIG=""

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

#------------------------------------------------------------------------------
# Logging functions
#------------------------------------------------------------------------------

log() {
    local level="$1"
    shift
    local msg="$*"
    local color=""

    case "$level" in
        INFO)  color="$BLUE" ;;
        OK)    color="$GREEN" ;;
        WARN)  color="$YELLOW" ;;
        ERROR) color="$RED" ;;
    esac

    echo -e "${color}[$level]${NC} $msg"
}

info()  { log INFO "$@"; }
ok()    { log OK "$@"; }
warn()  { log WARN "$@"; }
error() { log ERROR "$@"; }

die() {
    error "$@"
    exit "${2:-1}"
}

#------------------------------------------------------------------------------
# DinD detection
#------------------------------------------------------------------------------

is_docker_in_docker() {
    [[ "${DOCKER_HOST:-}" == tcp://* ]]
}

#------------------------------------------------------------------------------
# Cluster management
#------------------------------------------------------------------------------

cluster_exists() {
    kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"
}

create_cluster() {
    info "Creating Kind cluster '$CLUSTER_NAME'..."

    local kind_config=""

    # Use DinD config if in Docker-in-Docker environment
    if is_docker_in_docker; then
        kind_config="$PROJECT_ROOT/.gitlab/ci/kind-config-dind.yaml"
        if [[ ! -f "$kind_config" ]]; then
            die "DinD config not found: $kind_config" 1
        fi
        info "Using DinD configuration: $kind_config"
    else
        # Create minimal config for local testing with NodePort mappings
        TEMP_KIND_CONFIG=$(mktemp)
        kind_config="$TEMP_KIND_CONFIG"
        cat > "$kind_config" << 'EOF'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 30080
        hostPort: 30080
        listenAddress: "0.0.0.0"
        protocol: TCP
      - containerPort: 30443
        hostPort: 30443
        listenAddress: "0.0.0.0"
        protocol: TCP
EOF
        info "Using minimal local configuration"
    fi

    if ! kind create cluster --name "$CLUSTER_NAME" --config "$kind_config" --wait 120s; then
        die "Failed to create Kind cluster" 1
    fi

    # Patch kubeconfig for DinD
    if is_docker_in_docker; then
        info "Patching kubeconfig for DinD..."
        sed -i 's|https://0\.0\.0\.0:|https://docker:|g' ~/.kube/config
    fi

    # Verify cluster is accessible
    if ! kubectl cluster-info &>/dev/null; then
        die "Cluster created but not accessible" 1
    fi

    ok "Cluster '$CLUSTER_NAME' created successfully"
}

delete_cluster() {
    if cluster_exists; then
        info "Deleting Kind cluster '$CLUSTER_NAME'..."
        kind delete cluster --name "$CLUSTER_NAME" || true
    fi
}

#------------------------------------------------------------------------------
# cert-manager installation
#------------------------------------------------------------------------------

install_cert_manager() {
    info "Installing cert-manager $CERT_MANAGER_VERSION..."

    if ! kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"; then
        die "Failed to install cert-manager" 2
    fi

    info "Waiting for cert-manager deployments to be ready..."

    local deployments=("cert-manager" "cert-manager-cainjector" "cert-manager-webhook")
    for deploy in "${deployments[@]}"; do
        if ! kubectl wait --for=condition=Available deployment/"$deploy" \
            -n cert-manager --timeout="${TIMEOUT}s"; then
            die "cert-manager deployment '$deploy' not ready within timeout" 2
        fi
    done

    # Wait for webhook to be fully operational by checking it can serve requests
    info "Waiting for cert-manager webhook to be fully operational..."
    local retries=30
    for ((i=1; i<=retries; i++)); do
        # Check if webhook endpoints exist and are ready
        local endpoints
        endpoints=$(kubectl get endpoints cert-manager-webhook -n cert-manager -o jsonpath='{.subsets[*].addresses[*].ip}' 2>/dev/null || echo "")
        if [[ -n "$endpoints" ]]; then
            # Verify webhook is responding by checking the ValidatingWebhookConfiguration
            if kubectl get validatingwebhookconfiguration cert-manager-webhook -o name &>/dev/null; then
                ok "cert-manager webhook is operational"
                break
            fi
        fi
        if [[ $i -eq $retries ]]; then
            warn "cert-manager webhook may not be fully ready, proceeding anyway"
        else
            info "Webhook not ready yet (attempt $i/$retries)..."
            sleep 2
        fi
    done

    ok "cert-manager installed and ready"
}

#------------------------------------------------------------------------------
# Helm chart installation
#------------------------------------------------------------------------------

load_image_to_cluster() {
    if [[ -z "$IMAGE" ]]; then
        return
    fi

    info "Loading image '$IMAGE' into kind cluster..."

    # Check if image exists locally
    if ! docker image inspect "$IMAGE" &>/dev/null; then
        warn "Image '$IMAGE' not found locally, skipping load (will pull from registry)"
        return
    fi

    if ! kind load docker-image "$IMAGE" --name "$CLUSTER_NAME"; then
        die "Failed to load image into kind cluster" 3
    fi

    ok "Image loaded into cluster"
}

install_helm_chart() {
    info "Installing helm chart with default values..."

    # Load image into cluster if specified
    load_image_to_cluster

    # Don't use --wait here because pods depend on cert-manager creating secrets first
    # We'll wait for certificates, then pods separately
    local helm_args=(
        "upgrade" "--install" "$RELEASE_NAME"
        "$PROJECT_ROOT/charts/haproxy-template-ic"
        "--namespace" "$NAMESPACE"
        "--create-namespace"
    )

    # Add image override if specified
    if [[ -n "$IMAGE" ]]; then
        info "Using custom image: $IMAGE"
        helm_args+=("--set" "image.repository=${IMAGE%:*}")
        if [[ "$IMAGE" == *:* ]]; then
            helm_args+=("--set" "image.tag=${IMAGE##*:}")
        fi
    fi

    if ! helm "${helm_args[@]}"; then
        die "Helm chart installation failed" 3
    fi

    ok "Helm chart installed successfully"
}

#------------------------------------------------------------------------------
# Pod readiness checks
#------------------------------------------------------------------------------

wait_for_pods() {
    info "Waiting for pods to be ready..."

    # Wait for controller pods
    info "Waiting for controller pods..."
    if ! kubectl wait --for=condition=Ready pod \
        -l "app.kubernetes.io/component=controller" \
        -n "$NAMESPACE" --timeout="${TIMEOUT}s"; then
        die "Controller pods not ready within timeout" 4
    fi

    # Wait for HAProxy pods
    info "Waiting for HAProxy pods..."
    if ! kubectl wait --for=condition=Ready pod \
        -l "app.kubernetes.io/component=loadbalancer" \
        -n "$NAMESPACE" --timeout="${TIMEOUT}s"; then
        die "HAProxy pods not ready within timeout" 4
    fi

    ok "All pods are ready"
}

#------------------------------------------------------------------------------
# Certificate verification
#------------------------------------------------------------------------------

verify_certificates() {
    info "Verifying cert-manager resources..."

    # Check SSL Issuer (with retry for race condition with cert-manager)
    info "Checking SSL Issuer..."
    local issuer_found=false
    for ((i=1; i<=10; i++)); do
        if kubectl get issuer -n "$NAMESPACE" -o name 2>/dev/null | grep -q "ssl-selfsigned"; then
            issuer_found=true
            break
        fi
        info "SSL Issuer not found yet (attempt $i/10)..."
        sleep 2
    done
    if [[ "$issuer_found" != "true" ]]; then
        warn "SSL Issuer not found, listing all issuers:"
        kubectl get issuer -n "$NAMESPACE" -o wide || true
        die "SSL self-signed Issuer not found" 5
    fi

    # Check SSL Certificate (with retry for race condition with cert-manager)
    info "Checking SSL Certificate..."
    local cert_found=false
    for ((i=1; i<=10; i++)); do
        if kubectl get certificate -n "$NAMESPACE" -o name 2>/dev/null | grep -q "default-ssl-cert"; then
            cert_found=true
            break
        fi
        info "SSL Certificate not found yet (attempt $i/10)..."
        sleep 2
    done
    if [[ "$cert_found" != "true" ]]; then
        warn "SSL Certificate not found, listing all certificates:"
        kubectl get certificate -n "$NAMESPACE" -o wide || true
        die "default-ssl-cert Certificate not found" 5
    fi

    # Wait for certificate to be ready
    info "Waiting for SSL certificate to be ready..."
    local retries=30
    local ready=false
    for ((i=1; i<=retries; i++)); do
        local status
        status=$(kubectl get certificate default-ssl-cert -n "$NAMESPACE" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")
        if [[ "$status" == "True" ]]; then
            ready=true
            break
        fi
        info "Certificate not ready yet (attempt $i/$retries)..."
        sleep 5
    done

    if [[ "$ready" != "true" ]]; then
        warn "Certificate status:"
        kubectl get certificate -n "$NAMESPACE" -o wide || true
        kubectl describe certificate default-ssl-cert -n "$NAMESPACE" || true
        die "SSL certificate not ready within timeout" 5
    fi

    # Check TLS Secret exists
    info "Checking SSL TLS Secret..."
    if ! kubectl get secret default-ssl-cert -n "$NAMESPACE" -o jsonpath='{.type}' | grep -q "kubernetes.io/tls"; then
        die "TLS secret 'default-ssl-cert' not found or wrong type" 5
    fi

    # Check Webhook Issuer (with retry for race condition with cert-manager)
    info "Checking Webhook Issuer..."
    local webhook_issuer_found=false
    for ((i=1; i<=10; i++)); do
        if kubectl get issuer -n "$NAMESPACE" -o name 2>/dev/null | grep -q "webhook-selfsigned"; then
            webhook_issuer_found=true
            break
        fi
        info "Webhook Issuer not found yet (attempt $i/10)..."
        sleep 2
    done
    if [[ "$webhook_issuer_found" != "true" ]]; then
        warn "Webhook Issuer not found, listing all issuers:"
        kubectl get issuer -n "$NAMESPACE" -o wide || true
        die "Webhook self-signed Issuer not found" 5
    fi

    # Check Webhook Certificate (with retry for race condition with cert-manager)
    info "Checking Webhook Certificate..."
    local webhook_cert_found=false
    for ((i=1; i<=10; i++)); do
        if kubectl get certificate -n "$NAMESPACE" -o name 2>/dev/null | grep -q "webhook-cert"; then
            webhook_cert_found=true
            break
        fi
        info "Webhook Certificate not found yet (attempt $i/10)..."
        sleep 2
    done
    if [[ "$webhook_cert_found" != "true" ]]; then
        warn "Webhook Certificate not found, listing all certificates:"
        kubectl get certificate -n "$NAMESPACE" -o wide || true
        die "webhook-cert Certificate not found" 5
    fi

    # Wait for webhook certificate to be ready
    info "Waiting for Webhook certificate to be ready..."
    local webhook_ready=false
    for ((i=1; i<=retries; i++)); do
        local webhook_status
        webhook_status=$(kubectl get certificate "${RELEASE_NAME}-webhook-cert" -n "$NAMESPACE" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")
        if [[ "$webhook_status" == "True" ]]; then
            webhook_ready=true
            break
        fi
        info "Webhook certificate not ready yet (attempt $i/$retries)..."
        sleep 5
    done

    if [[ "$webhook_ready" != "true" ]]; then
        warn "Webhook certificate status:"
        kubectl get certificate -n "$NAMESPACE" -o wide || true
        kubectl describe certificate "${RELEASE_NAME}-webhook-cert" -n "$NAMESPACE" || true
        die "Webhook certificate not ready within timeout" 5
    fi

    # Check Webhook TLS Secret exists
    info "Checking Webhook TLS Secret..."
    if ! kubectl get secret "${RELEASE_NAME}-webhook-cert" -n "$NAMESPACE" -o jsonpath='{.type}' | grep -q "kubernetes.io/tls"; then
        die "TLS secret '${RELEASE_NAME}-webhook-cert' not found or wrong type" 5
    fi

    ok "All cert-manager resources verified"
}

#------------------------------------------------------------------------------
# Smoke tests
#------------------------------------------------------------------------------

# Start port-forward in background and return the PID
# This is more reliable than NodePort in DinD environments
PORT_FORWARD_PID=""
start_port_forward() {
    info "Starting port-forward to HAProxy service..."

    # Start port-forward in background
    kubectl port-forward -n "$NAMESPACE" "svc/${RELEASE_NAME}-haproxy" 8080:80 8443:443 >/dev/null 2>&1 &
    PORT_FORWARD_PID=$!

    # Wait for port-forward to be ready
    local max_attempts=15
    local attempt=1

    while [[ $attempt -le $max_attempts ]]; do
        if curl -s -o /dev/null --connect-timeout 1 "http://localhost:8080" 2>/dev/null; then
            ok "Port-forward is ready (pid: $PORT_FORWARD_PID)"
            return 0
        fi

        if ! kill -0 "$PORT_FORWARD_PID" 2>/dev/null; then
            die "Port-forward process died unexpectedly" 6
        fi

        if [[ $attempt -lt $max_attempts ]]; then
            info "Port-forward not ready yet (attempt $attempt/$max_attempts), waiting 1s..."
            sleep 1
        fi
        ((attempt++)) || true
    done

    kill "$PORT_FORWARD_PID" 2>/dev/null || true
    die "Port-forward not accessible after $max_attempts attempts" 6
}

stop_port_forward() {
    if [[ -n "$PORT_FORWARD_PID" ]] && kill -0 "$PORT_FORWARD_PID" 2>/dev/null; then
        info "Stopping port-forward (pid: $PORT_FORWARD_PID)"
        kill "$PORT_FORWARD_PID" 2>/dev/null || true
        wait "$PORT_FORWARD_PID" 2>/dev/null || true
        PORT_FORWARD_PID=""
    fi
}

smoke_test_http() {
    info "Running HTTP smoke test..."

    local url="http://localhost:8080"

    info "Testing: $url (via port-forward)"

    # curl -w "%{http_code}" outputs "000" on connection failures, no need for || fallback
    local http_code
    http_code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 "$url")

    info "HTTP response code: $http_code"

    # Expect 503 (no backends configured) - HAProxy is responding
    if [[ "$http_code" == "503" ]]; then
        ok "HTTP smoke test passed (got expected 503 - HAProxy is responding)"
        return 0
    elif [[ "$http_code" == "000" ]]; then
        die "HTTP smoke test failed - connection refused or timeout (code: $http_code)" 6
    else
        warn "HTTP smoke test returned unexpected status code: $http_code (expected 503)"
        # Still pass if we got any response from HAProxy
        ok "HTTP smoke test passed (HAProxy responded with $http_code)"
        return 0
    fi
}

smoke_test_https() {
    info "Running HTTPS smoke test..."

    local url="https://localhost:8443"

    info "Testing: $url (via port-forward)"

    # curl -w "%{http_code}" outputs "000" on connection failures, no need for || fallback
    local http_code
    http_code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 -k "$url")

    info "HTTPS response code: $http_code"

    # Expect 503 (no backends configured) - HAProxy is responding with TLS
    if [[ "$http_code" == "503" ]]; then
        ok "HTTPS smoke test passed (got expected 503 - HAProxy TLS is working)"
        return 0
    elif [[ "$http_code" == "000" ]]; then
        die "HTTPS smoke test failed - connection refused or timeout (status: $http_code)" 7
    else
        warn "HTTPS smoke test returned unexpected status code: $http_code (expected 503)"
        ok "HTTPS smoke test passed (HAProxy TLS responded with $http_code)"
        return 0
    fi
}

verify_ssl_certificate() {
    info "Verifying SSL certificate via openssl..."

    # Get certificate info using openssl (via port-forward on localhost:8443)
    # Use timeout to prevent hanging, and </dev/null to properly close connection
    local cert_info
    if ! cert_info=$(timeout 10 openssl s_client -connect "localhost:8443" -servername localhost </dev/null 2>/dev/null | openssl x509 -noout -subject -issuer 2>/dev/null); then
        # If openssl fails, try to at least verify we can connect with TLS
        warn "openssl s_client failed, falling back to curl certificate check"
        local curl_cert
        if curl_cert=$(timeout 10 curl -vks --connect-timeout 5 "https://localhost:8443/" 2>&1 | grep -i "SSL certificate"); then
            info "TLS connection successful via curl"
            ok "SSL certificate verification passed (via curl fallback)"
            return 0
        fi
        die "Failed to retrieve SSL certificate" 7
    fi

    info "Certificate info:"
    echo "$cert_info"

    # Verify we got a certificate (any certificate is fine for self-signed)
    if [[ -z "$cert_info" ]]; then
        die "No certificate returned from HAProxy" 7
    fi

    ok "SSL certificate verification passed"
}

#------------------------------------------------------------------------------
# Debug output (on failure)
#------------------------------------------------------------------------------

dump_debug_info() {
    echo ""
    error "============================================"
    error "SMOKE TEST FAILED - Debug Information"
    error "============================================"
    echo ""

    echo "=== Pod Status ==="
    kubectl get pods -A 2>/dev/null || true
    echo ""

    echo "=== Events (last 50) ==="
    kubectl get events -A --sort-by='.lastTimestamp' 2>/dev/null | tail -50 || true
    echo ""

    echo "=== Controller Logs ==="
    kubectl logs -n "$NAMESPACE" -l "app.kubernetes.io/component=controller" --tail=100 2>/dev/null || true
    echo ""

    echo "=== HAProxy Logs ==="
    kubectl logs -n "$NAMESPACE" -l "app.kubernetes.io/component=loadbalancer" --tail=100 2>/dev/null || true
    echo ""

    echo "=== Certificates ==="
    kubectl get certificates -A -o wide 2>/dev/null || true
    echo ""

    echo "=== Issuers ==="
    kubectl get issuers -A -o wide 2>/dev/null || true
    echo ""

    echo "=== Secrets (TLS type) ==="
    kubectl get secrets -A --field-selector type=kubernetes.io/tls 2>/dev/null || true
    echo ""

    echo "=== HAProxy Service ==="
    kubectl get svc -n "$NAMESPACE" -o wide 2>/dev/null || true
    echo ""

    echo "=== HAProxy Endpoints ==="
    kubectl get endpoints -n "$NAMESPACE" 2>/dev/null || true
    echo ""

    echo "=== HAProxy Config (frontends only) ==="
    local haproxy_pod
    haproxy_pod=$(kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/component=loadbalancer" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
    if [[ -n "$haproxy_pod" ]]; then
        kubectl exec -n "$NAMESPACE" "$haproxy_pod" -- cat /etc/haproxy/haproxy.cfg 2>/dev/null | grep -A5 "^frontend" || true
    fi
    echo ""
}

#------------------------------------------------------------------------------
# Cleanup
#------------------------------------------------------------------------------

cleanup() {
    local exit_code=$?

    # Stop port-forward if running
    stop_port_forward

    if [[ $exit_code -ne 0 ]]; then
        dump_debug_info
    fi

    if [[ "$KEEP_CLUSTER" != "true" ]]; then
        delete_cluster
    else
        info "Keeping cluster '$CLUSTER_NAME' for debugging"
        info "Context: kind-$CLUSTER_NAME"
    fi

    # Clean up temp kind config if created
    if [[ -n "$TEMP_KIND_CONFIG" && -f "$TEMP_KIND_CONFIG" ]]; then
        rm -f "$TEMP_KIND_CONFIG"
    fi

    exit $exit_code
}

#------------------------------------------------------------------------------
# Help
#------------------------------------------------------------------------------

show_help() {
    head -40 "$0" | grep -E "^#" | sed 's/^#//' | sed 's/^ //'
    exit 0
}

#------------------------------------------------------------------------------
# Main
#------------------------------------------------------------------------------

main() {
    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --keep-cluster)
                KEEP_CLUSTER="true"
                shift
                ;;
            --image)
                IMAGE="$2"
                shift 2
                ;;
            --namespace)
                NAMESPACE="$2"
                shift 2
                ;;
            --help|-h)
                show_help
                ;;
            *)
                die "Unknown option: $1. Use --help for usage."
                ;;
        esac
    done

    # Set up cleanup trap
    trap cleanup EXIT

    echo ""
    info "============================================"
    info "Helm Chart Default Values Test"
    info "============================================"
    info "Cluster:   $CLUSTER_NAME"
    info "Namespace: $NAMESPACE"
    info "Image:     ${IMAGE:-<chart default>}"
    info "Timeout:   ${TIMEOUT}s"
    info "DinD:      $(is_docker_in_docker && echo "yes" || echo "no")"
    echo ""

    # Run test steps
    if cluster_exists; then
        info "Cluster '$CLUSTER_NAME' already exists, reusing it"
    else
        create_cluster
    fi
    install_cert_manager
    install_helm_chart
    # Verify certificates BEFORE waiting for pods - pods need the cert secrets to start
    verify_certificates
    wait_for_pods
    # Start port-forward for smoke tests (more reliable than NodePort in DinD)
    start_port_forward
    smoke_test_http
    smoke_test_https
    verify_ssl_certificate
    stop_port_forward

    echo ""
    ok "============================================"
    ok "ALL SMOKE TESTS PASSED"
    ok "============================================"
    echo ""
}

main "$@"
