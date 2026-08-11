# pkg/stores

Store provider abstractions and overlay machinery used by the render-validate pipeline to evaluate hypothetical configuration changes without modifying live state.

## Overview

The controller's renderer takes a `StoreProvider` rather than a raw `map[string]Store` so it can be handed:

- A `RealStoreProvider` during normal reconciliation — backed directly by the live `pkg/k8s/store` instances.
- An `OverlayStoreProvider` during webhook validation or proposal-validator runs — wraps the live providers with a `ValidationContext` of overlays so the proposed change appears in templates without modifying the actual stores.

The package also defines its own `Store` interface (structurally identical to `pkg/k8s/types.Store`) and the `TypesStoreAdapter` that bridges them. The optional `ContextGetter` and `ContextLister` interfaces let API-backed reads inherit a render's cancellation without changing the legacy `Store` method set. The two Store interfaces stay structurally identical but are kept apart by `arch-go.yml` so `pkg/stores` can be consumed by the templating pipeline without dragging in `client-go`.

## Quick Start

### Real provider (production)

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/stores"

real := stores.NewRealStoreProvider(map[string]stores.Store{
    "ingresses": ingressStore,
    "services":  serviceStore,
})

// Renderer uses real.GetStore("ingresses") to fetch the live store.
// (The interface method is GetStore, not Get — Get exists on Store, not on StoreProvider.)
```

### Overlay provider (validation)

```go
overlays := map[string]*stores.StoreOverlay{
    "ingresses": stores.NewStoreOverlayForCreate(proposedIngress),
}
ctx := stores.NewValidationContext(overlays).
    WithHTTPOverlay(httpOverlay) // optional, for HTTP content validation

overlay := stores.NewOverlayStoreProvider(real, ctx)
// overlay.GetStore("ingresses") now returns a *CompositeStore that includes
// proposedIngress on top of the live ingresses store. Stores without a
// matching overlay pass through unchanged.
```

## Overlay Constructors

| Constructor | Use for |
|-------------|---------|
| `NewStoreOverlay()` | Empty overlay; populate manually |
| `NewStoreOverlayForCreate(obj)` | Webhook CREATE — `obj` appears in the composite store |
| `NewStoreOverlayForUpdate(obj)` | Webhook UPDATE — `obj` replaces the existing entry |
| `NewStoreOverlayForDelete(ns, name)` | Webhook DELETE — entry is hidden in the composite store |

The `*CompositeStore` returned by `OverlayStoreProvider.GetStore` is read-only — its `Add` / `Update` / `Delete` / `Clear` methods all return `*ReadOnlyStoreError` so a template that accidentally tries to write learns immediately rather than corrupting the live store. The provider itself doesn't expose mutation methods at all; the safety comes from the store it hands out.

## ContentOverlay vs HTTPContentOverlay

`ContentOverlay` is the marker interface ("does this overlay carry pending changes?"). `HTTPContentOverlay` is the same plus three methods for resolving HTTP content lookups during validation. Splitting them lets `pkg/stores` consume HTTP overlays without importing `pkg/httpstore` (the latter implements `HTTPContentOverlay` via its own `HTTPOverlay` type).

## See Also

- [`pkg/k8s/types`](../k8s/types/) — the structurally-identical `Store` interface that `TypesStoreAdapter` bridges from
- [`pkg/k8s/store`](../k8s/store/) — concrete `MemoryStore` / `CachedStore` implementations
- [`pkg/controller/dryrunvalidator`](../controller/dryrunvalidator/) / [`proposalvalidator`](../controller/proposalvalidator/) — primary `OverlayStoreProvider` consumers
- [`pkg/httpstore`](../httpstore/) — implements `HTTPContentOverlay`

## License

Apache-2.0 — see root `LICENSE`.
