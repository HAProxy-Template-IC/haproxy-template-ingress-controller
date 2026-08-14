# pkg/controller/commentator

The `EventCommentator` subscribes to every event on the `EventBus` and turns them into structured log lines with domain-specific context. It's the single component that logs system behaviour for humans — everything else emits events and lets the commentator describe them.

Key property: the commentator owns its own ring buffer (separate from the generic `pkg/events/ringbuffer` used by `pkg/controller/debug`). It retains only event type, timestamp, correlation identifiers, and the scalar phase metadata used by insights. The commentator therefore doesn't extend event payload lifetimes.

## Usage

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/commentator"

// bufferSize is the ring-buffer capacity (how many recent events
// to keep for correlation). 500 is the controller's default
// (hardcoded in pkg/controller/controller.go; not a CRD field).
c := commentator.NewEventCommentator(bus, logger, 500)
go c.Start(ctx)
```

Subscribe-before-`bus.Start()` is handled inside the constructor, so buffered startup events land in the logs too.

## What Gets Logged

Every domain event produces one log line. Level mapping (see `log_levels.go` and `insights_config.go` for the full rules):

- **Error** — anything that ends in `*FailedEvent` that wasn't recoverable (deployment failures, validation failures, etc.).
- **Warn** — invalid states that the controller can recover from (config rejected, credentials rejected).
- **Info** — lifecycle and successful completion events (controller started, reconciliation completed, became/lost leader).
- **Debug** — operational detail (resource index updates, parser progress, HTTP resource fetches).

Shape of a typical line:

```text
INFO  configuration validated successfully version=12345 templates=3
DEBUG resource index updated type=ingresses created=5 modified=2 deleted=1 initial_sync=false
INFO  reconciliation started trigger=config_change since_last=5.2s
INFO  reconciliation completed duration_ms=1234 resources=22
ERROR deployment failed instance=haproxy-0 error="connection refused"
```

## Correlation

The insight functions use the ring-buffer's queries — `FindByTypeInWindow` and `FindByCorrelationID` — to decide what context to attach to each log line. Queries return metadata projections through `Event` and, when applicable, `CorrelatedEvent`, not the original concrete events. Consumers outside this package should subscribe to the event bus directly and do their own correlation.

## Adding a Log Line for a New Event Type

When a new event type lands in `pkg/controller/events`:

1. Pick a case in `generateInsight` (or add one) to build the log message.
2. Pick a case in `determineLogLevel` (or add one) so it doesn't fall through to the default `Debug`.
3. If the message should reference prior events, use the `FindByCorrelationID` / `FindByTypeInWindow` helpers on the ring buffer rather than scanning the slice manually.

Missing a case isn't a hard error — the event still gets a metadata-only history entry — but it logs at `Debug` with only the generic event fields, which is usually not what you want for a new domain event.

## See Also

- [`pkg/controller/events`](../events/) — the catalogue this subscribes to
- `pkg/controller/commentator/CLAUDE.md` — insight patterns, when to add correlation, what the level-classification rules are
- [`pkg/controller/debug`](../debug/) — exposes a *different* ring buffer over `/debug/vars/events`; consumers of raw event history should look there

## License

Apache-2.0 — see root `LICENSE`.
