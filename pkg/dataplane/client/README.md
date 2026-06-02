# pkg/dataplane/client

Low-level multi-version HAProxy Dataplane API client.

## Overview

Wraps `haproxytech/client-native` clients for HAProxy Dataplane API v3.0 / v3.1 / v3.2 / v3.3 and Enterprise variants. The version is auto-detected at construction time; downstream callers reach for `Clientset().V33()` (etc.) only when they need a version-specific endpoint, otherwise they go through the version-aware helpers in this package.

The package also provides `Transaction` (a typed wrapper around the Dataplane API's transaction lifecycle) and `(*Transaction).Abort` to cancel one. The HAPTIC controller no longer uses transactions in its main sync path — it pushes the full rendered config via `PushRawConfiguration` / `PushRawConfigurationSkipReload`. `CreateTransaction` + `Transaction.Abort` are retained for the enterprise integration tests that drive per-section CRUD endpoints directly; new production callers should use the higher-level entry points in `pkg/dataplane` instead.

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

// Push a new config (triggers HAProxy reload; use PushRawConfigurationSkipReload
// plus runtime actions when only server addresses changed).
reloadID, err := dpClient.PushRawConfiguration(ctx, newConfig, expectedVersion)
```

`client.New` takes a `context.Context` (used for the version-detection probe) plus a `*Config` pointer; the config is validated synchronously and `BaseURL`, `Username`, and `Password` are required.

## See Also

- [`pkg/dataplane`](../) — high-level `Client.Sync`/`DryRun`/`Diff` API; that's where most callers should start
- [`pkg/dataplane/comparator`](../comparator/) — produces the operation lists this package commits

## License

Apache-2.0 — see root `LICENSE`.
