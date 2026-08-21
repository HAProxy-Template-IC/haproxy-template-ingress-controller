# Key design decisions

This page summarizes the standing architectural choices: what was decided and
why it holds, one screen per decision. Point-in-time decisions with full
context, alternatives, and "don't re-suggest" guards live in the
Architecture Decision Records (published under *Development → Architecture
Decision Records*); each summary links the governing Architecture Decision Record (ADR) where one exists.
Mechanism detail — API surfaces, metric catalogues, event-type lists — lives
in the package READMEs referenced below, not here.

## Configuration validation strategy

**Decision**: Validate rendered configs in-process with three phases —
client-native syntax parse, OpenAPI schema check, `haproxy -c` semantic
check — instead of running a validation sidecar.

The parse phase takes ~10 ms and the binary check ~50–100 ms, so full
validation fits inside the reconciliation hot path. There is no sidecar to
build, schedule, or keep version-matched with the controller, and the
verdict still comes from a real `haproxy -c` run. Two content-addressed
caches (a controller-side checksum cache and a dataplane-level
`(configHash, auxHash, versionHash)` cache) let drift-prevention cycles
short-circuit before any file is written; failures are never cached.

Implementation: `pkg/dataplane/validator.go` (plus `validate_syntax.go` /
`validate_schema.go` / `validate_haproxy.go`), wrapped by
`pkg/controller/validation.ValidationService`. Two operational facts worth
knowing: the validation paths under the CRD's `spec.dataplane` must match
the Dataplane API server's resource configuration, and `haproxy -c`
invocations are serialized process-wide because concurrent runs interfere
with each other even when their temp directories are isolated.

## Template engine selection

**Decision**: Use Scriggo (consumed via the `gitlab.com/haproxy-haptic/scriggo`
fork) as the template engine.

Scriggo is pure Go with Go-like template syntax, and ships control flow,
macros, and template inheritance without extra dependencies. The decisive
feature is its custom file-system support: include paths resolve at render
time, which is what makes the `render_glob` extension-point pattern — and
with it the whole chart library architecture — possible.

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

**Decision**: Goroutines and channels with structured concurrency — every
component runs a `Start(ctx)` loop, lifecycles compose via `errgroup`, and
`context.Context` propagates through every call chain.

Event processing uses buffered channels with debouncing at the watcher
layer; deployment fans out to HAProxy instances through bounded worker
pools; cancellation reaches every operation because contexts are passed,
not recreated. Shutdown publishes a shutdown event, cancels the component
context, and waits for the errgroup with a timeout.

## Observability Integration

**Decision**: Prometheus metrics plus structured `log/slog` logging with
event correlation via the Event Commentator (see
[below](#event-commentator-pattern)). Distributed tracing is out of scope —
the controller emits no OpenTelemetry spans.

The event stream already carries every state transition, so observability
subscribes to it instead of instrumenting business logic: a metrics event
adapter (`pkg/controller/metrics`) updates an instance-based Prometheus
registry, and the commentator produces correlated log lines. If end-to-end
trace correlation is ever needed, an OTel exporter would be one more
subscriber on the same stream. The metric catalogue lives in
`pkg/controller/metrics/README.md`; `metrics.go` in that package is the
authoritative list.

## Error handling strategy

**Decision**: Wrapped errors (`fmt.Errorf` + `%w`) with a small set of
custom error types at package boundaries.

`pkg/dataplane` defines `*ValidationError` (carrying the failed phase:
syntax / schema / semantic) and `*ParseError`; `pkg/templating` defines
`*RenderError`, `*CompilationError`, `*RenderTimeoutError`, and
`*TemplateNotFoundError`. Every type carries a cause and implements
`Unwrap()`, so `errors.Is` / `errors.As` chains work end to end and callers
can branch on failure mode without string matching.

## Event-driven architecture

**Decision**: Components coordinate through a homegrown EventBus
(`pkg/events`): async pub/sub, scatter-gather requests, pre-start
buffering, and a Pause/Resume hook for leadership transitions. Business
logic lives in pure libraries (`pkg/templating`, `pkg/dataplane`,
`pkg/k8s`) with no event dependencies; only `pkg/controller` contains event
adapters.

Decoupling is the point: publishers don't know their consumers, new
features subscribe to existing events, and the full event stream doubles as
a system-wide audit trail. Pure libraries stay testable without event
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
