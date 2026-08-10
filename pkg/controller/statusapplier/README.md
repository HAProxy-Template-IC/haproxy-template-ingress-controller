# pkg/controller/statusapplier

Applies template-driven status patches to Kubernetes resources via Server-Side Apply (SSA).

## Overview

Templates can register status patches against arbitrary Kubernetes resources (typically the Ingress / HTTPRoute / Gateway whose configuration was just rendered) using the `pkg/templating.StatusPatch` API. Each registered patch carries variants keyed by pipeline outcome — `rendered` (rendering succeeded), `deployed` (deployment succeeded), `renderFailed`, `deployFailed`. This component subscribes to the lifecycle events, picks the right variant, and applies it via SSA with a phase-scoped field manager (`haptic-rendered`, `haptic-deployed`, `haptic-renderFailed`, `haptic-validateFailed`, or `haptic-deployFailed`) so each phase owns disjoint condition entries and composes cleanly with patches from other controllers.

It runs on **every replica** (subscribes in the constructor like other all-replica components) but only the leader actually issues SSA patches. The component is stateless — patches travel on the events that trigger each apply, so a new leader simply relies on the `Reconciler` to fire a fresh reconciliation.

## Quick Start

```go
import (
    "k8s.io/client-go/dynamic"

    "gitlab.com/haproxy-haptic/haptic/pkg/controller/statusapplier"
)

applier := statusapplier.New(&statusapplier.Config{
    EventBus:      bus,
    DynamicClient: dynamicClient,
    GVRResolver:   statusapplier.NewRestMapperResolver(restMapper),
    Logger:        logger,
})
go applier.Start(ctx)
```

`GVRResolver` is an interface so tests can supply a fake. `NewRestMapperResolver(mapper)` (the default) takes the controller's `meta.RESTMapper` and resolves `apiVersion + kind` → `GroupVersionResource` from the cluster's discovery data — including each CRD's own `spec.names.plural`, so irregular plurals work without any Go-side pluralisation table, which covers the well-known Kubernetes and Gateway-API kinds (Ingress → ingresses, HTTPRoute → httproutes, etc.). Custom resources with non-standard pluralisation need a custom `GVRResolver` implementation.

## Event Flow

| Event | Action |
|-------|--------|
| `ResourcesAppliedEvent` | Apply the `rendered` variant directly from the event payload if leader (published by the ResourceApplier after the same render's resources exist — no caching, stateless) |
| `DeploymentCompletedEvent` | Apply the `deployed` variant if leader |
| `DeploymentSkippedEvent` | Apply the `deployed` variant if leader (deployment skipped because config unchanged) |
| `ReconciliationFailedEvent` | Apply `renderFailed` or `deployFailed` variant (depending on which phase failed) if leader |
| `BecameLeaderEvent` | Flip the leader flag on; clear the SSA checksum cache so the new leader writes at least once for every active resource on the next reconciliation (triggered by the `Reconciler`) |
| `LostLeadershipEvent` | Flip the leader flag off; in-flight handlers re-check via `leaderRLocked()` |

## SSA Conflict Handling

Each apply uses a phase-scoped field manager (e.g. `haptic-rendered`, `haptic-deployed`). The SSA calls use `Force: true` (`metav1.PatchOptions{Force: new(true)}`), so conflicting field ownership is taken from any other manager rather than returning a 409 Conflict. This means haptic always wins field-ownership races; there is no conflict-retry path.

## See Also

- [`pkg/templating`](../../templating/) — `StatusPatch` registration API used by templates
- [`pkg/controller/events`](../events/) — event types this component subscribes to
- [`docs/site/docs/templating.md`](../../../docs/site/docs/templating.md) — template-author view of status patches

## License

Apache-2.0 — see root `LICENSE`.
