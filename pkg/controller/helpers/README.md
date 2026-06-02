# pkg/controller/helpers

Shared utility for constructing a template engine from a `*config.Config`.

## Overview

Several code paths need to build a template engine from the controller's loaded config: the reconciliation wiring (`pkg/controller/reconciliation.go`), the webhook wiring (`pkg/controller/webhook.go`), the template validator (`pkg/controller/validator/template.go`), the CRD admission webhook (`pkg/controller/webhook/configvalidator.go`), and the `haptic-controller validate` CLI command. This package consolidates the construction so a change to template-extraction or filter-registration policy lands in one place.

This is a **utility package** — pure functions, no event-bus dependency, no goroutines.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"

// Default — Scriggo engine, all standard filters auto-registered, fail()
// auto-registered, post-processors auto-extracted from cfg.
engine, err := helpers.NewEngineFromConfig(cfg, nil, nil)

// Full-featured: pass custom filters / globals / post-processor overrides,
// add domain-specific Scriggo type declarations, enable include profiling.
engine, err = helpers.NewEngineFromConfigWithOptions(
    cfg,
    customGlobals,
    nil,                            // nil → auto-extract from cfg
    map[string]any{"someType": (*someType)(nil)}, // additional Scriggo declarations
    helpers.EngineOptions{EnableProfiling: true},
)
```

The standard filter set (`sort_by`, `glob_match`, `b64decode`, `strip`, `trim`, `debug`) and the `fail()` function are registered inside the engine itself — pass `nil` for the corresponding parameters unless you have *additional* custom filters or globals.

## ExtractTemplatesFromConfig

Used when a caller needs the list of templates without instantiating an engine — e.g. for logging the template count at startup, or for tooling that inspects the loaded config:

```go
extraction := helpers.ExtractTemplatesFromConfig(cfg)
// extraction.AllTemplates → map[string]string for the engine's filesystem
// extraction.EntryPoints  → []string of explicitly-compiled templates
```

For Scriggo with `inherit_context`, only entry points are compiled explicitly; snippets are compiled on demand when referenced via `render` / `render_glob`.

## See Also

- [`pkg/templating`](../../templating/) — the engine this helper constructs
- [`pkg/controller/reconciliation.go`](../reconciliation.go) / [`webhook.go`](../webhook.go) / [`validator/template.go`](../validator/template.go) — the main production callers
- `pkg/controller/helpers/CLAUDE.md` — design notes (why all filters live inside the engine, etc.)

## License

Apache-2.0 — see root `LICENSE`.
