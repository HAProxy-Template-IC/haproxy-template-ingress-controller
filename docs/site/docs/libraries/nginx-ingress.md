# `nginx-ingress` library

The Nginx Ingress library provides compatibility with the [nginx-ingress controller](https://kubernetes.github.io/ingress-nginx/) annotations for Kubernetes Ingress resources.

## Overview

This library enables `nginx.ingress.kubernetes.io/*` annotations on Ingress resources, providing a migration path for users coming from the nginx-ingress controller. It supports backend configuration, session affinity, rate limiting, URL rewriting, redirects, Cross-Origin Resource Sharing (CORS), access control, canary deployments, authentication, SSL passthrough, and mTLS certificate passthrough.

This library is disabled by default.

Because the preset mixes annotations that HAPTIC supports, maps differently, and drops, the migration report is the clearest live view:

<div class="pg-embed" markdown data-scenario="nginx-ingress" data-facade="resources" data-tab="migration" data-controls="tabs,resources" data-title="ingress-nginx annotation migration report" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, add <code>nginx.ingress.kubernetes.io/server-snippet: "more_set_headers X-From: nginx;"</code> to the `shop` Ingress, then watch a new **dropped** verdict appear in the **migration** report.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

The migration report gains a red `dropped` badge for `server-snippet` — "nginx server-level directives have no HAProxy equivalent" — and the dropped count rises by one. Because the annotation is dropped, it adds nothing to `haproxy.cfg`; the migration report is exactly where HAPTIC flags the annotations that won't carry over.

</details>

</div>

!!! note "Migrating from ingress-nginx"
    If you are migrating from ingress-nginx, enable this library and keep your existing `nginx.ingress.kubernetes.io/*` annotations — most carry over, some behave differently, and a few are dropped. See [Migrating from ingress-nginx](../migrating.md#from-ingress-nginx) for the cutover guide and the per-annotation verdict table, and [Annotations](../annotations.md) for the feature comparison between annotation libraries.

## Configuration

```yaml
controller:
  templateLibraries:
    nginxIngress:
      enabled: true  # Disabled by default
```

Enabling the library also auto-enables two Stream Processing Offload Agent (SPOA) hub plugins — `external-auth` (backing the `auth-url` family) and `coraza` (the Web Application Firewall (WAF) backing `modsecurity-snippet`) — which deploys the [SPOA hub sidecar](../operations/spoa-hub.md) in the HAProxy pod. An explicit `spoaHub.plugins.<name>.enabled` value overrides the auto-enable in either direction.

## Extension points

### Extension points used

The Nginx Ingress library implements these extension points:

| Extension Point | This Library's Snippets | What They Generate |
|-----------------|-------------------------|-------------------|
| Backend Directives | `backend-directives-670-nginx-ingress-session-affinity` | Cookie-based session affinity |
| Backend Directives | `backend-directives-700-nginx-ingress-timeouts` | Backend timeouts |
| Backend Directives | `backend-directives-710-nginx-ingress-load-balance` | Load balancing algorithm |
| Backend Directives | `backend-directives-715-nginx-ingress-next-upstream` | Retry conditions (`proxy-next-upstream`, `proxy-next-upstream-tries`) |
| Map (body-size) | `map-body-size-720-nginx-ingress` | Request body size limit (per-backend entry in `body-size.map`) |
| Backend Directives | `backend-directives-725-nginx-ingress-limit-rate` | Per-stream bandwidth throttle (`limit-rate`, `limit-rate-after`) |
| Backend Directives | `backend-directives-730-nginx-ingress-backend-protocol` | Backend protocol (HTTPS, gRPC) |
| Backend Directives | `backend-directives-740-nginx-ingress-proxy-protocol` | PROXY protocol to backend |
| Backend Directives | `backend-directives-750-nginx-ingress-rewrite-target` | URL rewriting (capture rewrites; literal rewrites go to `path-rewrite.map` via `map-path-rewrite-750-nginx-ingress`) |
| Backend Directives | `backend-directives-760-nginx-ingress-auth` | Basic auth enforcement |
| Backend Directives | `backend-directives-760-nginx-ingress-proxy-ssl` | Backend TLS (`proxy-ssl-*` server flags) |
| Backend Directives | `backend-directives-765-nginx-ingress-satisfy-any` | `satisfy: any` combined IP-or-auth gate |
| Backend Directives | `backend-directives-770-nginx-ingress-rate-limiting` | Rate limiting / connection limiting |
| Backend Directives | `backend-directives-780-nginx-ingress-upstream-hash` | Hash-based load balancing |
| Backend Directives | `backend-directives-790-nginx-ingress-proxy-cookie` | Upstream `Set-Cookie` rewriting (`proxy-cookie-domain`, `proxy-cookie-path`) |
| Backend Directives | `backend-directives-795-nginx-ingress-proxy-redirect` | Upstream `Location`/`Refresh` rewriting (`proxy-redirect-from`, `proxy-redirect-to`) |
| Backend Directives | `backend-directives-900-nginx-ingress-config-snippet` | Raw backend config injection |
| Map (request headers) | `map-reqhdr-host-760-nginx-ingress`, `map-reqhdr-xfwd-prefix-760-nginx-ingress`, `map-reqhdr-connection-760-nginx-ingress` | per-backend map entries for `upstream-vhost` / `x-forwarded-prefix` / `connection-proxy-header` |
| Map (host) | `map-host-720-nginx-ingress-server-alias` | `server-alias` hostnames → the rule host's routing key in `host.map` |
| Backends | `backends-510-nginx-ingress-default-backend` | Per-Ingress `default-backend` pools (+ catch-all path entries via `map-path-prefix-510-nginx-ingress-default-backend`) |
| Frontend Filters | `frontend-filters-700-nginx-ingress-access-control` | IP allowlist/denylist |
| Features | `features-105-nginx-ingress-ssl-redirect` | HTTP to HTTPS redirect (registers hosts into the shared `ssl-redirect-<code>.map`; ssl.yaml emits the rule) |
| Features | `features-155-nginx-ingress-hsts` | HTTP Strict Transport Security (HSTS) header — registers host→value into the shared `hsts.map` |
| Frontend Filters | `frontend-filters-730-nginx-ingress-cors` | CORS headers |
| Frontend Filters | `frontend-filters-740-nginx-ingress-custom-headers` | Custom request/response headers |
| Features | `features-145-nginx-ingress-app-root` | Root path redirect — registers host→path into the shared `app-root.map` |
| Features | `features-140-nginx-ingress-redirects` | Permanent (301) / temporal (302) redirect — registers host→location in the shared `redirect-loc-<code>.map` |
| Features | `features-150-nginx-ingress-mtls-error` | mTLS error-page redirect — registers host→URL into the shared `mtls-error.map` |
| Frontend Filters | `frontend-filters-775-nginx-ingress-from-to-www-redirect` | apex↔www redirect via `from-to-www.map` |
| Frontend Filters | `frontend-filters-780-nginx-ingress-canary` | Canary routing rules |
| Frontend Filters | `frontend-filters-790-nginx-ingress-mtls-error` | mTLS cert passthrough (set-headers; the error-page redirect moved to features-150) |
| Features | `features-100-nginx-ingress-ssl-passthrough` | SSL passthrough registration |
| Backends | `backends-501-nginx-ingress-ssl-passthrough` | SSL passthrough backends |
| Global Top | `global-top-700-nginx-ingress-auth` | Userlist definitions for basic auth |

---

## Backend configuration

### Timeouts

**Status**: ✅ Supported

**Annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `proxy-connect-timeout` | Backend connection timeout (seconds) | - |
| `proxy-read-timeout` | Backend response timeout (seconds) | - |
| `proxy-send-timeout` | Backend send timeout (seconds) | - |

!!! note "Timeout Value Format"
    Nginx-ingress timeout values are plain seconds (for example, `"60"`). The library automatically appends the `s` suffix for HAProxy.

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

### `nginx.ingress.kubernetes.io/load-balance`

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

### `nginx.ingress.kubernetes.io/proxy-body-size`

**Status**: ✅ Supported

**Description**: Maximum allowed request body size. Requests exceeding this limit receive a 413 response.

**Valid values**: Plain number (bytes), or with `k`/`m`/`g` suffix. Value `0` means unlimited (no map entry emitted).

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/proxy-body-size: "10m"
```

**Generated configuration**: the per-backend limit is written to `body-size.map`
(keyed on the resolved backend), not into the backend section. A shared,
resource-agnostic frontend rule (base.yaml `frontend-filters-250-request-body-size`)
enforces it, so adding or changing the limit is a map-only, reload-free update.

```
# body-size.map
default_my-ingress_svc_my-service_80 10485760
```

```haproxy
# frontend (shared, static — emitted once regardless of how many backends set a limit)
http-request set-var(txn.haptic_body_limit) var(txn.backend_name),map(maps/body-size.map,0),add(0)
http-request deny deny_status 413 if { var(txn.haptic_body_limit) -m int gt 0 } { req.body_size,sub(txn.haptic_body_limit) -m int gt 0 }
```

---

### `nginx.ingress.kubernetes.io/backend-protocol`

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
    `AJP` and `FCGI` aren't supported by HAProxy and fail with an error.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/backend-protocol: "GRPC"
```

---

### `nginx.ingress.kubernetes.io/use-proxy-protocol`

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

### `nginx.ingress.kubernetes.io/configuration-snippet`

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

### `nginx.ingress.kubernetes.io/upstream-hash-by`

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

Values not starting with `$` are passed through as-is (assumed to be HAProxy fetch expressions). Unrecognized `$variables` fail with an error.

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

### `nginx.ingress.kubernetes.io/proxy-next-upstream`

**Status**: ✅ Supported

**Description**: Conditions under which a failed request is retried against another server, mapped to HAProxy's `retry-on`.

**Mapping to HAProxy**:

| Nginx condition | HAProxy `retry-on` term |
|-----------------|-------------------------|
| `error` | `conn-failure` |
| `timeout` | `response-timeout` |
| `invalid_header` | `junk-response` |
| `http_<NNN>` (for example `http_503`) | `<NNN>` |
| `off` | `retries 0` (retries disabled) |
| `non_idempotent` | ignored (no HAProxy equivalent) |

**Related annotations**:

| Annotation | Description |
|------------|-------------|
| `proxy-next-upstream-tries` | Maps to HAProxy `retries`; `"0"` (nginx meaning unlimited) falls back to HAProxy's default retry count |

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/proxy-next-upstream: "error timeout http_503"
  nginx.ingress.kubernetes.io/proxy-next-upstream-tries: "3"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    retry-on conn-failure response-timeout 503
    retries 3
```

`option redispatch` is already set in the defaults section, so a retry lands on a different server.

---

### Upstream request headers

**Status**: ✅ Supported

**Annotations**:

| Annotation | Description |
|------------|-------------|
| `upstream-vhost` | Sets the `Host` header toward the backend |
| `x-forwarded-prefix` | Sets the `X-Forwarded-Prefix` request header |
| `connection-proxy-header` | Sets the `Connection` header toward the backend |

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/upstream-vhost: "internal.example.com"
  nginx.ingress.kubernetes.io/x-forwarded-prefix: "/app"
```

**Generated configuration**: each value is written to a per-header map keyed on
the resolved backend (`reqhdr-host.map`, `reqhdr-xfwd-prefix.map`,
`reqhdr-connection.map`); shared frontend rules in base.yaml apply them, so
changing a value is a map-only, reload-free update.

```text
# reqhdr-host.map
default_my-ingress_svc_my-service_80 internal.example.com
```

---

### Rate limiting

**Status**: ✅ Supported

**Annotations**:

| Annotation | Description |
|------------|-------------|
| `limit-rps` | Maximum requests per second per source IP |
| `limit-rpm` | Maximum requests per minute per source IP |
| `limit-connections` | Maximum concurrent connections per source IP |
| `limit-whitelist` | Comma-separated CIDRs exempt from the limits |

Exceeding a limit returns HTTP 429 — ingress-nginx allows a 5x burst and rejects with 503, so expect stricter enforcement at the same value after migrating. HAProxy stores one counter per backend stick-table, so the three limits are mutually exclusive with precedence `limit-rps` > `limit-rpm` > `limit-connections` (the rendered config notes ignored ones in a comment). Invalid CIDRs in `limit-whitelist` fail the render.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/limit-rps: "100"
  nginx.ingress.kubernetes.io/limit-whitelist: "10.0.0.0/8"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    stick-table type ip size 100k expire 1s store http_req_rate(1s) peers localinstance
    http-request track-sc0 src
    http-request deny deny_status 429 if { sc_http_req_rate(0) gt 100 } !{ src 10.0.0.0/8 }
```

The `peers localinstance` reference carries the per-source counters across HAProxy reloads, so accumulated rates survive config churn.

---

### `nginx.ingress.kubernetes.io/limit-rate`

**Status**: ⚠️ Caveat

**Description**: Download throttle — limits the bytes per second HAProxy sends toward the client, via an outbound bandwidth-limit filter. The limit applies per stream, so an HTTP/2 client that opens several streams gets a multiple of it.

**Related annotations**:

| Annotation | Description |
|------------|-------------|
| `limit-rate` | Maximum bytes per second per stream (`k`/`m`/`g` suffixes accepted) |
| `limit-rate-after` | Mapped to the bandwidth filter's `min-size` — the smallest chunk the filter forwards at a time, which trades CPU use against latency. It doesn't delay the throttle the way nginx's offset does, and HAProxy has no equivalent for that. A large value adds latency. Leave it unset unless you want to tune the forward chunk size, where roughly two TCP maximum segment sizes (about 2896 bytes) is HAProxy's suggested starting point. |

For a per-client or per-service budget rather than a per-stream one, the native library's [`bandwidth-limit-scope`](haptic-annotations.md#rate-and-bandwidth-limiting) covers what nginx can't express.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/limit-rate: "100k"
  nginx.ingress.kubernetes.io/limit-rate-after: "1m"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    filter bwlim-out ni_limitrate_default_my-ingress default-limit 100k default-period 1s min-size 1m
    http-request set-bandwidth-limit ni_limitrate_default_my-ingress
```

---

## Backend TLS (`proxy-ssl-*`)

The `proxy-ssl-*` family configures TLS toward the upstream: a client certificate, a CA to verify the upstream's certificate against, SNI, ciphers, and protocol bounds. The whole family requires backend TLS to be on — set `backend-protocol: "HTTPS"` (or `"GRPCS"`), otherwise the annotations have no effect.

### `nginx.ingress.kubernetes.io/proxy-ssl-secret`

**Status**: ✅ Supported

**Description**: Reference to a `kubernetes.io/tls` Secret: `tls.crt` + `tls.key` become the client certificate presented to the upstream, and `ca.crt` becomes the CA the upstream certificate is verified against when `proxy-ssl-verify` is on. The client certificate is presented regardless of the verify mode.

**Format**: `name` (resolves in the Ingress namespace) or `namespace/name`.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/backend-protocol: "HTTPS"
  nginx.ingress.kubernetes.io/proxy-ssl-secret: "upstream-tls"
  nginx.ingress.kubernetes.io/proxy-ssl-verify: "on"
  nginx.ingress.kubernetes.io/proxy-ssl-name: "backend.internal"
```

**Generated HAProxy Configuration** (flags on the backend's `default-server` line):

```haproxy
default-server check ssl verify required ca-file <upstream-tls-ca.pem> crt <upstream-tls-client.pem> sni str(backend.internal) verifyhost backend.internal
```

---

### `nginx.ingress.kubernetes.io/proxy-ssl-verify`

**Status**: ✅ Supported

**Description**: `"on"` verifies the upstream certificate against the referenced Secret's `ca.crt` (`verify required`); the default is off (`verify none`), matching ingress-nginx. The truthy spellings `on`/`true`/`yes`/`1` are matched case-insensitively so a spelling variant can't silently disable verification. Fail-closed: `"on"` without a resolvable `proxy-ssl-secret` containing `ca.crt` fails the render instead of silently skipping verification.

---

### `nginx.ingress.kubernetes.io/proxy-ssl-name`

**Status**: ✅ Supported

**Description**: Hostname used as SNI toward the upstream and — when verification is on — as `verifyhost` for certificate-name checking.

---

### `nginx.ingress.kubernetes.io/proxy-ssl-ciphers`

**Status**: ✅ Supported

**Description**: Cipher list for the upstream TLS connection (HAProxy's `ciphers` server option).

---

### `nginx.ingress.kubernetes.io/proxy-ssl-protocols`

**Status**: ✅ Supported

**Description**: Space-separated list of enabled TLS versions, for example `"TLSv1.2 TLSv1.3"`. HAProxy expresses a version span, not a list: the lowest listed version becomes `ssl-min-ver` and the highest `ssl-max-ver`, so gaps in the list can't be expressed.

!!! note "proxy-ssl-verify-depth and proxy-ssl-server-name not wired"
    `proxy-ssl-verify-depth` has no per-server HAProxy equivalent (chain depth is a bind-line option) — a warning comment is rendered and the CA bundle scope bounds the accepted chain instead. `proxy-ssl-server-name` isn't read; control SNI via `proxy-ssl-name`.

---

## Upstream response rewriting

### `nginx.ingress.kubernetes.io/proxy-cookie-domain`

**Status**: ✅ Supported

**Description**: Rewrites the `Domain=` attribute of upstream `Set-Cookie` response headers. Only the two-argument `"<from> <to>"` form is supported; any other value (including nginx's `"off"`) fails the render.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/proxy-cookie-domain: "backend.internal example.com"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    http-response replace-header Set-Cookie (.*)Domain=backend.internal(.*) \1Domain=example.com\2
```

---

### `nginx.ingress.kubernetes.io/proxy-cookie-path`

**Status**: ✅ Supported

**Description**: Rewrites the `Path=` attribute of upstream `Set-Cookie` response headers. Same `"<from> <to>"`-only contract as `proxy-cookie-domain`.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/proxy-cookie-path: "/internal /app"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    http-response replace-header Set-Cookie (.*)Path=/internal(.*) \1Path=/app\2
```

---

### `nginx.ingress.kubernetes.io/proxy-redirect-from`

**Status**: ✅ Supported

**Description**: Rewrites the `Location` and `Refresh` response headers coming from the upstream, replacing the `from` text with `proxy-redirect-to`'s value. Both annotations are required together, and neither value may contain spaces. `"default"` isn't supported — nginx derives it from `proxy_pass`, which has no HAProxy equivalent, so a warning comment is rendered and no rewrite happens; `"off"` disables the rewrite.

**Related annotations**:

| Annotation | Description |
|------------|-------------|
| `proxy-redirect-to` | Replacement text for the matched `from` value |

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/proxy-redirect-from: "http://backend.internal/"
  nginx.ingress.kubernetes.io/proxy-redirect-to: "https://example.com/"
```

**Generated HAProxy Configuration** (the `from` literal is regex-escaped):

```haproxy
backend my-backend
    http-response replace-header Location http://backend\.internal/ https://example.com/
    http-response replace-header Refresh http://backend\.internal/ https://example.com/
```

---

## Session affinity

Cookie-based session affinity — also called sticky sessions — pins a client to the same backend server across requests.

### `nginx.ingress.kubernetes.io/affinity`

**Status**: ✅ Supported

**Description**: Enable cookie-based session affinity.

**Valid values**: `cookie`

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `session-cookie-name` | Cookie name | `INGRESSCOOKIE` |
| `session-cookie-path` | `Path` cookie attribute | - |
| `session-cookie-domain` | `Domain` cookie attribute (HAProxy's `domain` keyword) | - |
| `session-cookie-secure` | Adds the `Secure` attribute when `"true"` | - |
| `session-cookie-samesite` | `SameSite` attribute: `Strict`, `Lax`, or `None` (other values fail the render) | - |
| `session-cookie-max-age` | `Max-Age` attribute (seconds) — the browser cookie lifetime | - |
| `session-cookie-expires` | Also emitted as `Max-Age` — HAProxy can't compute an absolute `Expires` date, and browsers treat both equivalently; `session-cookie-max-age` wins when both are set | - |
| `session-cookie-hash` | Accepted but not configurable — HAProxy's dynamic cookies always hash via `dynamic-cookie-key`, so the value is ignored with a rendered warning | - |

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/affinity: "cookie"
  nginx.ingress.kubernetes.io/session-cookie-name: "SERVERID"
  nginx.ingress.kubernetes.io/session-cookie-path: "/app"
  nginx.ingress.kubernetes.io/session-cookie-secure: "true"
  nginx.ingress.kubernetes.io/session-cookie-samesite: "Lax"
  nginx.ingress.kubernetes.io/session-cookie-max-age: "86400"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    cookie SERVERID insert indirect nocache dynamic attr Path=/app attr Secure attr SameSite=Lax attr Max-Age=86400
    dynamic-cookie-key <sha256-of-namespace/name>
```

---

## URL rewriting

### `nginx.ingress.kubernetes.io/rewrite-target`

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

Capture/regex rewrites (value contains `$N`/`\N`) stay as a per-backend `replace-path`:

```haproxy
backend my-backend
    http-request replace-path (.*) /\1
```

A **literal** rewrite (no capture, for example `rewrite-target: "/new"`) is instead written to
`path-rewrite.map` (`<backend_name> /new`) and applied by a shared frontend `set-path` rule,
so a rewrite change is a map-only, reload-free update.

---

### `nginx.ingress.kubernetes.io/app-root`

**Status**: ✅ Supported

**Description**: Redirect requests to root path (`/`) to the specified path.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/app-root: "/dashboard"
```

**Generated configuration**: the redirect target is registered host→path into the
shared `app-root.map` (built by base.yaml `features-175-app-root-map`); a single
shared frontend rule (base.yaml `frontend-filters-065-app-root`) applies it, so
adding or changing an app-root is a map-only, reload-free update.

```
# app-root.map
example.com /dashboard
```

```haproxy
# frontend (shared, static — emitted once regardless of how many hosts set app-root)
http-request redirect location %[var(txn.host),map(maps/app-root.map)] code 302 if { path / } { var(txn.host),map(maps/app-root.map) -m found }
```

---

## Redirects

### `nginx.ingress.kubernetes.io/ssl-redirect`

**Status**: ✅ Supported

**Description**: Redirect HTTP requests to HTTPS.

**Related annotations**:

| Annotation | Description | Redirect Code |
|------------|-------------|---------------|
| `ssl-redirect` | Enable SSL redirect | `308` |
| `force-ssl-redirect` | Force SSL redirect | `308` |

Both emit a `308` (Permanent Redirect), matching ingress-nginx, which sends both
via its `http-redirect-code` (default `308`). To change the code, set
`nginxHttpRedirectCode` (HAPTIC's equivalent of nginx's global
`http-redirect-code`) in values:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        nginxHttpRedirectCode: "301"   # valid: 301, 302, 303, 307, 308
```

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/ssl-redirect: "true"
```

**Generated configuration**: redirected hosts are registered into the shared
`ssl-redirect-<code>.map` (one map per distinct code; built by ssl.yaml
`features-160-ssl-redirect-map`); a single shared frontend rule per code
(ssl.yaml `frontend-filters-050-ssl-redirect`) applies it, so enabling or
disabling the redirect for a host is a map-only, reload-free update.

```
# ssl-redirect-308.map
example.com 1
```

```haproxy
# frontend (shared, static — one rule per distinct redirect code)
http-request redirect scheme https code 308 if !{ ssl_fc } { var(txn.host),map_str(maps/ssl-redirect-308.map) -m found }
```

---

### `nginx.ingress.kubernetes.io/permanent-redirect`

**Status**: ✅ Supported

**Description**: Redirect all requests for the Ingress's hosts to the specified URL. Host-scoped via a reload-free map; rules without a host are skipped.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `permanent-redirect-code` | HTTP status code for the redirect | `301` |

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/permanent-redirect: "https://new.example.com"
  nginx.ingress.kubernetes.io/permanent-redirect-code: "308"
```

---

### `nginx.ingress.kubernetes.io/temporal-redirect`

**Status**: ✅ Supported

**Description**: Redirect all requests for the Ingress's hosts to the specified URL. Host-scoped via a reload-free map; rules without a host are skipped.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `temporal-redirect-code` | HTTP status code for the redirect | `302` |

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/temporal-redirect: "https://maintenance.example.com"
```

---

### `nginx.ingress.kubernetes.io/from-to-www-redirect`

**Status**: ✅ Supported

**Description**: 301-redirect between each rule host and its `www.` counterpart, in whichever direction applies: host `example.com` redirects to `www.example.com`, host `www.example.com` redirects to `example.com`. The request path and scheme are preserved.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/from-to-www-redirect: "true"
```

**Generated configuration**: the host pairs land in `from-to-www.map`; two shared
scheme-split frontend rules apply it, so changing the host set is a map-only,
reload-free update.

```text
# from-to-www.map
example.com www.example.com
```

```haproxy
http-request redirect prefix https://%[var(txn.host),map(maps/from-to-www.map)] code 301 if { ssl_fc } { var(txn.host),map(maps/from-to-www.map) -m found }
http-request redirect prefix http://%[var(txn.host),map(maps/from-to-www.map)] code 301 if !{ ssl_fc } { var(txn.host),map(maps/from-to-www.map) -m found }
```

---

## `hsts`

### `nginx.ingress.kubernetes.io/hsts`

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

**Generated configuration**: the per-host header value is registered into the
shared `hsts.map` (built by base.yaml `features-190-hsts-map`); a single shared
frontend rule (base.yaml `frontend-filters-080-hsts`) applies it, so changing an
HSTS value is a map-only, reload-free update.

```
# hsts.map
example.com max-age=31536000; includeSubDomains; preload
```

```haproxy
# frontend (shared, static — emitted once for all per-Ingress HSTS hosts)
http-response set-header Strict-Transport-Security %[var(txn.host),map(maps/hsts.map)] if { ssl_fc } { var(txn.host),map(maps/hsts.map) -m found }
```

---

## `cors`

### `nginx.ingress.kubernetes.io/enable-cors`

**Status**: ✅ Supported

**Description**: Enable CORS handling for the ingress.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `enable-cors` | Enable CORS | - |
| `cors-allow-origin` | Allowed origins — comma-separated list, single-level `*.` wildcards; matched Origin is echoed back | `*` |
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

## Access control

### `nginx.ingress.kubernetes.io/whitelist-source-range`

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

### `nginx.ingress.kubernetes.io/denylist-source-range`

**Status**: ✅ Supported

**Description**: Comma-separated list of CIDRs denied access to this ingress.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/denylist-source-range: "203.0.113.0/24"
```

---

## Custom headers

### Custom request and response headers

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

## Server alias and default backend

### `nginx.ingress.kubernetes.io/server-alias`

**Status**: ✅ Supported

**Description**: Comma-separated extra hostnames that route exactly like the Ingress's first rule host. Each alias becomes a `host.map` entry pointing at the rule host's routing key, so every path already registered for that host applies to the alias — no backend or path duplication. Wildcard aliases (`*.example.com`) are normalized the same way rule hosts are.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/server-alias: "example.org,www.example.org"
```

**Generated configuration** (`host.map` entries, for an Ingress whose first rule host is `example.com`):

```text
example.org example.com
www.example.org example.com
```

---

### `nginx.ingress.kubernetes.io/default-backend`

**Status**: ✅ Supported

**Description**: Names a Service that serves requests matching one of this Ingress's hosts but none of its rule paths. The chart builds a dedicated backend pool for the Service's first port and adds a per-host catch-all entry to the path-prefix map — longest-prefix matching prefers the Ingress's own paths and falls through to the catch-all. Silently skipped when the Service doesn't resolve.

**Format**: `name` (resolves in the Ingress namespace) or `namespace/name`.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/default-backend: "error-pages"
```

**Generated configuration**:

```text
# path-prefix.map
example.com/ BACKEND:default_my-ingress_default-backend_error-pages
```

```haproxy
backend default_my-ingress_default-backend_error-pages
    default-server check
    server SRV_1 10.0.0.9:8080 enabled
```

---

## Authentication

### `nginx.ingress.kubernetes.io/auth-type`

**Status**: ✅ Supported

**Description**: Enable basic authentication using credentials from a Kubernetes Secret.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `auth-type` | Authentication type (only `basic` supported; `digest` fails the render) | - |
| `auth-secret` | Secret name (or `namespace/name`) | - |
| `auth-secret-type` | Secret layout: `auth-file` or `auth-map` (other values fail the render) | `auth-file` |
| `auth-realm` | Authentication realm | `Restricted` |

!!! note "Secret Format"
    With the default `auth-secret-type: auth-file`, the Secret has a single key named `auth` containing htpasswd-format `username:hash` lines. With `auth-secret-type: auth-map`, each Secret key is a username and its value is that user's hash — the same layout the haproxy-ingress library uses.

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

### `nginx.ingress.kubernetes.io/satisfy`

**Status**: ✅ Supported

**Description**: With `"any"`, a request passes if **either** its source IP is in `whitelist-source-range` **or** it authenticates via basic auth — instead of the default `"all"`, which requires both. The combined gate only forms when the Ingress has a whitelist, `auth-type: basic`, and a resolvable `auth-secret`. Unlike ingress-nginx, `satisfy` doesn't extend to external auth (`auth-url`).

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/satisfy: "any"
  nginx.ingress.kubernetes.io/whitelist-source-range: "10.0.0.0/8"
  nginx.ingress.kubernetes.io/auth-type: "basic"
  nginx.ingress.kubernetes.io/auth-secret: "basic-auth"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    acl ni_satisfy_ip_default_my-ingress src 10.0.0.0/8
    http-request auth realm "Restricted" if !ni_satisfy_ip_default_my-ingress !{ http_auth(ni_auth_default_basic-auth) }
```

The independent frontend whitelist deny and the unconditional backend auth challenge are suppressed for this Ingress and replaced by the combined check.

---

## External authentication

The library wires the `nginx.ingress.kubernetes.io/auth-*` family to the SPOA hub's `external-auth` plugin (v0.3.0+). When set, each request hits an HTTP auth subrequest before reaching the backend; the auth service's status code decides whether HAProxy forwards the request, redirects to a sign-in URL, or returns 401.

External auth is enforced independently of basic auth. When a route carries both `auth-url` and `auth-type: basic` + `auth-secret`, the two stack: a request must pass the external-auth subrequest *and* present valid basic-auth credentials — external auth denies at the frontend, basic auth challenges at the backend. You can't OR them; `satisfy: any` only OR-combines basic auth with the IP whitelist, not with external auth.

### Prerequisites

The SPOA hub sidecar with the `external-auth` plugin must be enabled:

```yaml
spoaHub:
  plugins:
    external-auth:
      enabled: true
```

The hub auto-enables when any plugin is on, and the spoa-hub template library auto-loads when the hub is enabled. Note: enabling `controller.templateLibraries.nginxIngress.enabled` **also** auto-enables `external-auth` (the nginx-ingress library is opt-in for this reason). See the [SPOA Hub operations guide](../operations/spoa-hub.md) for the full deployment surface.

!!! warning "Host-less rules error at render time"
    All external-auth annotations key their per-route lookup tables by `host+path`. An Ingress rule without an explicit `host` can't be enforced — silently skipping auth on a route the operator marked protected would be a security failure mode. The chart fails the Helm render with an explicit error identifying the offending Ingress.

---

### `nginx.ingress.kubernetes.io/auth-url`

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

### `nginx.ingress.kubernetes.io/auth-signin`

**Status**: ✅ Supported

**Description**: Browser-flow sign-in URL. When set, an auth failure produces a 302 redirect instead of a 401 — the standard pattern for OpenID Connect (OIDC) / Security Assertion Markup Language (SAML) flows. The deny rule still emits, so routes without `auth-signin` keep the API-friendly 401.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/auth-url: "https://auth.example.com/check"
  nginx.ingress.kubernetes.io/auth-signin: "https://login.example.com/oauth2/start?rd=$escaped_request_uri"
```

!!! note "nginx variables in the URL aren't expanded"
    HAProxy doesn't substitute `$escaped_request_uri` and friends at redirect time; the URL is used verbatim. Operators wanting the original-request preservation pattern should either set the param via the auth service (for example, oauth2-proxy handles it server-side) or extend the Stream Processing Offload Engine (SPOE) message body with the bits they need.

---

### `nginx.ingress.kubernetes.io/auth-method`

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
    `POST` / `PUT` / `PATCH` go to the auth service with an empty body — the plugin doesn't forward the original request payload.

---

### `nginx.ingress.kubernetes.io/auth-response-headers`

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

One `set-header` directive per unique header across all ingresses; per-route gating happens via the plugin's per-ingress `extract_headers` SPOE arg — routes that didn't list a header have its `txn` var unset, so the `var ... -m found` gate skips them.

!!! note "Failure-path response headers"
    nginx-ingress doesn't expose an annotation for "headers to send back to the client on auth failure" (the haproxy-ingress equivalent is `auth-headers-fail`). If you need that — for example `WWW-Authenticate` for Bearer challenges — switch to or add the haproxy-ingress library and use its annotation prefix.

!!! note "auth-snippet not wired"
    `nginx.ingress.kubernetes.io/auth-snippet` (used in nginx-ingress for arbitrary nginx config injection in the auth subrequest) is freeform nginx syntax with no parsable structure, so it can't be templated to HAProxy. The haproxy-ingress prefix has a typed `auth-headers-request` annotation that covers the most common use case.

---

## SSL features

### `nginx.ingress.kubernetes.io/ssl-passthrough`

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
- HTTP-level features (headers, path rewriting) aren't available for passthrough traffic
- Incoming client-cert mTLS (`auth-tls-*`) can't run on a passthrough host. Passthrough routes the connection by SNI to the TCP frontend in `mode tcp` and never terminates TLS, so HAProxy never sees the client certificate. If a host enables both, passthrough wins and the client-cert verification silently never runs.

---

## Canary deployments

### `nginx.ingress.kubernetes.io/canary`

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

!!! note "Canary and rate limiting compose per backend"
    Canary selection happens in the frontend (`use_backend ... if { rand(100) lt <weight> }`) before backend selection, and [rate limits](#rate-limiting) render into per-backend stick-tables. The main and canary Ingresses are separate backends, so each enforces the rate limit set on its own Ingress. A `limit-rps` on the main Ingress alone does *not* limit canary traffic — the split-off portion reaches the canary backend, which has no stick-table. To bound both, set the rate-limit annotation on the canary Ingress too. Gateway API weighted splitting has no rate-limit annotation, so there's nothing to combine there.

---

## Client certificate auth (mTLS)

The library wires the four `auth-tls-*` annotations that nginx-ingress uses for incoming client-cert verification. The CA bundle from the referenced Secret lands in the SSL cert dir and is referenced from the HAProxy `crt-list` line for the matching SNI as `[ca-file <path> verify <mode>]`, so HAProxy verifies incoming client certs at the TLS layer. The error-page and cert-passthrough annotations then react to the verification result via `ssl_c_verify`.

### `nginx.ingress.kubernetes.io/auth-tls-secret`

**Status**: ✅ Supported

**Description**: Reference to a `kubernetes.io/tls` Secret whose `ca.crt` field contains the CA bundle that signs the clients' certificates. The chart writes the CA to `ssl/<ns>-<secret>-client-ca.pem` and adds `[ca-file <path> verify <mode>]` to the crt-list line for every host on the annotated Ingress.

**Format**: `name` (resolves in the Ingress namespace) or `namespace/name`.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/auth-tls-secret: "client-ca"
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

**Generated crt-list entry**:

```
default_server-tls.pem [ocsp-update on ca-file ssl/default-client-ca-client-ca.pem verify required] api.example.com
```

!!! warning "Host-less rules error at render time"
    SNI-keyed verification can't be enforced on Ingress rules without a `host:`. The chart fails the Helm render with a descriptive error.

---

### `nginx.ingress.kubernetes.io/auth-tls-verify-client`

**Status**: ✅ Supported

**Description**: Client certificate verification mode.

**Valid values**:

| nginx value | HAProxy verify mode | Behaviour |
|-------------|---------------------|-----------|
| `on` (default) | `required` | Reject connections without a valid client cert |
| `off` | (no-op) | Don't enable verification on this host — the entry is skipped, falling through to the default crt-list line |
| `optional` | `optional` | Verify when a cert is presented; allow connections without |
| `optional_no_ca` | `optional` | Same as `optional` — HAProxy doesn't have a distinct mode for "verify but accept invalid"; operators wanting to accept self-signed certs should set `optional` and inspect `ssl_c_used` in their `auth-tls-error-page` logic |

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/auth-tls-secret: "client-ca"
  nginx.ingress.kubernetes.io/auth-tls-verify-client: "optional"
```

!!! note "auth-tls-verify-depth not wired"
    HAProxy's `crt-list` exposes per-line `ca-file` and `verify` but no per-line verify-depth — depth is global. Operators needing strict depth control should rely on the CA bundle scope instead (only certs signed within the bundle's chain depth validate).

---

### `nginx.ingress.kubernetes.io/auth-tls-error-page`

**Status**: ✅ Supported

**Description**: URL to redirect to (302) when client certificate verification fails.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/auth-tls-secret: "client-ca"
  nginx.ingress.kubernetes.io/auth-tls-error-page: "https://example.com/cert-required"
```

**Generated HAProxy Configuration**:

```haproxy
http-request redirect location https://example.com/cert-required code 302 if { ssl_c_verify gt 0 } { hdr(host) -i example.com }
```

The `ssl_c_verify gt 0` condition matches any verification error (including missing cert when `verify required` is set). Hosts without `auth-tls-error-page` fall through to HAProxy's default behaviour for failed verification (connection drop).

---

### `nginx.ingress.kubernetes.io/auth-tls-pass-certificate-to-upstream`

**Status**: ✅ Supported

**Description**: When `"true"`, forwards the verified client certificate and subject DN to the upstream backend as HTTP headers.

**Usage**:

```yaml
annotations:
  nginx.ingress.kubernetes.io/auth-tls-secret: "client-ca"
  nginx.ingress.kubernetes.io/auth-tls-pass-certificate-to-upstream: "true"
```

**Generated HAProxy Configuration**:

```haproxy
http-request set-header ssl-client-cert %[ssl_c_der,base64] if { hdr(host) -i example.com }
http-request set-header ssl-client-subject-dn %[ssl_c_s_dn] if { hdr(host) -i example.com }
```

---

## Web application firewall (`modsecurity`)

`nginx.ingress.kubernetes.io/modsecurity-snippet` and `enable-modsecurity` **are** supported, via the bundled SPOA hub **Coraza** WAF plugin (auto-enabled when the nginx-ingress or haproxy-ingress library is on). The `modsecurity-snippet` body (ModSecurity `SecRule` directives) is scanned into a per-Ingress `coraza-app.map` entry; `enable-modsecurity: "false"` adds the route to `coraza-disabled.map` so the WAF skips it. See the [SPOA Hub operations guide](../operations/spoa-hub.md) for the Coraza plugin's full configuration surface.

---

## Request mirroring

`nginx.ingress.kubernetes.io/mirror-target` **is** supported, via the bundled SPOA hub **mirror** plugin (the same machinery the Gateway API `RequestMirror` filter uses) — enable it with `spoaHub.plugins.mirror`. Mirroring is fire-and-forget: a copy of each matching request is sent to the target and its response is discarded. Only the authority (`host[:port]`) of the `scheme://host[:port]$request_uri` value is used; the plugin re-attaches the live request path/query. Any number of mirror-target Ingresses is supported: each appends its target to a per-request list that the single mirror SPOE message ships to the plugin, so adding or removing a mirror-target changes only the HAProxy frontend — never the SPOA hub's configuration, and the hub never reloads for it.

These constraints **fail the config** with an actionable message rather than silently doing nothing: the mirror plugin must be enabled, and the Ingress must define a `host` (host-less / default-backend mirroring is unsupported). `mirror-host` and `mirror-request-body: off` **aren't** honoured — the plugin always forces the mirrored Host to the target authority and always forwards the buffered request body.

---

## Unsupported annotations

The following nginx-ingress annotations aren't supported:

| Annotation | Reason |
|------------|--------|
| `mirror-host`, `mirror-request-body: off` | Only `mirror-target` is honoured (see [Request Mirroring](#request-mirroring)); the plugin forces the mirrored Host to the target authority and always forwards the buffered body |
| `enable-opentelemetry`, `opentelemetry-*` | Requires OpenTelemetry module |
| `enable-opentracing`, `opentracing-*` | Requires OpenTracing module |
| `server-snippet` | Nginx server-level directives have no HAProxy equivalent |
| `proxy-max-temp-file-size` | HAProxy uses in-memory buffering, no temp file concept |
| `stream-snippet` | Nginx stream directives have no HAProxy equivalent |
| `auth-snippet` | Freeform nginx configuration can't be translated to HAProxy; the haproxy-ingress library's `auth-headers-request` covers the common use case |
| `session-cookie-hash` | HAProxy's dynamic-cookie hashing isn't selectable; the value is ignored with a rendered warning |
| `auth-tls-verify-depth`, `proxy-ssl-verify-depth` | HAProxy has no per-host / per-server chain-depth option; the CA bundle scope bounds the accepted chain instead |
| `proxy-ssl-server-name` | Not read; control SNI toward the upstream via `proxy-ssl-name` |
| `canary-weight-total` | The canary weight base is fixed at 100 |

---

## Watched Resources

This library watches the following additional resources:

- **Secrets** (`v1/secrets`) — read for basic-auth credentials (`auth-secret`), incoming client-CA bundles (`auth-tls-secret`), and upstream TLS material (`proxy-ssl-secret`)

---

## Annotation Inventory

The machine-readable source of truth for this page is the library's migration coverage declaration in the chart (`charts/haptic/charts/nginx-ingress/90-migration-coverage.yaml`). It classifies every `nginx.ingress.kubernetes.io/*` annotation the library reads, and CI checks that each annotation it classifies as carried over has a reference entry above. The [migration guide](../migrating.md#from-ingress-nginx) renders the same data as a per-annotation support table.

## Access-log fields

The library contributes `mtls_verify` and `mtls_cn` to the
[structured access log](../haproxy-deployment.md#access-logging) when any Ingress
sets `auth-tls-secret` or `auth-tls-pass-certificate-to-upstream`. Its
rate-limit and WAF fail-closed gates also name themselves in the `denied_by`
field (`rate_limit_local`, `rate_limit_connections`, `basic_auth`,
`waf_policy_unavailable`).

## See also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Core HAProxy template
- [Ingress Library](ingress.md) - Standard Ingress support
- [HAProxy Ingress Library](haproxy-ingress.md) - `haproxy-ingress.github.io/*` annotations
- [HAProxyTech Library](haproxytech.md) - `haproxy.org/*` annotations
- [Nginx Ingress Documentation](https://kubernetes.github.io/ingress-nginx/user-guide/nginx-configuration/annotations/) - Original annotation reference
