# Base Library

The base library provides the core HAProxy configuration infrastructure. It defines extension points that other libraries use to inject their functionality, making it the foundation of the template library system.

## Overview

The base library is **enabled by default** and provides the entire `haproxyConfig` template that the rest of the libraries plug into. It provides:

- Core HAProxy configuration structure (global, defaults, frontends, backends)
- The plugin pattern via extension points for other libraries to inject content
- Frontend routing logic with path matching and backend selection
- Utility macros for template development
- Error page templates
- Map file infrastructure for routing decisions

## Configuration

The base library has the standard enable/disable flag, but disabling it is rarely useful: every other library plugs into the extension points base provides, so setting it to `false` produces a broken render with no `haproxyConfig` and no extension points.

```yaml
controller:
  templateLibraries:
    base:
      enabled: true  # Default; leave on unless you supply a complete replacement haproxyConfig
```

## Extension Points

The base library defines extension points using the `render_glob "prefix-*"` operator. Any template snippet with a matching prefix is automatically rendered at the designated location in the HAProxy configuration.

### Available Extension Points

| Extension Point | Prefix Pattern | Location in Config | Purpose |
|-----------------|----------------|-------------------|---------|
| Global Settings | `global-settings-*` | Inside `global` section | Global directives (logging, process, paths, SSL tuning) |
| Defaults Settings | `defaults-settings-*` | Inside `defaults` section | Defaults directives (options, balance, timeouts, errorfiles) |
| Features | `features-*` | Early in config generation | Feature initialization and registration |
| Global Top | `global-top-*` | After `defaults` section | Top-level HAProxy elements (userlists, peers, etc.) |
| Frontend Extra | `frontend-extra-*` | After frontend bind, before routing | Early frontend directives (options, captures, ACLs) |
| Frontend Matchers | `frontend-matchers-advanced-*` | Within frontend routing logic | Advanced request matching (method, headers, query params) |
| Frontend Filters | `frontend-filters-*` | HTTP frontend, after routing | Request/response filters (header modification, redirects) |
| Custom Frontends | `frontends-*` | After HTTP frontend | Additional frontend definitions |
| Custom Backends | `backends-*` | Before default_backend | Backend definitions from resource libraries |
| Host Map | `map-host-*` | host.map file | Host-to-group mapping entries |
| Path Exact Map | `map-path-exact-*` | path-exact.map file | Exact path match entries |
| Path Prefix Exact Map | `map-pfxexact-*` | path-prefix-exact.map file | Prefix-exact path match entries |
| Path Prefix Map | `map-path-prefix-*` | path-prefix.map file | Prefix path match entries |
| Path Regex Map | `map-path-regex-*` | path-regex.map file | Regex path match entries |
| Weighted Backend Map | `map-weighted-backend-*` | weighted-multi-backend.map file | Weighted routing entries |
| Status Patches | `status-patches-*` | After features, before backends | Resource status patch registration (side effects only) |
| Status Extra | `status-extra-*` | Inside the status frontend | Extra status-frontend directives (Prometheus exporter, custom endpoints) |

`backend-directives-*` is **not** a base-library extension point. It is invoked by `ingress.yaml`'s `backends-500-ingress` snippet (with `inherit_context`) so per-backend annotation libraries can extend each backend block; see [haproxytech library](haproxytech.md) for the producer side. Templates outside the ingress backend loop won't see it.

### How Extension Points Work

1. Base library uses `render_glob "prefix-*"` to render all snippets matching the pattern
2. Other libraries (or user config) define snippets with matching prefixes
3. At render time, all matching snippets are rendered in **alphabetical order** — numeric prefixes in snippet names (e.g. `backends-500-ingress`) control execution order

### Injecting Custom Configuration

You can inject custom HAProxy configuration by adding template snippets with the appropriate prefix in your values.yaml:

```yaml
controller:
  config:
    templateSnippets:
      # Override default timeouts (replaces the base library snippet)
      defaults-settings-300-timeouts:
        template: |
          timeout connect 5000
          timeout client 30000
          timeout server 30000
          timeout tunnel 600000
          timeout http-request 10000

      # Add custom global tuning directives (extends the global section)
      global-settings-500-tuning:
        template: |
          tune.bufsize 262144
          no-memory-trimming

      # Add custom security rules to the HTTP frontend
      frontend-filters-custom-security:
        template: |
          http-request deny if { path_beg /admin } !{ src 10.0.0.0/8 }
          http-request deny if { path_beg /.env }

      # Add a custom userlist
      global-top-custom-userlist:
        template: |
          userlist api_users
            user apiuser password $2y$05$...

      # Add custom backend
      backends-custom-maintenance:
        template: |
          backend maintenance_backend
              http-request return status 503 content-type text/html string "<h1>Under Maintenance</h1>"
```

### Snippet Priority

Snippets within a `render_glob` pattern execute in **alphabetical order**. Encode priority in the snippet name via a numeric prefix (lower numbers run first):

```yaml
templateSnippets:
  # Runs early — sorts before the 500-range
  features-050-ssl-initialization:
    template: |
      {# Initialize SSL infrastructure #}

  # Runs later — sorts after the 100-range certificate registration
  features-150-ssl-crtlist:
    template: |
      {# Generate certificate list #}
```

See [Template Libraries → Snippet Priority](../template-libraries.md#snippet-priority) for the reserved range conventions used by the built-in libraries.

## Features

### Frontend Routing Logic

The base library implements a sophisticated routing system using HAProxy maps and transaction variables:

1. **Host matching**: Extracts and matches the Host header
2. **Path matching**: Evaluates paths in order (Exact > Regex > Prefix-exact > Prefix). Set `controller.config.routing.regexMatchOrder=last` in values.yaml to switch to performance-first ordering (Exact > Prefix-exact > Prefix > Regex).
3. **Qualifier system**: Supports `BACKEND` (direct) and `MULTIBACKEND` (weighted) routing

```haproxy
# Path matching order: Exact > Regex > Prefix-exact > Prefix
http-request set-var(txn.path_match) var(txn.host),concat(,txn.path,),map(maps/path-exact.map)
http-request set-var(txn.path_match) var(txn.host),concat(,txn.path,),map_reg(maps/path-regex.map) if !{ var(txn.path_match) -m found }
http-request set-var(txn.path_match) var(txn.host),concat(,txn.path,),map(maps/path-prefix-exact.map) if !{ var(txn.path_match) -m found }
http-request set-var(txn.path_match) var(txn.host),concat(,txn.path,),map_beg(maps/path-prefix.map) if !{ var(txn.path_match) -m found }
```

!!! note "Overriding Path Match Order"
    Setting `controller.config.routing.regexMatchOrder=last` swaps in the alternate `frontend-routing-logic-regex-last` variant of this snippet at Helm load time, producing performance-first ordering (Exact > Prefix-exact > Prefix > Regex). Faster matchers run first and regex matching is only evaluated as a fallback. The variant snippet is unset before the merged config is rendered, so it never appears in the operator-visible output.

### Built-in Operators and Functions

#### render_glob (operator)

Renders all snippets matching a glob pattern. This is a built-in Scriggo operator, not a macro — no import is needed:

```scriggo
{{ render_glob "backends-*" }}
{{ render_glob "map-host-*" inherit_context }}
```

See the [Scriggo template guide](https://haproxy-haptic.org/controller/latest/templating/) for details.

#### sanitize_regex (function)

Built-in function that escapes regex metacharacters so a user-supplied pattern can be safely embedded in a `map_reg()` lookup:

```scriggo
{{ sanitize_regex("^/api/v[0-9]+$") }}
```

### Utility Macros (`util-macros`)

The `util-macros` snippet provides reusable macros imported by other libraries:

| Macro | Purpose |
|-------|---------|
| `SanitizeRegex(pattern)` | Escapes `$` for HAProxy's double-quoted context |
| `CalculateShardCount(resourceCount, itemsPerShard)` | Computes `clamp(count / itemsPerShard, 1, 2*GOMAXPROCS)` |
| `HostMatchCondition(hosts)` | Builds a host-match ACL condition |
| `BuildServerOptions(serverOpts)` | Renders server-line option flags |
| `BackendServers(serviceName, maxSlots, port, opts, portName, backendName, namespace)` | Generates the full server pool with reserved slots |

Usage:

```scriggo
{%- import "util-macros" for BackendServers %}
{{ BackendServers(serviceName, 10, port, serverOpts, nil, backendKey, namespace) }}
```

### Backend Server Pool

The `util-backend-servers` snippet generates server lines with:

- Pre-allocated server slots for dynamic scaling
- Health check configuration
- Support for per-server options (maxconn, SSL, etc.)

```scriggo
{%- var service_name = "my-service" %}
{%- var port = 8080 %}
{{ render "util-backend-servers" inherit_context }}
```

### Error Pages

Pre-configured error response templates for common HTTP errors:

| File | HTTP Status |
|------|-------------|
| 400.http | Bad Request |
| 403.http | Forbidden |
| 408.http | Request Timeout |
| 500.http | Internal Server Error |
| 502.http | Bad Gateway |
| 503.http | Service Unavailable |
| 504.http | Gateway Timeout |

### Debug Headers

When debug mode is enabled, the frontend adds response headers for routing introspection:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        debug: true
```

Debug headers include:

- `X-HAProxy-Backend`: Selected backend name
- `X-HAProxy-Host-Match`: Matched host group
- `X-HAProxy-Path-Match`: Full path match result
- `X-HAProxy-Path-Match-Qualifier`: BACKEND or MULTIBACKEND

### Shared Memory Stats (HAProxy 3.3+)

When `haproxy.shmStats.enabled` is `true` and HAProxy version is 3.3 or later, the base library adds `shm-stats-file` and `shm-stats-file-max-objects` to the global section. This persists stats counters (frontend/backend/server metrics) across HAProxy reloads via shared memory, eliminating counter resets during configuration changes.

The `shm-stats-file-max-objects` value is a configurable fixed value (default 50000) set via `haproxy.shmStats.maxObjects`. Since the shm-stats file is fixed-size and cannot be resized on reload, a large fixed value prevents reload failures when new ingresses are added. HAProxy allocates object slots lazily, so memory overhead is proportional to actual objects, not the configured maximum.

When shmStats is enabled, the chart automatically adds a `/dev/shm` emptyDir volume with `medium: Memory` to the HAProxy pod. The volume's `sizeLimit` is auto-calculated from `maxObjects` (~4KB per object with 10% margin), or can be overridden via `haproxy.shmStats.shmSizeLimit`. This volume counts against the pod's memory limit.

### Address Discovery

The base library watches controller LoadBalancer Services and discovers external addresses for status reporting. Addresses are aggregated from **all** matching services and deduplicated, then stored in `gf["addresses"]`. This supports multi-service setups where HAProxy is exposed via both internal and public LoadBalancers.

Controller Services are discovered via label selector (`app.kubernetes.io/name=<name>,app.kubernetes.io/component=loadbalancer`). If no Service has LoadBalancer addresses assigned yet, `gf["addresses"]` remains nil and status patches that depend on addresses are skipped.

Address discovery can be disabled via `controller.statusPatches.enabled: false`. When disabled, `gf["addresses"]` is never set, which prevents all `status-patches-*` snippets from writing to Ingress or Gateway status. This is useful during migration from another ingress controller to avoid premature DNS cutover when tools like external-dns watch status fields.

### Status Patch Extension Point

The `status-patches-*` extension point renders at priority 200 — after feature analysis (`features-*` at 050-150) but before backends and frontends (500+). This ensures status patches are captured even when later config generation fails, allowing the `renderFailed` variant to be applied.

Status patch snippets produce no HAProxy configuration output. They call `statusPatch()` as a side effect to register patches for later application by the controller.

## Map Files

The base library generates these map files for routing:

| Map File | Purpose | Matcher |
|----------|---------|---------|
| host.map | Host header to group mapping | Exact match |
| path-exact.map | Exact path matching | `map()` |
| path-prefix-exact.map | Prefix paths that should match exactly | `map()` |
| path-prefix.map | Prefix path matching | `map_beg()` |
| path-regex.map | Regex path matching | `map_reg()` |
| weighted-multi-backend.map | Weighted backend selection | `map()` |

## HAProxy Configuration Structure

The base library generates this configuration structure. The `global` and `defaults` sections are composed from individually overridable snippets:

```haproxy
global
    # global-settings-100-logging
    log stdout len 4096 local0 info
    # global-settings-200-process
    daemon
    nbthread 2          # auto-calculated from CPU requests
    # global-settings-300-paths — emits the BaseDir from pathResolver (chart default /etc/haproxy)
    default-path origin /etc/haproxy
    crt-base ssl/                              # relative to default-path origin
    # global-settings-400-ssl
    tune.ssl.default-dh-param 2048
    # global-settings-250-shm-stats (when haproxy.shmStats.enabled=true and HAProxy >= 3.3)
    shm-stats-file /dev/shm/haproxy-stats
    shm-stats-file-max-objects 50000  # configurable via haproxy.shmStats.maxObjects

defaults
    # defaults-settings-100-options
    mode http
    log global
    option httplog
    option dontlognull
    option log-health-checks
    option forwardfor
    # defaults-settings-200-balance
    balance roundrobin
    # defaults-settings-300-timeouts
    timeout connect 5000
    timeout client 50000
    timeout server 50000
    # defaults-settings-400-errorfiles — relative paths resolved via default-path origin
    errorfile 400 general/400.http
    # ... other error files

# global-top-* snippets here (userlists, etc.)

frontend status
    bind *:8404
    # Health check endpoints

frontend http_frontend
    bind *:8080
    # frontend-extra-* snippets (options, captures, ACLs)
    # Routing logic
    # frontend-matchers-advanced-* snippets
    # frontend-filters-* snippets
    use_backend %[var(txn.backend_name)] if { var(txn.backend_name) -m found }
    default_backend default_backend

# frontends-* snippets (HTTPS, TCP, etc.)

# backends-* snippets (resource-specific backends)

backend default_backend
    http-request return status 404
```

Each `global-settings-*` and `defaults-settings-*` snippet can be individually overridden or extended via `controller.config.templateSnippets` in your values.yaml. For example, to customize timeouts, override `defaults-settings-300-timeouts` with your own values. To add new global directives, create a `global-settings-500-tuning` snippet (or any name matching the pattern).

## See Also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [SSL Library](ssl.md) - TLS certificate management and HTTPS frontend
- [Configuration Reference](../configuration.md#path-matching-order) - Switching path-matching order
