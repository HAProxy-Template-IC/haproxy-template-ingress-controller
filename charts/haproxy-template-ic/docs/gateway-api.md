# Gateway API Support

The HAProxy Template Ingress Controller chart provides Gateway API support through the `gateway.yaml` template library. This document describes which features of Gateway API v1.2.0 HTTPRoute and GRPCRoute are currently supported.

## Overview

Gateway API support is implemented as a template library that plugs into the controller's resource-agnostic architecture. The controller itself doesn't know about Gateway API - resource support is added by the chart through template libraries.

**Enable Gateway API support:**

```yaml
# values.yaml
controller:
  templateLibraries:
    gateway:
      enabled: true
```

The Gateway API CRDs must be installed separately in your cluster. The chart automatically detects their presence and enables the gateway library if available.

## Architecture

The `gateway.yaml` library:
- Declares `httproutes` and `grpcroutes` as watched resources
- Implements backend generation for Gateway routes
- Adds routing rules to HAProxy map files
- Plugs into extension points defined in `base.yaml`

This architecture allows the controller to remain resource-agnostic while the chart provides specific resource support.

## HTTPRoute Support

### spec.parentRefs

| Field | Status | Notes |
|-------|--------|-------|
| `parentRefs[].name` | ✅ Supported | Gateway reference |
| `parentRefs[].namespace` | ⚠️ Partial | Field exists but cross-namespace not tested |
| `parentRefs[].sectionName` | ❌ Not Implemented | Listener-specific attachment not supported |
| `parentRefs[].port` | ❌ Not Implemented | Port override not supported |

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

### spec.rules[].filters

| Filter Type | Conformance | Status | Notes |
|-------------|-------------|--------|-------|
| `RequestHeaderModifier` | Core | ✅ Supported | Add/Set/Remove request headers |
| `ResponseHeaderModifier` | Extended | ✅ Supported | Add/Set/Remove response headers |
| `RequestRedirect` | Core | ✅ Supported | HTTP redirects with scheme/hostname/port/path/statusCode |
| `URLRewrite` | Extended | ✅ Supported | Path and hostname rewriting |
| `RequestMirror` | Extended | ❌ Not Implemented | Requires external SPOE agent (see [Criteo traffic-mirroring](https://github.com/criteo/traffic-mirroring)) |
| `ExtensionRef` | Implementation-specific | ❌ Not Implemented | Planned as Gateway API equivalent of Ingress annotations |

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
| `backendRefs[].filters[]` | ❌ Not Implemented | Per-backend filters not supported |
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

### Advanced Features

**Backend Deduplication:**

The template automatically deduplicates backends when multiple routes reference the same service+port combination, preventing duplicate HAProxy backend definitions.

**Route Key Generation:**

Internal route identifiers use the format `namespace_routename_ruleindex` to ensure uniqueness across namespaces and rules.

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
| `RequestMirror` | Extended | ❌ Not Implemented | Requires external SPOE agent (see [Criteo traffic-mirroring](https://github.com/criteo/traffic-mirroring)) |
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

## Debug Headers

When debug headers are enabled, the gateway library adds response headers to help troubleshoot routing decisions:

```yaml
# values.yaml
controller:
  config:
    debug:
      headers:
        enabled: true
```

**Response Headers:**
- `X-Gateway-Matched-Route` - The namespace/name of the matched HTTPRoute or GRPCRoute
- `X-Gateway-Match-Reason` - Additional information about why the route was selected (e.g., "method match", "header match")

These headers are useful for:
- Verifying which route handled a request
- Understanding precedence when multiple routes match
- Debugging complex routing configurations

## Known Limitations

### Not Implemented

1. **RequestMirror filter** - Requires external SPOE (Stream Processing Offload Engine) agent running alongside HAProxy. Cannot be implemented with configuration templates alone. Consider using [Criteo traffic-mirroring](https://github.com/criteo/traffic-mirroring) for modular traffic mirroring with HAProxy SPOE support.

2. **ExtensionRef filter** - Custom filter extension mechanism not yet implemented. This is planned as the Gateway API equivalent of Ingress annotations, enabling custom functionality beyond standard filters.

3. **Per-backend filters** (`backendRefs[].filters[]`) - Filters currently apply at the rule level only, not per-backend. This Gateway API feature allows different filter behavior for different backends within the same rule.

### Untested Features

- Cross-namespace backend references
- Cross-namespace parent Gateway references
- Wildcard hostname patterns
- Listener-specific route attachment (`sectionName`)

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
- HTTPRoute filters (RequestHeaderModifier, ResponseHeaderModifier, RequestRedirect, URLRewrite)
- Backend deduplication (multiple routes to same service+port)
- GRPCRoute backend generation with HTTP/2
- GRPCRoute method-based routing (service and method matching)
- GRPCRoute filters (RequestHeaderModifier, ResponseHeaderModifier)
- Complex route conflict resolution with VAR qualifiers

**Untested:**
- Cross-namespace references
- Wildcard hostnames
- Per-backend filters (`backendRefs[].filters[]`)
- RequestMirror filter (requires external SPOE agent)
- ExtensionRef filter (not yet implemented)

## Future Development

Priority areas for future enhancement:

1. **ExtensionRef support** - Implement custom filter extension mechanism as the Gateway API equivalent of Ingress annotations. This will enable custom functionality beyond standard filters.

2. **Per-backend filters** - Support `backendRefs[].filters[]` to allow different filter behavior for different backends within the same rule.

3. **Request mirroring** - Investigate integration with external SPOE agents (e.g., [Criteo traffic-mirroring](https://github.com/criteo/traffic-mirroring)) for traffic shadowing capabilities.

4. **Cross-namespace testing** - Validate cross-namespace backend and Gateway references work correctly.

5. **Wildcard hostname support** - Test and document wildcard hostname patterns (e.g., `*.example.com`).

## See Also

- [Gateway API Documentation](https://gateway-api.sigs.k8s.io/)
- [HAProxy Configuration Reference](../../docs/supported-configuration.md)
- [Template Library Architecture](../CLAUDE.md)
