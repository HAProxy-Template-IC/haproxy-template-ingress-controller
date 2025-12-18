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

Creates a template engine from configuration with all standard components.

**Used by:**

- `pkg/controller/renderer` - Main HAProxy config rendering
- `pkg/controller/dryrunvalidator` - Webhook validation rendering
- `pkg/controller/testrunner` - Validation test execution
- `cmd/controller/validate.go` - CLI validation command

**Example:**

```go
engine, err := helpers.NewEngineFromConfig(cfg, nil, nil)
if err != nil {
    return err
}
```

### ExtractTemplatesFromConfig

Extracts all templates (haproxy.cfg, snippets, maps, files, certs) from configuration.

**Used when:**

- Logging template count during initialization
- Needing template list without creating an engine

**Example:**

```go
templates := helpers.ExtractTemplatesFromConfig(cfg)
logger.Info("Compiling templates", "count", len(templates))
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
- Renderer component: `pkg/controller/renderer/CLAUDE.md`
