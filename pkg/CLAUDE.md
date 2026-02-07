# pkg/ - Package Organization

Development context for working with packages in this directory.

## Package Architecture

The codebase follows clean architecture with clear separation of concerns:

```
pkg/
├── core/              # Shared primitives (config, logging)
├── events/            # Generic event bus (domain-agnostic)
├── templating/        # Pure template engine library
├── k8s/               # Kubernetes integration library
├── dataplane/         # HAProxy integration library
└── controller/        # Orchestration and coordination
```

## Dependency Hierarchy

### Layer 1: Infrastructure (No Dependencies)

**pkg/events/**

- Generic pub/sub and request-response infrastructure
- NO business logic, NO domain knowledge
- Could be extracted as standalone library
- Imported by: everything else

### Layer 2: Pure Libraries (Minimal Dependencies)

**pkg/core/**

- Configuration types and parsing
- Logging setup
- Depends on: standard library only
- Imported by: most other packages

**pkg/templating/**

- Template compilation and rendering
- Depends on: Scriggo, standard library
- Imported by: controller package

**pkg/k8s/**

- Resource watching, indexing, storage
- Depends on: client-go, events (for coordination)
- Imported by: controller package

**pkg/dataplane/**

- HAProxy configuration sync
- Depends on: client-native, events (for observability)
- Imported by: controller package

### Layer 3: Coordination (Depends on Everything)

**pkg/controller/**

- Event-driven orchestration
- Component lifecycle management
- Event adapters wrapping pure components
- Depends on: all above packages
- Defines: domain-specific event types (in controller/events/)

## When to Create a New Package

### Create a new top-level package when

- **Reusable library**: Code could be used by multiple applications
- **Clear boundary**: Package has well-defined responsibility
- **Minimal dependencies**: Package has few dependencies on other packages
- **Pure logic**: Business logic without coordination concerns

**Example**: `pkg/templating` is a pure template engine that could be reused in other projects.

### Create a new sub-package when

- **Related functionality**: Code belongs to parent package's domain
- **Internal organization**: Breaking up a large package for readability
- **Implementation details**: Hide internal types from package users

**Example**: `pkg/dataplane/comparator/sections/` contains section-specific comparison logic.

### Extend existing package when

- **Same responsibility**: Feature fits existing package's purpose
- **Shared types**: Uses same core types and interfaces
- **No new dependencies**: Doesn't introduce new dependencies

## Package Design Patterns

### Pure Components

Packages like `templating`, `k8s`, `dataplane` provide pure business logic:

```go
// pkg/templating/engine.go
package templating

// No event dependencies - pure library
type Engine interface {
    Render(name string, ctx map[string]interface{}) (string, error)
}
```

### Event Adapters

Only `pkg/controller` contains event coordination:

```go
// pkg/controller/renderer/component.go
package renderer

import (
    "haptic/pkg/events"
    "haptic/pkg/templating"
)

// Event adapter wraps pure component
type Component struct {
    engine    templating.Engine    // Pure component
    eventBus  *events.EventBus    // Event coordination
    eventChan <-chan events.Event  // Subscribed in constructor
}

func New(bus *events.EventBus, engine templating.Engine) *Component {
    return &Component{
        engine:    engine,
        eventBus:  bus,
        eventChan: bus.Subscribe(100),  // Subscribe in constructor, before Start()
    }
}

func (c *Component) Run(ctx context.Context) error {
    for {
        select {
        case event := <-c.eventChan:
            // Convert event to pure function call
            switch e := event.(type) {
            case ReconciliationTriggeredEvent:
                output, err := c.engine.Render("haproxy.cfg", e.Context)
                // Publish result event
                if err != nil {
                    c.eventBus.Publish(RenderFailedEvent{Error: err})
                } else {
                    c.eventBus.Publish(RenderCompletedEvent{Output: output})
                }
            }
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}
```

## Interface Design

### Guidelines

1. **Keep interfaces small**: Single-method interfaces are idiomatic Go
2. **Define interfaces at use site**: Consumer defines the interface
3. **Accept interfaces, return structs**: Flexibility at boundaries
4. **Avoid interface pollution**: Not everything needs an interface

### Example Pattern

```go
// pkg/dataplane/client.go - Provide concrete type
package dataplane

type Client struct {
    // implementation
}

func (c *Client) GetVersion() (string, error) { ... }
func (c *Client) DeployConfig(cfg string) error { ... }

// pkg/controller/executor.go - Define interface at use site
package executor

// Only need subset of Client methods
type ConfigDeployer interface {
    DeployConfig(cfg string) error
}

type Executor struct {
    deployer ConfigDeployer  // Accepts any type implementing this
}
```

## Cross-Package Communication

### Direct Calls (Preferred within layers)

Use direct function calls for pure components:

```go
// pkg/controller/executor.go
import "haptic/pkg/templating"

func (e *Executor) render() (string, error) {
    // Direct call to pure component
    return e.templateEngine.Render("haproxy.cfg", e.context)
}
```

### Events (For cross-layer coordination)

Use events for decoupled coordination:

```go
// Resource watcher publishes event
watcher.eventBus.Publish(ResourceIndexUpdatedEvent{Type: "ingress"})

// Multiple subscribers can react
// - Reconciler triggers reconciliation
// - Commentator logs the change
// - Metrics collector updates counters
```

## Testing Strategies

### Unit Tests (Same Package)

Test pure components in isolation:

```go
// pkg/templating/engine_test.go
package templating

func TestEngine_Render(t *testing.T) {
    engine, _ := New(EngineTypeScriggo, map[string]string{
        "test": "Hello {{ name }}",
    }, nil, nil, nil)

    output, err := engine.Render("test", map[string]interface{}{
        "name": "World",
    })

    require.NoError(t, err)
    assert.Equal(t, "Hello World", output)
}
```

### Integration Tests (Cross-Package)

Test package interactions:

```go
// pkg/controller/executor_test.go
package executor

import (
    "haptic/pkg/events"
    "haptic/pkg/templating"
)

func TestExecutor_Integration(t *testing.T) {
    bus := events.NewEventBus(100)
    engine, _ := templating.New(...)
    exec := NewExecutor(bus, engine, ...)

    // Test cross-package interaction
    bus.Publish(ReconciliationTriggeredEvent{})
    // Verify expected behavior
}
```

## Common Pitfalls

### Circular Dependencies

**Problem**: Package A imports B, B imports A.

**Solution**: Extract shared types to new package or use interfaces.

```go
// Bad
pkg/dataplane → imports → pkg/controller
pkg/controller → imports → pkg/dataplane

// Good
pkg/dataplane → returns concrete types
pkg/controller → defines interfaces at use site
```

### Event Type Location

**Problem**: Putting domain events in `pkg/events`.

**Solution**: Domain events go in `pkg/controller/events`, only infrastructure in `pkg/events`.

```go
// Wrong location
pkg/events/types.go:
    type ReconciliationTriggeredEvent struct { ... }  // Domain event

// Correct location
pkg/controller/events/types.go:
    type ReconciliationTriggeredEvent struct { ... }  // Domain event

pkg/events/bus.go:
    type Event interface { ... }  // Infrastructure only
```

### Too Many Small Packages

**Problem**: Creating a package for every file.

**Solution**: Group related functionality. A package can have 5-10 files.

### Leaking Implementation Details

**Problem**: Exposing internal types in public API.

**Solution**: Use interfaces or copy data at package boundaries.

```go
// Bad - leaking internal type
func (c *Client) GetRawParser() *clientnative.Parser { ... }

// Good - return interface or copy
func (c *Client) ParseConfig(cfg string) (*ParsedConfig, error) { ... }
```

## Adding New Features

### Checklist

1. **Identify layer**: Infrastructure, library, or coordination?
2. **Check existing packages**: Does feature fit an existing package?
3. **Define interface**: What API should the feature expose?
4. **Write tests first**: Test-driven development
5. **Implement pure logic**: No event dependencies in libraries
6. **Add event adapter**: If needed, wrap in controller package
7. **Update README.md**: Document public API
8. **Update CLAUDE.md**: Add development context

### Example: Adding Custom Template Filters

```go
// Step 1: Add to pure library (pkg/templating)
func (e *ScriggoEngine) RegisterFilter(name string, fn FilterFunc) error {
    // Pure business logic
}

// Step 2: Use from controller (no event adapter needed, pure function call)
engine.RegisterFilter("b64decode", base64DecodeFilter)
```

## Resources

- Root-level architecture: `/CLAUDE.md`
- Package-specific context: `pkg/*/CLAUDE.md`
- Architecture documentation: `/docs/controller/docs/development/design.md`
- Package API documentation: `pkg/*/README.md`
