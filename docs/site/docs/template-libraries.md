# Template libraries

Template libraries are modular, composable configuration packages that extend HAProxy's capabilities. You enable or disable each library independently in values.yaml.

## Overview

HAPTIC uses a library-based architecture where YAML configuration files are merged at Helm render time. This enables:

- **Modularity**: Enable only the features you need
- **Extensibility**: Add custom configuration via extension points
- **Customization**: Override or extend library behavior through values.yaml

See the full library stack compose into one HAProxy config live:

<div class="pg-embed" markdown data-scenario="all" data-facade="spec.templateSnippets.map-host-500-ingress" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Full library stack → HAProxy config" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, change the `blog` Ingress's host from `blog.example.com` to `news.example.com`, then open the `maps` tab and watch the `host.map` entry follow.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

The Ingress library's `map-host-500-ingress` snippet emits one `host host` line per Ingress rule host, so `host.map` grows a `blog.example.com blog.example.com` entry for the `blog` Ingress. Rename the host and that line becomes `news.example.com news.example.com` — the whole stack re-renders from the edited resource, and only this library owns the host-to-group mapping.

</details>

</div>

## Available libraries

| Library | Default | Purpose |
|---------|---------|---------|
| [Base](libraries/base.md) | Enabled | Core HAProxy configuration, extension point definitions; disabling drops the `haproxyConfig` the other libraries plug into |
| [SSL](libraries/ssl.md) | Enabled | TLS certificate management, HTTPS frontend |
| [Ingress](libraries/ingress.md) | Enabled | Kubernetes Ingress resource support |
| [Gateway API](libraries/gateway.md) | Enabled | Gateway API (HTTP, gRPC, TLS and TCP routes) support |
| [ingress-annotations-compat](libraries/ingress-annotations-compat.md) | Enabled | Shared scaffold consumed by the Ingress vendor annotation libraries below (level 2.5) |
| [haptic-annotations](libraries/haptic-annotations.md) | Enabled | `haproxy-haptic.org/*` — HAPTIC's native vocabulary; a best-of-breed superset of the three vendor libraries. The only annotation library on by default |
| [haproxytech](libraries/haproxytech.md) | Disabled | `haproxy.org/*` annotations ([haproxytech/kubernetes-ingress](https://github.com/haproxytech/kubernetes-ingress) compat) — opt-in migration aid |
| [haproxy-ingress](libraries/haproxy-ingress.md) | Disabled | `haproxy-ingress.github.io/*` annotations ([jcmoraisjr/haproxy-ingress](https://haproxy-ingress.github.io/) compat) — opt-in migration aid |
| [nginx-ingress](libraries/nginx-ingress.md) | Disabled | `nginx.ingress.kubernetes.io/*` annotations ([kubernetes/ingress-nginx](https://kubernetes.github.io/ingress-nginx/) compat) — opt-in migration aid |
| [spoa-hub](operations/spoa-hub.md) | Auto | HAProxy-side wiring for the Stream Processing Offload Agent (SPOA) hub sidecar (auto-loaded when `spoaHub.enabled: true` or any `spoaHub.plugins.<X>.enabled` is truthy) |

## Enabling and disabling libraries

Configure libraries in your values.yaml (see the [Chart Values Reference](./reference.md#template-libraries) for every `controller.templateLibraries.*` value):

```yaml
controller:
  templateLibraries:
    base:
      enabled: true   # Default — disabling drops the haproxyConfig the other libraries plug into
    ssl:
      enabled: true   # TLS/HTTPS support
    ingress:
      enabled: true   # Kubernetes Ingress
    gateway:
      enabled: true   # Gateway API
    hapticAnnotations:
      enabled: true   # haproxy-haptic.org native annotations (default; best-of-breed superset)
    haproxytech:
      enabled: false  # haproxy.org compat — opt-in migration aid
    haproxyIngress:
      enabled: false  # haproxy-ingress.github.io compat — opt-in migration aid
    nginxIngress:
      enabled: false  # nginx-ingress compat — opt-in migration aid
  config:
    templatingSettings:
      extraContext:
        routing:
          regexMatchOrder: default  # "default" or "last" — see Path Matching Order below
```

## Path matching order

Path-based routing inside the rendered `frontend-routing-logic` snippet evaluates four map types: exact, regex, prefix-exact, and prefix. The evaluation order is selected by `controller.config.templatingSettings.extraContext.routing.regexMatchOrder`:

| Value | Order | Use case |
|-------|-------|----------|
| `default` (default) | Exact > Regex > Prefix-exact > Prefix | De-facto standard; matches typical Ingress controller behaviour |
| `last` | Exact > Prefix-exact > Prefix > Regex | Performance-first; evaluates faster matchers before regex |

The chart swaps in the `frontend-routing-logic-regex-last` variant of the snippet at Helm load time when `last` is set. No runtime difference.

## Library merge order

Libraries are merged in a specific order, with later libraries overriding earlier ones:

```
1. base.yaml             (lowest priority)
2. ssl.yaml
3. ingress.yaml
4. gateway/
5. ingress-annotations-compat.yaml  (level 2.5 - Ingress-only shared scaffold)
6. haptic-annotations/   (native haproxy-haptic.org/* superset)
7. haproxytech.yaml
8. haproxy-ingress/
9. nginx-ingress/
10. spoa-hub/            (auto-loaded when SPOA hub sidecar is enabled)
11. controller.config.*  (highest priority - your values.yaml overrides for templateSnippets / maps / files / sslCertificates / haproxyConfig / validationTests / watchedResources)
```

Your custom configuration in `controller.config` always takes precedence.

## Extension points

Extension points are **hook points** the base library defines, where other libraries — or your own configuration — inject content.

### How extension points work

The base library uses `render_glob "prefix-*"` to automatically include all template snippets matching a glob pattern:

```scriggo
{# In base.yaml #}
{{ render_glob "backends-*" }}
```

This includes all snippets whose names start with `backends-` (for example `backends-500-ingress`, `backends-500-gateway`, any user-provided `backends-*`). Snippets render in alphabetical order, so numeric prefixes control execution order — see the [snippet priority numbering table](#snippet-priority) below.

### Available extension points

These are the extension points custom snippets most commonly target. The authoritative full registry — including the bind hooks, listener-port translation, and the per-backend feature maps (body size, header overrides, path rewrite) — is [Base Library → Available Extension Points](libraries/base.md#available-extension-points).

| Extension Point | Prefix Pattern | Where Included | Purpose |
|-----------------|----------------|----------------|---------|
| Global Settings | `global-settings-*` | Inside `global` section | Global directives (logging, process, paths, SSL tuning) |
| Defaults Settings | `defaults-settings-*` | Inside `defaults` section | Defaults directives (options, balance, timeouts, errorfiles) |
| Features | `features-*` | Early in config | Feature initialization, SSL setup |
| Global Top | `global-top-*` | After `defaults` | Userlists, peers, global elements |
| Frontend Extra | `frontend-extra-*` | After frontend bind, before routing | Early frontend directives (options, captures, ACLs) |
| Frontend Matchers | `frontend-matchers-advanced-*` | Frontend routing | Method, header, query matching |
| Frontend Filters | `frontend-filters-*` | HTTP frontend | Request/response processing |
| Access Log Fields | `log-fields-*` | Per-frontend `log-format` | Named JSON fields for the structured access log |
| Custom Frontends | `frontends-*` | After HTTP frontend | HTTPS, TCP frontends |
| Custom Backends | `backends-*` | Before default backend | Backend definitions |
| Backend Directives | `backend-directives-*` | Within Ingress backends | Per-backend configuration (defined by the ingress library, not base) |
| Host Map | `map-host-*` | host.map | Host routing entries |
| Path Exact Map | `map-path-exact-*` | path-exact.map | Exact path entries |
| Path Prefix Exact Map | `map-pfxexact-*` | path-prefix-exact.map | Prefix exact entries |
| Path Prefix Map | `map-path-prefix-*` | path-prefix.map | Prefix path entries |
| Path Regex Map | `map-path-regex-*` | path-regex.map | Regex path entries |
| Weighted Backend Map | `map-weighted-backend-*` | weighted-multi-backend.map | Weighted routing |
| Status Patches | `status-patches-*` | After features, before backends | Resource status updates (side effects only, no config output) |

### Injecting custom configuration

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

### Library Configuration via `extraContext`

`extraContext` is a parameter bag exposed to every snippet (read with
`extraContext | dig("key") | fallback("default")`). It carries chart-computed
values (ports, the HAProxy service name, …) plus anything you set under
`controller.config.templatingSettings.extraContext`.

Bundled libraries ship sensible **defaults** for their tunables, which you can
override here. For example, the nginx-ingress library's HTTP→HTTPS redirect
status code — HAPTIC's equivalent of ingress-nginx's global `http-redirect-code`
(default `308`):

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        nginxHttpRedirectCode: "301"   # override the library default of 308
```

A value you set here always wins over the library's default. Custom snippets
read any key the same way:

```scriggo
{%- var code = extraContext | dig("nginxHttpRedirectCode") | fallback("308") | tostring() %}
```

### Snippet priority

Snippets within a `render_glob` pattern execute in **alphabetical order**. Priority is encoded in the snippet name via a numeric prefix:

```yaml
controller:
  config:
    templateSnippets:
      # Runs early (alphabetically sorts before the 500-range)
      features-050-my-init:
        template: |
          {# Initialize something first #}

      # Runs after the core 500-range snippets
      features-700-my-finalize:
        template: |
          {# Finalize after other initialization #}
```

Reserved numeric ranges used by the built-in libraries:

| Range | Purpose |
|-------|---------|
| 000-099 | Infrastructure / initialization |
| 100-199 | Feature registration |
| 200-499 | Security, Cross-Origin Resource Sharing (CORS), header manipulation, redirects |
| 500-599 | Core features (ingress, gateway) |
| 600-699 | haproxy-ingress (`haproxy-ingress.github.io/*`) compatibility |
| 700-799 | nginx-ingress (`nginx.ingress.kubernetes.io/*`) compatibility |
| 800-899 | haptic-annotations (`haproxy-haptic.org/*`) native vocabulary |
| 900-999 | Finalization / cleanup |

The haproxytech (`haproxy.org/*`) library is the exception among the vendor annotation libraries: its snippets sit in the 100–500 band rather than a dedicated block. The haproxy-ingress (600), nginx-ingress (700), and haptic-annotations (800) ranges deliberately sort after it, so when annotations from more than one prefix target the same directive on one Ingress, the later library's snippet wins — the native `haproxy-haptic.org/*` value layers last.

To override a built-in snippet, use the **same key name**; values-file entries take precedence over library entries during merge.

### Which libraries use which extension points

| Library | Extension Points Used |
|---------|----------------------|
| Base | Defines all extension points; provides `global-settings-*`, `defaults-settings-*` snippets |
| SSL | `features-*`, `frontends-*`, `backends-*`, `global-top-*` |
| Ingress | `features-*`, `backends-*`, `map-host-*`, `map-path-*`, `status-patches-*` |
| Gateway | `features-*`, `backends-*`, `map-*`, `frontend-matchers-advanced-*`, `frontend-filters-*`, `status-patches-*` |
| haptic-annotations | `map-path-*`, `map-pfxexact-*`, `map-host-*`, `map-hostregex-*`, `backend-directives-*`, `frontend-filters-*`, `features-*`, `backends-*`, `global-*`, `defaults-settings-*`, `frontend-extra-*` |
| haproxytech | `global-top-*`, `backend-directives-*`, `frontend-filters-*` |
| haproxy-ingress | `features-*`, `map-path-*`, `map-pfxexact-*`, `backend-directives-*`, `frontend-filters-*`, `global-top-*`, `backends-*` |
| nginx-ingress | `features-*`, `backends-*`, `global-top-*`, `backend-directives-*`, `frontend-filters-*` |

## Custom libraries

You can create custom libraries by watching any Kubernetes resource and implementing extension point patterns against it. Because HAPTIC is resource-agnostic, a plain ConfigMap becomes HAProxy config the same way an Ingress does — watch it, then emit into `backends-*` and `map-host-*` from a template snippet.

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="ConfigMaps → backends and host.map" data-height="480">

<p class="pg-task" markdown>Open the **Resources** panel and add `routing: enabled` to the `blog` ConfigMap's `metadata.labels`, then watch a `backend cm_content_blog` block appear in `haproxy.cfg` and a matching line show up in the `maps` tab.</p>

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: configmap-library-demo
spec:
  # Watch a resource the bundled libraries never touch.
  watchedResources:
    configmaps:
      apiVersion: v1
      resources: configmaps
      indexBy: ["metadata.namespace", "metadata.name"]

  # A minimal base that invokes the extension points your snippets plug into.
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend http
        bind :80
        use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]
        default_backend not-found
      {{ render_glob "backends-*" }}
      backend not-found
        http-request deny deny_status 404
  maps:
    host.map:
      template: |
        {{ render_glob "map-host-*" }}

  templateSnippets:
    # Emit one backend per labeled ConfigMap (matches backends-*).
    backends-configmap-routes:
      template: |
        {%- for cm in resources.configmaps.List() %}
        {%- if cm.metadata.labels["routing"] == "enabled" %}
        backend cm_{{ cm.metadata.namespace }}_{{ cm.metadata.name }}
            server app {{ cm.data["target"] }}
        {%- end %}
        {%- end %}

    # Emit one host.map entry per labeled ConfigMap (matches map-host-*).
    map-host-configmap-routes:
      template: |
        {%- for cm in resources.configmaps.List() %}
        {%- if cm.metadata.labels["routing"] == "enabled" %}
        {{ cm.data["hostname"] }} cm_{{ cm.metadata.namespace }}_{{ cm.metadata.name }}
        {%- end %}
        {%- end %}
```

```yaml
apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: shop
      namespace: storefront
      labels: {routing: enabled}
    data:
      hostname: shop.example.com
      target: 10.0.1.10:8080
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: blog
      namespace: content
    data:
      hostname: blog.example.com
      target: 10.0.2.20:8080
```

</div>

In your own values.yaml, drop the same `watchedResources` and `templateSnippets` under `controller.config` — the bundled base library already provides the `render_glob` invocations, so you only supply the snippets.

## Library Architecture

Each library sits at a hierarchy level that determines which other
libraries it may reference and the order it merges in. Lower levels merge
first; higher levels override. `controller.config` from values.yaml is
applied last and overrides anything below it.

```
┌──────────────────────────────────────────────────────────────────────┐
│                          values.yaml                                 │
│                       (highest priority)                             │
└──────────────────────────────────────────────────────────────────────┘
                                   ▲
┌──────────────────────────────────────────────────────────────────────┐
│ Level 3 — Vendor annotation libraries                                │
│   haproxytech.yaml      haproxy-ingress/          nginx-ingress/     │
│   (each compat-layer for one ingress controller's annotation set)    │
└──────────────────────────────────────────────────────────────────────┘
                                   ▲
┌──────────────────────────────────────────────────────────────────────┐
│ Level 2.5 — Ingress-annotations-compat scaffold                      │
│   ingress-annotations-compat.yaml  (shared macros for Ingress vendor │
│                                     libraries above; Ingress-scoped) │
└──────────────────────────────────────────────────────────────────────┘
                                   ▲
┌──────────────────────────────────────────────────────────────────────┐
│ Level 2 — Resource libraries                                         │
│   ingress.yaml                          gateway/                     │
│   (Kubernetes Ingress)                  (Gateway API HTTP/gRPC/     │
│                                          TLS/TCP routes)             │
└──────────────────────────────────────────────────────────────────────┘
                                   ▲
┌──────────────────────────────────────────────────────────────────────┐
│ Level 1 — SSL/TLS infrastructure                                     │
│   ssl.yaml                                                           │
└──────────────────────────────────────────────────────────────────────┘
                                   ▲
┌──────────────────────────────────────────────────────────────────────┐
│ Level 0 — Resource-agnostic core                                     │
│   base.yaml  (defines extension points; lowest priority)             │
└──────────────────────────────────────────────────────────────────────┘
```

The `spoa-hub/` library wires the SPOA hub sidecar into HAProxy
and is auto-loaded whenever the sidecar is enabled (by an explicit
`spoaHub.enabled: true` or any `spoaHub.plugins.<X>.enabled` truthy).
It plugs into the same extension points as the level-3 libraries
above.

Each library's own page (linked from the [Available Libraries](#available-libraries) table above) documents its snippets, tunables, and extension points in detail.

## See also

- [Annotations](./annotations.md) — which vendor annotation library covers which annotation prefix
- [Templating Guide](./templating.md) — writing your own snippets and templates
- [Chart Values Reference → Template Libraries](./reference.md#template-libraries) — every `controller.templateLibraries.*` value
