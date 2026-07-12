# haproxy-ingress Library

The haproxy-ingress library implements `haproxy-ingress.github.io/*` annotations compatible with [jcmoraisjr/haproxy-ingress](https://haproxy-ingress.github.io/), a community HAProxy ingress controller. It supports path matching, backend configuration, session affinity, SSL features, access control, and more.

## Overview

This library is enabled by default.

See the `haproxy-ingress.github.io/*` annotations render to HAProxy config live:

<div class="pg-embed" markdown data-scenario="haproxy-ingress" data-facade="spec.templateSnippets.backend-directives-630-haproxy-ingress-health-checks" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="haproxy-ingress.github.io/* annotations rendered" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, change the `shop` Ingress's `haproxy-ingress.github.io/health-check-uri` from `/healthz` to `/readyz`, then watch the shop backend's health-check line update in the `haproxy.cfg` tab.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

The `backend storefront_shop_svc_shop_http` section changes `option httpchk GET /healthz` to `option httpchk GET /readyz`, so HAProxy's active health check now probes `/readyz`. The `default-server` line keeps its `check inter 2s` (from `backend-check-interval`) — only the probed URI moves.

</details>

</div>

!!! note "Migrating from jcmoraisjr/haproxy-ingress"
    If you are migrating from jcmoraisjr/haproxy-ingress, your existing `haproxy-ingress.github.io/*` annotations work without changes. See [Migrating from haproxy-ingress](../migrating.md#from-haproxy-ingress) for the cutover guide and the per-annotation verdict table, and [Annotations](../annotations.md) for the feature comparison between annotation libraries.

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
| `features-105-haproxy-ingress-ssl-redirect` | Processes `ssl-redirect`, `ssl-redirect-code` — registers hosts into the shared `ssl-redirect-<code>.map` (ssl.yaml emits the redirect rule) |
| `features-135-haproxy-ingress-redirect-to` | Processes `redirect-to`, `redirect-to-code` — registers host→location in the shared `redirect-loc-<code>.map` |
| `features-145-haproxy-ingress-app-root` | Processes `app-root` — registers host→path into the shared `app-root.map` (base.yaml emits the gated rule) |
| `features-155-haproxy-ingress-hsts` | Processes `hsts`, `hsts-max-age`, `hsts-include-subdomains`, `hsts-preload` — registers host→value into the shared `hsts.map` (base.yaml emits the response-header rule) |

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
| `backend-directives-630-haproxy-ingress-health-checks` | `health-check-uri`, `backend-check-interval`, `health-check-port`, `health-check-fall-count`, `health-check-rise-count` |
| `backend-directives-640-haproxy-ingress-proxy-protocol` | `proxy-protocol` |
| `backend-directives-650-haproxy-ingress-ssl-backend` | `secure-backends`, `backend-protocol`, `secure-sni`, `secure-verify-hostname`, `secure-verify-ca-secret`, `secure-crt-secret` |
| `backend-directives-660-haproxy-ingress-server-options` | `initial-weight`, other server-line options |
| `backend-directives-670-haproxy-ingress-session-affinity` | `affinity`, `session-cookie-*` |
| `backend-directives-680-haproxy-ingress-auth` | `auth-secret`, `auth-realm` (attaches userlist to the backend) |
| `backend-directives-685-haproxy-ingress-rate-limiting` | `limit-rps`, `limit-rpm`, `limit-whitelist` |
| `backend-directives-690-haproxy-ingress-rewrite-target` | `rewrite-target` (capture-group rewrites; literal rewrites go to `path-rewrite.map`) |
| `backend-directives-695-haproxy-ingress-agent-check` | `agent-check-port`, `agent-check-addr`, `agent-check-interval`, `agent-check-send` |
| `backend-directives-900-haproxy-ingress-config-backend` | `config-backend` |

### frontend-filters-* (HTTP-frontend request/response filters)

| Snippet | Annotations Processed |
|---------|----------------------|
| `frontend-filters-600-haproxy-ingress-forwardfor` | `forwardfor` |
| `frontend-filters-610-haproxy-ingress-access-control` | `allowlist-source-range` (or its deprecated alias `whitelist-source-range`), `denylist-source-range` |
| `frontend-filters-660-haproxy-ingress-cors` | `cors-enable`, `cors-*` |
| `frontend-filters-670-haproxy-ingress-headers` | `headers` |
| `frontend-filters-680-haproxy-ingress-default-backend-redirect` | `default-backend-redirect`, `default-backend-redirect-code` |

### Other extension points

| Snippet | Extension Point | Purpose |
|---------|-----------------|---------|
| `global-top-600-haproxy-ingress-auth` | `global-top-*` | Emits a `userlist auth_<secretNs>_<secretName>` per unique auth secret (deduplicated) |
| `backends-501-haproxy-ingress-ssl-passthrough` | `backends-*` | TCP-mode passthrough backends for hosts with `ssl-passthrough: "true"` |
| `map-host-650-haproxy-ingress-alias` | `map-host-*` | `server-alias` hostnames → the primary host's routing key in `host.map` |
| `map-hostregex-650-haproxy-ingress-alias` | `map-hostregex-*` | `server-alias-regex` patterns → the primary host's routing key in `host-regex.map` |
| `map-body-size-680-haproxy-ingress` | `map-body-size-*` | `proxy-body-size` limits as per-backend `body-size.map` entries |
| `map-path-rewrite-690-haproxy-ingress` | `map-path-rewrite-*` | Literal `rewrite-target` values as per-backend `path-rewrite.map` entries |
| `frontend-extra-650-haproxy-ingress-config-frontend` | `frontend-extra-*` | `config-frontend` raw directives in the HTTP frontend |
| `global-settings-650-haproxy-ingress-config-global` | `global-settings-*` | `config-global` raw directives in the `global` section |
| `defaults-settings-650-haproxy-ingress-config-defaults` | `defaults-settings-*` | `config-defaults` raw directives in the `defaults` section |

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

### haproxy-ingress.github.io/agent-check-port

**Status**: ✅ Supported

**Description**: Enable HAProxy's auxiliary agent check: HAProxy connects to an agent on each server at the given port, and the agent's reply can adjust the server's weight or state. `agent-check-port` is the enabler — setting `agent-check-addr`, `agent-check-interval`, or `agent-check-send` without it fails the render, because HAProxy disables an agent check that has no port.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `agent-check-port` | Port the agent listens on (required to enable) | - |
| `agent-check-addr` | Address the agent listens on | server address |
| `agent-check-interval` | Interval between agent checks | HAProxy's `agent-inter` default |
| `agent-check-send` | String sent to the agent on connect | - |

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/agent-check-port: "9998"
  haproxy-ingress.github.io/agent-check-addr: "10.0.0.50"
  haproxy-ingress.github.io/agent-check-interval: "5s"
  haproxy-ingress.github.io/agent-check-send: "check"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    default-server check agent-check agent-port 9998 agent-addr 10.0.0.50 agent-inter 5s agent-send check
```

Like the health-check options, the agent-check parameters live on the `default-server` line, so they are runtime-safe.

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
| `ssl-ciphers-backend` | Cipher list (TLS ≤ 1.2) for the backend connection |
| `ssl-cipher-suites-backend` | Cipher suites (TLS 1.3) for the backend connection |

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/secure-backends: "true"
  haproxy-ingress.github.io/backend-protocol: "h2"
  haproxy-ingress.github.io/secure-verify-ca-secret: "backend-ca"
  haproxy-ingress.github.io/secure-crt-secret: "client-cert"
  haproxy-ingress.github.io/ssl-ciphers-backend: "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256"
```

**Generated HAProxy Configuration**:

```haproxy
server SRV_1 10.0.0.1:8443 ssl alpn h2 ca-file /path/to/ca.pem crt /path/to/client.pem verify required ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256
```

`ssl-ciphers-backend` (HAProxy's `ciphers` server option) and `ssl-cipher-suites-backend` (`ciphersuites`) apply only when TLS to the backend is on — `secure-backends: "true"` or an `-ssl` `backend-protocol`. Without backend TLS they are ignored, because HAProxy rejects the keywords on a plaintext server line.

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

### haproxy-ingress.github.io/proxy-body-size

**Status**: ✅ Supported

**Description**: Maximum allowed request body size. Requests exceeding the limit receive a 413 response.

**Valid values**: Plain number (bytes), or with `k`/`m`/`g` suffix (case-insensitive). Value `0` (the upstream default) means unlimited — no limit is emitted.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/proxy-body-size: "10m"
```

**Generated configuration**: the per-backend limit is written to `body-size.map`
(keyed on the resolved backend), not into the backend section. A shared,
resource-agnostic frontend rule (base.yaml `frontend-filters-250-request-body-size`)
enforces it, so adding or changing the limit is a map-only, reload-free update.

```text
# body-size.map
default_my-ingress_svc_my-service_80 10485760
```

```haproxy
# frontend (shared, static — emitted once regardless of how many backends set a limit)
http-request set-var(txn.haptic_body_limit) var(txn.backend_name),map(maps/body-size.map,0),add(0)
http-request deny deny_status 413 if { var(txn.haptic_body_limit) -m int gt 0 } { req.body_size,sub(txn.haptic_body_limit) -m int gt 0 }
```

---

## Rate Limiting

### haproxy-ingress.github.io/limit-rps

**Status**: ✅ Supported

**Description**: Reject a source IP's requests with HTTP 429 once it exceeds the configured rate.

**Related annotations**:

| Annotation | Description |
|------------|-------------|
| `limit-rps` | Maximum requests per second per source IP |
| `limit-rpm` | Maximum requests per minute per source IP |
| `limit-whitelist` | Comma-separated CIDRs exempt from the limit |

The cap is hard — jcmoraisjr/haproxy-ingress grants a burst allowance on top of the configured rate, so expect stricter enforcement at the same value after migrating. HAProxy stores one request-rate counter per backend, so when both are set `limit-rps` wins and `limit-rpm` is ignored (the rendered config notes it in a comment). Invalid CIDRs in `limit-whitelist` fail the render.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/limit-rps: "10"
  haproxy-ingress.github.io/limit-whitelist: "10.0.0.0/8"
```

**Generated HAProxy Configuration**:

```haproxy
backend my-backend
    stick-table type ip size 100k expire 1s store http_req_rate(1s) peers localinstance
    http-request track-sc0 src
    http-request deny deny_status 429 if { sc_http_req_rate(0) gt 10 } !{ src 10.0.0.0/8 }
```

The `peers localinstance` reference carries the per-source counters across HAProxy reloads, so accumulated rates survive config churn.

---

## URL Rewriting

### haproxy-ingress.github.io/rewrite-target

**Status**: ✅ Supported

**Description**: Rewrite the request path before forwarding to the backend. Capture groups written in the nginx-compatible `$1`–`$9` form are translated to HAProxy's `\1`–`\9` backreferences.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/rewrite-target: "/$1"
```

**Generated HAProxy Configuration**:

Capture rewrites (the value contains `$N`) stay as a per-backend `replace-path`:

```haproxy
backend my-backend
    http-request replace-path (.*) /\1
```

A **literal** rewrite (no capture, e.g. `rewrite-target: "/new"`) is instead written to
`path-rewrite.map` (`<backend_name> /new`) and applied by a shared frontend `set-path` rule,
so changing it is a map-only, reload-free update.

---

## Raw Configuration Injection

The four `config-*` annotations inject operator-authored HAProxy directives verbatim into a configuration section. HAPTIC validates the resulting config before deploying it, but the directives are yours — a typo fails the render.

### haproxy-ingress.github.io/config-backend

**Status**: ✅ Supported

**Description**: Raw HAProxy directives injected into each of the Ingress's backend sections.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/config-backend: |
    http-send-name-header X-Backend-Server
    retries 5
```

---

### haproxy-ingress.github.io/config-global

**Status**: ✅ Supported

**Description**: Raw HAProxy directives injected into the `global` section. The section is process-wide: every Ingress carrying the annotation contributes its block (prefixed with an `# Ingress: <namespace>/<name>` comment), and deduplication across Ingresses is your responsibility.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/config-global: |
    tune.bufsize 65536
```

---

### haproxy-ingress.github.io/config-frontend

**Status**: ✅ Supported

**Description**: Raw HAProxy directives injected into HAPTIC's shared HTTP frontend — not a per-Ingress frontend, so the directives apply to all HTTP traffic. They render before the routing logic, so captures, ACLs, and early `http-request` rules you inject are in scope for the routing that follows. Like `config-global`, every annotated Ingress contributes its block.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/config-frontend: |
    capture request header X-Request-Id len 64
```

---

### haproxy-ingress.github.io/config-defaults

**Status**: ✅ Supported

**Description**: Raw HAProxy directives injected into the `defaults` section. Same process-wide semantics as `config-global`.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/config-defaults: |
    option httplog
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

### haproxy-ingress.github.io/whitelist-source-range

**Status**: ✅ Supported (deprecated alias)

**Description**: Deprecated alias of `allowlist-source-range`, honoured only when `allowlist-source-range` is absent on the same Ingress. Prefer `allowlist-source-range` for new Ingresses.

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

### haproxy-ingress.github.io/default-backend-redirect

**Status**: ✅ Supported

**Description**: Redirect requests that match one of the Ingress's hosts but none of its paths, instead of letting them fall through to the default backend.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `default-backend-redirect` | Target URL | - |
| `default-backend-redirect-code` | HTTP status code: 301, 302, 303, 307, or 308 (other values fail the render) | `302` |

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/default-backend-redirect: "https://landing.example.com"
  haproxy-ingress.github.io/default-backend-redirect-code: "301"
```

**Generated HAProxy Configuration**:

```haproxy
http-request redirect location %[var(txn.host),map(maps/default-backend-redirect-301.map)] code 301 if !{ var(txn.backend_name) -m found } { var(txn.host),map(maps/default-backend-redirect-301.map) -m found }
```

The host→URL pairs live in a per-code map, so changing the target URL is a map-only, reload-free update. The `!{ var(txn.backend_name) -m found }` guard fires exactly when the routing cascade matched no path — the same condition that otherwise selects the default backend.

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

## Server Alias

### haproxy-ingress.github.io/server-alias

**Status**: ✅ Supported

**Description**: Comma-separated extra exact hostnames that route like the Ingress's first rule host. Each alias becomes a `host.map` entry pointing at the primary host's routing key, so every path already registered for that host applies to the alias — no backend or path duplication.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/server-alias: "example.org, www.example.org"
```

**Generated configuration** (`host.map` entries, for an Ingress whose first rule host is `example.com`):

```text
example.org example.com
www.example.org example.com
```

---

### haproxy-ingress.github.io/server-alias-regex

**Status**: ✅ Supported

**Description**: A regular expression matching extra hostnames, routed to the Ingress's first rule host via `host-regex.map`. The routing cascade consults `host-regex.map` after an exact `host.map` miss, so one entry routes every matching hostname. The value is emitted verbatim and must be a HAProxy-compatible PCRE.

**Usage**:

```yaml
annotations:
  haproxy-ingress.github.io/server-alias-regex: "^www\\.example\\.(com|org)$"
```

**Generated configuration** (`host-regex.map` entry):

```text
^www\.example\.(com|org)$ example.com
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

The hub auto-enables when any plugin is on, and the spoa-hub template library auto-loads when the hub is enabled. See the [SPOA Hub operations guide](../operations/spoa-hub.md) for the full deployment surface.

!!! warning "Not auto-enabled with this library"
    Unlike the nginx-ingress library, enabling the haproxy-ingress library does not auto-enable the `external-auth` plugin (the library is on by default, and auto-enabling would deploy the SPOA hub sidecar for everyone). Without the plugin, `auth-url` is silently not enforced — set `spoaHub.plugins.external-auth.enabled=true` explicitly.

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
    `POST` / `PUT` / `PATCH` go to the auth service with an empty body — the plugin does not forward the original request payload. Auth services that need the body should read it via sample-fetch args (see [SPOA hub operations](../operations/spoa-hub.md)) or use header-based auth.

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

### haproxy-ingress.github.io/oauth

**Status**: ✅ Supported

**Description**: Convenience wiring for [oauth2-proxy](https://oauth2-proxy.github.io/oauth2-proxy/). Setting `oauth: "oauth2_proxy"` (or `"oauth2-proxy"` — the only accepted values) desugars onto the external-auth machinery: it derives the auth URL, sign-in redirect, subrequest method, and success-header forwarding that you would otherwise wire via `auth-url`, `auth-signin`, `auth-method`, and `auth-headers-succeed`. The Ingress must route the `oauth-uri-prefix` path (default `/oauth2`) to the oauth2-proxy Service — the auth URL is derived from that path's Service, and the render fails without such a path.

**Related annotations**:

| Annotation | Description | Default |
|------------|-------------|---------|
| `oauth` | `oauth2_proxy` / `oauth2-proxy` (other values fail the render) | - |
| `oauth-uri-prefix` | The Ingress path routing to the oauth2-proxy Service | `/oauth2` |
| `oauth-headers` | Auth-reply headers forwarded to the backend on success | `X-Auth-Request-Email` |

**Usage**:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: oauth-protected
  annotations:
    haproxy-ingress.github.io/oauth: "oauth2_proxy"
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
                name: app-backend
                port:
                  number: 80
          - path: /oauth2
            pathType: Prefix
            backend:
              service:
                name: oauth2-proxy
                port:
                  number: 4180
```

**Effective external-auth configuration** (what the desugaring derives):

```text
auth-url     http://oauth2-proxy.<namespace>.svc:4180/oauth2/auth
auth-signin  /oauth2/start?rd=%[path]
auth-method  HEAD
forwarded success headers: X-Auth-Request-Email
```

An explicit `auth-url` on the same Ingress takes precedence and disables the oauth desugaring entirely; an explicit `auth-signin`, `auth-method`, or `auth-headers-succeed` overrides only its derived value.

!!! warning "Plaintext auth URL"
    The derived auth URL is plain `http://` toward the in-cluster oauth2-proxy Service, so the external-auth plugin must allow plaintext: set `allow_plaintext = true` in `spoaHub.plugins.external-auth.params`.

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

**Description**: Client certificate verification mode.

**Valid values**:

| Value | HAProxy verify mode | Behaviour |
|-------|---------------------|-----------|
| `on` (default) | `required` | Reject connections without a valid client cert |
| `off` | (no-op) | Don't enable verification on this host — the entry is skipped, falling through to the default crt-list line |
| `optional` | `optional` | Verify when a cert is presented; allow connections without |
| `optional_no_ca` | `optional` | Same as `optional` — HAProxy doesn't have a distinct mode for "verify but accept invalid"; operators wanting to accept self-signed certs should set `optional` and inspect `ssl_c_used` in their `auth-tls-error-page` logic |

Other values fail the render.

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

Opt an Ingress into the SPOA hub **Coraza** WAF with `haproxy-ingress.github.io/waf: "modsecurity"` (the only supported value). Each `<host><path>` is added to `haproxy-ingress-waf.map`, whose value is the enforcement mode from `haproxy-ingress.github.io/waf-mode`: `deny` (default) blocks on a rule hit, `detect` runs the WAF in shadow mode (logs without blocking). Setting `waf-mode` to anything other than `deny`/`detect`, or setting it without `waf`, fails the render. The Coraza plugin auto-enables when the haproxy-ingress or nginx-ingress library is on. See the [SPOA Hub operations guide](../operations/spoa-hub.md) for the plugin's configuration surface.

---

## Unsupported Annotations

The library reads only the annotations documented on this page; it ignores any other `haproxy-ingress.github.io/*` key. Two annotations are accepted for compatibility but have no effect:

| Annotation | Reason |
|------------|--------|
| `auth-tls-strict` | Upstream defaults it to true (fail-closed on a missing/invalid client CA); here a missing CA skips mTLS for the Ingress (fail-open), and this annotation can't restore fail-closed. For soft verification use `auth-tls-verify-client: optional` instead. |
| `docs` | A pointer to jcmoraisjr/haproxy-ingress documentation, not a configuration key. |

Before cutting over, run `migrate-check` against your manifests to get a per-annotation verdict for exactly the annotations you use — see [Check what will change](../migrating.md#step-0-check-what-will-change).

---

## Watched Resources

This library watches the following additional resources:

- **Secrets** (`v1/secrets`) — read for basic-auth credentials (`auth-secret`), backend TLS material (`secure-verify-ca-secret`, `secure-crt-secret`), and incoming client-CA bundles (`auth-tls-secret`)

## Annotation Inventory

The machine-readable source of truth for this page is the library's migration coverage declaration in the chart (`charts/haptic/charts/haproxy-ingress/90-migration-coverage.yaml`). It classifies every `haproxy-ingress.github.io/*` annotation the library reads, and CI checks that each annotation it classifies as carried over has a reference entry above. The [migration guide](../migrating.md#from-haproxy-ingress) renders the same data as a per-annotation support table.

## See Also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Path matching infrastructure
- [Ingress Library](ingress.md) - Standard Ingress support
- [HAProxyTech Library](haproxytech.md) - `haproxy.org/*` annotations
- [HAProxy Ingress Documentation](https://haproxy-ingress.github.io/docs/configuration/keys/) - Original annotation reference
