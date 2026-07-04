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
    "gitlab.com/haproxy-haptic/haptic/pkg/k8s/leaderelection"
)

config := &leaderelection.Config{
    Enabled:         true, // New() returns an error if this is false
    Identity:        "pod-1",
    LeaseName:       "my-app-leader",
    LeaseNamespace:  "default",
    LeaseDuration:   15 * time.Second,
    RenewDeadline:   10 * time.Second,
    RetryPeriod:     2 * time.Second,
    ReleaseOnCancel: true,
}

callbacks := leaderelection.Callbacks{
    OnStartedLeading: func(ctx context.Context) {
        // ctx is cancelled the moment leadership is lost — derive any
        // long-running work from it so it stops naturally.
        log.Println("Became leader")
    },
    OnStoppedLeading: func() {
        log.Println("Lost leadership")
    },
    OnNewLeader: func(identity string) {
        log.Printf("New leader: %s", identity)
    },
}

elector, err := leaderelection.New(config, clientset, callbacks, logger)
if err != nil {
    panic(err)
}

// Start blocks until the parent context is cancelled or the lease loop
// errors, so run it on its own goroutine.
ctx := context.Background()
go func() {
    if err := elector.Start(ctx); err != nil {
        log.Printf("leader election ended: %v", err)
    }
}()
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

- `New(*Config, kubernetes.Interface, Callbacks, *slog.Logger) (*Elector, error)` — validates inputs and returns an unstarted elector. Returns an error when `Config.Enabled` is `false`, when `Identity`, `LeaseName`, or `LeaseNamespace` is empty, or when the clientset is `nil`.
- `Start(ctx) error` — runs the lease loop and blocks until `ctx` is cancelled, the underlying client errors, or an acquired lease is lost. Run on a goroutine. Leadership state is observed through the `Callbacks` (`OnStartedLeading` / `OnStoppedLeading` / `OnNewLeader`); there are no snapshot accessors.
- Lost-lease semantics: client-go's `LeaderElector.Run` returns permanently after a lost lease (it does not re-enter the acquire loop), so `Start` returns `nil` with the caller's context still alive. Callers that need re-election must restart `Start` themselves — the controller does this by reinitializing (`pkg/controller/leader.go`, `superviseElection`).

## Thread Safety

All public methods are thread-safe and can be called concurrently.
