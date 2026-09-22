---
search:
  boost: 2
---

# Read resources in templates

Use watched Kubernetes resources to decide what your templates generate.
These examples show typed field access, filtering, and lookups across resource
types. To add or change a watch, use [Watch resources](watching-resources.md).
For loops and conditionals, start with [template syntax](template-language.md).

<a id="available-template-data"></a>
<a id="context-variables"></a>

## The `resources` variable

Each `watchedResources` key becomes a store under `resources`. For example,
the `ingresses` watch is available as `resources.ingresses`. Choose a method
based on what you need:

| Method | Result |
| --- | --- |
| `List()` | All objects in the watch |
| `Fetch(keys...)` | Objects whose index starts with those keys |
| `GetSingle(keys...)` | One matching object, or `nil` if none exists; multiple matches fail the render |
| `APIVersion()` | The API group/version the watch uses |

For a watch indexed by namespace and name:

```scriggo
{% for _, ingress := range resources.ingresses.Fetch("default") %}
# {{ ingress.metadata.name }}
{% end %}
```

This lists Ingresses in `default`. Use `Fetch("default", "my-app")` for that
specific Ingress. See [watch indexing](watching-resources.md#indexing-indexby)
for the configuration and matching rules.

## Typed resource access

Read fields using their names in Kubernetes YAML, such as `ingress.metadata.name`
or `ingress.spec.rules`. HAPTIC checks those names against the resource's schema,
so a misspelled field fails template compilation.

```scriggo
{% for _, gateway := range resources.gateways.List() %}
# {{ gateway.metadata.namespace }}/{{ gateway.metadata.name }}: {{ len(gateway.spec.listeners) }} listeners
{% end %}
```

The controller loads schemas from the Kubernetes API server. For offline
validation, pass [`--schema-dir`](validation-tests.md#running-tests). The live
examples in these docs include schemas for the bundled resource types.
For resources without a schema, use `dig()` as shown in [safe iteration](#safe-iteration).

The compiler also accepts generated Go field names such as `Metadata.Name`.
Use the [typed-resource reference](template-reference.md#typed-resource-types)
when declaring macro parameters or passing resources through an `any` value.

## Collection pipelines

Chain helpers to filter, flatten, or remove duplicates from resources. This example collects
unique addresses from EndpointSlices whose endpoints name a target pod:

```scriggo
{%%
  var addresses = resources.endpoints.List() |
    flat_map(slice => slice.endpoints) |
    reject(endpoint => endpoint.targetRef.name == "") |
    flat_map(endpoint => endpoint.addresses) |
    unique()
%%}
```

The values keep their types between stages, so field access still works and
misspelled names fail compilation. `map` produces one result per input;
`flat_map` combines the slices each call returns. Use `filter` to keep matching
items or `reject` to remove them. See the [collection helper reference](template-reference.md#collection-pipelines)
for grouping and sorting.

### `x => expr`

`slice => slice.endpoints` is a function of one argument. HAPTIC infers its input
and result types. Use `func` when the body needs more than one expression:

```scriggo
{% var names = pods | map(func(p *resources.pods.T) string {
    if p.metadata.labels["app"] != "" { return p.metadata.labels["app"] }
    return p.metadata.name
  }) %}
```

For a multiline chain, use `{%% %%}` and put each pipe at the end of its line.
`{{ }}` expressions can't span lines. Use an explicit loop when you need
`break`, to register a file, or to record an Event.

<a id="asking-whether-an-optional-field-was-set"></a>

### Handle optional fields

Range an optional typed slice directly; an absent slice produces no iterations:

```scriggo
{% for _, rule := range ingress.spec.rules %}
# {{ rule.host }}
{% end %}
```

Don't wrap a typed slice in `fallback(slice, []any{})`: that loses the element
type and prevents typed field access in the loop. Use `len(slice) > 0` to check
whether it contains entries.

For an optional object, use `if` to check whether any of its fields are set:

```scriggo
{% if ingress.spec.defaultBackend.service %}
# Default backend: {{ ingress.spec.defaultBackend.service.name }}
{% end %}
```

An absent object and an explicitly empty object both have the zero value of
the generated struct. Typed access doesn't distinguish them. Use `not`, `and`,
and `or` for conditions involving these objects; Go's `!`, `&&`, and `||`
operators require Boolean values.

`dig()` returns `nil` for zero values in optional typed fields, allowing
`fallback()` to supply a default. It preserves zero values in required fields.
For an untyped map, a missing key returns `nil`, while an explicit empty value
remains empty.

<a id="index-configuration"></a>

To choose which fields identify a resource, configure
[`indexBy`](watching-resources.md#indexing-indexby) on its watch.

## Common patterns

### Reading a custom annotation

Custom annotations are the usual way to let application teams opt individual Ingresses into behavior your templates control, without a controller fork or a new release. Read the annotation off the resource and branch on its value.

The config below defines the `haptic.example.com/balance` annotation: when an Ingress carries it, its backend uses that load-balancing algorithm; otherwise it falls back to `roundrobin`. The `shop` Ingress sets `leastconn`; `blog` sets nothing. Run it, then edit either Ingress's annotation in the **Resources** panel and watch the `balance` line follow.

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="A custom annotation drives the balance algorithm" data-height="480">

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: custom-annotation-demo
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      indexBy: ["metadata.namespace", "metadata.name"]
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      {%- for _, ingress := range resources.ingresses.List() %}
      backend {{ ingress.metadata.name }}
        {%- var algo = ingress.metadata.annotations["haptic.example.com/balance"] %}
        {%- if algo != "" && algo != "roundrobin" && algo != "leastconn" %}
        {%- fail("haptic.example.com/balance must be roundrobin or leastconn") %}
        {%- end %}
        {%- if algo != "" %}
        balance {{ algo }}
        {%- else %}
        balance roundrobin
        {%- end %}
        server app 127.0.0.1:8080 check
      {%- end %}
```

```yaml
apiVersion: v1
kind: List
items:
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: shop
      namespace: default
      annotations:
        haptic.example.com/balance: leastconn
    spec:
      rules:
        - host: shop.example.com
          http:
            paths:
              - path: /
                pathType: Prefix
                backend:
                  service:
                    name: shop
                    port:
                      number: 80
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: blog
      namespace: default
    spec:
      rules:
        - host: blog.example.com
          http:
            paths:
              - path: /
                pathType: Prefix
                backend:
                  service:
                    name: blog
                    port:
                      number: 80
```

</div>

`ingress.metadata.annotations` is a typed `map[string]string`, so indexing an absent key returns `""` — the `algo != ""` check covers both a missing annotation and an empty one. Pick an annotation prefix you own (here `haptic.example.com/`) so it can't collide with another controller's. The same read-and-branch pattern drives rate limits, header rewrites, custom ACLs — anything HAProxy can express. In the chart, place the snippet under a `features-*` or `backend-directives-*` extension point so the bundled libraries pick it up (see [Template Libraries](template-libraries.md#injecting-custom-configuration)).

### Servers named after their pods (avoid reloads)

This loop emits one server line per endpoint. Add an endpoint and run it again:

<div class="pg-embed" markdown data-scriggo data-title="Servers named after pods" data-height="360">

```go
{%- var active_endpoints = []any{
    map[string]any{"pod": "echo-pod-1", "address": "10.244.1.10", "port": 8080},
    map[string]any{"pod": "echo-pod-2", "address": "10.244.2.11", "port": 8080},
} %}
default-server check
{%- for _, ep := range active_endpoints %}
server {{ ep["pod"] }} {{ ep["address"] }}:{{ ep["port"] }}
{%- end %}
```

</div>

Plain text demonstrates the output but doesn't describe runtime operations to
HAPTIC. For reload-free updates, use the bundled `BackendServers` and `Backend`
macros: they record server identities and options as well as emitting text.
See [Reload-free routing](libraries/reload-free.md).

Put shared server options on `default-server` to avoid repeating them. HAPTIC
copies those options into runtime server-creation commands because HAProxy
doesn't inherit them during `add server`. Changing an existing server's options,
such as `check` or `proto`, still requires a reload; address, port, weight, and
maintenance-state changes can use the Runtime API.

### Cross-Resource Lookups

Use an Ingress's namespace and backend Service name to find its EndpointSlices.
Both keys matter: different namespaces can have Services with the same name.
This example prints the matching addresses as comments in the output; the
[Ingress library](libraries/ingress.md) handles production backend generation.

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Ingress → EndpointSlice lookup" data-height="460">

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: cross-resource-demo
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      indexBy: ["metadata.namespace", "metadata.name"]
    endpoints:
      apiVersion: discovery.k8s.io/v1
      resources: endpointslices
      indexBy: ["metadata.namespace", "metadata.labels.kubernetes\\.io/service-name"]
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      {%- for _, ing := range resources.ingresses.List() %}
      {%- for _, rule := range ing.spec.rules %}
      {%- for _, path := range rule.http.paths %}
      {%- var svc = path.backend.service.name %}
      # Ingress {{ ing.metadata.namespace }}/{{ ing.metadata.name }}, Service {{ svc }}
        {%- for _, es := range resources.endpoints.Fetch(ing.metadata.namespace, svc) %}
        {%- for _, ep := range es.endpoints %}
        {%- for _, addr := range ep.addresses %}
        # Endpoint address: {{ addr }}
        {%- end %}
        {%- end %}
        {%- end %}
      {%- end %}
      {%- end %}
      {%- end %}
```

```yaml
apiVersion: v1
kind: List
items:
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: shop
      namespace: storefront
    spec:
      rules:
        - host: shop.example.com
          http:
            paths:
              - path: /
                pathType: Prefix
                backend:
                  service:
                    name: shop
                    port:
                      number: 80
  - apiVersion: discovery.k8s.io/v1
    kind: EndpointSlice
    metadata:
      name: shop-a1b2
      namespace: storefront
      labels:
        kubernetes.io/service-name: shop
    addressType: IPv4
    endpoints:
      - addresses: [10.244.1.10]
        targetRef: {name: shop-pod-1}
        conditions: {ready: true}
      - addresses: [10.244.2.11]
        targetRef: {name: shop-pod-2}
        conditions: {ready: true}
```

</div>

`Fetch(namespace, serviceName)` returns the EndpointSlices for that Service in
that namespace. The argument order matches `indexBy`; dots in label keys need
escaping. See [watch indexing](./watching-resources.md#indexing-indexby).

### Safe Iteration

For an untyped list, use `dig()` to read the field and `toSlice()` to make it
safe to range over when absent. The second endpoint below has no `addresses`,
so it produces no server line. For typed resources, [range the slice directly](#handle-optional-fields).

<div class="pg-embed" markdown data-scriggo data-title="Safe iteration over missing fields" data-height="320">

```go
{# dig()+toSlice() never panics on a missing field, so the endpoint with
   no addresses is skipped instead of breaking the render. #}
{%- var endpoints = []any{
    map[string]any{"addresses": []any{"10.0.0.1"}},
    map[string]any{},
} %}
{%- for _, ep := range endpoints %}
{%- for _, addr := range ep | dig("addresses") | toSlice() %}
server srv {{ addr }}:80
{%- end %}
{%- end %}
```

</div>

### Filtering with conditionals

Test a field before you use it to skip resources that lack it. Only the map with an `http` field produces a backend line:

<div class="pg-embed" markdown data-scriggo data-title="Filter by field presence" data-height="320">

```go
{# Only rules that have an http section become backends. #}
{%- var rules = []any{
    map[string]any{"host": "web.example.com", "http": map[string]any{"paths": []any{}}},
    map[string]any{"host": "tcp.example.com"},
} %}
{%- for _, rule := range rules %}
{%- if dig(rule, "http") != nil %}
backend {{ dig(rule, "host") | tostring() }}
{%- end %}
{%- end %}
```

</div>

### Challenge: Add health checks

Put the loop-and-`dig` pattern to work:

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-focus="13-16" data-title="Challenge: give every server a health check" data-difficulty="1">

<p class="pg-task" markdown>This config renders two backends from an inline list, but the generated `server` lines have no active health checks. HAProxy can't use a failed health check to remove an unavailable server. Add `check` to the generated `server` line so every server gets an active health check.</p>

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: health-check-demo
spec:
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend http
        bind *:80
        default_backend web
      {%- var backends = []any{
        map[string]any{"name": "web", "servers": []any{"10.0.0.1:8080", "10.0.0.2:8080"}},
        map[string]any{"name": "api", "servers": []any{"10.0.1.5:9000"}},
      } %}
      {%- for _, be := range backends %}
      backend {{ be | dig("name") | tostring() }}
      {%- for i, addr := range be | dig("servers") | toSlice() %}
        server srv{{ i }} {{ addr | tostring() }}
      {%- end %}
      {%- end %}
```

<details class="pg-solution" markdown>
<summary>Peek at the solution</summary>

Append `check` to the `server` line inside the loop so HAProxy health-checks each pod and stops sending traffic to unhealthy ones.

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: health-check-demo
spec:
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend http
        bind *:80
        default_backend web
      {%- var backends = []any{
        map[string]any{"name": "web", "servers": []any{"10.0.0.1:8080", "10.0.0.2:8080"}},
        map[string]any{"name": "api", "servers": []any{"10.0.1.5:9000"}},
      } %}
      {%- for _, be := range backends %}
      backend {{ be | dig("name") | tostring() }}
      {%- for i, addr := range be | dig("servers") | toSlice() %}
        server srv{{ i }} {{ addr | tostring() }} check
      {%- end %}
      {%- end %}
```

</details>

</div>

### Challenge: Default a missing port

Combine `dig()` with `fallback()` to supply a default when a field is absent:

<div class="pg-embed" markdown data-scriggo data-title="Challenge: default a missing port to 80" data-difficulty="2" data-height="380">

<p class="pg-task" markdown>One service omits `spec.port`; give every `server` line a port, defaulting to 80 when the field is absent.</p>

```go
{%- var services = []any{
    map[string]any{"name": "api",   "spec": map[string]any{"port": 8080}},
    map[string]any{"name": "web",   "spec": map[string]any{"port": 3000}},
    map[string]any{"name": "cache", "spec": map[string]any{}},
} -%}
{% for _, svc := range services -%}
{%- var name = svc | dig("name") | fallback("") -%}
{#- TODO: cache has no spec.port — dig() returns nil and the port comes out blank -#}
{%- var port = svc | dig("spec", "port") -%}
server {{ name }} {{ name }}.svc:{{ port }}
{% end -%}
```

<details class="pg-solution" markdown>
<summary>Peek at the solution</summary>

Keep the raw `dig` result, pipe it through `fallback(80)`, and use a `nil` check to flag the line that was defaulted.

```go
{%- var services = []any{
    map[string]any{"name": "api",   "spec": map[string]any{"port": 8080}},
    map[string]any{"name": "web",   "spec": map[string]any{"port": 3000}},
    map[string]any{"name": "cache", "spec": map[string]any{}},
} -%}
{% for _, svc := range services -%}
{%- var name = svc | dig("name") | fallback("") -%}
{%- var portVal = svc | dig("spec", "port") -%}
{%- var port = portVal | fallback(80) -%}
server {{ name }} {{ name }}.svc:{{ port }}{% if portVal == nil %}  # default port{% end %}
{% end -%}
```

</details>

</div>
