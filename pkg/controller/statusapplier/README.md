# pkg/controller/statusapplier

Applies template-driven status patches to Kubernetes resources via Server-Side Apply (SSA).

## Overview

Templates can register status patches against arbitrary Kubernetes resources (typically the Ingress / HTTPRoute / Gateway whose configuration was just rendered) using the `pkg/templating.StatusPatch` API. Each registered patch carries variants keyed by pipeline outcome — `rendered` (rendering succeeded), `deployed` (deployment succeeded), `renderFailed`, `deployFailed`. This component subscribes to the lifecycle events, picks the right variant, and applies it via SSA with a phase-scoped field manager (`haptic-rendered`, `haptic-deployed`, `haptic-renderFailed`, `haptic-validateFailed`, or `haptic-deployFailed`) so each phase owns disjoint condition entries and composes cleanly with patches from other controllers.

It runs on **every replica** (subscribes in the constructor like other all-replica components) but only the leader actually issues SSA patches. The component is stateless. Successful lifecycle events carry one sealed render occurrence, and the applier reads the authenticated status snapshot from it. Public event fields are diagnostic shadows and never authorize an apply.

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

`GVRResolver` is an interface so tests can supply a fake. `NewRestMapperResolver(mapper)`
uses the controller's `meta.RESTMapper` to resolve `apiVersion + kind` from cluster
discovery, including each CRD's `spec.names.plural`. Custom resources with irregular
plurals need no special resolver.

## Why templates register variants

Templates supply the resource-specific condition fields and every outcome variant
during rendering. Later pipeline events select a variant without rerendering the
templates or adding resource-specific logic to Go. SSA respects the target schema's
list-map merge keys, so another controller's condition entries can coexist with
HAPTIC's entries.

After a render failure, the coordinator supplies the status snapshot from its last
successful render. A resource that has never appeared in a successful render has
no registered failure variant; inspect the controller's render error rather than
expecting a new status condition on that resource.

## Event Flow

| Event | Action |
|-------|--------|
| `ResourcesAppliedEvent` | Read the authenticated occurrence and apply its `rendered` variant if leader |
| `DeploymentCompletedEvent` | Read the authenticated occurrence and apply `deployed` when every pod runs it or `deployFailed` when a pod failed |
| `DeploymentSkippedEvent` | Read the authenticated occurrence and apply `deployed` if leader |
| `ReconciliationFailedEvent` | Apply `renderFailed` or `deployFailed` variant (depending on which phase failed) if leader |
| `BecameLeaderEvent` | Flip the leader flag on; clear the status apply cache so the next reconciliation writes the new leader's status |
| `LostLeadershipEvent` | Flip the leader flag off; in-flight handlers re-check via `leaderRLocked()` |

## SSA Conflict Handling

Each apply uses a phase-scoped field manager (e.g. `haptic-rendered`, `haptic-deployed`). The SSA calls use `Force: true` (`metav1.PatchOptions{Force: new(true)}`), so conflicting field ownership is taken from any other manager rather than returning a 409 Conflict. This means haptic always wins field-ownership races; there is no conflict-retry path.

## See Also

- [`pkg/templating`](../../templating/) — `StatusPatch` registration API used by templates
- [`pkg/controller/events`](../events/) — event types this component subscribes to
- [`docs/site/docs/templating.md`](../../../docs/site/docs/templating.md) — template-author view of status patches

## License

Apache-2.0 — see root `LICENSE`.
