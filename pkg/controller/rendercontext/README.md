# pkg/controller/rendercontext

Builds the template rendering context shared by every code path that renders HAProxy config.

## Overview

Four call sites need to render templates with the same context shape: production reconciliation (renderer), validation tests (test runner), benchmarks, and the webhook dry-run validator. This package consolidates that construction so the four can't drift — the context map you get back is identical regardless of who built it.

The builder also produces a `*FileRegistry` (templates can register dynamically generated auxiliary files via this) and supports a `StoreWrapper` adapter that gives Scriggo templates the `List` / `Fetch` / `GetSingle` methods on top of plain `types.Store` instances.

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
    rendercontext.WithCurrentConfig(parsedCurrent),
)

res := builder.Build()
// res.Context is map[string]any ready to pass to engine.Render
// res.FileRegistry, res.StatusPatchCollector, res.RenderedResourceCollector are also available
```

`ctx`, `cfg`, `pathResolver`, and `logger` are required positional arguments. The context cancels API-backed store reads when the render ends. Everything else is supplied through functional options. Omitting an option just leaves the corresponding context key unset (templates that try to read it will see `nil`).

## Context Keys

The context map produced by `Build()` carries the keys templates rely on:

| Key | Type | Source |
|-----|------|--------|
| `resources` | `map[string]ResourceStore` (wrapped) | `WithStores` |
| `controller` | `map[string]ResourceStore` containing `haproxy_pods` | `WithHAProxyPodStore` |
| `templateSnippets` | `[]string` (sorted) | `cfg.TemplateSnippets` keys |
| `fileRegistry` | `*FileRegistry` | always present |
| `statusPatchCollector` | `*templating.StatusPatchCollector` | always present (collects `statusPatch()` calls from `filters_status.go`; also returned as `Build()`'s third value) |
| `pathResolver` | `*templating.PathResolver` | required |
| `dataplane` | `config.DataplaneConfig` | from `cfg.Dataplane` |
| `shared` | `*templating.SharedContext` | always present (per-render cache) |
| `capabilities` | `map[string]any` | always present — `CapabilitiesToMap` of the `WithCapabilities` value, or an all-false map when the option is omitted, so validation and production expose the identical key |
| `runtimeEnvironment` | `*templating.RuntimeEnvironment` | always present (`GOMAXPROCS` and friends) |
| `currentConfig` | `*parserconfig.StructuredConfig` | `WithCurrentConfig` (optional; omitted when nil to dodge a Scriggo nil-pointer-initializer panic) |
| `http` | `templating.HTTPFetcher` | `WithHTTPFetcher` (optional) |
| `extraContext` | `map[string]any` | `cfg.TemplatingSettings.ExtraContext` (always set, possibly empty; top-level keys are also merged into the root context via `MergeExtraContextInto`) |

Adding a new context key means updating `Build()` plus the `pkg/templating/globals.go` declarations (so Scriggo knows the type at compile time).

## See Also

- [`pkg/templating`](../../templating/) — runtime variable typing and the engine that consumes this context
- [`pkg/controller/renderer`](../renderer/) — production caller (synchronous render service; builds context directly via its own `buildRenderingContext`, not via `NewBuilder`)
- [`pkg/controller/testrunner`](../testrunner/) — validation-test caller
- [`pkg/controller/dryrunvalidator`](../dryrunvalidator/) — webhook caller
- `pkg/controller/rendercontext/CLAUDE.md` — developer notes on adding new context keys

## License

Apache-2.0 — see root `LICENSE`.
