# Watching Resources

`spec.watchedResources` tells the controller which Kubernetes resources to subscribe to and how to make them available inside templates. This page explains the mental model. For field types, defaults, and validation rules see [CRD Reference](./crd-reference.md#watchedresources-required).

## Anatomy of an Entry

```yaml
watchedResources:
  ingresses:                                # arbitrary key — appears in templates as resources.ingresses
    apiVersion: networking.k8s.io/v1        # GroupVersion of the watched resource
    resources: ingresses                    # plural name as it appears in the REST API
    indexBy:                                # JSONPaths that form the composite lookup key
      - metadata.namespace
      - metadata.name
    namespace: ""                           # pin to one namespace, or leave empty to watch cluster-wide
    labelSelector: ""                       # "app=myapp" — string, not matchLabels object
    enableValidationWebhook: false          # include this kind in the webhook fan-out
    store: full                             # "full" (default) or "on-demand"
    debounceInterval: ""                    # Go duration string; empty / invalid uses the 1s default
```

All selector fields are plain label-selector strings — the `matchLabels`/`matchExpressions` object form that Prometheus Operator and others use is *not* accepted here.

## Two Store Types

Every entry uses one of two store backends. The choice controls memory footprint and rendering latency.

### `store: full` (default) — MemoryStore

- Keeps the full resource object in-process after trimming fields listed in `watchedResourcesIgnoreFields`.
- `.List()`, `.Fetch(...)`, `.GetSingle(...)` all resolve from memory with no API hit.
- Right for anything the templates iterate over (Ingresses, Services, EndpointSlices, small ConfigMaps).

### `store: on-demand` — CachedStore

- Stores only the index keys in-process; fetches the full object lazily on `.Fetch()` / `.GetSingle()` and caches the result.
- Cache TTL is auto-derived from `dataplane.driftPreventionInterval` (× 2.2) — it's **not** a user-configurable field.
- Right for large, rarely-touched resources — TLS Secrets with 20 kB certificate bodies, ConfigMaps used only for a handful of entries.
- `.List()` on a cached store forces a fetch for every reference; avoid it.

Use a mix: `store: full` for everything the templates iterate, `store: on-demand` for Secrets holding certificates or auth data.

## Typed Access in Templates

Every entry is exposed to templates **two ways**:

1. As a store under `resources.<key>` — `.List()` / `.Fetch(...)` / `.GetSingle(...)` over untyped maps. Works for any watched resource regardless of schema availability.
2. As a typed top-level global named `<key>` — a slice of the resource's strongly-typed struct, with field access via `.Metadata.Namespace`, `.Spec.X`, etc.

The typed shape comes from the resource's OpenAPI v3 schema. In production the controller fetches schemas live from the kube-apiserver. Offline (`controller validate`), schemas come from `--schema-dir` / `HAPTIC_SCHEMA_DIR`.

A misspelled field name in a template fails when the controller boots (or when `validate` runs against a schema-dir), not at the next reconcile.

See [Typed Top-Level Globals](./templating.md#typed-top-level-globals) for the field-name convention, when to prefer typed vs untyped access, and the worked-example snippet.

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

Supply fewer keys than `indexBy` defines to get a prefix scan — useful for one-to-many relationships. Always returns an empty slice (never `nil`) so templates iterate safely.

### Canonical index shapes

| Resource | `indexBy` | Why |
|----------|-----------|-----|
| Ingress / Service / ConfigMap | `["metadata.namespace", "metadata.name"]` | Standard unique lookup |
| EndpointSlice | `["metadata.labels.kubernetes\\.io/service-name"]` | One-to-many: many slices per Service |
| Secret (when sharded by type) | `["metadata.namespace", "type"]` | Group TLS vs. basic-auth vs. opaque |
| Namespace-pinned resource | `["metadata.name"]` | `namespace:` already narrows the watch |

Escape dots in JSONPath keys that contain them (`labels.kubernetes\\.io/service-name`), otherwise the path parser reads the dot as a subfield separator.

### `.GetSingle()` vs `.Fetch()`

- `GetSingle(...)` returns a single object or `nil` — use when the index is unique and you want nil-safe access.
- `Fetch(...)` always returns a slice — use in `for` loops and when the index may match multiple resources.
- `List()` returns everything in the store — avoid on `on-demand` stores (fetches everything).

## Narrowing the Watch

Two filters narrow what actually lands in the store:

- `namespace:` — hard pin to a single namespace. Drops the need for `metadata.namespace` in `indexBy`.
- `labelSelector:` — equality-only label-selector string applied to the resource itself (`"app=myapp"` or `"app=nginx,env=prod"`). Comma-separated `key=value` pairs only; set-based syntax (`"tier in (frontend,api)"`, `"!disabled"`) is **not** supported — `pkg/controller/conversion.parseLabelSelector` splits on `,` and `=`, dropping anything else.

Need to scope by namespace *labels* rather than a single name? Watch the `namespaces` resource and gate inside the template, or run separate controller instances per scope.

## Trimming Fields

`spec.watchedResourcesIgnoreFields` drops noisy subtrees before they're indexed, cutting memory use. Reasonable defaults:

```yaml
watchedResourcesIgnoreFields:
  - metadata.managedFields
  - metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']
```

Applies uniformly to every watched-resource store. Fields that are referenced by `indexBy` must not be trimmed.

## HTTP Resources

Templates can fetch arbitrary HTTP content via the `http.Fetch(url, opts)` template function — a separate mechanism from Kubernetes watching. The controller auto-registers any URL that appears in an `http.Fetch()` call during template rendering, periodically refreshes it at a per-URL `delay`, and surfaces the cached body back to the template on the next render.

For fixture-based mocking during validation tests, set per-test `httpResources` directly on the test (`spec.validationTests[].httpResources`, sibling to `fixtures` — not nested inside it); see [CRD Reference](./crd-reference.md). There is no top-level `spec.httpResources` field.

## Validating Webhook Scope

Setting `enableValidationWebhook: true` on an entry registers that kind with the admission webhook, so creates/updates are rendered against an overlay store before being accepted. Set it only on the kinds you actually want validated in-band — the default is off to avoid dragging unrelated kinds (e.g. EndpointSlice churn) through the webhook path.

## Debounce Override

Each watcher uses a leading-edge refractory window to coalesce bursts of changes into a single store-update event before forwarding to the Reconciler (which has its own separate, CRD-configurable debounce on top via `spec.controller.reconciliationDebounceInterval` — see [architecture-overview](./development/design/architecture-overview.md) for the two-layer flow). The default is 1 second (set in `pkg/k8s/types.DefaultDebounceInterval`) and works well for most workloads. Override per-resource via `debounceInterval`:

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

Empty / invalid strings fall back to the 5s default silently — the validating webhook does not reject unparseable values, so a typo just leaves you with the default. Format is any Go duration string (`"500ms"`, `"10s"`, `"1m30s"`, …).

## Troubleshooting

| Symptom | Likely cause |
|---------|--------------|
| `.List()` returns empty | Controller hasn't finished initial sync — check `haptic_reconciliation_total` or `kubectl logs … \| grep "initial sync"` |
| `.Fetch(ns, name)` returns empty for a resource that exists | `indexBy` doesn't match what you passed, or `labelSelector` / `namespace:` is filtering it out |
| OOMKilled on controller | Switch large resources (TLS Secrets, big ConfigMaps) to `store: on-demand`; add `watchedResourcesIgnoreFields` entries |
| Template rendering slow, many API logs | You're calling `.List()` on an `on-demand` store, or `.Fetch()` consistently missing the cache — profile with `/debug/pprof/profile`, consider `store: full` if the total size is modest |
| `kubectl apply` on CRD rejected with "watchedResources must be non-empty" | The CRD schema requires at least one entry; see [CRD Reference](./crd-reference.md) |

## See Also

- [CRD Reference](./crd-reference.md#watchedresources-required) — field-level documentation
- [Templating Guide — The `resources` Variable](./templating.md#the-resources-variable) — `.List()` / `.Fetch()` / `.GetSingle()` semantics from the template side
- [Performance](./operations/performance.md) — deciding when to narrow the watch versus scale the controller
- `pkg/k8s` README — store implementation details
