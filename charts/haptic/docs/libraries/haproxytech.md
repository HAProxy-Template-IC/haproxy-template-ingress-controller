# haproxytech Library

The haproxytech library implements `haproxy.org/*` annotations compatible with [haproxytech/kubernetes-ingress](https://github.com/haproxytech/kubernetes-ingress), the official HAProxy ingress controller by HAProxy Technologies. This enables fine-grained control over HAProxy behavior through Kubernetes resource annotations.

## Overview

Supported features:

- Basic authentication
- SSL/TLS redirection
- CORS configuration
- Rate limiting
- Session persistence
- Path rewriting
- Header manipulation
- Load balancing algorithms
- Health check configuration
- Timeout customization

This library is enabled by default.

!!! note "Migrating from haproxytech/kubernetes-ingress"
    If you are migrating from the official HAProxy Technologies ingress controller, your existing `haproxy.org/*` annotations work without changes. See [Annotations](../annotations.md) for the full feature comparison between annotation libraries.

**Important notes:**

- Annotations apply to **Ingress resources only** (not Services)
- Gateway API resources (HTTPRoute, GRPCRoute) use filters instead of annotations - see [Gateway API Library](gateway.md)
- All annotations use the `haproxy.org/` prefix
- Multiple ingresses can share the same annotation values (deduplication is handled automatically)

## Configuration

```yaml
controller:
  templateLibraries:
    haproxytech:
      enabled: true  # Enabled by default
```

## Extension Points

The haproxytech library implements these extension points from base.yaml. All snippets follow the `<extension-point>-<NNN>-haproxytech-*` naming convention, where `NNN` is the numeric priority (see [Template Libraries → Snippet Priority](../template-libraries.md#snippet-priority)).

### features-* (shared-state initialization)

| Snippet | Purpose |
|---------|---------|
| `features-100-haproxytech-ssl-redirect` | Registers SSL-redirect host/code pairs in `gf["sslRedirectHosts"]` |
| `features-100-haproxytech-ssl-passthrough` | Scans ingresses for `haproxy.org/ssl-passthrough` and registers backends in `gf["sslPassthroughBackends"]` |

### frontend-filters-* (HTTP-frontend request/response filters)

| Snippet | Annotations Processed |
|---------|----------------------|
| `frontend-filters-100-haproxytech-basic-headers` | `haproxy.org/forwarded-for`, `haproxy.org/src-ip-header` |
| `frontend-filters-200-haproxytech-access-control` | `haproxy.org/allowlist`, `haproxy.org/denylist` |
| `frontend-filters-300-haproxytech-cors` | `haproxy.org/cors-*` |
| `features-130-haproxytech-request-redirect` | `haproxy.org/request-redirect`, `haproxy.org/request-redirect-code` (registers host→location in the shared `redirect-loc-<code>.map`) |
| `frontend-filters-500-haproxytech-logging` | `haproxy.org/request-capture`, `haproxy.org/request-capture-len` |

### backend-directives-* (per-backend directives)

| Snippet | Annotations Processed |
|---------|----------------------|
| `backend-directives-100-haproxytech-pod-maxconn` | `haproxy.org/pod-maxconn` |
| `backend-directives-100-haproxytech-timeouts` | `haproxy.org/timeout-server`, `/timeout-connect`, `/timeout-queue`, `/timeout-tunnel`, `/timeout-check` |
| `backend-directives-150-haproxytech-load-balance` | `haproxy.org/load-balance` |
| `backend-directives-200-haproxytech-health-checks` | `haproxy.org/check` |
| `backend-directives-210-haproxytech-advanced-health-checks` | `haproxy.org/check-http`, `haproxy.org/check-interval` |
| `backend-directives-250-haproxytech-rate-limiting` | `haproxy.org/rate-limit-*` |
| `map-reqhdr-host-250-haproxytech` | `haproxy.org/set-host` (relocated to reqhdr-host.map + shared frontend rule) |
| `backend-directives-300-haproxytech-header-manipulation` | `haproxy.org/request-set-header`, `haproxy.org/response-set-header` |
| `backend-directives-350-haproxytech-path-rewrite` | `haproxy.org/path-rewrite` |
| `backend-directives-400-haproxytech-session-persistence` | `haproxy.org/cookie-persistence` |
| `backend-directives-401-haproxytech-session-persistence-no-dynamic` | `haproxy.org/cookie-persistence-no-dynamic` |
| `backend-directives-500-haproxytech-ingress-auth` | `haproxy.org/auth-*` (attaches the userlist per backend) |
| `backend-directives-900-haproxytech-advanced` | `haproxy.org/backend-config-snippet`, `haproxy.org/server-*`, `haproxy.org/send-proxy-protocol`, `haproxy.org/scale-server-slots` |

### Other extension points

| Snippet | Extension Point | Purpose |
|---------|-----------------|---------|
| `global-top-500-haproxytech-ingress-auth` | `global-top-*` | Emits a deduplicated `userlist auth_<secretNs>_<secretName>` per unique auth secret |
| `backends-501-haproxytech-ssl-passthrough` | `backends-*` | TCP-mode backends for hosts annotated with `haproxy.org/ssl-passthrough: "true"` |

### Injecting Custom Annotations

You can extend annotation processing by adding snippets with the right prefix and priority:

```yaml
controller:
  config:
    templateSnippets:
      # Runs alongside the built-in frontend filters (before the 200-range access-control)
      frontend-filters-150-custom-security:
        template: |
          {%- for ingress in resources.ingresses.List() %}
          {%- var security_level = ingress.metadata.annotations["custom.io/security-level"] | fallback("") %}
          {%- if security_level == "high" %}
          http-request deny unless { ssl_fc }
          {%- end %}
          {%- end %}
```

## Access Control & IP Filtering

### haproxy.org/allowlist

**Status**: ✅ Supported

**Description**: Whitelist IP addresses or CIDR ranges that are allowed to access the ingress.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: protected-api
  annotations:
    haproxy.org/allowlist: "192.168.1.0/24, 10.0.0.1"
spec:
  rules:
    - host: api.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: api-service
                port:
                  number: 8080
```

**Generated HAProxy Configuration**:

```haproxy
# Frontend ACL
acl allowlist_192_168_1_0_24 src 192.168.1.0/24
acl allowlist_10_0_0_1 src 10.0.0.1
http-request deny if !allowlist_192_168_1_0_24 !allowlist_10_0_0_1
```

**Dependencies**: None

**Related annotations**: Can be combined with `denylist`

---

### haproxy.org/denylist

**Status**: ✅ Supported

**Description**: Blacklist IP addresses or CIDR ranges that are denied access to the ingress.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: public-api
  annotations:
    haproxy.org/denylist: "203.0.113.0/24, 198.51.100.50"
spec:
  rules:
    - host: api.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: api-service
                port:
                  number: 8080
```

**Generated HAProxy Configuration**:

```haproxy
# Frontend ACL
acl denylist_203_0_113_0_24 src 203.0.113.0/24
acl denylist_198_51_100_50 src 198.51.100.50
http-request deny if denylist_203_0_113_0_24 or denylist_198_51_100_50
```

**Dependencies**: None

**Related annotations**: Can be combined with `allowlist`

---

### haproxy.org/whitelist

**Status**: ❌ Not Implemented (Deprecated)

**Description**: Legacy name for `allowlist`. Prefer using `allowlist` instead.

**Note**: Deprecated in favor of `allowlist`. Not recommended for new implementations.

---

### haproxy.org/blacklist

**Status**: ❌ Not Implemented (Deprecated)

**Description**: Legacy name for `denylist`. Prefer using `denylist` instead.

**Note**: Deprecated in favor of `denylist`. Not recommended for new implementations.

---

## CORS Configuration

### haproxy.org/cors-enable

**Status**: ✅ Supported

**Description**: Enable CORS (Cross-Origin Resource Sharing) processing for the ingress.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: cors-api
  annotations:
    haproxy.org/cors-enable: "true"
    haproxy.org/cors-allow-origin: "*"
    haproxy.org/cors-allow-methods: "GET, POST, PUT, DELETE"
    haproxy.org/cors-allow-headers: "Content-Type, Authorization"
spec:
  rules:
    - host: api.example.com
      http:
        paths:
          - path: /api
            pathType: Prefix
            backend:
              service:
                name: api-service
                port:
                  number: 8080
```

**Generated HAProxy Configuration**:

```haproxy
# Capture Origin header
http-request capture req.hdr(Origin) len 128

# Set CORS headers
http-response set-header Access-Control-Allow-Origin %[capture.req.hdr(0)]
http-response set-header Access-Control-Allow-Methods "GET, POST, PUT, DELETE"
http-response set-header Access-Control-Allow-Headers "Content-Type, Authorization"
```

**Dependencies**: All other `cors-*` annotations require `cors-enable: "true"`

---

### haproxy.org/cors-allow-origin

**Status**: ✅ Supported

**Description**: Specifies allowed origins for CORS requests. Supports wildcard (`*`), exact URL, or regex pattern.

**Usage**:

```yaml
# Wildcard (allow all origins)
haproxy.org/cors-allow-origin: "*"

# Exact match
haproxy.org/cors-allow-origin: "https://example.com"

# Regex pattern
haproxy.org/cors-allow-origin: "^https://(.+\\.)?(example\\.com)(:\\d{1,5})?$"
```

**Generated HAProxy Configuration**:

```haproxy
# Wildcard
http-response set-header Access-Control-Allow-Origin "*"

# Exact or regex match
http-response set-header Access-Control-Allow-Origin %[capture.req.hdr(0)] if { capture.req.hdr(0) -m reg ^https://(.+\.)?(example\.com)(:\d{1,5})?$ }
```

**Dependencies**: Requires `cors-enable: "true"`

---

### haproxy.org/cors-allow-methods

**Status**: ✅ Supported

**Description**: Specifies allowed HTTP methods for CORS requests.

**Valid values**: GET, POST, PUT, DELETE, HEAD, CONNECT, OPTIONS, TRACE, PATCH

**Usage**:

```yaml
haproxy.org/cors-allow-methods: "GET, POST, PUT, DELETE, OPTIONS"
```

**Generated HAProxy Configuration**:

```haproxy
http-response set-header Access-Control-Allow-Methods "GET, POST, PUT, DELETE, OPTIONS"
```

**Dependencies**: Requires `cors-enable: "true"`

---

### haproxy.org/cors-allow-headers

**Status**: ✅ Supported

**Description**: Specifies allowed request headers for CORS requests.

**Usage**:

```yaml
haproxy.org/cors-allow-headers: "Content-Type, Authorization, X-Requested-With"
```

**Generated HAProxy Configuration**:

```haproxy
http-response set-header Access-Control-Allow-Headers "Content-Type, Authorization, X-Requested-With"
```

**Dependencies**: Requires `cors-enable: "true"`

---

### haproxy.org/cors-allow-credentials

**Status**: ✅ Supported

**Description**: Indicates whether credentials (cookies, authorization headers) can be included in CORS requests.

**Usage**:

```yaml
haproxy.org/cors-allow-credentials: "true"
```

**Generated HAProxy Configuration**:

```haproxy
http-response set-header Access-Control-Allow-Credentials "true"
```

**Dependencies**: Requires `cors-enable: "true"`

**Note**: When `cors-allow-credentials: "true"`, `cors-allow-origin` cannot be `*` (must be specific origin)

---

### haproxy.org/cors-max-age

**Status**: ✅ Supported

**Description**: Specifies how long (in seconds) preflight request results can be cached.

**Usage**:

```yaml
haproxy.org/cors-max-age: "3600"  # 1 hour
```

**Generated HAProxy Configuration**:

```haproxy
http-response set-header Access-Control-Max-Age "3600"
```

**Dependencies**: Requires `cors-enable: "true"`

---

## Rate Limiting

### haproxy.org/rate-limit-requests

**Status**: ✅ Supported

**Description**: Maximum number of requests allowed in the specified time period (per source IP).

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: rate-limited-api
  annotations:
    haproxy.org/rate-limit-requests: "100"
    haproxy.org/rate-limit-period: "1m"
    haproxy.org/rate-limit-size: "100k"
    haproxy.org/rate-limit-status-code: "429"
spec:
  rules:
    - host: api.example.com
      http:
        paths:
          - path: /api
            pathType: Prefix
            backend:
              service:
                name: api-service
                port:
                  number: 8080
```

**Generated HAProxy Configuration**:

```haproxy
backend api-backend
    stick-table type ip size 100k expire 1m store http_req_rate(1m)
    http-request track-sc0 src
    http-request deny deny_status 429 if { sc_http_req_rate(0) gt 100 }
```

**Dependencies**: Other rate-limit annotations require this to be set

**Related annotations**: `rate-limit-period`, `rate-limit-size`, `rate-limit-status-code`

---

### haproxy.org/rate-limit-period

**Status**: ✅ Supported

**Description**: Time window for rate limiting. Supports duration format (e.g., `10s`, `1m`, `1h`).

**Default**: `1s` (1 second)

**Usage**:

```yaml
haproxy.org/rate-limit-period: "1m"
```

**Dependencies**: Requires `rate-limit-requests` to be set

---

### haproxy.org/rate-limit-size

**Status**: ✅ Supported

**Description**: Size of the stick-table used to track client IPs. Supports suffixes `k` (thousands) or `M` (millions).

**Default**: `100k` (100,000 entries)

**Usage**:

```yaml
haproxy.org/rate-limit-size: "100k"  # Track 100,000 IPs
haproxy.org/rate-limit-size: "1000000"  # Track 1 million IPs
```

**Dependencies**: Requires `rate-limit-requests` to be set

---

### haproxy.org/rate-limit-status-code

**Status**: ✅ Supported

**Description**: HTTP status code to return when rate limit is exceeded.

**Default**: `403` (Forbidden)

**Common values**: `403`, `429` (Too Many Requests), `503` (Service Unavailable)

**Usage**:

```yaml
haproxy.org/rate-limit-status-code: "429"
```

**Dependencies**: Requires `rate-limit-requests` to be set

---

## Request/Response Header Manipulation

### haproxy.org/request-set-header

**Status**: ✅ Supported

**Description**: Set or modify request headers before forwarding to backend. Multiline format with each line containing `HeaderName HeaderValue`.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: header-example
  annotations:
    haproxy.org/request-set-header: |
      X-Forwarded-Proto https
      X-Custom-Header custom-value
spec:
  rules:
    - host: api.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: api-service
                port:
                  number: 8080
```

**Generated HAProxy Configuration**:

```haproxy
http-request set-header X-Forwarded-Proto "https"
http-request set-header X-Custom-Header "custom-value"
```

**Dependencies**: None

**Related annotations**: `response-set-header`, `set-host`

---

### haproxy.org/response-set-header

**Status**: ✅ Supported

**Description**: Set or modify response headers before returning to client. Multiline format with each line containing `HeaderName HeaderValue`.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: security-headers
  annotations:
    haproxy.org/response-set-header: |
      Strict-Transport-Security "max-age=31536000; includeSubDomains"
      X-Frame-Options DENY
      X-Content-Type-Options nosniff
spec:
  rules:
    - host: example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: web-service
                port:
                  number: 80
```

**Generated HAProxy Configuration**:

```haproxy
http-response set-header Strict-Transport-Security "max-age=31536000; includeSubDomains"
http-response set-header X-Frame-Options "DENY"
http-response set-header X-Content-Type-Options "nosniff"
```

**Dependencies**: None

**Related annotations**: `request-set-header`

---

### haproxy.org/set-host

**Status**: ✅ Supported

**Description**: Modify the Host header after backend selection. Different from `request-set-header Host` in timing.

**Usage**:

```yaml
haproxy.org/set-host: "internal-api.example.svc.cluster.local"
```

**Generated HAProxy Configuration**:

```haproxy
http-request set-header Host "internal-api.example.svc.cluster.local"
```

**Dependencies**: None

**Note**: This happens after backend selection, while `request-set-header Host` happens before.

---

### haproxy.org/forwarded-for

**Status**: ✅ Supported

**Description**: Add X-Forwarded-For header with client IP address.

**Default**: `true`

**Usage**:

```yaml
haproxy.org/forwarded-for: "true"
```

**Generated HAProxy Configuration**:

```haproxy
option forwardfor
```

**Dependencies**: None

---

## Path Manipulation

### haproxy.org/path-rewrite

**Status**: ✅ Supported

**Description**: Rewrite request path using regex patterns before forwarding to backend. Supports two formats: single parameter (matches all paths) or two parameters (regex pattern and replacement).

**Usage**:

```yaml
# Strip prefix: /api/v1/users -> /users
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: path-rewrite-example
  annotations:
    haproxy.org/path-rewrite: |
      ^/api/v1/(.*) /\1
spec:
  rules:
    - host: api.example.com
      http:
        paths:
          - path: /api/v1
            pathType: Prefix
            backend:
              service:
                name: api-service
                port:
                  number: 8080
```

**Generated HAProxy Configuration**:

```haproxy
http-request replace-path ^/api/v1/(.*) /\1
```

**Dependencies**: None

**Related annotations**: Similar to Gateway API URLRewrite filter

---

## Request Redirect

### haproxy.org/request-redirect

**Status**: ✅ Supported

**Description**: Redirect requests to a different host/port. Supports formats: `example.com`, `example.com:8888`, `https://example.com`, `http://example.com`.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: redirect-example
  annotations:
    haproxy.org/request-redirect: "https://new.example.com"
    haproxy.org/request-redirect-code: "301"
spec:
  rules:
    - host: old.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: placeholder-service
                port:
                  number: 80
```

**Generated HAProxy Configuration**:

```haproxy
http-request redirect location https://new.example.com code 301
```

**Dependencies**: None

**Related annotations**: `request-redirect-code`

---

### haproxy.org/request-redirect-code

**Status**: ✅ Supported

**Description**: HTTP status code for redirect.

**Default**: `302` (Found)

**Valid values**: `301` (Moved Permanently), `302` (Found), `303` (See Other), `307` (Temporary Redirect), `308` (Permanent Redirect)

**Usage**:

```yaml
haproxy.org/request-redirect-code: "301"
```

**Dependencies**: Requires `request-redirect` to be set

---

## SSL/TLS Configuration

### haproxy.org/ssl-redirect

**Status**: ✅ Supported

**Description**: Force HTTPS redirect for HTTP requests. Automatically enabled when TLS secrets are present in the ingress.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ssl-redirect-example
  annotations:
    haproxy.org/ssl-redirect: "true"
    haproxy.org/ssl-redirect-code: "301"
spec:
  tls:
    - hosts:
        - example.com
      secretName: example-tls
  rules:
    - host: example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: web-service
                port:
                  number: 80
```

**Generated HAProxy Configuration**:

```haproxy
http-request redirect scheme https code 301 if !{ ssl_fc }
```

**Dependencies**: None

**Related annotations**: `ssl-redirect-code`, `ssl-redirect-port`

---

### haproxy.org/ssl-redirect-code

**Status**: ✅ Supported

**Description**: HTTP status code for SSL redirect.

**Default**: `302`

**Valid values**: `301`, `302`, `303`, `307`, `308`

**Usage**:

```yaml
haproxy.org/ssl-redirect-code: "301"
```

**Dependencies**: Requires `ssl-redirect: "true"`

---

### haproxy.org/ssl-redirect-port

**Status**: ❌ Not Implemented

**Description**: Target HTTPS port for the SSL redirect. The haproxytech library always redirects to the scheme (`https://`) without an explicit port.

**Workaround**: Override the `frontend-filters-050-ssl-redirect` snippet (from the SSL library) to emit a `redirect location https://…:<port>` line instead of `redirect scheme https`.

---

### haproxy.org/ssl-passthrough

**Status**: ✅ Supported

**Description**: Enable TCP mode SSL passthrough (Layer 4) for specific ingresses while allowing SSL termination for others. Uses SNI-based routing with unix socket loopback to support mixed passthrough and termination traffic.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ssl-passthrough-example
  annotations:
    haproxy.org/ssl-passthrough: "true"
spec:
  tls:
    - hosts:
        - secure.example.com
      secretName: example-tls
  rules:
    - host: secure.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: secure-service
                port:
                  number: 443
```

**Generated HAProxy Configuration**:

```haproxy
# TCP frontend for SNI-based routing
frontend ssl-tcp
    mode tcp
    bind *:443
    tcp-request inspect-delay 5s
    tcp-request content accept if { req_ssl_hello_type 1 }
    use_backend ssl-passthrough-default-ssl-passthrough-ingress if { req_ssl_sni -m str secure.example.com }
    default_backend ssl-loopback

# HTTPS frontend on unix socket (for SSL termination)
frontend ssl-https
    mode http
    bind unix@/etc/haproxy/ssl-frontend.sock mode 660 accept-proxy
    # Standard HTTP routing logic applies here

# SSL passthrough backend (TCP mode)
backend ssl-passthrough-default-ssl-passthrough-ingress
    mode tcp
    balance roundrobin
    server SRV_1 10.0.1.30:8443 check

# Loopback backend for SSL termination
backend ssl-loopback
    mode tcp
    server loopback unix@/etc/haproxy/ssl-frontend.sock send-proxy-v2
```

**Implementation Notes**:

- Uses unix socket loopback pattern to support mixed passthrough and termination
- TCP frontend extracts SNI without terminating SSL
- Passthrough traffic routes directly to backend pods
- Non-passthrough traffic routes to unix socket frontend for SSL termination
- PROXY protocol v2 preserves client IP information

**Dependencies**: None

**Warning**: For passthrough traffic, HTTP-level features (headers, path rewriting, etc.) are unavailable. Non-passthrough traffic on other hosts continues to support all HTTP features.

---

### haproxy.org/server-ssl

**Status**: ✅ Supported

**Description**: Enable SSL/TLS connection to backend servers.

**Usage**:

```yaml
haproxy.org/server-ssl: "true"
```

**Generated HAProxy Configuration**:

```haproxy
server pod1 10.0.1.5:8443 ssl verify none
```

**Dependencies**: None

**Related annotations**: `server-proto`, `server-crt`, `server-ca`

---

### haproxy.org/server-proto

**Status**: ✅ Supported

**Description**: Backend protocol (typically `h2` for HTTP/2).

**Usage**:

```yaml
haproxy.org/server-ssl: "true"
haproxy.org/server-proto: "h2"
```

**Generated HAProxy Configuration**:

```haproxy
server pod1 10.0.1.5:8443 ssl verify none proto h2
```

**Dependencies**: Typically used with `server-ssl`

---

### haproxy.org/server-crt

**Status**: ✅ Supported

**Description**: Client certificate for mTLS (mutual TLS) to backend. References a Secret containing `tls.crt` and `tls.key`. Supports cross-namespace format `namespace/secretname`.

**Usage**:

```yaml
haproxy.org/server-ssl: "true"
haproxy.org/server-crt: "default/client-cert"
haproxy.org/server-ca: "default/ca-cert"
```

**Generated HAProxy Configuration**:

```haproxy
server pod1 10.0.1.5:8443 ssl crt /etc/haproxy/ssl/client-cert.pem ca-file /etc/haproxy/ssl/ca-cert.pem verify required
```

**Dependencies**: Requires `server-ssl: "true"`

**Related annotations**: `server-ca` (required for verification)

---

### haproxy.org/server-ca

**Status**: ✅ Supported

**Description**: CA certificate for verifying backend server certificates. References a Secret containing `tls.crt`. Supports cross-namespace format `namespace/secretname`.

**Usage**:

```yaml
haproxy.org/server-ssl: "true"
haproxy.org/server-ca: "default/ca-cert"
```

**Generated HAProxy Configuration**:

```haproxy
server pod1 10.0.1.5:8443 ssl ca-file /etc/haproxy/ssl/ca-cert.pem verify required
```

**Dependencies**: Requires `server-ssl: "true"`

**Related annotations**: `server-crt` (for mTLS)

---

## Backend Health Checks & Connection Management

### haproxy.org/check

**Status**: ⚠️ Partial — value validated but not honoured

**Description**: Health checks are emitted unconditionally as `default-server check` by `backends-500-ingress`. This annotation is parsed (it errors on values other than `"true"` / `"false"`) but only adds a `# Note: check annotation value: ...` comment to the rendered config — there is no code path that disables checks when set to `"false"`. To disable health checks for a single backend, supply a `haproxy.org/backend-config-snippet` annotation that overrides `default-server`.

**Usage** (informational only):

```yaml
haproxy.org/check: "true"
```

**Generated HAProxy Configuration**:

```haproxy
default-server check
server SRV_1 10.0.1.5:8080 enabled
# Note: check annotation value: true
```

**Related annotations**: `check-http` (works), `check-interval` (silent no-op — see below), `timeout-check`

---

### haproxy.org/check-http

**Status**: ✅ Supported

**Description**: HTTP URI or full HTTP request for health checks.

**Usage**:

```yaml
# Simple URI
haproxy.org/check: "true"
haproxy.org/check-http: "/health"

# Full HTTP request
haproxy.org/check-http: "HEAD /health HTTP/1.1"
```

**Generated HAProxy Configuration**:

```haproxy
# Simple URI
option httpchk GET /health

# Full HTTP request
option httpchk HEAD /health HTTP/1.1
```

**Dependencies**: Requires `check: "true"`

---

### haproxy.org/check-interval

**Status**: ❌ Not Implemented

**Description**: Intended to set the health-check interval (e.g., `10s`, `1m`), but the library only emits a `# Note: check-interval annotation value: ...` comment — no `inter <duration>` flag is added to `default-server`. The annotation is silently ignored. To set a custom interval today, use `haproxy.org/backend-config-snippet` with an override of `default-server`.

**Usage** (no effect):

```yaml
haproxy.org/check-interval: "10s"
```

---

### haproxy.org/timeout-check

**Status**: ✅ Supported

**Description**: Timeout for health check responses. Supports duration format.

**Default**: `5s`

**Usage**:

```yaml
haproxy.org/timeout-check: "3s"
```

**Generated HAProxy Configuration**:

```haproxy
timeout check 3s
```

**Dependencies**: None

---

### haproxy.org/pod-maxconn

**Status**: ✅ Supported

**Description**: Maximum total concurrent connections for all backend pods combined, automatically divided equally among HAProxy controller replicas with ceiling rounding. Only Running and Ready pods are counted.

**Usage**:

```yaml
haproxy.org/pod-maxconn: "100"
```

**Behavior**:

The annotation value represents the **total** maximum connections across all HAProxy replicas. The controller automatically:

- Counts only **Running and Ready** HAProxy controller pods (Pending, CrashLoopBackOff, SysctlForbidden, and other non-ready pods are excluded)
- Quantizes the pod count to the **next power of 2** to avoid HAProxy reload cascades when pods scale up or down
- Divides the total by the quantized count (ceiling rounding)
- Applies the per-pod value to each server line

!!! note
    The power-of-2 quantization means the effective per-pod maxconn only changes when the ready pod count crosses a power-of-2 boundary (1, 2, 4, 8, 16, ...). This prevents unnecessary HAProxy reloads during scaling events. The trade-off is that the actual total capacity may be lower than the annotation value when the pod count is not an exact power of 2.

**Examples**:

**Single HAProxy pod**: Value applied directly without division

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  annotations:
    haproxy.org/pod-maxconn: "100"
```

Generated HAProxy configuration (1 ready HAProxy pod):

```haproxy
# pod-maxconn: 100 total / 1 ready pods (effective: 1) = 100 per pod
default-server check maxconn 100
server SRV_1 10.0.1.5:8080 enabled
```

**Multiple HAProxy pods**: Value divided with power-of-2 quantization

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  annotations:
    haproxy.org/pod-maxconn: "100"
```

Generated HAProxy configuration (2 ready HAProxy pods, quantized to 2):

```haproxy
# pod-maxconn: 100 total / 2 ready pods (effective: 2) = 50 per pod
default-server check maxconn 50
server SRV_1 10.0.1.5:8080 enabled
```

Generated HAProxy configuration (3 ready HAProxy pods, quantized to 4):

```haproxy
# pod-maxconn: 100 total / 3 ready pods (effective: 4) = 25 per pod
default-server check maxconn 25
server SRV_1 10.0.1.5:8080 enabled
```

`maxconn` and `check` live on `default-server` (not on individual server lines) so endpoint changes can be applied via the runtime API without a HAProxy reload. Individual server lines carry only `address:port` plus `enabled` (active) or `disabled` (reserved slot).

**Quantization reference** (for `pod-maxconn: 200`):

| Ready pods | Effective count | maxconn per pod |
|------------|-----------------|-----------------|
| 1          | 1               | 200             |
| 2          | 2               | 100             |
| 3-4        | 4               | 50              |
| 5-8        | 8               | 25              |
| 9-16       | 16              | 13              |

**Fallback behavior**: If no Running and Ready HAProxy pods are discovered yet (e.g., during initial startup), the full annotation value is used temporarily until pod discovery completes.

**Dependencies**: Requires HAProxy pod discovery to be operational for automatic division

---

### haproxy.org/scale-server-slots

**Status**: ❌ Not Implemented

**Description**: Intended to override the number of pre-allocated server slots, but the annotation is currently a silent no-op. The library reads it in `backend-directives-900-haproxytech-advanced`, validates it (must be a positive integer), and writes the value to `serverOpts["serverSlotsValue"]`. However, `backends-500-ingress` calls `BackendServers(svcName, 0, port, nil, portName, backendKey, ns)` with `nil` instead of `serverOpts` for the 4th argument, so the macro never consults the value and falls back to the default 10 slots.

**Default**: `10` slots (always — overrides via this annotation are dropped today).

**Usage** (no effect):

```yaml
haproxy.org/scale-server-slots: "100"
```

**Workaround**: To change the slot count, replace `backends-500-ingress` with a custom snippet that passes a non-zero `maxServerSlots` argument to `BackendServers`, or restructure `ingress.yaml`'s call to forward `serverOpts` to `BackendServers`.

---

## Load Balancing Algorithms

### haproxy.org/load-balance

**Status**: ✅ Supported

**Description**: Load balancing algorithm for distributing traffic across backend servers.

**Default**: `roundrobin`

**Valid values**: `roundrobin`, `leastconn`, `source`, `uri`, `hdr`, `random`, `rdp-cookie`

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: load-balance-example
  annotations:
    haproxy.org/load-balance: "leastconn"
spec:
  rules:
    - host: api.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: api-service
                port:
                  number: 8080
```

**Generated HAProxy Configuration**:

```haproxy
backend api-backend
    balance leastconn
```

**Dependencies**: None

---

## Session Persistence

### haproxy.org/cookie-persistence

**Status**: ✅ Supported

**Description**: Enable sticky sessions using dynamic cookies. The cookie value is dynamically generated per server.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: sticky-sessions
  annotations:
    haproxy.org/cookie-persistence: "SERVERID"
spec:
  rules:
    - host: app.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: app-service
                port:
                  number: 8080
```

**Generated HAProxy Configuration**:

```haproxy
backend app-backend
    cookie SERVERID insert indirect nocache dynamic
    server pod1 10.0.1.5:8080 cookie pod1
```

**Dependencies**: None

**Note**: `insert … dynamic` makes HAProxy derive the cookie value by hashing each server's address with a per-process key. No `dynamic-cookie-key` is emitted, so each HAProxy instance uses its own key — affinity holds per instance, not across instances. For affinity that survives across all replicas, pin a fixed key via a custom config snippet.

---

### haproxy.org/cookie-persistence-no-dynamic

**Status**: ✅ Supported

**Description**: Enable sticky sessions using static cookies (without dynamic-cookie-key). Use only in single-instance controller deployments.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: static-sticky-sessions
  annotations:
    haproxy.org/cookie-persistence-no-dynamic: "SERVERID"
spec:
  rules:
    - host: app.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: app-service
                port:
                  number: 8080
```

**Generated HAProxy Configuration**:

```haproxy
backend app-backend
    cookie SERVERID insert indirect nocache
    server pod1 10.0.1.5:8080 cookie pod1
```

**Dependencies**: None

**Note**: Mutually exclusive with `cookie-persistence`. For multi-instance deployments, use `cookie-persistence` (dynamic mode) instead to ensure consistent cookie values across controller instances.

**Warning**: Static cookies will differ across controller instances, breaking session affinity. Only use in single-instance deployments.

---

## Timeouts

### haproxy.org/timeout-server

**Status**: ✅ Supported

**Description**: Maximum time to wait for backend server response. Supports duration format.

**Default**: `50s`

**Usage**:

```yaml
haproxy.org/timeout-server: "30s"
```

**Generated HAProxy Configuration**:

```haproxy
timeout server 30s
```

**Dependencies**: None

---

### haproxy.org/timeout-client

**Status**: ❌ Not Implemented

**Description**: Maximum inactivity time on the client side. The haproxytech library does not emit a per-backend `timeout client` (it would have no effect anyway — `timeout client` only applies in frontend/defaults sections).

**Workaround**: Set the global `timeout client` via the `defaults-settings-300-timeouts` snippet override. See [Base Library](base.md#injecting-custom-configuration).

---

### haproxy.org/timeout-connect

**Status**: ✅ Supported

**Description**: Maximum time to wait for backend connection. Supports duration format.

**Default**: `5s`

**Usage**:

```yaml
haproxy.org/timeout-connect: "10s"
```

**Generated HAProxy Configuration**:

```haproxy
timeout connect 10s
```

**Dependencies**: None

---

### haproxy.org/timeout-http-request

**Status**: ❌ Not Implemented

**Description**: The haproxytech library does not process this annotation. The equivalent exists in the `haproxy-ingress` library as `haproxy-ingress.github.io/timeout-http-request`.

**Workaround**: Either add the `haproxy-ingress.github.io/timeout-http-request` annotation (see [haproxy-ingress library](haproxy-ingress.md)), or override `defaults-settings-300-timeouts` globally.

---

### haproxy.org/timeout-http-keep-alive

**Status**: ❌ Not Implemented

**Description**: The haproxytech library does not process this annotation. The equivalent exists in the `haproxy-ingress` library as `haproxy-ingress.github.io/timeout-keep-alive`.

**Workaround**: Either add the `haproxy-ingress.github.io/timeout-keep-alive` annotation (see [haproxy-ingress library](haproxy-ingress.md)), or override `defaults-settings-300-timeouts` globally.

---

### haproxy.org/timeout-queue

**Status**: ✅ Supported

**Description**: Maximum time a request can wait in queue when all backend servers are busy. Supports duration format.

**Default**: `5s`

**Usage**:

```yaml
haproxy.org/timeout-queue: "30s"
```

**Generated HAProxy Configuration**:

```haproxy
timeout queue 30s
```

**Dependencies**: None

---

### haproxy.org/timeout-tunnel

**Status**: ✅ Supported

**Description**: Maximum inactivity time on tunnel connections (WebSocket, CONNECT). Supports duration format.

**Default**: `1h`

**Usage**:

```yaml
haproxy.org/timeout-tunnel: "2h"
```

**Generated HAProxy Configuration**:

```haproxy
timeout tunnel 2h
```

**Dependencies**: None

---

## Request Capture & Logging

### haproxy.org/request-capture

**Status**: ✅ Supported

**Description**: Capture request data for logging. Multiline format with HAProxy sample expressions.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: logging-example
  annotations:
    haproxy.org/request-capture: |
      hdr(User-Agent)
      cookie(session)
      path
      method
    haproxy.org/request-capture-len: "256"
spec:
  rules:
    - host: api.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: api-service
                port:
                  number: 8080
```

**Generated HAProxy Configuration**:

```haproxy
capture request header User-Agent len 256
capture request header Cookie len 256
# path and method captured via special handling
```

**Dependencies**: None

**Related annotations**: `request-capture-len`

---

### haproxy.org/request-capture-len

**Status**: ✅ Supported

**Description**: Maximum length for captured request data.

**Default**: `128`

**Usage**:

```yaml
haproxy.org/request-capture-len: "256"
```

**Dependencies**: Applies to `request-capture` expressions

---

## Source IP Detection

### haproxy.org/src-ip-header

**Status**: ✅ Supported

**Description**: Extract true client IP from a specific header (useful when behind proxies/CDNs).

**Usage**:

```yaml
# Behind Cloudflare
haproxy.org/src-ip-header: "CF-Connecting-IP"

# Behind AWS ALB
haproxy.org/src-ip-header: "X-Forwarded-For"

# Behind custom proxy
haproxy.org/src-ip-header: "True-Client-IP"
```

**Generated HAProxy Configuration**:

```haproxy
http-request set-src hdr(CF-Connecting-IP)
```

**Dependencies**: None

**Note**: Use with caution - ensure the header is set by a trusted proxy.

---

## Advanced Backend Configuration

### haproxy.org/backend-config-snippet

**Status**: ✅ Supported

**Description**: Inject raw HAProxy configuration directives into backend section. Multiline YAML string.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: advanced-backend
  annotations:
    haproxy.org/backend-config-snippet: |
      stick-table type string len 32 size 100k expire 30m
      stick store-response res.cook(JSESSIONID)
      http-send-name-header X-Backend-Server
spec:
  rules:
    - host: app.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: app-service
                port:
                  number: 8080
```

**Generated HAProxy Configuration**:

```haproxy
backend app-backend
    stick-table type string len 32 size 100k expire 30m
    stick store-response res.cook(JSESSIONID)
    http-send-name-header X-Backend-Server
    # ... other backend config
```

**Dependencies**: None

**Warning**: Raw config injection bypasses validation. User is responsible for correct syntax.

---

### haproxy.org/send-proxy-protocol

**Status**: ✅ Supported

**Description**: Enable PROXY protocol for backend connections to preserve client IP information.

**Valid values**: `proxy`, `proxy-v1`, `proxy-v2`, `proxy-v2-ssl`, `proxy-v2-ssl-cn`

**Usage**:

```yaml
haproxy.org/send-proxy-protocol: "proxy-v2"
```

**Generated HAProxy Configuration**:

```haproxy
server pod1 10.0.1.5:8080 send-proxy-v2
```

**Dependencies**: None

**Note**: Backend application must support PROXY protocol.

---

### haproxy.org/standalone-backend

**Status**: ❌ Not Implemented (Not Planned)

**Description**: Create a dedicated backend for this ingress instead of sharing backends across ingresses.

**Note**: This controller's architecture already generates standalone backends (one backend per ingress+service+port combination) rather than sharing backends across ingresses. Each unique combination of `<namespace>_<ingress-name>_svc_<service-name>_<port-name>` gets its own dedicated backend, making this annotation redundant. Implementation is not planned.

---

## Authentication

### haproxy.org/auth-type

**Status**: ✅ Supported

**Description**: Type of authentication to enforce. Currently only `basic-auth` is supported.

**Valid values**: `basic-auth`

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: protected-api
  annotations:
    haproxy.org/auth-type: basic-auth
    haproxy.org/auth-secret: auth-credentials
    haproxy.org/auth-realm: "API Access"
spec:
  rules:
    - host: api.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: api-service
                port:
                  number: 8080
```

**Generated HAProxy Configuration**:

```haproxy
# Global section
userlist auth_default_auth-credentials
  user admin password $2y$05$...

# Backend section
http-request auth realm "API Access" unless { http_auth(auth_default_auth-credentials) }
```

**Dependencies**: Requires `auth-secret` to be set

**Related annotations**: `auth-secret`, `auth-realm`

**Implementation notes**:

- Secret format: Opaque secret where key=username, value=base64-encoded password hash
- Supports cross-namespace secrets: `namespace/secretname`
- Automatic deduplication: multiple ingresses sharing the same secret generate a single userlist
- HAProxy parses `$1$` (MD5 crypt), `$5$` (SHA-256), `$6$` (SHA-512), and `$2y$` (bcrypt). It does **not** parse `$apr1$` (Apache MD5 — the htpasswd *default* without `-B` / `-5` / `-6`); generate hashes with `htpasswd -nbB` (bcrypt), `-nb5` (SHA-256), or `-nb6` (SHA-512). See [Performance — Password hash validation](https://haproxy-haptic.org/controller/latest/operations/performance/#password-hash-performance) for the cost/perf trade-off.

!!! note "Implementation Difference from HAProxy Ingress Controller"
    This controller uses **per-secret** userlist naming (`auth_{secretNs}_{secretName}`) rather than the official HAProxy Ingress Controller's per-ingress naming (`{namespace}-{ingressName}`). This deduplicates userlists when multiple Ingresses reference the same secret, significantly improving configuration validation performance for expensive password hashes like bcrypt (~85ms per hash validation).

---

### haproxy.org/auth-secret

**Status**: ✅ Supported

**Description**: Reference to Kubernetes Secret containing authentication credentials. Supports cross-namespace format `namespace/secretname`.

**Usage**:

```yaml
# Same namespace
haproxy.org/auth-secret: "auth-credentials"

# Cross-namespace
haproxy.org/auth-secret: "auth-system/shared-credentials"
```

**Secret format**:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: auth-credentials
  namespace: default
type: Opaque
data:
  # Key: username
  # Value: base64-encoded password hash (NOT htpasswd format)
  admin: JDJ5JDA1JG1OMVdWazVRbmJnNFF3ZEFkWGJmei44YjNjZUg2UTVLT1ZDS3hSMklrTkFmSmdMaTVwSUtX
```

**Generate password hash**:

```bash
# SHA-512 ($6$) — recommended: ~3ms validation per hash
htpasswd -nb6 admin mypassword | cut -d: -f2 | base64 -w0

# bcrypt ($2y$) — strongest, but ~85ms validation per hash at cost 10
htpasswd -nbB admin mypassword | cut -d: -f2 | base64 -w0
```

Validation cost matters because HAProxy re-runs the hash at every config parse, and the controller validates the config on every reconciliation. With many users or high-cost bcrypt, this dominates reconciliation time. Stick with SHA-512 unless you specifically need bcrypt's adjustable work factor.

**Dependencies**: Requires `auth-type: basic-auth`

**Implementation notes**:

- Value must be ONLY the password hash, NOT "username:hash" (htpasswd format)
- Multiple usernames supported: add multiple keys to the secret
- The hash must be in a format HAProxy parses (`$1$` MD5 crypt, `$5$` SHA-256, `$6$` SHA-512, or `$2y$` bcrypt). `$apr1$` (Apache MD5 — what plain `htpasswd -nb` produces) is **not** parsed; pass `-B`, `-5`, or `-6` to `htpasswd` instead.

---

### haproxy.org/auth-realm

**Status**: ✅ Supported

**Description**: Authentication realm displayed in browser's authentication prompt.

**Default**: `RestrictedArea`

**Usage**:

```yaml
haproxy.org/auth-realm: "API Access"
```

**Dependencies**: Requires `auth-type: basic-auth` and `auth-secret`

**Note**: HAProxy Data Plane API requires realm without spaces (regex: `^[^\s]+$`). Use hyphenated names.

---

## Known Limitations

### Not Implemented

1. **Service-level annotations** - Annotations on Service resources are not supported. Only Ingress annotations are implemented.

2. **Deprecated annotations** - Legacy annotation names (`whitelist`, `blacklist`, `ingress.class`) are not implemented. Use current names instead.

3. **RequestMirror equivalent** - No annotation-based traffic mirroring. Consider using Gateway API with external SPOE agent for this feature.

### Implementation Differences from HAProxy Tech

1. **Template-based approach** - This implementation uses Scriggo templates rather than Go code, allowing users to customize behavior through template overrides.

2. **Resource-agnostic architecture** - The controller doesn't have built-in annotation handling. All annotation support is provided through pluggable template libraries.

3. **Validation tests** - Each annotation implementation includes comprehensive validation tests that run during chart development/testing.

4. **Secret format for auth** - Password values must be base64-encoded hashes only, not htpasswd format (`username:hash`). This simplifies template logic and aligns with Kubernetes Secret best practices.

## Implementation Status Summary

The library processes **47** `haproxy.org/*` annotations (verified against `libraries/haproxytech.yaml`).

**Supported by category:**

| Category | Count | Annotations |
|----------|-------|-------------|
| Access control | 2 | `allowlist`, `denylist` |
| Authentication | 3 | `auth-type`, `auth-secret`, `auth-realm` |
| CORS | 6 | `cors-enable`, `cors-allow-origin`, `cors-allow-methods`, `cors-allow-headers`, `cors-allow-credentials`, `cors-max-age` |
| Rate limiting | 4 | `rate-limit-requests`, `rate-limit-period`, `rate-limit-size`, `rate-limit-status-code` |
| Header manipulation | 3 | `forwarded-for`, `request-set-header`, `response-set-header` |
| Path manipulation | 1 | `path-rewrite` |
| Request redirect | 2 | `request-redirect`, `request-redirect-code` |
| SSL/TLS | 3 | `ssl-redirect`, `ssl-redirect-code`, `ssl-passthrough` |
| Health checks | 3 | `check`, `check-http`, `check-interval` |
| Load balancing | 1 | `load-balance` |
| Session persistence | 2 | `cookie-persistence`, `cookie-persistence-no-dynamic` |
| Timeouts | 5 | `timeout-server`, `timeout-connect`, `timeout-queue`, `timeout-tunnel`, `timeout-check` |
| Logging | 3 | `src-ip-header`, `request-capture`, `request-capture-len` |
| Host manipulation | 1 | `set-host` |
| Connection management | 1 | `pod-maxconn` |
| Server scaling | 1 | `scale-server-slots` |
| Backend server options | 4 | `server-ssl`, `server-proto`, `server-crt`, `server-ca` |
| Proxy protocol | 1 | `send-proxy-protocol` |
| Advanced backend config | 1 | `backend-config-snippet` |

**Not implemented in the `haproxy.org/*` namespace:**

- `ssl-redirect-port`, `timeout-client`, `timeout-http-request`, `timeout-http-keep-alive` — four of these timeouts are available under `haproxy-ingress.github.io/*` instead (see [haproxy-ingress library](haproxy-ingress.md))
- `whitelist`, `blacklist` — replaced by `allowlist` / `denylist` in the upstream project
- `standalone-backend` — not needed; this controller already emits a dedicated backend per `<namespace>_<ingress-name>_svc_<service-name>_<port>` tuple

## See Also

- [HAProxy Ingress Controller Documentation](https://www.haproxy.com/documentation/kubernetes-ingress/community/configuration-reference/ingress)
- [HAProxy Ingress Controller Source Code](https://github.com/haproxytech/kubernetes-ingress)
- [Gateway API Library](gateway.md) - For Gateway API resources (HTTPRoute, GRPCRoute)
- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Extension points
- [Ingress Library](ingress.md) - Ingress resource support
