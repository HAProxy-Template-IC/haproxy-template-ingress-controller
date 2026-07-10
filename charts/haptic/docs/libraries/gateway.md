# Gateway API Library

The Gateway API library provides support for Kubernetes Gateway API resources, enabling modern, expressive traffic routing through HAProxy.

## Overview

The Gateway API library implements the [Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/) specification, providing:

- HTTPRoute and GRPCRoute support
- Advanced request matching (method, headers, query parameters)
- Traffic splitting with weighted backends
- Request/response header modification
- URL rewrites and redirects
- TLS termination and SSL passthrough

This library is **enabled by default**.

!!! warning "Gateway API CRDs Required"
    The Gateway API library requires Gateway API CRDs to be installed in your cluster. Without them, the library is not merged into the configuration.

Watch an HTTPRoute compile down to HAProxy config live:

<div class="pg-embed" markdown data-scenario="gateway" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Gateway API → HAProxy config" data-height="440">

<p class="pg-task" markdown>**Try it:** In the **Resources** panel, add `- www.example.com` under the `api` HTTPRoute's `spec.hostnames`, then open the **maps** tab and watch `host.map` gain a second entry.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

`host.map`'s provenance comment goes from `# HTTPRoute: platform/api (1 hosts)` to `(2 hosts)`, and a second `www.example.com…` line appears next to the `api.example.com…` one — the gateway library writes one entry per effective hostname. It derives them from `spec.hostnames` in `map-host-500-gateway` (`charts/haptic/charts/gateway/40-maps-host.yaml:437`), emitting one `<hostKey> <hostKey>` line per host (line 205). Each key carries a `:<port>` suffix because the demo Gateway's HTTP listener is a catch-all with no hostname, which scopes its routes to that Gateway's own bind port.

</details>

</div>

## Configuration

```yaml
controller:
  templateLibraries:
    gateway:
      enabled: true  # Enabled by default
```

## Extension Points

The Gateway API library hooks into these extension points from base.yaml. Snippet names encode their priority via a numeric prefix (see [Template Libraries → Snippet Priority](../template-libraries.md#snippet-priority)).

| Extension Point | Snippet | What It Generates |
|-----------------|---------|-------------------|
| `features-*` | `features-100-gateway-ssl-passthrough` | Populates `gf["sslPassthroughBackends"]` from HTTPRoutes annotated for SNI passthrough |
| `features-*` | `features-100-gateway-tls` | Registers TLS certificates from Gateway listeners into `gf["tlsCertificates"]` |
| `backends-*` | `backends-500-gateway` | HTTP backend blocks for every unique `(namespace, service, port)` touched by an HTTPRoute or GRPCRoute |
| `backends-*` | `backends-501-gateway-ssl-passthrough` | TCP-mode passthrough backends for listeners with `tls.mode: Passthrough` |
| `map-host-*` | `map-host-500-gateway` | Host → group mapping entries derived from `spec.hostnames` |
| `map-path-exact-*` | `map-path-exact-500-gateway` | Entries for `path.type: Exact` matches |
| `map-pfxexact-*` | `map-pfxexact-500-gateway` | Prefix-exact entries (e.g. matching `/foo` but not `/foobar`) |
| `map-path-prefix-*` | `map-path-prefix-500-gateway` | Entries for `path.type: PathPrefix` matches |
| `map-path-regex-*` | `map-path-regex-500-gateway` | Entries for `path.type: RegularExpression` matches |
| `map-weighted-backend-*` | `map-weighted-backend-500-gateway` | Weighted-multi-backend entries for traffic-split `backendRefs[].weight` |
| `frontend-matchers-advanced-*` | `frontend-matchers-advanced-010-route-id-setup` | Sets up per-request route-ID variables before the 500-range matchers run |
| `frontend-matchers-advanced-*` | `frontend-matchers-advanced-500-gateway` | Method, header, and query-parameter matchers |
| `frontend-matchers-advanced-*` | `frontend-matchers-advanced-900-path-match` | Final path-match backend-selection logic |
| `frontend-filters-*` | `frontend-filters-500-gateway-request-header` | `RequestHeaderModifier` filter |
| `frontend-filters-*` | `frontend-filters-500-gateway-response-header` | `ResponseHeaderModifier` filter |
| `frontend-filters-*` | `frontend-filters-500-gateway-redirect` | `RequestRedirect` filter |
| `frontend-filters-*` | `frontend-filters-500-gateway-urlrewrite` | `URLRewrite` filter |
| `http-bind-extra-*` | `http-bind-extra-050-gateway-multi-port-bind` | One `bind *:<port>` per non-default Gateway HTTP listener port (skips chart-static `httpPort` and `httpsPort` to avoid duplicate-bind errors) |
| `https-bind-extra-*` | `https-bind-extra-050-gateway-multi-port-bind` | One `bind *:<port> ssl crt-list ...` per non-default Gateway HTTPS listener port (skips chart-static `httpsPort` and `httpPort` to avoid duplicate-bind errors); reuses `util-ssl-bind-options` so the SSL handshake matches the chart-static HTTPS bind |
| `status-patches-*` | `status-patches-200-gateway` | Patches Gateway / HTTPRoute / GRPCRoute `status` (Accepted, ResolvedRefs, attachedRoutes, addresses) |

### Injecting Custom Configuration

You can extend Gateway API functionality by adding snippets with the right prefix and priority:

```yaml
controller:
  config:
    templateSnippets:
      # Runs before the 500-range gateway matchers so the deny takes effect per route
      frontend-matchers-advanced-400-custom-auth:
        template: |
          # Custom authentication check
          http-request deny if { var(txn.matched_route) -m found } !{ req.hdr(Authorization) -m found }
```

## Watched Resources

The gateway library declares these resources in its `watchedResources`:

Every Gateway API kind is an **optional** watched resource with an ordered
`apiVersions` candidate list. At startup — and again whenever a relevant CRD
is installed, upgraded, or removed — the controller resolves each entry to
the first candidate the cluster serves and strips the features of kinds it
doesn't serve at any candidate version. You don't redeploy the chart when
you install or upgrade Gateway API; support activates by itself.

| Resource | API version candidates (preferred first) | Purpose |
|----------|-------------------------------------------|---------|
| Gateways | v1, v1beta1 | Gateway definitions (filtered by `gatewayClass.name`) |
| GatewayClasses | v1, v1beta1 | GatewayClass definitions (field-selector scoped to owned class) |
| HTTPRoutes | v1, v1beta1 | HTTP routing rules |
| GRPCRoutes | v1, v1alpha2 | gRPC routing rules |
| TLSRoutes | v1, v1alpha3, v1alpha2 | TLS passthrough routing rules |
| TCPRoutes | v1, v1alpha2 | Raw-TCP listener forwarding |
| ReferenceGrants | v1, v1beta1 | Cross-namespace reference policy |
| ListenerSets | v1 | Additional listeners attached to Gateways (GEP-1713) |
| BackendTLSPolicies | v1, v1alpha3 | Backend TLS validation (v1alpha2 is excluded: incompatible shape) |
| Namespaces | v1 (required) | Namespace metadata for listener attachment evaluation |
| Services | v1 (required) | Service discovery |
| EndpointSlices | discovery.k8s.io/v1 (required) | Backend endpoints |

Features whose *fields* don't exist in an older release's schemas (for
example the HTTPRoute CORS filter before Gateway API v1.6, or Gateway
frontend mTLS) stay inactive on that release; everything else works. The
per-release expectations are pinned by `tests/schemas-ga-*` and
`scripts/test-templates.sh`.

TLS Secrets are watched by the SSL library (not gateway), and controller-service address discovery for status patches is owned by base.yaml. See [SSL Library](ssl.md) and [Base Library](base.md).

## Architecture

The `gateway/` library:

- Declares `httproutes` and `grpcroutes` as watched resources
- Implements backend generation for Gateway routes
- Adds routing rules to HAProxy map files
- Plugs into extension points defined in `base.yaml`

This architecture allows the controller to remain resource-agnostic while the chart provides specific resource support.

---

**Status legend:** ✅ Supported · ⚠️ Partial or untested · ❌ Not implemented

## HTTPRoute Support

### spec.parentRefs

| Field | Status | Notes |
|-------|--------|-------|
| `parentRefs[].name` | ✅ Supported | Gateway reference |
| `parentRefs[].namespace` | ⚠️ Partial | Field exists but cross-namespace not tested |
| `parentRefs[].sectionName` | ⚠️ Partial | Used for listener-level `attachedRoutes` counting in status; routing not listener-specific |
| `parentRefs[].port` | ✅ Supported | Pins the route to Gateway listeners on the named port (attachment selection per spec); a route only attaches to listeners whose port matches |

### spec.hostnames

| Field | Status | Notes |
|-------|--------|-------|
| `hostnames[]` | ✅ Supported | Multiple hostnames per route |
| Wildcard hostnames (e.g., `*.example.com`) | ⚠️ Untested | May work but not validated |
| Empty hostnames list | ✅ Supported | Matches all hosts |

**Example:**

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: example
spec:
  hostnames:
    - "example.com"
    - "www.example.com"
  rules:
    - backendRefs:
        - name: example-svc
          port: 80
```

### spec.rules[].matches - Path Matching

| Field | Status | Notes |
|-------|--------|-------|
| `matches[].path.type: Exact` | ✅ Supported | Exact path match using HAProxy map |
| `matches[].path.type: PathPrefix` | ✅ Supported | Prefix match using HAProxy map_beg |
| `matches[].path.type: RegularExpression` | ✅ Supported | Regex match using HAProxy map_reg |
| `matches[].path.value` | ✅ Supported | Path value used in matching |
| Empty matches list | ✅ Supported | Defaults to PathPrefix `/` |

**Path Match Priority:** Exact > Regex > Prefix-exact > Prefix (configurable via libraries)

**Example - Path matching:**

```yaml
spec:
  rules:
    # Exact path match
    - matches:
        - path:
            type: Exact
            value: /api/v1/users
      backendRefs:
        - name: users-api-svc
          port: 8080

    # Prefix match
    - matches:
        - path:
            type: PathPrefix
            value: /api
      backendRefs:
        - name: api-svc
          port: 8080

    # Regex match
    - matches:
        - path:
            type: RegularExpression
            value: ^/api/v[0-9]+/.*
      backendRefs:
        - name: versioned-api-svc
          port: 8080
```

The path type decides which map file HAProxy consults — flip it live:

<div class="pg-embed" markdown data-scenario="gateway" data-tab="maps" data-controls="tabs,resources" data-title="Path type → map file" data-height="440">

<p class="pg-task" markdown>**Try it:** In the **Resources** panel, change the `api` HTTPRoute's `path.type` from `PathPrefix` to `Exact`, then watch the entry move between map files in the **maps** tab.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

With `PathPrefix`, the route's entry (`api.example.com…/ GW_ROUTE_ID:http:platform_api_0`) sits in `path-prefix.map`. Switch to `Exact` and the same entry moves to `path-exact.map`, leaving `path-prefix.map` empty. Each path type is filled by its own snippet — `map-path-exact-500-gateway` and `map-path-prefix-500-gateway` (`charts/haptic/charts/gateway/41-maps-path.yaml:166` and `:182`) — and a route's entry only lands in the map whose `pathType` matches (`41-maps-path.yaml:22`).

</details>

</div>

### spec.rules[].matches - Method, Header and Query Matching

| Field | Status | Notes |
|-------|--------|-------|
| `matches[].method` | ✅ Supported | HTTP method matching (GET, POST, etc.) |
| `matches[].headers[]` | ✅ Supported | Header-based routing with exact and regex matching |
| `matches[].headers[].type: Exact` | ✅ Supported | Exact header value matching |
| `matches[].headers[].type: RegularExpression` | ✅ Supported | Regex header value matching |
| `matches[].headers[].name` | ✅ Supported | Case-insensitive header name |
| `matches[].headers[].value` | ✅ Supported | Header value to match |
| `matches[].queryParams[]` | ✅ Supported | Query parameter matching |
| `matches[].queryParams[].type: Exact` | ✅ Supported | Exact query parameter value matching |
| `matches[].queryParams[].type: RegularExpression` | ✅ Supported | Regex query parameter matching |
| `matches[].queryParams[].name` | ✅ Supported | Query parameter name |
| `matches[].queryParams[].value` | ✅ Supported | Query parameter value to match |

**Match Precedence (Gateway API v1 spec):**

When multiple routes match the same request, ties are broken in the following order:

1. **Path specificity** - Exact > RegularExpression > PathPrefix (by length)
2. **Method matchers** - Routes with method matchers have higher priority
3. **Header matchers** - More header matchers = higher priority
4. **Query parameter matchers** - More query matchers = higher priority
5. **Creation timestamp** - Older routes have priority
6. **Alphabetical order** - By namespace/name as final tie-breaker

**Example - Method matching:**

```yaml
spec:
  rules:
    # Match only GET requests
    - matches:
        - path:
            type: PathPrefix
            value: /api
          method: GET
      backendRefs:
        - name: api-read-svc
          port: 8080

    # Match only POST requests
    - matches:
        - path:
            type: PathPrefix
            value: /api
          method: POST
      backendRefs:
        - name: api-write-svc
          port: 8080
```

**Example - Header matching:**

```yaml
spec:
  rules:
    # Exact header match
    - matches:
        - path:
            type: PathPrefix
            value: /api
          headers:
            - name: X-API-Version
              type: Exact
              value: "v2"
      backendRefs:
        - name: api-v2-svc
          port: 8080

    # Regex header match
    - matches:
        - path:
            type: PathPrefix
            value: /api
          headers:
            - name: User-Agent
              type: RegularExpression
              value: ".*Mobile.*"
      backendRefs:
        - name: mobile-api-svc
          port: 8080
```

**Example - Query parameter matching:**

```yaml
spec:
  rules:
    # Exact query parameter match
    - matches:
        - path:
            type: PathPrefix
            value: /search
          queryParams:
            - name: category
              type: Exact
              value: electronics
      backendRefs:
        - name: electronics-search-svc
          port: 8080

    # Regex query parameter match
    - matches:
        - path:
            type: PathPrefix
            value: /api
          queryParams:
            - name: version
              type: RegularExpression
              value: "^v[2-3]$"
      backendRefs:
        - name: modern-api-svc
          port: 8080
```

**Example - Complex matching with precedence:**

```yaml
spec:
  rules:
    # Higher priority: method + headers + query
    - matches:
        - path:
            type: Exact
            value: /api/users
          method: POST
          headers:
            - name: Content-Type
              type: Exact
              value: application/json
          queryParams:
            - name: action
              type: Exact
              value: create
      backendRefs:
        - name: user-create-svc
          port: 8080

    # Lower priority: only path matching
    - matches:
        - path:
            type: Exact
            value: /api/users
      backendRefs:
        - name: user-generic-svc
          port: 8080
```

Add a matcher to the demo route and watch the frontend gain a condition:

<div class="pg-embed" markdown data-scenario="gateway" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Method / header / query matchers" data-height="440">

<p class="pg-task" markdown>**Try it:** In the **Resources** panel, add `method: GET` to the `api` HTTPRoute's match (as a sibling of its `path`), then find the matcher line in the `haproxy.cfg` tab.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

Under `# Advanced route matching`, the rule's provenance comment changes from `- path-only` to `- method GET`, and its `http-request set-var(txn.gw_rule_id) …` guard gains a `{ method GET }` condition. `frontend-matchers-advanced-500-gateway` emits that condition (`charts/haptic/charts/gateway/60-frontend.yaml:313`) and the comment (`:394`). Header and query matchers build the same guard: a `headers:` entry adds `{ req.hdr(<name>) "<value>" }` (`:324`) and a `queryParams:` entry adds `{ urlp(<name>) "<value>" }` (`:339`).

</details>

</div>

### spec.rules[].filters

| Filter Type | Conformance | Status | Notes |
|-------------|-------------|--------|-------|
| `RequestHeaderModifier` | Core | ✅ Supported | Add/Set/Remove request headers |
| `ResponseHeaderModifier` | Extended | ✅ Supported | Add/Set/Remove response headers |
| `RequestRedirect` | Core | ✅ Supported | HTTP redirects with scheme/hostname/port/path/statusCode |
| `URLRewrite` | Extended | ✅ Supported | Path and hostname rewriting |
| `RequestMirror` | Extended | ✅ Supported | Per-route request mirroring via the bundled spoa-hub `mirror` plugin (enable `spoaHub.plugins.mirror`); supports percent/fraction sampling and multiple mirrors per rule |
| `ExtensionRef` | Implementation-specific | ⚠️ Partial | Only `kind: SSLPassthrough` is honored (flags the HTTPRoute for TLS passthrough); other kinds planned as the Gateway API equivalent of Ingress annotations |

#### RequestHeaderModifier Filter

The `RequestHeaderModifier` filter modifies HTTP request headers before forwarding to backends. Supports set (replace), add (append), and remove operations.

**Supported Operations:**

- `set` - Sets a header value, replacing any existing values
- `add` - Adds a header value, appending to existing values
- `remove` - Removes all values for a header

**Example - Set and add headers:**

```yaml
spec:
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /api
      filters:
        - type: RequestHeaderModifier
          requestHeaderModifier:
            set:
              - name: X-API-Version
                value: "v2"
            add:
              - name: X-Request-ID
                value: "%[rand]"
            remove:
              - Authorization
      backendRefs:
        - name: api-svc
          port: 8080
```

**HAProxy Implementation:**

Generates `http-request` directives with conditions based on route matching:

```haproxy
# Set header (replaces existing)
http-request set-header X-API-Version "v2" if <route-conditions>

# Add header (appends to existing)
http-request add-header X-Request-ID "%[rand]" if <route-conditions>

# Remove header
http-request del-header Authorization if <route-conditions>
```

#### ResponseHeaderModifier Filter

The `ResponseHeaderModifier` filter modifies HTTP response headers before returning to clients. Supports the same set/add/remove operations as RequestHeaderModifier.

**Example - Add security headers:**

```yaml
spec:
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      filters:
        - type: ResponseHeaderModifier
          responseHeaderModifier:
            set:
              - name: Strict-Transport-Security
                value: "max-age=31536000; includeSubDomains"
              - name: X-Frame-Options
                value: "DENY"
            add:
              - name: X-Custom-Header
                value: "custom-value"
            remove:
              - Server
              - X-Powered-By
      backendRefs:
        - name: web-svc
          port: 80
```

**HAProxy Implementation:**

Generates `http-response` directives:

```haproxy
# Set response header (replaces existing)
http-response set-header Strict-Transport-Security "max-age=31536000; includeSubDomains" if <route-conditions>

# Add response header (appends to existing)
http-response add-header X-Custom-Header "custom-value" if <route-conditions>

# Remove response header
http-response del-header Server if <route-conditions>
```

#### RequestRedirect Filter

The `RequestRedirect` filter implements HTTP redirects with support for scheme, hostname, port, path, and status code modifications. **Only available for HTTPRoute** (not applicable to gRPC).

**Supported Fields:**

- `scheme` - Change protocol (http/https)
- `hostname` - Change destination hostname
- `port` - Change destination port
- `path.type` - ReplaceFullPath or ReplacePrefixMatch
- `path.replaceFullPath` - New absolute path
- `path.replacePrefixMatch` - New path prefix
- `statusCode` - HTTP status code (default: 302)

**Example - HTTPS redirect:**

```yaml
spec:
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      filters:
        - type: RequestRedirect
          requestRedirect:
            scheme: https
            statusCode: 301
```

**Example - Path rewrite with redirect:**

```yaml
spec:
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /old-api
      filters:
        - type: RequestRedirect
          requestRedirect:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /api/v2
            statusCode: 308
```

**HAProxy Implementation:**

Generates `http-request redirect` directives:

```haproxy
# HTTPS redirect
http-request redirect scheme https code 301 if <route-conditions>

# Path prefix replacement
http-request redirect prefix "/api/v2" code 308 if <route-conditions>

# Full path replacement
http-request redirect location "https://example.com/new/path" code 302 if <route-conditions>
```

#### URLRewrite Filter

The `URLRewrite` filter rewrites request URLs before forwarding to backends, supporting both hostname and path modifications. **Only available for HTTPRoute** (not applicable to gRPC).

**Supported Fields:**

- `hostname` - Rewrite the Host header
- `path.type` - ReplaceFullPath or ReplacePrefixMatch
- `path.replaceFullPath` - New absolute path
- `path.replacePrefixMatch` - New path prefix

**Example - Strip path prefix:**

```yaml
spec:
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /api/v1
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
      backendRefs:
        - name: api-svc
          port: 8080
```

**Example - Hostname and path rewrite:**

```yaml
spec:
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /external
      filters:
        - type: URLRewrite
          urlRewrite:
            hostname: internal-api.example.svc.cluster.local
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /api
      backendRefs:
        - name: internal-api-svc
          port: 8080
```

**HAProxy Implementation:**

Generates `http-request` directives for header and path manipulation:

```haproxy
# Hostname rewrite
http-request set-header Host "internal-api.example.svc.cluster.local" if <route-conditions>

# Full path replacement
http-request set-path "/new/path" if <route-conditions>

# Prefix replacement (using regex)
http-request replace-path "^/api/v1(.*)" "/\1" if <route-conditions>
```

**Difference from RequestRedirect:**

- **URLRewrite** rewrites the request and forwards to backend (transparent to client)
- **RequestRedirect** sends HTTP redirect response to client (client sees new URL)

### spec.rules[].backendRefs

| Field | Status | Notes |
|-------|--------|-------|
| `backendRefs[].name` | ✅ Supported | Service name |
| `backendRefs[].namespace` | ⚠️ Partial | Not explicitly handled, likely defaults to route namespace |
| `backendRefs[].port` | ✅ Supported | Service port number |
| `backendRefs[].weight` | ✅ Supported | Traffic splitting with weighted distribution |
| `backendRefs[].filters[]` | ⚠️ Partial | `RequestHeaderModifier` emitted per-backend (rule-scoped via `gw_rule_id`); other filter types not handled at the backendRef level |
| Multiple backends | ✅ Supported | Weighted traffic splitting using MULTIBACKEND qualifier |
| Single backend | ✅ Supported | Optimized with BACKEND qualifier (avoids weighted logic) |
| Omitted weight | ✅ Supported | Defaults to weight 1 |

**Weighted Backend Implementation:**

The gateway library uses HAProxy's `rand()` function and map-based selection for O(1) weighted routing:

- Weights are pre-expanded into map entries (e.g., 70/30 split = 100 map entries)
- Entry 0-69 map to backend 1, entries 70-99 map to backend 2
- HAProxy generates random number % total_weight and looks up backend in map

**Example - Weighted traffic splitting:**

```yaml
spec:
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /app
      backendRefs:
        # 70% of traffic
        - name: app-v1
          port: 80
          weight: 70
        # 30% of traffic
        - name: app-v2
          port: 80
          weight: 30
```

**Example - Default weights:**

```yaml
spec:
  rules:
    - backendRefs:
        # Omitted weight defaults to 1 (50/50 split)
        - name: backend-a
          port: 80
        - name: backend-b
          port: 80
```

Split the demo route's traffic and inspect the generated weight map:

<div class="pg-embed" markdown data-scenario="gateway" data-tab="maps" data-controls="tabs,resources" data-title="Weighted traffic split → map" data-height="440">

<p class="pg-task" markdown>**Try it:** In the **Resources** panel, give the `api` HTTPRoute a second backend: add `weight: 90` to its existing `api` ref and append `- {name: api-canary, port: 80, weight: 10}`, then open `weighted-multi-backend.map` in the **maps** tab.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

`weighted-multi-backend.map` fills with 100 entries keyed `<0-99>:platform_api_0` — indexes 0–89 map to `gtw_platform_api_api_80` and 90–99 to `gtw_platform_api_api-canary_80`, the 90/10 split expanded one map entry per weight unit. A rule only produces these entries once it has more than one `backendRef` (`charts/haptic/charts/gateway/42-maps-weighted.yaml:16`); the per-unit expansion and emission are at `:25` and `:40`. A new `backend gtw_platform_api_api-canary_80` block also appears in the `haproxy.cfg` tab (empty of servers until an `api-canary` Service exists).

</details>

</div>

### Advanced Features

**Backend Deduplication:**

The template automatically deduplicates backends when multiple routes reference the same service+port combination, preventing duplicate HAProxy backend definitions.

**Route Key Generation:**

Internal route identifiers use the format `namespace_routename_ruleindex` to ensure uniqueness across namespaces and rules.

---

## GRPCRoute Support

### spec.parentRefs

| Field | Status | Notes |
|-------|--------|-------|
| All fields | ⚠️ Similar to HTTPRoute | Same template pattern and limitations |

### spec.hostnames

| Field | Status | Notes |
|-------|--------|-------|
| `hostnames[]` | ✅ Supported | Multiple hostnames per route |

### spec.rules[].matches

| Field | Status | Notes |
|-------|--------|-------|
| `matches[].method.type: Exact` | ✅ Supported | Exact match for gRPC service/method |
| `matches[].method.type: RegularExpression` | ✅ Supported | Regex match for gRPC service/method |
| `matches[].method.service` | ✅ Supported | gRPC service name (e.g., `com.example.User`) |
| `matches[].method.method` | ✅ Supported | gRPC method name (e.g., `GetUser`) |
| `matches[].headers[]` | ✅ Supported | Header matching (same as HTTPRoute) |

**gRPC Method Routing:**

The gateway library now supports routing based on gRPC service and method names. The gRPC path format `/package.Service/Method` is used for matching.

**Example - gRPC method routing:**

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: GRPCRoute
metadata:
  name: grpc-users
spec:
  hostnames:
    - "api.example.com"
  rules:
    # Route GetUser calls to read-only service
    - matches:
        - method:
            type: Exact
            service: com.example.UserService
            method: GetUser
      backendRefs:
        - name: user-read-svc
          port: 9090

    # Route CreateUser calls to write service
    - matches:
        - method:
            type: Exact
            service: com.example.UserService
            method: CreateUser
      backendRefs:
        - name: user-write-svc
          port: 9090

    # Route all other UserService calls with regex
    - matches:
        - method:
            type: RegularExpression
            service: com\.example\.UserService
            # Matches any method
      backendRefs:
        - name: user-general-svc
          port: 9090
```

### spec.rules[].filters

| Filter Type | Conformance | Status | Notes |
|-------------|-------------|--------|-------|
| `RequestHeaderModifier` | Core | ✅ Supported | Same implementation as HTTPRoute |
| `ResponseHeaderModifier` | Extended | ✅ Supported | Same implementation as HTTPRoute |
| `RequestRedirect` | Core | N/A | HTTPRoute only - not applicable to gRPC |
| `URLRewrite` | Extended | N/A | HTTPRoute only - not applicable to gRPC |
| `RequestMirror` | Extended | ✅ Supported | Per-route request mirroring via the bundled spoa-hub `mirror` plugin (enable `spoaHub.plugins.mirror`); supports percent/fraction sampling and multiple mirrors per rule |
| `ExtensionRef` | Implementation-specific | ❌ Not Implemented | Planned as Gateway API equivalent of Ingress annotations |

### spec.rules[].backendRefs

| Field | Status | Notes |
|-------|--------|-------|
| All `backendRefs` fields | ✅ Supported | Same implementation as HTTPRoute |
| HTTP/2 protocol | ✅ Supported | Backends generated with `proto h2` flag |

**Example - GRPCRoute:**

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: GRPCRoute
metadata:
  name: grpc-example
spec:
  hostnames:
    - "grpc.example.com"
  rules:
    - backendRefs:
        - name: grpc-svc
          port: 9090
```

---

## Debug Headers

When debug headers are enabled, the gateway library adds response headers to help troubleshoot routing decisions:

```yaml
# values.yaml
controller:
  config:
    templatingSettings:
      extraContext:
        debug: true
```

**Response Headers:**

- `X-Gateway-Matched-Route` - The namespace/name of the matched HTTPRoute or GRPCRoute
- `X-Gateway-Match-Reason` - Additional information about why the route was selected (e.g., "method match", "header match")

These headers are useful for:

- Verifying which route handled a request
- Understanding precedence when multiple routes match
- Debugging complex routing configurations

---

## Per-Gateway Kubernetes Resources

Two Gateway-API features cause the gateway library to emit additional Kubernetes resources alongside the chart's main HAProxy Service. Both flow through the controller CRD's top-level `spec.k8sResources` map (sibling of `templateSnippets`, `maps`, `files`, `sslCertificates`); the controller renderer parses the rendered YAML and the resourceapplier reconciles each emitted resource via Server-Side Apply with field manager `haptic` and a `controller=true` `OwnerReference` to the `HAProxyTemplateConfig` CR (so cascade-delete / `helm uninstall` GCs them).

| Template name | Triggered when | Emits |
|---------------|----------------|-------|
| `gateway-static-addresses` | A Gateway's `spec.addresses[]` lists at least one valid `IPAddress` entry — `SupportGatewayStaticAddresses` (Extended). | One `LoadBalancer` Service per such Gateway in the controller's namespace, named `gw-<gateway-namespace>-<gateway-name>`, carrying the requested addresses via `metallb.universe.tf/loadBalancerIPs` annotation (and `spec.loadBalancerIP` for non-MetalLB cloud LBs). The Service selects the chart's shared HAProxy pods, so the per-Gateway IP routes to the same data plane the rest of the cluster uses. |
| `gateway-infrastructure-propagation` | A Gateway sets `spec.infrastructure` (labels and / or annotations) but no `spec.addresses[]` — `SupportGatewayInfrastructurePropagation` (Extended). | One headless `ClusterIP` Service per such Gateway, also named `gw-<gateway-namespace>-<gateway-name>`. The Service has a placeholder `marker` port and an empty selector — its only purpose is to surface the propagated `spec.infrastructure` labels and annotations on a discoverable Kubernetes object. |

Both templates draw their data from the per-Gateway computation that already runs during `haproxy.cfg` rendering (the `status-patches-200-gateway` block in `70-status-gateway.yaml`). That block stashes the per-Gateway Service spec into the per-render `shared` cache (`shared.Get("gatewayStaticAddressServices")` / `gatewayInfrastructureServices`) keyed by `<namespace>/<name>`; the `k8sResources` templates read the same map back during their post-`haproxyConfig` render pass and emit one Service per entry. Multi-doc YAML (`---`-separated) is used because a single template emits zero, one, or many Services depending on cluster state.

These templates replace the previous in-template `renderResource()` calls. The semantics on the cluster are unchanged: same Service name, same selector, same ownership story. The wire-up is now declarative — anyone reading the rendered `HAProxyTemplateConfig` sees the templates explicitly under `spec.k8sResources`, and the controller's overlay-store dry-run path can validate them like any other rendered output.

## Features Summary

| Feature | Support | Notes |
|---------|---------|-------|
| HTTPRoute | Full | All matching types, filters |
| GRPCRoute | Full | HTTP/2 protocol |
| Path Matching | Exact, PathPrefix, RegularExpression | |
| Method Matching | Full | GET, POST, etc. |
| Header Matching | Exact, RegularExpression | Request headers |
| Query Param Matching | Exact, RegularExpression | URL parameters |
| RequestHeaderModifier | Full | Add, set, remove headers |
| ResponseHeaderModifier | Full | Add, set, remove headers |
| RequestRedirect | Full | HTTP redirects |
| URLRewrite | Full | Path and hostname rewrite |
| Traffic Splitting | Full | Weighted backends |
| SSL Passthrough | Full | Via annotation |

---

## Status Reporting

The Gateway API library automatically updates the `.status` of Gateway, HTTPRoute, and GRPCRoute resources to reflect their processing state. Status is applied via Server-Side Apply with field manager `haptic`.

### Gateway Status

Each Gateway receives:

- **Conditions**: `Accepted` (True after template rendering) and `Programmed` (True after successful HAProxy deployment, False if deployment fails)
- **Addresses**: LoadBalancer addresses from the controller Service, converted to Gateway API format (`IPAddress` or `Hostname`)
- **Listener status**: Per-listener conditions (`Accepted`, `Programmed`, `ResolvedRefs`, `Conflicted`), `supportedKinds` based on protocol, and `attachedRoutes` count

### HTTPRoute and GRPCRoute Status

Each route receives a `parents[]` entry for each `parentRef` that matches a Gateway managed by this controller:

- **Accepted**: True if the parentRef references a known Gateway
- **ResolvedRefs**: True if all backend Service references can be resolved; False with reason `BackendNotFound` if a referenced Service does not exist

The `controllerName` in route status is set from `gatewayClass.controllerName` in the Helm values.

### Address Discovery

Addresses are automatically discovered from the controller's LoadBalancer Service. If no address is assigned yet, Gateway addresses and Ingress status are not populated. Once an address becomes available, subsequent reconciliations update all resource statuses.

### Phase-Aware Status

Status patches use outcome-keyed variants:

| Phase | Gateway | Routes |
|-------|---------|--------|
| `deployed` | Programmed=True, addresses populated | Accepted=True, ResolvedRefs checked |
| `deployFailed` | Programmed=False, empty addresses | Same as deployed (route acceptance is deployment-independent) |

---

## Known Limitations

### Not Implemented

1. **ExtensionRef filter** - General custom-filter extension mechanism not yet implemented (planned as the Gateway API equivalent of Ingress annotations). One narrow internal use exists: an `ExtensionRef` selecting SSL passthrough is honored.

2. **Per-backend filters** (`backendRefs[].filters[]`) - Partially implemented: a `RequestHeaderModifier` on a backendRef **is** emitted per-backend (rule-scoped via `gw_rule_id`; see `test-httproute-backend-request-header-modifier`). Other filter types (ResponseHeaderModifier, RequestRedirect, URLRewrite, RequestMirror) apply at the rule level only, not per-backend.

(The `RequestMirror` filter, previously listed here, **is** implemented — per-route mirroring via the bundled spoa-hub `mirror` plugin with percent/fraction sampling and multiple mirrors per rule.)

### Partially Covered Features

- Cross-namespace **backend** references are implemented (`backendRef.namespace` honored, gated by `ReferenceGrant`) and exercised by the upstream Gateway API conformance suite — they aren't pinned by this library's own validationTests.
- Cross-namespace **parent** Gateway references — not pinned by a validationTest.
- Wildcard hostname patterns — regex host-map support exists; not pinned by a validationTest.
- Listener-specific route attachment — `sectionName` drives `attachedRoutes` status counting, but per-listener routing isolation is not implemented.

---

## Testing Coverage

The gateway library includes comprehensive validation tests:

**Well-tested:**

- HTTPRoute path matching (Exact, PathPrefix, RegularExpression)
- HTTPRoute method matching (GET, POST, etc.)
- HTTPRoute header matching (Exact and RegularExpression types)
- HTTPRoute query parameter matching (Exact and RegularExpression types)
- HTTPRoute weighted backends (various weight combinations, defaults)
- HTTPRoute default behaviors (no matches → PathPrefix /)
- HTTPRoute match precedence and tie-breaking rules
- HTTPRoute filters (RequestHeaderModifier, ResponseHeaderModifier, RequestRedirect, URLRewrite, RequestMirror — single/percent/fraction/multiple)
- Backend deduplication (multiple routes to same service+port)
- GRPCRoute backend generation with HTTP/2
- GRPCRoute method-based routing (service and method matching)
- GRPCRoute filters (RequestHeaderModifier, ResponseHeaderModifier)
- Complex route conflict resolution with VAR qualifiers

**Untested:**

- Cross-namespace references
- Wildcard hostnames
- ExtensionRef filter (general mechanism not yet implemented)

---

## Future Development

Priority areas for future enhancement:

1. **ExtensionRef support** - Implement custom filter extension mechanism as the Gateway API equivalent of Ingress annotations. This will enable custom functionality beyond standard filters.

2. **Per-backend filters** - Extend `backendRefs[].filters[]` beyond the already-supported `RequestHeaderModifier` to the remaining filter types, for different filter behavior per backend within the same rule.

3. **Cross-namespace validationTests** - Add library validationTests pinning cross-namespace **parent** Gateway references (cross-namespace backend refs via `ReferenceGrant` are already implemented and conformance-covered).

4. **Wildcard hostname tests** - Pin the existing regex host-map wildcard support (e.g., `*.example.com`) with validationTests.

---

## See Also

- [Gateway API Documentation](https://gateway-api.sigs.k8s.io/)
- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Extension points and routing infrastructure
- [SSL Library](ssl.md) - TLS certificate management
- [haproxytech library](haproxytech.md) - Annotation-based configuration
