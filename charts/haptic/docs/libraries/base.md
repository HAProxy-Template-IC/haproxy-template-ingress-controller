# Base Library

The base library provides the core HAProxy configuration infrastructure. It defines extension points that other libraries use to inject their functionality, making it the foundation of the template library system.

## Overview

The base library is **always enabled** and cannot be disabled. It provides:

- Core HAProxy configuration structure (global, defaults, frontends, backends)
- The plugin pattern via extension points for other libraries to inject content
- Frontend routing logic with path matching and backend selection
- Utility macros for template development
- Error page templates
- Map file infrastructure for routing decisions

## Configuration

The base library is always included and has no enable/disable option:

```yaml
controller:
  templateLibraries:
    base:
      enabled: true  # Always true, cannot be disabled
```

## Extension Points

The base library defines extension points using the `include_matching("prefix-*")` pattern. Any template snippet with a matching prefix is automatically included at the designated location in the HAProxy configuration.

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
| Backend Directives | `backend-directives-*` | Within backend blocks | Per-backend directives (auth, rate limiting) |
| Host Map | `map-host-*` | host.map file | Host-to-group mapping entries |
| Path Exact Map | `map-path-exact-*` | path-exact.map file | Exact path match entries |
| Path Prefix Exact Map | `map-path-prefix-exact-*` | path-prefix-exact.map file | Prefix-exact path match entries |
| Path Prefix Map | `map-path-prefix-*` | path-prefix.map file | Prefix path match entries |
| Path Regex Map | `map-path-regex-*` | path-regex.map file | Regex path match entries |
| Weighted Backend Map | `map-weighted-backend-*` | weighted-multi-backend.map file | Weighted routing entries |
| Status Patches | `status-patches-*` | After features, before backends | Resource status patch registration (side effects only) |

### How Extension Points Work

1. Base library uses `include_matching("prefix-*")` to include all snippets matching the pattern
2. Other libraries (or user config) define snippets with matching prefixes
3. At render time, all matching snippets are included in order

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

Snippets can specify a `priority` field to control inclusion order (lower numbers run first):

```yaml
templateSnippets:
  features-ssl-initialization:
    priority: 50  # Runs early
    template: |
      {# Initialize SSL infrastructure #}

  features-ssl-crtlist:
    priority: 150  # Runs after certificates are registered
    template: |
      {# Generate certificate list #}
```

## Features

### Frontend Routing Logic

The base library implements a sophisticated routing system using HAProxy maps and transaction variables:

1. **Host matching**: Extracts and matches the Host header
2. **Path matching**: Evaluates paths in priority order (Exact > Regex > Prefix)
3. **Qualifier system**: Supports `BACKEND` (direct) and `MULTIBACKEND` (weighted) routing

```haproxy
# Path matching order: Exact > Regex > Prefix-exact > Prefix
http-request set-var(txn.path_match) var(txn.host_match),concat(,txn.path,),map(/etc/haproxy/maps/path-exact.map)
http-request set-var(txn.path_match) var(txn.host_match),concat(,txn.path,),map_reg(/etc/haproxy/maps/path-regex.map) if !{ var(txn.path_match) -m found }
```

!!! note "Overriding Path Match Order"
    The `path-regex-last` library overrides this to use performance-first ordering (Exact > Prefix > Regex).

### Utility Macros

#### include_matching

Includes all snippets matching a glob pattern:

```jinja2
{%- from "util-macros" import include_matching -%}
{{ include_matching("backends-*") }}
```

#### sanitize_regex

Escapes regex patterns for HAProxy's double-quoted context:

```jinja2
{%- from "util-regex-sanitize" import sanitize_regex -%}
{{ sanitize_regex("^/api/v[0-9]+$") }}
```

### Backend Server Pool

The `util-backend-servers` snippet generates server lines with:

- Pre-allocated server slots for dynamic scaling
- Health check configuration
- Support for per-server options (maxconn, SSL, etc.)

```jinja2
{%- set service_name = "my-service" %}
{%- set port = 8080 %}
{% include "util-backend-servers" %}
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

The `shm-stats-file-max-objects` value is a configurable fixed value (default 500000) set via `haproxy.shmStats.maxObjects`. Since the shm-stats file is fixed-size and cannot be resized on reload, a large fixed value prevents reload failures when new ingresses are added. HAProxy allocates object slots lazily, so memory overhead is proportional to actual objects, not the configured maximum.

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
    # global-settings-300-paths
    default-path origin /etc/haproxy
    crt-base /etc/haproxy/certs
    # global-settings-400-ssl
    tune.ssl.default-dh-param 2048
    # global-settings-250-shm-stats (when haproxy.shmStats.enabled=true and HAProxy >= 3.3)
    shm-stats-file /dev/shm/haproxy-stats
    shm-stats-file-max-objects 2000  # dynamically calculated from guid count

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
    # defaults-settings-400-errorfiles
    errorfile 400 /etc/haproxy/general/400.http
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
- [Path Regex Last Library](path-regex-last.md) - Alternative path matching order
