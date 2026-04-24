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
    namespaceSelector: ""                   # "env=watched" — applies to namespace labels
    enableValidationWebhook: false          # include this kind in the webhook fan-out
    store: full                             # "full" (default) or "on-demand"
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

Three independent filters narrow what actually lands in the store:

- `namespace:` — hard pin to a single namespace. Drops the need for `metadata.namespace` in `indexBy`.
- `labelSelector:` — standard label-selector string applied to the resource itself (`"app=myapp"`, `"tier in (frontend,api)"`).
- `namespaceSelector:` — applied to namespace *labels*. Use this to watch a dynamic set of namespaces (`env=prod`) without cluster-wide RBAC on individual objects.

## Trimming Fields

`spec.watchedResourcesIgnoreFields` drops noisy subtrees before they're indexed, cutting memory use. Reasonable defaults:

```yaml
watchedResourcesIgnoreFields:
  - metadata.managedFields
  - metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']
```

Applies uniformly to every watched-resource store. Fields that are referenced by `indexBy` must not be trimmed.

## HTTP Resources

`spec.httpResources` is a separate mechanism that doesn't involve Kubernetes watches — the controller periodically fetches HTTP URLs and exposes the content through the `http.Fetch()` template function. See [Templating Guide — HTTP Resources](./templating.md) and the CRD reference for schedule and caching knobs.

## Validating Webhook Scope

Setting `enableValidationWebhook: true` on an entry registers that kind with the admission webhook, so creates/updates are rendered against an overlay store before being accepted. Set it only on the kinds you actually want validated in-band — the default is off to avoid dragging unrelated kinds (e.g. EndpointSlice churn) through the webhook path.

## Troubleshooting

| Symptom | Likely cause |
|---------|--------------|
| `.List()` returns empty | Controller hasn't finished initial sync — check `haptic_reconciliation_total` or `kubectl logs … \| grep "initial sync"` |
| `.Fetch(ns, name)` returns empty for a resource that exists | `indexBy` doesn't match what you passed, or `labelSelector`/`namespaceSelector` is filtering it out |
| OOMKilled on controller | Switch large resources (TLS Secrets, big ConfigMaps) to `store: on-demand`; add `watchedResourcesIgnoreFields` entries |
| Template rendering slow, many API logs | You're calling `.List()` on an `on-demand` store, or `.Fetch()` consistently missing the cache — profile with `/debug/pprof/profile`, consider `store: full` if the total size is modest |
| `kubectl apply` on CRD rejected with "watchedResources must be non-empty" | The CRD schema requires at least one entry; see [CRD Reference](./crd-reference.md) |

## See Also

- [CRD Reference](./crd-reference.md#watchedresources-required) — field-level documentation
- [Templating Guide — The `resources` Variable](./templating.md#the-resources-variable) — `.List()` / `.Fetch()` / `.GetSingle()` semantics from the template side
- [Performance](./operations/performance.md) — deciding when to narrow the watch versus scale the controller
- `pkg/k8s` README — store implementation details
