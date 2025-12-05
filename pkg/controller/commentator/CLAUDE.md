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

```go
// Example: Correlating reconciliation frequency
lastRecon := ec.ringBuffer.FindLast("reconciliation.started")
if lastRecon != nil {
    timeSince := event.Timestamp.Sub(lastRecon.Timestamp)
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
- Ring buffer: `pkg/events/ringbuffer/CLAUDE.md`
