# pkg/controller/credentialsloader - Credentials Loader

Development context for the CredentialsLoader component. The user-facing API surface is documented in `pkg/controller/credentialsloader/README.md`; this file only adds the internal-development perspective.

## When to Work Here

Work in this package when:

- Modifying Secret parsing logic (`processSecretChange` / `failInvalid`)
- Changing what counts as a valid set of credentials
- Wiring additional event-time observability into the loader

**DO NOT** work here for:

- Credential schema (`Credentials` struct, `ParseSecretData`, `LoadCredentials`, `ValidateCredentials`) → `pkg/core/config`
- Watching the Secret in production → that's `watcher.NewSingle` in `pkg/k8s/watcher`, wired by `pkg/controller/watchers.go` (which publishes `SecretResourceChangedEvent`). The `SecretWatcher` struct that lives in this package (`secret_watcher.go`) is **not** used by the controller — it's a self-contained informer-based alternative kept around for tests and tooling.

## Package Purpose

Stage-1 event adapter that converts a Kubernetes `Secret` change into the controller's `config.Credentials` value. It is built on the shared `pkg/controller/resourceloader.BaseLoader` scaffold (which itself wraps `pkg/controller/component.Base`), so the only loader-specific logic in this file is `ProcessEvent` → `processSecretChange`.

## Architecture

```
                       (single-resource informer)
pkg/k8s/watcher.NewSingle                                          pkg/controller/watchers.go
        │
        │  on Add / Update of the Secret
        ▼
events.SecretResourceChangedEvent{Resource: *unstructured.Unstructured}
        │
        ▼
CredentialsLoaderComponent.ProcessEvent          (this package)
        ├─ extract `data` map (still base64 strings)
        ├─ config.ParseSecretData (base64 → []byte per key)
        ├─ config.LoadCredentials (rejects missing / empty username or password)
        └─ publish:
             • CredentialsUpdatedEvent{Credentials, SecretVersion}   on success
             • CredentialsInvalidEvent{SecretVersion, Error}         on failure
```

Notes worth knowing before editing:

- The event payload is `*unstructured.Unstructured`; `data` values come back as base64-encoded strings (Kubernetes does **not** auto-decode through unstructured). Decoding is done by `config.ParseSecretData`, not by the loader directly — keep the contract there.
- `resourceVersion` is read off the unstructured resource by the loader itself; there's no separate version field on the inbound event. Both outbound events carry it as `SecretVersion` so downstream subscribers can correlate against the live `Secret`.
- A `CredentialsInvalidEvent` does **not** roll back previously-accepted credentials — it just signals failure; the previously-accepted `Credentials` stay in effect until a valid Secret is seen.
- `Start(ctx)` is promoted from `component.Base`; this loader does not define its own.

## Usage

```go
loader := credentialsloader.NewCredentialsLoaderComponent(bus, logger)
go loader.Start(ctx)
```

Subscription happens inside the constructor (via `BaseLoader`), which is why this is safe to call before `bus.Start()`. Buffered `SecretResourceChangedEvent`s from the watcher's initial sync are delivered correctly.

## Common Pitfalls

### Adding new required Secret keys

`config.LoadCredentials` is the gate. Do not duplicate that check in the loader — it's already there, and tests live alongside `LoadCredentials` in `pkg/core/config`.

### Confusing the two SecretWatcher implementations

There are two ways to watch a Secret in this codebase:

| Path | Used by production? | What publishes `SecretResourceChangedEvent`? |
|------|--------------------|-----------------------------------------------|
| `pkg/k8s/watcher.NewSingle` (typed via `types.SingleWatcherConfig`) | **Yes** — see `pkg/controller/watchers.go:137` | Yes (`bus.Publish(events.NewSecretResourceChangedEvent(obj))`) |
| `pkg/controller/credentialsloader.NewSecretWatcher` | No — unused outside its own package | Yes, but nothing wires it up |

If you're wiring a new component that needs Secret events, follow `watchers.go`; don't reach for `credentialsloader.NewSecretWatcher`.

### Logging at the wrong level

`failInvalid` logs at `Error` deliberately — invalid credentials block deployment, so this should be visible. Don't quiet it down to `Warn`; if a flap is genuinely expected (e.g. during rotation tests), gate on the caller, not the loader.
