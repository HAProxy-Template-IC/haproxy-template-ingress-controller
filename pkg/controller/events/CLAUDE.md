# pkg/controller/events - Domain Event Types

Development context for domain-specific event type definitions.

**API Documentation**: See `pkg/controller/events/README.md`

## When to Work Here

Modify this package when:

- Adding new event types for controller coordination
- Modifying existing event structures
- Documenting event contracts and usage

**DO NOT** modify this package for:

- Event infrastructure (publish/subscribe) → Use `pkg/events`
- Business logic → Use appropriate domain package
- Event handling → Use controller components (reconciler, executor, etc.)

## Package Purpose

Defines all domain-specific event types used for controller coordination. This is the catalog of events that flow through the EventBus to coordinate controller components.

**Separation of Concerns**:

- `pkg/events` - Generic pub/sub infrastructure (domain-agnostic)
- `pkg/controller/events` - Domain event types (controller-specific)

## File Organization

Events are organized into separate files by category:

| File | Description |
|------|-------------|
| `types.go` | Event type constants and package documentation |
| `config.go` | HAProxyTemplateConfig CRD changes and validation events |
| `resource.go` | Kubernetes resource indexing events |
| `reconciliation.go` | Orchestration lifecycle events |
| `template.go` | Template rendering events |
| `validation.go` | Three-phase HAProxy validation events |
| `deployment.go` | HAProxy deployment events |
| `discovery.go` | HAProxy pod discovery events |
| `credentials.go` | Credentials loading and validation events |
| `leader.go` | Leader election events |
| `publishing.go` | Config publishing events (includes SyncMetadata types) |
| `certificate.go` | Webhook certificate events |
| `http.go` | HTTP resource events |
| `status.go` | Status patch application events |
| `proposal.go` | Proposal validation request/response events (used by both webhook and HTTP store) |
| `correlation.go` | Correlation ID helpers for tracing events |
| `timestamped.go` | Embedded `timestamped` mixin used by all events |
| `internal_copy.go` | Internal helpers for defensive slice/map copying |
| `runtime_fast_path.go` | Runtime fast-path result events |

## Event Categories

Events are organized by lifecycle phase:

1. **Configuration Events** (`config.go`) - HAProxyTemplateConfig CRD changes and validation
2. **Resource Events** (`resource.go`) - Kubernetes resource indexing
3. **Reconciliation Events** (`reconciliation.go`) - Orchestration lifecycle
4. **Template Events** (`template.go`) - Template rendering
5. **Validation Events** (`validation.go`) - Three-phase HAProxy validation
6. **Deployment Events** (`deployment.go`) - HAProxy deployment
7. **HAProxy Pod Events** (`discovery.go`) - Pod discovery
8. **Credentials Events** (`credentials.go`) - Credentials management
9. **Leader Election Events** (`leader.go`) - Leadership transitions
10. **Publishing Events** (`publishing.go`) - Config publishing (includes auxiliary file sync metadata)
11. **Certificate Events** (`certificate.go`) - Webhook certificates
12. **HTTP Resource Events** (`http.go`) - HTTP resource management
13. **Proposal Events** (`proposal.go`) - Speculative validation of hypothetical configs (used by both the admission webhook's synchronous `ValidateDirect` path and the HTTP store's content-validation cycle)
14. **Status Events** (`status.go`) - Kubernetes status patch results

## Key Principles

### Immutability Contract

Events are immutable after creation. For value-typed payloads (small structs
with no pointers, like `types.ChangeStats`) a plain assignment is enough — Go
copies the value. For slices and maps, the constructor must allocate a fresh
backing array/map and copy into it so the publisher can't mutate the event
after `bus.Publish`.

```go
// Value-typed payload: assignment is the defensive copy.
func NewResourceIndexUpdatedEvent(resourceTypeName string, changeStats types.ChangeStats) *ResourceIndexUpdatedEvent {
    return &ResourceIndexUpdatedEvent{
        ResourceTypeName: resourceTypeName,
        ChangeStats:      changeStats, // small struct, no pointers
        timestamped:      newTimestamped(),
    }
}

// Slice payload: copy the backing array so the caller can't mutate it later.
func NewConfigInvalidEvent(version string, templateConfig any, validationErrors map[string][]string) *ConfigInvalidEvent {
    errsCopy := make(map[string][]string, len(validationErrors))
    for k, v := range validationErrors {
        errsCopy[k] = slices.Clone(v)
    }
    return &ConfigInvalidEvent{
        Version:          version,
        TemplateConfig:   templateConfig,
        ValidationErrors: errsCopy,
        timestamped:      newTimestamped(),
    }
}
```

**Enforcement**:

- Custom static analyzer detects parameter mutations
- Code review
- Documentation and team discipline

### Pointer Receivers

All events use pointer receivers for EventType():

```go
func (e *ConfigValidatedEvent) EventType() string {
    return EventTypeConfigValidated
}
```

**Why pointers?**

- Avoids copying large structs (200+ bytes)
- Follows Go best practices
- Consistent with Kubernetes API style

### Exported Fields

Event fields are exported for idiomatic Go access:

```go
type ConfigValidatedEvent struct {
    Config         any            // *config.Config (any to avoid circular deps)
    TemplateConfig any            // typed CRD wrapper (any to avoid circular deps)
    Version        string
    SecretVersion  string
    timestamped                   // mixin: provides Timestamp() time.Time
}
```

**Why exported?**

- JSON serialization support
- Idiomatic Go access
- Matches industry standards (Kubernetes, NATS)

## Usage Patterns

### Publishing Events

```go
// Real signature: NewConfigParsedEvent(config, templateConfig, version, secretVersion)
// templateConfig is the typed CRD wrapper; the two version strings let
// downstream subscribers correlate against the CRD's resourceVersion and
// the credentials Secret's resourceVersion independently.
bus.Publish(events.NewConfigParsedEvent(config, templateCfg, "1234", "5678"))
```

### Consuming Events

```go
// Component subscribes and handles events
eventChan := bus.Subscribe("consumer", 100)
for event := range eventChan {
    if validated, ok := event.(*events.ConfigValidatedEvent); ok {
        handleValidatedConfig(validated.Config)
    }
}
```

### Scatter-Gather (Validation)

```go
// Request event
req := events.NewConfigValidationRequest(config, "v1")
result, err := bus.Request(ctx, req, events.RequestOptions{
    Timeout:            10 * time.Second,
    ExpectedResponders: []string{"basic", "template", "jsonpath"},
})

// Response events
for _, resp := range result.Responses {
    if valResp, ok := resp.(*events.ConfigValidationResponse); ok {
        if !valResp.Valid {
            // Handle validation failure
        }
    }
}
```

## Common Event Types

### Configuration Events

```go
// Config parsed from the HAProxyTemplateConfig CRD (config.go)
ConfigParsedEvent{
    Config:         config,        // *config.Config (typed any to dodge import cycles)
    TemplateConfig: templateCfg,   // typed CRD wrapper
    Version:        "1234",        // CRD resourceVersion
    SecretVersion:  "5678",        // credentials Secret resourceVersion
}

// Config validated (all validators passed)
ConfigValidatedEvent{
    Config:         config,
    TemplateConfig: templateCfg,
    Version:        "1234",
    SecretVersion:  "5678",
}

// Config invalid (validation failed) — note the field is ValidationErrors,
// keyed by validator name, not a flat []string.
ConfigInvalidEvent{
    Version:          "1234",
    TemplateConfig:   templateCfg,
    ValidationErrors: map[string][]string{
        "template": {"line 12: unexpected '{%'"},
        "jsonpath": {"watched_resources.ingresses.index_by[0]: invalid expression"},
    },
}
```

### Resource Events

```go
// Resource index updated — the field is ResourceTypeName (not ResourceType),
// and there is no Changes slice; ChangeStats is the only payload.
ResourceIndexUpdatedEvent{
    ResourceTypeName: "ingresses",
    ChangeStats: types.ChangeStats{
        Created:       5,  // not "Added"
        Modified:      2,  // not "Updated"
        Deleted:       1,
        IsInitialSync: false,
    },
}

// All resources synchronized
IndexSynchronizedEvent{
    ResourceCounts: map[string]int{
        "ingresses": 5,
        "services":  12,
    },
}
```

### Reconciliation Events

```go
// Reconciliation triggered (debouncer / sync-complete signal)
ReconciliationTriggeredEvent{
    Reason: "config_change",
}

// Reconciliation started — the field is Trigger here, not Reason.
ReconciliationStartedEvent{
    Trigger: "config_change",
    // Timestamp is provided by the embedded `timestamped` mixin via Timestamp(),
    // not as an exported field on the event struct.
}

// Reconciliation completed
ReconciliationCompletedEvent{
    DurationMs: 1234,
}
```

### Status-patch carriage (deployment + failure events)

Four events carry a `StatusPatches []templating.StatusPatch` payload so the
`StatusApplier` is stateless — it reads patches directly from the event
that triggers the apply, with no side-channel cache:

| Event | Patches describe | Who populates | Applier writes variant |
|---|---|---|---|
| `DeploymentScheduledEvent` | the config this deploy will push | `DeploymentScheduler.scheduleOrQueue` from cached `lastValidatedStatusPatches` | (forwarded only — applier doesn't subscribe) |
| `DeploymentCompletedEvent` | the config this deploy just pushed | `Deployer.deployToEndpoints` (forwarded unchanged from the scheduled event) | `deployed` (if Total>0 && Succeeded>0) |
| `DeploymentSkippedEvent` | the config the data plane is already at | `DeploymentScheduler.handleValidationCompleted` (skip branch) | `deployed` (if Total>0) |
| `ReconciliationFailedEvent` | the LAST successful render's patches (failure paths produce no fresh patches) | `Coordinator.handlePipelineFailure` from `lastSuccessfulPatches` | `renderFailed` or `deployFailed` |

The defensive-copy contract (`slices.Clone` the outer slice in every
constructor) is mandatory — the deployment scheduler reuses
`s.lastValidatedStatusPatches` across reconciliation cycles, so a shared
backing array would silently mutate previously-published events still held
by subscribers (commentator ring buffer, debug-event dump). Tests pinning
this contract live next to each event's constructor (e.g.
`deployment_scheduled_test.go` `TestNew…_StatusPatchesDefensiveCopy`).

`TemplateRenderedEvent` already carried `StatusPatches` before the
stateless refactor; that pre-existing field is what the
`DeploymentScheduler` snapshots into `lastValidatedStatusPatches`.

### Rendered-resources carriage (ReconciliationCompletedEvent)

`ResourceApplier` follows the same stateless contract for full
Kubernetes resources rendered via the chart's top-level
`spec.k8sResources` map: the resources travel on the same event that
triggers the apply (`ReconciliationCompletedEvent.RenderedResources`),
never via a side-channel cache. The `Coordinator` populates the field
from `PipelineResult.RenderedResources` (which itself originates from
`TemplateRenderedEvent.RenderedResources` — `Pipeline.Execute` packs
them in synchronously); the applier reads from the event payload and
calls `applyAndPrune`. The same defensive-`slices.Clone` rule applies
to the constructor.

`TemplateRenderedEvent` already carries `RenderedResources` for the
synchronous reader (Coordinator → ResourceApplier hop via the
intermediate `ReconciliationCompletedEvent`). Both events carrying the
same slice intentionally — `TemplateRenderedEvent` is the renderer's
fan-out for any other subscriber that wants the raw render output;
`ReconciliationCompletedEvent` is the apply trigger.

## Adding New Event Types

### Checklist

1. **Add EventType constant** to `types.go`
2. **Choose appropriate category file** (or create new one if needed)
3. **Define event struct** with exported fields in the category file
4. **Implement EventType() method** with pointer receiver
5. **Create constructor** with defensive copying
6. **Document contract** (when published, who consumes)
7. **Update commentator** to log the event
8. **Add to README.md** event catalog

### Example

```go
// Step 1: Add constant to types.go
const EventTypeMyNew = "my.new"

// Step 2-5: Add to appropriate category file (e.g., config.go)

// Define struct
type MyNewEvent struct {
    Field1 string
    Field2 int
    Data   []string  // Will be copied in constructor
}

// Implement EventType()
func (e *MyNewEvent) EventType() string {
    return EventTypeMyNew
}

// Create constructor with defensive copy
func NewMyNewEvent(field1 string, field2 int, data []string) *MyNewEvent {
    // Defensive copy
    dataCopy := make([]string, len(data))
    copy(dataCopy, data)

    return &MyNewEvent{
        Field1: field1,
        Field2: field2,
        Data:   dataCopy,
    }
}

// Step 6: Document in README.md
// Step 7: Update pkg/controller/commentator
```

## Common Pitfalls

### Modifying Event Fields

**Problem**: Consumer mutates a slice/map field, affecting later subscribers.

```go
// Bad — mutates a slice that another subscriber is still iterating
event := <-eventChan
if invalid, ok := event.(*events.ConfigInvalidEvent); ok {
    invalid.ValidationErrors["template"] = append(
        invalid.ValidationErrors["template"], "extra error",
    ) // Mutation propagates to every other subscriber!
}
```

**Solution**: Treat the event as read-only. If you need to derive a new
collection, copy first.

```go
// Good — read-only
event := <-eventChan
if invalid, ok := event.(*events.ConfigInvalidEvent); ok {
    for validator, errs := range invalid.ValidationErrors {
        for _, e := range errs {
            log.Warn("config invalid", "validator", validator, "error", e)
        }
    }
}
```

### Forgetting Defensive Copy

**Problem**: Constructor doesn't copy slices/maps, allowing mutations.

```go
// Bad - no defensive copy
func NewMyEvent(data []string) *MyEvent {
    return &MyEvent{
        Data: data,  // Shares underlying array!
    }
}

// Caller can mutate after publishing
data := []string{"a", "b"}
event := NewMyEvent(data)
bus.Publish(event)
data[0] = "modified"  // Affects published event!
```

**Solution**: Always copy slices and maps in constructors.

```go
// Good - defensive copy
func NewMyEvent(data []string) *MyEvent {
    dataCopy := make([]string, len(data))
    copy(dataCopy, data)

    return &MyEvent{
        Data: dataCopy,  // Independent copy
    }
}
```

### Using Value Receiver

**Problem**: EventType() uses value receiver instead of pointer.

```go
// Bad - value receiver
func (e ConfigParsedEvent) EventType() string {
    return EventTypeConfigParsed
}
```

**Solution**: Always use pointer receivers.

```go
// Good - pointer receiver
func (e *ConfigParsedEvent) EventType() string {
    return EventTypeConfigParsed
}
```

## Resources

- Event infrastructure: `pkg/events/CLAUDE.md`
- Controller coordination: `pkg/controller/CLAUDE.md`
- Commentator (event logging): `pkg/controller/commentator/CLAUDE.md`
- Static analyzer: `tools/linters/eventimmutability/`
