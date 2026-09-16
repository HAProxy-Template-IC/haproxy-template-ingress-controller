# pkg/controller/rendercontext

Builds the template rendering context shared by every code path that renders HAProxy config.

## Overview

The renderer, validation-test runner, and benchmarks use this builder. Admission delegates to the renderer through the proposal pipeline. Callers select stores, capabilities, and render mode through options; the builder supplies their shared context structure.

The builder also produces a `*FileRegistry` (templates can register dynamically generated auxiliary files via this) and supports a `StoreWrapper` adapter that gives Scriggo templates the `List` / `Fetch` / `GetSingle` methods on top of `stores.Store` instances.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"

builder := rendercontext.NewBuilder(
    ctx,
    cfg,
    pathResolver,
    logger,
    rendercontext.WithStores(storeMap),
    rendercontext.WithHAProxyPodStore(haproxyPodStore),
    rendercontext.WithHTTPFetcher(httpWrapper),
    rendercontext.WithCurrentConfig(currentConfig),
)

res := builder.Build()
// res.Context is map[string]any ready to pass to engine.Render
// res.FileRegistry and the other output collectors are also available
// res.Err(ctx) reports cancellation or deferred resource-input failures
```

`ctx`, `cfg`, `pathResolver`, and `logger` are required positional arguments. The context cancels API-backed store reads when the render ends. Everything else is supplied through functional options. Defaults depend on the option: for example, capabilities default to false and render mode defaults to `reconcile`; `http` is omitted without a fetcher.

Store methods remain value-only for Scriggo, but they record read failures,
ambiguous `GetSingle` results, and typed conversion failures in the returned
`BuildResult`. Rendering callers must check `res.Err(ctx)` before accepting output.

## Context Keys

The context map produced by `Build()` carries the keys templates rely on:

| Key | Type | Source |
|-----|------|--------|
| `resources` | Schema-derived struct of resource stores | `WithStores` and typed-resource options |
| `controller` | `map[string]ResourceStore` containing `haproxy_pods` | `WithHAProxyPodStore` |
| `templateSnippets` | `[]string` (sorted) | `cfg.TemplateSnippets` keys |
| `fileRegistry` | `*FileRegistry` | always present |
| `statusPatchCollector` | `*templating.StatusPatchCollector` | Always present; also exposed as `BuildResult.StatusPatchCollector` |
| `pathResolver` | `*templating.PathResolver` | required |
| `dataplane` | `config.DataplaneConfig` | from `cfg.Dataplane` |
| `shared` | `*templating.SharedContext` | always present (per-render cache) |
| `capabilities` | `map[string]any` | `CapabilitiesToMap` of the supplied value; all false when omitted |
| `runtimeEnvironment` | `*templating.RuntimeEnvironment` | always present (`GOMAXPROCS` and friends) |
| `currentConfig` | `*renderplan.CurrentConfig` | `WithCurrentConfig` (optional; omitted when nil to dodge a Scriggo nil-pointer-initializer panic) |
| `http` | `templating.HTTPFetcher` | `WithHTTPFetcher` (optional) |
| `extraContext` | `map[string]any` | `cfg.TemplatingSettings.ExtraContext` (always set, possibly empty; top-level keys are also merged into the root context via `MergeExtraContextInto`) |

Adding a new context key means updating `Build()` plus the `pkg/templating/globals.go` declarations (so Scriggo knows the type at compile time).

## See Also

- [`pkg/templating`](../../templating/) — runtime variable typing and the engine that consumes this context
- [`pkg/controller/renderer`](../renderer/) — production caller; prepares inputs before calling `NewBuilder`
- [`pkg/controller/testrunner`](../testrunner/) — validation-test caller
- [`pkg/controller/dryrunvalidator`](../dryrunvalidator/) — webhook caller
- `pkg/controller/rendercontext/CLAUDE.md` — developer notes on adding new context keys

## License

Apache-2.0 — see root `LICENSE`.
