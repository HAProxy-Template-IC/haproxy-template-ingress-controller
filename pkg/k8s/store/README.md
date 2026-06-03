# pkg/k8s/store

Two concrete implementations of `types.Store`: `MemoryStore` (holds full resources in memory) and `CachedStore` (holds index references and fetches on demand). Both are thread-safe and support composite-key indexing with prefix-scan lookups.

For the user-facing framing (when to pick which, template-side `List`/`Fetch`/`GetSingle` semantics) see [`docs/controller/docs/watching-resources.md`](../../../docs/controller/docs/watching-resources.md). This README covers the Go API.

## Interface

Both stores satisfy `pkg/k8s/types.Store`:

```go
type Store interface {
    Get(keys ...string) ([]any, error)    // exact match, or prefix scan if len(keys) < numKeys
    List() ([]any, error)                 // everything in the store
    Add(resource any, keys []string) error
    Update(resource any, keys []string) error
    Delete(keys ...string) error
    Clear() error
}
```

The number of keys is fixed at construction time — it comes from the `indexBy` JSONPath list. Passing fewer keys to `Get` does a prefix scan that returns every resource whose composite key starts with those values (useful for one-to-many relationships like "all EndpointSlices for service X").

## MemoryStore

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"

mem := store.NewMemoryStore(2)          // 2-key composite index (e.g. namespace + name)
mem.Add(ingress, []string{"default", "my-ingress"})
ingresses, _ := mem.Get("default", "my-ingress") // single match
inNs, _      := mem.Get("default")               // prefix scan (all in namespace)
all, _       := mem.List()
```

Implementation highlights (see `pkg/k8s/store/memory.go`):

- Backing data is `map[string][]any` keyed by the composite-string form of the index. Multiple resources can share a key; `Get` returns them all.
- `Get` with the full key count is an O(1) map lookup that returns the per-bucket slice as-is (zero-copy — see "Immutability Contract"). Per-bucket slices are kept sorted at insert time so reads are deterministic without runtime sorting; partial-prefix scans aggregate matching buckets and sort the result.
- `List` rebuilds and sorts the full slice on every call — there's no memoised result. The optimisation is "buckets are pre-sorted, so per-bucket reads are zero-copy", not "the whole list is cached". A consumer that needs a memoised `List` should cache at its own layer, not inside the store.
- An `RWMutex` protects the data map; concurrent readers don't contend.

## CachedStore

```go
cached, _ := store.NewCachedStore(&store.CachedStoreConfig{
    NumKeys:  2,
    CacheTTL: 2*time.Minute + 10*time.Second,
    Client:   dynamicClient,
    GVR:      schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"},
    Indexer:  indexer,          // used by the watcher to extract keys on Add
    Logger:   logger,
})
```

- Stores only `resourceRef` tuples (index keys + namespace/name) in memory.
- `Get` cache hits return immediately; cache misses call the dynamic client, cache the result with `CacheTTL`, and return it.
- The cache is keyed by `namespace/name`, separate from the index composite key — multiple references can share the same index key while each has its own cache entry.
- **`List` forces a fetch for every reference.** Use it only for small collections or debugging; prefer `MemoryStore` for templates that iterate everything.
- The implementation releases the store lock *before* dispatching API calls so one slow fetch doesn't block other lookups.

In the controller the TTL is auto-derived from `dataplane.driftPreventionInterval × 2.2` (see `pkg/controller/resourcewatcher/watcher.go`) — it's **not** a user-configurable CRD field. That derivation means a cached entry stays warm for slightly longer than the drift prevention window, so it's already there when the next reconciliation fires.

## Error Shape

`StoreError` wraps every failure with the operation name and the keys involved:

```go
var sErr *store.StoreError
if errors.As(err, &sErr) {
    log.Error("store op failed",
        "op", sErr.Operation,
        "keys", sErr.Keys,
        "cause", sErr.Cause)
}
```

Common `.Cause` values: `errors.New("at least one key required")`, key-count mismatch (passed `N` keys when the store was built for `M`), fetcher errors (`CachedStore` only).

## Immutability Contract

**Returned slices and resources must not be mutated.** Both stores return their internal data directly for performance; a caller mutating the returned value corrupts the store for every subsequent reader. Clone before modifying:

```go
for _, obj := range resources {
    copy := runtime.DeepCopyObject(obj.(runtime.Object))
    // mutate `copy` freely
}
```

This is enforced by convention, not by type — the `Store` interface returns `any`, so the compiler can't help. Watchers and `pkg/stores/overlay.go` both rely on this contract.

## Non-Unique Keys

Indexing by labels (e.g. `metadata.labels.kubernetes\\.io/service-name` for EndpointSlices) is expected to collide — many slices share a service. `Add` appends to the slot instead of overwriting, and `Get` with that key returns every match. Resource-identity equality for `Update` (and `Delete`-by-name) is **namespace + name** only, via `extractNamespaceName` — UID is not consulted, so a deleted-and-recreated resource looks identical to its predecessor (which is correct: the watcher fires `Update`, not `Delete`+`Add`, on a re-create). `Add` itself does not dedupe — duplicates are possible if the watcher's delta logic is wrong. That's by design: cheap append, dedupe lives in `Update`.

## Testing

```bash
go test ./pkg/k8s/store/...          # unit tests
go test ./pkg/k8s/store/... -race    # race detector
```

Tests exercise both stores against the same `types.Store` interface contract — adding a new implementation means satisfying the same test table.

## See Also

- [`pkg/k8s/types`](../types/) — `Store` interface definition
- [`pkg/k8s/watcher`](../watcher/) — `Watcher` builds these stores from a `SharedInformerFactory`
- [`pkg/k8s/indexer`](../indexer/) — JSONPath extraction that produces the composite keys
- [`pkg/stores/overlay.go`](../../stores/overlay.go) — overlay wrapper used by the webhook dry-run path
- `pkg/k8s/store/CLAUDE.md` — developer context (adding storage strategies, cache tuning, thread-safety proofs)

## License

Apache-2.0 — see root `LICENSE`.
