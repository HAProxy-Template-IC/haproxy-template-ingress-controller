# pkg/k8s/leaderelection

Pure leader election library wrapping `k8s.io/client-go/tools/leaderelection`.

## Overview

Provides a clean interface for leader election using Kubernetes Lease resources. This is a pure library with no dependencies on the event bus or controller coordination logic.

## Usage

```go
import (
    "context"
    "log/slog"
    "time"

    "k8s.io/client-go/kubernetes"
    "haproxy-template-ic/pkg/k8s/leaderelection"
)

// Create configuration
config := &leaderelection.Config{
    Enabled:         true,
    Identity:        "pod-1",
    LeaseName:       "my-app-leader",
    LeaseNamespace:  "default",
    LeaseDuration:   15 * time.Second,
    RenewDeadline:   10 * time.Second,
    RetryPeriod:     2 * time.Second,
    ReleaseOnCancel: true,
}

// Define callbacks
callbacks := leaderelection.Callbacks{
    OnStartedLeading: func(ctx context.Context) {
        log.Println("Became leader")
        // Start leader-only components
    },
    OnStoppedLeading: func() {
        log.Println("Lost leadership")
        // Stop leader-only components
    },
    OnNewLeader: func(identity string) {
        log.Printf("New leader: %s", identity)
    },
}

// Create elector
elector, err := leaderelection.New(config, clientset, callbacks, logger)
if err != nil {
    panic(err)
}

// Start leader election (blocks until context cancelled)
ctx := context.Background()
elector.Start(ctx)
```

## API

### Config

Configuration for leader election:

- `Enabled`: Whether leader election is active
- `Identity`: Unique identifier (usually pod name)
- `LeaseName`: Name of Lease resource
- `LeaseNamespace`: Namespace of Lease resource
- `LeaseDuration`: How long non-leaders wait before forcing acquisition
- `RenewDeadline`: How long leader retries before giving up
- `RetryPeriod`: Wait duration between retry attempts
- `ReleaseOnCancel`: Release leadership when context cancelled

### Callbacks

Event callbacks:

- `OnStartedLeading(ctx)`: Called when becoming leader
- `OnStoppedLeading()`: Called when losing leadership
- `OnNewLeader(identity)`: Called when new leader observed

### Elector

Main leader election type:

- `New()`: Create new elector
- `Start(ctx)`: Start leader election loop (blocks)
- `IsLeader()`: Check if currently leader
- `GetLeader()`: Get current leader identity

## Thread Safety

All public methods are thread-safe and can be called concurrently.
