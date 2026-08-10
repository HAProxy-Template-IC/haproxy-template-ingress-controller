# Gateway API library

The Gateway API library compiles HTTPRoute, GRPCRoute, TLSRoute, and TCPRoute resources into HAProxy routing configuration.

## Overview

The Gateway API library implements the [Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/) specification, providing:

- HTTPRoute, GRPCRoute, TLSRoute, and TCPRoute support
- Advanced request matching (method, headers, query parameters)
- Traffic splitting with weighted backends
- Request/response header modification
- URL rewrites and redirects
- TLS termination and SSL passthrough

This library is **enabled by default**. For a runnable end-to-end walkthrough (a Gateway with an HTTP listener, an HTTPRoute, and a backend Service), see [Expose a Service through a Gateway](../gateway-class.md#expose-a-service-through-a-gateway).

!!! note "Gateway API CRDs are resolved at runtime"
    The library is merged whenever it's enabled — there's no Helm capability gate. Gateway API availability is a runtime question: kinds whose CRD the cluster doesn't serve are dropped from the effective config, and every snippet and `validationTests` entry that `requires` them is stripped with them. Install the CRDs later and the controller picks them up without a redeploy.

Watch an HTTPRoute compile down to HAProxy config live:

<div class="pg-embed" markdown data-scenario="gateway" data-facade="spec.templateSnippets.map-host-500-gateway" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Gateway API → HAProxy config" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, add `- www.example.com` under the `api` HTTPRoute's `spec.hostnames`, then open the **maps** tab and watch `host.map` gain a second entry.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

`host.map`'s provenance comment goes from `# HTTPRoute: platform/api (1 hosts)` to `(2 hosts)`, and a second `www.example.com…` line appears next to the `api.example.com…` one — the gateway library writes one entry per effective hostname. It derives them from `spec.hostnames` in `map-host-500-gateway`, emitting one `<hostKey> <hostKey>` line per host. Each key carries a `:<port>` suffix because the demo Gateway's HTTP listener is a catch-all with no hostname, which scopes its routes to that Gateway's own bind port.

</details>

</div>

## Configuration

```yaml
controller:
  templateLibraries:
    gateway:
      enabled: true  # Enabled by default
```

## Extension points

The Gateway API library hooks into these extension points from base.yaml. Snippet names encode their priority via a numeric prefix (see [Template Libraries → Snippet Priority](../template-libraries.md#snippet-priority)).

| Extension Point | Snippet | What It Generates |
|-----------------|---------|-------------------|
| `features-*` | `features-100-gateway-ssl-passthrough` | Populates `gf["sslPassthroughBackends"]` from HTTPRoutes annotated for SNI passthrough |
| `features-*` | `features-100-gateway-tls` | Registers TLS certificates from Gateway listeners into `gf["tlsCertificates"]` |
| `backends-*` | `backends-500-gateway` | HTTP backend blocks for every unique `(namespace, service, port)` touched by an HTTPRoute or GRPCRoute |
| `backends-*` | `backends-501-gateway-ssl-passthrough` | TCP-mode backends for SSL-passthrough HTTPRoutes and for TLSRoute rules (`gtw_tls_*`) |
| `backends-*` | `backends-502-gateway-tcproute` | TCP-mode backends for TCPRoute rules (`gtw_tcp_*`), with weighted server pools when a rule has several `backendRefs` |
| `map-host-*` | `map-host-500-gateway` | Host → group mapping entries derived from `spec.hostnames` |
| `map-path-exact-*` | `map-path-exact-500-gateway` | Entries for `path.type: Exact` matches |
| `map-pfxexact-*` | `map-pfxexact-500-gateway` | Prefix-exact entries (for example matching `/foo` but not `/foobar`) |
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
| `frontends-*` | `frontends-600-gateway-tls-listener` | One `mode tcp` frontend per Gateway TLS listener port — SNI dispatch for TLSRoutes, with an `ssl crt-list` bind for `Terminate` listeners |
| `frontends-*` | `frontends-700-gateway-tcp-listener` | One `mode tcp` frontend per TCPRoute-claimed TCP listener port |
| `status-patches-*` | `status-patches-200-gateway` | Patches Gateway / HTTPRoute / GRPCRoute `status` (Accepted, ResolvedRefs, `attachedRoutes`, addresses) |
| `status-patches-*` | `status-patches-205-gateway-tlsroute` | Patches TLSRoute `status` (Accepted, ResolvedRefs) |
| `status-patches-*` | `status-patches-210-gateway-tcproute` | Patches TCPRoute `status` (Accepted, ResolvedRefs) |

### Injecting custom configuration

You can extend Gateway API features by adding snippets with the right prefix and priority:

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
| Gateways | v1, v1beta1 | Gateway definitions (filtered by `gatewayClass.name` — see [GatewayClass](../gateway-class.md)) |
| GatewayClasses | v1, v1beta1 | GatewayClass definitions (field-selector scoped to owned class) |
| HTTPRoutes | v1, v1beta1 | HTTP routing rules |
| GRPCRoutes | v1, v1alpha2 | gRPC routing rules |
| TLSRoutes | v1, v1alpha3, v1alpha2 | TLS passthrough routing rules |
| TCPRoutes | v1, v1alpha2 | Raw-TCP listener forwarding |
| ReferenceGrants | v1, v1beta1 | Cross-namespace reference policy |
| ListenerSets | v1 | Additional listeners attached to Gateways (Gateway Enhancement Proposal (GEP) 1713) |
| BackendTLSPolicies | v1, v1alpha3 | Backend TLS validation (v1alpha2 is excluded: incompatible shape) |
| Namespaces | v1 (required) | Namespace metadata for listener attachment evaluation |
| Services | v1 (required) | Service discovery |
| EndpointSlices | `discovery.k8s.io/v1` (required) | Backend endpoints |

Features whose *fields* don't exist in an older release's schemas (for
example the HTTPRoute Cross-Origin Resource Sharing (CORS) filter before Gateway API v1.6, or Gateway
frontend mTLS) stay inactive on that release; everything else works. The
per-release expectations are pinned by `tests/schemas-ga-*` and
`scripts/test-templates.sh`.

TLS Secrets are watched by the SSL library (not gateway), and controller-service address discovery for status patches is owned by base.yaml. See [SSL Library](ssl.md) and [Base Library](base.md).

## Supported Gateway API versions and channels

HAPTIC targets the Gateway API `v1` API group. Each kind is an optional watched resource with an ordered `apiVersions` candidate list (see [Watched Resources](#watched-resources)); the controller resolves each kind to the first version the cluster serves and activates its features at runtime, so installing or upgrading the Gateway API needs no chart redeploy. The per-release behavior is pinned by tests against Gateway API v1.1, v1.4, and v1.5, plus a no-CRD baseline.

Which kinds a Gateway API install provides depends on its **channel**. The standard channel (`standard-install.yaml`) covers most kinds; a few graduated from the experimental channel (`experimental-install.yaml`) only in recent releases:

| Kind | Channel |
|------|---------|
| Gateway, GatewayClass | Standard |
| HTTPRoute | Standard |
| GRPCRoute | Standard (since Gateway API v1.1) |
| ReferenceGrant | Standard |
| BackendTLSPolicy | Standard |
| TLSRoute | Standard since Gateway API v1.5; experimental channel before |
| TCPRoute | Standard since Gateway API v1.6; experimental channel before |
| ListenerSet | Experimental channel (GEP-1713) |

Installing the v1.6.0 standard channel gives you every route kind, including TLSRoute and TCPRoute. On Gateway API v1.5, install the experimental channel for TCPRoute; before v1.5, install it for both TLSRoute and TCPRoute.

The word "experimental" describes two independent things, which don't gate each other:

- **Channel** — which route *kinds* (CRDs) a Gateway API install ships, shown in the table above.
- **The `controller.templateLibraries.gateway.experimentalChannel` value** — a separate switch that tells HAPTIC's `validationTests` the experimental **HTTPRoute schema** is installed, so tests exercising experimental HTTPRoute *fields* (`retry` per GEP-1731, `sessionPersistence` per GEP-1619) run. HAPTIC emits those directives whenever the fields are present, regardless of the flag; see the [Chart Values Reference](../reference.md). This value gates no route kind.

## Architecture

The `gateway/` library:

- Declares the Gateway API resource set as watched resources — `httproutes`, `grpcroutes`, `tlsroutes`, `tcproutes`, plus `gateways`, `gatewayclasses`, `referencegrants`, and the other supporting kinds (see the table above)
- Implements backend generation for Gateway routes
- Adds routing rules to HAProxy map files
- Plugs into extension points defined in `base.yaml`

This architecture allows the controller to remain resource-agnostic while the chart provides specific resource support.

---

**Status legend:** ✅ Supported · ⚠️ Partial or untested · ❌ Not implemented

## HTTPRoute support

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
| Wildcard hostnames (for example `*.example.com`) | ⚠️ Untested | Regex host-map support exists; not pinned by a `validationTest` |
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

### `spec.rules[].matches` - path matching

| Field | Status | Notes |
|-------|--------|-------|
| `matches[].path.type: Exact` | ✅ Supported | Exact path match using HAProxy map |
| `matches[].path.type: PathPrefix` | ✅ Supported | Prefix match using HAProxy `map_beg` |
| `matches[].path.type: RegularExpression` | ✅ Supported | Regex match using HAProxy `map_reg` |
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

<div class="pg-embed" markdown data-scenario="gateway" data-facade="spec.templateSnippets.map-path-exact-500-gateway" data-tab="maps" data-controls="tabs,resources" data-title="Path type → map file" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, change the `api` HTTPRoute's `path.type` from `PathPrefix` to `Exact`, then watch the entry move between map files in the **maps** tab.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

With `PathPrefix`, the route's entry (`api.example.com…/ GW_ROUTE_ID:http:platform_api_0`) sits in `path-prefix.map`. Switch to `Exact` and the same entry moves to `path-exact.map`, leaving `path-prefix.map` empty. Each path type is filled by its own snippet — `map-path-exact-500-gateway` and `map-path-prefix-500-gateway` — and a route's entry only lands in the map whose `pathType` matches.

</details>

</div>

### `spec.rules[].matches` - method, header, and query matching

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

<div class="pg-embed" markdown data-scenario="gateway" data-facade="spec.templateSnippets.frontend-matchers-advanced-500-gateway" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Method / header / query matchers" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, add `method: GET` to the `api` HTTPRoute's match (as a sibling of its `path`), then find the matcher line in the `haproxy.cfg` tab.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

Under `# Advanced route matching`, the rule's provenance comment changes from `- path-only` to `- method GET`, and its `http-request set-var(txn.gw_rule_id) …` guard gains a `{ method GET }` condition. `frontend-matchers-advanced-500-gateway` emits that condition and the comment. Header and query matchers build the same guard: a `headers:` entry adds `{ req.hdr(<name>) "<value>" }` and a `queryParams:` entry adds `{ urlp(<name>) "<value>" }`.

</details>

</div>

### `spec.rules[].filters`

| Filter Type | Conformance | Status | Notes |
|-------------|-------------|--------|-------|
| `RequestHeaderModifier` | Core | ✅ Supported | Add/Set/Remove request headers |
| `ResponseHeaderModifier` | Extended | ✅ Supported | Add/Set/Remove response headers |
| `RequestRedirect` | Core | ✅ Supported | HTTP redirects with scheme/hostname/port/path/statusCode |
| `URLRewrite` | Extended | ✅ Supported | Path and hostname rewriting |
| `RequestMirror` | Extended | ✅ Supported | Per-route request mirroring via the bundled spoa-hub `mirror` plugin (enable `spoaHub.plugins.mirror`); supports percent/fraction sampling and multiple mirrors per rule |
| `CORS` | Extended (GEP-1767) | ✅ Supported | HTTPRoute only, via `frontend-filters-450-gateway-cors`. Honours `allowOrigins` (exact values, a bare `*`, and `*.`-prefixed wildcards compiled to a regex against the request `Origin`), `allowMethods`, `allowHeaders`, `exposeHeaders`, `allowCredentials`, and `maxAge` |
| `ExtensionRef` | Implementation-specific | ⚠️ Partial | Only `kind: SSLPassthrough` is honored (flags the HTTPRoute for TLS passthrough); other kinds planned as the Gateway API equivalent of Ingress annotations |

#### `RequestHeaderModifier` filter

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

#### `ResponseHeaderModifier` filter

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

#### `RequestRedirect` filter

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

#### `URLRewrite` filter

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

Attach a filter to the demo route and watch the real directive compile — no `<route-conditions>` placeholder:

<div class="pg-embed" markdown data-scenario="gateway" data-facade="spec.templateSnippets.frontend-filters-500-gateway-request-header" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Filter → http-request directive" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, give the `api` HTTPRoute's rule a `filters:` list (a sibling of `backendRefs`) with a `RequestHeaderModifier` that sets a header — `set: [{name: X-API-Version, value: "v2"}]` — then find the generated `http-request set-header X-API-Version` line in the `haproxy.cfg` tab.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

A `http-request set-header X-API-Version "v2"` directive appears under the filters, guarded by an `if` condition that matches the route's `gw_rule_id` — so it fires only for requests the `api` HTTPRoute selected, not for every request on the frontend. The `frontend-filters-500-gateway-request-header` snippet emits it; `add`/`remove` operations become `http-request add-header` / `http-request del-header` lines the same way. This is the real condition the engine writes, in place of the `<route-conditions>` placeholder shown in the static examples above.

</details>

</div>

### `spec.rules[].backendRefs`

| Field | Status | Notes |
|-------|--------|-------|
| `backendRefs[].name` | ✅ Supported | Service name |
| `backendRefs[].namespace` | ⚠️ Partial | Not explicitly handled, likely defaults to route namespace |
| `backendRefs[].port` | ✅ Supported | Service port number |
| `backendRefs[].weight` | ✅ Supported | Traffic splitting with weighted distribution |
| `backendRefs[].filters[]` | ⚠️ Partial | `RequestHeaderModifier` emitted per-backend (rule-scoped via `gw_rule_id`); other filter types not handled at the `backendRef` level |
| Multiple backends | ✅ Supported | Weighted traffic splitting using MULTIBACKEND qualifier |
| Single backend | ✅ Supported | Optimized with BACKEND qualifier (avoids weighted logic) |
| Omitted weight | ✅ Supported | Defaults to weight 1 |
| Explicit `weight: 0` | ✅ Supported | Valid ref that receives no traffic: it contributes zero weighted-map entries, but its backend block is still rendered (per Gateway API) |

**Weighted Backend Implementation:**

The gateway library uses HAProxy's `rand()` function and map-based selection for O(1) weighted routing:

- Weights are pre-expanded into map entries (for example 70/30 split = 100 map entries)
- Entry 0-69 map to backend 1, entries 70-99 map to backend 2
- HAProxy generates random number % `total_weight` and looks up backend in map

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

<div class="pg-embed" markdown data-scenario="gateway" data-facade="spec.templateSnippets.map-weighted-backend-500-gateway" data-tab="maps" data-controls="tabs,resources" data-title="Weighted traffic split → map" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, give the `api` HTTPRoute a second backend: add `weight: 90` to its existing `api` ref and append `- {name: api-canary, port: 80, weight: 10}`, then open `weighted-multi-backend.map` in the **maps** tab.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

`weighted-multi-backend.map` fills with 100 entries keyed `<0-99>:platform_api_0` — indexes 0–89 map to `gtw_platform_api_api_80` and 90–99 to `gtw_platform_api_api-canary_80`, the 90/10 split expanded one map entry per weight unit. A rule only produces these entries once it has more than one `backendRef`, in `map-weighted-backend-500-gateway`, which expands and emits one map entry per weight unit. A new `backend gtw_platform_api_api-canary_80` block also appears in the `haproxy.cfg` tab (empty of servers until an `api-canary` Service exists).

</details>

</div>

### Advanced features

**Backend Deduplication:**

When multiple routes reference the same service and port, the template emits a single shared HAProxy backend.

**Route Key Generation:**

Internal route identifiers use the format `namespace_routename_ruleindex` to ensure uniqueness across namespaces and rules.

### Misdirected requests on HTTPS listeners

When a Gateway has multiple HTTPS listeners with distinct hostnames, HAPTIC enforces RFC 9110 listener isolation. If a request's TLS SNI selects one HTTPS listener but its `Host` header canonically belongs to a *different* HTTPS listener on the Gateway, HAPTIC returns `421 Misdirected Request` (Gateway API conformance test `HTTPRouteHTTPSListenerDetectMisdirectedRequests`).

The check applies only to HTTPS connections that carry an SNI. Plain-HTTP requests are unaffected.

---

## GRPCRoute support

### spec.parentRefs

| Field | Status | Notes |
|-------|--------|-------|
| All fields | ⚠️ Similar to HTTPRoute | Same template pattern and limitations |

### spec.hostnames

| Field | Status | Notes |
|-------|--------|-------|
| `hostnames[]` | ✅ Supported | Multiple hostnames per route |

### `spec.rules[].matches`

| Field | Status | Notes |
|-------|--------|-------|
| `matches[].method.type: Exact` | ✅ Supported | Exact match for gRPC service/method |
| `matches[].method.type: RegularExpression` | ✅ Supported | Regex match for gRPC service/method |
| `matches[].method.service` | ✅ Supported | gRPC service name (for example `com.example.User`) |
| `matches[].method.method` | ✅ Supported | gRPC method name (for example `GetUser`) |
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

### `spec.rules[].filters`

| Filter Type | Conformance | Status | Notes |
|-------------|-------------|--------|-------|
| `RequestHeaderModifier` | Core | ✅ Supported | Same implementation as HTTPRoute |
| `ResponseHeaderModifier` | Extended | ✅ Supported | Same implementation as HTTPRoute |
| `RequestRedirect` | Core | N/A | HTTPRoute only - not applicable to gRPC |
| `URLRewrite` | Extended | N/A | HTTPRoute only - not applicable to gRPC |
| `RequestMirror` | Extended | ✅ Supported | Per-route request mirroring via the bundled spoa-hub `mirror` plugin (enable `spoaHub.plugins.mirror`); supports percent/fraction sampling and multiple mirrors per rule |
| `ExtensionRef` | Implementation-specific | ❌ Not Implemented | Planned as Gateway API equivalent of Ingress annotations |

### `spec.rules[].backendRefs`

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

## TLSRoute support

TLSRoute routes TLS connections by SNI. Depending on the listener's TLS mode, HAProxy either forwards the still-encrypted stream to the backend (`tls.mode: Passthrough`) or terminates TLS and forwards the decrypted stream (`tls.mode: Terminate`).

### Example: passthrough Gateway and TLSRoute

This Gateway opens a `Passthrough` TLS listener on port 6443 and forwards `secure.example.com` — matched by SNI — to a backend that terminates TLS itself:

```bash
kubectl apply -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: tls-edge
  namespace: default
spec:
  gatewayClassName: haptic
  listeners:
    - name: tls
      protocol: TLS
      port: 6443
      tls:
        mode: Passthrough
      allowedRoutes:
        namespaces:
          from: Same
        kinds:
          - kind: TLSRoute
---
apiVersion: gateway.networking.k8s.io/v1
kind: TLSRoute
metadata:
  name: secure-app
  namespace: default
spec:
  parentRefs:
    - name: tls-edge
  hostnames:
    - secure.example.com
  rules:
    - backendRefs:
        - name: secure-app
          port: 8443
EOF
```

HAPTIC renders a dedicated `frontend gateway-tls-port-6443` in `mode tcp` that reads the SNI from the buffered ClientHello and dispatches `secure.example.com` to backend `gtw_tls_default_secure-app_0`, forwarding the still-encrypted bytes to `secure-app:8443`. Choose a listener port other than the chart-static HTTPS port (`haproxy.ports.https`, default 443): a TLS listener on that port is dropped to avoid a duplicate bind (see [Forwarding behavior](#forwarding-behavior)). Each rule needs at least one `spec.hostnames` entry, and traffic goes to the rule's first `backendRef` — `weight` isn't honored for TLSRoute.

### Attachment semantics

A TLSRoute attaches to a Gateway listener when every check in this table passes:

| Check | Behavior |
|-------|----------|
| `parentRefs[]` kind | Must reference a `Gateway` in group `gateway.networking.k8s.io` (both default when omitted) |
| `parentRefs[].sectionName` | When set, only the named listener is considered |
| `parentRefs[].port` | When set, only listeners on that port are considered |
| Listener protocol | Must be `TLS`; both `tls.mode: Passthrough` and `tls.mode: Terminate` accept TLSRoutes (an empty mode defaults to `Terminate`) |
| Mixed modes on one port | A port hosting both a `Passthrough` and a `Terminate` listener is a protocol conflict — no route attaches to either listener |
| `allowedRoutes.kinds` | Honored; when omitted, the protocol default (TLSRoute on TLS listeners) applies |
| `allowedRoutes.namespaces.from` | `Same`, `All`, and `Selector` (with `matchLabels`; `matchExpressions` isn't supported) |
| `spec.hostnames` | The route needs at least one hostname — routing is SNI-based, so a TLSRoute without hostnames attaches nowhere. Each route hostname is intersected with the listener hostname (wildcards supported); the intersection becomes the SNI the frontend matches |

### Forwarding behavior

- **Passthrough on the chart-static HTTPS port**: the route's SNIs join the shared `ssl-tcp` frontend alongside Ingress SSL-passthrough entries, dispatched with `use_backend ... if { req_ssl_sni -m str <host> }`.
- **TLS listeners on other ports**: each port gets a dedicated `mode tcp` frontend (`frontend gateway-tls-port-<port>`). `Terminate` listeners bind with `ssl crt-list` and dispatch on `ssl_fc_sni` (the SNI HAProxy consumed during the handshake); `Passthrough` listeners bind plain and dispatch on `req_ssl_sni` read from the buffered ClientHello. Wildcard SNIs (`*.example.com`) match by suffix.
- **Reject by default**: an SNI no attached TLSRoute claims is rejected at the TCP level. A rule whose `backendRefs` don't all resolve still claims its SNIs, so connections to them are refused rather than silently passed through — the behavior the upstream `TLSRouteInvalidBackendRef*` conformance tests mandate.
- **Backends**: one `mode tcp` backend per route rule, named `gtw_tls_<namespace>_<route>_<ruleIndex>`; all SNIs of a rule share it. Traffic goes to the rule's **first** `backendRef` (default port 443).
- A Gateway TLS listener on the chart-static HTTPS port is dropped when the chart already binds that port (chart-static HTTPS frontend or Ingress SSL passthrough active). Move the listener to another port or override `httpsPort`.

### TLSRoute status

Each `parentRef` targeting a Gateway owned by this controller receives two conditions:

- **Accepted**: `True`, or `False` with reason `NoMatchingParent` (`sectionName` or `port` matched no listener), `NoMatchingListenerHostname`, or `NotAllowedByListeners`
- **ResolvedRefs**: `True`, or `False` with reason `InvalidKind` (a `backendRef` isn't a core/v1 Service), `RefNotPermitted` (cross-namespace ref without a ReferenceGrant), or `BackendNotFound`

TLSRoutes count toward `attachedRoutes` on TLS listeners only; listeners on a mixed-mode port count zero. Status is written on the `deployed` outcome — `Accepted` turns `True` once HAProxy serves the route, not at render time.

### TLSRoute limitations

- **Single backend per rule**: traffic goes to the first `backendRef`; `weight` isn't honored for TLSRoute (TCPRoute rules do support weighted refs).
- `backendRefs` must be core/v1 Services.
- At least one `spec.hostnames` entry is required.

---

## TCPRoute support

TCPRoute forwards raw TCP: each claimed listener port becomes a dedicated `mode tcp` frontend whose `default_backend` is the route's backend. There is no per-connection matching — TCP carries no hostname or SNI, so a port forwards to exactly one backend.

!!! note "TCPRoute needs Gateway API v1.6 standard channel (or the experimental channel)"
    TCPRoute is in the Gateway API standard channel (`standard-install.yaml`) since v1.6. On v1.5 and earlier, install it from the experimental channel (`experimental-install.yaml`). HAPTIC activates TCPRoute support automatically once the CRD is served — no chart redeploy. See [Supported Gateway API versions and channels](#supported-gateway-api-versions-and-channels).

### Example: TCP Gateway and TCPRoute

This Gateway opens a `TCP` listener on port 5432 and forwards every connection to a PostgreSQL Service:

```bash
kubectl apply -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: tcp-edge
  namespace: default
spec:
  gatewayClassName: haptic
  listeners:
    - name: postgres
      protocol: TCP
      port: 5432
      allowedRoutes:
        namespaces:
          from: Same
        kinds:
          - kind: TCPRoute
---
apiVersion: gateway.networking.k8s.io/v1
kind: TCPRoute
metadata:
  name: postgres
  namespace: default
spec:
  parentRefs:
    - name: tcp-edge
  rules:
    - backendRefs:
        - name: postgres
          port: 5432
EOF
```

HAPTIC renders `frontend gateway-tcp-port-5432` (`mode tcp`, `bind *:5432`, `default_backend gtw_tcp_default_postgres_0`) with no ACLs — the whole port maps to this one route rule. A TCPRoute has no hostnames. Choose a listener port other than the chart-static `haproxy.ports.http` / `haproxy.ports.https` (default 80 / 443): a TCP listener on either is dropped to avoid a duplicate bind.

### Attachment semantics

| Check | Behavior |
|-------|----------|
| `parentRefs[]` kind | Must reference a `Gateway` in group `gateway.networking.k8s.io` (both default when omitted) |
| `parentRefs[].sectionName` / `parentRefs[].port` | When set, restrict which listeners are considered |
| Listener protocol | Must be `TCP` |
| `allowedRoutes.kinds` | Honored; when omitted, the protocol default (TCPRoute on TCP listeners) applies |
| `allowedRoutes.namespaces.from` | `Same`, `All`, and `Selector` (with `matchLabels`; `matchExpressions` isn't supported) |
| Port ownership | Each listener port belongs to exactly one route rule. When several TCPRoutes claim the same port, the **oldest** route wins (`creationTimestamp`, then `namespace/name` as tie-breaker) |

### Forwarding behavior

- **One frontend per claimed port**: `frontend gateway-tcp-port-<port>` with `mode tcp`, `bind *:<port>`, and a `default_backend` — no ACLs.
- **Backends**: `mode tcp` blocks named `gtw_tcp_<namespace>_<route>_<ruleIndex>`. A single `backendRef` gets the standard reserved-slot server pool; multiple `backendRefs` get `balance roundrobin` with each Service in its own slot range carrying its `weight` (default 1; a `weight: 0` ref stays in the config but takes no traffic).
- A route without `sectionName` attaches to every TCP listener on the Gateway: each port gets its own frontend, all sharing one backend.
- TCP listeners whose port equals the chart-static `httpPort` or `httpsPort` are dropped to avoid a duplicate bind.

### TCPRoute status

Each `parentRef` targeting a Gateway owned by this controller receives two conditions:

- **Accepted**: `True`, or `False` with reason `NoMatchingParent` (`sectionName` or `port` matched no listener) or `NotAllowedByListeners`
- **ResolvedRefs**: `True`, or `False` with reason `InvalidKind`, `RefNotPermitted`, or `BackendNotFound` (same semantics as TLSRoute)

TCPRoutes count toward `attachedRoutes` on TCP listeners only. Status is written on the `deployed` outcome, as for TLSRoute.

### TCPRoute limitations

- **One backend per port**: TCP can't be multiplexed by hostname or path; a port maps to a single route rule, and competing claims resolve oldest-first.
- `backendRefs` must be core/v1 Services.
- Listener ports colliding with the chart-static `httpPort` / `httpsPort` are dropped.

---

## Cross-namespace routes (ReferenceGrant)

Cross-namespace routing has two independent gates, and both apply to every route kind (HTTPRoute, GRPCRoute, TLSRoute, TCPRoute):

- **Listener attachment** — a Gateway listener's `allowedRoutes.namespaces.from` decides which namespaces' routes may attach. HAPTIC honors `Same` (the default — routes in the Gateway's own namespace), `All` (routes in any namespace), and `Selector` (routes in namespaces matching `matchLabels`; `matchExpressions` isn't supported). Attaching a route to a Gateway in another namespace needs no ReferenceGrant — only a permissive `allowedRoutes`.
- **Backend references** — a rule's `backendRef.namespace` pointing at a Service in another namespace is permitted only by a ReferenceGrant in the **target** (Service) namespace whose `from` clause names the route's group, kind, and namespace and whose `to` clause names the Service group and kind. Without a matching grant, the route's `ResolvedRefs` condition turns `False` with reason `RefNotPermitted` and the backend isn't served.

The example below runs a Gateway in the `infra` namespace that accepts routes from any namespace, an HTTPRoute in `store-a` whose backend Service lives in `store-b`, and the ReferenceGrant in `store-b` that permits it:

```bash
kubectl apply -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: edge
  namespace: infra
spec:
  gatewayClassName: haptic
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: All
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: shop
  namespace: store-a
spec:
  parentRefs:
    - name: edge
      namespace: infra
  hostnames:
    - shop.example.com
  rules:
    - backendRefs:
        - name: shop
          namespace: store-b
          port: 80
---
apiVersion: gateway.networking.k8s.io/v1beta1
kind: ReferenceGrant
metadata:
  name: allow-store-a-httproutes
  namespace: store-b
spec:
  from:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      namespace: store-a
  to:
    - group: ""
      kind: Service
EOF
```

The `to` clause omits `name`, so it permits references to any Service in `store-b`; add `name: shop` to scope the grant to one Service. To permit a different route kind, set `from[].kind` to `GRPCRoute`, `TLSRoute`, or `TCPRoute`. Cross-namespace Gateway certificate references (a listener's `tls.certificateRefs` pointing at a Secret in another namespace) follow the same rule with a `to` clause of `group: "", kind: Secret`.

---

## Debug headers

When debug headers are enabled, the gateway library adds response headers to help troubleshoot routing decisions:

```yaml
# values.yaml
controller:
  config:
    templatingSettings:
      extraContext:
        diagnostics:
          routingHeaders:
            enabled: true
```

**Response Headers:**

- `X-Gateway-Matched-Route` - The namespace/name of the matched HTTPRoute or GRPCRoute
- `X-Gateway-Match-Reason` - Additional information about why the route was selected (for example `method match`, `header match`)

These headers are useful for:

- Verifying which route handled a request
- Understanding precedence when multiple routes match
- Debugging complex routing configurations

---

## Per-Gateway Kubernetes Resources

Two Gateway-API features cause the gateway library to emit additional Kubernetes resources alongside the chart's main HAProxy Service. Both flow through the controller CRD's top-level `spec.k8sResources` map (sibling of `templateSnippets`, `maps`, `files`, `sslCertificates`); the controller renderer parses the rendered YAML and the resourceapplier reconciles each emitted resource via Server-Side Apply with field manager `haptic` and a `controller=true` `OwnerReference` to the `HAProxyTemplateConfig` CR (so cascade-delete / `helm uninstall` GCs them).

| Template name | Triggered when | Emits |
|---------------|----------------|-------|
| `gateway-static-addresses` | A Gateway's `spec.addresses[]` lists at least one valid `IPAddress` entry — `SupportGatewayStaticAddresses` (Extended). | One `LoadBalancer` Service **per requested IP** in the controller's namespace, named `gw-<gateway-namespace>-<gateway-name>-<ip-with-dashes>` (names over 63 characters are truncated with a hash suffix). Each Service carries its single IP via the `metallb.universe.tf/loadBalancerIPs` annotation and selects the chart's shared HAProxy pods, so the per-Gateway IP routes to the same data plane the rest of the cluster uses. |
| `gateway-infrastructure-propagation` | A Gateway sets `spec.infrastructure` (labels and / or annotations) but no `spec.addresses[]` — `SupportGatewayInfrastructurePropagation` (Extended). | One headless `ClusterIP` Service per such Gateway, also named `gw-<gateway-namespace>-<gateway-name>`. The Service has a placeholder `marker` port and an empty selector — its only purpose is to surface the propagated `spec.infrastructure` labels and annotations on a discoverable Kubernetes object. |

Both templates draw their data from the per-Gateway computation that already runs during `haproxy.cfg` rendering (the `status-patches-200-gateway` block in `70-status-gateway.yaml`). That block stashes the per-Gateway Service spec into the per-render `shared` cache (`shared.Get("gatewayStaticAddressServices")` / `gatewayInfrastructureServices`) keyed by `<namespace>/<name>`; the `k8sResources` templates read the same map back during their post-`haproxyConfig` render pass and emit one Service per entry. Multi-doc YAML (`---`-separated) is used because a single template emits zero, one, or many Services depending on cluster state.

These templates replace the previous in-template `renderResource()` calls. The semantics on the cluster are unchanged: same Service name, same selector, same ownership story. The wire-up is now declarative — anyone reading the rendered `HAProxyTemplateConfig` sees the templates explicitly under `spec.k8sResources`, and the controller's overlay-store dry-run path can validate them like any other rendered output.

### Request a static IP for a Gateway

Set `spec.addresses` on a Gateway to pin it to a fixed IP:

```bash
kubectl apply -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: edge
  namespace: platform
spec:
  gatewayClassName: haptic
  addresses:
    - type: IPAddress
      value: 203.0.113.5
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: Same
EOF
```

HAPTIC emits a `LoadBalancer` Service named `gw-platform-edge-203-0-113-5` in the controller's namespace, annotated `metallb.universe.tf/loadBalancerIPs: 203.0.113.5` and selecting the shared HAProxy pods. Find it by its Gateway label:

```bash
kubectl get svc -n haptic -l gateway.networking.k8s.io/gateway-name=edge
```

Once MetalLB (or your cloud load balancer) allocates the IP, it appears in the Gateway's `status.addresses`. Listing several `spec.addresses[]` entries emits one Service per IP; an IP that can't be allocated is left out of `status.addresses` while the usable ones still bind.

## Features summary

| Feature | Support | Notes |
|---------|---------|-------|
| HTTPRoute | Full | All matching types, filters |
| GRPCRoute | Full | HTTP/2 protocol |
| TLSRoute | Full | SNI routing on TLS listeners, `Passthrough` and `Terminate`; first `backendRef` takes traffic |
| TCPRoute | Full | One frontend per claimed TCP listener port; weighted `backendRefs` |
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

## Status reporting

The Gateway API library automatically updates the `.status` of GatewayClass, Gateway, ListenerSet, HTTPRoute, GRPCRoute, TLSRoute, TCPRoute, and BackendTLSPolicy resources to reflect their processing state. Status is applied via Server-Side Apply with field manager `haptic`. TLSRoute and TCPRoute conditions are listed in their sections above; this section covers the rest.

### Gateway status

Each Gateway receives:

- **Conditions**: `Accepted` (True after template rendering) and `Programmed` (True after successful HAProxy deployment, False if deployment fails)
- **Addresses**: LoadBalancer addresses from the controller Service, converted to Gateway API format (`IPAddress` or `Hostname`)
- **Listener status**: Per-listener conditions (`Accepted`, `Programmed`, `ResolvedRefs`, `Conflicted`), `supportedKinds` based on protocol, and `attachedRoutes` count

### HTTPRoute and GRPCRoute status

Each route receives a `parents[]` entry for each `parentRef` that matches a Gateway managed by this controller:

- **Accepted**: True if the parentRef references a known Gateway
- **ResolvedRefs**: True if all backend Service references can be resolved; False with reason `BackendNotFound` if a referenced Service doesn't exist

The `controllerName` in route status is set from `gatewayClass.controllerName` in the Helm values — see [GatewayClass](../gateway-class.md) for the class configuration and ownership rules.

### Address discovery

Addresses are automatically discovered from the controller's LoadBalancer Service. If no address is assigned yet, Gateway addresses and Ingress status aren't populated. Once an address becomes available, subsequent reconciliations update all resource statuses.

### Phase-aware status

Status patches use outcome-keyed variants:

| Phase | Gateway | Routes |
|-------|---------|--------|
| `deployed` | Programmed=True, addresses populated | Accepted=True, ResolvedRefs checked |
| `deployFailed` | Programmed=False, empty addresses | Same as deployed (route acceptance is deployment-independent) |

TLSRoute and TCPRoute status is written on the `deployed` outcome only (see their sections above).

---

## Known limitations

**Not implemented:**

1. **ExtensionRef filter** — the general custom-filter extension mechanism (planned as the Gateway API equivalent of Ingress annotations). One narrow internal use exists: an `ExtensionRef` selecting SSL passthrough is honored.
2. **Per-backend filters** (`backendRefs[].filters[]`) beyond `RequestHeaderModifier` — a `RequestHeaderModifier` on a `backendRef` **is** emitted per-backend (rule-scoped via `gw_rule_id`; see `test-httproute-backend-request-header-modifier`). The other filter types (ResponseHeaderModifier, RequestRedirect, URLRewrite, RequestMirror) apply at the rule level only.
3. **Listener-specific HTTP route isolation** — `sectionName` drives `attachedRoutes` status counting, but HTTP/HTTPS routing itself isn't isolated per listener. (TLSRoute and TCPRoute do route per listener; see their sections.)

**Implemented but not pinned by this library's `validationTests`:**

- Cross-namespace **backend** references (`backendRef.namespace` honored, gated by `ReferenceGrant`) — exercised by the upstream Gateway API conformance suite instead.
- Cross-namespace **parent** Gateway references.
- Wildcard hostname patterns (regex host-map support exists).

TLSRoute- and TCPRoute-specific limitations are listed in their sections above. If one of these gaps matters to you, [open an issue](https://gitlab.com/haproxy-haptic/haptic/-/issues).

---

## Access-log fields

The library contributes `gw_route` to the [structured access log](../haproxy-deployment.md#access-logging)
when at least one Gateway exists. It carries `<namespace>_<name>_<ruleIndex>` of
the HTTPRoute rule that won, which answers "which rule of this route matched?" —
the core `resource` field already names the route itself.

## See also

- [Gateway API Documentation](https://gateway-api.sigs.k8s.io/)
- [GatewayClass](../gateway-class.md) - Configuring the class this controller owns (`gatewayClass.name`, `gatewayClass.controllerName`)
- [Template Reference](../template-reference.md) - Template context, typed resource access, and functions the library's snippets build on
- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Extension points and routing infrastructure
- [SSL Library](ssl.md) - TLS certificate management
- [haproxytech library](haproxytech.md) - Annotation-based configuration
