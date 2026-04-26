# pkg/controller/statusapplier

Applies template-driven status patches to Kubernetes resources via Server-Side Apply (SSA).

## Overview

Templates can register status patches against arbitrary Kubernetes resources (typically the Ingress / HTTPRoute / Gateway whose configuration was just rendered) using the `pkg/templating.StatusPatch` API. Each registered patch carries variants keyed by pipeline outcome — `rendered` (rendering succeeded), `deployed` (deployment succeeded), `renderFailed`, `deployFailed`. This component subscribes to the lifecycle events, picks the right variant, and applies it via SSA with `fieldManager: "haptic"` so it composes cleanly with patches from other controllers.

It runs on **every replica** (subscribes in the constructor like other all-replica components) but only the leader actually issues SSA patches — followers cache state so they're ready to take over instantly on `BecameLeaderEvent`.

## Quick Start

```go
import (
    "k8s.io/client-go/dynamic"

    "gitlab.com/haproxy-haptic/haptic/pkg/controller/statusapplier"
)

applier := statusapplier.New(&statusapplier.Config{
    EventBus:      bus,
    DynamicClient: dynamicClient,
    GVRResolver:   statusapplier.NewRestMapperResolver(),
    Logger:        logger,
})
go applier.Start(ctx)
```

`GVRResolver` is an interface so tests can supply a fake. `NewRestMapperResolver()` (the default) takes no arguments and resolves `apiVersion + kind` → `GroupVersionResource` via static lowercase-pluralisation, which covers the well-known Kubernetes and Gateway-API kinds (Ingress → ingresses, HTTPRoute → httproutes, etc.). Custom resources with non-standard pluralisation need a custom `GVRResolver` implementation.

## Event Flow

| Event | Action |
|-------|--------|
| `TemplateRenderedEvent` | Cache the patches (registered by templates during rendering); apply the `rendered` variant if leader |
| `ReconciliationCompletedEvent` | Apply the `deployed` variant if leader |
| `ReconciliationFailedEvent` | Apply `renderFailed` or `deployFailed` variant (depending on which phase failed) if leader |
| `BecameLeaderEvent` | Clear the per-pod checksum cache and re-apply the cached `rendered` variant |
| `LostLeadershipEvent` | Clear pending per-pod state |

## SSA Conflict Handling

Each apply uses `fieldManager: "haptic"`. If another controller has set a conflicting field, SSA returns a 409 Conflict — the component logs the conflict and continues; the next reconciliation cycle will retry. Persistent conflicts indicate a configuration mistake (two controllers both claiming ownership of the same status field) and need human attention.

## See Also

- [`pkg/templating`](../../templating/) — `StatusPatch` registration API used by templates
- [`pkg/controller/events`](../events/) — event types this component subscribes to
- [`docs/controller/docs/templating.md`](../../../docs/controller/docs/templating.md) — template-author view of status patches

## License

Apache-2.0 — see root `LICENSE`.
