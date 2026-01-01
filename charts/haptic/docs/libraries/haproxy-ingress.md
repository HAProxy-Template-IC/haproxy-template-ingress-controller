# HAProxy Ingress Library

The HAProxy Ingress library provides compatibility with the [HAProxy Ingress Controller](https://haproxy-ingress.github.io/) annotations for Kubernetes Ingress resources.

## Overview

This library enables `haproxy-ingress.github.io/*` annotations on Ingress resources, providing a migration path for users coming from the HAProxy Ingress Controller. It supports path matching, backend configuration, session affinity, SSL features, access control, and more.

This library is enabled by default.

## Configuration

```yaml
controller:
  templateLibraries:
    haproxyIngress:
      enabled: true  # Enabled by default
```

## Extension Points

### Extension Points Used

The HAProxy Ingress library implements these extension points:

| Extension Point | This Library's Snippets | What They Generate |
|-----------------|-------------------------|-------------------|
| Path Regex Map | `map-path-regex-600-haproxy-ingress` | Regex path map entries |
| Path Exact Map | `map-path-exact-600-haproxy-ingress` | Exact path map entries |
| Path Prefix Map | `map-path-prefix-600-haproxy-ingress` | Prefix path map entries |
| Path Prefix Map | `map-path-prefix-600-haproxy-ingress-begin` | Begin path map entries |
| Backend Directives | `backend-directives-600-haproxy-ingress-*` | Timeouts, load balancing, health checks |
| Backends | `backends-600-haproxy-ingress-affinity` | Session affinity configuration |
| Frontend Filters | `frontend-filters-600-haproxy-ingress-*` | Access control, redirects, CORS, headers |
| Features | `features-600-haproxy-ingress-ssl-passthrough` | SSL passthrough backends |
| Global Top | `global-top-600-haproxy-ingress-userlist` | Userlist definitions for basic auth |

---

## Path Matching

### haproxy-ingress.github.io/path-type

**Status**: ✅ Supported

**Description**: Controls how path matching is performed for paths with `pathType: ImplementationSpecific`.

**Valid values**: `regex`, `exact`, `prefix`, `begin`

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: path-matching-example
  annotations:
    haproxy-ingress.github.io/path-type: "regex"
spec:
  ingressClassName: haproxy
  rules:
    - host: api.example.com
      http:
        paths:
          - path: "^/api/v[0-9]+/users/[0-9]+$"
            pathType: ImplementationSpecific
            backend:
              service:
                name: users-service
                port:
                  number: 80
```

**Path type behaviors**:

| Value | Behavior | Example Path | Matches |
|-------|----------|--------------|---------|
| `regex` | Regular expression matching | `^/api/v[0-9]+/` | `/api/v1/`, `/api/v2/users` |
| `exact` | Exact string match | `/api/users` | Only `/api/users` |
| `prefix` | Path prefix (with segment boundaries) | `/api/` | `/api/`, `/api/users`, `/api/v1/` |
| `begin` | Legacy prefix (simple string prefix) | `/api` | `/api`, `/api/users`, `/apikey` |

!!! note "Standard Path Types"
    For `pathType: Exact` or `pathType: Prefix`, the annotation is ignored. The annotation only affects `pathType: ImplementationSpecific` paths.

---

## Backend Configuration

### Timeouts

**Status**: ✅ Supported

**Annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `timeout-connect` | Backend connection timeout | - |
| `timeout-server` | Backend response timeout | - |
| `timeout-queue` | Queue wait timeout | - |
| `timeout-tunnel` | Tunnel/WebSocket timeout | - |
| `timeout-http-request` | HTTP request timeout | - |
| `timeout-keep-alive` | Keep-alive timeout | - |

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/timeout-server: "60s"
  haproxy-ingress.github.io/timeout-connect: "10s"
  haproxy-ingress.github.io/timeout-queue: "30s"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    timeout server 60s
    timeout connect 10s
    timeout queue 30s
```

---

### haproxy-ingress.github.io/balance-algorithm

**Status**: ✅ Supported

**Description**: Load balancing algorithm for the backend.

**Valid values**: `roundrobin`, `leastconn`, `source`, `first`, `random`, `static-rr`, `uri`, `url_param`, `hdr`, `rdp-cookie`

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/balance-algorithm: "leastconn"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    balance leastconn
```

---

### Connection Limits

**Status**: ✅ Supported

**Annotations**:

| Annotation | Description |
|------------|-------------|
| `maxconn-server` | Maximum connections per server |
| `maxqueue-server` | Maximum queue size per server |
| `limit-connections` | Backend fullconn limit |

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/maxconn-server: "100"
  haproxy-ingress.github.io/maxqueue-server: "50"
  haproxy-ingress.github.io/limit-connections: "1000"
```

---

### Health Checks

**Status**: ✅ Supported

**Annotations**:

| Annotation | Description |
|------------|-------------|
| `backend-check-interval` | Health check interval (e.g., `5s`) |
| `health-check-uri` | HTTP health check path |
| `health-check-port` | Custom health check port |
| `health-check-fall-count` | Failures before marking down |
| `health-check-rise-count` | Successes before marking up |

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/health-check-uri: "/healthz"
  haproxy-ingress.github.io/backend-check-interval: "5s"
  haproxy-ingress.github.io/health-check-fall-count: "3"
  haproxy-ingress.github.io/health-check-rise-count: "2"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    option httpchk GET /healthz
    server SRV_1 10.0.0.1:8080 check inter 5s fall 3 rise 2
```

---

### haproxy-ingress.github.io/proxy-protocol

**Status**: ✅ Supported

**Description**: Enable PROXY protocol when connecting to backend servers.

**Valid values**: `v1`, `v2`, `v2-ssl`, `v2-ssl-cn`

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/proxy-protocol: "v2"
```

**Generated HAProxy Configuration**:

```haproxy
server SRV_1 10.0.0.1:8080 send-proxy-v2
```

---

### Backend SSL

**Status**: ✅ Supported

**Annotations**:

| Annotation | Description |
|------------|-------------|
| `secure-backends` | Enable SSL to backend (`true`/`false`) |
| `backend-protocol` | Protocol: `h1`, `h2`, `h1-ssl`, `h2-ssl` |
| `secure-sni` | SNI value for backend connection |
| `secure-verify-hostname` | Hostname for certificate verification |
| `secure-verify-ca-secret` | CA secret for backend verification |
| `secure-crt-secret` | Client certificate for mTLS |

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/secure-backends: "true"
  haproxy-ingress.github.io/backend-protocol: "h2"
  haproxy-ingress.github.io/secure-verify-ca-secret: "backend-ca"
  haproxy-ingress.github.io/secure-crt-secret: "client-cert"
```

**Generated HAProxy Configuration**:

```haproxy
server SRV_1 10.0.0.1:8443 ssl alpn h2 ca-file /path/to/ca.pem crt /path/to/client.pem verify required
```

---

### haproxy-ingress.github.io/initial-weight

**Status**: ✅ Supported

**Description**: Initial weight for backend servers (0-256).

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/initial-weight: "100"
```

---

### haproxy-ingress.github.io/config-backend

**Status**: ✅ Supported

**Description**: Raw HAProxy configuration to inject into the backend section.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/config-backend: |
    http-send-name-header X-Backend-Server
    retries 5
```

---

## Session Affinity

### haproxy-ingress.github.io/affinity

**Status**: ✅ Supported

**Description**: Enable cookie-based session affinity.

**Valid values**: `cookie`

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `session-cookie-name` | Cookie name | `INGRESSCOOKIE` |
| `session-cookie-strategy` | `insert`, `rewrite`, `prefix` | `insert` |
| `session-cookie-dynamic` | Use dynamic cookie key | `true` |
| `session-cookie-keywords` | Additional cookie options | - |
| `session-cookie-domain` | Cookie domain | - |
| `session-cookie-same-site` | `None`, `Lax`, `Strict` | - |
| `session-cookie-preserve` | Preserve backend cookies | - |

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/affinity: "cookie"
  haproxy-ingress.github.io/session-cookie-name: "SERVERID"
  haproxy-ingress.github.io/session-cookie-strategy: "insert"
  haproxy-ingress.github.io/session-cookie-same-site: "Lax"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    cookie SERVERID insert indirect nocache dynamic attr "SameSite=Lax"
    dynamic-cookie-key <generated-key>
```

---

## Access Control

### haproxy-ingress.github.io/allowlist-source-range

**Status**: ✅ Supported

**Description**: Comma-separated list of CIDRs allowed to access this ingress.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/allowlist-source-range: "10.0.0.0/8, 192.168.0.0/16"
```

**Generated HAProxy Configuration**:

```haproxy
http-request deny unless { src 10.0.0.0/8 } or { src 192.168.0.0/16 }
```

---

### haproxy-ingress.github.io/denylist-source-range

**Status**: ✅ Supported

**Description**: Comma-separated list of CIDRs denied access to this ingress.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/denylist-source-range: "203.0.113.0/24"
```

---

## Redirects

### haproxy-ingress.github.io/ssl-redirect

**Status**: ✅ Supported

**Description**: Redirect HTTP requests to HTTPS.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `ssl-redirect` | Enable SSL redirect | - |
| `ssl-redirect-code` | HTTP status code | `302` |

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/ssl-redirect: "true"
  haproxy-ingress.github.io/ssl-redirect-code: "301"
```

---

### haproxy-ingress.github.io/app-root

**Status**: ✅ Supported

**Description**: Redirect requests to root path (`/`) to the specified path.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/app-root: "/dashboard"
```

---

### haproxy-ingress.github.io/redirect-to

**Status**: ✅ Supported

**Description**: Redirect all requests to the specified URL.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `redirect-to` | Target URL | - |
| `redirect-to-code` | HTTP status code | `302` |

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/redirect-to: "https://new.example.com"
  haproxy-ingress.github.io/redirect-to-code: "301"
```

---

## HSTS

### haproxy-ingress.github.io/hsts

**Status**: ✅ Supported

**Description**: Enable HTTP Strict Transport Security headers.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `hsts` | Enable HSTS | - |
| `hsts-max-age` | Max-age in seconds | `15768000` |
| `hsts-include-subdomains` | Include subdomains | - |
| `hsts-preload` | Enable preload | - |

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/hsts: "true"
  haproxy-ingress.github.io/hsts-max-age: "31536000"
  haproxy-ingress.github.io/hsts-include-subdomains: "true"
  haproxy-ingress.github.io/hsts-preload: "true"
```

**Generated HAProxy Configuration**:

```haproxy
http-response set-header Strict-Transport-Security "max-age=31536000; includeSubDomains; preload"
```

---

## CORS

### haproxy-ingress.github.io/cors-enable

**Status**: ✅ Supported

**Description**: Enable CORS handling for the ingress.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `cors-enable` | Enable CORS | - |
| `cors-allow-origin` | Allowed origins | `*` |
| `cors-allow-methods` | Allowed methods | `GET, PUT, POST, DELETE, PATCH, OPTIONS` |
| `cors-allow-headers` | Allowed headers | Common headers |
| `cors-allow-credentials` | Allow credentials | - |
| `cors-expose-headers` | Exposed headers | - |
| `cors-max-age` | Preflight cache time | `86400` |

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/cors-enable: "true"
  haproxy-ingress.github.io/cors-allow-origin: "https://example.com"
  haproxy-ingress.github.io/cors-allow-credentials: "true"
```

---

## Headers

### haproxy-ingress.github.io/forwardfor

**Status**: ✅ Supported

**Description**: Configure X-Forwarded-For header handling.

**Valid values**: `add`, `update`, `ignore`, `ifmissing`

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/forwardfor: "add"
```

---

### haproxy-ingress.github.io/headers

**Status**: ✅ Supported

**Description**: Add request headers. Pipe-separated `name:value` pairs.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/headers: "X-Custom-Header:value|X-Another:test"
```

---

## SSL Features

### haproxy-ingress.github.io/ssl-passthrough

**Status**: ✅ Supported

**Description**: Enable TCP-level SSL passthrough (Layer 4) where HAProxy routes based on SNI without terminating SSL.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ssl-passthrough-example
  annotations:
    haproxy-ingress.github.io/ssl-passthrough: "true"
spec:
  tls:
    - hosts:
        - secure.example.com
  rules:
    - host: secure.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: secure-backend
                port:
                  number: 443
```

**Implementation notes**:

- Uses SNI-based routing in TCP mode
- Backend receives encrypted traffic and terminates SSL
- HTTP-level features (headers, path rewriting) are not available for passthrough traffic
- Mixed passthrough and termination on different hosts is supported

---

## Authentication

### haproxy-ingress.github.io/auth-secret

**Status**: ✅ Supported

**Description**: Enable basic authentication using credentials from a Kubernetes Secret.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `auth-secret` | Secret name (or `namespace/name`) | - |
| `auth-realm` | Authentication realm | `Restricted` |

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/auth-secret: "basic-auth"
  haproxy-ingress.github.io/auth-realm: "Protected Area"
```

**Secret format**:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: basic-auth
type: Opaque
data:
  # Key: username, Value: base64-encoded bcrypt hash (NOT htpasswd format)
  admin: JDJ5JDA1JG1OMVdWazVRbmJnNFF3ZEFkWGJmei44YjNjZUg2UTVLT1ZDS3hSMklrTkFmSmdMaTVwSUtX
```

**Generate password hash**:

```bash
htpasswd -nbB admin mypassword | cut -d: -f2 | base64 -w0
```

---

## Watched Resources

This library does not add additional watched resources. It uses Ingress resources already watched by the [Ingress library](ingress.md).

## Implementation Status Summary

**Total annotations**: 47

- ✅ **Fully Supported**: 47 (100%)
  - Path Matching: 4 values (regex, exact, prefix, begin)
  - Timeouts: 6 annotations
  - Load Balancing: 1 annotation
  - Connection Limits: 3 annotations
  - Health Checks: 5 annotations
  - Backend SSL: 6 annotations
  - PROXY Protocol: 1 annotation
  - Session Affinity: 8 annotations
  - Access Control: 2 annotations
  - SSL Redirect: 2 annotations
  - HSTS: 4 annotations
  - App Root: 1 annotation
  - Redirects: 2 annotations
  - CORS: 7 annotations
  - Headers: 2 annotations
  - SSL Passthrough: 1 annotation
  - Basic Auth: 2 annotations
  - Backend Config: 2 annotations

## See Also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Path matching infrastructure
- [Ingress Library](ingress.md) - Standard Ingress support
- [HAProxyTech Library](haproxytech.md) - `haproxy.org/*` annotations
- [Path Regex Last Library](path-regex-last.md) - Alternative path matching order
- [HAProxy Ingress Documentation](https://haproxy-ingress.github.io/docs/configuration/keys/) - Original annotation reference
