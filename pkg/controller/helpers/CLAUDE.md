# pkg/controller/helpers - Shared Controller Utilities

Development context for shared utility functions in the controller layer.

## When to Work Here

Modify this package when:

- Adding shared utility functions used by multiple controller components
- Modifying template engine creation logic
- Adding common template extraction patterns

**DO NOT** modify this package for:

- Template rendering logic → Use `pkg/templating`
- Event coordination → Use `pkg/controller`
- Configuration parsing → Use `pkg/core/config`

## Package Purpose

Provides shared utility functions for the controller layer, reducing code duplication across components that need template engines.

This is a **utility package** with pure functions - no event dependencies, no state.

## Key Functions

### NewEngineFromConfig

Creates a template engine from configuration with all standard filters and the
`fail()` function pre-registered. Signature is
`(cfg, globalFunctions, postProcessorConfigs)` — both optional maps may be nil.

**Used by:**

- `pkg/controller/reconciliation.go` - Engine creation for the reconciliation pipeline
- `pkg/controller/validator/validationtests.go` - Validation-test rendering
- `cmd/haptic/validate.go` - CLI validation command
- `cmd/haptic/benchmark_render.go` - Benchmark rendering

```go
engine, err := helpers.NewEngineFromConfig(cfg, nil, nil)
if err != nil {
    return err
}
```

### NewEngineFromConfigWithOptions

Same as above plus two extras: `additionalDeclarations map[string]any` for
domain-specific Scriggo type declarations (e.g. `currentConfig`), and an
`EngineOptions` struct that currently only carries `EnableProfiling bool`. Use
this when you need profile data (`templating.IncludeStats`) or when a caller
needs to pass a runtime variable that the templating package shouldn't have to
know about.

```go
engine, err := helpers.NewEngineFromConfigWithOptions(
    cfg, nil, nil,
    map[string]any{"currentConfig": (*parserconfig.StructuredConfig)(nil)},
    helpers.EngineOptions{EnableProfiling: true},
)
```

### ExtractTemplatesFromConfig

Returns a `TemplateExtraction` (not a flat slice) summarising every template in
the config:

```go
type TemplateExtraction struct {
    AllTemplates map[string]string // Entry points + snippets, by name
    EntryPoints  []string          // Names that should be compiled explicitly
}
```

Snippets aren't entry points — Scriggo discovers them via
`render`/`render_glob` with `inherit_context`. Use this when you need the
template list without paying the cost of compiling an engine.

```go
extraction := helpers.ExtractTemplatesFromConfig(cfg)
logger.Info("Compiling templates",
    "total",        len(extraction.AllTemplates),
    "entry_points", len(extraction.EntryPoints),
)
```

## Design Notes

### All Standard Filters Are Internal

All standard template filters are registered internally by each engine:

- `sort_by` - Multi-field sorting
- `glob_match` - Glob pattern filtering
- `b64decode` - Base64 decoding
- `strip` - Whitespace removal
- `trim` - Character trimming
- `debug` - Development debugging

**Callers should NOT register these filters** - pass `nil` for the filters parameter.

### The fail() Function Is Auto-Registered

The Scriggo engine automatically registers the `fail()` function for template assertions.

**Callers should NOT pass fail in globalFunctions** - pass `nil` unless you have OTHER custom functions.

```go
// Good - fail() is auto-registered
engine, err := helpers.NewEngineFromConfig(cfg, nil, nil)

// Bad - redundant registration
functions := map[string]templating.GlobalFunc{
    "fail": templating.FailFunction,  // Already registered internally!
}
engine, err := helpers.NewEngineFromConfig(cfg, functions, nil)
```

### Engine Type Selection

The engine type is parsed from `cfg.TemplatingSettings.Engine`:

- `""` or `"scriggo"` → Scriggo engine (Go template syntax)

## Testing

Tests are in `templating_test.go`. They verify:

- Template extraction works correctly
- Engine creation with various configurations
- Error handling for invalid configurations

## Resources

- Template engine: `pkg/templating/CLAUDE.md`
- Configuration types: `pkg/core/CLAUDE.md`
- Renderer component: `pkg/controller/renderer/README.md` (no CLAUDE.md in that package)
