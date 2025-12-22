#!/bin/bash
# Generate a self-signed SSL certificate for local development and testing
# NOT for production use - use cert-manager or your own certificates in production

set -euo pipefail

# Configuration
NAMESPACE="${1:-haptic}"
SECRET_NAME="${2:-default-ssl-cert}"
CERT_DOMAIN="*.example.com"
CERT_VALIDITY_DAYS=365

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Helper functions
info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1"
    exit 1
}

# Main execution
main() {
    info "Generating self-signed SSL certificate for development"
    info "Domain: $CERT_DOMAIN"
    info "Validity: $CERT_VALIDITY_DAYS days"
    info "Target: Secret '$SECRET_NAME' in namespace '$NAMESPACE'"
    echo

    # Check if kubectl is available
    if ! command -v kubectl &> /dev/null; then
        error "kubectl command not found. Please install kubectl first."
    fi

    # Check if openssl is available
    if ! command -v openssl &> /dev/null; then
        error "openssl command not found. Please install openssl first."
    fi

    # Check if Secret already exists
    if kubectl get secret "$SECRET_NAME" -n "$NAMESPACE" &>/dev/null; then
        warn "Secret '$SECRET_NAME' already exists in namespace '$NAMESPACE'"
        read -p "Do you want to replace it? (y/N): " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            info "Keeping existing certificate"
            exit 0
        fi
        info "Deleting existing Secret..."
        kubectl delete secret "$SECRET_NAME" -n "$NAMESPACE"
    fi

    # Create namespace if it doesn't exist
    if ! kubectl get namespace "$NAMESPACE" &>/dev/null; then
        info "Creating namespace '$NAMESPACE'..."
        kubectl create namespace "$NAMESPACE"
    fi

    # Generate certificate
    info "Generating self-signed certificate..."

    # Create temporary directory for certificate files
    TEMP_DIR=$(mktemp -d)
    trap "rm -rf '$TEMP_DIR'" EXIT

    CERT_FILE="$TEMP_DIR/tls.crt"
    KEY_FILE="$TEMP_DIR/tls.key"

    # Generate self-signed certificate with SANs
    openssl req -x509 -newkey rsa:2048 -nodes \
        -keyout "$KEY_FILE" \
        -out "$CERT_FILE" \
        -days "$CERT_VALIDITY_DAYS" \
        -subj "/CN=$CERT_DOMAIN" \
        -addext "subjectAltName=DNS:$CERT_DOMAIN,DNS:example.com" \
        2>/dev/null || error "Failed to generate certificate"

    info "Certificate generated successfully"

    # Create Kubernetes TLS Secret
    info "Creating Kubernetes Secret..."
    kubectl create secret tls "$SECRET_NAME" \
        --cert="$CERT_FILE" \
        --key="$KEY_FILE" \
        --namespace="$NAMESPACE" \
        || error "Failed to create Secret"

    info "Success! Certificate created:"
    echo "  Namespace: $NAMESPACE"
    echo "  Secret:    $SECRET_NAME"
    echo "  Domain:    $CERT_DOMAIN"
    echo "  Valid for: $CERT_VALIDITY_DAYS days"
    echo
    info "You can now install the Helm chart with this certificate"
}

# Run main function
main
