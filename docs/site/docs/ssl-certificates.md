# SSL certificates

By default, the [HAPTIC Helm chart](deploying-with-helm.md) provisions a default SSL certificate for HTTPS traffic — via cert-manager when it's installed, otherwise as a chart-generated self-signed Secret — and the controller watches and deploys it to HAProxy. You can also disable HTTPS entirely — see [Disabling HTTPS](#disabling-https).

!!! tip "The default certificate and per-host TLS"
    This page covers the chart's **default** certificate. HAPTIC serves it for every Ingress over HTTPS by default, and as the fallback when a Server Name Indication (SNI) match isn't found. To serve a specific certificate for one host, add a `spec.tls` entry and a `kubernetes.io/tls` Secret to the Ingress itself. See [Ingress library — TLS configuration](libraries/ingress.md#tls-configuration) for per-host certificates and the `ingressDefaultHTTPS` toggle.

!!! note "Exact hostnames win over wildcards"
    When a wildcard certificate (`*.example.com`) and an exact-hostname certificate (`app.example.com`) both match the same SNI — registered through separate `spec.tls` entries — HAProxy presents the most specific match: `app.example.com` gets the exact certificate, other subdomains fall to the wildcard. HAPTIC emits both into `certificate-list.txt`; HAProxy's SNI lookup performs this specificity selection regardless of the order the certificates appear in the list. Order only sets the default first-line certificate served for unmatched SNIs and clients that send no SNI.

## Default SSL certificate

### Default behavior (development/testing)

A default install converges out of the box with or without cert-manager:

- **cert-manager installed** (the `cert-manager.io/v1` API is present when Helm renders): the chart creates a self-signed `Issuer` named `<release>-ssl-selfsigned` and a `Certificate` for `localdev.me` and `*.localdev.me`; cert-manager provisions the `default-ssl-cert` Secret and renews it before expiry.
- **cert-manager absent**: the chart generates a self-signed `default-ssl-cert` Secret itself, for the same DNS names. This certificate is valid for 10 years and **isn't** auto-rotated. The Secret survives uninstall and upgrade (`helm.sh/resource-policy: keep`), and the chart only generates it when the Secret doesn't already exist — a Secret you created out-of-band is left untouched.

The `localdev.me` domain resolves to `127.0.0.1`, making it useful for local development. No additional configuration is required:

```bash
helm install my-release oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic --version 0.2.0-alpha.1 \
  --namespace haptic --create-namespace
```

!!! note
    Both default certificates are self-signed and intended for development and testing only. For production, override with your own domain and issuer.

!!! warning "GitOps tools that render without cluster access"
    The no-cert-manager fallback checks for an existing Secret with Helm's `lookup` function, which returns nothing when the chart is rendered without cluster access (`helm template`, Argo CD) — every sync would then generate a fresh certificate. For those deployments, install cert-manager, or provide the certificate explicitly: inline via `defaultSSLCertificate.create`/`cert`/`key` together with `defaultSSLCertificate.certManager.enabled=false`, or as a manually created Secret (see [Alternative: Manual Certificate](#alternative-manual-certificate)). The chart rejects inline creation while cert-manager is enabled because two actors must not own the same Secret.

### Production Deployment

For production, override the default certificate configuration with your actual domain and a trusted issuer:

```yaml
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

### Alternative: Manual certificate

To manage certificates without cert-manager, disable cert-manager integration and create a TLS Secret manually:

```yaml
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

### Custom certificate names

To use a different Secret name or namespace:

```yaml
defaultSSLCertificate:
  secretName: "my-wildcard-cert"
  namespace: "certificates"
```

The controller references the Secret at `certificates/my-wildcard-cert`.

### TLS Secret format

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
defaultSSLCertificate:
  enabled: false
```

### Certificate rotation

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

### SSL troubleshooting

For SSL symptom diagnosis — "Secret not found" errors, HAProxy failing to start with SSL errors, or a certificate that isn't updating — see [Troubleshooting → SSL/TLS Issues](./troubleshooting.md#ssltls-issues).

## HTTP strict transport security (HSTS)

To send the `Strict-Transport-Security` response header on every HTTPS response — across all TLS hosts — enable HSTS in the template engine's `extraContext`:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        tls:
          hsts:
            enabled: true
            maxAge: "31536000"          # one year (default)
            includeSubdomains: false
            preload: false
```

HSTS takes effect only over HTTPS, so pair it with an HTTP-to-HTTPS redirect. The rendered config emits a warning when HSTS is on but no redirect is configured.

This sets the header for every host. To enable HSTS per host instead — or override the global value for specific hosts — use the per-Ingress `hsts` annotations (see [Annotations](annotations.md)). A per-Ingress annotation wins over the global default for its hosts.

## TLS cipher suites and protocol versions

HAPTIC applies one cipher and protocol policy to every HTTPS bind, through the template engine's `extraContext.tls` block. The default is inclusive and forward-secret: a single cipher list spanning the ECDSA, RSA, and DHE families, TLS 1.2 and 1.3, with a TLS 1.2 floor. The common case needs no configuration — the defaults apply out of the box.

To pin or change the policy, set the sub-keys under `extraContext.tls`:

<!-- The cipher/ciphersuite defaults below mirror the chart default in
     charts/haptic/libraries/ssl.yaml (extraContext.tls). Keep them in sync. -->

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        tls:
          # TLS 1.2 cipher list          → ssl-default-bind-ciphers
          ciphers: "ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-AES128-SHA256:ECDHE-RSA-AES128-SHA256:ECDHE-ECDSA-AES128-SHA:ECDHE-RSA-AES128-SHA:ECDHE-ECDSA-AES256-SHA:ECDHE-RSA-AES256-SHA:DHE-RSA-AES128-SHA256:DHE-RSA-AES256-SHA256:DHE-RSA-AES128-SHA:DHE-RSA-AES256-SHA"
          # TLS 1.3 cipher suites         → ssl-default-bind-ciphersuites
          ciphersuites: "TLS_AES_128_GCM_SHA256:TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256"
          # Minimum protocol version      → ssl-default-bind-options ssl-min-ver
          minVersion: "TLSv1.2"
```

You override one sub-key without restating the others — Helm deep-merges your value with the defaults. Set any value to an empty string (`""`) to omit its directive and fall back to HAProxy's built-in default. Per-listener Gateway TLS options still override this policy for their own bind.

### It works with whatever certificate you provide

The default `ciphers` list carries both `ECDHE-ECDSA-*` and `ECDHE-RSA-*` suites, and HAProxy offers only the suites it holds a matching certificate for. So the same policy works unchanged whether your Secret carries an RSA certificate, an ECDSA certificate, or [both](#dual-rsa-and-ecdsa-certificates) — you don't configure the cipher list per certificate type.

### Supporting legacy clients

The default reaches clients back to roughly 2014 (Android 4.4, Java 8, OpenSSL 1.0.1) and keeps forward secrecy for all of them. To also reach older appliances that only support static-RSA key exchange, append the static-RSA suites to `ciphers` and lower `minVersion`:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        tls:
          ciphers: "ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-AES128-SHA256:ECDHE-RSA-AES128-SHA256:ECDHE-ECDSA-AES128-SHA:ECDHE-RSA-AES128-SHA:ECDHE-ECDSA-AES256-SHA:ECDHE-RSA-AES256-SHA:DHE-RSA-AES128-SHA256:DHE-RSA-AES256-SHA256:DHE-RSA-AES128-SHA:DHE-RSA-AES256-SHA:AES128-GCM-SHA256:AES256-GCM-SHA384:AES128-SHA256:AES256-SHA256:AES128-SHA:AES256-SHA"
          minVersion: "TLSv1.0"
```

HAProxy serves clients in cipher-list order, so modern clients still negotiate a forward-secret ECDHE suite. Only clients that can offer nothing better fall to the static-RSA suites, and only those connections lose forward secrecy.

## Dual RSA and ECDSA certificates

You can serve both an ECDSA and an RSA certificate for the same host. HAProxy presents the ECDSA certificate to clients that support it — a smaller, faster handshake — and falls back to the RSA certificate for older clients. It selects per connection from the client's capabilities, so you don't choose which to serve; you provide both.

Provide two `kubernetes.io/tls` Secrets for the host — one ECDSA, one RSA — and reference both from the Ingress with two `spec.tls` entries for the same host. HAPTIC writes every certificate a host references into HAProxy's certificate list under that host's SNI.

### With cert-manager

Create two Certificate resources for the same DNS names, one per key algorithm:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: app-ecdsa
  namespace: my-app
spec:
  secretName: app-tls-ecdsa
  dnsNames: ["app.example.com"]
  privateKey:
    algorithm: ECDSA
    size: 256
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: app-rsa
  namespace: my-app
spec:
  secretName: app-tls-rsa
  dnsNames: ["app.example.com"]
  privateKey:
    algorithm: RSA
    size: 2048
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
```

Reference both Secrets from the Ingress:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: app
  namespace: my-app
spec:
  ingressClassName: haproxy
  tls:
    - hosts: ["app.example.com"]
      secretName: app-tls-ecdsa
    - hosts: ["app.example.com"]
      secretName: app-tls-rsa
  rules:
    - host: app.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: app
                port:
                  number: 80
```

HAProxy then serves the ECDSA or RSA certificate per client. A single-algorithm setup needs only one Certificate and one `spec.tls` entry — the cipher policy [works with whatever you provide](#it-works-with-whatever-certificate-you-provide).

!!! note "How HAProxy selects the certificate"
    HAProxy can also auto-select from a directory of certificates by reading each certificate's Subject Alternative Name (SAN). HAPTIC instead builds an explicit certificate list from your Ingress `spec.tls` entries: the hostnames you route are the source of truth — they can differ from a certificate's SAN, such as a wildcard certificate serving an exact host — and the list carries per-certificate OCSP-stapling and client-certificate options a bare directory can't. The RSA/ECDSA auto-selection is identical either way; it's a HAProxy handshake behavior, not a property of how the certificates are loaded.

## TLS session resumption

TLS session resumption lets a returning client skip the full handshake and reconnect with an abbreviated one — one fewer round trip and no repeated asymmetric crypto. HAProxy does this with *session tickets*: it encrypts the session state into a ticket the client presents on its next connection.

A ticket only helps if the pod that receives it can decrypt it. HAPTIC runs an active-active HAProxy fleet, and a client's reconnect can land on any pod, so if each pod used its own random ticket key, resumption would fail whenever a client hit a different pod than the one that issued its ticket. HAPTIC instead gives every pod the same session-ticket encryption key (STEK), so a ticket issued by one pod resumes on any other. This covers both TLS 1.2 (RFC 5077 tickets) and TLS 1.3 (RFC 8446 pre-shared keys).

Session resumption is off by default. Enable it under the same `extraContext.tls` block as the cipher policy and HSTS:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        tls:
          sessionTickets:
            enabled: true
```

### Key rotation

A long-lived ticket key weakens forward secrecy: an attacker who later obtains it can decrypt every past session it protected. HAPTIC rotates the key daily and keeps a sliding window of three keys — the newest encrypts new tickets, the older two still decrypt tickets they issued, so tickets stay resumable for about two days after issue. Rotation is automatic and needs no external component: the controller renders the key file, reads back its own previous output on the next render to tell whether a day has passed, and slides the window forward with one hitless HAProxy reload. Keys are full-entropy random values generated in the cluster — nothing derives them from a static secret.

You don't manage, rotate, or back up the keys; the toggle is the only configuration.

## Webhook certificates

The admission webhook requires TLS certificates. By default the chart generates a self-signed certificate itself — no cert-manager required (`controller.webhook.certManager.enabled` is `false`):

```yaml
controller:
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
controller:
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
controller:
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
controller:
  webhook:
    certManager:
      enabled: false
    caBundle: "LS0tLS1CRUdJTi..."  # Base64-encoded CA certificate
```

## See also

- [Security](./operations/security.md) — webhook hardening, RBAC, and network exposure
- [Troubleshooting](./troubleshooting.md) — SSL symptom diagnosis and general debugging
