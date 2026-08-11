# pkg/controller/configchange

Configuration validation orchestrator and reinitialization signaller.

## Overview

`ConfigChangeHandler` bridges configuration parsing, validation, and controller reinitialization. It does **not** watch any Kubernetes resource — that's the job of the CRD watcher in `pkg/controller`. Instead it consumes events:

1. **Validation orchestration** — subscribes to `ConfigParsedEvent`, fans a `ConfigValidationRequest` out to all registered validators (`basic`, `template`, `jsonpath`, `validationtests`) using the bus's scatter-gather (`bus.Request`), aggregates the `ConfigValidationResponse` events, and publishes either `ConfigValidatedEvent` or `ConfigInvalidEvent`. The scatter-gather can take tens of seconds, so it runs off the event loop — single-flight, latest-wins for parsed configs arriving mid-validation — keeping side events responsive throughout.
2. **Reinitialization signal** — keeps the running iteration's active snapshot separate from its latest accepted candidate and hands one authoritative snapshot to the controller. Config, credential, and effective-resolution reasons are tracked independently, so superseding a config candidate can't discard a credential or schema reload. Retiring an accepted candidate also replays the active snapshot to state consumers without producing another validation verdict or reload.

This package also contains `StatusUpdater`, which writes validation results back onto the `HAProxyTemplateConfig` CRD's status subresource.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/configchange"

configChangeCh := make(chan *configchange.ReloadRequest, 1)

handler := configchange.NewConfigChangeHandler(
    bus,
    logger,
    configChangeCh,
    []string{"basic", "template", "jsonpath"}, // validator names that must respond
    0,                                         // 0 → DefaultReinitDebounceInterval (2s)
)
go handler.Start(ctx)

// Elsewhere: react to the reinit signal
for request := range configChangeCh {
    // controller restarts its iteration with request.Snapshot
}
```

The next iteration consumes the snapshot rather than refetching a newer, unvalidated CR. A newer parsed config retracts an accepted candidate until that newer generation passes, while independent credential and schema reloads continue from the active snapshot. Schema changes re-resolve the selected raw config and run the complete config-validation contract before activation. Exact startup-version echoes are ignored; newer changes observed during startup are replayed latest-wins by `EnableReinitialization`.

## Events

- Subscribes: `ConfigParsedEvent`, `ConfigValidatedEvent`, `BecameLeaderEvent`, `CredentialsUpdatedEvent`
- Publishes: `ConfigValidationRequest` (scatter), `ConfigValidatedEvent`, `ConfigInvalidEvent`
- Receives: `ConfigValidationResponse` (gather — collected via `bus.Request`, not a registered responder)

## License

Apache-2.0 — see root `LICENSE`.
