# pkg/controller - Controller Orchestration

Development context for the controller coordination layer.

**API Documentation**: See `pkg/controller/README.md`
**Architecture**: See `/docs/site/docs/development/design/architecture-overview.md` (controller internal architecture)

## When to Work Here

This package is the **coordination layer** - it orchestrates pure components via event-driven patterns.

Modify this package when:

- Adding new controller components (validators, renderers, deployers)
- Modifying event coordination logic
- Changing startup sequencing
- Adding new event types (in `controller/events/`)
- Implementing new event adapters

**DO NOT** modify this package for:

- Template rendering logic → Use `pkg/templating`
- Kubernetes client code → Use `pkg/k8s`
- HAProxy sync logic → Use `pkg/dataplane`
- Event bus infrastructure → Use `pkg/events`

## Package Structure

```
pkg/controller/
├── commentator/          # Event observability (logs all events)
├── configchange/         # Configuration change handler
├── configloader/         # HAProxyTemplateConfig CRD parsing and loading
├── credentialsloader/    # Secret parsing and loading
├── events/               # Domain event type catalog (~50 types)
├── indextracker/         # Index synchronization tracker
├── leaderelection/       # Leader election event adapter
│   └── component.go     # Wraps pure leader election, publishes events
├── pipeline/             # Render-validate pipeline (pure service)
│   └── pipeline.go      # Composes renderer + validator services
├── reconciler/           # Reconciliation coordination (Stage 5)
│   ├── reconciler.go    # Triggers reconciliation immediately (no debounce)
│   ├── coordinator.go   # Orchestrates pipeline execution
│   └── *_test.go        # Tests
├── renderer/             # Synchronous RenderService (stores → HAProxy config; ADR-0001)
├── resourcewatcher/      # Resource watcher lifecycle management
├── deployer/             # Deployment scheduler + drift monitor (leader-only)
├── configpublisher/      # Publishes rendered config/aux files as CRDs (leader-only)
├── resourceapplier/      # Server-Side Apply of template-emitted k8sResources
├── statusapplier/        # Applies template-driven status patches (leader-only)
├── discovery/            # HAProxy pod discovery
├── webhook/              # Admission webhook wiring
├── dryrunvalidator/      # Synchronous admission dry-run validation
├── proposalvalidator/    # Render + validate a proposed config/overlay
├── httpstore/            # Event adapter over the pure HTTP store
├── metrics/              # Prometheus metrics event adapter
├── debug/                # /debug/vars state + event buffer
├── validator/            # Config validation responders (scatter-gather participants)
│   ├── base.go          # Shared BaseValidator that subscribes to ConfigValidationRequest
│   ├── basic.go         # Structural validation
│   ├── template.go      # Template syntax validation
│   └── jsonpath.go      # JSONPath expression validation
└── controller.go         # Main controller with staged startup

```

This tree is a representative subset; `pkg/controller` has ~40 sub-packages. Run
`ls pkg/controller` (or see `docs/site/docs/development/design/package-structure.md`)
for the full layout.

## Key Design Pattern: Event Adapters

This package wraps pure components in event adapters to coordinate them:

```
Pure Component              Event Adapter
(pkg/templating)            (pkg/controller/configloader, etc.)
     ↓                            ↓
Engine          ────wraps──→  ConfigLoaderComponent
  .Render()                    - Subscribes via component.Base
                               - Calls into the pure component
                               - Publishes result events
```

### Example Event Adapter

The skeleton below illustrates the pattern with a hypothetical adapter. For the production scaffold every adapter embeds, see `pkg/controller/component.Base` (subscribes in the constructor, dispatches one event at a time, recovers from panics). Live examples include `pkg/controller/configloader.ConfigLoaderComponent` and `pkg/controller/credentialsloader.CredentialsLoaderComponent`.

Note: the production renderer is the synchronous `renderer.RenderService` (driven by `pkg/controller/pipeline.Pipeline`, no event hop), so don't model new event adapters on a "renderer wrapper" — there is no event hop for rendering.

```go
// Illustrative — not a real package. Shows the event-adapter shape.
package examplerenderer

import (
    "haptic/pkg/controller/events"
    "haptic/pkg/templating"
    busevents "haptic/pkg/events"
)

type Component struct {
    engine    templating.Engine          // Pure component
    eventBus  *busevents.EventBus       // Event coordination
    eventChan <-chan busevents.Event     // Subscribed in constructor
}

func New(bus *busevents.EventBus, engine templating.Engine) *Component {
    return &Component{
        engine:    engine,
        eventBus:  bus,
        eventChan: bus.Subscribe("examplerenderer", 100),  // Subscribe in constructor, before bus.Start()
    }
}

func (c *Component) Start(ctx context.Context) error {
    for {
        select {
        case event := <-c.eventChan:
            switch e := event.(type) {
            case *events.ReconciliationTriggeredEvent:
                // Extract primitives for pure component
                renderCtx := c.buildContext(e)

                // Call pure component
                output, err := c.engine.Render(ctx, "haproxy.cfg", renderCtx)

                // Publish result event
                if err != nil {
                    c.eventBus.Publish(events.NewTemplateRenderFailedEvent("haproxy.cfg", err.Error(), ""))
                } else {
                    c.publishRendered(output)
                }
            }
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}
```

## Validation Tests Ride the Config Objects

A configuration's `validationTests` live inline on the objects of the merged
set — one `HAProxyTemplateLibrary` per chart library plus the operator's own
`HAProxyTemplateConfig` — and
`conversion.MergeSpecs` unions them per source: a non-`_global` test name
defined by two objects is an error naming both, and the reserved `_global`
baseline accumulates. (The `HAProxyValidationTests` companion kind, its
selector, and `requireValidationTests` were retired unreleased — ADR-0016. The
empty-suite hazard they compensated for was a property of *discovery*, which
no longer exists: inline tests cannot silently vanish, because startup waits
for every `CRD_NAME` object.)

`len(ValidationTests) == 0` still short-circuits to success in the runner, so
never convert a fetch/merge failure into an empty set — `MergeSpecs` errors
instead, and the loader keeps the previously published config.

## Utility Components Pattern

Not all dependencies require event coordination. The controller uses both **pure components** and **utility components**:

### Pure Components (Event Adapters Required)

Pure components contain domain business logic and must be wrapped in event adapters:

- `pkg/templating`: Template rendering
- `pkg/dataplane`: HAProxy synchronization
- `pkg/k8s`: Kubernetes resource watching

Example — a hypothetical event-adapter that wraps the `templating.Engine`:

```go
// Illustrative — not a real package. Shows the event-adapter shape.
type Component struct {
    engine    templating.Engine    // Pure component
    eventBus  *events.EventBus
    eventChan <-chan events.Event  // Subscribed in constructor
}

func New(bus *events.EventBus, engine templating.Engine) *Component {
    return &Component{
        engine:    engine,
        eventBus:  bus,
        eventChan: bus.Subscribe("examplerenderer", 100),  // Subscribe in constructor
    }
}
```

### Utility Components (Direct Calls Allowed)

Utility components provide infrastructure services and can be called directly without events:

- **EventBus**: Event infrastructure (`pkg/events`)
- **Metrics**: Prometheus metrics (`pkg/controller/metrics`)
- **RestMapper**: Kubernetes API mapping (`k8s.io/apimachinery/pkg/api/meta`)

Example - DryRunValidator is itself called directly by the webhook:

```go
// pkg/controller/dryrunvalidator/component.go
type Component struct {
    proposalValidator *proposalvalidator.Component // Pure component (delegates render+validate)
    // ... config, engine, testRunner, logger ...
}

// pkg/controller/webhook calls this synchronously per admission request.
func (c *Component) ValidateDirect(ctx context.Context, gvk, namespace, name string, object any, operation string) (allowed bool, reason string, warnings []string) {
    // Build a *stores.StoreOverlay representing the admission request, hand it to
    // proposalValidator.ValidateSync, return a flat allow/deny answer. No event hop.
}
```

### When to Use Direct Calls vs Events

**Direct calls are acceptable for:**

1. Utility/infrastructure components (StoreManager, Metrics, RestMapper)
2. Pure components within a single reconciliation context (DryRunValidator renders templates)
3. Synchronous operations that don't need coordination
4. Performance-critical paths where event overhead is unacceptable

**Events are required for:**

1. Cross-component coordination (Reconciler → Coordinator → Deployer)
2. Scatter-gather operations (multiple validators responding)
3. Asynchronous workflows
4. Observability needs (commentator logs all events)

### Guiding principle: local consistency, principle of least surprise

The choice between events and direct calls is per-call-site, not a global rule. Pick whichever makes the *local* path obviously correct to a reader who lands on it cold:

- A call site whose surrounding code is event-driven and whose work is asynchronous (timer-driven, multi-subscriber, decouples latency) should use events. Carving out a single direct-call hop in an otherwise event-driven module is more surprising than the asymmetry would be.
- A call site whose surrounding code is synchronous and whose work is single-participant (one publisher, one subscriber, closed loop) should be a direct call. Wrapping it in events for symmetry adds latency, parallel code paths, and an event hop that readers must trace before realising it's plumbing.

ADR-0001 (renderer) and ADR-0006 (httpstore↔proposalvalidator) settle the same question in opposite directions for the same reason: each call site got the shape its local context required.

### Adding New Components

When creating a new component, ask:

1. **Does it contain domain business logic?**
   - YES → Create as pure component in `pkg/` + event adapter in `pkg/controller/`
   - NO → Consider if it's infrastructure/utility

2. **Will multiple components need to observe/react to it?**
   - YES → Use events for coordination
   - NO → Direct calls may be sufficient

3. **Is it synchronous infrastructure?**
   - YES → Create as utility component, allow direct calls
   - NO → Use event-driven pattern

Document the decision in the component's CLAUDE.md file.

## Sub-Package Guidelines

### events/ - Domain Event Catalog

All domain-specific event types live here:

```go
// pkg/controller/events/types.go
package events

import "haptic/pkg/events"

// Lifecycle events
type ControllerStartedEvent struct {
    ConfigVersion string
}
func (e ControllerStartedEvent) EventType() string { return "controller.started" }

// Configuration events
type ConfigParsedEvent struct {
    Config  Config
    Version string
}
func (e ConfigParsedEvent) EventType() string { return "config.parsed" }

// ~50 more event types...
```

**When adding new event:**

1. Define struct with event data
2. Implement EventType() method
3. Document when event is published
4. Decide the commentator's treatment (insight case, level arm, or the generic fallback)
5. Add to relevant component tests

### commentator/ - Event Observability

Subscribes to all events and produces domain-aware logs:

```go
// pkg/controller/commentator/commentator.go
// Subscription happens in NewEventCommentator (constructor), not in Start.
func (c *EventCommentator) Start(ctx context.Context) error {
    for {
        select {
        case event := <-c.eventChan:  // c.eventChan subscribed in constructor via SubscribeLossy
            c.ringBuffer.Add(event)  // Track recent events

            // Domain-aware logging
            switch e := event.(type) {
            case *events.ConfigValidatedEvent:
                c.logger.Info("configuration validated successfully",
                    "version", e.Version,
                )

            case *ReconciliationStartedEvent:
                // Add contextual insights -- there's no FindLast helper,
                // pull the most recent matching event out of FindByTypeInWindow.
                prior := c.ringBuffer.FindByTypeInWindow(EventTypeReconciliationStarted, time.Minute)
                if len(prior) > 0 {
                    last := prior[len(prior)-1]
                    timeSince := e.Timestamp().Sub(last.Timestamp())
                    c.logger.Info("reconciliation started",
                        "trigger", e.Trigger,
                        "since_last", timeSince,
                    )
                }

            // ~50 more event types with rich context...
            }
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}
```

**When to update commentator:**

- New event type added → Add logging case
- Need better insights → Add ring buffer correlation
- Performance issues → Reduce log verbosity

### validator/ - Configuration Validation

Implements the scatter-gather pattern for multi-phase validation. Three concrete validators (`BasicValidator`, `TemplateValidator`, `JSONPathValidator`) all wrap a shared `BaseValidator` and subscribe to `events.ConfigValidationRequest`. The orchestration that *issues* those requests does **not** live in this package — it's `pkg/controller/configchange.ConfigChangeHandler`, which subscribes to `ConfigParsedEvent` from the configloader, fans out via `bus.Request`, and publishes `ConfigValidatedEvent` / `ConfigInvalidEvent` based on the responses.

```go
// configchange/handler.go (orchestration side)
result, err := h.eventBus.Request(ctx, events.NewConfigValidationRequest(cfg, version),
    busevents.RequestOptions{
        Timeout:            10 * time.Second,
        ExpectedResponders: h.validators, // ["basic", "template", "jsonpath"]
    })
// aggregate result.Responses → ConfigValidatedEvent or ConfigInvalidEvent

// validator/template.go (responder side)
func NewTemplateValidator(eventBus *busevents.EventBus, logger *slog.Logger) *TemplateValidator {
    // BaseValidator subscribes the component to ConfigValidationRequest
    // and dispatches to the validator's HandleRequest method.
    v := &TemplateValidator{eventBus: eventBus, logger: logger}
    v.BaseValidator = NewBaseValidator(eventBus, logger, ValidatorNameTemplate, v)
    return v
}
```

When adding a new validator, register it in the `ConfigChangeHandler.validators` list so the scatter-gather waits for its response.

### reconciler/ - Reconciliation Trigger

Triggers reconciliation events immediately on every change (Stage 5 component 1):

```go
// pkg/controller/reconciler/reconciler.go
type Reconciler struct {
    eventBus  *busevents.EventBus
    eventChan <-chan busevents.Event // Subscribed in New()
    logger    *slog.Logger
}

func New(eventBus *busevents.EventBus, logger *slog.Logger) *Reconciler {
    // Subscribe BEFORE bus.Start() runs, narrowed to the event types we handle
    // so the buffer doesn't fill with traffic for other components.
    eventChan := eventBus.SubscribeTypes(ComponentName, EventBufferSize,
        events.EventTypeResourceIndexUpdated,
        events.EventTypeIndexSynchronized,
        events.EventTypeHTTPResourceUpdated,
        events.EventTypeHTTPResourceAccepted,
        events.EventTypeDriftPreventionTriggered,
        events.EventTypeBecameLeader,
    )
    return &Reconciler{eventBus: eventBus, eventChan: eventChan, logger: logger}
}

func (r *Reconciler) Start(ctx context.Context) error {
    for {
        select {
        case event := <-r.eventChan:
            // handleEvent dispatches on the concrete event type and fires a
            // ReconciliationTriggeredEvent immediately (the initial-sync
            // variant of ResourceIndexUpdatedEvent is the only one filtered
            // out, since IndexSynchronizedEvent covers the bulk load).
            r.handleEvent(event)

        case <-ctx.Done():
            return nil
        }
    }
}
```

**Features:**

- Fires a reconciliation immediately on every event — no reconciler-level debounce, no refractory window, no timer. Batching of rapid changes is the per-watcher debounce window's job (`types.DefaultDebounceInterval`, currently 2s; EndpointSlice watchers run at `debounceInterval: "0"`). Reload throttling is the deployer's `minDeploymentInterval`. There is no `spec.controller.reconciliationDebounceInterval` CRD knob and no `reconciler.Config`.
- Triggers immediate reconciliation when all indices are synchronized
- Filters initial sync events to prevent premature reconciliation
- Publishes ReconciliationTriggeredEvent

### Coordinator (reconciler/coordinator.go) - Pipeline Orchestrator

Orchestrates the render-validate pipeline directly (Stage 5 component 2):

```go
// pkg/controller/reconciler/coordinator.go
type Coordinator struct {
    eventBus      *busevents.EventBus
    eventChan     <-chan busevents.Event  // Subscribed in Start() (leader-only pattern)
    pipeline      PipelineExecutor
    storeProvider stores.StoreProvider
    currentFiles  CurrentFilesAuthority
    logger        *slog.Logger
}

func (c *Coordinator) handleReconciliationTriggered(ctx context.Context, event *events.ReconciliationTriggeredEvent, generation uint64) {
    if context.Cause(ctx) != nil {
        return
    }
    // Publish reconciliation started
    c.eventBus.Publish(events.NewReconciliationStartedEvent(event.Reason))

    // Execute pipeline directly (synchronous call)
    currentFiles, err := c.currentFiles.Snapshot(generation)
    if err != nil {
        c.handlePipelineFailure(ctx, err, event, startTime)
        return
    }
    result, err := c.pipeline.Execute(ctx, c.storeProvider, rendercontext.RenderModeReconcile,
        rendercontext.WithCurrentAuxFiles(currentFiles))
    if context.Cause(ctx) != nil {
        return
    }
    if err != nil {
        c.handlePipelineFailure(ctx, err, event, startTime)
        return
    }

    c.currentFiles.Accept(generation, result.AuxiliaryFiles)
    c.handlePipelineSuccess(ctx, result, event, startTime)
}
```

**Implementation:**

- Subscribes to ReconciliationTriggeredEvent in Start() (leader-only pattern)
- Calls Pipeline.Execute() synchronously (no event-driven render/validate flow)
- Advances `currentFiles` synchronously after validation, scoped to the active leader term
- Bootstraps all-replica `currentFiles` only from a completely resolved `HAProxyCfg` auxiliary reference set, including certificate Secret metadata, and fails closed on ambiguous legacy changes or a post-modern set-ID loss
- Publishes TemplateRenderedEvent + ValidationCompletedEvent for downstream components
- Publishes ReconciliationCompletedEvent or ReconciliationFailedEvent based on outcome
- Uses structured PipelineError for phase detection via errors.As()

## Staged Startup Pattern

The controller uses an eight-stage startup sequence coordinated via events (Stage 5 = reconciliation + observability, Stage 6 = leader election, Stage 7 = webhook HTTPS server, Stage 8 = debug-variable + health-checker wiring; all stage labels are logged in `iteration.go` / `infrastructure.go`). The entry point is the package-level function `controller.Run` (no `Controller` struct); each iteration is `pkg/controller/iteration.go`. Below is the *shape* of the first six stages — read `iteration.go` for the canonical wiring (including Stage 7/8) plus the constructor signatures, error handling, and leader-only gating that this sketch elides.

```go
// pkg/controller/iteration.go (sketch — see source for the real thing)
func runIteration(ctx context.Context, k8sClient *client.Client, ...) error {
    bus := busevents.NewEventBus(busBufferSize)

    // Stage 1: Config management — every component subscribes to its events
    // *during construction*. bus.Start() does NOT happen here — it's deferred
    // until the very end so all components (including the Stage 5 ones below)
    // are subscribed before the pre-start buffer is released.
    configLoader := configloader.NewConfigLoaderComponent(bus, logger)
    credentialsLoader := credentialsloader.NewCredentialsLoaderComponent(bus, logger)
    validator.NewBasicValidator(bus, logger)      // BaseValidator subscribes
    validator.NewTemplateValidator(bus, logger)
    validator.NewJSONPathValidator(bus, logger)
    handler := configchange.NewConfigChangeHandler(bus, logger, configChangeCh,
        []string{"basic", "template", "jsonpath"}, 0 /* default reinit debounce */)

    // Launch the Stage 1 background loops *now*, well before bus.Start().
    // Each Start() blocks on its own pre-subscribed channel, which the bus
    // doesn't drain until bus.Start() runs — so these goroutines park
    // harmlessly until then. In the real code (controller.go:setupComponents)
    // they're spawned through an errgroup so a failed Start() cancels the
    // iteration; this sketch elides the errgroup for brevity.
    go configLoader.Start(iterCtx)
    go credentialsLoader.Start(iterCtx)
    go handler.Start(iterCtx)

    // Stage 2: synchronously fetch + validate the CRDs/Secret before continuing.
    // --crd-name is ONE name. The named HAProxyTemplateConfig is fetched, its
    // ordered spec.libraryRefs are resolved into HAProxyTemplateLibrary
    // objects, and the set is merged later-wins by conversion.MergeSpecs. The
    // whole pipeline below (ValidateStructure, effective-config resolution, the
    // load gate) runs on the merged result. Startup waits for the config AND
    // every referenced library at the revision it names — a partial set is as
    // unusable as none. See ADR-0017.
    cfg, creds := fetchAndValidate(ctx, k8sClient, ...)

    // Stage 3: watch each spec.watchedResources entry; wait for initial sync.
    rw := resourcewatcher.New(bus, cfg, ...)
    go rw.Start(iterCtx)
    rw.WaitForAllSync(ctx)

    // Stage 4 sits inside Stage 3's WaitForAllSync.

    // Stage 5: reconciliation + observability components — also subscribe
    // during construction.
    reconciler.New(bus, logger)
    reconciler.NewCoordinator(&reconciler.CoordinatorConfig{
        EventBus: bus, Pipeline: pipeline, StoreProvider: storeProvider, Logger: logger,
    })
    // … plus deployer, discovery, metrics, commentator, debug HTTP server …

    // Now release the pre-start buffer — every subscriber is in place.
    bus.Start()

    // Stage 6: leader election (sets up the leader-only components, which
    // subscribe inside their Start methods after BecameLeaderEvent).
    setupLeaderElection(...)

    <-iterCtx.Done()  // until config change cancels the iteration or shutdown signal
    return nil
}
```

Key non-obvious points:

- **Subscribe in constructors**, not in `Run` / `Start`. `bus.Start()` flushes the pre-start buffer to whoever's subscribed *at that moment*; late subscribers miss buffered events. Every constructor in this tree obeys this rule via the shared `pkg/controller/component.Base` scaffold.
- **A critical drop ends the iteration.** The EventBus stays non-blocking, but a full non-lossy subscriber buffer cancels the iteration and reconstructs its state. Only `SubscribeLossy` observability consumers may drop and continue.
- **Leader-only components** (Coordinator, DeploymentScheduler, DriftMonitor, ConfigPublisher) subscribe inside `Start()` after `BecameLeaderEvent` — constructor subscription would have follower replicas fill their buffers (the inputs are published on every replica) and log critical drops. The Deployer is the exception among leader-only components: it embeds `component.Base` and subscribes at construction, which is safe because its event types (`deployment.scheduled`, `deployment.cancel.request`) are published only by the leader-only DeploymentScheduler, so follower buffers stay empty. StatusApplier is all-replica (constructor subscription, actions gated on an internal leader flag). All-replica components that hold state (Discovery → `HAProxyPodsDiscoveredEvent`, ConfigChangeHandler → `ConfigValidatedEvent`, both via `pkg/controller/leadership.NewStateReplayer`) re-publish their last state on `BecameLeaderEvent` so the late-subscribed leader-only components don't miss the events that landed during the leadership transition. The renderer is *not* in this list — it's a synchronous service driven by the leader-only Coordinator (ADR-0001), so the new leader's first reconciliation produces a fresh render rather than replaying a stale one.

**Why staged startup?**

1. **Prevents partial state**: Don't reconcile until all resources loaded
2. **Clear dependencies**: Each stage waits for previous stage
3. **Debuggable**: Clear log progression shows where startup blocked
4. **Testable**: Can test each stage independently

## Testing Strategies

### Testing Event Adapters

```go
// Illustrative — using the examplerenderer skeleton from above.
func TestExampleRenderer(t *testing.T) {
    bus := busevents.NewEventBus(100)
    engine, _ := templating.New(testTemplates, nil)
    component := examplerenderer.New(bus, engine)

    // Subscribe to output events BEFORE starting the bus
    eventChan := bus.Subscribe("test", 10)
    bus.Start()

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    // Start component
    go component.Start(ctx)

    // Trigger event
    bus.Publish(events.NewReconciliationTriggeredEvent("test", true))

    // Verify response event
    select {
    case event := <-eventChan:
        if rendered, ok := event.(*events.TemplateRenderedEvent); ok {
            assert.Contains(t, rendered.HAProxyConfig, "expected haproxy config")
        } else {
            t.Fatalf("expected *TemplateRenderedEvent, got %T", event)
        }
    case <-time.After(1 * time.Second):
        t.Fatal("timeout waiting for render event")
    }
}
```

### Testing Scatter-Gather Validation

```go
func TestConfigChangeHandler_ScatterGather(t *testing.T) {
    bus := busevents.NewEventBus(100)
    logger := slog.Default()

    // Wire all three validators (each subscribes to ConfigValidationRequest
    // via its embedded BaseValidator).
    validator.NewBasicValidator(bus, logger)
    validator.NewTemplateValidator(bus, logger)
    validator.NewJSONPathValidator(bus, logger)

    // The orchestrator that fans out the request and aggregates responses.
    configChangeCh := make(chan *coreconfig.Config, 1)
    handler := configchange.NewConfigChangeHandler(bus, logger, configChangeCh,
        []string{"basic", "template", "jsonpath"}, 0 /* default reinit debounce */)

    bus.Start()
    go handler.Start(ctx)

    // Trigger validation
    bus.Publish(events.NewConfigParsedEvent(validConfig, templateConfig, "v1", ""))

    // Verify the validated config flows through the channel
    select {
    case cfg := <-configChangeCh:
        require.NotNil(t, cfg)
    case <-time.After(2 * time.Second):
        t.Fatal("validation timeout")
    }
}
```

## Common Pitfalls

### Putting Business Logic in Event Adapters

**Problem**: Event adapter contains complex logic.

```go
// Bad - business logic in adapter
func (c *Component) Start(ctx context.Context) error {
    for event := range eventChan {
        if req, ok := event.(ReconciliationTriggeredEvent); ok {
            // Complex template processing logic (50 lines)
            output := complexTemplateProcessing(req.Config)
            c.eventBus.Publish(RenderCompletedEvent{Output: output})
        }
    }
}
```

**Solution**: Extract to pure component.

```go
// Good - delegate to pure component
func (c *Component) Start(ctx context.Context) error {
    for event := range eventChan {
        if req, ok := event.(ReconciliationTriggeredEvent); ok {
            // Adapter just coordinates
            output, err := c.renderer.Process(req.Config)
            if err != nil {
                c.eventBus.Publish(RenderFailedEvent{Error: err})
            } else {
                c.eventBus.Publish(RenderCompletedEvent{Output: output})
            }
        }
    }
}
```

### Event Type in Wrong Package

**Problem**: Domain events in `pkg/events` instead of `pkg/controller/events`.

```go
// Wrong location
pkg/events/types.go:
    type ReconciliationTriggeredEvent struct { ... }
```

**Solution**: Domain events belong in controller.

```go
// Correct location
pkg/controller/events/types.go:
    type ReconciliationTriggeredEvent struct { ... }
```

### Not Using Scatter-Gather for Validation

**Problem**: Manual timeout management for multi-validator coordination.

```go
// Bad - manual coordination
func (v *Coordinator) validate(config Config) bool {
    responses := make(map[string]bool)
    timeout := time.After(10 * time.Second)

    // Publish validation request
    v.bus.Publish(ValidationRequest{config: config})

    // Manually collect responses
    for len(responses) < 3 {
        select {
        case event := <-v.eventChan:
            if resp, ok := event.(ValidationResponse); ok {
                responses[resp.Validator] = resp.Valid
            }
        case <-timeout:
            return false
        }
    }

    return allTrue(responses)
}
```

**Solution**: Use EventBus.Request() scatter-gather.

```go
// Good - use built-in scatter-gather
func (v *Coordinator) validate(ctx context.Context, config Config) bool {
    req := NewValidationRequest(config)

    result, err := v.bus.Request(ctx, req, events.RequestOptions{
        Timeout:            10 * time.Second,
        ExpectedResponders: []string{"basic", "template", "jsonpath"},
    })

    if err != nil {
        return false
    }

    return allResponsesValid(result.Responses)
}
```

### Forgetting to Add Commentator Logging

**Problem**: New event added but commentator doesn't log it.

**Solution**: Always update commentator when adding events.

```go
// pkg/controller/commentator/commentator.go
func (c *EventCommentator) Start(ctx context.Context) error {
    for {
        select {
        case event := <-c.eventChan:
            switch e := event.(type) {
            // ... existing cases ...

            case NewEventType:  // Add case for new event
                c.logger.Info("new event occurred",
                    "field1", e.Field1,
                    "field2", e.Field2,
                )
            }
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}
```

## Adding New Components

### Checklist

1. **Identify pure component**: What business logic do you need? (e.g., `pkg/templating`)
2. **Define events**: What events trigger this component? What events does it publish?
3. **Create event adapter**: Wrap pure component in controller package
4. **Add to startup**: Integrate into staged startup sequence
5. **Decide the commentator's treatment**: an insight case, a level arm, or the generic fallback
6. **Write tests**: Test event adapter behavior
7. **Update README.md**: Document new component

### Example: Adding Cache Warming Component

```go
// Step 1: Pure component exists (pkg/cache)
package cache
func (c *Cache) Warm(keys []string) error { ... }

// Step 2: Define events (pkg/controller/events)
type CacheWarmingTriggeredEvent struct {}
type CacheWarmingCompletedEvent struct { Count int }

// Step 3: Create adapter (pkg/controller/cache)
package cache

type CacheWarmerComponent struct {
    cache     *cache.Cache
    eventBus  *events.EventBus
    eventChan <-chan events.Event // subscribed in New(), before bus.Start()
}

// NewCacheWarmerComponent subscribes during construction. Every component MUST
// subscribe before EventBus.Start() flushes the pre-start buffer — subscribing
// in Start() would miss events buffered before the goroutine runs (see the
// "Subscribe in constructors, not in Start()" rule above).
func NewCacheWarmerComponent(c *cache.Cache, bus *events.EventBus) *CacheWarmerComponent {
    return &CacheWarmerComponent{
        cache:     c,
        eventBus:  bus,
        eventChan: bus.Subscribe("cache-warmer", 50),
    }
}

func (c *CacheWarmerComponent) Start(ctx context.Context) error {
    for {
        select {
        case event := <-c.eventChan:
            if _, ok := event.(CacheWarmingTriggeredEvent); ok {
                keys := c.extractKeys()
                err := c.cache.Warm(keys)

                if err != nil {
                    c.eventBus.Publish(CacheWarmingFailedEvent{Error: err})
                } else {
                    c.eventBus.Publish(CacheWarmingCompletedEvent{Count: len(keys)})
                }
            }
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}

// Step 4: Add to controller.go startup
warmer := cache.NewCacheWarmerComponent(c.cache, c.eventBus)
go warmer.Start(ctx)

// Step 5: Update commentator
case CacheWarmingCompletedEvent:
    c.logger.Info("cache warming completed", "keys", e.Count)
```

## Event Coordination Patterns

### Debounced Reconciliation

```go
type ReconciliationComponent struct {
    eventBus  *events.EventBus
    debouncer *time.Timer
    interval  time.Duration
}

func (r *ReconciliationComponent) Start(ctx context.Context) error {
    eventChan := r.eventBus.Subscribe("reconciliation", 100)

    for {
        select {
        case event := <-eventChan:
            if _, ok := event.(ResourceIndexUpdatedEvent); ok {
                // Reset debounce timer on each change
                r.debouncer.Reset(r.interval)
            }

        case <-r.debouncer.C:
            // Timer expired, trigger reconciliation
            r.eventBus.Publish(ReconciliationTriggeredEvent{
                Reason: "debounce_timer",
            })

        case <-ctx.Done():
            return ctx.Err()
        }
    }
}
```

### Conditional Event Publishing

```go
// Publish different events based on result
output, err := c.engine.Render(template, context)
if err != nil {
    c.eventBus.Publish(RenderFailedEvent{
        Template: template,
        Error:    err.Error(),
    })
} else {
    c.eventBus.Publish(RenderCompletedEvent{
        Template: template,
        Output:   output,
        Size:     len(output),
    })
}
```

### Event Filtering

```go
// Only handle events matching specific criteria
for event := range eventChan {
    if update, ok := event.(ResourceIndexUpdatedEvent); ok {
        // Only handle ingress updates
        if update.ResourceType == "ingress" {
            handleIngressUpdate(update)
        }
    }
}
```

### Scatter-Gather Pattern

The `EventBus.Request()` method implements scatter-gather for operations requiring coordinated responses from multiple components.

**Current production usage:**

- **Config validation** — `pkg/controller/configchange.ConfigChangeHandler` fans `ConfigValidationRequest` out to `BasicValidator`, `TemplateValidator`, and `JSONPathValidator` (all under `pkg/controller/validator`) and aggregates the `ConfigValidationResponse` events into `ConfigValidatedEvent` / `ConfigInvalidEvent`.

The admission webhook does **not** use scatter-gather. It calls `dryrunvalidator.Component.ValidateDirect` synchronously to keep the request path tight (see `pkg/controller/dryrunvalidator/README.md`). ADR-0001 records the same shape for the renderer; both removals follow the same principle — no event hop where there is no second participant.

**When to Use Scatter-Gather:**

1. **Need responses from multiple components** - validation where multiple validators must respond
2. **Responses must be correlated** - matching responses to the original request
3. **Timeout handling required** - can't wait forever for responses
4. **Parallel processing** - all responders process the request simultaneously

**When NOT to Use Scatter-Gather:**

- Fire-and-forget notifications (use Publish)
- Single responder (use direct function call)
- High-frequency operations on the request hot path (the per-request synchronisation overhead is real — webhook chose `ValidateDirect` for this reason)
- Uncoordinated observers (use regular Subscribe)

**Example — config validation (the live scatter-gather caller):**

```go
// Requester: configchange.ConfigChangeHandler fans the request out
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

// Responder: each validator subscribes via the shared BaseValidator,
// runs its check, then publishes a ConfigValidationResponse with the
// matching request ID.
```

**Potential Future Usage:**

- Multi-stage HAProxy validation (syntax → semantic → connectivity)
- Pre-deployment readiness checks (all pods ready, services healthy)
- Distributed configuration queries (gather state from multiple stores)

## Leadership Transition Patterns

### The "Late Subscriber Problem"

When leadership transitions occur, leader-only components start subscribing AFTER critical state events have already been published. This creates event ordering bugs where leader-only components miss essential state.

**Example timeline:**

```
14:03:29 - All-replica: Discovery publishes HAProxyPodsDiscoveredEvent
14:03:30 - All-replica: Renderer publishes TemplateRenderedEvent
14:03:31 - All-replica: Validator publishes ValidationCompletedEvent
         ↓
14:05:04 - Leader election completes
14:05:05 - Leader-only: DeploymentScheduler starts subscribing
         ↓
         ❌ DeploymentScheduler never receives critical events
         ❌ Deployment deadlocked forever
```

### Solution 1: State Replay on BecameLeaderEvent

All-replica components that maintain state must re-publish their last state when a new leader is elected.

**Pattern:**

```go
// All-replica component (Renderer, Validator, Discovery, etc.)
type Component struct {
    eventBus *busevents.EventBus
    logger   *slog.Logger

    // State protected by mutex
    mu         sync.RWMutex
    lastState  State
    hasState   bool
}

func (c *Component) handleEvent(event busevents.Event) {
    switch e := event.(type) {
    case *events.BecameLeaderEvent:
        c.handleBecameLeader(e)
    // ... other cases ...
    }
}

func (c *Component) handleBecameLeader(_ *events.BecameLeaderEvent) {
    c.mu.RLock()
    hasState := c.hasState
    state := c.lastState
    c.mu.RUnlock()

    if !hasState {
        c.logger.Debug("became leader but no state available yet, skipping state replay")
        return
    }

    c.logger.Info("became leader, re-publishing last state for leader-only components",
        "state_size", len(state))

    // Re-publish the last state event
    c.eventBus.Publish(events.NewStateEvent(state))
}

// Cache state when publishing normally
func (c *Component) handleWork(event *events.WorkEvent) {
    // ... perform work ...

    result := processWork(event)

    // Cache result for leadership transition replay
    c.mu.Lock()
    c.lastState = result
    c.hasState = true
    c.mu.Unlock()

    // Publish normally
    c.eventBus.Publish(events.NewStateEvent(result))
}
```

**Implemented in** (see `pkg/controller/leadership.NewStateReplayer[T]` for the helper they all use; line numbers drift, grep for `handleBecameLeader`):

- `pkg/controller/discovery/handlers.go` — re-publishes `HAProxyPodsDiscoveredEvent`
- `pkg/controller/configchange/handler.go` — re-publishes `ConfigValidatedEvent`

There is no renderer *component* (ADR-0001) — `renderer.RenderService` runs synchronously inside the leader-only Coordinator's `Pipeline.Execute`. So no separate subscription exists to be late, and no `TemplateRenderedEvent` replay is needed. Instead the reconciler triggers a fresh reconciliation on `BecameLeaderEvent` and the new leader's pipeline produces a current render rather than replaying a stale one.

### Solution 2: State Cleanup on LostLeadershipEvent

Leader-only components must clean up state when losing leadership to prevent deadlocks.

**Pattern:**

```go
// Leader-only component (DeploymentScheduler, DriftMonitor, etc.)
type Component struct {
    eventBus *busevents.EventBus
    logger   *slog.Logger

    // State protected by mutex
    mu          sync.Mutex
    inProgress  bool
    pendingWork *Work
    timer       *time.Timer
}

func (c *Component) handleEvent(event busevents.Event) {
    switch e := event.(type) {
    case *events.LostLeadershipEvent:
        c.handleLostLeadership(e)
    // ... other cases ...
    }
}

func (c *Component) handleLostLeadership(_ *events.LostLeadershipEvent) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if c.inProgress || c.pendingWork != nil {
        c.logger.Info("lost leadership, clearing component state",
            "in_progress", c.inProgress,
            "has_pending", c.pendingWork != nil)
    }

    // Clear in-progress flags to prevent deadlocks
    c.inProgress = false
    c.pendingWork = nil

    // Stop timers to prevent leaked goroutines
    if c.timer != nil {
        c.timer.Stop()
    }

    // Note: Historical data like lastCompletionTime can be kept for rate limiting
}
```

**Implemented in** (line numbers drift; grep for `handleLostLeadership`):

- `pkg/controller/deployer/scheduler_handlers.go` (`(*DeploymentScheduler).handleLostLeadership`) — clears in-progress flags and pending deployment work
- `pkg/controller/deployer/drift_monitor.go` (`(*DriftPreventionMonitor).handleLostLeadership`) — stops the drift timer

### Checklist for New Components

**For all-replica components that maintain state:**

- [ ] Cache last successful state with `sync.RWMutex`
- [ ] Include `hasState bool` to distinguish "no state" from "zero state"
- [ ] Subscribe to `BecameLeaderEvent`
- [ ] Re-publish last state in `handleBecameLeader()`
- [ ] Check `hasState` before replaying (don't publish uninitialized state)

**For leader-only components:**

- [ ] Subscribe to `LostLeadershipEvent`
- [ ] Clear in-progress flags in `handleLostLeadership()`
- [ ] Stop timers/goroutines to prevent leaks
- [ ] Clear transient state (but keep historical data like timestamps)

**For both:**

- [ ] Document state dependencies in component CLAUDE.md
- [ ] Add component to `LEADER_ONLY_COMPONENTS.md` checklist
- [ ] Test leadership transitions manually
- [ ] Log state replay and cleanup events for debugging

### Testing Leadership Transitions

```bash
# Deploy with 2 replicas
kubectl -n haptic scale deployment haptic-controller --replicas=2

# Delete current leader to trigger election
LEADER=$(kubectl -n haptic get pods -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller -o jsonpath='{.items[0].metadata.name}')
kubectl -n haptic delete pod $LEADER

# Expected log pattern after transition:
# 14:05:04.123 | INFO | Became leader
# 14:05:04.124 | INFO | became leader, re-discovering HAProxy pods for deployment scheduler
# 14:05:04.125 | INFO | became leader, re-publishing last rendered config
# 14:05:04.126 | INFO | became leader, re-publishing last validation result (success)
# 14:05:04.127 | INFO | scheduling deployment | endpoint_count=2
```

## Resources

- Event infrastructure: `pkg/events/CLAUDE.md`
- Package organization: `pkg/CLAUDE.md`
- Leader election: `pkg/controller/leaderelection/CLAUDE.md`
- Leadership transition guidelines: `pkg/controller/LEADER_ONLY_COMPONENTS.md`
- Metrics component: `pkg/controller/metrics/CLAUDE.md`
- Architecture: `/docs/site/docs/development/design.md`
- API documentation: `pkg/controller/README.md`
