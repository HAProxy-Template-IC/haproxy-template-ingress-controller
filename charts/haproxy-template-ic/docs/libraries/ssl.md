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

Configure the default certificate used when no SNI match is found:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        default_ssl_cert_namespace: haproxy-template-ic
        default_ssl_cert_name: default-ssl-cert
```

The referenced Secret must be of type `kubernetes.io/tls` with `tls.crt` and `tls.key` fields.

## Extension Points

### Extension Points Used

The SSL library implements these extension points from base.yaml:

| Extension Point | This Library's Snippet | What It Generates |
|-----------------|------------------------|-------------------|
| Features | `features-ssl-initialization` | Initializes SSL data structures (priority 50) |
| Features | `features-ssl-crtlist` | Generates CRT-list file (priority 150) |
| Global Top | `global-ssl-ocsp-config` | OCSP stapling configuration |
| Frontends | `frontends-https` | HTTPS frontend with TLS termination |
| Frontends | `frontends-ssl-tcp` | TCP frontend for SSL passthrough (conditional) |
| Backends | `backends-ssl-loopback` | Loopback backend for passthrough architecture |

### Extension Points Provided

The SSL library provides infrastructure for other libraries to register TLS features:

| Data Structure | Purpose | How to Use |
|----------------|---------|------------|
| `global_features.tls_certificates` | Array of TLS certificates to include in CRT-list | Append `{secret_namespace, secret_name, sni_patterns[]}` |
| `global_features.ssl_passthrough_backends` | Array of SSL passthrough backends | Append `{name, sni}` |

**Example - Registering a TLS certificate (from ingress.yaml):**

```jinja2
{%- set cert = {
    "secret_namespace": tls.secretName.split("/")[0] | default(ingress.metadata.namespace),
    "secret_name": tls.secretName.split("/")[-1],
    "sni_patterns": tls.hosts
} %}
{%- set global_features.tls_certificates = global_features.tls_certificates.append(cert) %}
```

## Features

### HTTPS Frontend

The SSL library generates an HTTPS frontend that:

- Binds to port 8443 (configurable via `httpsPort`)
- Uses CRT-list for certificate selection
- Enables HTTP/2 via ALPN negotiation
- Reuses routing logic from base.yaml

```haproxy
frontend https
    mode http
    bind *:8443 ssl crt-list /etc/haproxy/crt-list/certificate-list.txt alpn h2,http/1.1

    # Routing logic (same as HTTP frontend)
    # ...

    use_backend %[var(txn.backend_name)] if { var(txn.backend_name) -m found }
    default_backend default_backend
```

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

Automatic OCSP (Online Certificate Status Protocol) stapling is configured globally:

```haproxy
ocsp-update.mode on
ocsp-update.mindelay 300
ocsp-update.maxdelay 3600
```

This enables HAProxy to automatically fetch and cache OCSP responses for certificate validation.

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
- [HAProxy Annotations Library](haproxytech.md) - SSL passthrough annotation
