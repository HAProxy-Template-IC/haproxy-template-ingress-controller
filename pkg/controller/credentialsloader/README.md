# pkg/controller/credentialsloader

Stage-1 event adapter that turns `Secret` updates into internal `Credentials`. Subscribes to `SecretResourceChangedEvent`, calls `config.ParseSecretData` + `config.LoadCredentials` (which rejects missing or empty `dataplane_username` / `dataplane_password`), and publishes `CredentialsUpdatedEvent` or `CredentialsInvalidEvent`. Stronger structural validation (`config.ValidateCredentials`) is applied separately at controller startup in `pkg/controller/config.go`.

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

Two non-empty keys are required (both used to authenticate against every HAProxy Dataplane API instance the controller talks to — production deployment and local validation alike):

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

`config.LoadCredentials` rejects either key being missing or decoded-to-zero-bytes. On failure the loader publishes `CredentialsInvalidEvent`; the previously-accepted credentials stay active.

## Event Contract

**In** — `SecretResourceChangedEvent{Resource}` from a `SingleWatcher` that points at the `Secret` referenced by `spec.credentialsSecretRef`. The `resourceVersion` is read from the unstructured resource by the loader itself; there's no separate version field on the event.

**Out** — `CredentialsUpdatedEvent{Credentials, SecretVersion}` on success, `CredentialsInvalidEvent{SecretVersion, Error}` on failure.

## See Also

- [`pkg/core/config`](../../core/config/) — `ParseSecretData` / `LoadCredentials` (the functions this adapter wraps), and `ValidateCredentials` for the startup-time structural check
- [`pkg/controller/resourceloader`](../resourceloader/) — shared event-loop base
- [`pkg/controller/configloader`](../configloader/) / [`certloader`](../certloader/) — sibling loaders built on the same pattern
- `docs/controller/docs/operations/security.md` — operator-facing credential rotation + secret-management guidance

## License

Apache-2.0 — see root `LICENSE`.
