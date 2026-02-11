# SSL Certificates

## Overview

The controller requires a default SSL certificate for HTTPS traffic. This page covers SSL certificate configuration via cert-manager or manual management, as well as webhook certificate setup.

## Default SSL Certificate

### Default Behavior (Development/Testing)

The chart works out of the box with cert-manager installed. By default, it creates:

- A self-signed `Issuer` named `<release>-ssl-selfsigned`
- A `Certificate` for `localdev.me` and `*.localdev.me`

The `localdev.me` domain resolves to `127.0.0.1`, making it useful for local development. No additional configuration is required beyond having cert-manager installed:

```bash
# Install cert-manager (if not already installed)
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.16.0/cert-manager.yaml

# Install the chart - SSL works out of the box
helm install my-release oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic --version 0.1.0-alpha.11
```

!!! note
    The default self-signed certificate is intended for development and testing only. For production, override with your own domain and issuer.

### Production Deployment

For production, override the default certificate configuration with your actual domain and a trusted issuer:

```yaml
controller:
  defaultSSLCertificate:
    certManager:
      createIssuer: false  # Use your own issuer
      dnsNames:
        - "*.example.com"
        - "example.com"
      issuerRef:
        name: letsencrypt-prod
        kind: ClusterIssuer
```

This requires an existing ClusterIssuer or Issuer. Create one if you haven't already:

```bash
# Create a ClusterIssuer (example with Let's Encrypt)
kubectl apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: your-email@example.com
    privateKeySecretRef:
      name: letsencrypt-prod
    solvers:
    - http01:
        ingress:
          class: haproxy
EOF
```

The Helm chart creates a Certificate resource that cert-manager uses to automatically provision and renew the TLS Secret.

### Alternative: Manual Certificate

To manage certificates without cert-manager, disable cert-manager integration and create a TLS Secret manually:

```yaml
controller:
  defaultSSLCertificate:
    certManager:
      enabled: false
```

```bash
kubectl create secret tls default-ssl-cert \
  --cert=path/to/tls.crt \
  --key=path/to/tls.key \
  --namespace=haptic
```

### Custom Certificate Names

To use a different Secret name or namespace:

```yaml
controller:
  defaultSSLCertificate:
    secretName: "my-wildcard-cert"
    namespace: "certificates"
```

The controller will reference the Secret at `certificates/my-wildcard-cert`.

### TLS Secret Format

The Secret must be of type `kubernetes.io/tls` and contain two keys:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: default-ssl-cert
  namespace: haptic
type: kubernetes.io/tls
data:
  tls.crt: LS0tLS1CRUdJTi... # Base64-encoded certificate
  tls.key: LS0tLS1CRUdJTi... # Base64-encoded private key
```

### Disabling HTTPS

To run in HTTP-only mode (not recommended):

```yaml
controller:
  defaultSSLCertificate:
    enabled: false
```

!!! warning
    This disables HTTPS support entirely. HAProxy will only serve HTTP traffic.

### Certificate Rotation

**With cert-manager**: Certificates are automatically renewed before expiration.

**Manual certificates**: You must update the Secret with a new certificate before the old one expires:

```bash
# Update Secret with new certificate
kubectl create secret tls default-ssl-cert \
  --cert=new-tls.crt \
  --key=new-tls.key \
  --namespace=haptic \
  --dry-run=client -o yaml | kubectl apply -f -
```

The controller watches the Secret and will automatically deploy the updated certificate to HAProxy.

### SSL Troubleshooting

**"Secret not found" errors:**

Check that the Secret exists in the correct namespace:

```bash
kubectl get secret default-ssl-cert -n haptic
```

**HAProxy fails to start with SSL errors:**

Verify the certificate and key are valid:

```bash
# Extract and verify certificate
kubectl get secret default-ssl-cert -n haptic -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -text -noout

# Verify key
kubectl get secret default-ssl-cert -n haptic -o jsonpath='{.data.tls\.key}' | base64 -d | openssl rsa -check -noout
```

**Certificate not being updated:**

The controller watches Secrets with `store: on-demand`. Changes are detected automatically, but HAProxy deployment follows the configured drift prevention interval (default: 60s).

## Webhook Certificates

The admission webhook requires TLS certificates. The simplest setup uses cert-manager with a self-signed issuer:

```yaml
webhook:
  enabled: true
  certManager:
    enabled: true
    createIssuer: true  # Creates a self-signed Issuer automatically
```

This is the recommended approach when cert-manager is installed. The chart creates:

- A self-signed `Issuer` resource
- A `Certificate` resource that references the Issuer
- The webhook is automatically configured with CA bundle injection

To use an existing Issuer or ClusterIssuer instead:

```yaml
webhook:
  certManager:
    enabled: true
    createIssuer: false
    issuerRef:
      name: my-existing-issuer
      kind: ClusterIssuer
```

For manual certificate management without cert-manager, provide the CA bundle:

```yaml
webhook:
  certManager:
    enabled: false
  caBundle: "LS0tLS1CRUdJTi..."  # Base64-encoded CA certificate
```
