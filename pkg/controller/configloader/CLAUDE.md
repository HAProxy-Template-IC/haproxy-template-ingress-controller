# pkg/controller/configloader - Configuration Loader

Development context for the ConfigLoader component.

**API Documentation**: See `pkg/controller/configloader/README.md`

## When to Work Here

Work in this package when:

- Modifying HAProxyTemplateConfig CRD parsing logic
- Changing how configuration is extracted from the CRD's `spec`
- Adding validation before config parsing
- Debugging configuration loading issues

**DO NOT** work here for:

- Configuration schema definition → Use `pkg/core/config`
- Configuration validation → Use `pkg/controller/validator`
- CRD watching itself → the SingleWatcher is wired in `pkg/controller/iteration.go`; orchestration of validation lives in `pkg/controller/configchange`

## Package Purpose

Pure event-driven component that subscribes to `ConfigResourceChangedEvent` and converts the wrapped HAProxyTemplateConfig CRDs into the internal `*config.Config`. This is part of Stage 1 (Config Management) in the controller lifecycle.

**It merges a SET, not a single object.** The chart renders one config per
template library plus one for the operator (ADR-0014), and the component is
constructed with the ordered names it should merge. Each config has its own
watcher, so they arrive — and later change — one at a time. The component holds
the latest object per name and stays silent until every configured name has been
seen; a change to any one re-merges against the held copies of the others, so a
library change still loses to the operator's override.

A change event for a name it was not configured with is logged and dropped.

The controller is CRD-driven, not ConfigMap-driven; this component does **not** read raw ConfigMap data.

Key responsibilities:

- Type-assert the event payload to `*unstructured.Unstructured`
- Record it under its name, and wait until the whole configured set is present
- Run `conversion.MergeSpecs` over the set, in configured order, later wins
- Validate it's `haproxy-haptic.org/v1alpha1.HAProxyTemplateConfig` and run
  `conversion.ParseCRD` to produce `*config.Config` and the typed CRD wrapper
- Publish `ConfigParsedEvent` on success, versioned by
  `conversion.CompositeVersion` — the merged object carries only the primary's
  resourceVersion, and the redundant-reinit guard compares versions for equality,
  so a library-only change would otherwise be silently dropped
- Log a snippet-override line for each `templateSnippets` name defined by more
  than one config (an operator override is the expected case; two libraries
  colliding is a bug that used to resolve silently)
- Log errors for unsupported types, merge failures, or conversion failures (no
  event is published — the previously published config keeps serving, so a torn
  read during a rolling upgrade resolves itself on the next event)

## Architecture

```
ConfigResourceChangedEvent (from CRD SingleWatcher)
    ↓
ConfigLoaderComponent
    ├─ Type-assert *unstructured.Unstructured
    ├─ Validate apiVersion=haproxy-haptic.org/v1alpha1, kind=HAProxyTemplateConfig
    ├─ conversion.ParseCRD → *config.Config + typed CRD
    └─ Publish ConfigParsedEvent (Version filled, SecretVersion left empty)
            ↓
    pkg/controller/configchange.ConfigChangeHandler  (validation orchestrator)
```

Event-driven with no direct Kubernetes or watcher dependencies.

## Component Lifecycle

```go
func main() {
    loader := configloader.NewConfigLoaderComponent(bus, logger)
    go loader.Start(ctx)

    // Component runs until context cancelled
    // Processes ConfigResourceChangedEvent → ConfigParsedEvent
}
```

## Usage Patterns

### Basic Integration

```go
// Create component
loader := configloader.NewConfigLoaderComponent(bus, logger)

// Start in goroutine
go loader.Start(ctx)

// Component subscribes to ConfigResourceChangedEvent
// Publishes ConfigParsedEvent when valid config found
```

### Event Flow

```go
// 1. CRD SingleWatcher publishes ConfigResourceChangedEvent
bus.Publish(events.NewConfigResourceChangedEvent(crdResource))  // *unstructured.Unstructured

// 2. ConfigLoader processes event
// - Type-asserts to *unstructured.Unstructured and validates the GVK
// - Runs conversion.ParseCRD
// - Publishes ConfigParsedEvent (Version from resourceVersion, SecretVersion empty)

// 3. ConfigChangeHandler receives ConfigParsedEvent and runs scatter-gather validation.
```

## Common Pitfalls

### Invalid CRD Spec

**Problem**: HAProxyTemplateConfig CRD has invalid spec format.

**Solution**: Component logs error but doesn't publish event. Verify CRD spec against schema and fix validation errors.

### Resource Type Mismatch

**Problem**: ConfigResourceChangedEvent contains non-HAProxyTemplateConfig resource.

**Solution**: This should not happen if watcher is configured correctly. Check watcher configuration.

## Integration with Controller

Controller creates and starts component in Stage 1:

The real wiring lives in `pkg/controller/controller.go` (`setupComponents`); the loader is one of the components constructed during the Stage 1 setup (alongside `configchange.NewConfigChangeHandler`, `credentialsloader.NewCredentialsLoaderComponent`, and the validators) before `bus.Start()` is called.

```go
// Sketch — see pkg/controller/controller.go (setupComponents) for actual sequencing.
configLoader := configloader.NewConfigLoaderComponent(bus, logger)
// ... other Stage 1 components also constructed here, all subscribing during construction ...
bus.Start()
go configLoader.Start(ctx)
```

## Resources

- Configuration schema and parsing: `pkg/core/CLAUDE.md` (the `config/` subpackage doesn't have its own CLAUDE.md; `pkg/core/config/README.md` covers the public API)
- Event types: `pkg/controller/events/CLAUDE.md`
- Controller lifecycle: `pkg/controller/CLAUDE.md`
