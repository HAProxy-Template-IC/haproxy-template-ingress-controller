# pkg/events

Generic, domain-agnostic event bus with pub/sub and scatter-gather coordination. The controller pulls this in; everything else in the tree stays clean of it (only `pkg/controller/events` knows about domain event types).

Module path: `gitlab.com/haproxy-haptic/haptic`. Source is authoritative (`go doc ./pkg/events`); this README is a short orientation.

## Minimal Usage

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/events"

bus := events.NewEventBus(100)          // pre-start buffer capacity
ch := bus.Subscribe("my-component", 50) // per-subscriber buffer

bus.Start()   // call once all subscribers are registered; releases buffered events

bus.Publish(MyEvent{...})

for event := range ch {
    switch e := event.(type) {
    case MyEvent:
        // ...
    }
}
```

Every event type implements `Event`:

```go
type Event interface {
    EventType() string
    Timestamp() time.Time
}
```

The `CoalescibleEvent` interface marks events that `pkg/controller/coalesce` can collapse when bursts arrive faster than a component can process them.

## Startup Buffering

Subscribers must register **before** `Start()` or they miss the replay of events published during initialisation. The controller's reinitialisation loop does this in a strict order inside `iteration.go`; bespoke users of this library should follow the same pattern:

1. `NewEventBus(n)` — `n` is the buffer that holds pre-start publishes.
2. Construct every component. Each calls `Subscribe(...)` inside its constructor.
3. `bus.Start()` — flushes the pre-start buffer to each subscriber's channel.
4. Start the component goroutines.

Forgetting step 3 is a common footgun: publishes succeed silently (buffered) but nothing ever fires.

## Typed Subscriptions

Three filter patterns on top of the base `Subscribe`:

- **`SubscribeTypes(name, bufferSize, types...)`** — filters at the bus, only delivers events whose `EventType()` is in the list. Cheapest when you only care about a handful of types.
- **`Subscribe[T](ctx, bus, bufferSize) <-chan T`** — generic helper that returns a typed channel for a single event type. No type assertion in the consumer loop.
- **`SubscribeMultiple(ctx, bus, bufferSize, types...)`** — like `SubscribeTypes` but ties the subscription lifetime to a `context.Context` for automatic cleanup.

The commentator and anything logging "everything" should use plain `Subscribe` — filtering there would just hide events.

## Scatter-Gather (Request/Response)

Used when multiple independent responders all need to approve something (the admission-webhook validator is the canonical example).

```go
req := NewMyRequest(payload)
result, err := bus.Request(ctx, req, events.RequestOptions{
    Timeout:            10 * time.Second,
    ExpectedResponders: []string{"basic", "template", "jsonpath"},
})
if err != nil {
    // timeout or context cancelled
}
for _, resp := range result.Responses {
    // resp.Responder() identifies who sent it, resp.RequestID() ties it back
}
```

Responders listen on their own subscription, match on request ID, and `Publish` a `Response` — there's no direct wiring, they just need to call `bus.Publish(resp)` with the request ID matching.

Don't nest `Request()` calls on the same path without spawning a goroutine for the outer call — a responder that blocks on another `Request` while its own pending request waits can deadlock.

## Back-Pressure

Publish is non-blocking. If a subscriber's buffer is full, the event is **dropped for that subscriber** (others still receive it). The bus increments a drop counter and invokes the registered `DropCallback` (used by `pkg/controller/metrics` to emit `haptic_events_dropped_*_total`). Slow consumers are your problem — hand work off to a goroutine or raise the subscriber buffer.

## Buffer Sizing Rule of Thumb

`pkg/events/defaults.go` exposes five named constants — prefer them over raw integers so the intent is readable at the call site:

| Tier | Constant | Value | For |
|------|----------|-------|-----|
| Low | `LowVolumeSubscriberBuffer` | 10 | Components that see at most a handful of events per second (leadership transitions, config reloads). |
| Standard | `StandardSubscriberBuffer` | 50 | Default for most controller components. |
| High | `HighVolumeSubscriberBuffer` | 100 | Reconciliation-path consumers that fan in from many resource types. |
| Publishing | `PublishingSubscriberBuffer` | 200 | Components that publish downstream work in response to every event. |
| Debug | `DebugSubscriberBuffer` | 1000 | Observability consumers (commentator, debug ring buffer) that must catch everything. |

Pre-start buffer on `NewEventBus`: roughly the number of events you expect during initialisation; 100 is fine for the main controller.

## Testing

```bash
go test ./pkg/events/...           # unit tests
go test ./pkg/events/... -race     # race detector
go test ./pkg/events/... -bench=.  # pub/sub benchmarks
```

Tests define ad-hoc `Event` types inline — the infrastructure never needs domain types.

## See Also

- `pkg/events/CLAUDE.md` — design rationale, pitfall catalogue (blocking handlers, buffer sizing, Request deadlocks), extension points
- `pkg/events/ringbuffer` — generic thread-safe ring buffer used by commentator and debug event history
- `pkg/controller/events` — domain event catalogue (~50 types across lifecycle, config, resources, reconciliation, deployment, leader election)
- `pkg/controller/commentator` — subscribes to every event for domain-aware logging
- `pkg/controller/coalesce` — collapses `CoalescibleEvent` bursts

## License

Apache-2.0 — see root `LICENSE`.
