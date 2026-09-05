# resourceapplier

Generic, leader-only applier for template-declared Kubernetes resources.

## Purpose

Templates declare desired Kubernetes resources via the
`spec.k8sResources` map on `HAProxyTemplateConfig`. Each entry's template
renders to one or more YAML documents (`---`-separated) describing full
Kubernetes resources. The renderer parses each document, validates
required fields (`apiVersion`, `kind`, `metadata.{name,namespace}`),
and seals the resulting set into a render occurrence. This component reads the
authenticated snapshot, reconciles it through Server-Side Apply (SSA), and
prunes resources removed from later renders with exact UID and resourceVersion
preconditions.

For full-ownership resources in the controller's own namespace the
applier injects a controller `OwnerReference` (controller=true,
blockOwnerDeletion=true) pointing at the `HAProxyTemplateConfig` CR, so
cascade-delete (e.g. `helm uninstall`) GCs them automatically.

Mirrors `statusapplier` exactly except it operates on full resources
rather than `.status` sub-paths.

## Resource-agnostic by design

The controller never names "Service" or "Gateway" or any specific
Kubernetes resource type. Templates emit; the applier reconciles
whatever they produced. Same architectural principle as `statusPatch()`,
`templateSnippets`, and `watchedResources` — generic plumbing, chart
templates own the domain knowledge.

## Safety contract: dry-runs and webhook validations

This is the contract every contributor should understand before touching
this package or any caller.

**Subscribed events** (the only inputs that lead to API calls):

- `ReconciliationCompletedEvent` — authenticates `RenderOccurrence()`, applies
  the resources from its sealed cycle, then publishes `ResourcesAppliedEvent`
  with that same occurrence
- `RenderGateCompletedEvent` — authenticates the same occurrence before holding,
  releasing, or reverting resources
- `BecameLeaderEvent` — rebuilds the owned set from cluster state
- `LostLeadershipEvent` — pauses applies

**Publishers of those events:**

- `pkg/controller/reconciler.Coordinator` — the leader-only Stage 5
  component that drives the production render → validate → publish
  pipeline. **This is the only publisher.**

**What does NOT publish those events:**

- `pkg/controller/dryrunvalidator` — admission webhook handler. Calls
  `pkg/controller/proposalvalidator.ValidateSync` directly; renders
  templates, checks assertions, returns a verdict. Never touches the
  reconciliation event types this applier subscribes to.
- `pkg/controller/proposalvalidator` — only emits
  `ProposalValidationCompletedEvent` / `ProposalValidationFailedEvent`.
- `pkg/controller/testrunner` — pure component, no event coordination.
  Used by the `validate` CLI subcommand and the dryrunvalidator.
- `pkg/webhook` — synchronous HTTP path; no event publishing.
- `cmd/haptic/benchmark_render.go` — local-only, no bus involved.

**What that means in practice:**

When a webhook admission request arrives, the dryrunvalidator renders
the proposed config in an overlay store. Each `spec.k8sResources`
template runs and its YAML output is parsed into a per-render
`*RenderedResourceCollector` (same lifecycle as `*StatusPatchCollector`
for `statusPatch()` calls). Both collectors are constructed by
`pkg/controller/rendercontext.Builder.Build()` and live in the
rendering context for that single call. After the render finishes, the
testrunner / dryrunvalidator inspects the rendered HAProxy config and
aux files, but **does not read the collectors back** — see
`pkg/controller/testrunner/rendering.go`:

```go
renderCtx := builder.Build().Context
```

The `BuildResult` also carries `.StatusPatchCollector` and `.RenderedResourceCollector`;
the testrunner reads only `.Context`, so both collectors fall out of scope. The collectors fall out of scope,
GC eats them, and no API call ever happens.

**The structural property to preserve:** any future code that wires a
non-production caller into a `TemplateRenderedEvent` *must* also flow
its renders through the same path the production Coordinator uses —
which means it would be a new production path, and therefore the apply
behaviour is intended. A wire-up that publishes `TemplateRenderedEvent`
from a webhook handler with rendered resources attached would be a
regression in this contract; reviewers should reject it.

## Exact convergence

Every authenticated cycle reaches the API server. A local payload digest can't
prove that an unwatched target wasn't mutated or recreated after the previous
apply. The SSA response supplies the exact UID and resourceVersion later used as
delete preconditions. Missing or mismatched lineage fails closed; a replacement
resource with the same name is never deleted as an orphan.

Render-gate state is keyed by the opaque occurrence, not by public
`CycleSnapshot`, `RenderProof`, or plan fields. A verdict for another occurrence,
including one with identical output, can't release or revert the held cycle.

## Orphan pruning

Each successful pass populates an in-memory `lastAppliedKeys` map keyed
by `namespace/name/gvr`. The next pass computes the new desired set;
any key in `lastAppliedKeys` not in the new set is *deleted* via the
dynamic client. This handles the common case where a Gateway is
deleted and the per-Gateway Service should disappear with it.

### Startup-orphan recovery

If the controller crashes (or is upgraded) between applying a resource
and observing the user's deletion of its driver, the orphan would
otherwise persist indefinitely — the new controller process starts
with an empty `lastAppliedKeys` and has no way to know what the
previous incarnation applied.

To close this gap, `handleBecameLeader` runs a discovery pass on every
leader-acquire:

1. `discoveryClient.ServerPreferredNamespacedResources()` enumerates
   every namespace-scoped API resource type the cluster supports.
2. For each type that supports both `list` and `delete` verbs, the
   applier issues `dynamicClient.Resource(gvr).Namespace(ownNs).List(opts)`
   with a label selector pinning the managed-by label
   (`haproxy-haptic.org/managed-by=<controller-name>`).
3. Each returned resource is added to `lastAppliedKeys`.

Errors are silent: types we don't have RBAC for return `403 Forbidden`,
unsupported list operations return `405 MethodNotSupported`, CRDs that
disappeared between discovery and list return `404 NotFound`. None of
these abort recovery — the applier discovers what it can, not what it
must.

The discovery loop has a `recover()` fence around `List` calls so a
panicking dynamic-client implementation (e.g. a test fake with a
mis-registered scheme) skips the offending type rather than blowing
up the whole recovery.

After recovery completes, the next reconciliation's `applyAndPrune`
sees the orphans in `lastAppliedKeys` and (since the new render
doesn't include them) deletes them. Single-reconciliation convergence,
fully resource-agnostic — no hardcoded GVR list, no extra persistence
layer.

The discovery cost is one round-trip per cluster API resource type
(typically ~50–100 calls), incurred once per leader-acquire. Routine
operation is unaffected.

### Manual sweep (still useful)

The managed-by label is also useful for operator audits:

```bash
kubectl get services,configmaps,secrets -A -l haproxy-haptic.org/managed-by=haptic-controller
```

Lists everything the controller currently owns. Useful for diagnosing
unexpected behaviour or for migration scenarios.

## Namespace restriction

`Config.RestrictToOwnNamespace=true` (default for the chart) refuses to
apply any resource whose `Namespace` is empty (cluster-scoped) or
differs from `OwnNamespace`. Refusals are logged with a clear hint
about how to opt in.

This is **defense in depth** on top of the chart's RBAC. The chart
binds the controller's ServiceAccount to a namespace-scoped `Role`,
not a `ClusterRole`, so the API server will reject foreign-namespace
applies regardless of what this component sends. The applier-level
guard catches the misbehaviour earlier, surfaces it in logs at WARN
level (so a misbehaving template is visible), and prevents the failed
API call from cluttering audit logs.

To opt into cluster-scoped or cross-namespace provisioning, set
`RestrictToOwnNamespace=false` in `Config` *and* grant the appropriate
ClusterRole RBAC. Doing only one without the other will break the
apply: RBAC denies the request before the in-process guard fires.

## Field manager

All applies use field manager `haptic` (same as `statusapplier` — both
subsystems are part of the same controller and a single field-manager
identity is the simplest audit story). With `Force=true`, the applier
takes ownership of any field it sets even if another manager
previously claimed it.

## Wiring into the controller

Constructed in `pkg/controller/reconciliation.go` alongside
`statusapplier`, registered as an all-replica subscriber, leader-only
applier:

```go
resourceApplierComponent := resourceapplier.New(&resourceapplier.Config{
    EventBus:               bus,
    DynamicClient:          k8sClient.DynamicClient(),
    DiscoveryClient:        k8sClient.Clientset().Discovery(),
    GVRResolver:            statusapplier.NewRestMapperResolver(gvrMapper),
    Logger:                 logger,
    OwnNamespace:           ownNamespace,
    RestrictToOwnNamespace: false,
    OwnerRef:               ownerRef,
})
reg.Register(resourceApplierComponent, false) // all-replica subscriber; applies are gated on the internal leader flag
```

`OwnNamespace` is read from the `POD_NAMESPACE` env var (set by the
chart via the downward API), falling back to the in-cluster client's
namespace.

## Tests

`component_test.go` covers:

- New() initialization and field defaults
- Leader / non-leader gating (non-leader does not apply)
- Every cycle verifies live state through SSA
- Authenticated occurrence propagation, shadow poisoning, and A-B-A cycles
- Exact-UID/resourceVersion orphan deletion and replacement protection
- Namespace restriction (foreign + cluster-scoped resources are refused)
- BecameLeaderEvent rebuilds deletion lineage and re-applies
- LostLeadershipEvent pauses applies
- Label injection preserves caller's labels and is non-mutating
- Start() returns cleanly on context cancellation
- ReconciliationCompletedEvent authenticates the resources it applies

Total coverage: 79.3% of statements.
