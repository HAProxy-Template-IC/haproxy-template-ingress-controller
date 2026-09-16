# Key design decisions

This page summarizes the current architecture. The linked Architecture Decision
Records (ADRs) explain the alternatives and tradeoffs.

## Configuration validation strategy

**Decision**: Let HAProxy validate its own configuration. Production binaries
contain no HAProxy configuration parser or Dataplane API schema validator.

Admission and configuration loading run `haproxy -c` synchronously.
Reconciliation runs that check in the asynchronous render gate; a refusal holds
subsequent renders until one passes. Each pod's HAProxy binary must also accept
a reload. Configured auxiliary-file validators run before dispatch.

[ADR-0022](../adr/0022-haptic-agent.md) defines these paths and the recovery
behavior. See [Pluggable validators](../../operations/pluggable-validators.md)
for auxiliary-file validation.

## Template engine selection

**Decision**: Use Scriggo (consumed via the `gitlab.com/haproxy-haptic/scriggo`
fork) as the template engine.

Scriggo provides Go-like control flow, macros, and template inheritance in a
Go library. Its virtual filesystem lets HAPTIC compile named snippets into one
program. HAPTIC's `render_glob` extension includes snippets matching a pattern,
which supports the chart's extension points.

Related: [ADR-0010](../adr/0010-typed-watched-resources.md) records the
follow-on decision to expose watched resources as typed top-level globals
instead of `dig()` chains.

## Kubernetes client architecture

**Decision**: Use client-go with `SharedInformerFactory` directly — no
controller framework.

The watched resource set is operator-defined and only known at runtime
(**Rule #1**: the Go code is resource-agnostic), so a framework's generated
per-kind scaffolding buys nothing. Direct informer usage keeps the
controller in charge of informer lifecycle, custom indexing, and cache
behaviour without fighting framework defaults.

Related: [ADR-0012](../adr/0012-on-demand-projection-and-access-gated-reconcile.md)
refines the store layer behind the informers — `store: on-demand` kinds
project informer bodies down to metadata to bound memory.

## Concurrency model

**Decision**: Use goroutines and channels for concurrent components, with
cancellable contexts managed by the lifecycle registry.

Watchers debounce resource events. Deployment uses bounded per-pod concurrency.
Components and blocking operations receive contexts so shutdown can cancel
work and wait for it to finish.

## Observability Integration

**Decision**: Prometheus metrics plus structured `log/slog` logging with
event correlation via the Event Commentator (see
[below](#event-commentator-pattern)). Distributed tracing is out of scope —
the controller emits no OpenTelemetry spans.

The metrics adapter (`pkg/controller/metrics`) observes domain events and updates
Prometheus counters and histograms. The commentator groups related events into
log messages. See `pkg/controller/metrics/README.md` for the metric catalog.
HAProxy request tracing is separate: the chart can export spans from access logs
through Vector.

## Error handling strategy

**Decision**: Wrapped errors (`fmt.Errorf` + `%w`) with a small set of
custom error types at package boundaries.

`pkg/dataplane.ValidationError` identifies the failed validation phase;
`pkg/controller/pipeline.PipelineError` identifies the failed pipeline stage.
Template errors distinguish compilation, rendering, timeouts, and missing
templates. Callers use `errors.Is` and `errors.As` to inspect wrapped causes.

## Event-driven architecture

**Decision**: Components coordinate through a homegrown EventBus
(`pkg/events`): async pub/sub, scatter-gather requests, pre-start
buffering, and subscriptions scoped to leadership. Business
logic lives in pure libraries (`pkg/templating`, `pkg/dataplane`,
`pkg/k8s`) with no event dependencies; only `pkg/controller` contains event
adapters.

Decoupling is the point: publishers don't know their consumers, new
features subscribe to existing events, and observers can correlate events across components. Pure libraries stay testable without event
infrastructure. The bus API surface (typed and lossy subscription variants,
drop accounting, `Publish` semantics) is documented in
`pkg/events/README.md`.

Three boundaries of the pattern are recorded as ADRs:

- **Rendering is synchronous, not an event adapter**
  ([ADR-0001](../adr/0001-renderer-is-synchronous-not-event-adapter.md)).
  The leader-only Coordinator drives `Pipeline.Execute` as one direct call
  and publishes `TemplateRenderedEvent` itself. The event hop was removed
  because it added hot-path latency and made the sequence harder to reason
  about. On the admission path render and `haproxy -c` still produce a
  single atomic verdict, because the reply carries it; on the reconcile
  path the check moved into the leader-only `rendergate` component
  precisely to keep it off that call stack (ADR-0022).
- **The HTTP store ↔ proposal validator hop stays event-driven**
  ([ADR-0006](../adr/0006-httpstore-proposal-validation-stays-event-driven.md)),
  even though it looks like the same single-publisher/single-subscriber
  shape ADR-0001 removed. There the async coupling is load-bearing: it
  decouples the refresh-timer cadence from multi-second validation latency.
- **Domain events require a concrete payload consumer**
  ([ADR-0019](../adr/0019-domain-events-require-a-payload-consumer.md)).
  Generic tracing doesn't keep an event alive. An observability subscriber
  qualifies only when it emits an operator-visible log, metric, or debug state
  that the publisher doesn't already emit.

Two invariants keep the pattern safe in practice:

- **Subscribe in constructors, before `EventBus.Start()`.** All components
  subscribe during construction; the bus buffers pre-start events and
  flushes them on `Start()`, so no component can miss an event published
  during startup. Timing-based fixes (sleeps) are banned.
- **Events are immutable facts.** Constructors defensively copy slices and
  maps, all `Event` methods use pointer receivers (enforced by the custom
  `eventimmutability` linter in `tools/linters/`), and consumers treat
  events as read-only.

## Request-response pattern (scatter-gather)

**Decision**: Configuration validation coordinates through the EventBus's
scatter-gather `Request()` API rather than hand-rolled response aggregation.

Config validation needs all of several validators (structure, template
compilation, JSONPath) to approve before a config becomes active. The bus
broadcasts a `ConfigValidationRequest`, correlates responses by request ID,
and enforces the timeout — the `ConfigChangeHandler`
(`pkg/controller/configchange`) aggregates the verdicts into
`ConfigValidatedEvent` or `ConfigInvalidEvent`. The validators themselves
are thin adapters in `pkg/controller/validator` over pure functions, so
adding a validator is one constructor plus one name in the expected-responder
list. The full flow is diagrammed in
[Sequence Diagrams → Configuration Validation Process](sequence-diagrams.md#configuration-validation-process).

The rule of thumb: use scatter-gather when multiple responders must answer
(validation, distributed queries); use plain pub/sub for fire-and-forget
notification and observability; use a direct function call when there is
exactly one callee and no coordination —
[ADR-0001](../adr/0001-renderer-is-synchronous-not-event-adapter.md) is
that rule applied to the renderer.

## Event commentator pattern

**Decision**: A dedicated component (`pkg/controller/commentator`)
subscribes to the full event stream and produces domain-aware log lines,
instead of scattering log statements through business logic.

The commentator keeps a ring buffer of recent events, so a log line can say
what a single call site can't — which change triggered this reconciliation,
how long the end-to-end deployment took across component boundaries:

```text
INFO  Reconciliation started trigger_event=resource.index.updated debounce_duration_ms=234
INFO  Deployment completed total_instances=3 succeeded=3 failed=0 total_duration_ms=456
```

It subscribes lossily (`SubscribeLossy`): commentary is observability, so
under backpressure dropping events is preferable to slowing publishers.
Cross-event domain knowledge lives in this one component, business logic
stays free of logging clutter, and new event types get commentary without
touching their publishers. See `pkg/controller/commentator/README.md` for
the implementation.
