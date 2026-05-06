# pkg/controller/reconciler - Reconciliation Debouncer

Development context for the Reconciler component.

## When to Work Here

Work in this package when:

- Modifying debounce logic
- Changing reconciliation triggering behavior
- Adjusting debounce interval
- Adding reconciliation triggers

**DO NOT** work here for:

- Render-validate pipeline → Use `pkg/controller/pipeline`
- Template rendering → Use `pkg/controller/renderer`
- Deployment → Use `pkg/controller/deployer`

## Package Purpose

Stage 5 component that debounces resource changes and triggers reconciliation events. Uses leading-edge triggering with refractory period for fast response to isolated changes while still batching rapid successive changes.

## Architecture

```
ResourceIndexUpdatedEvent  → Debounce (1s default, leading-edge)
IndexSynchronizedEvent     → Immediate Trigger (initial reconciliation)
HTTPResourceUpdatedEvent   → Debounce (1s default, leading-edge)
HTTPResourceAcceptedEvent  → Immediate Trigger
DriftPreventionTriggeredEvent → Immediate Trigger
BecameLeaderEvent          → Immediate Trigger (bootstraps the new leader)

    ↓
ReconciliationTriggeredEvent → Coordinator
```

## Debounce Behavior

### Leading-Edge Triggering with Refractory Period

The debouncer uses leading-edge triggering: the first change fires immediately if no callback has fired recently. Subsequent changes within the refractory period are batched.

```
t=0.0s: Resource change → Immediate callback (no recent activity)
t=0.3s: Resource change → Queued (refractory period active)
t=0.7s: Resource change → Queued (refractory period active)
t=1.0s: Refractory expires → Callback with batched changes
```

The refractory period defaults to `pkg/k8s/types.DefaultDebounceInterval` (1s) and is overridable via `spec.controller.reconciliationDebounceInterval` on the CRD. The default is shared with the per-watcher debounce default so the two debouncers come from one source of truth (see "centralized defaults" in `pkg/core/CLAUDE.md`). This ensures fast response (0ms delay) for isolated changes while batching rapid successive changes during busy periods.

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

```go
reconciler := reconciler.New(bus, logger, &reconciler.Config{
    DebounceInterval: 100 * time.Millisecond,  // Customizable
})
```

The default interval is defined in `pkg/k8s/types.DefaultDebounceInterval` (1s) and shared across all debouncing components. Operators override it on the CRD via `spec.controller.reconciliationDebounceInterval`; the controller passes the parsed value into `Config{DebounceInterval}` at construction in `pkg/controller/reconciliation.go`.

## Common Patterns

### Default Configuration

```go
// Uses types.DefaultDebounceInterval (1s)
reconciler := reconciler.New(bus, logger, nil)
go reconciler.Start(ctx)
```

### Custom Debounce

```go
// Longer debounce for high-volume environments
reconciler := reconciler.New(bus, logger, &reconciler.Config{
    DebounceInterval: 500 * time.Millisecond,
})
```

## Common Pitfalls

### Too Short Debounce

**Problem**: Reconciliation triggered too frequently during bulk operations.

**Solution**: Increase debounce interval for high-volume environments.

### Too Long Debounce

**Problem**: Slow to react to changes, causing 503 errors during rolling deployments.

**Solution**: Use leading-edge triggering (now default) which fires immediately for isolated changes while still batching rapid successive changes.

## Integration

Controller creates Reconciler in Stage 5:

```go
// Stage 5: Reconciliation
reconciler := reconciler.New(bus, logger, nil)
go reconciler.Start(ctx)
```

## Resources

- Coordinator (in this package): Orchestrates the render-validate pipeline
- Pipeline: `pkg/controller/pipeline/pipeline.go`
- Events: `pkg/controller/events/CLAUDE.md`
