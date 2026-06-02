# pkg/controller/testutil

Shared test helpers for `pkg/controller/*` package tests.

## Overview

Most controller-package tests need the same handful of pieces: a fresh `*EventBus`, a quiet logger, generic helpers for waiting on a typed event, and consistent timing constants so the test suite isn't a graveyard of magic numbers. This package consolidates them so individual `_test.go` files stay focused on the behaviour under test.

This package is **only consumed from tests**. Production code must not import it.

## Helpers

| Function | Purpose |
|----------|---------|
| `NewTestBus()` | `*EventBus` with a 100-slot buffer |
| `NewTestLogger()` | `*slog.Logger` writing to stderr at ERROR level (quiet during runs) |
| `NewTestBusAndLogger()` | Both of the above in one call (the most common setup) |
| `WaitForEvent[T](t, ch, timeout)` | Drain `ch` until an event of type `T` arrives, fail test on timeout |
| `WaitForEventWithPredicate[T](t, ch, timeout, pred)` | Same plus a predicate filter |
| `AssertNoEvent[T](t, ch, timeout)` | Fail if an event of type `T` arrives within `timeout` |
| `DrainChannel(ch)` | Non-blocking drain (helpful for "ignore everything queued" before triggering the action under test) |
| `RunComponentStartStop(t, bus, start, stop)` | Standard lifecycle test for a Start/Stop component |

## Timing Constants

| Name | Value | Use for |
|------|-------|---------|
| `StartupDelay` | 50ms | Brief settling pause after starting components |
| `DebounceWait` | 100ms | Long enough for debounce timers (default is 2s in production but tests override down) |
| `EventTimeout` | 500ms | Default waiting timeout for a single event |
| `LongTimeout` | 1s | Operations that may take longer (graceful stop) |
| `VeryLongTimeout` | 2s | Integration-style tests inside unit test files |
| `NoEventTimeout` | 200ms | Shorter timeout for "verify nothing arrives" |

Use these instead of inline `time.Second / 2` literals — when CI runners get slower, one constant moves and every test gets the headroom.

## Quick Start

```go
import (
    "testing"

    "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

func TestMyComponent_Foo(t *testing.T) {
    bus, logger := testutil.NewTestBusAndLogger()
    eventChan := bus.Subscribe("test", 10)
    bus.Start()

    component := New(bus, logger)
    go component.Start(t.Context()) // Go 1.24+ test-scoped context

    bus.Publish(events.NewSomeRequest("input"))
    got := testutil.WaitForEvent[*events.SomeResponse](t, eventChan, testutil.EventTimeout)
    assert.Equal(t, "expected", got.Result)
}
```

## License

Apache-2.0 — see root `LICENSE`.
