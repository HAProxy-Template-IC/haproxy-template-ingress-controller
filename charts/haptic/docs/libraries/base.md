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
| Features | `features-*` | Early in config generation | Feature initialization and registration |
| Global Top | `global-top-*` | After `defaults` section | Top-level HAProxy elements (userlists, peers, etc.) |
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

The base library generates this configuration structure:

```haproxy
global
    log stdout len 4096 local0 info
    daemon
    ca-base /etc/ssl/certs
    crt-base /etc/haproxy/certs
    tune.ssl.default-dh-param 2048

defaults
    mode http
    log global
    option httplog
    option dontlognull
    option log-health-checks
    option forwardfor
    timeout connect 5000
    timeout client 50000
    timeout server 50000
    errorfile 400 /etc/haproxy/general/400.http
    # ... other error files

# global-top-* snippets here (userlists, etc.)

frontend status
    bind *:8404
    # Health check endpoints

frontend http_frontend
    bind *:8080
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

## See Also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [SSL Library](ssl.md) - TLS certificate management and HTTPS frontend
- [Path Regex Last Library](path-regex-last.md) - Alternative path matching order
