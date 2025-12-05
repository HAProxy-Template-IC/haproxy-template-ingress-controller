# pkg/controller/events

Domain-specific event type definitions for controller coordination.

## Overview

This package defines all event types used by the controller for component coordination via the EventBus. Events represent facts about what happened in the system and are immutable after creation.

**Separation**:

- `pkg/events` - Generic pub/sub infrastructure
- `pkg/controller/events` - Domain event types (this package)

## Installation

```go
import "haproxy-template-ic/pkg/controller/events"
```

## Event Categories

### Lifecycle Events

```go
const (
    EventTypeControllerStarted  = "controller.started"
    EventTypeControllerShutdown = "controller.shutdown"
)
```

- **ControllerStartedEvent** - Controller initialization complete
- **ControllerShutdownEvent** - Controller shutting down

### Configuration Events

```go
const (
    EventTypeConfigParsed             = "config.parsed"
    EventTypeConfigValidationRequest  = "config.validation.request"
    EventTypeConfigValidationResponse = "config.validation.response"
    EventTypeConfigValidated          = "config.validated"
    EventTypeConfigInvalid            = "config.invalid"
)
```

- **ConfigParsedEvent** - ConfigMap parsed successfully
- **ConfigValidationRequest** - Scatter-gather validation request
- **ConfigValidationResponse** - Validator response
- **ConfigValidatedEvent** - All validators passed
- **ConfigInvalidEvent** - Validation failed

### Resource Events

```go
const (
    EventTypeResourceIndexUpdated = "resource.index.updated"
    EventTypeResourceSyncComplete = "resource.sync.complete"
    EventTypeIndexSynchronized    = "index.synchronized"
)
```

- **ResourceIndexUpdatedEvent** - Resource index changed (add/update/delete)
- **ResourceSyncCompleteEvent** - Single resource type synced
- **IndexSynchronizedEvent** - All resource types synced

### Reconciliation Events

```go
const (
    EventTypeReconciliationTriggered = "reconciliation.triggered"
    EventTypeReconciliationStarted   = "reconciliation.started"
    EventTypeReconciliationCompleted = "reconciliation.completed"
    EventTypeReconciliationFailed    = "reconciliation.failed"
)
```

- **ReconciliationTriggeredEvent** - Reconciliation requested
- **ReconciliationStartedEvent** - Reconciliation cycle started
- **ReconciliationCompletedEvent** - Reconciliation succeeded
- **ReconciliationFailedEvent** - Reconciliation failed

### Template Events

```go
const (
    EventTypeTemplateRendered     = "template.rendered"
    EventTypeTemplateRenderFailed = "template.render.failed"
)
```

- **TemplateRenderedEvent** - Template rendering succeeded
- **TemplateRenderFailedEvent** - Template rendering failed

### Validation Events

```go
const (
    EventTypeValidationStarted   = "validation.started"
    EventTypeValidationCompleted = "validation.completed"
    EventTypeValidationFailed    = "validation.failed"
)
```

- **ValidationStartedEvent** - HAProxy config validation started
- **ValidationCompletedEvent** - Validation succeeded
- **ValidationFailedEvent** - Validation failed

### Deployment Events

```go
const (
    EventTypeDeploymentStarted        = "deployment.started"
    EventTypeInstanceDeployed         = "instance.deployed"
    EventTypeInstanceDeploymentFailed = "instance.deployment.failed"
    EventTypeDeploymentCompleted      = "deployment.completed"
)
```

- **DeploymentStartedEvent** - Deployment to HAProxy pods started
- **InstanceDeployedEvent** - Single HAProxy instance deployed
- **InstanceDeploymentFailedEvent** - Single instance deployment failed
- **DeploymentCompletedEvent** - All instances deployed

### HAProxy Pod Events

```go
const (
    EventTypeHAProxyPodsDiscovered = "haproxy.pods.discovered"
    EventTypeHAProxyPodAdded       = "haproxy.pod.added"
    EventTypeHAProxyPodRemoved     = "haproxy.pod.removed"
)
```

- **HAProxyPodsDiscoveredEvent** - HAProxy pods discovered
- **HAProxyPodAddedEvent** - New HAProxy pod added
- **HAProxyPodRemovedEvent** - HAProxy pod removed

## Usage

### Publishing Events

```go
import "haproxy-template-ic/pkg/controller/events"

// Create event using constructor (performs defensive copying)
event := events.NewConfigParsedEvent(config, "v1")

// Publish to EventBus
eventBus.Publish(event)
```

### Consuming Events

```go
// Subscribe to EventBus
eventChan := eventBus.Subscribe(100)

for event := range eventChan {
    // Type assertion to specific event
    if parsed, ok := event.(*events.ConfigParsedEvent); ok {
        fmt.Printf("Config version: %s\n", parsed.Version)
        // Process config...
    }
}
```

### Scatter-Gather Pattern

```go
// Create validation request
req := events.NewConfigValidationRequest(config, "v1")

// Send scatter-gather request
result, err := eventBus.Request(ctx, req, events.RequestOptions{
    Timeout:            10 * time.Second,
    ExpectedResponders: []string{"basic", "template", "jsonpath"},
})

if err != nil {
    // Timeout or error
}

// Process responses
for _, resp := range result.Responses {
    if valResp, ok := resp.(*events.ConfigValidationResponse); ok {
        if !valResp.Valid {
            fmt.Printf("Validator %s failed: %v\n", valResp.Validator, valResp.Errors)
        }
    }
}
```

## Event Immutability

Events are immutable after creation to ensure consistency across consumers.

### Constructors Perform Defensive Copying

```go
func NewResourceIndexUpdatedEvent(resourceType string, changes []types.ResourceChange) *ResourceIndexUpdatedEvent {
    // Copy slice to prevent external mutations
    changesCopy := make([]types.ResourceChange, len(changes))
    copy(changesCopy, changes)

    return &ResourceIndexUpdatedEvent{
        ResourceType: resourceType,
        Changes:      changesCopy,
    }
}
```

### Consumers Must Not Modify Events

```go
// Good - read-only access
event := <-eventChan
if update, ok := event.(*events.ResourceIndexUpdatedEvent); ok {
    for _, change := range update.Changes {
        processChange(change)  // Read only
    }
}

// Bad - DO NOT MODIFY
if update, ok := event.(*events.ResourceIndexUpdatedEvent); ok {
    update.Changes = append(update.Changes, newChange)  // FORBIDDEN!
}
```

## Adding New Event Types

1. Define event struct with exported fields
2. Add EventType constant
3. Implement EventType() method with pointer receiver
4. Create constructor with defensive copying
5. Update commentator to log the event

Example:

```go
// 1. Define struct
type MyNewEvent struct {
    Field string
    Data  []string
}

// 2. Add constant
const EventTypeMyNew = "my.new"

// 3. Implement EventType()
func (e *MyNewEvent) EventType() string {
    return EventTypeMyNew
}

// 4. Create constructor
func NewMyNewEvent(field string, data []string) *MyNewEvent {
    dataCopy := make([]string, len(data))
    copy(dataCopy, data)

    return &MyNewEvent{
        Field: field,
        Data:  dataCopy,
    }
}
```

## Common Event Fields

Most events include:

- **Timestamp** - When the event occurred
- **Version** - Resource version (for ConfigMap/Secret events)
- **Errors** - Error messages (for failure events)

## Examples

See:

- Event publishing: `pkg/controller/configloader/`
- Event consumption: `pkg/controller/reconciler/`
- Scatter-gather: `pkg/controller/validator/coordinator.go`
- Event logging: `pkg/controller/commentator/`

## License

See main repository for license information.
