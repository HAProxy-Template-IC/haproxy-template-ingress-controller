# Event Immutability Analyzer

A custom Go analyzer that detects modifications to event struct fields in the HAPTIC controller.

## Purpose

Event structs in `pkg/controller/events` are designed to be immutable after creation. They are passed by reference through the event bus to multiple subscribers, and consumers must treat them as read-only to prevent unintended side effects.

This analyzer provides automated enforcement of the immutability contract by detecting assignments to event struct fields.

## What It Detects

The analyzer flags violations where event struct fields are modified:

```go
// VIOLATION: Modifying event parameter
func handleEvent(event *ReconciliationTriggeredEvent) {
    event.Reason = "modified"  // ❌ Detected!
}
```

## What It Allows

The analyzer allows these patterns:

```go
// OK: Read-only access
func handleEvent(event *ReconciliationTriggeredEvent) {
    reason := event.Reason  // ✓ Reading is fine
    log.Info("reason", reason)
}

// OK: Local variable creation
func createEvent() {
    event := &ReconciliationTriggeredEvent{}
    event.Reason = "new"  // ✓ Local variable, not from event bus
}

// OK: Method receivers can modify (for EventType(), Timestamp(), etc.)
type MyEvent struct {
    field string
}

func (e *MyEvent) EventType() string {
    e.field = "updated"  // ✓ Method receiver can modify
    return "my.event"
}
```

## Limitations

The analyzer currently focuses on the most common case: detecting mutations to event parameters. It does **not** detect:

1. **Range loop variable mutations**: Modifying events in a loop is harder to detect and relies on code review
2. **Closure variable mutations**: Events captured in closures
3. **Indirect mutations**: Passing events to functions that modify them

These limitations are acceptable for an internal project where the team controls all consumers.

## Usage

### Via Make

The analyzer is integrated into the project's linting workflow:

```bash
# Run all linters including eventimmutability
make lint

# Run only eventimmutability
go run ./tools/linters/eventimmutability/cmd/eventimmutability ./...
```

### Standalone

You can also run the analyzer directly:

```bash
cd tools/linters/eventimmutability
go run ./cmd/eventimmutability ../../...
```

## Testing

The analyzer includes comprehensive tests:

```bash
cd tools/linters/eventimmutability
go test -v
```

## Implementation Details

The analyzer uses the `golang.org/x/tools/go/analysis` framework and works by:

1. Identifying all struct types in `pkg/controller/events`
2. Tracking function parameters of these types
3. Detecting assignment statements to fields of parameter variables
4. Reporting violations with clear error messages

## Future Enhancements

Potential improvements:

- Detect range loop variable mutations
- Detect closure variable mutations
- Support configuration for additional immutable packages
- Integration as golangci-lint plugin
