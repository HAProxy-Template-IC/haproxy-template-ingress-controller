# pkg/controller/commentator - Event Commentator

Development context for the Event Commentator component.

## When to Work Here

Work in this package when:

- Adding logging for new event types
- Improving event correlation logic
- Enhancing domain-aware insights
- Debugging event flow

**DO NOT** work here for:

- Event definitions → Use `pkg/controller/events`
- Business logic → Use appropriate domain package

## Package Purpose

Observability component that subscribes to ALL events and produces domain-aware logs with contextual insights. Decouples logging from business logic.

## Architecture

```
EventBus (all events)
    ↓
EventCommentator
    ├─ Ring Buffer (correlation)
    ├─ Domain Insights (context)
    └─ Structured Logging
```

**Key Feature**: Uses ring buffer to correlate events and add timing context (e.g., "last reconciliation was 5s ago").

## Event Correlation

The internal `*RingBuffer` (in `ring_buffer.go`, *not* `pkg/events/ringbuffer`) exposes three lookup methods:

- `FindByType(eventType)` — every entry of the given type, oldest-first.
- `FindByTypeInWindow(eventType, window)` — entries of the given type whose timestamp falls within `window` of now.
- `FindByCorrelationID(correlationID, maxCount)` — entries sharing a correlation ID.

All of them live on the private `ringBuffer` field; insight code inside this package uses them directly. There is no `FindLast` — use `FindByTypeInWindow` and pick the most recent entry instead.

```go
// Example: how long since the previous reconciliation started?
// Inside this package, where ec.ringBuffer is reachable.
const window = 60 * time.Second
prior := ec.ringBuffer.FindByTypeInWindow(events.EventTypeReconciliationStarted, window)
if len(prior) > 0 {
    last := prior[len(prior)-1]
    timeSince := event.Timestamp().Sub(last.Timestamp())
    logger.Info("reconciliation started",
        "since_last", timeSince,
        "trigger", event.Trigger)
}
```

## Log Levels

- **Error**: Failures (reconciliation failed, deployment failed)
- **Warn**: Invalid states (config invalid, credentials invalid)
- **Info**: Lifecycle/completion (controller started, reconciliation completed)
- **Debug**: Operational details (resource changes, parsing)

## Adding New Event Logging

When adding new event type:

1. Add case in `generateInsight()` method
2. Determine appropriate log level in `determineLogLevel()`
3. Add correlation logic if needed
4. Test logging output

## Resources

- Event types: `pkg/controller/events/CLAUDE.md`
- Internal ring buffer (the one this component uses): `pkg/controller/commentator/ring_buffer.go` (no separate doc — read the source). The generic `pkg/events/ringbuffer.RingBuffer[T]` is a different type with no `Find*` methods; do not confuse the two.
