# Watching Resources

`spec.watchedResources` tells the controller which Kubernetes resources to subscribe to and how to make them available inside templates. This page explains the mental model. For field types, defaults, and validation rules see [CRD Reference](./crd-reference.md#watchedresources).

Watch it end-to-end — a watched Ingress feeding the render:

<div class="pg-embed" markdown data-scenario="ingress" data-facade="spec.watchedResources" data-tab="haproxy.cfg" data-controls="tabs" data-title="A watched Ingress rendered to haproxy.cfg" data-height="440">

</div>

When a schema is loaded — the norm in production, where the controller fetches it live from the kube-apiserver — watched resources arrive as strongly typed values you navigate with dotted field access (`ing.spec.rules`), covered in [Typed access in templates](#typed-access-in-templates) below. For the cases where you're working with untyped data — an inline `map[string]any` literal, a schema-less custom resource, or a genuinely optional field that may be absent — navigate with `dig(...)` and supply a default with `fallback(...)` so a missing field never breaks the render. The [Templating Guide](./templating.md#safe-iteration) teaches both patterns with runnable challenges.

## Anatomy of an entry

```yaml
watchedResources:
  ingresses:                                # arbitrary key — appears in templates as resources.ingresses
    apiVersion: networking.k8s.io/v1        # GroupVersion of the watched resource
    resources: ingresses                    # plural name as it appears in the REST API
    indexBy:                                # JSONPaths that form the composite lookup key
      - metadata.namespace
      - metadata.name
    labelSelector: ""                       # "app=myapp" — string, not matchLabels object
    enableValidationWebhook: false          # include this kind in the webhook fan-out
    store: full                             # "full" (default) or "on-demand"
    debounceInterval: ""                    # Go duration string; empty / invalid uses the 2s default
```

All selector fields are plain label-selector strings — the `matchLabels`/`matchExpressions` object form that Prometheus Operator and others use is *not* accepted here.

`watchedResources` is an unbounded map — there's no maximum number of watched kinds. Each entry costs one informer and one apiserver watch stream, and a `store: full` entry holds its objects resident in memory, so the ceiling is apiserver watch capacity and controller memory, not a fixed count. See [Resource watching optimization](./operations/performance.md#resource-watching-optimization).

## Two store types

Every entry uses one of two store backends. The choice controls memory footprint and rendering latency.

### `store: full` (default) — `MemoryStore`

- Keeps the full resource object in-process after trimming fields listed in `watchedResourcesIgnoreFields`.
- `.List()`, `.Fetch(...)`, `.GetSingle(...)` all resolve from memory with no API hit.
- Right for anything the templates iterate over (Ingresses, Services, EndpointSlices, small ConfigMaps).

### `store: on-demand` — `CachedStore`

- Stores only the index keys in-process; fetches the full object lazily on `.Fetch()` / `.GetSingle()` and caches the result.
- Cache TTL is auto-derived from `dataplane.driftPreventionInterval` (× 2.2) — it's **not** a user-configurable field.
- Right for large, rarely touched resources — TLS Secrets with 20 kB certificate bodies, ConfigMaps used only for a handful of entries.
- `.List()` on a cached store forces a fetch for every reference; avoid it.

Use a mix: `store: full` for everything the templates iterate, `store: on-demand` for Secrets holding certificates or auth data.

## Typed access in templates

Every entry is exposed to templates **two equivalent ways** when a schema is loaded for the resource:

1. As a store under `resources.<key>` — `.List()`, `.Fetch(...)`, and `.GetSingle(...)` return typed pointers (`[]*resources.<key>.T` / `*resources.<key>.T`).
2. As a typed top-level global named `<key>` — a typed slice of the same shape (`[]*resources.<key>.T`).

Both surfaces share the same typed pointer; iterating either way yields `*resources.<key>.T` with strongly typed `.Metadata.Namespace`, `.Spec.X`, etc. Without a schema, both surfaces fall back to `[]any` / `map[string]any` and the chart's `dig()`-based snippets work unchanged.

The typed shape comes from the resource's OpenAPI v3 schema — fetched live from the kube-apiserver in production, or from `--schema-dir` when running offline; see [Templating — Typed Resource Access](./templating.md#typed-resource-access) for the full schema-source story and the repo's bundled `tests/schemas/` directory.

A misspelled field name in a template fails when the controller boots (or when `validate` runs against a schema-dir), not at the next reconcile. The `<key>.T` type expression also works in macro signatures, type-switch case clauses (`case *resources.<key>.T`), and slice types for sharded rendering.

See [Typed Resource Access](./templating.md#typed-resource-access) for the field-name convention, type-switch dispatch pattern, when to prefer typed vs untyped access, and the worked-example snippet.

## Indexing (`indexBy`)

The `.Fetch()` template method takes the index keys in the order you listed them. With:

```yaml
indexBy: ["metadata.namespace", "metadata.name"]
```

these are all valid:

```scriggo
{% for _, ing := range resources.ingresses.Fetch("default", "my-app") %}  {# exact match #}
{% for _, ing := range resources.ingresses.Fetch("default") %}           {# prefix: all in namespace #}
{% for _, ing := range resources.ingresses.List() %}                     {# everything #}
```

Supply fewer keys than `indexBy` defines to get a prefix scan — useful for one-to-many relationships. Always returns an empty slice (never `nil`) so templates iterate safely. When a watched object changes an indexed value, HAPTIC moves it to the new lookup key; if the updated object no longer has every indexed field, HAPTIC removes it until a later update can be indexed again.

Run the prefix scan below: three Ingresses across two namespaces, but `Fetch("shop")` returns only the two in `shop`.

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-controls="tabs,resources" data-focus="25" data-title="Prefix scan: Fetch one key of a two-key index" data-height="460">

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: indexby-demo
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      indexBy:
        - metadata.namespace
        - metadata.name
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
        default_backend unmatched
      {%- for _, ing := range resources.ingresses.Fetch("shop") %}
      backend {{ ing.metadata.name }}
        server app {{ ing.metadata.name }}.svc:80
      {%- end %}
      backend unmatched
        http-request deny deny_status 404
```

```yaml
apiVersion: v1
kind: List
items:
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: web
      namespace: shop
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: api
      namespace: shop
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: blog
      namespace: content
```

</div>

### Canonical index shapes

| Resource | `indexBy` | Why |
|----------|-----------|-----|
| Ingress / Service / ConfigMap | `["metadata.namespace", "metadata.name"]` | Standard unique lookup |
| EndpointSlice | `["metadata.labels.kubernetes\\.io/service-name"]` | One-to-many: many slices per Service |
| Secret (when sharded by type) | `["metadata.namespace", "type"]` | Group TLS vs. basic-auth vs. opaque |
| Cluster-scoped resource (Namespace, GatewayClass) | `["metadata.name"]` | No namespace to index by |

Escape dots in JSONPath keys that contain them (`labels.kubernetes\\.io/service-name`), otherwise the path parser reads the dot as a subfield separator.

### `.GetSingle()` vs `.Fetch()`

- `GetSingle(...)` returns a single object or `nil` — use when the index is unique and you want nil-safe access.
- `Fetch(...)` always returns a slice — use in `for` loops and when the index may match multiple resources.
- `List()` returns everything in the store — avoid on `on-demand` stores (fetches everything).

`GetSingle(...)` fails the render if its key matches multiple objects. Kubernetes
read errors and schema-to-typed-value conversion errors also fail the render;
they never turn into an empty resource set. A missing object remains `nil` or an
empty slice.

## Narrowing the watch

Two filters narrow what actually lands in the store:

- `labelSelector:` — equality-only label-selector string applied to the resource itself (`"app=myapp"` or `"app=nginx,env=prod"`). Comma-separated `key=value` pairs only; set-based syntax (`"tier in (frontend,api)"`, `"!disabled"`) **isn't** supported — `pkg/controller/conversion.parseLabelSelector` splits on `,` and `=`, dropping anything else.
- `fieldSelector:` — a client-side JSONPath equality filter applied *after* the list is fetched (format `"field.path=value"`, for example `"spec.ingressClassName=haproxy"`). Unlike Kubernetes' native field selectors it can target **any** field, not just the server-supported ones, because the watcher evaluates it itself (at the cost of fetching the full list first). A resource that stops matching is handled as a delete; one that starts matching, as an add. This is what the bundled ingress / gateway libraries use to scope by `ingressClassName` / `gatewayClassName`. To pin a watch to a single namespace, filter on `"metadata.namespace=<ns>"`.

Watch the filter in action: two Ingresses reach the playground, but only the `haptic`-class one survives the `fieldSelector` and reaches a backend.

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-controls="tabs,resources" data-focus="10" data-title="fieldSelector scopes the watch by ingress class" data-height="460">

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: fieldselector-demo
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      fieldSelector: "spec.ingressClassName=haptic"
      indexBy:
        - metadata.namespace
        - metadata.name
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
        default_backend unmatched
      {%- for _, ing := range resources.ingresses.List() %}
      backend {{ ing.metadata.name }}
        server app {{ ing.metadata.name }}.svc:80
      {%- end %}
      backend unmatched
        http-request deny deny_status 404
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
    spec:
      ingressClassName: haptic
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: legacy
      namespace: default
    spec:
      ingressClassName: nginx
```

</div>

Need to scope by namespace *labels* rather than a single name? Watch the `namespaces` resource and gate inside the template, or run separate controller instances per scope.

## Trimming fields

`spec.watchedResourcesIgnoreFields` drops noisy subtrees before they're indexed, cutting memory use. Reasonable defaults:

```yaml
watchedResourcesIgnoreFields:
  - metadata.managedFields
  - metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']
```

Applies uniformly to every watched-resource store. Fields that are referenced by `indexBy` must not be trimmed.

## HTTP Resources

Templates can fetch arbitrary HTTP content via the `http.Fetch(url, opts, auth)` template function — a separate mechanism from Kubernetes watching. The controller auto-registers any URL that appears in an `http.Fetch()` call during template rendering, periodically re-checks it at a per-URL `interval`, and surfaces the cached body back to the template on the next render. The first fetch is synchronous — `interval` only sets how often the content is re-checked afterwards, and the re-check is conditional (`If-None-Match` / `If-Modified-Since`), so unchanged content costs one 304 and triggers no re-render. `Fetch` returns the response body as a string.

### Fetch parameters

The second argument is an options map. All keys are optional:

| Key | Type | Default | Effect |
|-----|------|---------|--------|
| `interval` | Go duration string | none | How often the content is re-checked. The first fetch is synchronous regardless — this only governs what happens afterwards. Omit it (or set `"0"`) to fetch once and never re-check. Also accepted as `delay`, the original spelling, which reads like a wait before fetching and never was one; set one or the other, not both. |
| `timeout` | Go duration string | `30s` | Per-request timeout. |
| `retries` | integer | 2 | Retry attempts on a failed request, with a growing delay between attempts. |
| `critical` | boolean | `false` | Failure mode. With `false`, a failed fetch returns an empty string and rendering continues (a warning is logged). With `true`, a failed fetch aborts the render with an error, like [`fail()`](./template-reference.md#functions-and-filters). |

Set `critical: true` only when an empty body would produce a dangerously wrong config (for example, a security blocklist that must not silently become empty); leave it `false` when a stale-or-empty body is safer than blocking every render on one unreachable URL.

A third optional argument supplies authentication: `{"type": "bearer", "token": "..."}`, `{"type": "basic", "username": "...", "password": "..."}`, or `{"type": "header", "headers": {"X-API-Key": "..."}}`.

Response bodies are capped at 10 MiB. A larger response fails the fetch with `response body exceeds maximum size of N bytes` — it isn't truncated — and the limit is fixed with no per-call override. A failed fetch is then handled per the `critical` setting above.

### Example

This backend denies any client IP listed in a remotely hosted blocklist. The list refreshes every 5 minutes; `critical: false` keeps a transient fetch failure from taking down the whole render:

```yaml
spec:
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend web
        bind :80
        {%- var blocklist = http.Fetch("https://example.com/ip-blocklist.txt", map[string]any{"interval": "5m", "critical": false}) %}
        {%- for _, ip := range split(tostring(blocklist), "\n") %}
        {%- if strip(ip) != "" %}
        http-request deny if { src {{ strip(ip) }} }
        {%- end %}
        {%- end %}
        default_backend app
      backend app
        server s1 10.0.0.1:8080 check
```

The playground can't reach external URLs, so this example doesn't run there — deploy it to a cluster to see the fetched content. For fixture-based mocking during validation tests, set per-test `httpResources` directly on the test (`spec.validationTests[].httpResources`, sibling to `fixtures` — not nested inside it); see [CRD Reference](./crd-reference.md). There is no top-level `spec.httpResources` field.

## Validating webhook scope

Setting `enableValidationWebhook: true` on an entry registers that kind with the admission webhook, so creates and updates are rendered against overlay stores before being accepted. The controller maps the request's Kubernetes group, version, and kind back to every configured `watchedResources` key for that group, version, and resource tuple. The map key can differ from the Kubernetes plural, and multiple filtered aliases can watch the same tuple. Each alias receives the same selector transition its watcher applies: entering a selector adds the object, leaving it removes the object, and remaining inside updates it.

Set the flag only on the kinds you want validated in-band. The default is off to avoid dragging unrelated kinds, such as EndpointSlice churn, through the webhook path. If any alias for a group, version, and resource tuple enables validation, the proposed object is overlaid on every alias for that tuple because all of those stores observe the same API write.

## Debounce override

Each watcher uses a leading-edge refractory window to coalesce bursts of changes into a single store-update event before forwarding to the Reconciler. This is the only debounce layer — the Reconciler itself fires immediately on every event, and reload throttling lives in the deployer's `minDeploymentInterval` (see the [architecture overview](./development/design/architecture-overview.md)). The default is 2 seconds (set in `pkg/k8s/types.DefaultDebounceInterval`) and works well for most workloads. Override per-resource via `debounceInterval` (the bundled chart sets it to `"0"` on EndpointSlice so pod-IP rotations react instantly during rolling restarts):

```yaml
watchedResources:
  httproutes:
    apiVersion: gateway.networking.k8s.io/v1
    resources: httproutes
    indexBy: ["metadata.namespace", "metadata.name"]
    debounceInterval: "500ms"   # react fast on canary rollouts
  endpointslices:
    apiVersion: discovery.k8s.io/v1
    resources: endpointslices
    indexBy: ["metadata.namespace", "metadata.labels.kubernetes\\.io/service-name"]
    debounceInterval: "30s"     # absorb endpoint churn on large clusters
```

Empty / invalid strings fall back to the `2s` default silently — the validating webhook doesn't reject unparseable values, so a typo just leaves you with the default. Format is any Go duration string (`"500ms"`, `"10s"`, `"1m30s"`, …); `"0"` disables debouncing so every change fires immediately.

## Troubleshooting

| Symptom | Likely cause |
|---------|--------------|
| `.List()` returns empty | Controller hasn't finished initial sync — check `haptic_reconciliation_total` or `kubectl logs … \| grep "initial sync"` |
| `.Fetch(ns, name)` returns empty for a resource that exists | `indexBy` doesn't match what you passed, or `labelSelector` / `fieldSelector` is filtering it out |
| OOMKilled on controller | Switch large resources (TLS Secrets, big ConfigMaps) to `store: on-demand`; add `watchedResourcesIgnoreFields` entries |
| Template rendering slow, many API logs | You're calling `.List()` on an `on-demand` store, or `.Fetch()` consistently missing the cache — profile with `/debug/pprof/profile`, consider `store: full` if the total size is modest |
| `kubectl apply` rejected with `a HAProxyTemplateConfig needs podSelector, at least one watchedResources entry, and haproxyConfig …` | The CRD's validation rule requires `podSelector`, at least one `watchedResources` entry, and a `haproxyConfig` — inline or from a `spec.libraryRefs` entry; see [CRD Reference](./crd-reference.md) |

## See also

- [Bring-your-own-CRD example](https://gitlab.com/haproxy-haptic/haptic/-/tree/main/examples/byo-crd) — a runnable, self-validating example: watch a custom CRD, route on it, and write status back, with no Go
- [CRD Reference](./crd-reference.md#watchedresources) — field-level documentation
- [Templating Guide — The `resources` Variable](./templating.md#the-resources-variable) — `.List()` / `.Fetch()` / `.GetSingle()` semantics from the template side
- [Performance](./operations/performance.md) — deciding when to narrow the watch versus scale the controller
- `pkg/k8s` README — store implementation details
