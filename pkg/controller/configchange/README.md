# pkg/controller/configchange

Configuration validation orchestrator and reinitialization signaller.

## Overview

`ConfigChangeHandler` bridges configuration parsing, validation, and controller reinitialization. It does **not** watch any Kubernetes resource — that's the job of the CRD watcher in `pkg/controller`. Instead it consumes events:

1. **Validation orchestration** — subscribes to `ConfigParsedEvent`, fans a `ConfigValidationRequest` out to all registered validators (`basic`, `template`, `jsonpath`) using the bus's scatter-gather (`bus.Request`), aggregates the `ConfigValidationResponse` events, and publishes either `ConfigValidatedEvent` or `ConfigInvalidEvent`.
2. **Reinitialization signal** — subscribes to its own `ConfigValidatedEvent` output and forwards the validated config on a channel back to the controller, debounced so rapid CRD updates coalesce into a single reinit.

This package also contains `StatusUpdater`, which writes validation results back onto the `HAProxyTemplateConfig` CRD's status subresource.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/configchange"

configChangeCh := make(chan *coreconfig.Config, 1)

handler := configchange.NewConfigChangeHandler(
    bus,
    logger,
    configChangeCh,
    []string{"basic", "template", "jsonpath"}, // validator names that must respond
    0,                                         // 0 → DefaultReinitDebounceInterval (2s)
)
go handler.Start(ctx)

// Elsewhere: react to the reinit signal
for cfg := range configChangeCh {
    // controller restarts its iteration with cfg
}
```

The handler also exposes `SetInitialConfigVersion` and `EnableReinitialization` to suppress the bootstrap `ConfigValidatedEvent` that the CRD watcher emits at startup; without those calls every cold start would reinit itself.

## Events

- Subscribes: `ConfigParsedEvent`, `ConfigValidatedEvent`, `BecameLeaderEvent`, `CredentialsUpdatedEvent`, `CertParsedEvent`
- Publishes: `ConfigValidationRequest` (scatter), `ConfigValidatedEvent`, `ConfigInvalidEvent`
- Receives: `ConfigValidationResponse` (gather — collected via `bus.Request`, not a registered responder)

## License

Apache-2.0 — see root `LICENSE`.
