# Templating Guide

## Overview

The HAProxy Template Ingress Controller uses [Scriggo](https://scriggo.com/), a Go template engine, to generate HAProxy configurations from Kubernetes resources. The Helm chart ships with ready-to-use [template libraries](/helm-chart/latest/template-libraries/) that cover standard Ingress and Gateway API use cases — you only need to write templates when you want to extend or replace that default behavior. Templates access watched Kubernetes resources, and the controller renders them whenever resources change, validates the output, and deploys it to HAProxy instances.

Templates are rendered automatically when any watched resource changes, during initial synchronization, or periodically for drift detection.

## What You Can Template

| Template Type | Use When |
|---------------|----------|
| `haproxyConfig` | Main HAProxy configuration (frontends, backends, global settings) |
| `maps` | HAProxy lookup tables for host/path routing decisions |
| `files` | Auxiliary files like custom error pages |
| `sslCertificates` | TLS certificate files assembled from Kubernetes Secrets |

### HAProxy Configuration

The main `haproxyConfig` template generates the complete HAProxy configuration file:

```yaml
haproxyConfig:
  template: |
    global
        log stdout len 4096 local0 info
        daemon
        maxconn 4096

    defaults
        mode http
        timeout connect 5s
        timeout client 50s
        timeout server 50s

    frontend http
        bind *:80
        use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

    {% for _, ingress := range resources.ingresses.List() %}
    backend {{ ingress.metadata.name }}
        balance roundrobin
    {% end %}
```

!!! important
    Whenever your HAProxy config references a map file, error file, certificate, or crt-list, use `pathResolver.GetPath(filename, type)` instead of a hard-coded path. The controller deploys these files to a configurable directory (set in `spec.dataplane.mapsDir`, `sslCertsDir`, `generalStorageDir`) and `pathResolver` knows where they live, so the path stays correct even if you reconfigure those directories.

### Map Files

Map files generate HAProxy lookup tables. They are written to `spec.dataplane.mapsDir` (default `/etc/haproxy/maps/`) on the HAProxy pod:

```yaml
maps:
  host.map:
    template: |
      {%- for _, ingress := range resources.ingresses.List() %}
      {%- for _, rule := range fallback(ingress.spec.rules, []any{}) %}
      {%- if rule.http != nil %}
      {{ rule.host }} {{ rule.host }}
      {%- end %}
      {%- end %}
      {%- end %}
```

### General Files

Auxiliary files like custom error pages. Written to `spec.dataplane.generalStorageDir` (default `/etc/haproxy/general/`):

```yaml
files:
  503.http:
    template: |
      HTTP/1.0 503 Service Unavailable
      Cache-Control: no-cache
      Connection: close
      Content-Type: text/html

      <html><body><h1>503 Service Unavailable</h1></body></html>
```

### SSL Certificates

SSL/TLS certificate files assembled from Kubernetes Secrets. Written to `spec.dataplane.sslCertsDir` (default `/etc/haproxy/ssl/`):

```yaml
sslCertificates:
  example-com.pem:
    template: |
      {%- var secret = resources.secrets.GetSingle("default", "example-com-tls") %}
      {%- if secret != nil %}
      {{ b64decode(secret.data["tls.crt"]) }}
      {{ b64decode(secret.data["tls.key"]) }}
      {%- end %}
```

!!! note
    Certificate data in Secrets is base64-encoded. Use the `b64decode` filter to decode it.

### Template Snippets

Reusable template fragments included via `{{ render "snippet-name" }}`:

```yaml
templateSnippets:
  backend-name:
    template: >-
      ing_{{ ingress.metadata.namespace }}_{{ ingress.metadata.name }}

  backend-servers:
    template: |
      {%- for _, endpoint_slice := range resources.endpoints.Fetch(service_name) %}
      {%- for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
      {%- for _, address := range fallback(endpoint.addresses, []any{}) %}
      server {{ endpoint.targetRef.name }} {{ address }}:{{ port }} check
      {%- end %}
      {%- end %}
      {%- end %}
```

Include a snippet in a template:

```go
{{ render "backend-name" }}
```

Include all snippets matching a glob pattern (rendered in alphabetical order):

```go
{{ render_glob "backend-*" }}
```

Pass local variables to rendered snippets with `inherit_context`:

```go
{%- var service_name = "my-service" %}
{{ render "backend-servers" inherit_context }}
```

### Post-Processing

The `haproxyConfig` section supports a `postProcessing` list that transforms the rendered output before deployment. Post-processors run sequentially on the rendered configuration.

Available types:

| Type | Description |
|------|-------------|
| `regex_replace` | Line-by-line regex find/replace (`pattern` and `replace` params) |
| `template` | Scriggo template transformation with access to the rendered output via the `input` variable (`source` param) |

```yaml
haproxyConfig:
  template: |
    global
        daemon
    # ...
  postProcessing:
    - type: template
      params:
        source: |
          {%- if strings_contains(input, "__PLACEHOLDER__") -%}
          {{ replace(input, "__PLACEHOLDER__", "computed-value") }}
          {%- else -%}
          {{ input }}
          {%- end -%}
    - type: regex_replace
      params:
        pattern: "^[ ]+"
        replace: "  "
```

The `template` post-processor receives the fully rendered output as the `input` variable and has access to all standard Scriggo builtins (`regexp`, `replace`, `len`, `tostring`, etc.). Its output becomes the new rendered content.

## Template Syntax

Templates use Scriggo's template syntax. For complete syntax reference, see the [Scriggo documentation](https://scriggo.com/templates).

### Control Structures

```go
{# Loops #}
{% for _, ingress := range resources.ingresses.List() %}
  backend {{ ingress.metadata.name }}
{% end %}

{# Conditionals #}
{% if ingress.spec.tls != nil %}
  bind *:443 ssl crt {{ pathResolver.GetPath(ingress.metadata.name + ".pem", "cert") }}
{% end %}

{# Variables #}
{% var service_name = path.backend.service.name %}
{% var port = fallback(path.backend.service.port.number, 80) %}

{# Comments #}
{# This is a comment #}
```

### Helper Functions

The most commonly needed helpers when writing HAPTIC templates. All can be called as functions or via the pipe operator (`x | fn()` is equivalent to `fn(x)`).

| Helper | Purpose | Example |
|--------|---------|---------|
| `fallback(value, default)` | Return `default` if `value` is nil / empty / zero | `fallback(svc.port.number, 80)` |
| `dig(obj, "k1", "k2", ...)` | Walk a nested map without nil-checking each level | `dig(ing, "metadata", "annotations")` |
| `toSlice(v)` | Coerce `any` to `[]any` (safe to range over even if nil) | `for _, r := range toSlice(ing.spec.rules)` |
| `tostring(v)`, `toint(v)`, `tofloat(v)` | Type conversions from `any` | `port = toint(annotation)` |
| `len(v)` | Length of slice / map / string | `len(ing.spec.rules)` |
| `keys(m)` | Sorted keys of a map | `for _, k := range keys(annotations)` |
| `merge(a, b)` | New map combining `a` and `b` (b wins on conflict) | `merge(defaults, overrides)` |
| `toLower(s)` / `toUpper(s)` | Case conversion | `host = toLower(rule.host)` |
| `replace(s, old, new)`, `split(s, sep)`, `join(slice, sep)`, `trim(s)`, `hasPrefix(s, p)`, `hasSuffix(s, p)` | String operations | `join(items, ", ")` |
| `first_seen(prefix, keys...)` | Returns `true` only the first time the key tuple is seen — for deduplicating | `if first_seen("backend", svc.namespace, svc.name)` |
| `sanitize_regex(s)` | Escape regex metacharacters in user input | `sanitize_regex(annotation)` |
| `semver_gte(version, "3.3")` | Compare HAProxy version (major.minor) | `if semver_gte(haproxyVersion, "3.3")` |
| `fail(msg)` | Abort rendering with an error message (surfaces in validation tests and webhooks) | `fail("missing required annotation")` |

For complete coverage including crypto, encoding, and Scriggo built-ins (`abs`, `min`, `max`, `sprintf`, `now()`, etc.), see the [Scriggo built-ins reference](https://scriggo.com/templates/builtins).

### Path Resolution

`pathResolver` is a helper available in every template. Its `GetPath(filename, type)` method returns the path that HAProxy should use to reference an auxiliary file (map, error file, certificate, crt-list). Use it instead of writing paths by hand so the controller and HAProxy agree on where files live.

By default `GetPath` returns paths *relative* to HAProxy's `default-path` directive. The chart's `base` template library puts `default-path config` in the global section, which tells HAProxy to resolve relative paths against the directory containing `haproxy.cfg` — the same directory the controller writes maps, files, and certs to. If you replace the base library, keep that directive (or switch to `default-path origin <BaseDir>`) or the relative paths returned by `GetPath` will not resolve.

```go
{# Map files — resolves to maps/host.map #}
use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

{# General files — resolves to files/504.http #}
errorfile 504 {{ pathResolver.GetPath("504.http", "file") }}

{# SSL certificates — resolves to ssl/example.com.pem #}
bind *:443 ssl crt {{ pathResolver.GetPath("example.com.pem", "cert") }}

{# crt-list files — resolves to ssl/cert-list.txt #}
bind *:443 ssl crt-list {{ pathResolver.GetPath("cert-list.txt", "crt-list") }}
```

**Arguments**: `filename` (string), `type` (one of `"map"`, `"file"`, `"cert"`, `"crt-list"`)

### Custom Filters

| Filter | Description | Example |
|--------|-------------|---------|
| `b64decode` | Decode base64 strings | `{{ secret.data.password \| b64decode() }}` |
| `glob_match` | Filter strings by glob pattern | `{{ templateSnippets \| glob_match("backend-*") }}` |
| `group_by` | Group items by JSONPath | `{{ ingresses \| group_by("$.metadata.namespace") }}` |
| `indent` | Indent each line by N spaces | `{{ render("snippet") \| indent(4) }}` |
| `sanitize_regex` | Escape regex special characters | `{{ path \| sanitize_regex() }}` |
| `sort_by` | Sort by JSONPath expressions | `{{ routes \| sort_by(["$.priority:desc"]) }}` |
| `debug` | Output as JSON comment | `{{ routes \| debug("routes") }}` |
| `toJSON` | Convert value to JSON string | `{{ myMap \| toJSON() }}` |
| `semver_gte` | Compare HAProxy version (major.minor) | `{{ semver_gte(haproxyVersion, "3.3") }}` |

!!! note "Pipe operator requires parentheses"
    Scriggo's pipe operator requires a function call on the right side. `{{ value \| toLower }}` is a parse error; write `{{ value \| toLower() }}`. For filters that take additional arguments, the pipe passes the left-hand value as the first argument: `{{ items \| join(", ") }}` is equivalent to `{{ join(items, ", ") }}`.

**sort_by modifiers**: `:desc` (descending), `:exists` (by field presence), `| length` (by length)

**Example - Route precedence sorting**:

```go
{% var sorted = sort_by(routes, []string{
    "$.match.method:exists:desc",
    "$.match.headers | length:desc",
    "$.match.path.value | length:desc",
}) %}
```

## Available Template Data

### Context Variables

All templates have access to the following top-level variables:

| Variable | Type | Description |
|----------|------|-------------|
| `resources` | map of stores | Kubernetes resources indexed per `watchedResources` config |
| `pathResolver` | object | Resolves filenames to HAProxy paths — use `GetPath(name, type)` |
| `haproxyVersion` | string | HAProxy version string, e.g. `"3.2"` — use with `semver_gte` filter |
| `currentConfig` | string | The currently deployed HAProxy configuration — used for slot-preserving updates |
| `shared` | `*SharedContext` | Thread-safe compute-once cache for expensive computations (`shared.ComputeIfAbsent(key, factory)` + `shared.Get(key)`; no `Set` — prevents racy check-then-act patterns) |
| `templateSnippets` | list | Names of all available template snippets — useful for dynamic `render_glob` patterns |

Custom variables defined in `templatingSettings.extraContext` are also available directly by name. See [Custom Template Variables](#custom-template-variables).

### The `resources` Variable

Templates access watched resources through the `resources` variable. Each store provides `List()`, `Fetch()`, and `GetSingle()` methods.

!!! note
    The keys available under `resources.*` are determined by the `watchedResources` configuration. See [Watching Resources](./watching-resources.md) to add resource types beyond the defaults.

```go
{# List all resources #}
{% for _, ingress := range resources.ingresses.List() %}

{# Fetch by index keys (parameters match indexBy configuration) #}
{% for _, ingress := range resources.ingresses.Fetch("default", "my-ingress") %}

{# Get single resource or nil #}
{% var secret = resources.secrets.GetSingle("default", "my-secret") %}
```

### Index Configuration

The `indexBy` field determines what parameters `Fetch()` expects:

```yaml
watchedResources:
  ingresses:
    apiVersion: networking.k8s.io/v1
    resources: ingresses
    indexBy: ["metadata.namespace", "metadata.name"]
    # Fetch(namespace, name)

  endpoints:
    apiVersion: discovery.k8s.io/v1
    resources: endpointslices
    indexBy: ["metadata.labels.kubernetes\\.io/service-name"]
    # Fetch(service_name)
```

!!! tip
    Escape dots in JSONPath for labels: `kubernetes\\.io/service-name`

## Custom Template Variables

Add custom variables via `templatingSettings.extraContext`:

```yaml
spec:
  templatingSettings:
    extraContext:
      environment: production
      debug: true
      limits:
        maxConn: 10000
```

Access in templates:

```go
{% if debug %}
  http-response set-header X-Debug %[be_name]
{% end %}

global
  maxconn {{ limits.maxConn }}
```

## Common Patterns

### Reserved Server Slots (Avoid Reloads)

Pre-allocate server slots to enable runtime API updates without reloads:

```go
{%- var initial_slots = 10 %}
{%- var active_endpoints = []map[string]any{} %}

{# Collect endpoints #}
{%- for _, endpoint_slice := range resources.endpoints.Fetch(service_name) %}
  {%- for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
    {%- for _, address := range fallback(endpoint.addresses, []any{}) %}
      {%- active_endpoints = append(active_endpoints, map[string]any{"address": address, "port": port}) %}
    {%- end %}
  {%- end %}
{%- end %}

{# Fixed slots - active endpoints fill first, rest are disabled #}
{%- for i := 1; i <= initial_slots; i++ %}
  {%- if i-1 < len(active_endpoints) %}
    {%- var ep = active_endpoints[i-1] %}
server SRV_{{ i }} {{ ep["address"] }}:{{ ep["port"] }} check
  {%- else %}
server SRV_{{ i }} 127.0.0.1:1 disabled
  {%- end %}
{%- end %}
```

**Benefit**: Endpoint changes update server addresses via runtime API without dropping connections.

!!! tip "Maximize Runtime API Usage"
    Keep server lines minimal - only `address:port` plus `enabled` or `disabled`. Place all other options (`check`, `proto h2`, SSL settings) in the `default-server` directive:

    ```haproxy
    backend my-backend
        default-server check proto h2
        server SRV_1 10.0.0.1:8080 enabled
        server SRV_2 10.0.0.2:8080 enabled
        server SRV_3 127.0.0.1:1 disabled
    ```

    The Dataplane API can update Address, Port, and enabled/disabled state at runtime without reloading HAProxy. Both `enabled` and `disabled` are runtime-supported, enabling the reserved slots pattern. Options like `check` on individual server lines trigger reloads on any change.

### Cross-Resource Lookups

Use fields from one resource to query another:

```go
{% for _, ingress := range resources.ingresses.List() %}
{% for _, rule := range fallback(ingress.spec.rules, []any{}) %}
{% for _, path := range fallback(rule.http.paths, []any{}) %}
  {% var service_name = path.backend.service.name %}
  {% var port = fallback(path.backend.service.port.number, 80) %}

backend ing_{{ ingress.metadata.name }}_{{ service_name }}
    {%- for _, endpoint_slice := range resources.endpoints.Fetch(service_name) %}
    {%- for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
    {%- for _, address := range fallback(endpoint.addresses, []any{}) %}
    server {{ endpoint.targetRef.name }} {{ address }}:{{ port }} check
    {%- end %}
    {%- end %}
    {%- end %}
{% end %}
{% end %}
{% end %}
```

**Required indexing**:

```yaml
watchedResources:
  ingresses:
    indexBy: ["metadata.namespace", "metadata.name"]
  endpoints:
    indexBy: ["metadata.labels.kubernetes\\.io/service-name"]
```

### Safe Iteration

Use `fallback` function to handle missing fields:

```go
{% for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
  {% for _, address := range fallback(endpoint.addresses, []any{}) %}
    server srv {{ address }}:80
  {% end %}
{% end %}
```

### Filtering with Conditionals

Filter resources by attribute presence:

```go
{% for _, rule := range fallback(ingress.spec.rules, []any{}) %}
  {% if rule.http != nil %}
  {# rule.http is guaranteed to exist #}
  {% end %}
{% end %}
```

### Mutable Variables

Accumulate values across loop iterations using Go-style variable assignment:

```go
{% var active_endpoints = []map[string]any{} %}

{% for _, endpoint_slice := range resources.endpoints.Fetch(service_name) %}
  {% for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
    {% active_endpoints = append(active_endpoints, map[string]any{"address": endpoint.addresses[0]}) %}
  {% end %}
{% end %}

{% for i, ep := range active_endpoints %}
  server srv{{ i + 1 }} {{ ep["address"] }}:80
{% end %}
```

### Whitespace Control

```go
{%- for _, item := range items %}   {# Strip before #}
{% for _, item := range items -%}   {# Strip after #}
{%- for _, item := range items -%}  {# Strip both #}
```

## Status Patches

Templates can register status patches for Kubernetes resources using the `statusPatch()` function. The controller applies these patches to the `/status` subresource via Server-Side Apply (SSA) after each reconciliation phase.

This allows templates to report processing results back to resources (e.g., setting `Accepted` and `Programmed` conditions on Gateways, or propagating LoadBalancer addresses to Ingress status) without the controller needing to understand any specific resource's status schema.

### statusPatch()

Registers a status patch for a Kubernetes resource with outcome-keyed variants:

```go
{% statusPatch(namespace, name, apiVersion, kind, map[string]any{
    "deployed": map[string]any{
        "status": map[string]any{
            "conditions": []any{
                condition("Accepted", "True", "Accepted", "Resource accepted", generation, transitionTime(resource, "Accepted", "True")),
            },
        },
    },
    "deployFailed": map[string]any{
        "status": map[string]any{
            "conditions": []any{
                condition("Accepted", "True", "Accepted", "Resource accepted", generation, transitionTime(resource, "Accepted", "True")),
                condition("Programmed", "False", "AddressNotAssigned", "No address available", generation, transitionTime(resource, "Programmed", "False")),
            },
        },
    },
}) %}
```

**Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `namespace` | `string` | Resource namespace |
| `name` | `string` | Resource name |
| `apiVersion` | `string` | Resource API version (e.g., `networking.k8s.io/v1`) |
| `kind` | `string` | Resource kind (e.g., `Ingress`, `Gateway`) |
| `variants` | `map[string]any` | Status payloads keyed by pipeline phase |

**Variants:**

| Key | Applied When |
|-----|-------------|
| `rendered` | After successful template rendering (before deployment) |
| `deployed` | After successful HAProxy deployment |
| `renderFailed` | When a later rendering phase fails |
| `deployFailed` | When HAProxy deployment fails |

Templates render all variants upfront. The controller selects the appropriate variant based on the pipeline outcome.

### condition()

Creates a `metav1.Condition`-compatible map:

```go
{{ condition("Accepted", "True", "Accepted", "Resource is accepted", observedGeneration, lastTransitionTime) }}
```

**Parameters:** `type`, `status`, `reason`, `message`, `observedGeneration`, `lastTransitionTime`

### transitionTime()

Returns the correct `lastTransitionTime` for a condition: preserves the existing timestamp if the condition status hasn't changed, or returns the current time if it has changed or doesn't exist yet:

```go
{{ transitionTime(resource, "Accepted", "True") }}
```

For resources with nested condition arrays (e.g., Gateway API Route `parents[]`), pass the parent index:

```go
{{ transitionTime(resource, "Accepted", "True", parentIndex) }}
```

### Using Status Patches in Custom Templates

Status patch snippets should use the `status-patches-*` extension point (priority 200). This renders after feature analysis but before complex config generation, ensuring patches are captured even if later rendering fails.

```yaml
controller:
  config:
    templateSnippets:
      status-patches-200-custom:
        template: |
          {%- for _, resource := range resources.myresources.List() %}
            {%%
              var ns = resource | dig("metadata", "namespace") | fallback("") | tostring()
              var name = resource | dig("metadata", "name") | fallback("") | tostring()
              var gen = resource | dig("metadata", "generation") | fallback(0)
            %%}
            {%- statusPatch(ns, name, "example.com/v1", "MyResource", map[string]any{
                "deployed": map[string]any{
                    "status": map[string]any{
                        "conditions": []any{
                            condition("Ready", "True", "Deployed", "Successfully deployed", gen, transitionTime(resource, "Ready", "True")),
                        },
                    },
                },
            }) %}
          {%- end %}
```

The built-in Ingress and Gateway API libraries already include status patch snippets. You only need custom status patches for resources not covered by the default libraries.

## Complete Example

Full ingress → service → endpoints chain with reserved slots:

```yaml
watchedResources:
  ingresses:
    apiVersion: networking.k8s.io/v1
    resources: ingresses
    indexBy: ["metadata.namespace", "metadata.name"]
  endpoints:
    apiVersion: discovery.k8s.io/v1
    resources: endpointslices
    indexBy: ["metadata.labels.kubernetes\\.io/service-name"]

maps:
  host.map:
    template: |
      {%- for _, ingress := range resources.ingresses.List() %}
      {%- for _, rule := range fallback(ingress.spec.rules, []any{}) %}
      {{ rule.host }} ing_{{ ingress.metadata.name }}
      {%- end %}
      {%- end %}

templateSnippets:
  backend-servers:
    template: |
      {%- var initial_slots = 10 %}
      {%- var active_endpoints = []map[string]any{} %}
      {%- for _, es := range resources.endpoints.Fetch(service_name) %}
        {%- for _, ep := range fallback(es.endpoints, []any{}) %}
          {%- for _, addr := range fallback(ep.addresses, []any{}) %}
            {%- active_endpoints = append(active_endpoints, map[string]any{"addr": addr}) %}
          {%- end %}
        {%- end %}
      {%- end %}
      {%- for i := 1; i <= initial_slots; i++ %}
        {%- if i-1 < len(active_endpoints) %}
      server SRV_{{ i }} {{ active_endpoints[i-1]["addr"] }}:{{ port }} check
        {%- else %}
      server SRV_{{ i }} 127.0.0.1:1 disabled
        {%- end %}
      {%- end %}

haproxyConfig:
  template: |
    global
        daemon
        maxconn 4096

    defaults
        mode http
        timeout connect 5s
        timeout client 50s
        timeout server 50s

    frontend http
        bind *:80
        use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

    {% for _, ingress := range resources.ingresses.List() %}
    {% for _, rule := range fallback(ingress.spec.rules, []any{}) %}
    {% for _, path := range fallback(rule.http.paths, []any{}) %}
    {%- var service_name = path.backend.service.name %}
    {%- var port = fallback(path.backend.service.port.number, 80) %}

    backend ing_{{ ingress.metadata.name }}
        balance roundrobin
        {{ render "backend-servers" inherit_context }}
    {% end %}
    {% end %}
    {% end %}
```

## See Also

- [Template Engine Reference](https://gitlab.com/haproxy-haptic/haptic/blob/main/pkg/templating/README.md)
- [Scriggo Documentation](https://scriggo.com/templates)
- [HAProxy Configuration Manual](https://www.haproxy.com/documentation/haproxy-configuration-manual/latest/)
