# pkg/controller/renderer

Renderer component — template rendering for HAProxy configuration.

## Overview

Event-driven component that renders HAProxy configuration and auxiliary files from templates using the current resource state. Wraps the pure `pkg/templating.Engine` in a `pkg/controller/component.Base`-backed event adapter.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"

component, err := renderer.New(
    bus,
    cfg,                 // *config.Config built from the HAProxyTemplateConfig CRD
    storeMap,            // map[string]stores.Store — one per spec.watchedResources entry
    haproxyPodStore,     // stores.Store of HAProxy pods (from discovery)
    currentConfigStore,  // *currentconfigstore.Store — last-deployed HAProxy config
    capabilities,        // dataplane.Capabilities (HAProxy version-derived)
    logger,
)
if err != nil { /* ... */ }
go component.Run(ctx)
```

The constructor is `renderer.New` and the type it returns is `*renderer.Component` (not `RendererComponent`). For the pure rendering surface used by the pipeline and the dry-run validator, see `renderer.NewRenderService(*RenderServiceConfig)` in `service.go`.

## Events

- Subscribes: `ReconciliationTriggeredEvent`
- Publishes: `TemplateRenderedEvent`, `TemplateRenderFailedEvent`

## Template Context

The renderer builds a context with all watched Kubernetes resources:

```
{
  "resources": {
    "ingresses": *StoreWrapper,   // .List() / .Get(keys...) / .GetSingle(keys...)
    "services":  *StoreWrapper,
    "endpoints": *StoreWrapper,
    // ... one entry per spec.watchedResources key
  }
}
```

### StoreWrapper Performance

`StoreWrapper` unwraps `unstructured.Unstructured` objects to plain maps for template access:

- **`.List()`** — lazy-cached: unwraps every resource on the first call, caches the result for the rest of this reconciliation.
- **`.Get(keys...)` / `.GetSingle(keys...)`** — on-demand: unwraps only the matched resources each call (typically small result sets).

So templates pay the unwrapping cost once per reconciliation no matter how many times they call `List()`.

## License

Apache-2.0 — see root `LICENSE`.
