#!/usr/bin/env bash
#
# Generate self-signed certificates for webhook validation in development.
# Creates CA certificate and server certificate with all required DNS SANs.
#
# Usage: ./generate-webhook-certs.sh <namespace> <service-name> <output-dir>
#
# Example: ./generate-webhook-certs.sh haptic haptic-webhook /tmp/certs
#

set -euo pipefail

NAMESPACE="${1:-haptic}"
SERVICE_NAME="${2:-haptic-webhook}"
OUTPUT_DIR="${3:-/tmp/webhook-certs}"

# Certificate validity (1 year for dev is fine)
DAYS_VALID=365

# Create output directory
mkdir -p "$OUTPUT_DIR"

echo "Generating webhook certificates for development..."
echo "  Namespace: $NAMESPACE"
echo "  Service: $SERVICE_NAME"
echo "  Output: $OUTPUT_DIR"
echo

# Generate CA private key
openssl genrsa -out "$OUTPUT_DIR/ca.key" 2048 2>/dev/null

# Generate CA certificate
openssl req -x509 -new -nodes \
  -key "$OUTPUT_DIR/ca.key" \
  -subj "/CN=Webhook CA" \
  -days "$DAYS_VALID" \
  -out "$OUTPUT_DIR/ca.crt" 2>/dev/null

echo "✓ Generated CA certificate"

# Generate server private key
openssl genrsa -out "$OUTPUT_DIR/tls.key" 2048 2>/dev/null

# Create OpenSSL config with all DNS SANs
cat > "$OUTPUT_DIR/server.conf" <<EOF
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name

[req_distinguished_name]

[v3_req]
basicConstraints = CA:FALSE
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names

[alt_names]
DNS.1 = ${SERVICE_NAME}
DNS.2 = ${SERVICE_NAME}.${NAMESPACE}
DNS.3 = ${SERVICE_NAME}.${NAMESPACE}.svc
DNS.4 = ${SERVICE_NAME}.${NAMESPACE}.svc.cluster.local
EOF

# Generate server certificate signing request
openssl req -new \
  -key "$OUTPUT_DIR/tls.key" \
  -subj "/CN=${SERVICE_NAME}.${NAMESPACE}.svc" \
  -out "$OUTPUT_DIR/server.csr" \
  -config "$OUTPUT_DIR/server.conf" 2>/dev/null

# Sign server certificate with CA
openssl x509 -req \
  -in "$OUTPUT_DIR/server.csr" \
  -CA "$OUTPUT_DIR/ca.crt" \
  -CAkey "$OUTPUT_DIR/ca.key" \
  -CAcreateserial \
  -out "$OUTPUT_DIR/tls.crt" \
  -days "$DAYS_VALID" \
  -extensions v3_req \
  -extfile "$OUTPUT_DIR/server.conf" 2>/dev/null

echo "✓ Generated server certificate"

# Clean up temporary files
rm -f "$OUTPUT_DIR/server.csr" "$OUTPUT_DIR/server.conf" "$OUTPUT_DIR/ca.srl"

# Display certificate information
echo
echo "Certificate details:"
echo "  CA valid until: $(openssl x509 -enddate -noout -in "$OUTPUT_DIR/ca.crt" | cut -d= -f2)"
echo "  Server valid until: $(openssl x509 -enddate -noout -in "$OUTPUT_DIR/tls.crt" | cut -d= -f2)"
echo
echo "DNS SANs in server certificate:"
openssl x509 -text -noout -in "$OUTPUT_DIR/tls.crt" | grep -A1 "Subject Alternative Name" | tail -n1 | sed 's/^[[:space:]]*/  /'
echo

# Output base64-encoded CA bundle for ValidatingWebhookConfiguration
CA_BUNDLE=$(base64 < "$OUTPUT_DIR/ca.crt" | tr -d '\n')
echo "Base64-encoded CA bundle (for webhook configuration):"
echo "$CA_BUNDLE"
echo
echo "$CA_BUNDLE" > "$OUTPUT_DIR/ca-bundle.b64"

echo "✓ Certificates generated successfully in $OUTPUT_DIR"
echo
echo "Files created:"
echo "  ca.crt           - CA certificate"
echo "  ca.key           - CA private key"
echo "  tls.crt          - Server certificate"
echo "  tls.key          - Server private key"
echo "  ca-bundle.b64    - Base64-encoded CA bundle"
