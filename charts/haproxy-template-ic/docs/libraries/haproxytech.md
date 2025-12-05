# HAProxy Annotations Library

The HAProxy Annotations library provides support for `haproxy.org/*` annotations, enabling fine-grained control over HAProxy behavior through Kubernetes resource annotations.

## Overview

The HAProxy Annotations library implements annotations compatible with the [HAProxy Kubernetes Ingress Controller](https://www.haproxy.com/documentation/kubernetes-ingress/). This enables:

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

### Extension Points Used

The HAProxy Annotations library implements these extension points from base.yaml:

| Extension Point | This Library's Snippets | What They Generate |
|-----------------|-------------------------|-------------------|
| Global Top | `global-top-haproxytech-ingress-auth` | Userlist definitions for basic auth |
| Frontend Filters | `frontend-filters-haproxytech-*` | SSL redirect, CORS, access control, logging |
| Backend Directives | `backend-directives-haproxytech-*` | Load balancing, rate limiting, health checks |

### Frontend Filter Snippets

| Snippet | Annotations Processed |
|---------|----------------------|
| `frontend-filters-haproxytech-ssl-redirect` | `haproxy.org/ssl-redirect` |
| `frontend-filters-haproxytech-redirects` | `haproxy.org/request-redirect`, `haproxy.org/request-redirect-code` |
| `frontend-filters-haproxytech-cors` | `haproxy.org/cors-*` annotations |
| `frontend-filters-haproxytech-access-control` | `haproxy.org/allowlist-with-*`, `haproxy.org/denylist-with-*` |
| `frontend-filters-haproxytech-logging` | `haproxy.org/log-format` |
| `frontend-filters-haproxytech-basic-headers` | `haproxy.org/forwarded-for`, `haproxy.org/set-host` |
| `frontend-filters-haproxytech-timeouts` | `haproxy.org/timeout-client` |

### Backend Directive Snippets

| Snippet | Annotations Processed |
|---------|----------------------|
| `backend-directives-haproxytech-load-balance` | `haproxy.org/load-balance` |
| `backend-directives-haproxytech-rate-limiting` | `haproxy.org/rate-limit-*` |
| `backend-directives-haproxytech-session-persistence` | `haproxy.org/cookie-persistence` |
| `backend-directives-haproxytech-advanced` | `haproxy.org/server-*`, `haproxy.org/check-*` |
| `backend-directives-haproxytech-path-rewrite` | `haproxy.org/path-rewrite` |
| `backend-directives-haproxytech-header-manipulation` | `haproxy.org/request-set-header`, `haproxy.org/response-set-header` |
| `backend-directives-haproxytech-set-host` | `haproxy.org/set-host` |
| `backend-directives-haproxytech-health-checks` | `haproxy.org/check`, `haproxy.org/check-*` |
| `backend-directives-haproxytech-pod-maxconn` | `haproxy.org/pod-maxconn` |
| `backend-directives-haproxytech-timeouts` | `haproxy.org/timeout-*` |

### Injecting Custom Annotations

You can extend annotation processing by adding snippets:

```yaml
controller:
  config:
    templateSnippets:
      # Add custom frontend filter
      frontend-filters-custom-security:
        template: |
          {%- for ingress in resources.ingresses.List() %}
          {%- set security_level = ingress.metadata.annotations["custom.io/security-level"] | default("") %}
          {%- if security_level == "high" %}
          http-request deny unless { ssl_fc }
          {%- endif %}
          {%- endfor %}
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

**Status**: ✅ Supported

**Description**: Target HTTPS port for SSL redirect.

**Default**: `443`

**Usage**:

```yaml
haproxy.org/ssl-redirect-port: "8443"
```

**Dependencies**: Requires `ssl-redirect: "true"`

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
server pod1 10.0.1.5:8443 ssl crt /etc/haproxy/certs/client-cert.pem ca-file /etc/haproxy/certs/ca-cert.pem verify required
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
server pod1 10.0.1.5:8443 ssl ca-file /etc/haproxy/certs/ca-cert.pem verify required
```

**Dependencies**: Requires `server-ssl: "true"`

**Related annotations**: `server-crt` (for mTLS)

---

## Backend Health Checks & Connection Management

### haproxy.org/check

**Status**: ✅ Supported

**Description**: Enable health checks for backend servers.

**Default**: `true`

**Usage**:

```yaml
haproxy.org/check: "true"
```

**Generated HAProxy Configuration**:

```haproxy
server pod1 10.0.1.5:8080 check
```

**Dependencies**: None

**Related annotations**: `check-http`, `check-interval`, `timeout-check`

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

**Status**: ✅ Supported

**Description**: Frequency of health checks. Supports duration format (e.g., `10s`, `1m`).

**Default**: `2s`

**Usage**:

```yaml
haproxy.org/check: "true"
haproxy.org/check-interval: "10s"
```

**Generated HAProxy Configuration**:

```haproxy
server pod1 10.0.1.5:8080 check inter 10s
```

**Dependencies**: Requires `check: "true"`

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

**Description**: Maximum total concurrent connections for all backend pods combined, automatically divided equally among HAProxy controller replicas with ceiling rounding.

**Usage**:

```yaml
haproxy.org/pod-maxconn: "100"
```

**Behavior**:

The annotation value represents the **total** maximum connections across all HAProxy replicas. The controller automatically:

- Counts running HAProxy controller pods
- Divides the total by the pod count (ceiling rounding)
- Applies the per-pod value to each server line

**Examples**:

**Single HAProxy pod**: Value applied directly without division

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  annotations:
    haproxy.org/pod-maxconn: "100"
```

Generated HAProxy configuration (1 HAProxy pod):

```haproxy
# pod-maxconn: 100 total / 1 HAProxy pods = 100 per pod
server SRV_1 10.0.1.5:8080 maxconn 100 check
```

**Multiple HAProxy pods**: Value divided equally with ceiling rounding

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  annotations:
    haproxy.org/pod-maxconn: "100"
```

Generated HAProxy configuration (2 HAProxy pods):

```haproxy
# pod-maxconn: 100 total / 2 HAProxy pods = 50 per pod
server SRV_1 10.0.1.5:8080 maxconn 50 check
```

Generated HAProxy configuration (3 HAProxy pods, with ceiling rounding):

```haproxy
# pod-maxconn: 100 total / 3 HAProxy pods = 34 per pod
server SRV_1 10.0.1.5:8080 maxconn 34 check
```

**Fallback behavior**: If no HAProxy pods are discovered yet (e.g., during initial startup), the full value is used temporarily until pod discovery completes.

**Dependencies**: Requires HAProxy pod discovery to be operational for automatic division

---

### haproxy.org/scale-server-slots

**Status**: ✅ Supported

**Description**: Number of server slots to pre-allocate for dynamic scaling.

**Default**: `10` (template default; HAProxy Ingress Controller uses `42`)

**Usage**:

```yaml
haproxy.org/scale-server-slots: "100"
```

**Generated HAProxy Configuration**:

```haproxy
# Pre-allocates 100 server slots for dynamic pod scaling
server pod1 10.0.1.5:8080
server pod2 10.0.1.6:8080
# ... up to 100 slots
```

**Dependencies**: None

**Note**: Used for runtime server addition/removal without config reload.

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
    dynamic-cookie-key <generated-key>
    server pod1 10.0.1.5:8080 cookie pod1
```

**Dependencies**: None

**Note**: For multi-instance setups, dynamic cookies ensure consistency across controller instances.

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

**Status**: ✅ Supported

**Description**: Maximum inactivity time on client side. Supports duration format.

**Default**: `50s`

**Usage**:

```yaml
haproxy.org/timeout-client: "60s"
```

**Generated HAProxy Configuration**:

```haproxy
timeout client 60s
```

**Dependencies**: None

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

**Status**: ✅ Supported

**Description**: Maximum time to wait for complete HTTP request. Supports duration format.

**Default**: `5s`

**Usage**:

```yaml
haproxy.org/timeout-http-request: "10s"
```

**Generated HAProxy Configuration**:

```haproxy
timeout http-request 10s
```

**Dependencies**: None

---

### haproxy.org/timeout-http-keep-alive

**Status**: ✅ Supported

**Description**: Maximum time to wait for a new HTTP request on a keep-alive connection. Supports duration format.

**Default**: `1m`

**Usage**:

```yaml
haproxy.org/timeout-http-keep-alive: "2m"
```

**Generated HAProxy Configuration**:

```haproxy
timeout http-keep-alive 2m
```

**Dependencies**: None

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

**Note**: This controller's architecture already generates standalone backends (one backend per ingress+service+port combination) rather than sharing backends across ingresses. Each unique combination of `namespace/ingress-name/service-name/port` gets its own dedicated backend, making this annotation redundant. Implementation is not planned.

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
- HAProxy 3.2+ uses bcrypt password hashing (not MD5 apr1)

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
# Create bcrypt hash and encode to base64
htpasswd -nbB admin mypassword | cut -d: -f2 | base64 -w0
```

**Dependencies**: Requires `auth-type: basic-auth`

**Implementation notes**:

- Value must be ONLY the password hash, NOT "username:hash" (htpasswd format)
- Multiple usernames supported: add multiple keys to the secret
- HAProxy 3.2+ supports bcrypt ($2y$), not MD5 apr1 ($apr1$)

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

1. **Template-based approach** - This implementation uses Jinja2-like templates (Gonja) rather than Go code, allowing users to customize behavior through template overrides.

2. **Resource-agnostic architecture** - The controller doesn't have built-in annotation handling. All annotation support is provided through pluggable template libraries.

3. **Validation tests** - Each annotation implementation includes comprehensive validation tests that run during chart development/testing.

4. **Secret format for auth** - Password values must be base64-encoded hashes only, not htpasswd format (`username:hash`). This simplifies template logic and aligns with Kubernetes Secret best practices.

## Implementation Status Summary

**Total annotations**: 54

- ✅ **Fully Supported**: 51 (94.4%)
  - Access Control & IP Filtering: 2 annotations (allowlist, denylist)
  - Authentication: 3 annotations (auth-type, auth-secret, auth-realm)
  - CORS: 6 annotations (enable, allow-origin, allow-methods, allow-headers, allow-credentials, max-age)
  - Rate Limiting: 4 annotations (requests, period, size, status-code)
  - Header Manipulation: 3 annotations (forwarded-for, request-set-header, response-set-header)
  - Path Manipulation: 1 annotation (path-rewrite)
  - Request Redirect: 2 annotations (request-redirect, request-redirect-code)
  - SSL/TLS: 4 annotations (ssl-redirect, ssl-redirect-code, ssl-redirect-port, ssl-passthrough)
  - Health Checks: 3 annotations (check, check-http, check-interval)
  - Load Balancing: 1 annotation (load-balance)
  - Session Persistence: 2 annotations (cookie-persistence, cookie-persistence-no-dynamic)
  - Timeouts: 8 annotations (server, client, connect, http-request, http-keep-alive, queue, tunnel, check)
  - Logging: 3 annotations (src-ip-header, request-capture, request-capture-len)
  - Host Manipulation: 1 annotation (set-host)
  - Connection Management: 1 annotation (pod-maxconn)
  - Server Scaling: 1 annotation (scale-server-slots)
  - Backend Server Options: 4 annotations (server-ssl, server-proto, server-crt, server-ca)
  - Proxy Protocol: 1 annotation (send-proxy-protocol)
  - Advanced Backend Config: 1 annotation (backend-config-snippet)

- ⚠️ **Partially Implemented**: 0 (0%)

- ❌ **Not Implemented**: 3 (5.6%)
  - 1 annotation: standalone-backend (not planned - architecture already provides this functionality)
  - 2 deprecated annotations: whitelist, blacklist (replaced by allowlist/denylist)

## See Also

- [HAProxy Ingress Controller Documentation](https://www.haproxy.com/documentation/kubernetes-ingress/community/configuration-reference/ingress)
- [HAProxy Ingress Controller Source Code](https://github.com/haproxytech/kubernetes-ingress)
- [Gateway API Library](gateway.md) - For Gateway API resources (HTTPRoute, GRPCRoute)
- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Extension points
- [Ingress Library](ingress.md) - Ingress resource support
