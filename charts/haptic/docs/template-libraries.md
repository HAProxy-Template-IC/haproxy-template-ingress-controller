# Template Libraries

Template libraries are modular, composable configuration packages that extend HAProxy functionality. Each library provides specific features that can be enabled or disabled based on your requirements.

## Overview

HAPTIC uses a library-based architecture where YAML configuration files are merged at Helm render time. This enables:

- **Modularity**: Enable only the features you need
- **Extensibility**: Add custom configuration via extension points
- **Maintainability**: Each library focuses on a specific concern
- **Customization**: Override or extend library behavior through values.yaml

## Available Libraries

| Library | Default | Purpose |
|---------|---------|---------|
| [Base](libraries/base.md) | Always enabled | Core HAProxy configuration, extension point definitions |
| [SSL](libraries/ssl.md) | Enabled | TLS certificate management, HTTPS frontend |
| [Ingress](libraries/ingress.md) | Enabled | Kubernetes Ingress resource support |
| [Gateway API](libraries/gateway.md) | Enabled | Gateway API (HTTPRoute, GRPCRoute) support |
| [haproxytech](libraries/haproxytech.md) | Enabled | `haproxy.org/*` annotations ([haproxytech/kubernetes-ingress](https://github.com/haproxytech/kubernetes-ingress) compat) |
| [haproxy-ingress](libraries/haproxy-ingress.md) | Enabled | `haproxy-ingress.github.io/*` annotations ([jcmoraisjr/haproxy-ingress](https://haproxy-ingress.github.io/) compat) |
| [Path Regex Last](libraries/path-regex-last.md) | Disabled | Performance-first path matching order |

## Enabling and Disabling Libraries

Configure libraries in your values.yaml:

```yaml
controller:
  templateLibraries:
    base:
      enabled: true   # Always enabled (cannot be disabled)
    ssl:
      enabled: true   # TLS/HTTPS support
    ingress:
      enabled: true   # Kubernetes Ingress
    gateway:
      enabled: true   # Gateway API
    haproxytech:
      enabled: true   # haproxy.org annotations
    haproxyIngress:
      enabled: true   # HAProxy Ingress compatibility
    pathRegexLast:
      enabled: false  # Performance optimization (opt-in)
```

## Library Merge Order

Libraries are merged in a specific order, with later libraries overriding earlier ones:

```
1. base.yaml           (lowest priority)
2. ssl.yaml
3. ingress.yaml
4. gateway.yaml
5. haproxytech.yaml
6. haproxy-ingress.yaml
7. path-regex-last.yaml
8. values.yaml         (highest priority - your configuration)
```

Your custom configuration in `controller.config` always takes precedence.

## Extension Points

Extension points are the core mechanism for library extensibility. The base library defines **hook points** where other libraries (or your custom configuration) can inject content.

### How Extension Points Work

The base library uses `include_matching("prefix-*")` to automatically include all template snippets matching a glob pattern:

```scriggo
{# In base.yaml #}
{%- import "util-macros" for include_matching -%}
{{ include_matching("backends-*") }}
```

This includes all snippets with names starting with `backends-`:

- `backends-ingress` (from ingress library)
- `backends-gateway` (from gateway library)
- `backends-custom` (from your values.yaml)

### Available Extension Points

| Extension Point | Prefix Pattern | Where Included | Purpose |
|-----------------|----------------|----------------|---------|
| Global Settings | `global-settings-*` | Inside `global` section | Global directives (logging, process, paths, SSL tuning) |
| Defaults Settings | `defaults-settings-*` | Inside `defaults` section | Defaults directives (options, balance, timeouts, errorfiles) |
| Features | `features-*` | Early in config | Feature initialization, SSL setup |
| Global Top | `global-top-*` | After `defaults` | Userlists, peers, global elements |
| Frontend Extra | `frontend-extra-*` | After frontend bind, before routing | Early frontend directives (options, captures, ACLs) |
| Frontend Matchers | `frontend-matchers-advanced-*` | Frontend routing | Method, header, query matching |
| Frontend Filters | `frontend-filters-*` | HTTP frontend | Request/response processing |
| Custom Frontends | `frontends-*` | After HTTP frontend | HTTPS, TCP frontends |
| Custom Backends | `backends-*` | Before default backend | Backend definitions |
| Backend Directives | `backend-directives-*` | Within backends | Per-backend configuration |
| Host Map | `map-host-*` | host.map | Host routing entries |
| Path Exact Map | `map-path-exact-*` | path-exact.map | Exact path entries |
| Path Prefix Exact Map | `map-pfxexact-*` | path-prefix-exact.map | Prefix exact entries |
| Path Prefix Map | `map-path-prefix-*` | path-prefix.map | Prefix path entries |
| Path Regex Map | `map-path-regex-*` | path-regex.map | Regex path entries |
| Weighted Backend Map | `map-weighted-backend-*` | weighted-multi-backend.map | Weighted routing |
| Status Patches | `status-patches-*` | After features, before backends | Resource status updates (side effects only, no config output) |

### Injecting Custom Configuration

Add custom snippets in your values.yaml to inject configuration at extension points:

```yaml
controller:
  config:
    templateSnippets:
      # Override default timeouts (replaces defaults-settings-300-timeouts)
      defaults-settings-300-timeouts:
        template: |
          timeout connect 5000
          timeout client 30000
          timeout server 30000
          timeout tunnel 600000
          timeout http-request 10000

      # Add custom global tuning directives
      global-settings-500-tuning:
        template: |
          tune.bufsize 262144
          no-memory-trimming

      # Add early frontend directives (matches frontend-extra-*)
      frontend-extra-custom-captures:
        template: |
          capture request header X-Request-ID len 64

      # Inject into frontend (matches frontend-filters-*)
      frontend-filters-security:
        template: |
          # Block admin paths from external IPs
          http-request deny if { path_beg /admin } !{ src 10.0.0.0/8 }

      # Inject into backends (matches backends-*)
      backends-maintenance:
        template: |
          backend maintenance
              http-request return status 503 content-type text/html string "<h1>Maintenance</h1>"

      # Inject into host map (matches map-host-*)
      map-host-custom:
        template: |
          # Custom host routing
          legacy.example.com legacy.example.com
```

### Snippet Priority

Control execution order with the `priority` field (lower numbers execute first):

```yaml
controller:
  config:
    templateSnippets:
      features-init-early:
        priority: 10   # Runs early
        template: |
          {# Initialize something first #}

      features-init-late:
        priority: 200  # Runs after other features-* snippets
        template: |
          {# Finalize after other initialization #}
```

Default priority is 100 if not specified.

### Which Libraries Use Which Extension Points

| Library | Extension Points Used |
|---------|----------------------|
| Base | Defines all extension points; provides `global-settings-*`, `defaults-settings-*` snippets |
| SSL | `features-*`, `frontends-*`, `backends-*`, `global-top-*` |
| Ingress | `features-*`, `backends-*`, `map-host-*`, `map-path-*`, `status-patches-*` |
| Gateway | `features-*`, `backends-*`, `map-*`, `frontend-matchers-advanced-*`, `frontend-filters-*`, `status-patches-*` |
| haproxytech | `global-top-*`, `backend-directives-*`, `frontend-filters-*` |
| haproxy-ingress | `map-path-regex-*` |
| Path Regex Last | Overrides `frontend-routing-logic` (not an extension point pattern) |

## Custom Libraries

You can create custom libraries by providing template snippets that implement extension point patterns:

```yaml
# values.yaml
controller:
  config:
    # Add watched resources for your custom library
    watchedResources:
      configmaps:
        apiVersion: v1
        resources: configmaps
        indexBy: ["metadata.namespace", "metadata.name"]

    # Implement extension points
    templateSnippets:
      # Process ConfigMaps and generate backends
      backends-configmap-routes:
        template: |
          {%- for cm in resources.configmaps.List() %}
          {%- if cm.metadata.labels["routing"] | fallback("") == "enabled" %}
          backend cm_{{ cm.metadata.namespace }}_{{ cm.metadata.name }}
              # Generate backend from ConfigMap data
              server app {{ cm.data["target"] }}
          {%- end %}
          {%- end %}

      # Generate host map entries
      map-host-configmap-routes:
        template: |
          {%- for cm in resources.configmaps.List() %}
          {%- if cm.metadata.labels["routing"] | fallback("") == "enabled" %}
          {{ cm.data["hostname"] }} {{ cm.data["hostname"] }}
          {%- end %}
          {%- end %}
```

## Library Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        values.yaml                          │
│                    (highest priority)                       │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│              Optional Libraries (if enabled)                │
│  path-regex-last.yaml  haproxy-ingress.yaml  gateway.yaml  │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                  Core Libraries                             │
│       haproxytech.yaml    ingress.yaml    ssl.yaml         │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                       base.yaml                             │
│        (defines extension points, lowest priority)          │
└─────────────────────────────────────────────────────────────┘
```

## See Also

- [Base Library](libraries/base.md) - Core template and extension point definitions
- [SSL Library](libraries/ssl.md) - TLS certificate management and HTTPS frontend
- [Ingress Library](libraries/ingress.md) - Kubernetes Ingress resource support
- [Gateway API Library](libraries/gateway.md) - HTTPRoute and GRPCRoute support
- [haproxytech library](libraries/haproxytech.md) - `haproxy.org/*` annotations ([haproxytech/kubernetes-ingress](https://github.com/haproxytech/kubernetes-ingress) compat)
- [haproxy-ingress library](libraries/haproxy-ingress.md) - `haproxy-ingress.github.io/*` annotations ([jcmoraisjr/haproxy-ingress](https://haproxy-ingress.github.io/) compat)
- [Path Regex Last Library](libraries/path-regex-last.md) - Alternative path matching order
