# pkg/events - Event Bus Infrastructure

Development context for working with event bus infrastructure.

**API Documentation**: See `pkg/events/README.md`
**Architecture**: See `/docs/development/design.md` (Event-Driven Architecture section)

## When to Work Here

Modify this package when:

- Fixing EventBus infrastructure bugs
- Adding new coordination patterns (like scatter-gather)
- Improving performance or memory usage of event system
- Enhancing startup coordination logic

**DO NOT** modify this package for:

- Adding new event types → Use `pkg/controller/events`
- Changing business logic → Use appropriate domain package
- Adding domain-specific validation → Use `pkg/controller/validators`

## Key Design Principle

This package is **domain-agnostic infrastructure**. It contains zero business logic and no knowledge of controllers, HAProxy, Kubernetes, or templates.

Think of this as a library that could be extracted and used in any Go project needing pub/sub or request-response patterns.

## Core Components

### EventBus

Thread-safe pub/sub coordinator with startup synchronization.

**Key Design Decisions:**

1. **Non-blocking publish**: Drops events to slow subscribers rather than blocking
2. **Startup buffering**: Prevents race conditions during initialization
3. **No event replay**: Once dropped, events are gone (by design)
4. **Minimal API**: Publish, Subscribe, Start, Request

**Implementation Notes:**

```go
// bus.go internals
type EventBus struct {
    subscribers    []chan Event
    mu             sync.RWMutex

    // Startup coordination
    started        bool
    startMu        sync.Mutex
    preStartBuffer []Event
}
```

Why this design?

- RWMutex allows concurrent reads (publish checks subscriber list)
- Separate startMu prevents deadlock between startup and publish
- Buffering before Start() prevents lost events during component initialization

### Typed Subscriptions

Filter events at the bus level for improved performance and type safety.

**Three patterns available:**

1. **SubscribeTypes()** - Filter by event type strings at bus level (most efficient)
2. **Subscribe\[T\]()** - Generic function returning typed channel (best type safety)
3. **SubscribeMultiple()** - Filter multiple types with context cancellation

**When to Use:**

- Component only cares about specific event types
- You want compile-time type safety
- High-volume event streams where filtering matters

**When NOT to Use:**

- Commentator/logging (needs all events)
- Components that already use type switches
- Debugging (universal subscription is clearer)

**Examples:**

```go
// Method 1: SubscribeTypes - efficient, filters at bus level
eventChan := bus.SubscribeTypes(100, "reconciliation.triggered", "reconciliation.completed")
for event := range eventChan {
    // Only receives the specified event types
    switch e := event.(type) {
    case *events.ReconciliationTriggeredEvent:
        // handle
    case *events.ReconciliationCompletedEvent:
        // handle
    }
}

// Method 2: Generic Subscribe - typed channel, best for single type
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

triggerChan := events.Subscribe[*events.ReconciliationTriggeredEvent](ctx, bus, 100)
for trigger := range triggerChan {
    // trigger is already *ReconciliationTriggeredEvent - no assertion needed
    fmt.Println(trigger.Reason)
}

// Method 3: SubscribeMultiple - context-aware filtering
multiChan := events.SubscribeMultiple(ctx, bus, 100,
    "template.rendered",
    "template.render.failed")
for event := range multiChan {
    // event matches one of the specified types
}
```

### Request-Response (Scatter-Gather)

Synchronous coordination using timeout and response correlation.

**When to Use:**

- Configuration validation (need all validators to respond)
- Distributed queries (gather info from multiple sources)
- Coordinated operations (need acknowledgment from multiple parties)

**When NOT to Use:**

- Fire-and-forget notifications (use Publish instead)
- Single responder (use direct function call)
- High-frequency operations (too much overhead)

**Implementation Pattern:**

```go
// Internal correlation
type pendingRequest struct {
    request   Request
    responses chan Response
    done      chan struct{}
}

// Request() creates correlation context and waits
func (b *EventBus) Request(ctx context.Context, req Request, opts RequestOptions) (*RequestResult, error) {
    // 1. Create correlation context
    // 2. Publish request to all subscribers
    // 3. Collect responses matching request ID
    // 4. Return when all expected responses received or timeout
}
```

## Testing Approach

### Test Infrastructure, Not Domain Logic

```go
func TestEventBus_Publish(t *testing.T) {
    bus := NewEventBus(100)

    // Use simple test event (no domain knowledge)
    type TestEvent struct{ Value string }
    func (e TestEvent) EventType() string { return "test" }

    sub := bus.Subscribe(10)
    bus.Start()

    bus.Publish(TestEvent{Value: "hello"})

    event := <-sub
    assert.Equal(t, "hello", event.(TestEvent).Value)
}
```

### Test Scenarios

1. **Basic pub/sub**: Publish event, verify subscriber receives it
2. **Startup buffering**: Publish before Start(), verify replay after Start()
3. **Slow subscribers**: Fill subscriber buffer, verify drop behavior
4. **Concurrent publish**: Multiple goroutines publishing simultaneously
5. **Request-response**: Send request, verify response correlation
6. **Timeout handling**: Request with no responders, verify timeout
7. **Context cancellation**: Cancel context during Request(), verify cleanup

## Common Pitfalls

### Blocking in Event Handlers

**Problem**: Subscriber blocks, channel fills, events dropped.

```go
// Bad - blocks for 5 seconds
for event := range eventChan {
    time.Sleep(5 * time.Second)  // Simulating slow work
    process(event)
}
```

**Solution**: Process quickly or spawn goroutine.

```go
// Good - non-blocking handler
for event := range eventChan {
    event := event  // Capture
    go func() {
        process(event)  // Long-running work in goroutine
    }()
}
```

### Buffer Sizing

**Problem**: Buffer too small → frequent drops; too large → high memory.

**Guidelines:**

```go
// Control events (low frequency)
controlChan := bus.Subscribe(10)  // Small buffer OK

// High-volume events (resource changes)
resourceChan := bus.Subscribe(200)  // Larger buffer

// Pre-start buffer
bus := NewEventBus(100)  // Based on expected init events
```

**Rule of Thumb:**

- Control events: 10-50
- Resource events: 100-200
- Pre-start buffer: 100-200

### Forgetting EventBus.Start()

**Problem**: Events published before Start() never reach early subscribers.

```go
// Bad
bus := NewEventBus(100)
component1 := NewComponent1(bus)  // Subscribes
component2 := NewComponent2(bus)  // Subscribes
// Events published during setup are buffered
// Forgot to call bus.Start() - events never replayed!
```

**Solution**: Always call Start() after all components subscribe.

```go
// Good
bus := NewEventBus(100)

// Components subscribe during initialization
component1 := NewComponent1(bus)
component2 := NewComponent2(bus)

// Start after all subscribers ready
bus.Start()  // Replays buffered events

// Now normal operation
bus.Publish(SystemReadyEvent{})
```

### Request() Deadlock

**Problem**: Responder also calls Request(), causing deadlock.

```go
// Bad - deadlock risk
func (c *Component) Run(ctx context.Context, bus *EventBus) {
    events := bus.Subscribe(10)
    for event := range events {
        if req, ok := event.(MyRequest); ok {
            // This can deadlock if request depends on this component responding
            result, _ := bus.Request(ctx, OtherRequest{}, ...)
            bus.Publish(MyResponse{result: result})
        }
    }
}
```

**Solution**: Use separate goroutine or don't nest Request() calls.

```go
// Good - handle in goroutine
func (c *Component) Run(ctx context.Context, bus *EventBus) {
    events := bus.Subscribe(10)
    for event := range events {
        if req, ok := event.(MyRequest); ok {
            req := req  // Capture
            go func() {
                // Won't block event loop
                result, _ := bus.Request(ctx, OtherRequest{}, ...)
                bus.Publish(MyResponse{result: result})
            }()
        }
    }
}
```

## Extension Points

### Adding New Event Interfaces

If you need new event metadata beyond EventType():

```go
// events/types.go
type Event interface {
    EventType() string
}

// New interface for events with priority
type PrioritizedEvent interface {
    Event
    Priority() int
}

// Update EventBus to handle priority (if needed)
func (b *EventBus) PublishPriority(event PrioritizedEvent) {
    // Implementation
}
```

### Adding New Coordination Patterns

Follow scatter-gather as example:

1. Define new interfaces (if needed)
2. Add method to EventBus
3. Implement correlation logic
4. Add comprehensive tests
5. Update README.md with usage examples

## Performance Characteristics

### Memory

- EventBus: O(N) where N = number of subscribers (channel slice)
- Pre-start buffer: O(M) where M = events before Start()
- Request tracking: O(R) where R = concurrent requests

### CPU

- Publish: O(N) with non-blocking select (very fast)
- Subscribe: O(1) append to slice
- Request correlation: O(R×T) where T = responses per request

### Benchmarking

```go
func BenchmarkEventBus_Publish(b *testing.B) {
    bus := NewEventBus(100)
    sub := bus.Subscribe(1000)
    bus.Start()

    event := TestEvent{Value: "test"}

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        bus.Publish(event)
    }

    b.StopTimer()
    // Drain subscriber
    for len(sub) > 0 {
        <-sub
    }
}
```

Expected: ~100-500ns per publish with 1 subscriber.

## Related Packages

**Domain Event Types:**

- `pkg/controller/events` - All domain-specific event definitions

**Event Consumers:**

- `pkg/controller/commentator` - Logs all events with domain context
- `pkg/controller/reconciler` - Subscribes to change events
- `pkg/controller/executor` - Subscribes to reconciliation events

**Event Producers:**

- `pkg/k8s/watcher` - Publishes resource change events
- `pkg/controller/configloader` - Publishes config events
- All controller components - Publish completion/failure events

## Troubleshooting

### Events Not Reaching Subscriber

**Diagnosis:**

1. Verify EventBus.Start() was called
2. Check if subscriber buffer is full (events being dropped)
3. Verify event type matches subscriber's type assertion
4. Check subscriber is subscribed before event published

```go
// Debug subscriber state
log.Info("subscriber buffer usage", "buffered", len(eventChan), "capacity", cap(eventChan))
```

### Request() Always Timing Out

**Diagnosis:**

1. Verify responders are subscribed and running
2. Check request ID correlation matches
3. Verify responder publishes Response with correct request ID
4. Check context timeout is reasonable

```go
// Debug request-response flow
log.Info("request sent", "req_id", req.RequestID(), "expected", opts.ExpectedResponders)

// In responder
log.Info("response sent", "req_id", resp.RequestID(), "responder", resp.Responder())
```

### High Memory Usage

**Diagnosis:**

1. Check subscriber buffer sizes (reduce if too large)
2. Verify subscribers are draining channels
3. Check for subscriber goroutine leaks
4. Profile with pprof

```bash
go tool pprof http://localhost:6060/debug/pprof/heap
```

## Best Practices

### 1. Keep Event Interfaces Minimal

```go
// Good - minimal interface
type Event interface {
    EventType() string
}

// Avoid - too much infrastructure
type Event interface {
    EventType() string
    Timestamp() time.Time
    CorrelationID() string
    Priority() int
    // ...
}
```

### 2. Use Type Assertions, Not Type Switches

```go
// Good - explicit type assertion
if req, ok := event.(MyRequest); ok {
    handle(req)
}

// Avoid - large type switches (indicates domain logic)
switch e := event.(type) {
case Type1:
case Type2:
case Type3:
// 50 more cases...
}
```

If you need large type switches, the logic probably belongs in controller package, not events package.

### 3. Document Event Contracts

Event types should document their contract:

```go
// MyRequest is published when X happens.
// Responders must publish MyResponse with matching request ID within 5 seconds.
type MyRequest struct {
    id string
}

func (r MyRequest) RequestID() string { return r.id }
```

### 4. Test Thoroughly

Event infrastructure bugs affect the entire system. Write extensive tests:

- Unit tests for basic behavior
- Concurrent stress tests
- Timeout tests
- Memory leak tests

## Migration Guide

If modifying EventBus interface:

1. Update EventBus implementation
2. Update tests
3. Update README.md
4. Search codebase for all EventBus usage
5. Update all consumers incrementally
6. Run full test suite including integration tests

Example breaking change:

```go
// Old
bus.Publish(event Event) int

// New
bus.Publish(event Event) error
```

This requires updating ~50 call sites throughout codebase.
