# pkg/controller/reconciler

Reconciler component - debounces resource changes and triggers reconciliation.

## Overview

Stage 5 component that applies debouncing logic to prevent excessive reconciliations. Waits for quiet periods before triggering reconciliation events.

## Features

- **Debouncing**: Batches rapid resource changes
- **Immediate initial sync**: Triggers reconciliation when all resources synced
- **Configurable interval**: Default 500ms
- **Initial sync filtering**: Ignores initial resource sync events

## Quick Start

```go
import "haproxy-template-ic/pkg/controller/reconciler"

// Default configuration (500ms debounce)
reconciler := reconciler.New(bus, logger, nil)
go reconciler.Start(ctx)

// Custom debounce interval
reconciler := reconciler.New(bus, logger, &reconciler.Config{
    DebounceInterval: 2 * time.Second,
})
go reconciler.Start(ctx)
```

## How It Works

### Resource Changes (Debounced)

1. ResourceIndexUpdatedEvent received
2. Debounce timer reset to 500ms
3. If another change arrives, timer reset again
4. When timer expires (no changes for 500ms), publish ReconciliationTriggeredEvent

### Index Synchronized (Immediate)

1. IndexSynchronizedEvent received (all resource watchers synced)
2. Stop any pending debounce timer
3. Immediately publish ReconciliationTriggeredEvent

This triggers the initial reconciliation after all resources are indexed,
ensuring the first render has a complete view of cluster state.

## Events

### Subscribes To

- **ResourceIndexUpdatedEvent**: Resource change (debounced)
- **IndexSynchronizedEvent**: All resources synced (immediate)
- **HTTPResourceUpdatedEvent**: HTTP content change (debounced)
- **HTTPResourceAcceptedEvent**: HTTP content accepted (immediate)
- **DriftPreventionTriggeredEvent**: Drift prevention (immediate)

### Publishes

- **ReconciliationTriggeredEvent**: Reconciliation requested

## Configuration

```go
type Config struct {
    DebounceInterval time.Duration  // Default: 500ms
}
```

## Constants

```go
const (
    DefaultDebounceInterval = 500 * time.Millisecond
    EventBufferSize        = 100
)
```

## Example Timing

```
t=0ms:    Resource change → Start 500ms timer
t=100ms:  Resource change → Reset timer (now expires at t=600ms)
t=300ms:  Resource change → Reset timer (now expires at t=800ms)
t=800ms:  Timer expires → Trigger reconciliation
```

Result: 3 changes batched into 1 reconciliation.

## License

See main repository for license information.
