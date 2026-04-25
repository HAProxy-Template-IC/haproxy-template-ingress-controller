# pkg/controller/reconciler

Entry point to the reconciliation pipeline. Two components:

- **`Reconciler`** — leading-edge-triggered with a refractory period on watched-resource / HTTP-resource changes, immediate on whole-index events. Publishes `ReconciliationTriggeredEvent`.
- **`Coordinator`** — leader-only adapter that consumes `ReconciliationTriggeredEvent` and drives the synchronous render/validate pipeline via a `PipelineExecutor`.

## Reconciler

```go
import (
    "context"
    "time"

    "gitlab.com/haproxy-haptic/haptic/pkg/controller/reconciler"
)

// Nil config → default refractory period from types.DefaultDebounceInterval (5s)
r := reconciler.New(bus, logger, nil)

// Or override:
r = reconciler.New(bus, logger, &reconciler.Config{
    DebounceInterval: 2 * time.Second,
})

go r.Start(ctx)
```

### Triggering Rules

| Incoming event | Behaviour |
|----------------|-----------|
| `ResourceIndexUpdatedEvent` (real change) | Leading-edge: fire immediately if no recent reconciliation, otherwise batch into the next one |
| `ResourceIndexUpdatedEvent` (initial sync) | Ignored — the initial bulk load is covered by `IndexSynchronizedEvent` |
| `HTTPResourceUpdatedEvent` | Same leading-edge rule |
| `IndexSynchronizedEvent` | Immediate — first reconciliation always runs with a complete store |
| `HTTPResourceAcceptedEvent` | Immediate — content is only promoted from pending to accepted after validation |
| `DriftPreventionTriggeredEvent` | Immediate — periodic redeploy path, no debounce |

The refractory-period design is deliberately different from classic trailing-edge debouncing: the *first* change in a quiet period fires with 0ms delay (so single ingress flips react fast), and only further changes arriving within the refractory window are batched. This removes the multi-second latency that a trailing-edge debouncer introduces during rolling deployments where many `ResourceIndexUpdatedEvent`s arrive in sequence. 5s default is shared with `pkg/k8s/types.DefaultDebounceInterval`.

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
4. Publish the results: `TemplateRenderedEvent` + `ValidationCompletedEvent` on success, `ReconciliationFailedEvent` (with a `PipelineError` carrying the failing phase via `errors.As`) on error. Either path ends with `ReconciliationCompletedEvent` so the metrics adapter can close its histogram observation.

The pipeline is called directly, not through another event hop. That's deliberate: from the controller's perspective a reconciliation is one atomic stage, so making it a function call keeps error propagation straightforward and avoids inter-stage synchronization events.

## See Also

- [`pkg/controller/pipeline`](../pipeline/) — `Pipeline.Execute` implementation driven by this coordinator
- [`pkg/controller/renderer`](../renderer/) / [`validator`](../validator/) — pure stages composed into the pipeline
- [`pkg/controller/deployer`](../deployer/) — downstream consumer of `TemplateRenderedEvent` + `ValidationCompletedEvent`
- [`pkg/controller/timers`](../timers/) — `DriftPreventionTriggeredEvent` source
- `pkg/controller/reconciler/CLAUDE.md` — developer context (leading-edge triggering design, leadership-transition patterns)

## License

Apache-2.0 — see root `LICENSE`.
