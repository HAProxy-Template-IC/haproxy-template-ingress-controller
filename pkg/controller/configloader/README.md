# pkg/controller/configloader

Stage-1 event adapter that turns `HAProxyTemplateConfig` CRD changes into internal config. Subscribes to `ConfigResourceChangedEvent`, runs `conversion.ParseCRD` on the unstructured resource, and publishes a `ConfigParsedEvent` with the resulting `*config.Config` plus the typed CRD wrapper.

This is a thin event-loop on top of `pkg/controller/resourceloader.BaseLoader` — same scaffold used by `credentialsloader` and `certloader`. The actual parsing lives in `pkg/controller/conversion`; this package just wires the event flow.

## Minimal Usage

```go
import (
    "context"

    "gitlab.com/haproxy-haptic/haptic/pkg/controller/configloader"
    "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

bus := events.NewEventBus(100)
loader := configloader.NewConfigLoaderComponent(bus, logger)

go loader.Start(ctx)
bus.Start()

// Upstream: a SingleWatcher for the HAProxyTemplateConfig CRD emits
// ConfigResourceChangedEvent{Resource: *unstructured.Unstructured}.
// Downstream: the validation coordinator consumes ConfigParsedEvent.
```

Subscription happens in `NewConfigLoaderComponent`, **before** `bus.Start()`, so any buffered `ConfigResourceChangedEvent` from the watcher's initial sync is delivered once the bus is released.

## Event Contract

**In**

```go
type ConfigResourceChangedEvent struct {
    Resource *unstructured.Unstructured  // HAProxyTemplateConfig CRD
    Version  string                      // resourceVersion
    // ... see pkg/controller/events
}
```

**Out — on successful parse**

```go
type ConfigParsedEvent struct {
    Config         *config.Config                  // internal struct
    TemplateConfig *v1alpha1.HAProxyTemplateConfig // typed CRD (metadata + spec)
    ConfigVersion  string                          // CRD resourceVersion
}
```

**Out — on failure**: nothing is published. Errors are logged with the CRD's namespace/name. A stale config stays active; the controller doesn't start a failed reinitialisation.

## What It Validates

`ParseCRD` rejects resources that aren't `haproxy-haptic.org/v1alpha1.HAProxyTemplateConfig` before conversion. Everything after that (port ranges, required fields, enum values) is done by the scatter-gather validators in `pkg/controller/validator`, not here. `configloader` is intentionally small — it converts, it doesn't judge.

## See Also

- [`pkg/controller/conversion`](../conversion/) — `ParseCRD` and `ConvertSpec` implementation
- [`pkg/controller/resourceloader`](../resourceloader/) — shared event-loop scaffold
- [`pkg/controller/credentialsloader`](../credentialsloader/) / [`pkg/controller/certloader`](../certloader/) — sibling loaders built on the same base
- [`pkg/controller/validator`](../validator/) — scatter-gather validators that consume `ConfigParsedEvent`
- [`pkg/core/config`](../../core/config/) — target struct for `ParseCRD`'s conversion

## License

Apache-2.0 — see root `LICENSE`.
