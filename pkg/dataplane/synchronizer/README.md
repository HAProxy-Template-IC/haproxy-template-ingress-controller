# pkg/dataplane/synchronizer

Executes a list of comparator operations against the HAProxy Dataplane API inside an open transaction.

## Overview

`SyncOperations` is the only exported entry point. Given a `[]comparator.Operation` produced by `pkg/dataplane/comparator`, it groups them by `Priority()`, runs each priority group in parallel (capped by `maxParallel`), and stops at the first error. The caller owns the surrounding transaction lifecycle and the post-commit reload tracking.

This is a **library**, not a component — there's no goroutine, no lifecycle, no event publishing. Reach for it from inside `dataplane.Client.Sync` or your own transaction-aware wrapper; don't wire it directly into the controller's event flow.

## Quick Start

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
    "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
    "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/synchronizer"
)

adapter := client.NewVersionAdapter(dpClient, 3)
err := adapter.ExecuteTransaction(ctx, func(ctx context.Context, tx *client.Transaction) error {
    _, err := synchronizer.SyncOperations(ctx, dpClient, diff.Operations, tx, 80)
    return err
})
```

`maxParallel = 0` means no cap.

`SyncOperationsResult` is always returned empty — its `ReloadTriggered` / `ReloadID` fields exist for future use but the function never sets them, because `SyncOperations` runs *inside* the transaction and doesn't see the commit response that carries the `Reload-ID` header. The production caller (`pkg/dataplane/orchestrator_execution.go`) discards the result with `_, err := …` and reads reload information from the commit step instead. Treat the struct as a stable shape for forward-compat, not as a meaningful return value today.

## Why Group by Priority?

Operations within the same priority bucket have no ordering dependencies (e.g. ten new servers across different backends), so running them in parallel is a meaningful win. Cross-priority dependencies do exist — a frontend's `default_backend` must point at a backend that already exists — and the priority numbers in `pkg/dataplane/comparator/sections` encode those.

## See Also

- [`pkg/dataplane/comparator`](../comparator/) — produces the `[]comparator.Operation` consumed here
- [`pkg/dataplane/client`](../client/) — `Transaction`, `VersionAdapter`, and the per-version REST client
- [`pkg/dataplane`](../) — `Client.Sync` is the production caller; reach for that instead of this package directly when possible

## License

Apache-2.0 — see root `LICENSE`.
