# haproxy-ingress Library

The haproxy-ingress library implements `haproxy-ingress.github.io/*` annotations compatible with [jcmoraisjr/haproxy-ingress](https://haproxy-ingress.github.io/), a community HAProxy ingress controller. It supports path matching, backend configuration, session affinity, SSL features, access control, and more.

## Overview

This library is enabled by default.

See the `haproxy-ingress.github.io/*` annotations render to HAProxy config live:

<div class="pg-embed" markdown data-scenario="haproxy-ingress" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="haproxy-ingress.github.io/* annotations rendered" data-height="440">

<p class="pg-task" markdown>**Try it:** In the **resources** tab, change the `shop` Ingress's `haproxy-ingress.github.io/health-check-uri` from `/healthz` to `/readyz`, then watch the shop backend's health-check line update in the `haproxy.cfg` tab.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

The `backend storefront_shop_svc_shop_http` section changes `option httpchk GET /healthz` to `option httpchk GET /readyz`, so HAProxy's active health check now probes `/readyz`. The `default-server` line keeps its `check inter 2s` (from `backend-check-interval`) — only the probed URI moves.

</details>

</div>

!!! note "Migrating from jcmoraisjr/haproxy-ingress"
    If you are migrating from jcmoraisjr/haproxy-ingress, your existing `haproxy-ingress.github.io/*` annotations work without changes. See [Annotations](../annotations.md) for the full feature comparison between annotation libraries.

## Configuration

```yaml
controller:
  templateLibraries:
    haproxyIngress:
      enabled: true  # Enabled by default
```

## Extension Points

The haproxy-ingress library hooks into these extension points. Snippet names encode priority via a numeric prefix — the 600-range deliberately runs after the core haproxytech 100–500 range so `haproxy-ingress.github.io/*` annotations override `haproxy.org/*` when both are set on the same Ingress.

### features-* (shared-state initialization)

| Snippet | Purpose |
|---------|---------|
| `features-100-haproxy-ingress-ssl-passthrough` | Scans ingresses annotated with `haproxy-ingress.github.io/ssl-passthrough` and registers backends in `gf["sslPassthroughBackends"]` |

### map-path-* (path-map extension points)

| Snippet | Extension Point | Purpose |
|---------|-----------------|---------|
| `map-path-regex-600-haproxy-ingress` | `map-path-regex-*` | Regex path-map entries for `path-type: regex` |
| `map-path-exact-600-haproxy-ingress` | `map-path-exact-*` | Exact path-map entries for `path-type: exact` |
| `map-path-prefix-600-haproxy-ingress` | `map-path-prefix-*` | Prefix path-map entries for `path-type: begin` |
| `map-pfxexact-600-haproxy-ingress` | `map-pfxexact-*` | Prefix-exact entries for `path-type: prefix` |

### backend-directives-* (per-backend directives)

| Snippet | Annotations Processed |
|---------|----------------------|
| `backend-directives-600-haproxy-ingress-timeouts` | `timeout-connect`, `timeout-server`, `timeout-queue`, `timeout-http-request`, `timeout-keep-alive`, `timeout-tunnel` |
| `backend-directives-610-haproxy-ingress-load-balance` | `balance-algorithm` |
| `backend-directives-620-haproxy-ingress-maxconn` | `maxconn-server` |
| `backend-directives-630-haproxy-ingress-health-checks` | `health-check-uri`, `backend-check-interval` |
| `backend-directives-640-haproxy-ingress-proxy-protocol` | `proxy-protocol` |
| `backend-directives-650-haproxy-ingress-ssl-backend` | `secure-backends`, `backend-protocol`, `secure-sni`, `secure-verify-ca-secret`, `secure-crt-secret` |
| `backend-directives-660-haproxy-ingress-server-options` | `initial-weight`, other server-line options |
| `backend-directives-670-haproxy-ingress-session-affinity` | `affinity`, `session-cookie-*` |
| `backend-directives-680-haproxy-ingress-auth` | `auth-secret`, `auth-realm` (attaches userlist to the backend) |
| `backend-directives-900-haproxy-ingress-config-backend` | `config-backend` |

### frontend-filters-* (HTTP-frontend request/response filters)

| Snippet | Annotations Processed |
|---------|----------------------|
| `frontend-filters-600-haproxy-ingress-forwardfor` | `forwardfor` |
| `frontend-filters-610-haproxy-ingress-access-control` | `allowlist-source-range`, `denylist-source-range` |
| `features-105-haproxy-ingress-ssl-redirect` | `ssl-redirect`, `ssl-redirect-code` (registers hosts into the shared `ssl-redirect-<code>.map`; ssl.yaml emits the redirect rule) |
| `features-155-haproxy-ingress-hsts` | `hsts`, `hsts-max-age`, `hsts-include-subdomains`, `hsts-preload` (registers host→value into the shared `hsts.map`; base.yaml emits the response-header rule) |
| `features-145-haproxy-ingress-app-root` | `app-root` (registers host→path into the shared `app-root.map`; base.yaml emits the gated rule) |
| `features-135-haproxy-ingress-redirect-to` | `redirect-to`, `redirect-to-code` (registers host→location in the shared `redirect-loc-<code>.map`) |
| `frontend-filters-660-haproxy-ingress-cors` | `cors-enable`, `cors-*` |
| `frontend-filters-670-haproxy-ingress-headers` | `headers` |

### Other extension points

| Snippet | Extension Point | Purpose |
|---------|-----------------|---------|
| `global-top-600-haproxy-ingress-auth` | `global-top-*` | Emits a `userlist auth_<secretNs>_<secretName>` per unique auth secret (deduplicated) |
| `backends-501-haproxy-ingress-ssl-passthrough` | `backends-*` | TCP-mode passthrough backends for hosts with `ssl-passthrough: "true"` |

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
  ingressClassName: haptic
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

**Annotations**:

| Annotation | Status | Description |
|------------|--------|-------------|
| `limit-connections` | ✅ Supported | Backend `fullconn` limit |
| `maxconn-server` | ✅ Supported | Per-server `maxconn` on the `default-server` line |
| `maxqueue-server` | ✅ Supported | Per-server `maxqueue` on the `default-server` line |

`maxconn-server` and `maxqueue-server` append `maxconn <n>` / `maxqueue <n>` to
`serverOpts["flags"]` in `backend-directives-620-haproxy-ingress-maxconn`, which
`BuildServerOptions` (base library) emits onto the `default-server` line.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/limit-connections: "1000"
  haproxy-ingress.github.io/maxconn-server: "50"
  haproxy-ingress.github.io/maxqueue-server: "100"
```

---

### Health Checks

**Annotations**:

| Annotation | Status | Description |
|------------|--------|-------------|
| `health-check-uri` | ✅ Supported | HTTP health check path — emitted as `option httpchk GET <uri>` |
| `backend-check-interval` | ✅ Supported | Emitted as `inter <value>` on the `default-server` line |
| `health-check-port` | ✅ Supported | Emitted as `port <n>` on the `default-server` line |
| `health-check-fall-count` | ✅ Supported | Emitted as `fall <n>` on the `default-server` line |
| `health-check-rise-count` | ✅ Supported | Emitted as `rise <n>` on the `default-server` line |

`backend-directives-630-haproxy-ingress-health-checks` appends `inter` / `port` /
`fall` / `rise` to `serverOpts["flags"]`, which `BuildServerOptions` (base
library) emits onto the `default-server` line.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/health-check-uri: "/healthz"
  haproxy-ingress.github.io/backend-check-interval: "5s"
  haproxy-ingress.github.io/health-check-port: "8082"
  haproxy-ingress.github.io/health-check-fall-count: "3"
  haproxy-ingress.github.io/health-check-rise-count: "2"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    option httpchk GET /healthz
    default-server check inter 5s port 8082 fall 3 rise 2
    server SRV_1 10.0.0.1:8080 enabled
```

`check` and its tuning (`inter`, `port`, `fall`, `rise`) live on `default-server` (not on individual server lines) so endpoint changes can be applied via the runtime API without a HAProxy reload.

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

**Description**: Initial weight for backend servers (0-256). The library reads and validates the value (0-256 range), then appends `weight <n>` to `serverOpts["flags"]` in `backend-directives-660-haproxy-ingress-server-options`, which `BuildServerOptions` (base library) emits onto the `default-server` line.

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
| `cors-allow-origin` | Allowed origins — comma-separated list, single-level `*.` wildcards; matched Origin is echoed back | `*` |
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

## External Authentication

The library wires the `haproxy-ingress.github.io/auth-*` annotation family to the SPOA hub's `external-auth` plugin (v0.3.0+). When set, each request hits an HTTP auth subrequest before reaching the backend; the auth service's status code decides whether HAProxy forwards the request, redirects to a sign-in URL, or returns 401.

### Prerequisites

The SPOA hub sidecar with the `external-auth` plugin must be enabled:

```yaml
spoaHub:
  plugins:
    external-auth:
      enabled: true
```

The hub auto-enables when any plugin is on, and the spoa-hub template library auto-loads when the hub is enabled. See the [SPOA Hub operations guide](https://haproxy-haptic.org/controller/latest/operations/spoa-hub/) for the full deployment surface.

!!! warning "Host-less rules error at render time"
    All external-auth annotations key their per-route lookup tables by `host+path`. An Ingress rule without an explicit `host` cannot be enforced — silently skipping auth on a route the operator marked protected would be a security failure mode. The chart fails the Helm render with an explicit error identifying the offending Ingress; add a `host:` to the rule to fix.

---

### haproxy-ingress.github.io/auth-url

**Status**: ✅ Supported

**Description**: Auth service URL the SPOA hub calls per request. The plugin appends the original request path, sends a GET (overridable via `auth-method`), and gates the request based on the response status: 2xx allows, 3xx with `auth-signin` redirects, anything else returns 401.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/auth-url: "https://auth.example.com/check"
```

**Generated HAProxy Configuration**:

```haproxy
http-request set-var(txn.auth_url) var(txn.host_match),concat(,txn.path,),map(maps/auth-url.map)
http-request send-spoe-group spoa-hub check-auth-group if { var(txn.auth_url) -m found }
http-request deny deny_status 401 if { var(txn.auth_url) -m found } !{ var(txn.hub.external_auth.allowed) -m bool }
```

The matching `auth-url.map` entry:

```
app.example.com/api https://auth.example.com/check
```

---

### haproxy-ingress.github.io/auth-signin

**Status**: ✅ Supported

**Description**: Browser-flow sign-in URL. When set, an auth failure produces a 302 redirect instead of a 401 — the standard pattern for OIDC / SAML flows where unauthenticated users go to a login page. The deny rule still emits, so routes without `auth-signin` keep the API-friendly 401.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/auth-url: "https://auth.example.com/check"
  haproxy-ingress.github.io/auth-signin: "https://login.example.com/oauth2/start"
```

**Generated HAProxy Configuration**:

```haproxy
http-request redirect location %[var(txn.auth_signin)] code 302 if { var(txn.auth_url) -m found } !{ var(txn.hub.external_auth.allowed) -m bool } { var(txn.auth_signin) -m found }
http-request deny deny_status 401 if { var(txn.auth_url) -m found } !{ var(txn.hub.external_auth.allowed) -m bool }
```

---

### haproxy-ingress.github.io/auth-method

**Status**: ✅ Supported

**Description**: HTTP method for the auth subrequest. Defaults to `GET` (or whatever the plugin's TOML config sets); set this to override per-route.

**Valid values**: `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/auth-url: "https://auth.example.com/check"
  haproxy-ingress.github.io/auth-method: "POST"
```

!!! note "Body-having methods carry an empty body"
    `POST` / `PUT` / `PATCH` go to the auth service with an empty body — the plugin does not forward the original request payload. Auth services that need the body should read it via sample-fetch args (see [SPOA hub operations](https://haproxy-haptic.org/controller/latest/operations/spoa-hub/)) or use header-based auth.

---

### haproxy-ingress.github.io/auth-headers-request

**Status**: ✅ Supported

**Description**: Comma-separated list of request header names to forward to the auth service. The chart auto-extends the SPOE message body to capture every header listed across ingresses (deduped, the six standard headers — Authorization, Cookie, X-Forwarded-{For,Proto,Host,Uri} — are always captured), and the plugin then narrows the per-route forwarded set to exactly the headers the annotation lists.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/auth-url: "https://auth.example.com/check"
  haproxy-ingress.github.io/auth-headers-request: "Authorization, X-Tenant-Id, X-Request-Id"
```

**Generated HAProxy Configuration**:

```haproxy
http-request set-var(txn.auth_forward_headers) var(txn.host_match),concat(,txn.path,),map(maps/auth-forward-headers.map)
```

The corresponding entries in the SPOE message:

```
spoe-message check-auth
    args ... forward_headers=var(txn.auth_forward_headers) ... hdr_authorization=req.hdr(Authorization) hdr_x_tenant_id=req.hdr(X-Tenant-Id) hdr_x_request_id=req.hdr(X-Request-Id)
```

Header names are validated against the RFC 7230 token grammar; values containing whitespace, fetch syntax (`%[var(...)]`), or other non-tchar characters fail the Helm render.

---

### haproxy-ingress.github.io/auth-headers-succeed

**Status**: ✅ Supported

**Description**: Comma-separated list of response header names from the auth service to forward to the upstream backend on auth success. Common pattern: the auth service returns `X-Auth-User: alice` on 200, this annotation makes that header available to the backend application.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/auth-url: "https://auth.example.com/check"
  haproxy-ingress.github.io/auth-headers-succeed: "X-Auth-User, X-Auth-Roles"
```

**Generated HAProxy Configuration**:

```haproxy
http-request set-header X-Auth-User %[var(txn.hub.external_auth.x_auth_user)] if { var(txn.hub.external_auth.x_auth_user) -m found } { var(txn.hub.external_auth.allowed) -m bool }
http-request set-header X-Auth-Roles %[var(txn.hub.external_auth.x_auth_roles)] if { var(txn.hub.external_auth.x_auth_roles) -m found } { var(txn.hub.external_auth.allowed) -m bool }
```

One `set-header` directive per unique header across all ingresses; the per-route gating happens via the plugin's per-ingress `extract_headers` SPOE arg — routes that didn't list a header have its txn var unset, so the `var ... -m found` gate skips them.

---

### haproxy-ingress.github.io/auth-headers-fail

**Status**: ✅ Supported

**Description**: Comma-separated list of response header names from the auth service to forward to the *client* on auth failure. Drives e.g. `WWW-Authenticate` for Bearer challenges or `X-Error-Reason` for diagnostics on 401 / 5xx.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/auth-url: "https://auth.example.com/check"
  haproxy-ingress.github.io/auth-headers-fail: "WWW-Authenticate, X-Error-Reason"
```

**Generated HAProxy Configuration**:

```haproxy
http-after-response set-header WWW-Authenticate %[var(txn.hub.external_auth.www_authenticate)] if { var(txn.auth_url) -m found } !{ var(txn.hub.external_auth.allowed) -m bool } { var(txn.hub.external_auth.www_authenticate) -m found }
http-after-response set-header X-Error-Reason %[var(txn.hub.external_auth.x_error_reason)] if { var(txn.auth_url) -m found } !{ var(txn.hub.external_auth.allowed) -m bool } { var(txn.hub.external_auth.x_error_reason) -m found }
```

The conditions ensure the directive only fires on the deny response (auth path ran, not allowed, plugin actually extracted the header). The plugin v0.3.0+ extracts headers on every reply path (2xx, 3xx, 4xx, 5xx, fail-policy), so 401 and 5xx replies populate the txn vars too.

!!! note "Why `http-after-response` rather than `http-response`"
    HAProxy's `http-request deny` short-circuits the request flow — the 401 response is generated internally, so `http-response` rules (which fire only on responses *received from a backend*) never apply to it. `http-after-response` runs after every response, including HAProxy-generated ones, and is the only directive that lets the extracted fail-path headers reach the client.

---

### Combined example

End-to-end: a protected API route with browser sign-in, custom request header forwarding, identity propagation to the backend, and a Bearer challenge on failure.

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: protected-api
  annotations:
    haproxy-ingress.github.io/auth-url: "https://auth.example.com/check"
    haproxy-ingress.github.io/auth-signin: "https://login.example.com/oauth2/start"
    haproxy-ingress.github.io/auth-method: "GET"
    haproxy-ingress.github.io/auth-headers-request: "Authorization, X-Tenant-Id"
    haproxy-ingress.github.io/auth-headers-succeed: "X-Auth-User, X-Auth-Roles"
    haproxy-ingress.github.io/auth-headers-fail: "WWW-Authenticate"
spec:
  ingressClassName: haptic
  rules:
    - host: api.example.com
      http:
        paths:
          - path: /v1
            pathType: Prefix
            backend:
              service:
                name: api-backend
                port:
                  number: 80
```

---

## Client Certificate Auth (mTLS)

The library wires the haproxy-ingress.github.io/auth-tls-* annotations for incoming client-cert verification. The CA bundle from the referenced Secret is written to the SSL cert dir and referenced from the HAProxy `crt-list` line for the matching SNI as `[ca-file <path> verify <mode>]` — HAProxy verifies incoming client certs at the TLS layer. Companion annotations react to the verification result via `ssl_c_verify` and `ssl_fc_has_crt`.

### haproxy-ingress.github.io/auth-tls-secret

**Status**: ✅ Supported

**Description**: Reference to a `kubernetes.io/tls` Secret whose `ca.crt` field contains the CA bundle that signs the clients' certificates. The chart writes the CA to `ssl/<ns>-<secret>-client-ca.pem` and adds `[ca-file <path> verify <mode>]` to the crt-list line for every host on the annotated Ingress.

**Format**: `name` (resolves in the Ingress namespace) or `namespace/name`.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/auth-tls-secret: "client-ca"
```

The Secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: client-ca
type: kubernetes.io/tls
data:
  ca.crt: <base64 PEM CA bundle>
```

!!! warning "Host-less rules error at render time"
    SNI-keyed verification can't be enforced on Ingress rules without a `host:`. The chart fails the Helm render with a descriptive error.

---

### haproxy-ingress.github.io/auth-tls-verify-client

**Status**: ✅ Supported

**Description**: Client certificate verification mode. Same value semantics as the nginx-ingress flavour; see the [nginx-ingress documentation for auth-tls-verify-client](nginx-ingress.md#nginxingresskubernetesioauth-tls-verify-client) for the value mapping table.

**Valid values**: `on` (default), `off`, `optional`, `optional_no_ca`.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/auth-tls-secret: "client-ca"
  haproxy-ingress.github.io/auth-tls-verify-client: "optional"
```

!!! note "auth-tls-strict not separately wired"
    haproxy-ingress's `auth-tls-strict` annotation overlaps with `auth-tls-verify-client: optional` — operators wanting "soft" verification (allow connections without cert, defer to backend header inspection) should use `auth-tls-verify-client: optional` directly.

---

### haproxy-ingress.github.io/auth-tls-error-page

**Status**: ✅ Supported

**Description**: URL to redirect to (302) when client certificate verification fails.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/auth-tls-secret: "client-ca"
  haproxy-ingress.github.io/auth-tls-error-page: "https://example.com/cert-required"
```

**Generated HAProxy Configuration**:

```haproxy
http-request redirect location https://example.com/cert-required code 302 if { ssl_c_verify gt 0 } { hdr(host) -i example.com }
```

The `ssl_c_verify gt 0` condition matches any verification error (including missing cert when `verify required` is set).

---

### haproxy-ingress.github.io/auth-tls-cert-header

**Status**: ✅ Supported

**Description**: When `"true"`, forwards the verified client certificate (base64-encoded DER), subject CN, and full subject DN to the upstream backend as HTTP headers.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/auth-tls-secret: "client-ca"
  haproxy-ingress.github.io/auth-tls-cert-header: "true"
```

**Generated HAProxy Configuration**:

```haproxy
http-request set-header X-SSL-Client-CN %[ssl_c_s_dn(CN)] if { ssl_fc_has_crt } { hdr(host) -i example.com }
http-request set-header X-SSL-Client-DN %[ssl_c_s_dn] if { ssl_fc_has_crt } { hdr(host) -i example.com }
http-request set-header X-SSL-Client-Cert %[ssl_c_der,base64] if { ssl_fc_has_crt } { hdr(host) -i example.com }
```

The `ssl_fc_has_crt` gate ensures the headers only flow when a cert was actually presented (relevant when `auth-tls-verify-client: optional` is in effect — connections without a cert get no headers rather than empty ones).

---

## Web Application Firewall (ModSecurity / Coraza)

Opt an Ingress into the SPOA hub **Coraza** WAF with `haproxy-ingress.github.io/waf: "modsecurity"` (the only supported value). Each `<host><path>` is added to `haproxy-ingress-waf.map`, whose value is the enforcement mode from `haproxy-ingress.github.io/waf-mode`: `deny` (default) blocks on a rule hit, `detect` runs the WAF in shadow mode (logs without blocking). Setting `waf-mode` to anything other than `deny`/`detect`, or setting it without `waf`, fails the render. The Coraza plugin auto-enables when the haproxy-ingress or nginx-ingress library is on. See the [SPOA Hub operations guide](https://haproxy-haptic.org/controller/latest/operations/spoa-hub/) for the plugin's configuration surface.

---

## Watched Resources

This library does not add additional watched resources. It uses Ingress resources already watched by the [Ingress library](ingress.md).

## Implementation Status Summary

The library processes **68** `haproxy-ingress.github.io/*` annotations (verified against `libraries/haproxy-ingress/`).

**Supported by category:**

| Category | Count | Notes |
|----------|-------|-------|
| Path matching | 1 | `path-type` (four values: regex, exact, prefix, begin) |
| Timeouts | 6 | `timeout-connect`, `timeout-server`, `timeout-queue`, `timeout-http-request`, `timeout-keep-alive`, `timeout-tunnel` |
| Load balancing | 1 | `balance-algorithm` |
| Connection limits | 4 | `maxconn-server`, `maxqueue-server`, `initial-weight`, `limit-connections` |
| Health checks | 5 | `backend-check-interval`, `health-check-uri`, `health-check-port`, `health-check-fall-count`, `health-check-rise-count` |
| Backend SSL / mTLS | 6 | `secure-backends`, `backend-protocol`, `secure-sni`, `secure-verify-ca-secret`, `secure-crt-secret`, `secure-verify-hostname` |
| PROXY protocol | 1 | `proxy-protocol` |
| Session affinity | 8 | `affinity`, `session-cookie-name`, `session-cookie-domain`, `session-cookie-strategy`, `session-cookie-same-site`, `session-cookie-keywords`, `session-cookie-preserve`, `session-cookie-dynamic` |
| Access control | 2 | `allowlist-source-range`, `denylist-source-range` |
| SSL redirect | 2 | `ssl-redirect`, `ssl-redirect-code` |
| HSTS | 4 | `hsts`, `hsts-max-age`, `hsts-include-subdomains`, `hsts-preload` |
| App root | 1 | `app-root` |
| Redirects | 2 | `redirect-to`, `redirect-to-code` |
| CORS | 7 | `cors-enable`, `cors-allow-origin`, `cors-allow-methods`, `cors-allow-headers`, `cors-allow-credentials`, `cors-max-age`, `cors-expose-headers` |
| Headers | 2 | `headers`, `forwardfor` |
| SSL passthrough | 1 | `ssl-passthrough` |
| Basic auth | 2 | `auth-secret`, `auth-realm` |
| External auth | 6 | `auth-url`, `auth-signin`, `auth-method`, `auth-headers-request`, `auth-headers-succeed`, `auth-headers-fail` (requires SPOA hub `external-auth` plugin) |
| Client mTLS | 4 | `auth-tls-secret`, `auth-tls-verify-client`, `auth-tls-error-page`, `auth-tls-cert-header` (incoming client-cert verification via crt-list per-line ca-file/verify options) |
| WAF | 2 | `waf` (opt-in, only `"modsecurity"`), `waf-mode` (`deny`/`detect` shadow mode, default `deny`) — ModSecurity `SecRule` enforcement via the SPOA hub `coraza` plugin |
| Backend config | 1 | `config-backend` |

All annotations are fully supported.

## See Also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Path matching infrastructure
- [Ingress Library](ingress.md) - Standard Ingress support
- [HAProxyTech Library](haproxytech.md) - `haproxy.org/*` annotations
- [HAProxy Ingress Documentation](https://haproxy-ingress.github.io/docs/configuration/keys/) - Original annotation reference
