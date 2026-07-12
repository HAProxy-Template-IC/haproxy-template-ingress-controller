# SSL Certificates

By default, the [HAPTIC Helm chart](deploying-with-helm.md) provisions a default SSL certificate for HTTPS traffic — via cert-manager when it's installed, otherwise as a chart-generated self-signed Secret — and the controller watches and deploys it to HAProxy. You can also disable HTTPS entirely — see [Disabling HTTPS](#disabling-https).

## Default SSL Certificate

### Default Behavior (Development/Testing)

A default install converges out of the box, with or without cert-manager:

- **cert-manager installed** (the `cert-manager.io/v1` API is present when Helm renders): the chart creates a self-signed `Issuer` named `<release>-ssl-selfsigned` and a `Certificate` for `localdev.me` and `*.localdev.me`; cert-manager provisions the `default-ssl-cert` Secret and renews it before expiry.
- **cert-manager absent**: the chart generates a self-signed `default-ssl-cert` Secret itself, for the same DNS names. This certificate is valid for 10 years and is **not** auto-rotated. The Secret survives uninstall and upgrade (`helm.sh/resource-policy: keep`), and the chart only generates it when the Secret doesn't already exist — a Secret you created out-of-band is left untouched.

The `localdev.me` domain resolves to `127.0.0.1`, making it useful for local development. No additional configuration is required:

```bash
helm install my-release oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic --version 0.2.0-alpha.1 \
  --namespace haptic --create-namespace
```

!!! note
    Both default certificates are self-signed and intended for development and testing only. For production, override with your own domain and issuer.

!!! warning "GitOps tools that render without cluster access"
    The no-cert-manager fallback checks for an existing Secret with Helm's `lookup` function, which returns nothing when the chart is rendered without cluster access (`helm template`, Argo CD) — every sync would then generate a fresh certificate. For those deployments, install cert-manager, or provide the certificate explicitly: inline via `controller.defaultSSLCertificate.create`/`cert`/`key`, or as a manually created Secret (see [Alternative: Manual Certificate](#alternative-manual-certificate)).

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
          class: haptic   # Match ingressClass.name from chart values
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

The controller references the Secret at `certificates/my-wildcard-cert`.

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

### Certificate Rotation

**With cert-manager**: Certificates are automatically renewed before expiration.

**Chart-generated self-signed Secret** (no cert-manager): never rotated automatically — it's valid for 10 years. Replace it like a manual certificate if you need a different one.

**Manual certificates**: You must update the Secret with a new certificate before the old one expires:

```bash
# Update Secret with new certificate
kubectl create secret tls default-ssl-cert \
  --cert=new-tls.crt \
  --key=new-tls.key \
  --namespace=haptic \
  --dry-run=client -o yaml | kubectl apply -f -
```

The controller watches the Secret and automatically deploys the updated certificate to HAProxy.

### SSL Troubleshooting

For SSL symptom diagnosis — "Secret not found" errors, HAProxy failing to start with SSL errors, or a certificate that isn't updating — see [Troubleshooting → SSL/TLS Issues](./troubleshooting.md#ssltls-issues).

## Webhook Certificates

The admission webhook requires TLS certificates. By default the chart generates a self-signed certificate itself — no cert-manager required (`webhook.certManager.enabled` is `false`):

```yaml
webhook:
  enabled: true
  # certManager.enabled defaults to false → the chart issues a self-signed cert
```

Rotate the self-signed certificate by deleting its Secret and re-running the upgrade:

```bash
kubectl delete secret <release>-webhook-cert -n haptic
helm upgrade <release> oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic --reuse-values
```

If cert-manager is installed, hand it the certificate instead so it issues and **auto-rotates** with a real CA:

```yaml
webhook:
  enabled: true
  certManager:
    enabled: true
    createIssuer: true  # Creates a self-signed Issuer automatically
```

The chart then creates:

- A self-signed `Issuer` resource
- A `Certificate` resource that references the Issuer
- CA-bundle injection into the webhook configuration

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

## See Also

- [Security](./operations/security.md) — webhook hardening, RBAC, and network exposure
- [Troubleshooting](./troubleshooting.md) — SSL symptom diagnosis and general debugging
