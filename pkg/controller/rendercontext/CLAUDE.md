# pkg/controller/rendercontext - Template Context Builder

Development context for the centralized template rendering context builder.

## When to Work Here

Modify this package when:

- Adding new context keys that all templates need access to
- Changing how context is built (e.g., new options)
- Modifying helper functions like `MergeExtraContextInto`

**DO NOT** modify this package for:

- Template rendering logic → Use `pkg/templating`
- HAProxy capabilities → Use `pkg/dataplane`

## Package Purpose

This package consolidates template context creation from 4 previously duplicated locations:

| Original Location | Usage |
|-------------------|-------|
| `pkg/controller/renderer/context.go` | Production rendering |
| `pkg/controller/testrunner/runner.go` | Validation tests |
| `cmd/controller/benchmark.go` | Performance benchmarks |
| `pkg/controller/dryrunvalidator/component.go` | Webhook admission |

## Usage

```go
import "haproxy-template-ic/pkg/controller/rendercontext"

builder := rendercontext.NewBuilder(
    cfg,
    pathResolver,
    logger,
    rendercontext.WithStores(stores),
    rendercontext.WithHAProxyPodStore(haproxyPodStore),
    rendercontext.WithHTTPFetcher(httpWrapper),
    rendercontext.WithCapabilities(capabilities),
)

ctx, fileRegistry := builder.Build()
```

## Context Structure

The builder creates a context map with these keys:

| Key | Type | Description |
|-----|------|-------------|
| `resources` | `map[string]ResourceStore` | Wrapped Kubernetes resource stores |
| `controller` | `map[string]ResourceStore` | Controller metadata (haproxy_pods) |
| `templateSnippets` | `[]string` | Sorted snippet names |
| `fileRegistry` | `*FileRegistry` | Dynamic file registration |
| `pathResolver` | `*PathResolver` | File path resolution |
| `dataplane` | `config.Dataplane` | DataPlane API config |
| `capabilities` | `map[string]bool` | HAProxy feature flags (optional) |
| `shared` | `map[string]interface{}` | Cross-template cache |
| `runtimeEnvironment` | `*RuntimeEnvironment` | GOMAXPROCS, etc. |
| `http` | `HTTPFetcher` | HTTP resource fetching (optional) |
| `extraContext` | `map[string]interface{}` | User-defined variables |

## Functional Options

| Option | Purpose |
|--------|---------|
| `WithStores(stores)` | Set resource stores for `resources` map |
| `WithHAProxyPodStore(store)` | Set HAProxy pod store for `controller.haproxy_pods` |
| `WithHTTPFetcher(fetcher)` | Set HTTP fetcher for `http.Fetch()` |
| `WithCapabilities(caps)` | Set HAProxy capabilities for feature detection |

## Package Contents

This package contains:

- **Builder**: Constructs template rendering contexts with functional options pattern
- **StoreWrapper**: Wraps types.Store to provide template-friendly methods (List, Fetch, GetSingle)
- **FileRegistry**: Enables dynamic auxiliary file registration during template rendering
- **MergeAuxiliaryFiles**: Utility to combine static and dynamic auxiliary files
- **SortSnippetNames**: Helper to sort template snippet names alphabetically

## Dependencies

This package imports from:

- `pkg/core/config` - Config types
- `pkg/dataplane` - Capabilities, AuxiliaryFiles
- `pkg/k8s/types` - Store interface
- `pkg/templating` - ResourceStore, PathResolver, RuntimeEnvironment

## Testing

The builder is tested indirectly through the components that use it:

- Renderer tests verify production context creation
- TestRunner tests verify fixture-based contexts
- Benchmark tests verify performance characteristics

## Adding New Context Keys

When adding a new key to the template context:

1. Add the key in `Build()` method
2. Add an option if it's configurable
3. Update all callers if needed
4. Document in the table above
5. Update `pkg/templating/filters_scriggo.go` if Scriggo needs type info

## Resources

- Template engine: `pkg/templating/CLAUDE.md`
- Renderer component: `pkg/controller/renderer/CLAUDE.md`
- TestRunner: `pkg/controller/testrunner/CLAUDE.md`
- DryRunValidator: `pkg/controller/dryrunvalidator/CLAUDE.md`
