# pkg/events

Pure event bus infrastructure for event-driven architecture.

## Overview

This package provides generic pub/sub and request-response coordination mechanisms. It contains **no domain knowledge** - all application-specific event types are defined in `pkg/controller/events`.

## Architecture

```
pkg/events/              # Generic infrastructure (reusable)
├── bus.go              # EventBus with startup coordination
├── request.go          # Scatter-gather pattern
└── bus_test.go         # Infrastructure tests

pkg/controller/events/   # Domain-specific event catalog
└── types.go            # 50+ controller event types
```

## Core Components

### EventBus

Thread-safe pub/sub coordinator with startup synchronization.

**Features:**

- Non-blocking publish with backpressure handling (drops events to slow subscribers)
- Startup coordination via buffering (prevents race conditions during initialization)
- Thread-safe concurrent access
- Simple subscribe/publish API

**Usage:**

```go
import "haptic/pkg/events"

// Create bus with buffer capacity for pre-start events
bus := events.NewEventBus(100)

// Subscribe (returns read-only channel)
eventChan := bus.Subscribe("component", 200) // 200 event buffer

// Start after all subscribers are ready (releases buffered events)
bus.Start()

// Publish events
bus.Publish(myEvent)

// Receive events
for event := range eventChan {
    switch e := event.(type) {
    case SomeEventType:
        // Handle event
    }
}
```

### Event Interface

All events must implement the Event interface:

```go
type Event interface {
    EventType() string      // Unique event identifier (e.g., "config.validated")
    Timestamp() time.Time   // When the event occurred
}
```

### Startup Coordination

The EventBus includes buffering to prevent race conditions during initialization:

1. **Before Start()**: Events are buffered
2. **After Start()**: Buffered events are replayed, then events flow normally

This ensures no events are lost if published before all subscribers connect.

**Example:**

```go
bus := events.NewEventBus(100)

// Components subscribe during setup
component1 := NewComponent1(bus)
component2 := NewComponent2(bus)
component3 := NewComponent3(bus)

// Start the bus after all subscribers are ready
// This replays any buffered events and switches to normal operation
bus.Start()

// Now all components will receive events
bus.Publish(SystemReadyEvent{})
```

### Request-Response (Scatter-Gather)

Synchronous coordination across multiple responders using the scatter-gather pattern.

**Use Cases:**

- Configuration validation (multiple validators must approve)
- Distributed queries (gather responses from multiple sources)
- Coordinated operations (need confirmation from multiple parties)

**Interfaces:**

```go
type Request interface {
    Event
    RequestID() string  // Unique ID for correlating responses
}

type Response interface {
    Event
    RequestID() string  // Links back to request
    Responder() string  // Who sent this response
}
```

**Usage:**

```go
import (
    "context"
    "time"
    "haptic/pkg/events"
)

// Create request
req := MyValidationRequest{
    id: "req-123",
    data: configToValidate,
}

// Send request and wait for responses
result, err := bus.Request(ctx, req, events.RequestOptions{
    Timeout:            10 * time.Second,
    ExpectedResponders: []string{"validator-1", "validator-2", "validator-3"},
    MinResponses:       2,  // Optional: allow partial responses
})

if err != nil {
    // Timeout or context cancellation
    log.Error("request failed", "error", err)
}

// Process responses
for _, resp := range result.Responses {
    // Handle each response
}

// Check for missing responders
if len(result.Errors) > 0 {
    log.Warn("some responders did not reply", "errors", result.Errors)
}
```

**Responder Implementation:**

```go
func (v *Validator) Run(ctx context.Context, bus *events.EventBus) {
    eventChan := bus.Subscribe("responder", 100)

    for {
        select {
        case event := <-eventChan:
            if req, ok := event.(MyValidationRequest); ok {
                // Process request
                valid, errors := v.validate(req.data)

                // Send response
                resp := MyValidationResponse{
                    reqID:     req.RequestID(),
                    responder: "validator-1",
                    valid:     valid,
                    errors:    errors,
                }
                bus.Publish(resp)
            }
        case <-ctx.Done():
            return
        }
    }
}
```

## Design Principles

### 1. Generic Infrastructure

This package is **domain-agnostic** - it provides the plumbing, not the events:

- EventBus, Request, Response are generic mechanisms
- Event types are defined in `pkg/controller/events`
- Could be extracted as a standalone library

### 2. Non-Blocking

The EventBus never blocks publishers:

- Full subscriber channels → event dropped for that subscriber
- Prevents slow consumers from blocking the system
- Subscribers must drain their channels promptly

### 3. Thread-Safe

All operations are safe for concurrent access:

- Multiple goroutines can publish simultaneously
- Multiple goroutines can subscribe simultaneously
- Thread-safe startup coordination

### 4. Simple API

Minimal surface area:

- `Publish(event)` - send event to all subscribers
- `Subscribe(name, bufferSize)` - create new event channel
- `Start()` - release buffered events
- `Request(ctx, req, opts)` - scatter-gather pattern

## Testing

Tests use simple mock events and verify infrastructure behavior:

```bash
go test ./pkg/events/... -v
```

Test coverage includes:

- Basic pub/sub
- Startup coordination (buffering/replay)
- Slow subscriber behavior
- Concurrent publishing
- Request-response coordination
- Timeout handling
- Context cancellation

## Performance Characteristics

- **Publish**: O(N) where N = number of subscribers (non-blocking select)
- **Subscribe**: O(1) append to slice
- **Memory**: Bounded by subscriber buffer sizes and pre-start buffer
- **Startup Buffer**: O(M) where M = events published before Start()

## When to Use

**Use EventBus for:**

- Async pub/sub notifications
- Observability events
- Decoupling components
- Event-driven workflows

**Use Request() for:**

- Multi-phase validation
- Distributed queries
- Coordinated operations requiring responses

**Don't Use for:**

- Direct function calls (if you don't need decoupling)
- High-frequency data streams (consider channels instead)
- Large payloads (events should be lightweight)

## Integration

See `docs/design.md` for:

- Event-driven architecture overview
- Controller event catalog
- Startup coordination flow
- Request-response validation example

## Related Packages

- `pkg/controller/events` - Domain-specific event type definitions
- `pkg/controller/commentator` - Event observability component
- `pkg/controller/reconciler` - Event-driven reconciliation logic
