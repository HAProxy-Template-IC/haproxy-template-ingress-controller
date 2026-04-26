# pkg/dataplane/client

Low-level multi-version HAProxy Dataplane API client.

## Overview

Wraps `haproxytech/client-native` clients for HAProxy Dataplane API v3.0 / v3.1 / v3.2 / v3.3 and Enterprise variants. The version is auto-detected at construction time; downstream callers reach for `Clientset().V33()` (etc.) only when they need a version-specific endpoint, otherwise they go through the version-aware helpers in this package.

The package also provides `Transaction` (a typed wrapper around the Dataplane API's transaction lifecycle) and `VersionAdapter`, which owns commit/abort and the 409-conflict retry loop. Use `VersionAdapter.ExecuteTransaction` rather than calling `(*DataplaneClient).CreateTransaction(ctx, version)` and `(*Transaction).Commit` / `Abort` yourself — the adapter exists precisely so callers don't have to reason about transaction lifetime, error-classification, or 409 retries themselves. (Note: the lifecycle verbs are *Commit* and *Abort*; there is no `Rollback` and no `StartTransaction` — both names appeared in older drafts of this file.)

## Quick Start

```go
import (
    "context"

    "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

dpClient, err := client.New(ctx, &client.Config{
    BaseURL:  "http://haproxy-dataplane:5555",
    Username: "admin",
    Password: "password",
})
if err != nil { /* ... */ }

caps := dpClient.Clientset().Capabilities()
if caps.SupportsCrtList { /* HAProxy 3.2+ — use crt-list storage endpoints */ }

// Transactional change with version-conflict retry baked in
adapter := client.NewVersionAdapter(dpClient, 3) // 3 = max retries on 409
err = adapter.ExecuteTransaction(ctx, func(ctx context.Context, tx *client.Transaction) error {
    // run operations through tx.ID
    return nil
})
```

`client.New` takes a `context.Context` (used for the version-detection probe) plus a `*Config` pointer; the config is validated synchronously and `BaseURL`, `Username`, and `Password` are required.

## See Also

- [`pkg/dataplane`](../) — high-level `Client.Sync`/`DryRun`/`Diff` API; that's where most callers should start
- [`pkg/dataplane/synchronizer`](../synchronizer/) — runs operation lists inside a transaction created by `VersionAdapter`
- [`pkg/dataplane/comparator`](../comparator/) — produces the operation lists this package commits

## License

Apache-2.0 — see root `LICENSE`.
