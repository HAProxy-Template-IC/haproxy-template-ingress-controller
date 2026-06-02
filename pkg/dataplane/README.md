# pkg/dataplane

Pure library for synchronising HAProxy configurations over the [Dataplane API](https://www.haproxy.com/documentation/haproxy-data-plane-api/). Given a target endpoint and a desired config string (plus optional auxiliary files), the library brings HAProxy into that state by pushing the full rendered config, applying runtime-eligible server changes via the runtime API to avoid reloads whenever possible.

Module path: `gitlab.com/haproxy-haptic/haptic`. Source is authoritative (`go doc ./pkg/dataplane`); this README is a short map.

## What the Library Does

1. Parse the desired config with [`haproxytech/client-native`](https://github.com/haproxytech/client-native) (syntax).
2. Optionally run `haproxy -c` on it (semantics) — see `validator.go`.
3. Fetch the current config from the Dataplane API and compare section-by-section to classify the changes as runtime-eligible (server field updates) or structural.
4. Push the full rendered config in one request: a `skip_reload` push carrying `X-Runtime-Actions` when every change is a runtime-eligible server-field update (no reload), otherwise a `force_reload` push.
5. Sync auxiliary files (maps, SSL certs, general files, crt-lists) in three phases — pre-config, config, post-config — so the main config never references a file that doesn't exist yet.
6. Retry transient connection errors and surface structured errors (`ValidationError`, `ParseError`, `ConflictError`, etc.).

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

The `DefaultSyncOptions()` constructor returns the recommended baseline. Behaviour fields:

```go
opts := &dataplane.SyncOptions{
    Timeout:                   2 * time.Minute,    // overall sync deadline (default 2m)
    VerifyReload:              true,               // poll reload-status until done (default true)
    ReloadVerificationTimeout: 10 * time.Second,   // upper bound on the reload poll (default 10s)
}
```

`SyncOptions` also exposes optimisation fields the controller's pipeline populates between calls — leave them zero unless you're feeding a parser/checksum result you already have:

| Field | What it does |
|-------|--------------|
| `PreParsedConfig *parser.StructuredConfig` | Skips parsing `desiredConfig` if non-nil. Set by callers that already parsed the config (e.g. the validation pipeline). |
| `CachedCurrentConfig *parser.StructuredConfig` + `CachedConfigVersion int64` | Used together: `GetVersion()` is consulted first, and the expensive `GetRawConfiguration()`+parse round-trip is skipped if the live version on the pod matches `CachedConfigVersion`. |
| `ContentChecksum string` + `LastDeployedChecksum string` | Used together: when both are set and equal, the orchestrator skips the auxiliary-file comparison entirely (no downloads from HAProxy). Drift-prevention syncs should leave `LastDeployedChecksum` empty to force a real check. |

`DryRunOptions()` returns the preview variant — `VerifyReload: false` (no reload happens) and a shorter timeout (1m).

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
| Three-phase sync workflow (orchestrator + comparison + raw-push apply [runtime / reload] + per-version cache + runtime API) | `orchestrator_*.go` |
| HAProxy three-phase validation (`haproxy -c` + OpenAPI schema + client-native syntax) | `validate_haproxy.go`, `validate_schema.go`, `validate_syntax.go`, `validator.go` |
| Version detection (local binary + remote API) and capability matrix for DP API v3.0 / v3.1 / v3.2 / v3.3 | `version.go`, `capabilities.go` |
| Dataplane API client (dispatcher pattern, transactions, retries) | `client/` (+ `client/enterprise/` for ALOHA / WAF / Bot / UDP / Keepalived / git / dynamic-update / logging / misc) |
| Config parsing via client-native | `parser/` (+ `parser/enterprise/` for Enterprise sections) |
| Fine-grained diff engine | `comparator/` + `comparator/sections/` (operations are pure descriptors; execution is raw push in `orchestrator_*.go`) |
| Raw-push execution (structural + runtime paths) | `orchestrator_*.go` (integrated) |
| Auxiliary file sync (maps, SSL, SSL-CA, general files, crt-list) | `auxiliaryfiles/` |
| Generated per-model OpenAPI validators | `validators/` |

Endpoint discovery (probing HAProxy pods, picking up credentials, etc.) is the controller's job in `pkg/controller/discovery`, not this package's.

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
    // top-level sync failure with a hint and phase (stage) context
}

var valErr *dataplane.ValidationError
var parseErr *dataplane.ParseError
var connErr *dataplane.ConnectionError
var conflictErr *dataplane.ConflictError
var opErr *dataplane.OperationError
```

For user-facing surfaces (webhook responses, CLI output), call `dataplane.SimplifyValidationError(err)` / `dataplane.SimplifyRenderingError(err)` to turn verbose library errors into a single readable line. Internal logs and metrics should keep the full chain.

## Common Pitfalls

- **Skipping aux-file pre-sync.** If `haproxy.cfg` references `maps/host.map` and the file hasn't been uploaded yet, HAProxy validation fails. `AuxiliaryFiles` plus the orchestrator handle this automatically; bypassing them is almost always a bug.
- **Leaking transactions.** The controller does not use transactions in its sync path — all production changes go through `Sync` / `PushRawConfiguration` / `PushRawConfigurationSkipReload`. `CreateTransaction` is retained only for enterprise integration tests; if you call it directly you own commit/abort responsibility (abort = `DELETE /v3/.../transactions/<id>`).
- **Hand-rolling a retry loop.** The orchestrator re-resolves the config version on each sync and retries transient connection errors via `client.WithRetry` (3 attempts); don't layer your own retry loop on top.
- **Pushing aux-file deletes before config.** Phase 3 must run *after* the main config is applied so we're not deleting files the live config still references.
- **Using `List()` patterns at the dataplane layer.** This package operates on parsed config structures, not on Kubernetes stores — the `.List()` / `.Fetch()` semantics from `pkg/k8s` are irrelevant here.

`pkg/dataplane/CLAUDE.md` has the longer catalogue (retry logic, parser error wrapping, per-section comparator examples) plus the multi-version dispatch pattern and the three-phase sync rationale.

## Zero-Reload Rules of Thumb

A small set of server-level changes can apply through the runtime API without reloading HAProxy:

- `Weight`, `Address`, `Port`, `Maintenance` (enable/disable/drain)
- `AgentCheck`, `AgentAddr`, `AgentSend`, `HealthCheckPort`
- Frontend `Maxconn`
- Map file content, ACL file content, SSL certificate content (via storage API)

Everything else — creating or deleting a server, changing `check` / `inter` / `ssl` settings, touching bind addresses, frontend/backend structure, rules, filters — triggers a reload. The comparator detects this and the orchestrator picks the cheapest path. To maximise zero-reload updates, templates should keep individual `server` lines to `address:port [enabled|disabled]` and push all other options into `default-server`.

## Testing

```bash
go test ./pkg/dataplane/...          # unit + comparator tests
go test ./pkg/dataplane/... -race    # race detector
```

Integration tests that need a real HAProxy instance live under `tests/integration` and `tests/acceptance`.

## See Also

- `pkg/dataplane/CLAUDE.md` — multi-version dispatch, comparator patterns, parser quirks, testing strategies
- `pkg/dataplane/comparator/sections/` — section factories + the JSON-marshal trick for converting unified `dataplaneapi.*` models into per-version `v3{0,1,2,3}.*` types
- `pkg/controller/deployer` — event adapter that wires `Client.Sync` into the controller's reconciliation pipeline
- `pkg/controller/discovery` — probes HAProxy pods + builds the `*Endpoint` slice that gets handed to `Sync`
- `docs/controller/docs/supported-configuration.md` — user-facing view of which HAProxy sections / fields are synced

## License

Apache-2.0 — see root `LICENSE`.
