# haproxytech library

The haproxytech library implements `haproxy.org/*` annotations compatible with [haproxytech/kubernetes-ingress](https://github.com/haproxytech/kubernetes-ingress), the official HAProxy ingress controller by HAProxy Technologies.

## Overview

Supported features:

- Basic authentication
- SSL/TLS redirection
- Cross-Origin Resource Sharing (CORS) configuration
- Rate limiting
- Session persistence
- Path rewriting
- Header manipulation
- Load balancing algorithms
- Health check configuration
- Timeout customization

This library is **opt-in** — disabled by default. Enable it to keep your existing `haproxy.org/*` annotations working when migrating from haproxytech/kubernetes-ingress. For new configuration, the enabled-by-default [`haproxy-haptic.org/*`](haptic-annotations.md) native library covers the same capabilities (and more); the two coexist, so you can migrate at your own pace.

Watch the `haproxy.org/*` annotations render to HAProxy config live:

<div class="pg-embed" markdown data-scenario="haproxytech" data-facade="spec.templateSnippets.backend-directives-150-haproxytech-load-balance" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="haproxy.org/* annotations rendered" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, change the `shop` Ingress's `haproxy.org/load-balance` value from `leastconn` to `source`, then watch the shop backend's `balance` line update in the `haproxy.cfg` tab.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

The `backend storefront_shop_svc_shop_http` section swaps `balance leastconn` for `balance source`, so HAProxy pins each client to a server by source-IP hash instead of choosing the least-loaded one. Only `roundrobin`, `static-rr`, `leastconn`, `first`, `source`, `random`, or a parameterized `uri`/`url_param`/`hdr`/`rdp-cookie` are accepted — any other value fails the render.

</details>

</div>

!!! note "Migrating from haproxytech/kubernetes-ingress"
    Enable this library (see [Configuration](#configuration) below), and your existing `haproxy.org/*` annotations work without changes. See [Annotations](../annotations.md) for the full feature comparison between annotation libraries.

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
      enabled: true  # Set to enable this opt-in library (disabled by default)
```

## Extension points

The haproxytech library implements these extension points from base.yaml. All snippets follow the `<extension-point>-<NNN>-haproxytech-*` naming convention, where `NNN` is the numeric priority (see [Template Libraries → Snippet Priority](../template-libraries.md#snippet-priority)).

### `features-*` (shared-state initialization)

| Snippet | Purpose |
|---------|---------|
| `features-100-haproxytech-ssl-redirect` | Registers SSL-redirect host/code pairs in `gf["sslRedirectHosts"]` |
| `features-100-haproxytech-ssl-passthrough` | Scans ingresses for `haproxy.org/ssl-passthrough` and registers backends in `gf["sslPassthroughBackends"]` |

### `frontend-filters-*` (HTTP-frontend request/response filters)

| Snippet | Annotations Processed |
|---------|----------------------|
| `frontend-filters-100-haproxytech-basic-headers` | `haproxy.org/forwarded-for`, `haproxy.org/src-ip-header` |
| `frontend-filters-200-haproxytech-access-control` | `haproxy.org/allow-list`, `haproxy.org/deny-list` |
| `frontend-filters-300-haproxytech-cors` | `haproxy.org/cors-*` |
| `frontend-filters-500-haproxytech-logging` | `haproxy.org/request-capture`, `haproxy.org/request-capture-len` |

### `backend-directives-*` (per-backend directives)

| Snippet | Annotations Processed |
|---------|----------------------|
| `backend-directives-100-haproxytech-pod-maxconn` | `haproxy.org/pod-maxconn` |
| `backend-directives-100-haproxytech-timeouts` | `haproxy.org/timeout-server`, `/timeout-connect`, `/timeout-queue`, `/timeout-tunnel`, `/timeout-check` |
| `backend-directives-150-haproxytech-load-balance` | `haproxy.org/load-balance` |
| `backend-directives-200-haproxytech-health-checks` | `haproxy.org/check` |
| `backend-directives-210-haproxytech-advanced-health-checks` | `haproxy.org/check-http`, `haproxy.org/check-interval` |
| `backend-directives-250-haproxytech-rate-limiting` | `haproxy.org/rate-limit-*` |
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
| `features-130-haproxytech-request-redirect` | `features-*` | Registers host→location for `haproxy.org/request-redirect` / `haproxy.org/request-redirect-code` in the shared `redirect-loc-<code>.map` |
| `map-reqhdr-host-250-haproxytech` | `map-reqhdr-host-*` | Relocates `haproxy.org/set-host` to `reqhdr-host.map` + a shared frontend rule |

### Injecting custom annotations

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

## Access control & IP filtering

### `haproxy.org/allow-list`

**Status**: ✅ Supported

**Description**: Whitelist IP addresses or CIDR ranges that are allowed to access the ingress.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: protected-api
  annotations:
    haproxy.org/allow-list: "192.168.1.0/24, 10.0.0.1"
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

**Related annotations**: Can be combined with `deny-list`

---

### `haproxy.org/deny-list`

**Status**: ✅ Supported

**Description**: Blacklist IP addresses or CIDR ranges that are denied access to the ingress.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: public-api
  annotations:
    haproxy.org/deny-list: "203.0.113.0/24, 198.51.100.50"
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

**Related annotations**: Can be combined with `allow-list`

---

### `haproxy.org/whitelist`

**Status**: ✅ Supported (deprecated alias)

**Description**: Deprecated alias for `allow-list`, honoured only when `allow-list` is absent on the same Ingress. Kept for upstream parity — prefer `allow-list` for new Ingresses.

**Note**: If both `allow-list` and `whitelist` are set, `allow-list` wins and `whitelist` is ignored.

---

### `haproxy.org/blacklist`

**Status**: ✅ Supported (deprecated alias)

**Description**: Deprecated alias for `deny-list`, honoured only when `deny-list` is absent on the same Ingress. Kept for upstream parity — prefer `deny-list` for new Ingresses.

**Note**: If both `deny-list` and `blacklist` are set, `deny-list` wins and `blacklist` is ignored.

---

## CORS configuration

### `haproxy.org/cors-enable`

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
# Capture the request Origin into a transaction variable
http-request set-var(txn.cors_origin) req.hdr(origin) if { var(txn.host) -m str api.example.com }

# CORS response headers, added via http-after-response so they apply to backend
# responses AND to a HAProxy-generated preflight response (see cors-respond-to-options)
http-after-response set-header Access-Control-Allow-Origin '*' if { var(txn.host) -m str api.example.com } { var(txn.cors_origin) -m found }
http-after-response set-header Access-Control-Allow-Methods 'GET, POST, PUT, DELETE' if { var(txn.host) -m str api.example.com } { var(txn.cors_origin) -m found }
http-after-response set-header Access-Control-Allow-Headers 'Content-Type, Authorization' if { var(txn.host) -m str api.example.com } { var(txn.cors_origin) -m found }
```

This mirrors the upstream HAProxy Kubernetes Ingress Controller. By default the CORS headers are added to whatever response the backend returns; to have HAProxy answer the preflight itself, set [`cors-respond-to-options`](#haproxyorgcors-respond-to-options).

**Dependencies**: All other `cors-*` annotations require `cors-enable: "true"`

---

### `haproxy.org/cors-allow-origin`

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
http-after-response set-header Access-Control-Allow-Origin '*' if { var(txn.cors_origin) -m found }

# Exact or regex match: the annotation value is a regex matched against the
# request Origin, and the header echoes the matched origin (never the raw regex)
http-after-response set-header Access-Control-Allow-Origin '%[var(txn.cors_origin)]' if { var(txn.cors_origin) -m reg ^https://(.+\.)?(example\.com)(:\d{1,5})?$ }
```

**Dependencies**: Requires `cors-enable: "true"`

---

### `haproxy.org/cors-allow-methods`

**Status**: ✅ Supported

**Description**: Specifies allowed HTTP methods for CORS requests.

**Valid values**: GET, POST, PUT, DELETE, HEAD, CONNECT, OPTIONS, TRACE, PATCH

**Usage**:

```yaml
haproxy.org/cors-allow-methods: "GET, POST, PUT, DELETE, OPTIONS"
```

**Generated HAProxy Configuration**:

```haproxy
http-after-response set-header Access-Control-Allow-Methods "GET, POST, PUT, DELETE, OPTIONS"
```

**Dependencies**: Requires `cors-enable: "true"`

---

### `haproxy.org/cors-allow-headers`

**Status**: ✅ Supported

**Description**: Specifies allowed request headers for CORS requests.

**Usage**:

```yaml
haproxy.org/cors-allow-headers: "Content-Type, Authorization, X-Requested-With"
```

**Generated HAProxy Configuration**:

```haproxy
http-after-response set-header Access-Control-Allow-Headers "Content-Type, Authorization, X-Requested-With"
```

**Dependencies**: Requires `cors-enable: "true"`

---

### `haproxy.org/cors-allow-credentials`

**Status**: ✅ Supported

**Description**: Indicates whether credentials (cookies, authorization headers) can be included in CORS requests.

**Usage**:

```yaml
haproxy.org/cors-allow-credentials: "true"
```

**Generated HAProxy Configuration**:

```haproxy
http-after-response set-header Access-Control-Allow-Credentials "true"
```

**Dependencies**: Requires `cors-enable: "true"`

**Note**: When `cors-allow-credentials: "true"`, `cors-allow-origin` can't be `*` (must be specific origin)

---

### `haproxy.org/cors-max-age`

**Status**: ✅ Supported

**Description**: Specifies how long (in seconds) preflight request results can be cached.

**Usage**:

```yaml
haproxy.org/cors-max-age: "3600"  # 1 hour
```

**Generated HAProxy Configuration**:

```haproxy
http-after-response set-header Access-Control-Max-Age "3600"
```

**Dependencies**: Requires `cors-enable: "true"`

---

### `haproxy.org/cors-respond-to-options`

**Status**: ✅ Supported

**Description**: When `"true"`, HAProxy answers the CORS preflight (an `OPTIONS` request) itself with a `204 No Content` instead of forwarding it to the backend. The `Access-Control-*` headers are added via `http-after-response`, so they apply to this synthetic response too. This matches the upstream HAProxy Kubernetes Ingress Controller, where preflight answering is opt-in.

**Usage**:

```yaml
haproxy.org/cors-enable: "true"
haproxy.org/cors-allow-origin: "https://app.example.com"
haproxy.org/cors-respond-to-options: "true"
```

**Generated HAProxy Configuration**:

```haproxy
http-request return status 204 if { var(txn.host) -m str api.example.com } METH_OPTIONS
```

**Dependencies**: Requires `cors-enable: "true"`

---

## Rate limiting

### `haproxy.org/rate-limit-requests`

**Status**: ✅ Supported

**Description**: Maximum number of requests allowed in the specified period (per source IP).

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

### `haproxy.org/rate-limit-period`

**Status**: ✅ Supported

**Description**: Time window for rate limiting. Supports duration format (for example, `10s`, `1m`, `1h`).

**Default**: `1s` (1 second)

**Usage**:

```yaml
haproxy.org/rate-limit-period: "1m"
```

**Dependencies**: Requires `rate-limit-requests` to be set

---

### `haproxy.org/rate-limit-size`

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

### `haproxy.org/rate-limit-status-code`

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

### `haproxy.org/rate-limit-whitelist`

**Status**: ✅ Supported

**Description**: Comma-separated IP addresses or CIDR ranges that are exempt from the rate-limit deny. Whitelisted sources are still tracked in the stick-table but are never denied, mirroring haproxy-ingress' `limit-whitelist`.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: rate-limited-api
  annotations:
    haproxy.org/rate-limit-requests: "10"
    haproxy.org/rate-limit-period: "10s"
    haproxy.org/rate-limit-whitelist: "10.0.0.0/8, 192.168.1.5"
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
                  number: 80
```

**Generated HAProxy Configuration**:

```haproxy
stick-table type ip size 100k expire 10s store http_req_rate(10s) peers localinstance
http-request track-sc0 src
http-request deny deny_status 403 if { sc_http_req_rate(0) gt 10 } !{ src 10.0.0.0/8 192.168.1.5 }
```

The trailing `!{ src … }` guard makes the deny fire only for non-whitelisted sources.

**Dependencies**: Requires `rate-limit-requests` to be set

---

## Request/response header manipulation

### `haproxy.org/request-set-header`

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

### `haproxy.org/response-set-header`

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

### `haproxy.org/set-host`

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

### `haproxy.org/forwarded-for`

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

## Path manipulation

### `haproxy.org/path-rewrite`

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

## Request redirect

### `haproxy.org/request-redirect`

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

The library records each host→location pair in a per-code map, then base.yaml emits one host-keyed redirect rule per code in the HTTP frontend, so adding or changing a redirect target is a reload-free map update:

```haproxy
# maps/redirect-loc-301.map
old.example.com https://new.example.com
```

```haproxy
# base/redirect-location (code 301)
http-request redirect location %[var(txn.host),map(maps/redirect-loc-301.map)] code 301 if { var(txn.host),map(maps/redirect-loc-301.map) -m found }
```

**Dependencies**: None

**Related annotations**: `request-redirect-code`

---

### `haproxy.org/request-redirect-code`

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

### `haproxy.org/ssl-redirect`

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

### `haproxy.org/ssl-redirect-code`

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

### `haproxy.org/ssl-redirect-port`

**Status**: ✅ Supported

**Description**: Redirect HTTP requests to HTTPS on an explicit port instead of the default `https://` scheme. The original request URI is preserved. Requires `ssl-redirect: "true"` (or the `ssl_redirect_default` extra-context flag), and uses `ssl-redirect-code` for the status code (default `302`). Must be a positive integer port — other values fail the render.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ssl-redirect-port-example
  annotations:
    haproxy.org/ssl-redirect: "true"
    haproxy.org/ssl-redirect-port: "8443"
spec:
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
                  number: 80
```

**Generated HAProxy Configuration**:

The library records each host→port pair in a per-code map (`ssl-redirect-port-302.map` for the default code), so changing a port is a reload-free map update:

```haproxy
# maps/ssl-redirect-port-302.map
secure.example.com 8443
```

It then emits one host-keyed redirect rule per code in the HTTP frontend:

```haproxy
# haproxytech/ssl-redirect-port (code 302)
http-request redirect location https://%[hdr(host),field(1,:)]:%[var(txn.host),map(maps/ssl-redirect-port-302.map)]%[capture.req.uri] code 302 if !{ ssl_fc } { var(txn.host),map(maps/ssl-redirect-port-302.map) -m found }
```

**Dependencies**: Requires `ssl-redirect: "true"`

**Related annotations**: `ssl-redirect`, `ssl-redirect-code`

---

### `haproxy.org/ssl-passthrough`

**Status**: ✅ Supported

**Description**: Enable TCP mode SSL passthrough (Layer 4) for specific ingresses while allowing SSL termination for others. Uses SNI-based routing with Unix socket loopback to support mixed passthrough and termination traffic.

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

- Uses Unix socket loopback pattern to support mixed passthrough and termination
- TCP frontend extracts SNI without terminating SSL
- Passthrough traffic routes directly to backend pods
- Non-passthrough traffic routes to Unix socket frontend for SSL termination
- PROXY protocol v2 preserves client IP information

**Dependencies**: None

**Warning**: For passthrough traffic, HTTP-level features (headers, path rewriting, etc.) are unavailable. Non-passthrough traffic on other hosts continues to support all HTTP features.

---

### `haproxy.org/server-ssl`

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

### `haproxy.org/server-proto`

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

### `haproxy.org/server-crt`

**Status**: ✅ Supported

**Description**: Client certificate for mTLS (mutual TLS) to backend. References a Secret containing `tls.crt` and `tls.key`. Supports cross-namespace format `namespace/secretname`. Unlike Gateway API cross-namespace references, this isn't gated by a ReferenceGrant — the Secret resolves against any namespace the controller watches, bounded only by the `watchedResources` scope and the controller's RBAC.

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

### `haproxy.org/server-ca`

**Status**: ✅ Supported

**Description**: CA certificate for verifying backend server certificates. References a Secret containing `tls.crt`. Supports cross-namespace format `namespace/secretname`. Unlike Gateway API cross-namespace references, this isn't gated by a ReferenceGrant — the Secret resolves against any namespace the controller watches, bounded only by the `watchedResources` scope and the controller's RBAC.

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

## Backend health checks & connection management

### `haproxy.org/check`

**Status**: ✅ Supported

**Description**: Toggle active health checks for the backend's servers. Health checks are on by default; set the value to `"false"` to turn them off. Only `"true"` and `"false"` are accepted — any other value fails the render. The toggle renders as the first token of the backend's `default-server` line, so it applies to every server in the pool and endpoint changes still avoid a reload.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: no-check-backend
  annotations:
    haproxy.org/check: "false"
spec:
  rules:
    - host: internal.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: internal-service
                port:
                  number: 8080
```

**Generated HAProxy Configuration**:

With `haproxy.org/check: "false"`, the backend's `default-server` carries `no-check`:

```haproxy
default-server no-check
```

With `haproxy.org/check: "true"` (or the annotation absent), health checks stay on — the default:

```haproxy
default-server check
```

**Dependencies**: None

**Related annotations**: `check-http`, `check-interval`, `timeout-check`

---

### `haproxy.org/check-http`

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

### `haproxy.org/check-interval`

**Status**: ✅ Supported

**Description**: Set the interval between active health checks (for example, `10s`, `1m`). The value becomes the `inter` parameter on the backend's `default-server`. Ignored when health checks are disabled with `haproxy.org/check: "false"`, since an interval on an unchecked server has no meaning.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: slow-check-backend
  annotations:
    haproxy.org/check-interval: "10s"
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
default-server check inter 10s
```

**Dependencies**: None (has no effect when `haproxy.org/check: "false"`)

**Related annotations**: `check`, `check-http`, `timeout-check`

---

### `haproxy.org/timeout-check`

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

### `haproxy.org/pod-maxconn`

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
    The power-of-2 quantization means the effective per-pod `maxconn` only changes when the ready pod count crosses a power-of-2 boundary (1, 2, 4, 8, 16, and so on). This prevents unnecessary HAProxy reloads during scaling events. The trade-off is that the actual total capacity may be lower than the annotation value when the pod count isn't an exact power of 2.

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

| Ready pods | Effective count | `maxconn` per pod |
|------------|-----------------|-----------------|
| 1          | 1               | 200             |
| 2          | 2               | 100             |
| 3-4        | 4               | 50              |
| 5-8        | 8               | 25              |
| 9-16       | 16              | 13              |

**Fallback behavior**: If no Running and Ready HAProxy pods are discovered yet (for example, during initial startup), the full annotation value is used temporarily until pod discovery completes.

**Dependencies**: Requires HAProxy pod discovery to be operational for automatic division

---

### `haproxy.org/scale-server-slots`

**Status**: ✅ Supported

**Description**: Override the number of pre-allocated server slots for the backend. HAProxy reserves this many `server` lines so endpoints can be added or removed via the runtime API without a reload. Must be a positive integer — other values fail the render.

**Default**: `10` slots (when the annotation is absent).

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: scaled-backend
  annotations:
    haproxy.org/scale-server-slots: "5"
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

With `scale-server-slots: "5"`, the backend pre-allocates five slots (`SRV_1`–`SRV_5`); active endpoints fill the leading slots and the rest stay reserved as disabled placeholders (RFC 5737 `192.0.2.1:1`):

```haproxy
default-server check
server SRV_1 10.0.1.5:8080 enabled
server SRV_2 192.0.2.1:1 disabled
server SRV_3 192.0.2.1:1 disabled
server SRV_4 192.0.2.1:1 disabled
server SRV_5 192.0.2.1:1 disabled
```

!!! warning "Size the slot count to your peak replica count"
    Set the slot count to at least the backend's maximum expected replica count. When a backend has more ready endpoints than slots, the excess endpoints get no `server` line and receive no traffic — HAPTIC drops them silently, with no error, warning, or Event, and the render still succeeds. The default of 10 applies to every backend without this annotation, so a Deployment scaled past 10 ready pods starves the extra pods until you raise `scale-server-slots`.

There's no fixed maximum — any positive integer is accepted. In practice the count is bounded by total config size and, when the shared-memory stats file is enabled (`haproxy.shmStats.maxObjects`, default 50000, shared across the whole config), the shm-stats object budget.

**Dependencies**: None

---

## Load balancing algorithms

### `haproxy.org/load-balance`

**Status**: ✅ Supported

**Description**: Load balancing algorithm for distributing traffic across backend servers.

**Default**: `roundrobin`

**Valid values**: `roundrobin`, `static-rr`, `leastconn`, `first`, `source`, `random`, plus the parameterized `uri`, `url_param(name)`, `hdr(name)`, `rdp-cookie(name)`

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

## Session persistence

### `haproxy.org/cookie-persistence`

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

### `haproxy.org/cookie-persistence-no-dynamic`

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

**Warning**: Static cookies differ across controller instances, breaking session affinity. Only use in single-instance deployments.

---

## Timeouts

### `haproxy.org/timeout-server`

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

### `haproxy.org/timeout-client`

**Status**: ❌ Not Implemented

**Description**: Maximum inactivity time on the client side. The haproxytech library doesn't emit a per-backend `timeout client` (it would have no effect — `timeout client` only applies in frontend/defaults sections).

**Workaround**: Set the global `timeout client` via the `defaults-settings-300-timeouts` snippet override. See [Base Library](base.md#injecting-custom-configuration).

---

### `haproxy.org/timeout-connect`

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

### `haproxy.org/timeout-http-request`

**Status**: ❌ Not Implemented

**Description**: The haproxytech library doesn't process this annotation. The equivalent exists in the `haproxy-ingress` library as `haproxy-ingress.github.io/timeout-http-request`.

**Workaround**: Either add the `haproxy-ingress.github.io/timeout-http-request` annotation (see [haproxy-ingress library](haproxy-ingress.md)), or override `defaults-settings-300-timeouts` globally.

---

### `haproxy.org/timeout-http-keep-alive`

**Status**: ❌ Not Implemented

**Description**: The haproxytech library doesn't process this annotation. The equivalent exists in the `haproxy-ingress` library as `haproxy-ingress.github.io/timeout-keep-alive`.

**Workaround**: Either add the `haproxy-ingress.github.io/timeout-keep-alive` annotation (see [haproxy-ingress library](haproxy-ingress.md)), or override `defaults-settings-300-timeouts` globally.

---

### `haproxy.org/timeout-queue`

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

### `haproxy.org/timeout-tunnel`

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

## Request capture & logging

### `haproxy.org/request-capture`

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

### `haproxy.org/request-capture-len`

**Status**: ✅ Supported

**Description**: Maximum length for captured request data.

**Default**: `128`

**Usage**:

```yaml
haproxy.org/request-capture-len: "256"
```

**Dependencies**: Applies to `request-capture` expressions

---

## Source IP detection

### `haproxy.org/src-ip-header`

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

## Advanced backend configuration

### `haproxy.org/backend-config-snippet`

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

**Note**: The snippet is injected verbatim into the rendered backend section, but HAPTIC still validates the resulting config with `haproxy -c` before deploying it. The Ingress admission webhook (`failurePolicy: Fail`) rejects a syntax error at apply time, and the daemon's config load gate refuses to load a config that doesn't parse — a typo fails the render rather than being silently deployed. HAPTIC doesn't vet what the directives *do*, only that the config parses.

---

### `haproxy.org/send-proxy-protocol`

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

### `haproxy.org/standalone-backend`

**Status**: ❌ Not Implemented (Not Planned)

**Description**: Create a dedicated backend for this ingress instead of sharing backends across ingresses.

**Note**: This controller's architecture already generates standalone backends (one backend per ingress+service+port combination) rather than sharing backends across ingresses. Each unique combination of `<namespace>_<ingress-name>_svc_<service-name>_<port-name>` gets its own dedicated backend, making this annotation redundant. Implementation isn't planned.

---

## Authentication

### `haproxy.org/auth-type`

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
# The realm "API Access" is normalized to "API-Access" (see note below)
http-request auth realm "API-Access" unless { http_auth(auth_default_auth-credentials) }
```

**Dependencies**: Requires `auth-secret` to be set

**Related annotations**: `auth-secret`, `auth-realm`

**Implementation notes**:

- Secret format: Opaque secret where key=username, value=base64-encoded password hash
- Supports cross-namespace secrets: `namespace/secretname`
- Automatic deduplication: multiple ingresses sharing the same secret generate a single userlist
- HAProxy parses `$1$` (MD5 crypt), `$5$` (SHA-256), `$6$` (SHA-512), and `$2y$` (bcrypt). It **doesn't** parse `$apr1$` (Apache MD5 — the htpasswd *default* without `-B` / `-5` / `-6`); generate hashes with `htpasswd -nbB` (bcrypt), `-nb5` (SHA-256), or `-nb6` (SHA-512). See [Performance — Password hash validation](../operations/performance.md#password-hash-performance) for the cost/perf trade-off.

!!! note "Implementation Difference from HAProxy Ingress Controller"
    This controller uses **per-secret** userlist naming (`auth_{secretNs}_{secretName}`) rather than the official HAProxy Ingress Controller's per-ingress naming (`{namespace}-{ingressName}`). This deduplicates userlists when multiple Ingresses reference the same secret, significantly improving configuration validation performance for expensive password hashes like bcrypt (~85 ms per hash validation).

---

### `haproxy.org/auth-secret`

**Status**: ✅ Supported

**Description**: Reference to Kubernetes Secret containing authentication credentials. Supports cross-namespace format `namespace/secretname`. Unlike Gateway API cross-namespace references, this isn't gated by a ReferenceGrant — the Secret resolves against any namespace the controller watches, bounded only by the `watchedResources` scope and the controller's RBAC.

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

Hash validation runs on every config parse and reconciliation, so with many users or bcrypt it can dominate reconciliation time — prefer SHA-512 unless you need bcrypt's tunable work factor. See [Performance — Password hash validation](../operations/performance.md#password-hash-performance).

**Dependencies**: Requires `auth-type: basic-auth`

**Implementation notes**:

- Value must be **only** the password hash, **not** "username:hash" (htpasswd format)
- Multiple usernames supported: add multiple keys to the secret
- The hash must be in a format HAProxy parses (`$1$` MD5 crypt, `$5$` SHA-256, `$6$` SHA-512, or `$2y$` bcrypt). `$apr1$` (Apache MD5 — what plain `htpasswd -nb` produces) **isn't** parsed; pass `-B`, `-5`, or `-6` to `htpasswd` instead.

---

### `haproxy.org/auth-realm`

**Status**: ✅ Supported

**Description**: Authentication realm displayed in browser's authentication prompt.

**Default**: `Protected-Content`

**Usage**:

```yaml
haproxy.org/auth-realm: "API Access"
```

**Dependencies**: Requires `auth-type: basic-auth` and `auth-secret`

**Note**: The HAProxy Data Plane API forbids spaces in the realm (regex: `^[^\s]+$`). Like the upstream controller, the library automatically replaces spaces with dashes, so `"API Access"` renders as `realm "API-Access"` — you don't need to hyphenate the value yourself.

---

## Known limitations

### Not implemented

1. **Service-level annotations** - Annotations on Service resources aren't supported. Only Ingress annotations are implemented.

2. **Deprecated annotations** - `whitelist` and `blacklist` are honoured as deprecated aliases of `allow-list` / `deny-list` (only when the canonical key is absent). `ingress.class` isn't implemented — set `spec.ingressClassName` instead.

3. **RequestMirror equivalent** - No annotation-based traffic mirroring. Consider using Gateway API with an external Stream Processing Offload Engine (SPOE) agent for this feature.

### Implementation differences from HAProxy Tech

1. **Template-based approach** - This implementation uses Scriggo templates rather than Go code, allowing users to customize behavior through template overrides.

2. **Resource-agnostic architecture** - The controller doesn't have built-in annotation handling. All annotation support is provided through pluggable template libraries.

3. **Validation tests** - Each annotation ships validation tests in the chart's `validationTests`, which the controller re-runs on every config load — not just in CI.

4. **Secret format for auth** - Password values must be base64-encoded hashes only, not htpasswd format (`username:hash`). The key is the username and the value is the hash — templates need no string parsing.

## Watched Resources

This library watches the following additional resources:

- **Secrets** (`v1/secrets`) — read for basic-auth credentials (`auth-secret`) and backend TLS material (`server-ca`, `server-crt`)

## Implementation status summary

The library supports **50** `haproxy.org/*` annotations, grouped by category below. Deprecated aliases (`whitelist`, `blacklist`) are excluded from this count. The generated migration-coverage table on the [Migrating from other controllers](../migrating.md) page is the machine-readable source of truth for each annotation's status.

**Supported by category:**

| Category | Count | Annotations |
|----------|-------|-------------|
| Access control | 2 | `allow-list`, `deny-list` |
| Authentication | 3 | `auth-type`, `auth-secret`, `auth-realm` |
| CORS | 7 | `cors-enable`, `cors-allow-origin`, `cors-allow-methods`, `cors-allow-headers`, `cors-allow-credentials`, `cors-max-age`, `cors-respond-to-options` |
| Rate limiting | 5 | `rate-limit-requests`, `rate-limit-period`, `rate-limit-size`, `rate-limit-status-code`, `rate-limit-whitelist` |
| Header manipulation | 3 | `forwarded-for`, `request-set-header`, `response-set-header` |
| Path manipulation | 1 | `path-rewrite` |
| Request redirect | 2 | `request-redirect`, `request-redirect-code` |
| SSL/TLS | 4 | `ssl-redirect`, `ssl-redirect-code`, `ssl-redirect-port`, `ssl-passthrough` |
| Health checks | 3 | `check`, `check-http`, `check-interval` |
| Load balancing | 1 | `load-balance` |
| Session persistence | 2 | `cookie-persistence`, `cookie-persistence-no-dynamic` |
| Timeouts | 5 | `timeout-server`, `timeout-connect`, `timeout-queue`, `timeout-tunnel`, `timeout-check` |
| Logging | 3 | `src-ip-header`, `request-capture`, `request-capture-len` |
| Host manipulation | 1 | `set-host` |
| Connection management | 1 | `pod-maxconn` |
| Backend server options | 4 | `server-ssl`, `server-proto`, `server-crt`, `server-ca` |
| Proxy protocol | 1 | `send-proxy-protocol` |
| Advanced backend config | 2 | `backend-config-snippet`, `scale-server-slots` |

**Supported deprecated aliases** (honoured only when the canonical key is absent on the same Ingress):

- `whitelist` → `allow-list`, `blacklist` → `deny-list`

**Not implemented in the `haproxy.org/*` namespace:**

- `timeout-client`, `timeout-http-request`, `timeout-http-keep-alive` — `timeout-http-request` and `timeout-http-keep-alive` are available under `haproxy-ingress.github.io/*` instead (see [haproxy-ingress library](haproxy-ingress.md)); `timeout-client` only takes effect in a global override (see [Base Library](base.md#injecting-custom-configuration))
- `standalone-backend` — not needed; this controller already emits a dedicated backend per `<namespace>_<ingress-name>_svc_<service-name>_<port>` tuple

## Access-log fields

The library contributes `captured_headers` (HAProxy's `%hr`) to the
[structured access log](../haproxy-deployment.md#access-logging) when any Ingress
sets `haproxy.org/request-capture` — without it, the annotation configures
captures that nothing reads.

## See also

- [HAProxy Ingress Controller Documentation](https://www.haproxy.com/documentation/kubernetes-ingress/community/configuration-reference/ingress)
- [HAProxy Ingress Controller Source Code](https://github.com/haproxytech/kubernetes-ingress)
- [Gateway API Library](gateway.md) - For Gateway API resources (HTTPRoute, GRPCRoute)
- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Extension points
- [Ingress Library](ingress.md) - Ingress resource support
