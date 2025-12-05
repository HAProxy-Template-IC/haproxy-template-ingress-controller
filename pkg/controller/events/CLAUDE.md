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

## Event Categories

Events are organized by lifecycle phase:

1. **Lifecycle Events** - System startup/shutdown
2. **Configuration Events** - ConfigMap/Secret changes and validation
3. **Resource Events** - Kubernetes resource indexing
4. **Reconciliation Events** - Orchestration lifecycle
5. **Template Events** - Template rendering
6. **Validation Events** - Configuration validation
7. **Deployment Events** - HAProxy deployment
8. **Storage Events** - Auxiliary file sync
9. **HAProxy Pod Events** - Pod discovery

## Key Principles

### Immutability Contract

Events are immutable after creation:

```go
// Constructor performs defensive copying
func NewResourceIndexUpdatedEvent(resourceType string, changes []types.ResourceChange) *ResourceIndexUpdatedEvent {
    // Copy slice to prevent mutations
    changesCopy := make([]types.ResourceChange, len(changes))
    copy(changesCopy, changes)

    return &ResourceIndexUpdatedEvent{
        ResourceType: resourceType,
        Changes:      changesCopy,  // Safe from external modification
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
    Config  interface{}
    Version string
}
```

**Why exported?**

- JSON serialization support
- Idiomatic Go access
- Matches industry standards (Kubernetes, NATS)

## Usage Patterns

### Publishing Events

```go
// Component publishes event after action
config := loadConfig()
bus.Publish(events.NewConfigParsedEvent(config, "v1"))
```

### Consuming Events

```go
// Component subscribes and handles events
eventChan := bus.Subscribe(100)
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
// Config parsed from ConfigMap
ConfigParsedEvent{
    Config:  config,
    Version: "v1",
}

// Config validated (all validators passed)
ConfigValidatedEvent{
    Config:  config,
    Version: "v1",
}

// Config invalid (validation failed)
ConfigInvalidEvent{
    Version: "v1",
    Errors:  []string{"template syntax error"},
}
```

### Resource Events

```go
// Resource index updated
ResourceIndexUpdatedEvent{
    ResourceType: "ingresses",
    Changes:      []types.ResourceChange{...},
    ChangeStats:  types.ChangeStats{
        Added:   5,
        Updated: 2,
        Deleted: 1,
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
// Reconciliation triggered by config/resource change
ReconciliationTriggeredEvent{
    Reason: "config_change",
}

// Reconciliation started
ReconciliationStartedEvent{
    Reason:    "config_change",
    Timestamp: time.Now(),
}

// Reconciliation completed
ReconciliationCompletedEvent{
    DurationMs: 1234,
    Timestamp:  time.Now(),
}
```

## Adding New Event Types

### Checklist

1. **Define event struct** with exported fields
2. **Add EventType constant** to types.go
3. **Implement EventType() method** with pointer receiver
4. **Create constructor** with defensive copying
5. **Document contract** (when published, who consumes)
6. **Update commentator** to log the event
7. **Add to README.md** event catalog

### Example

```go
// Step 1: Define struct
type MyNewEvent struct {
    Field1 string
    Field2 int
    Data   []string  // Will be copied in constructor
}

// Step 2: Add constant
const EventTypeMyNew = "my.new"

// Step 3: Implement EventType()
func (e *MyNewEvent) EventType() string {
    return EventTypeMyNew
}

// Step 4: Create constructor
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

// Step 5: Document in README.md
// Step 6: Update pkg/controller/commentator
```

## Common Pitfalls

### Modifying Event Fields

**Problem**: Consumer modifies event fields, affecting other consumers.

```go
// Bad - modifies event
event := <-eventChan
if indexUpdate, ok := event.(*events.ResourceIndexUpdatedEvent); ok {
    indexUpdate.Changes = append(indexUpdate.Changes, newChange)  // Mutation!
}
```

**Solution**: Don't modify events. Create new ones if needed.

```go
// Good - read-only access
event := <-eventChan
if indexUpdate, ok := event.(*events.ResourceIndexUpdatedEvent); ok {
    for _, change := range indexUpdate.Changes {
        processChange(change)  // Read-only
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
