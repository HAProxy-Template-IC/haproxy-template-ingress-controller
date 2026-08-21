# pkg/controller/reconciler

Entry point to the reconciliation pipeline. Two components:

- **`Reconciler`** — fires immediately on every watched-resource / HTTP-resource change and on whole-index events. No reconciler-level debounce. Publishes `ReconciliationTriggeredEvent`.
- **`Coordinator`** — leader-only adapter that consumes `ReconciliationTriggeredEvent` and drives the synchronous render/validate pipeline via a `PipelineExecutor`.

## Reconciler

```go
import (
    "context"

    "gitlab.com/haproxy-haptic/haptic/pkg/controller/reconciler"
)

// No configuration — the reconciler fires immediately on every event.
r := reconciler.New(bus, logger)

go r.Start(ctx)
```

### Triggering Rules

| Incoming event | Behaviour |
|----------------|-----------|
| `ResourceIndexUpdatedEvent` (real change) | Immediate — fire a reconciliation now |
| `ResourceIndexUpdatedEvent` (initial sync) | Ignored — the initial bulk load is covered by `IndexSynchronizedEvent` |
| `HTTPResourceUpdatedEvent` | Immediate |
| `IndexSynchronizedEvent` | Immediate — first reconciliation always runs with a complete store |
| `HTTPResourceAcceptedEvent` | Immediate — content is only promoted from pending to accepted after validation |
| `DriftPreventionTriggeredEvent` | Immediate — periodic redeploy path |
| `BecameLeaderEvent` | Immediate — bootstraps the new leader's pipeline so the (leader-only) renderer produces fresh `TemplateRenderedEvent` instead of relying on a stale replay |

The Reconciler adds zero latency: every event it handles fires a reconciliation immediately. Coalescing of rapid changes is the per-watcher debounce window's job (default 100ms, `pkg/k8s/types.DefaultDebounceInterval`; EndpointSlice watchers use `debounceInterval: "0"` so pod-IP rotations react instantly during rolling restarts). Reload throttling is the deployer's `minDeploymentInterval` (bypassed by the runtime-eligible fast path). This split keeps single ingress flips and rolling-restart endpoint rotations both fast without a reconciler-level refractory.

The initial-sync filter exists because `ResourceIndexUpdatedEvent` fires for every object as stores hydrate. Early reconciliations there would run against an incomplete store, so `IndexSynchronizedEvent` (which fires once every watcher finishes its initial list) is the correct first-reconciliation trigger.

## Coordinator

```go
coord := reconciler.NewCoordinator(&reconciler.CoordinatorConfig{
    EventBus:      bus,
    Pipeline:      pipeline,       // implements PipelineExecutor
    StoreProvider: storeProvider,  // pkg/stores.StoreProvider
    Logger:        logger,
})
go coord.Start(ctx)
```

Leader-only adapter around `pkg/controller/pipeline`:

1. Subscribe in `Start` (leader-only subscription pattern — not in the constructor, because followers must not subscribe).
2. On `ReconciliationTriggeredEvent`, publish `ReconciliationStartedEvent`.
3. Call `Pipeline.Execute(ctx, storeProvider)` synchronously — render + validate + build render context in one atomic step.
4. Publish the results: `TemplateRenderedEvent` on success, `ReconciliationFailedEvent` (with a `PipelineError` carrying the failing phase via `errors.As`) on error. Either path ends with `ReconciliationCompletedEvent` so the metrics adapter can close its histogram observation. HAProxy's verdict on the render follows asynchronously as `RenderGateCompletedEvent`, which the Coordinator consumes to settle the term's auxiliary baseline.

The pipeline is called directly, not through another event hop. That's deliberate: from the controller's perspective a reconciliation is one atomic stage, so making it a function call keeps error propagation straightforward and avoids inter-stage synchronization events.

## See Also

- [`pkg/controller/pipeline`](../pipeline/) — `Pipeline.Execute` implementation driven by this coordinator
- [`pkg/controller/renderer`](../renderer/) / [`validator`](../validator/) — pure stages composed into the pipeline
- [`pkg/controller/deployer`](../deployer/) — downstream consumer of `TemplateRenderedEvent` + `RenderGateCompletedEvent`
- [`pkg/controller/deployer`](../deployer/) — the `DriftPreventionMonitor` here is the actual `DriftPreventionTriggeredEvent` source (see `drift_monitor.go`); `pkg/controller/timers` is just a `SafeTimer` wrapper, not an event publisher
- `pkg/controller/reconciler/CLAUDE.md` — developer context (immediate-triggering design, leadership-transition patterns)

## License

Apache-2.0 — see root `LICENSE`.
