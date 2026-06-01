# pkg/controller/reconciler - Reconciliation Trigger

Development context for the Reconciler component.

## When to Work Here

Work in this package when:

- Changing reconciliation triggering behavior
- Adding reconciliation triggers

**DO NOT** work here for:

- Render-validate pipeline → Use `pkg/controller/pipeline`
- Template rendering → Use `pkg/controller/renderer`
- Deployment → Use `pkg/controller/deployer`

## Package Purpose

Stage 5 component that triggers reconciliation events. It fires **immediately** on every resource/HTTP event — there is no reconciler-level debounce or refractory window. Batching happens entirely upstream in the per-watcher debounce window (default 2s, `pkg/k8s/types.DefaultDebounceInterval`; EndpointSlice watchers run with `debounceInterval: "0"` for instant rolling-restart reaction). Reload throttling happens entirely downstream in the deployer's `minDeploymentInterval` (which the runtime-eligible fast path bypasses).

## Architecture

```
ResourceIndexUpdatedEvent  → Immediate Trigger
IndexSynchronizedEvent     → Immediate Trigger (initial reconciliation)
HTTPResourceUpdatedEvent   → Immediate Trigger
HTTPResourceAcceptedEvent  → Immediate Trigger
DriftPreventionTriggeredEvent → Immediate Trigger
BecameLeaderEvent          → Immediate Trigger (bootstraps the new leader)

    ↓
ReconciliationTriggeredEvent → Coordinator
```

## Triggering Behavior

The Reconciler triggers a `ReconciliationTriggeredEvent` immediately on every event it handles — isolated changes and bursts alike. It holds no timer and keeps no refractory state. Coalescing of rapid changes is the per-watcher debounce window's job (each watcher emits one `ResourceIndexUpdatedEvent` per quiet window); reload throttling is the deployer's job. The reconciler itself adds zero latency, which is what keeps a single ingress flip and a rolling-restart EndpointSlice rotation both fast.

### Index Synchronized (Immediate)

```
IndexSynchronizedEvent → Immediate ReconciliationTriggeredEvent
```

When all resource watchers complete initial sync, trigger immediate reconciliation.
This ensures the first render happens with a complete view of cluster state.

### HTTP Resource Accepted (Immediate)

```
HTTPResourceAcceptedEvent → Immediate ReconciliationTriggeredEvent
```

When HTTP content is promoted from pending to accepted (after validation succeeds),
trigger immediate reconciliation to deploy the new content.

## Configuration

The Reconciler takes no configuration — it fires immediately on every event and has no tunable interval:

```go
reconciler := reconciler.New(bus, logger)
go reconciler.Start(ctx)
```

There is no `reconciler.Config`, no `DebounceInterval` field, and no `spec.controller.reconciliationDebounceInterval` CRD knob. Batching lives in the per-watcher debounce window (default 2s, `pkg/k8s/types.DefaultDebounceInterval`; EndpointSlice at `"0"`); reload throttling lives in the deployer's `minDeploymentInterval`.

## Common Pitfalls

### Reload storms from over-eager reconciliation

**Problem**: Many reconciliations during bulk operations.

**Solution**: This is bounded upstream and downstream, not here. Raise the per-watcher `debounceInterval` to coalesce more events into a single `ResourceIndexUpdatedEvent`, or rely on the deployer's `minDeploymentInterval` to throttle reload-inducing pushes. Never reintroduce a reconciler-level refractory.

### Slow reaction during rolling deployments

**Problem**: 503 errors because a pod-IP rotation reaches HAProxy too late.

**Solution**: Keep the reconciler firing immediately (it already does), set the relevant watcher's `debounceInterval: "0"` (the chart does this for EndpointSlice), and let the deployer's runtime-eligible fast path apply server changes without waiting on `minDeploymentInterval`.

## Integration

Controller creates Reconciler in Stage 5:

```go
// Stage 5: Reconciliation
reconciler := reconciler.New(bus, logger)
go reconciler.Start(ctx)
```

## Resources

- Coordinator (in this package): Orchestrates the render-validate pipeline
- Pipeline: `pkg/controller/pipeline/pipeline.go`
- Events: `pkg/controller/events/CLAUDE.md`
