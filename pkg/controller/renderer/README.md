# pkg/controller/renderer

Pure render service that turns the controller's templates + Kubernetes state into a complete HAProxy configuration plus auxiliary files. Wraps `pkg/templating.Engine` with the controller-side concerns (path resolution, template context building, status-patch collection).

## Overview

Two surfaces live in this package:

- **`RenderService`** (`service.go`) — the synchronous, library-style API used in production. The render-validate pipeline (`pkg/controller/pipeline.Pipeline`) calls `service.Render(ctx, storeProvider)` directly, with no event hop. The leader-only Coordinator drives the pipeline; it then publishes `TemplateRenderedEvent` itself.
- **`Component`** (`component.go`) — an event-adapter wrapper around the same engine that subscribes to `ReconciliationTriggeredEvent` and publishes `TemplateRenderedEvent` / `TemplateRenderFailedEvent`. Currently used only by tests; the production wiring went straight to `RenderService` to avoid the extra event hop.

For new code use `RenderService`. The `Component` form is documented here only because the test fixtures still build it.

## Quick Start (RenderService)

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"

svc := renderer.NewRenderService(&renderer.RenderServiceConfig{
    Engine:             templateEngine,        // pre-compiled *templating.Engine
    Config:             cfg,                   // built from the HAProxyTemplateConfig CRD
    Logger:             logger,
    Capabilities:       capabilities,          // from local HAProxy probe
    HAProxyPodStore:    haproxyPodStore,       // optional; needed for {{ controller.haproxy_pods }}
    HTTPStoreComponent: httpStoreComponent,    // optional; needed for {{ http.Fetch(...) }}
    CurrentConfigStore: currentConfigStore,    // optional; needed for slot-aware server assignment
})

result, err := svc.Render(ctx, storeProvider) // *RenderResult, error
```

`RenderResult` carries everything downstream consumers need:

- `HAProxyConfig string` — the rendered config
- `AuxiliaryFiles *dataplane.AuxiliaryFiles` — maps + general files + SSL certificates + crt-list files
- `StatusPatches []templating.StatusPatch` — status mutations templates registered during the render
- `DurationMs int64` — wall time
- `AuxFileCount int` — convenience aggregate

Path resolution uses *relative* paths derived from `cfg.Dataplane.{MapsDir,SSLCertsDir,GeneralStorageDir}`. The rendered config relies on HAProxy's `default-path origin <baseDir>` directive, so the same render output works in:

- Local validation, where the validation service swaps `baseDir` for a temp directory.
- Production deployment, where `baseDir` is wherever the Dataplane API actually writes auxiliary files.

## Template Context

The rendering context is assembled by `pkg/controller/rendercontext.Builder` (see its README for the full key list). The `resources` map exposes one `*rendercontext.StoreWrapper` per `spec.watchedResources` entry; templates iterate them with `.List()` / `.Get(keys...)` / `.GetSingle(keys...)`.

`StoreWrapper` lazy-caches `.List()` per render — every resource is unwrapped from `*unstructured.Unstructured` to a plain map on the first call and reused for the rest of the reconciliation. `.Get` / `.GetSingle` unwrap on demand for the matched subset only.

## Component (Test-Only)

```go
component, err := renderer.New(
    bus,
    cfg,
    storeMap,           // map[string]stores.Store
    haproxyPodStore,    // stores.Store
    currentConfigStore, // *currentconfigstore.Store
    capabilities,       // dataplane.Capabilities
    logger,
)
go component.Start(ctx) // method is Start, not Run — matches the lifecycle.Component contract
```

Subscribes to `ReconciliationTriggeredEvent`; publishes `TemplateRenderedEvent` / `TemplateRenderFailedEvent`. The seven-arg constructor (no `RenderServiceConfig` struct) is the historical event-driven shape; the production controller stopped using it once the pipeline went synchronous.

## See Also

- [`pkg/controller/pipeline`](../pipeline/) — calls `RenderService.Render` then runs validation
- [`pkg/controller/rendercontext`](../rendercontext/) — the Builder that assembles every render's template context
- [`pkg/templating`](../../templating/) — the template engine `RenderService` wraps
- [`pkg/controller/reconciler`](../reconciler/) — Coordinator that drives the pipeline (leader-only)

## License

Apache-2.0 — see root `LICENSE`.
