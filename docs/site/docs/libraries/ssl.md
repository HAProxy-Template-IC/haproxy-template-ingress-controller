# SSL library

The SSL library provides TLS certificate management, HTTPS frontend configuration, and SSL passthrough infrastructure for HAProxy.

## Overview

The SSL library provides:

- HTTPS frontend with TLS termination
- Dynamic TLS certificate loading from Kubernetes Secrets
- CRT-list generation for SNI-based certificate selection
- Online Certificate Status Protocol (OCSP) stapling configuration
- SSL passthrough infrastructure (TCP mode for end-to-end encryption)

This library is enabled by default and works in conjunction with resource libraries (ingress, gateway) that register TLS certificates.

Explore the decoded certificates the SSL library assembles into the crt-list, live:

<div class="pg-embed" markdown data-scenario="all" data-facade="spec.templateSnippets.util-haproxytech-ssl-passthrough" data-tab="certs" data-controls="tabs,resources" data-title="SSL library → decoded certificates" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, give the `shop` Ingress the annotation `haproxy.org/ssl-passthrough: "true"` (add an `annotations:` block under its `metadata:`), then open the **haproxy.cfg** tab and watch a new `frontend ssl-tcp` appear alongside a `backend ssl-passthrough-storefront-shop`.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

The annotation registers `shop.example.com` as an SSL-passthrough backend, so the shared `sslPassthroughBackends` list becomes non-empty. That flips `gf["bindHTTPSDefault"]` on (`features-140-ssl-passthrough-binds`), which lets `frontends-500-ssl-tcp` emit a `mode tcp` frontend bound to the HTTPS port. That frontend reads the SNI without decrypting and routes it with `use_backend ssl-passthrough-storefront-shop if { req_ssl_sni -m str shop.example.com }`; the matching `backend ssl-passthrough-storefront-shop` (also `mode tcp`) forwards the still-encrypted stream straight to the shop pods. The **certs** tab is unchanged — passthrough never terminates TLS, so no certificate is loaded for it.

</details>

</div>

## Configuration

```yaml
controller:
  templateLibraries:
    ssl:
      enabled: true  # Enabled by default
```

### Default SSL certificate

Configure the default certificate used when no SNI match is found via the chart-level values (recommended):

```yaml
controller:
  defaultSSLCertificate:
    secretName: default-ssl-cert
    namespace: haptic            # defaults to the Helm release namespace
```

The chart wires those values into the template engine as `extraContext.default_ssl_cert_name` and `extraContext.default_ssl_cert_namespace`; the SSL library reads them and emits the corresponding `default.pem` entry in `certificate-list.txt`.

The referenced Secret must be of type `kubernetes.io/tls` with `tls.crt` and `tls.key` fields. For the full configuration surface (cert-manager integration, disabling HTTPS, manual certificates) see [SSL Certificates](../ssl-certificates.md).

## Extension points

### Extension points used

The SSL library implements these extension points from base.yaml:

| Extension Point | This Library's Snippet | What It Generates |
|-----------------|------------------------|-------------------|
| `features-*` | `features-050-ssl-initialization` | Initializes shared state (`gf["tlsCertificates"]`, `gf["sslPassthroughBackends"]`) |
| `features-*` | `features-140-ssl-passthrough-binds` | Sets `gf["bindHTTPSDefault"]` / `gf["needHTTPSFrontend"]` when passthrough backends are registered (ensures the ssl-tcp frontend binds on the https port even without HTTPS termination) |
| `features-*` | `features-150-ssl-crtlist` | Generates `certificate-list.txt` (runs after resource libraries have registered certs) |
| `features-*` | `features-160-ssl-redirect-map` | Builds the HTTP→HTTPS redirect map consumed by `frontend-filters-050-ssl-redirect` |
| `frontend-filters-*` | `frontend-filters-050-ssl-redirect` | HTTP→HTTPS redirect rules |
| `frontends-*` | `frontends-500-https` | HTTPS frontend with TLS termination |
| `frontends-*` | `frontends-500-ssl-tcp` | TCP frontend for SSL passthrough (conditional on registered passthrough backends) |
| `backends-*` | `backends-500-ssl-loopback` | Loopback backend that forwards TLS-termination traffic (still encrypted) from the TCP frontend to the HTTPS frontend, which decrypts it |

Snippet names reflect their real numeric-prefix values in `libraries/ssl.yaml`; lower-numbered `features-050-*` snippets run before higher-numbered `features-150-*` ones, which is how SSL initializes shared state before resource libraries populate it and before the CRT-list is emitted.

### Extension points provided

The SSL library provides infrastructure for other libraries to register TLS features:

| Data Structure | Purpose | How to Use |
|----------------|---------|------------|
| `gf["tlsCertificates"]` | Array of TLS certificates to include in CRT-list | Append `{secret_namespace, secret_name, sni_patterns[]}` |
| `gf["sslPassthroughBackends"]` | Array of SSL passthrough backends | Append `{name, sni}` |
| `https-bind-extra-*` | Glob extension point inside the HTTPS frontend, *after* the chart-static `bind` line. Resource libraries emit one TLS bind per non-default Gateway HTTPS listener port via this hook. See "Adding HTTPS binds" below. | Provide a snippet matching the glob; render `{{ render "util-ssl-bind-options" }}` to reuse the chart-static SSL options (crt-list + Application-Layer Protocol Negotiation (ALPN)). |

#### Adding HTTPS binds via `https-bind-extra-*`

The HTTPS frontend (`frontends-500-https`) emits its chart-static `bind *:<httpsPort> ssl crt-list ... alpn h2,http/1.1`, then runs `render_glob "https-bind-extra-*"` so resource libraries can contribute additional TLS binds without forking the frontend or duplicating SSL options.

The Gateway library uses this hook to support Gateway listeners on non-default HTTPS ports (for example, `port: 9443`). Its `https-bind-extra-050-gateway-multi-port-bind` snippet:

1. Walks `Gateway.spec.listeners` and admitted `XListenerSet.spec.listeners`.
2. Filters to `protocol: HTTPS`.
3. Skips the chart-static https port (`extraContext.httpsPort`) and the chart-static http port (`extraContext.httpPort`) so duplicate binds don't break HAProxy startup.
4. Emits one `bind *:<port>{{ render "util-ssl-bind-options" }}` per remaining unique port.

Custom libraries that need additional TLS binds (for example, for protocol-specific extensions) follow the same pattern. Always reuse `util-ssl-bind-options` so the SSL handshake behaves identically across all binds — direct use of literal SSL options would drift from chart-static if the operator overrides crt-list path or ALPN settings.

The HTTP frontend has a sibling hook `http-bind-extra-*` for plain-HTTP Gateway listener ports. See the [base library extension points](base.md#extension-points) for the full list.

**Example - Registering a TLS certificate (from ingress.yaml):**

```scriggo
{%- var parts = split(tls.secretName, "/") %}
{%- var cert = map[string]any{
    "secret_namespace": len(parts) > 1 ? parts[0] : ingress.metadata.namespace,
    "secret_name": parts[len(parts)-1],
    "sni_patterns": tls.hosts,
} %}
{%- var certs []any = gf["tlsCertificates"].([]any) %}
{%- gf["tlsCertificates"] = append(certs, cert) %}
```

## Features

### HTTPS frontend

The SSL library generates an HTTPS frontend that:

- Binds to port 443 by default. The chart wires `haproxy.ports.https` through to `extraContext.httpsPort` automatically, so changing one updates the other in lockstep — both the HAProxy container bind and the Service `targetPort` resolve from the same value. Override with either `haproxy.ports.https` (preferred — keeps Service and HAProxy aligned) or `controller.config.templatingSettings.extraContext.httpsPort` (only the bind, leaves Service unchanged; mismatched values mean external traffic to the Service won't reach the HAProxy bind).
- Additional TLS binds for non-default Gateway HTTPS listener ports get appended via the `https-bind-extra-*` extension point (see [Extension Points Provided](#extension-points-provided)).
- Uses CRT-list for certificate selection
- Enables HTTP/2 via ALPN negotiation
- Reuses routing logic from base.yaml

```haproxy
frontend https
    mode http
    bind *:443 ssl crt-list general/certificate-list.txt alpn h2,http/1.1

    # Routing logic (same as HTTP frontend)
    # ...

    use_backend %[var(txn.backend_name)] if { var(txn.backend_name) -m found }
    default_backend default_backend
```

The `general/` prefix is the basename of `dataplane.generalStorageDir` (chart default `/etc/haproxy/general`); HAProxy resolves it via the `default-path origin` directive in the global section.

### CRT-list certificate management

TLS certificates are managed via HAProxy's crt-list feature:

1. Resource libraries (ingress, gateway) register TLS Secrets with their SNI patterns
2. SSL library generates `certificate-list.txt` with all registered certificates
3. Each certificate has OCSP stapling enabled

**CRT-list format:**

```
namespace_secretname.pem [ocsp-update on] host1.example.com host2.example.com
default.pem [ocsp-update on]
```

### OCSP stapling

Every certificate line in the generated `certificate-list.txt` is emitted with the `[ocsp-update on]` option, which instructs HAProxy to fetch and cache OCSP responses for that certificate:

```
namespace_secretname.pem [ocsp-update on] host1.example.com host2.example.com
default.pem [ocsp-update on]
```

There is no separate global OCSP configuration — the per-certificate option is all that's needed with HAProxy 3.0+.

### Restricting frontend TLS versions and ciphers

The client-facing HTTPS listener inherits HAProxy's default cipher list and protocol versions. To harden it, emit `ssl-default-bind-*` directives into the `global` section through a [`global-settings-*` extension point](base.md#extension-points). These directives set the defaults for every frontend `bind ... ssl` line:

```yaml
controller:
  config:
    templateSnippets:
      global-settings-400-tls-hardening:
        template: |
          ssl-default-bind-ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305
          ssl-default-bind-ciphersuites TLS_AES_128_GCM_SHA256:TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256
          ssl-default-bind-options ssl-min-ver TLSv1.2
```

- `ssl-default-bind-ciphers` sets the cipher list for TLS 1.2 and below.
- `ssl-default-bind-ciphersuites` sets the cipher suites for TLS 1.3.
- `ssl-default-bind-options ssl-min-ver TLSv1.2` rejects handshakes below TLS 1.2.

The bundled community HAProxy images use AWS-LC, so HAPTIC doesn't emit
`tune.ssl.default-dh-param`: HAProxy doesn't support that setting with AWS-LC
and warns that the directive was ignored. If a custom OpenSSL build must
support finite-field Diffie-Hellman-only clients, add `ssl-dh-param-file` (preferred) or
`tune.ssl.default-dh-param` in another `global-settings-*` snippet.
Elliptic-curve Diffie-Hellman and TLS 1.3 key exchange are unaffected.

!!! warning "These harden the client-facing listener, not the backend"
    `ssl-default-bind-*` directives apply to the frontend — traffic between clients and HAProxy. They don't affect TLS between HAProxy and your upstream pods. Backend TLS ciphers and versions are set per route with annotations instead: the default [haptic-annotations](haptic-annotations.md) library exposes `haproxy-haptic.org/backend-ciphers` / `backend-ciphersuites` / `backend-ssl-protocols`; the opt-in vendor libraries name the same thing as `nginx.ingress.kubernetes.io/proxy-ssl-ciphers` / `proxy-ssl-protocols` or haproxy-ingress `ssl-ciphers-backend`.

### SSL passthrough

When resource libraries register SSL passthrough backends (via the default `haproxy-haptic.org/ssl-passthrough: "true"` annotation, or a vendor equivalent such as `haproxy.org/ssl-passthrough`), the SSL library generates a dual-frontend architecture:

```
                           ┌─────────────────────┐
                           │   ssl-tcp frontend  │
                           │   (mode tcp :443)   │
                           └──────────┬──────────┘
                                      │
                    ┌─────────────────┴─────────────────┐
                    │                                   │
                    ▼                                   ▼
         ┌──────────────────┐               ┌──────────────────┐
         │ SSL Passthrough  │               │  ssl-loopback    │
         │    Backend       │               │    backend       │
         │  (TCP to pods)   │               │ (unix socket)    │
         └──────────────────┘               └────────┬─────────┘
                                                     │
                                                     ▼
                                           ┌──────────────────┐
                                           │  https frontend  │
                                           │  (SSL termination│
                                           │   on unix sock)  │
                                           └──────────────────┘
```

**How it works:**

1. TCP frontend receives all port 443 traffic
2. Extracts SNI (Server Name Indication) without terminating TLS
3. Routes passthrough traffic directly to backend pods (TCP mode)
4. Routes termination traffic to Unix socket → HTTPS frontend

## Watched resources

| Resource | API Version | Purpose |
|----------|-------------|---------|
| Secrets | v1 | Load TLS certificates (`kubernetes.io/tls` type) |

## Validation tests

The SSL library includes these validation tests:

| Test | Description |
|------|-------------|
| `test-ssl-certificate-loading` | Verifies default SSL certificate loads correctly |
| `test-ssl-https-frontend-basic` | Verifies HTTPS frontend with SSL bind options |
| `test-ssl-crtlist-basic` | Verifies CRT-list generation with OCSP configuration |
| `test-ssl-certificate-dots-in-name` | Secret names containing dots are encoded correctly in the PEM filename |
| `test-ssl-certificate-custom-namespace` | TLS secret in a non-default namespace is loaded and referenced correctly |

Run tests with:

```bash
./scripts/test-templates.sh --test test-ssl-https-frontend-basic
```

## Requirements

!!! warning "HAProxy 3.0+ Required"
    The SSL library requires HAProxy 3.0 or newer for:

    - CRT-list certificate management
    - OCSP stapling (`ocsp-update` directive)
    - HTTP/2 ALPN negotiation

## See also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Core configuration infrastructure
- [Ingress Library](ingress.md) - Ingress TLS configuration
- [haproxytech library](haproxytech.md) - SSL passthrough annotation
