# Nginx Ingress Library

The Nginx Ingress library provides compatibility with the [nginx-ingress controller](https://kubernetes.github.io/ingress-nginx/) annotations for Kubernetes Ingress resources.

## Overview

This library enables `nginx.ingress.kubernetes.io/*` annotations on Ingress resources, providing a migration path for users coming from the nginx-ingress controller. It supports backend configuration, session affinity, rate limiting, URL rewriting, redirects, CORS, access control, canary deployments, authentication, SSL passthrough, and mTLS certificate passthrough.

This library is disabled by default.

## Configuration

```yaml
controller:
  templateLibraries:
    nginxIngress:
      enabled: true  # Disabled by default
```

## Extension Points

### Extension Points Used

The Nginx Ingress library implements these extension points:

| Extension Point | This Library's Snippets | What They Generate |
|-----------------|-------------------------|-------------------|
| Backend Directives | `backend-directives-670-nginx-ingress-session-affinity` | Cookie-based session affinity |
| Backend Directives | `backend-directives-700-nginx-ingress-timeouts` | Backend timeouts |
| Backend Directives | `backend-directives-710-nginx-ingress-load-balance` | Load balancing algorithm |
| Backend Directives | `backend-directives-720-nginx-ingress-proxy-body-size` | Request body size limit |
| Backend Directives | `backend-directives-730-nginx-ingress-backend-protocol` | Backend protocol (HTTPS, gRPC) |
| Backend Directives | `backend-directives-740-nginx-ingress-proxy-protocol` | PROXY protocol to backend |
| Backend Directives | `backend-directives-750-nginx-ingress-rewrite-target` | URL rewriting |
| Backend Directives | `backend-directives-760-nginx-ingress-auth` | Basic auth enforcement |
| Backend Directives | `backend-directives-770-nginx-ingress-rate-limiting` | Rate limiting / connection limiting |
| Backend Directives | `backend-directives-780-nginx-ingress-upstream-hash` | Hash-based load balancing |
| Backend Directives | `backend-directives-900-nginx-ingress-config-snippet` | Raw backend config injection |
| Frontend Filters | `frontend-filters-700-nginx-ingress-access-control` | IP allowlist/denylist |
| Frontend Filters | `frontend-filters-710-nginx-ingress-ssl-redirect` | HTTP to HTTPS redirect |
| Frontend Filters | `frontend-filters-720-nginx-ingress-hsts` | HSTS headers |
| Frontend Filters | `frontend-filters-730-nginx-ingress-cors` | CORS headers |
| Frontend Filters | `frontend-filters-740-nginx-ingress-custom-headers` | Custom request/response headers |
| Frontend Filters | `frontend-filters-750-nginx-ingress-app-root` | Root path redirect |
| Frontend Filters | `frontend-filters-760-nginx-ingress-permanent-redirect` | Permanent redirect (301) |
| Frontend Filters | `frontend-filters-770-nginx-ingress-temporal-redirect` | Temporal redirect (302) |
| Frontend Filters | `frontend-filters-780-nginx-ingress-canary` | Canary routing rules |
| Frontend Filters | `frontend-filters-790-nginx-ingress-mtls-error` | mTLS error page and cert passthrough |
| Features | `features-100-nginx-ingress-ssl-passthrough` | SSL passthrough registration |
| Backends | `backends-501-nginx-ingress-ssl-passthrough` | SSL passthrough backends |
| Global Top | `global-top-700-nginx-ingress-auth` | Userlist definitions for basic auth |

---

## Backend Configuration

### Timeouts

**Status**: ✅ Supported

**Annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `proxy-connect-timeout` | Backend connection timeout (seconds) | - |
| `proxy-read-timeout` | Backend response timeout (seconds) | - |
| `proxy-send-timeout` | Backend send timeout (seconds) | - |

!!! note "Timeout Value Format"
    Nginx-ingress timeout values are plain seconds (e.g., `"60"`). The library automatically appends the `s` suffix for HAProxy.

!!! note "Server Timeout Mapping"
    Both `proxy-read-timeout` and `proxy-send-timeout` map to HAProxy's single `timeout server` directive. If both are set, the larger value is used.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/proxy-connect-timeout: "10"
  nginx.ingress.kubernetes.io/proxy-read-timeout: "60"
  nginx.ingress.kubernetes.io/proxy-send-timeout: "30"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    timeout connect 10s
    timeout server 60s
```

---

### nginx.ingress.kubernetes.io/load-balance

**Status**: ✅ Supported

**Description**: Load balancing algorithm for the backend.

**Valid values**: `round_robin`, `least_conn`, `ip_hash`, `random`, `ewma`

**Mapping to HAProxy**:

| Nginx Value | HAProxy Value |
|-------------|---------------|
| `round_robin` | `roundrobin` |
| `least_conn` | `leastconn` |
| `ip_hash` | `source` |
| `random` | `random` |
| `ewma` | `leastconn` (closest equivalent) |

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/load-balance: "least_conn"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    balance leastconn
```

---

### nginx.ingress.kubernetes.io/proxy-body-size

**Status**: ✅ Supported

**Description**: Maximum allowed request body size. Requests exceeding this limit receive a 413 response.

**Valid values**: Plain number (bytes), or with `k`/`m`/`g` suffix. Value `0` means unlimited (no directive emitted).

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/proxy-body-size: "10m"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    http-request deny deny_status 413 if { req.body_size gt 10485760 }
```

---

### nginx.ingress.kubernetes.io/backend-protocol

**Status**: ✅ Supported

**Description**: Protocol used to communicate with the backend.

**Valid values**: `HTTP`, `HTTPS`, `GRPC`, `GRPCS`

**Mapping to HAProxy server options**:

| Value | HAProxy Server Flags |
|-------|---------------------|
| `HTTP` | (default, no additional flags) |
| `HTTPS` | `ssl verify none` |
| `GRPC` | `proto h2` |
| `GRPCS` | `ssl verify none proto h2` |

!!! warning "Unsupported Protocols"
    `AJP` and `FCGI` are not supported by HAProxy and will fail with an error.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/backend-protocol: "GRPC"
```

---

### nginx.ingress.kubernetes.io/use-proxy-protocol

**Status**: ✅ Supported

**Description**: Send PROXY protocol v2 header to the backend.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/use-proxy-protocol: "true"
```

**Generated HAProxy Configuration**:

```haproxy
server SRV_1 10.0.0.1:8080 send-proxy-v2
```

---

### nginx.ingress.kubernetes.io/configuration-snippet

**Status**: ✅ Supported

**Description**: Raw HAProxy configuration to inject into the backend section.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/configuration-snippet: |
    http-send-name-header X-Backend-Server
    retries 5
```

---

### nginx.ingress.kubernetes.io/upstream-hash-by

**Status**: ✅ Supported

**Description**: Hash-based load balancing using a nginx variable or HAProxy fetch expression.

**Supported nginx variable translations**:

| Nginx Variable | HAProxy Fetch |
|----------------|---------------|
| `$request_uri` | `url` |
| `$remote_addr` | `src` |
| `$cookie_XXXX` | `req.cook(XXXX)` |
| `$http_xxxx` | `req.hdr(xxxx)` (underscores replaced with hyphens) |
| `$arg_XXXX` | `url_param(XXXX)` |

Values not starting with `$` are passed through as-is (assumed to be HAProxy fetch expressions). Unrecognized `$variables` will fail with an error.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/upstream-hash-by: "$request_uri"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    balance hash url
    hash-type consistent
```

---

### Connection Limits

**Status**: ✅ Supported

**Annotations**:

| Annotation | Description |
|------------|-------------|
| `limit-rps` | Maximum requests per second per source IP |
| `limit-connections` | Maximum concurrent connections per source IP |

!!! note "Mutual Exclusivity"
    HAProxy supports only one stick-table per backend. If both `limit-rps` and `limit-connections` are set, `limit-rps` takes precedence. Exceeding the limit returns HTTP 429.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/limit-rps: "100"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    stick-table type ip size 100k expire 1s store http_req_rate(1s)
    http-request track-sc0 src
    http-request deny deny_status 429 if { sc_http_req_rate(0) gt 100 }
```

---

## Session Affinity

### nginx.ingress.kubernetes.io/affinity

**Status**: ✅ Supported

**Description**: Enable cookie-based session affinity.

**Valid values**: `cookie`

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `session-cookie-name` | Cookie name | `INGRESSCOOKIE` |
| `session-cookie-hash` | Hash algorithm (`md5`, `sha1`, `index`) | - |
| `session-cookie-path` | Cookie path | - |

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/affinity: "cookie"
  nginx.ingress.kubernetes.io/session-cookie-name: "SERVERID"
  nginx.ingress.kubernetes.io/session-cookie-path: "/app"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    cookie SERVERID insert indirect nocache dynamic path /app
    dynamic-cookie-key <sha256-of-namespace/name>
```

---

## URL Rewriting

### nginx.ingress.kubernetes.io/rewrite-target

**Status**: ✅ Supported

**Description**: Rewrite the URL path before forwarding to the backend.

!!! note "Capture Group Translation"
    Nginx capture groups use `$1`, `$2`, etc. The library automatically translates these to HAProxy's `\1`, `\2` syntax.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/rewrite-target: "/$1"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    http-request replace-path (.*) /\1
```

---

### nginx.ingress.kubernetes.io/app-root

**Status**: ✅ Supported

**Description**: Redirect requests to root path (`/`) to the specified path.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/app-root: "/dashboard"
```

**Generated HAProxy Configuration**:

```haproxy
http-request redirect location /dashboard code 302 if { path / } { hdr(host) -i example.com }
```

---

## Redirects

### nginx.ingress.kubernetes.io/ssl-redirect

**Status**: ✅ Supported

**Description**: Redirect HTTP requests to HTTPS.

**Related annotations**:

| Annotation | Description | Redirect Code |
|------------|-------------|---------------|
| `ssl-redirect` | Enable SSL redirect | `301` |
| `force-ssl-redirect` | Force SSL redirect | `308` |

If both are set, `force-ssl-redirect` takes precedence (code 308).

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/ssl-redirect: "true"
```

**Generated HAProxy Configuration**:

```haproxy
http-request redirect scheme https code 301 if !{ ssl_fc } { hdr(host) -i example.com }
```

---

### nginx.ingress.kubernetes.io/permanent-redirect

**Status**: ✅ Supported

**Description**: Redirect all requests to the specified URL with code 301.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/permanent-redirect: "https://new.example.com"
```

---

### nginx.ingress.kubernetes.io/temporal-redirect

**Status**: ✅ Supported

**Description**: Redirect all requests to the specified URL with code 302.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/temporal-redirect: "https://maintenance.example.com"
```

---

## HSTS

### nginx.ingress.kubernetes.io/hsts

**Status**: ✅ Supported

**Description**: Enable HTTP Strict Transport Security headers.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `hsts` | Enable HSTS | - |
| `hsts-max-age` | Max-age in seconds | `15724800` |
| `hsts-include-subdomains` | Include subdomains | - |
| `hsts-preload` | Enable preload | - |

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/hsts: "true"
  nginx.ingress.kubernetes.io/hsts-max-age: "31536000"
  nginx.ingress.kubernetes.io/hsts-include-subdomains: "true"
  nginx.ingress.kubernetes.io/hsts-preload: "true"
```

**Generated HAProxy Configuration**:

```haproxy
http-response set-header Strict-Transport-Security "max-age=31536000; includeSubDomains; preload" if { ssl_fc } { hdr(host) -i example.com }
```

---

## CORS

### nginx.ingress.kubernetes.io/enable-cors

**Status**: ✅ Supported

**Description**: Enable CORS handling for the ingress.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `enable-cors` | Enable CORS | - |
| `cors-allow-origin` | Allowed origins | `*` |
| `cors-allow-methods` | Allowed methods | `GET, PUT, POST, DELETE, PATCH, OPTIONS` |
| `cors-allow-headers` | Allowed headers | Common headers |
| `cors-allow-credentials` | Allow credentials | - |
| `cors-expose-headers` | Exposed headers | - |
| `cors-max-age` | Preflight cache time | `1728000` |

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/enable-cors: "true"
  nginx.ingress.kubernetes.io/cors-allow-origin: "https://example.com"
  nginx.ingress.kubernetes.io/cors-allow-credentials: "true"
```

---

## Access Control

### nginx.ingress.kubernetes.io/whitelist-source-range

**Status**: ✅ Supported

**Description**: Comma-separated list of CIDRs allowed to access this ingress.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/whitelist-source-range: "10.0.0.0/8, 192.168.0.0/16"
```

**Generated HAProxy Configuration**:

```haproxy
acl ni_allowlist_default_my-ingress src 10.0.0.0/8 192.168.0.0/16
http-request deny if { hdr(host) -i example.com } !ni_allowlist_default_my-ingress
```

---

### nginx.ingress.kubernetes.io/denylist-source-range

**Status**: ✅ Supported

**Description**: Comma-separated list of CIDRs denied access to this ingress.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/denylist-source-range: "203.0.113.0/24"
```

---

## Custom Headers

### Custom Request and Response Headers

**Status**: ✅ Supported

**Annotations**:

| Annotation | Description |
|------------|-------------|
| `custom-request-headers` | Pipe-separated `name:value` pairs for request headers |
| `custom-response-headers` | Pipe-separated `name:value` pairs for response headers |

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/custom-request-headers: "X-Custom-Header:value|X-Another:test"
  nginx.ingress.kubernetes.io/custom-response-headers: "X-Frame-Options:DENY"
```

**Generated HAProxy Configuration**:

```haproxy
http-request set-header X-Custom-Header 'value' if { hdr(host) -i example.com }
http-request set-header X-Another 'test' if { hdr(host) -i example.com }
http-response set-header X-Frame-Options 'DENY' if { hdr(host) -i example.com }
```

---

## Authentication

### nginx.ingress.kubernetes.io/auth-type

**Status**: ✅ Supported

**Description**: Enable basic authentication using credentials from a Kubernetes Secret.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `auth-type` | Authentication type (only `basic` supported) | - |
| `auth-secret` | Secret name (or `namespace/name`) | - |
| `auth-realm` | Authentication realm | `Restricted` |

!!! note "Secret Format"
    Nginx-ingress uses htpasswd format. The Secret must have a single key named `auth` containing `username:hash` entries separated by newlines. This differs from the haproxy-ingress library which uses one key per user.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/auth-type: "basic"
  nginx.ingress.kubernetes.io/auth-secret: "basic-auth"
  nginx.ingress.kubernetes.io/auth-realm: "Protected Area"
```

**Secret format**:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: basic-auth
type: Opaque
stringData:
  auth: |
    admin:$2y$05$mO1VWak5QnbhNgJ4QwdAdXbfz.8b3ceH6U5KOVCKxR2IkNAfJgLi5pIKW
    user:$2y$05$anotherBcryptHash
```

**Generated HAProxy Configuration**:

```haproxy
userlist ni_auth_default_basic-auth
  user admin password '$2y$05$mO1VWak5QnbhNgJ4QwdAdXbfz.8b3ceH6U5KOVCKxR2IkNAfJgLi5pIKW'
  user user password '$2y$05$anotherBcryptHash'

backend my-backend
    http-request auth realm "Protected Area" unless { http_auth(ni_auth_default_basic-auth) }
```

---

## External Authentication

The library wires the `nginx.ingress.kubernetes.io/auth-*` family to the SPOA hub's `external-auth` plugin (v0.3.0+). When set, each request hits an HTTP auth subrequest before reaching the backend; the auth service's status code decides whether HAProxy forwards the request, redirects to a sign-in URL, or returns 401.

### Prerequisites

The SPOA hub sidecar with the `external-auth` plugin must be enabled:

```yaml
spoaHub:
  plugins:
    external-auth:
      enabled: true
```

The hub auto-enables when any plugin is on, and the spoa-hub template library auto-loads when the hub is enabled. Note: enabling `controller.templateLibraries.nginxIngress.enabled` ALSO auto-enables `external-auth` (the nginx-ingress library is opt-in for this reason). See the [SPOA Hub operations guide](https://haproxy-haptic.org/controller/operations/spoa-hub/) for the full deployment surface.

!!! warning "Host-less rules error at render time"
    All external-auth annotations key their per-route lookup tables by `host+path`. An Ingress rule without an explicit `host` cannot be enforced — silently skipping auth on a route the operator marked protected would be a security failure mode. The chart fails the Helm render with an explicit error identifying the offending Ingress.

---

### nginx.ingress.kubernetes.io/auth-url

**Status**: ✅ Supported

**Description**: Auth service URL the SPOA hub calls per request. The plugin appends the original request path, sends a GET (overridable via `auth-method`), and gates the request based on the response status: 2xx allows, 3xx with `auth-signin` redirects, anything else returns 401.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/auth-url: "https://auth.example.com/check"
```

**Generated HAProxy Configuration**:

```haproxy
http-request set-var(txn.auth_url) var(txn.host_match),concat(,txn.path,),map(maps/auth-url.map)
http-request send-spoe-group spoa-hub check-auth-group if { var(txn.auth_url) -m found }
http-request deny deny_status 401 if { var(txn.auth_url) -m found } !{ var(txn.hub.external_auth.allowed) -m bool }
```

---

### nginx.ingress.kubernetes.io/auth-signin

**Status**: ✅ Supported

**Description**: Browser-flow sign-in URL. When set, an auth failure produces a 302 redirect instead of a 401 — the standard pattern for OIDC / SAML flows. The deny rule still emits, so routes without `auth-signin` keep the API-friendly 401.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/auth-url: "https://auth.example.com/check"
  nginx.ingress.kubernetes.io/auth-signin: "https://login.example.com/oauth2/start?rd=$escaped_request_uri"
```

!!! note "nginx variables in the URL are not expanded"
    HAProxy doesn't substitute `$escaped_request_uri` and friends at redirect time; the URL is used verbatim. Operators wanting the original-request preservation pattern should either set the param via the auth service (e.g. oauth2-proxy handles it server-side) or extend the SPOE message body with the bits they need.

---

### nginx.ingress.kubernetes.io/auth-method

**Status**: ✅ Supported

**Description**: HTTP method for the auth subrequest. Defaults to `GET` (or whatever the plugin's TOML config sets); set this to override per-route.

**Valid values**: `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/auth-url: "https://auth.example.com/check"
  nginx.ingress.kubernetes.io/auth-method: "POST"
```

!!! note "Body-having methods carry an empty body"
    `POST` / `PUT` / `PATCH` go to the auth service with an empty body — the plugin does not forward the original request payload.

---

### nginx.ingress.kubernetes.io/auth-response-headers

**Status**: ✅ Supported

**Description**: Comma-separated list of response header names from the auth service to forward to the upstream backend on auth success. Common pattern: the auth service returns `X-Auth-User: alice` on 200, this annotation makes that header available to the backend application.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/auth-url: "https://auth.example.com/check"
  nginx.ingress.kubernetes.io/auth-response-headers: "X-Auth-User, X-Auth-Roles"
```

**Generated HAProxy Configuration**:

```haproxy
http-request set-header X-Auth-User %[var(txn.hub.external_auth.x_auth_user)] if { var(txn.hub.external_auth.x_auth_user) -m found } { var(txn.hub.external_auth.allowed) -m bool }
http-request set-header X-Auth-Roles %[var(txn.hub.external_auth.x_auth_roles)] if { var(txn.hub.external_auth.x_auth_roles) -m found } { var(txn.hub.external_auth.allowed) -m bool }
```

One `set-header` directive per unique header across all ingresses; per-route gating happens via the plugin's per-ingress `extract_headers` SPOE arg — routes that didn't list a header have its txn var unset, so the `var ... -m found` gate skips them.

!!! note "Failure-path response headers"
    nginx-ingress doesn't expose an annotation for "headers to send back to the client on auth failure" (the haproxy-ingress equivalent is `auth-headers-fail`). If you need that — e.g. `WWW-Authenticate` for Bearer challenges — switch to or add the haproxy-ingress library and use its annotation prefix.

!!! note "auth-snippet not wired"
    `nginx.ingress.kubernetes.io/auth-snippet` (used in nginx-ingress for arbitrary nginx config injection in the auth subrequest) is freeform nginx syntax with no parsable structure, so it cannot be templated to HAProxy. The haproxy-ingress prefix has a typed `auth-headers-request` annotation that covers the most common use case.

---

## SSL Features

### nginx.ingress.kubernetes.io/ssl-passthrough

**Status**: ✅ Supported

**Description**: Enable TCP-level SSL passthrough (Layer 4) where HAProxy routes based on SNI without terminating SSL.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ssl-passthrough-example
  annotations:
    nginx.ingress.kubernetes.io/ssl-passthrough: "true"
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

---

## Canary Deployments

### nginx.ingress.kubernetes.io/canary

**Status**: ✅ Supported

**Description**: Route a percentage or subset of traffic to a canary backend.

**Related annotations**:

| Annotation | Description |
|------------|-------------|
| `canary` | Mark this Ingress as a canary (`"true"`) |
| `canary-by-header` | Route to canary when this header is present |
| `canary-by-header-value` | Required header value (default: `always`) |
| `canary-by-header-pattern` | Regex pattern for header matching |
| `canary-by-cookie` | Route to canary when cookie value is `always` |
| `canary-weight` | Percentage of traffic to route to canary (0-100) |

**Priority order**: header > cookie > weight

The canary Ingress must share a host with a non-canary (main) Ingress. Mark the secondary Ingress with `canary: "true"` and it routes to the parent's host.

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-app-canary
  annotations:
    nginx.ingress.kubernetes.io/canary: "true"
    nginx.ingress.kubernetes.io/canary-by-header: "X-Canary"
    nginx.ingress.kubernetes.io/canary-weight: "20"
spec:
  ingressClassName: haptic
  rules:
    - host: app.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-app-canary
                port:
                  number: 80
```

**Generated HAProxy Configuration**:

```haproxy
use_backend default_my-app-canary_svc_my-app-canary_http if { req.hdr(X-Canary) -m str always } { hdr(host) -i app.example.com }
use_backend default_my-app-canary_svc_my-app-canary_http if { rand(100) lt 20 } { hdr(host) -i app.example.com }
```

---

## Client Certificate Auth (mTLS)

### mTLS Certificate Passthrough

**Status**: ✅ Supported

**Annotations**:

| Annotation | Description |
|------------|-------------|
| `auth-tls-pass-certificate-to-upstream` | Pass client certificate and subject DN as headers |
| `auth-tls-error-page` | Redirect URL on client certificate verification failure |

!!! note "Partial mTLS Support"
    `auth-tls-secret`, `auth-tls-verify-client`, and `auth-tls-verify-depth` are supported via crt-list per-entry options but are not yet implemented.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/auth-tls-pass-certificate-to-upstream: "true"
  nginx.ingress.kubernetes.io/auth-tls-error-page: "https://example.com/auth-error"
```

**Generated HAProxy Configuration**:

```haproxy
http-request redirect location https://example.com/auth-error code 302 if { ssl_c_verify gt 0 } { hdr(host) -i example.com }
http-request set-header ssl-client-cert %[ssl_c_der,base64] if { hdr(host) -i example.com }
http-request set-header ssl-client-subject-dn %[ssl_c_s_dn] if { hdr(host) -i example.com }
```

---

## Unsupported Annotations

The following nginx-ingress annotations are not supported:

| Annotation | Reason |
|------------|--------|
| `enable-modsecurity`, `modsecurity-*` | ModSecurity integration requires SPOE agent |
| `mirror-*` | Traffic mirroring has no native HAProxy equivalent |
| `enable-opentelemetry`, `opentelemetry-*` | Requires OpenTelemetry module |
| `enable-opentracing`, `opentracing-*` | Requires OpenTracing module |
| `server-snippet` | Nginx server-level directives have no HAProxy equivalent |
| `proxy-max-temp-file-size` | HAProxy uses in-memory buffering, no temp file concept |
| `stream-snippet` | Nginx stream directives have no HAProxy equivalent |

---

## Watched Resources

This library watches the following additional resources:

- **Secrets** (`v1/secrets`) - Used for basic auth credential lookup

---

## Implementation Status Summary

**Total annotations**: 52

- ✅ **Fully Supported**: 52
  - Timeouts: 3 annotations
  - Load Balancing: 1 annotation
  - Body Size Limit: 1 annotation
  - Backend Protocol: 1 annotation
  - Proxy Protocol: 1 annotation
  - Rewrite Target: 1 annotation
  - Upstream Hash: 1 annotation
  - Config Snippet: 1 annotation
  - Session Affinity: 4 annotations
  - Rate Limiting: 2 annotations
  - Access Control: 2 annotations
  - SSL Redirect: 2 annotations
  - HSTS: 4 annotations
  - CORS: 7 annotations
  - Custom Headers: 2 annotations
  - App Root: 1 annotation
  - Redirects: 2 annotations
  - Authentication: 3 annotations
  - External Authentication: 4 annotations (`auth-url`, `auth-signin`, `auth-method`, `auth-response-headers` — requires SPOA hub `external-auth` plugin)
  - SSL Passthrough: 1 annotation
  - Canary: 6 annotations
  - mTLS: 2 annotations

## See Also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Core HAProxy template
- [Ingress Library](ingress.md) - Standard Ingress support
- [HAProxy Ingress Library](haproxy-ingress.md) - `haproxy-ingress.github.io/*` annotations
- [HAProxyTech Library](haproxytech.md) - `haproxy.org/*` annotations
- [Nginx Ingress Documentation](https://kubernetes.github.io/ingress-nginx/user-guide/nginx-configuration/annotations/) - Original annotation reference
