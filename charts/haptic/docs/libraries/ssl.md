# SSL Library

The SSL library provides TLS certificate management, HTTPS frontend configuration, and SSL passthrough infrastructure for HAProxy.

## Overview

The SSL library handles all SSL/TLS-related functionality:

- HTTPS frontend with TLS termination
- Dynamic TLS certificate loading from Kubernetes Secrets
- CRT-list generation for SNI-based certificate selection
- OCSP stapling configuration
- SSL passthrough infrastructure (TCP mode for end-to-end encryption)

This library is enabled by default and works in conjunction with resource libraries (ingress, gateway) that register TLS certificates.

## Configuration

```yaml
controller:
  templateLibraries:
    ssl:
      enabled: true  # Enabled by default
```

### Default SSL Certificate

Configure the default certificate used when no SNI match is found via the chart-level values (recommended):

```yaml
controller:
  defaultSSLCertificate:
    secretName: default-ssl-cert
    namespace: haptic            # defaults to the Helm release namespace
```

The chart wires those values into the template engine as `extraContext.default_ssl_cert_name` and `extraContext.default_ssl_cert_namespace`; the SSL library reads them and emits the corresponding `default.pem` entry in `certificate-list.txt`.

The referenced Secret must be of type `kubernetes.io/tls` with `tls.crt` and `tls.key` fields. For the full configuration surface (cert-manager integration, disabling HTTPS, manual certificates) see [SSL Certificates](../ssl-certificates.md).

## Extension Points

### Extension Points Used

The SSL library implements these extension points from base.yaml:

| Extension Point | This Library's Snippet | What It Generates |
|-----------------|------------------------|-------------------|
| `features-*` | `features-050-ssl-initialization` | Initializes shared state (`gf["tlsCertificates"]`, `gf["sslPassthroughBackends"]`) |
| `features-*` | `features-150-ssl-crtlist` | Generates `certificate-list.txt` (runs after resource libraries have registered certs) |
| `features-*` | `features-160-ssl-redirect-map` | Builds the HTTP→HTTPS redirect map consumed by `frontend-filters-050-ssl-redirect` |
| `frontend-filters-*` | `frontend-filters-050-ssl-redirect` | HTTP→HTTPS redirect rules |
| `frontends-*` | `frontends-500-https` | HTTPS frontend with TLS termination |
| `frontends-*` | `frontends-500-ssl-tcp` | TCP frontend for SSL passthrough (conditional on registered passthrough backends) |
| `backends-*` | `backends-500-ssl-loopback` | Loopback backend that forwards passthrough-decrypted traffic from the TCP frontend to the HTTPS frontend |

Snippet names reflect their real numeric-prefix values in `libraries/ssl.yaml`; lower-numbered `features-050-*` snippets run before higher-numbered `features-150-*` ones, which is how SSL initializes shared state before resource libraries populate it and before the CRT-list is emitted.

### Extension Points Provided

The SSL library provides infrastructure for other libraries to register TLS features:

| Data Structure | Purpose | How to Use |
|----------------|---------|------------|
| `gf["tlsCertificates"]` | Array of TLS certificates to include in CRT-list | Append `{secret_namespace, secret_name, sni_patterns[]}` |
| `gf["sslPassthroughBackends"]` | Array of SSL passthrough backends | Append `{name, sni}` |

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

### HTTPS Frontend

The SSL library generates an HTTPS frontend that:

- Binds to port 8443 (override via `controller.config.templatingSettings.extraContext.httpsPort`). The bind port and the HAProxy container port (`haproxy.ports.https`) both default to 8443, but they are **not** linked — the chart does not derive one from the other, so if you change one you must set the other explicitly to keep them aligned.
- Uses CRT-list for certificate selection
- Enables HTTP/2 via ALPN negotiation
- Reuses routing logic from base.yaml

```haproxy
frontend https
    mode http
    bind *:8443 ssl crt-list general/certificate-list.txt alpn h2,http/1.1

    # Routing logic (same as HTTP frontend)
    # ...

    use_backend %[var(txn.backend_name)] if { var(txn.backend_name) -m found }
    default_backend default_backend
```

The `general/` prefix is the basename of `dataplane.generalStorageDir` (chart default `/etc/haproxy/general`); HAProxy resolves it via the `default-path origin` directive in the global section.

### CRT-List Certificate Management

TLS certificates are managed via HAProxy's crt-list feature:

1. Resource libraries (ingress, gateway) register TLS Secrets with their SNI patterns
2. SSL library generates `certificate-list.txt` with all registered certificates
3. Each certificate has OCSP stapling enabled

**CRT-list format:**

```
namespace_secretname.pem [ocsp-update on] host1.example.com host2.example.com
default.pem [ocsp-update on]
```

### OCSP Stapling

Every certificate line in the generated `certificate-list.txt` is emitted with the `[ocsp-update on]` option, which instructs HAProxy to fetch and cache OCSP responses for that certificate:

```
namespace_secretname.pem [ocsp-update on] host1.example.com host2.example.com
default.pem [ocsp-update on]
```

There is no separate global OCSP configuration — the per-certificate option is all that's needed with HAProxy 3.0+.

### SSL Passthrough

When resource libraries register SSL passthrough backends (via `haproxy.org/ssl-passthrough: "true"` annotation), the SSL library generates a dual-frontend architecture:

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
4. Routes termination traffic to unix socket → HTTPS frontend

## Watched Resources

| Resource | API Version | Purpose |
|----------|-------------|---------|
| Secrets | v1 | Load TLS certificates (`kubernetes.io/tls` type) |

## Validation Tests

The SSL library includes these validation tests:

| Test | Description |
|------|-------------|
| `test-ssl-certificate-loading` | Verifies default SSL certificate loads correctly |
| `test-ssl-https-frontend-basic` | Verifies HTTPS frontend with SSL bind options |
| `test-ssl-crtlist-basic` | Verifies CRT-list generation with OCSP configuration |

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

## See Also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Core configuration infrastructure
- [Ingress Library](ingress.md) - Ingress TLS configuration
- [haproxytech library](haproxytech.md) - SSL passthrough annotation
