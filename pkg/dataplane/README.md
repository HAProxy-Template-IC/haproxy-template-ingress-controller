# pkg/dataplane

Pure library for synchronising HAProxy configurations over the [Dataplane API](https://www.haproxy.com/documentation/haproxy-data-plane-api/). Given a target endpoint and a desired config string (plus optional auxiliary files), the library brings HAProxy into that state using fine-grained operations that avoid reloads whenever possible.

Module path: `gitlab.com/haproxy-haptic/haptic`. Source is authoritative (`go doc ./pkg/dataplane`); this README is a short map.

## What the Library Does

1. Parse the desired config with [`haproxytech/client-native`](https://github.com/haproxytech/client-native) (syntax).
2. Optionally run `haproxy -c` on it (semantics) — see `validator.go`.
3. Fetch the current config from the Dataplane API, compare section-by-section, and emit a minimal list of create/update/delete operations.
4. Execute the operations inside a Dataplane API transaction, falling back to a raw config push if fine-grained sync hits a non-recoverable error.
5. Sync auxiliary files (maps, SSL certs, general files, crt-lists) in three phases — pre-config, config, post-config — so the main config never references a file that doesn't exist yet.
6. Retry on `409` version conflicts and surface structured errors (`ValidationError`, `ParseError`, `ConflictError`, etc.).

## Top-level API

```go
import (
    "context"
    "log"

    "gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

endpoint := &dataplane.Endpoint{
    URL:      "http://haproxy:5555/v3",
    Username: "admin",
    Password: "secret",
}

// One-shot convenience functions (create client + operation + close).
result, err := dataplane.Sync(ctx, endpoint, desiredConfig, auxFiles, nil)
diff,   err := dataplane.DryRun(ctx, endpoint, desiredConfig)
diff,   err := dataplane.Diff(ctx, endpoint, desiredConfig)
```

`Endpoint` is always passed as a pointer. `auxFiles` and `opts` are both `nil`-safe. `DryRun` and `Diff` are equivalent — both compare without applying.

For anything more than a single call, create a `Client` once and reuse it:

```go
client, err := dataplane.NewClient(ctx, endpoint)
if err != nil {
    log.Fatal(err)
}
defer client.Close()

result, err := client.Sync(ctx, desiredConfig, auxFiles, opts)
diff,   err := client.DryRun(ctx, desiredConfig)
```

### `SyncOptions`

```go
opts := &dataplane.SyncOptions{
    MaxRetries:       3,                // retries for 409 version conflicts (default 3)
    Timeout:          2 * time.Minute,  // overall operation deadline (default 2m)
    ContinueOnError:  false,            // keep going after a failing operation (default false)
    FallbackToRaw:    true,             // fall back to raw push on non-recoverable failure (default true)
    RawPushThreshold: 100,              // switch to raw push when > N operations would be applied (0 = disabled, the default)
    MaxParallel:      0,                // cap concurrent Dataplane API ops (0 = unlimited; not recommended for large configs)
}
```

Use `DryRunOptions()` if you want a preview-only variant with safe defaults.

### `AuxiliaryFiles`

```go
aux := &dataplane.AuxiliaryFiles{
    GeneralFiles:    []auxiliaryfiles.GeneralFile{...},
    SSLCertificates: []auxiliaryfiles.SSLCertificate{...},
    SSLCaFiles:      []auxiliaryfiles.SSLCaFile{...},
    MapFiles:        []auxiliaryfiles.MapFile{...},
    CRTListFiles:    []auxiliaryfiles.CRTListFile{...},  // v3.2+
}
```

`CRTListFiles` is only supported on Dataplane API v3.2+; unsupported entries fail fast with a capability error rather than silently.

## Sub-Package Map

| Purpose | Package |
|---------|---------|
| Public types and entry points (`Sync`, `DryRun`, `Diff`, `Client`, `Endpoint`, `SyncOptions`, `AuxiliaryFiles`) | `pkg/dataplane` (top level) |
| Three-phase sync workflow (orchestrator, comparison, execution) | `orchestrator_*.go` |
| HAProxy syntax + `haproxy -c` validator | `validator*.go` |
| Version detection and capability matrix for DP API v3.0 / v3.1 / v3.2 / v3.3 | `version.go`, `capabilities.go` |
| Dataplane API client (dispatcher pattern, transactions, retries) | `client/` |
| Config parsing via client-native | `parser/` |
| Fine-grained diff engine | `comparator/` + `comparator/sections/` |
| Operation executor | `synchronizer/` |
| Auxiliary file sync (maps, SSL, general files, crt-list) | `auxiliaryfiles/` |
| Endpoint discovery helpers | `discovery/` |
| Generated per-model OpenAPI validators | `validators/` |

## Versioning and Capabilities

The client detects the Dataplane API version by calling `/v3/info` and exposes a `Capabilities` struct that downstream code can query without needing a live connection:

```go
caps := client.Clientset().Capabilities()
if caps.SupportsCrtList {
    // v3.2+ only
}
```

For local-validation paths (where no Dataplane API is reachable), derive capabilities from a detected HAProxy binary version via `dataplane.CapabilitiesFromVersion(version)`. Passing `nil` returns a conservative all-false capability set — the safe default.

All public client methods route through a single `Dispatch()` dispatcher so adding a new API version only touches `client/dispatcher.go`. `pkg/dataplane/CLAUDE.md` has the full walkthrough (when to use `DispatchWithCapability`, `DispatchGeneric[T]`, how to add a new method).

## Error Types

```go
var syncErr *dataplane.SyncError
if errors.As(err, &syncErr) {
    // transaction-level failure with a hint and phase context
}

var valErr *dataplane.ValidationError
var parseErr *dataplane.ParseError
var connErr *dataplane.ConnectionError
var conflictErr *dataplane.ConflictError
var opErr *dataplane.OperationError
var fallbackErr *dataplane.FallbackError
```

For user-facing surfaces (webhook responses, CLI output), call `dataplane.SimplifyValidationError(err)` / `dataplane.SimplifyRenderingError(err)` to turn verbose library errors into a single readable line. Internal logs and metrics should keep the full chain.

## Common Pitfalls

- **Skipping aux-file pre-sync.** If `haproxy.cfg` references `maps/host.map` and the file hasn't been uploaded yet, HAProxy validation fails. `AuxiliaryFiles` plus the orchestrator handle this automatically; bypassing them is almost always a bug.
- **Leaking transactions.** If you reach into `pkg/dataplane/client` and call `dpClient.CreateTransaction(ctx, version)` yourself, you take on commit/abort responsibility (the Dataplane API has no rollback verb — abort = `DELETE /v3/.../transactions/<id>`). Almost always you want `client.NewVersionAdapter(dpClient, maxRetries).ExecuteTransaction(ctx, fn)` instead — it owns the lifecycle and the 409-retry loop. A leaked transaction blocks future writes until it times out on the Dataplane API side.
- **Comparing on 409.** Version conflicts (`409`) mean someone else moved the current config forward. The orchestrator re-fetches and retries up to `MaxRetries`; don't layer your own retry loop on top.
- **Pushing aux-file deletes before config.** Phase 3 must run *after* the main config is applied so we're not deleting files the live config still references.
- **Using `List()` patterns at the dataplane layer.** This package operates on parsed config structures, not on Kubernetes stores — the `.List()` / `.Fetch()` semantics from `pkg/k8s` are irrelevant here.

`pkg/dataplane/CLAUDE.md` has the longer catalogue (transaction retry, parser error wrapping, per-section comparator examples) plus the multi-version dispatch pattern and the three-phase sync rationale.

## Zero-Reload Rules of Thumb

A small set of server-level changes can apply through the runtime API without reloading HAProxy:

- `Weight`, `Address`, `Port`, `Maintenance` (enable/disable/drain)
- `AgentCheck`, `AgentAddr`, `AgentSend`, `HealthCheckPort`
- Frontend `Maxconn`
- Map file content, ACL file content, SSL certificate content (via storage API)

Everything else — creating or deleting a server, changing `check` / `inter` / `ssl` settings, touching bind addresses, frontend/backend structure, rules, filters — triggers a reload. The comparator detects this and the synchronizer picks the cheapest path. To maximise zero-reload updates, templates should keep individual `server` lines to `address:port [enabled|disabled]` and push all other options into `default-server`.

## Testing

```bash
go test ./pkg/dataplane/...          # unit + comparator tests
go test ./pkg/dataplane/... -race    # race detector
```

Integration tests that need a real HAProxy instance live under `tests/integration` and `tests/acceptance`.

## See Also

- `pkg/dataplane/CLAUDE.md` — multi-version dispatch, comparator patterns, parser quirks, testing strategies
- `pkg/dataplane/transform` — client-native ↔ Dataplane API model conversion (used by every section comparator)
- `pkg/controller/deployer` — event adapter that wires `Client.Sync` into the controller's reconciliation pipeline
- `docs/controller/docs/supported-configuration.md` — user-facing view of which HAProxy sections / fields are synced

## License

Apache-2.0 — see root `LICENSE`.
