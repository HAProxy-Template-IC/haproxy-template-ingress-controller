# pkg/controller/renderer

Pure render service that turns the controller's templates + Kubernetes state into a complete HAProxy configuration plus auxiliary files. Wraps `pkg/templating.Engine` with the controller-side concerns (path resolution, template context building, status-patch collection).

## Overview

`RenderService` (`service.go`) is synchronous. The reconciliation coordinator,
follower warmer, and admission/proposal pipeline call it directly; only the
leader deploys the resulting plan. The coordinator publishes `TemplateRenderedEvent`.

The renderer is a library, not an event-driven component. See `docs/adr/0001-renderer-is-synchronous-not-event-adapter.md` for the rationale.

## Quick Start (RenderService)

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
)

svc := renderer.NewRenderService(&renderer.RenderServiceConfig{
    Engine:             templateEngine,        // pre-compiled templating.Engine
    Config:             cfg,                   // built from the HAProxyTemplateConfig CRD
    Logger:             logger,
    Capabilities:       capabilities,          // from local HAProxy probe
    HAProxyPodStore:    haproxyPodStore,       // optional; needed for {{ controller.haproxy_pods }}
    HTTPStoreComponent: httpStoreComponent,    // optional; needed for {{ http.Fetch(...) }}
})

result, err := svc.Render(ctx, storeProvider, rendercontext.RenderModeReconcile) // *RenderResult, error
```

`RenderResult` binds the configuration, deployment plan, auxiliary files, status
patches, Events, and owned resources in immutable snapshots. Production callers
use `CycleSnapshot` and the output-specific snapshots. The mutable compatibility
fields (`AuxiliaryFiles`, `StatusPatches`, `Events`, and `RenderedResources`) stay
nil; use the `Materialize*` methods when a detached copy is required.

`PlanID` identifies the desired plan. `DurationMs`, `AuxFileCount`, and optional
`IncludeStats` report render cost.

Path resolution uses *relative* paths derived from `cfg.Dataplane.{MapsDir,SSLCertsDir,GeneralStorageDir}`. The rendered config relies on HAProxy's `default-path origin <baseDir>` directive, so the same render output works in:

- Local validation, where the validation service swaps `baseDir` for a temp directory.
- Production deployment, where `baseDir` is the agent's configuration directory.

## Template Context

`buildRenderingContext` prepares the resource stores, acknowledged configuration,
and HTTP input view, then delegates to `rendercontext.NewBuilder`. Watched
resources expose `List`, `Fetch`, and `GetSingle`; schema-backed resources support
typed field access. Store reads and typed projections are cached for reuse.

A Kubernetes read failure, typed-resource conversion failure, or ambiguous
`.GetSingle()` result aborts the render. Only a stale on-demand reference whose
live API object is already gone is treated as ordinary absence.

## See Also

- [`pkg/controller/pipeline`](../pipeline/) — calls `RenderService.Render` then runs validation
- [`pkg/controller/rendercontext`](../rendercontext/) — the Builder that assembles every render's template context
- [`pkg/templating`](../../templating/) — the template engine `RenderService` wraps
- [`pkg/controller/reconciler`](../reconciler/) — leader coordinator that drives deployment reconciliation

## License

Apache-2.0 — see root `LICENSE`.
