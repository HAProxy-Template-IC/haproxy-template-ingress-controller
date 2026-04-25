# pkg/controller/credentialsloader

Stage-1 event adapter that turns `Secret` updates into internal `Credentials`. Subscribes to `SecretResourceChangedEvent`, calls `config.LoadCredentials` + `config.ValidateCredentials`, and publishes `CredentialsUpdatedEvent` or `CredentialsInvalidEvent`.

Like its siblings [`configloader`](../configloader/) and [`certloader`](../certloader/), it's built on the `pkg/controller/resourceloader.BaseLoader` scaffold — the event-loop plumbing is shared, only the parse step differs.

## Minimal Usage

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/credentialsloader"
    "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

loader := credentialsloader.NewCredentialsLoaderComponent(bus, logger)
go loader.Start(ctx)
```

Subscription happens in the constructor, so buffered `SecretResourceChangedEvent`s from the single-resource watcher's initial sync are delivered when `bus.Start()` is called.

## Required Secret Keys

Four non-empty keys are required — the two `dataplane_*` pairs authenticate against the production HAProxy instances, the `validation_*` pair authenticates against the local validation endpoint used by `haproxy -c`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: haproxy-credentials
type: Opaque
stringData:
  dataplane_username: admin
  dataplane_password: <random>
```

`config.ValidateCredentials` rejects either being empty after base64 decode. On failure the loader publishes `CredentialsInvalidEvent` with the specific field name; the previously-accepted credentials stay active.

## Event Contract

**In** — `SecretResourceChangedEvent{Resource, Version}` from a `SingleWatcher` that points at the `Secret` referenced by `spec.credentialsSecretRef`.

**Out** — `CredentialsUpdatedEvent{Credentials, Version}` on success, `CredentialsInvalidEvent{Reason, Version}` on failure.

## See Also

- [`pkg/core/config`](../../core/config/) — `LoadCredentials` + `ValidateCredentials` (the functions this adapter wraps)
- [`pkg/controller/resourceloader`](../resourceloader/) — shared event-loop base
- [`pkg/controller/configloader`](../configloader/) / [`certloader`](../certloader/) — sibling loaders built on the same pattern
- `docs/controller/docs/operations/security.md` — operator-facing credential rotation + secret-management guidance

## License

Apache-2.0 — see root `LICENSE`.
