# pkg/controller/events

Domain event catalogue for the controller. All event types that flow through the `pkg/events` bus live here; the infrastructure itself (publish/subscribe, scatter-gather) lives in `pkg/events` and has no knowledge of domain semantics.

- ~45 event types across ~14 categories.
- All implement the `pkg/events.Event` interface (`EventType() string`, `Timestamp() time.Time`) via pointer receivers.
- All exported constructors `NewFooEvent(...)` perform defensive copies of slices and maps so consumers can't mutate a published event.
- A custom `go vet`-style analyzer in `tools/linters/eventimmutability` enforces the pointer-receiver rule at build time.

## Source of Truth

One file per category. The full list as of writing, with representative types:

| File | Category | Representative types |
|------|----------|----------------------|
| `types.go` | Event-type constants and shared helpers | — |
| `correlation.go` | Request/response correlation metadata | `Correlation` (struct), `CorrelatedEvent` (interface, exposes `CorrelationID() string`) |
| `config.go` | CRD parsed / validated / invalid | `ConfigParsedEvent`, `ConfigValidatedEvent` |
| `credentials.go` | `Secret` ingestion and validation | `CredentialsUpdatedEvent` |
| `resource.go` | Watched-resource index changes | `ResourceIndexUpdatedEvent`, `IndexSynchronizedEvent` |
| `reconciliation.go` | Reconciliation pipeline lifecycle | `ReconciliationTriggeredEvent`, `ReconciliationCompletedEvent`, `ResourcesAppliedEvent` |
| `template.go` | Rendering | `TemplateRenderedEvent`, `TemplateRenderFailedEvent` |
| `validation.go` | Syntax/semantic validation | `ValidationFailedEvent` |
| `rendergate.go` | Asynchronous `haproxy -c` verdict on a render | `RenderGateCompletedEvent` |
| `deployment.go` | HAProxy deployment scheduler + executor | `DeploymentScheduledEvent`, `InstanceDeploymentFailedEvent` |
| `discovery.go` | HAProxy pod discovery | `HAProxyPodsDiscoveredEvent`, `HAProxyPodRejectedEvent` |
| `leader.go` | Leader election | `BecameLeaderEvent`, `LostLeadershipEvent` |
| `publishing.go` | Output-CRD publishing (`HAProxyCfg` + `HAProxy{General,Map,CRTList}File`) and per-pod sync outcomes | `ConfigPublishedEvent`, `ConfigAppliedToPodEvent` |
| `proposal.go` | Admission-time proposal validation | `ProposalValidationRequestedEvent`, `ProposalValidationCompletedEvent` |
| `http.go` | HTTP resource fetcher | `HTTPResourceUpdatedEvent` |
| `status.go` | Status-patch application | `StatusUpdateCompletedEvent`, `StatusUpdateFailedEvent` |

`types.go` plus the event-category files enumerate every constant — if the list above looks incomplete, check `grep -E "^type [A-Z].*Event " pkg/controller/events/*.go` rather than trusting this README.

## Publishing

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
    busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

bus.Publish(events.NewConfigParsedEvent(cfg, templateConfig, version, secretVersion))
```

Always use the `New*` constructors — they copy any slices/maps to cut off the ownership chain. Don't construct events with a struct literal; you lose the defensive copy and the analyzer can't help.

### Render occurrence authority

Production render lifecycle events carry one sealed `*rendercycle.Occurrence` from rendering through resource apply, validation, scheduling, and deployment completion. `NewTemplateRenderedEventWithCycle` creates it. Read it with `RenderOccurrence()` and pass that exact pointer to downstream `WithCycle` constructors or `NewDeploymentResultWithOccurrence`.

`CycleSnapshot`, `OutputSnapshot`, `RenderProof`, `PlanID`, and checksum fields are compatibility and diagnostic shadows. They aren't authentication inputs. Mutating or substituting them doesn't change `AuthenticatedRenderIdentity()`, and subscriber clones restore them from the private occurrence. Legacy constructors don't authenticate an occurrence; their occurrence accessors return an error.

Don't reconstruct identity from a snapshot and proof. A proof is diagnostic, and equal output can occur in distinct A-B-A render executions.

## Consuming

Either accept everything and switch on type, or subscribe to a filtered set:

```go
// Any event
eventChan := bus.Subscribe("my-component", 100)
for ev := range eventChan {
    switch e := ev.(type) {
    case *events.ConfigValidatedEvent:
        // ...
    case *events.ReconciliationTriggeredEvent:
        // ...
    }
}

// Just a few types (cheaper — filtered at the bus)
filtered := bus.SubscribeTypes(
    "my-component", 100,
    events.EventTypeReconciliationTriggered,
    events.EventTypeReconciliationCompleted,
)
```

## Scatter-Gather (Config Validation)

`ConfigValidationRequest` is the single `Request` type in the catalogue. Usage goes through `pkg/events.Request`:

```go
req := events.NewConfigValidationRequest(cfg, version)

result, err := bus.Request(ctx, req, busevents.RequestOptions{
    Timeout:            10 * time.Second,
    ExpectedResponders: []string{"basic", "template", "jsonpath"},
})
if err != nil {
    return err
}
for _, resp := range result.Responses {
    if r, ok := resp.(*events.ConfigValidationResponse); ok && !r.Valid {
        return fmt.Errorf("validator %s: %s", r.ValidatorName, strings.Join(r.Errors, "; "))
    }
}
```

Each responder subscribes normally, matches on request ID, and `bus.Publish`es a matching `*ConfigValidationResponse`.

## Adding a New Event

1. Add `const EventTypeFoo = "foo"` to `types.go` (or the category file if it already has its own constants block).
2. Define the struct in the appropriate category file (or create a new file if there's no natural home).
3. Implement `EventType() string` with a **pointer** receiver.
4. Add a `NewFooEvent(...)` constructor that `copy()`s every incoming slice and map field.
5. Decide `pkg/controller/commentator`'s treatment: Give the commentator an insight case OR a level arm, unless the publisher already logs the payload — the fallback then records only that the event happened.
6. If this event should drive a Prometheus metric, wire a case into `pkg/controller/metrics.Component.HandleEvent` and update `pkg/controller/metrics/README.md`.

## See Also

- [`pkg/events`](../../events/) — generic bus (`Publish`, `Subscribe`, `Request`, typed subscriptions)
- [`pkg/controller/commentator`](../commentator/) — logs every event, attaches recent-event context via the ring buffer
- [`pkg/controller/metrics`](../metrics/) — subscribes to nearly everything in this catalogue for domain metrics
- [`tools/linters/eventimmutability`](../../../tools/linters/eventimmutability/) — custom analyzer that enforces pointer receivers
- `pkg/controller/events/CLAUDE.md` — developer context (immutability rules, category file organisation, common pitfalls)

## License

Apache-2.0 — see root `LICENSE`.
