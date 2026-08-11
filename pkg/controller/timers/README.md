# pkg/controller/timers

Single-goroutine `*time.Timer` wrapper that papers over Go's well-known timer footguns.

## Overview

`SafeTimer` provides safe `Stop` / `Reset` operations and exposes the channel as `Chan() <-chan time.Time` (returning nil when no timer is active so it blocks forever in a `select`). It exists because plain `time.Timer.Reset` after `Stop` returning `false` can leak a stale tick into the next cycle, and every component that uses timers in its event loop would otherwise re-implement the drain dance.

It must only be used from a single goroutine — there is no internal synchronisation.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/timers"

var t timers.SafeTimer

for {
    select {
    case event := <-eventChan:
        // Trailing-edge debounce: every event resets the countdown.
        t.Reset(5 * time.Second)
        process(event)

    case <-t.Chan():
        t.Fired() // MUST be called after receiving — clears the internal
                  // reference so Chan() goes back to nil until the next Reset.
        flush()

    case <-ctx.Done():
        t.Stop()
        return
    }
}
```

`Fired()` is easy to forget. Without it, `t.timer != nil` keeps reading as
true even after the timer's channel was drained, so a stale reference lingers
until the next `Reset` or `Stop`.

`Reset` implements trailing-edge debouncing. Controller event loops use it for
configuration coalescing, CRD discovery settling, and drift-prevention cadence.

## See Also

- [`pkg/controller/configchange`](../configchange/) — uses trailing-edge `Reset` for reinit coalescing
- [`pkg/controller/crdwatch`](../crdwatch/) — settles CRD discovery after changes
- [`pkg/controller/deployer`](../deployer/) — owns the drift-prevention cadence

## License

Apache-2.0 — see root `LICENSE`.
