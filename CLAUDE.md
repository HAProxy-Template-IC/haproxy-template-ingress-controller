# HAProxy Template Ingress Controller - Development Context

This file contains cross-cutting development context for working on this codebase. For package-specific context, see CLAUDE.md files in relevant subdirectories.

## Project Overview

Event-driven Kubernetes operator that manages HAProxy configurations through template-driven approaches. Uses pure components wrapped in event adapters for clean separation of concerns.

Architecture documentation: `docs/development/design.md`

## Coding Standards

### Go Idioms

- Follow standard Go conventions (effective Go, Go proverbs)
- Use `gofmt` and `goimports` for formatting
- Run linters before commits: `make lint`
- Table-driven tests for multiple scenarios
- Early returns for error cases
- **NEVER use //nolint directives** - Fix linting issues properly by refactoring code, not by suppressing warnings

### Error Handling

```go
// Wrap errors with context
if err != nil {
    return fmt.Errorf("failed to parse config: %w", err)
}

// Custom error types for different failure modes
type ValidationError struct {
    Field   string
    Message string
    Err     error
}

func (e *ValidationError) Error() string {
    return fmt.Sprintf("validation failed on %s: %s", e.Field, e.Message)
}

func (e *ValidationError) Unwrap() error {
    return e.Err
}
```

### Context Propagation

Always propagate context through the call chain:

```go
func ProcessResource(ctx context.Context, resource Resource) error {
    // Pass context to all calls
    result, err := fetchData(ctx, resource.ID)
    if err != nil {
        return err
    }

    // Use context for timeouts
    ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()

    return deploy(ctx, result)
}
```

## Pre-commit and CI Requirements

**CRITICAL**: All code must pass linting and security checks before committing.

### Pre-commit Hooks

Pre-commit hooks run automatically via git hooks and enforce:
- **golangci-lint**: Code quality, style, and common mistakes
- **govulncheck**: Security vulnerability scanning

### Commit Requirements

- **NEVER use `git commit --no-verify`** to bypass pre-commit hooks
- **Fix all linting issues** reported by `make lint` before committing
- **Address security vulnerabilities** reported by `govulncheck`
- **Run `gofmt` and `goimports`** to fix formatting issues
- **Refactor code** to resolve complexity and maintainability issues

### Why This Matters

**CI pipeline will fail** if code contains:
- Linting violations (golangci-lint errors)
- Security vulnerabilities (govulncheck findings)
- Formatting issues (gofmt/goimports)
- Test failures

Bypassing pre-commit hooks with `--no-verify` only delays the problem until CI runs, wasting time and blocking MR merges.

### Fixing Common Issues

```bash
# Run linting and see all issues
make lint

# Fix formatting automatically
gofmt -w .
goimports -w .

# Check for security vulnerabilities
govulncheck ./...

# Run all checks locally before committing
make check-all
```

**If extensive refactoring is needed**: Create separate commits/MRs for linting fixes rather than bypassing checks.

### GitLab CLI (glab) Commands

Common `glab` commands for development workflow:

```bash
# Create merge request
glab mr create --title "Fix template rendering" --description "Fixes issue with..."

# View MR details
glab mr view 123

# List open MRs
glab mr list

# Check CI pipeline status
glab ci view

# View failed job logs
glab ci view --job-id 12345

# Create issue
glab issue create --title "Bug: ..." --description "..."

# View CI configuration
glab ci lint .gitlab-ci.yml

# Download job artifacts for debugging CI failures
# Replace <JOB_ID> with the numeric job ID from the GitLab job URL
glab api --method GET "projects/haproxy-template-ic%2Fhaproxy-template-ingress-controller/jobs/<JOB_ID>/artifacts" > artifacts.zip
unzip artifacts.zip

# View job trace/log
glab api --method GET "projects/haproxy-template-ic%2Fhaproxy-template-ingress-controller/jobs/<JOB_ID>/trace"

# Get job details (status, artifacts info, ref, etc)
glab api projects/haproxy-template-ic%2Fhaproxy-template-ingress-controller/jobs/<JOB_ID>
```

## Event-Driven Architecture Principles

### Pure Components Pattern

Business logic should be pure (no event dependencies):

```go
// pkg/templating/engine.go - Pure component
type TemplateEngine struct {
    // No EventBus dependency
}

func (e *TemplateEngine) Render(templateName string, context map[string]interface{}) (string, error) {
    // Pure business logic
    return e.render(templateName, context)
}
```

### Event Adapter Pattern

Only controller package contains event adapters:

```go
// pkg/controller/renderer/renderer.go - Event adapter
type RendererComponent struct {
    engine   *templating.TemplateEngine  // Pure component
    eventBus *events.EventBus
}

func (r *RendererComponent) Run(ctx context.Context) error {
    eventChan := r.eventBus.Subscribe(100)

    for {
        select {
        case event := <-eventChan:
            if req, ok := event.(ReconciliationTriggeredEvent); ok {
                // Call pure component
                output, err := r.engine.Render("haproxy.cfg", req.Context)

                // Publish result event
                if err != nil {
                    r.eventBus.Publish(RenderFailedEvent{Error: err.Error()})
                } else {
                    r.eventBus.Publish(RenderCompletedEvent{Output: output})
                }
            }
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}
```

### When to Use Events vs Direct Calls

**Use EventBus (async pub/sub):**
- Component coordination across packages
- Observability and logging
- Extensibility (new features can subscribe to existing events)
- Fire-and-forget notifications

**Use Request() (scatter-gather):**
- Configuration validation (multiple validators must respond)
- Distributed queries
- Coordinated operations requiring multiple confirmations

**Use direct function calls:**
- Within the same package
- Pure components calling other pure components
- Utility components (see below)
- No need for decoupling or observability

### Utility Components vs Pure Components

Not all dependencies require event-driven coordination. The codebase distinguishes between:

**Pure Components** (require event adapters):
- Contain domain business logic
- Examples: `pkg/templating`, `pkg/k8s`, `pkg/dataplane`
- Should be wrapped in event adapters when used in `pkg/controller`
- Changes to these affect reconciliation logic

**Utility Components** (can be called directly):
- Infrastructure/cross-cutting concerns
- Examples: EventBus, StoreManager, Metrics, RestMapper
- Provide services used by multiple components
- No domain-specific business logic
- Can be injected and called directly without events

#### Examples of Direct Utility Calls

```go
// Good - direct utility component calls
func (c *DryRunValidator) handleValidationRequest(req *events.WebhookValidationRequest) {
    // StoreManager is a utility component - direct call is acceptable
    overlayStores, err := c.storeManager.CreateOverlayMap(resourceType, req.Namespace, req.Name, req.Object, operation)

    // Metrics is a utility component - direct call
    if c.metrics != nil {
        c.metrics.RecordValidation(result)
    }

    // RestMapper is a utility component - direct call
    gvk, err := c.restMapper.KindFor(gvr)
}

// Bad - calling pure component without event adapter
func (c *SomeComponent) Run(ctx context.Context) error {
    // This should go through an event adapter, not called directly
    config, err := c.templateEngine.Render("haproxy.cfg", context)
    return err
}
```

#### Decision Tree: Events vs Direct Calls

```
Does the call involve domain business logic?
├─ YES → Use event-driven pattern
│   └─ Examples: template rendering, config validation, HAProxy sync
│
└─ NO → Is it infrastructure/utility?
    ├─ YES → Direct call is acceptable
    │   └─ Examples: EventBus.Publish(), StoreManager.Get(), Metrics.Record()
    │
    └─ MAYBE → Review with team
        └─ Ask: "Could this become reusable business logic?"
```

#### Utility Components Registry

Current utility components that can be called directly:

- **EventBus** (`pkg/events`): Event infrastructure
- **StoreManager** (`pkg/controller/resourcestore`): Resource storage utilities
- **Metrics** (`pkg/controller/metrics`): Prometheus metrics recording
- **RestMapper** (`k8s.io/apimachinery/pkg/api/meta`): Kubernetes API mapping
- **Logger** (`log/slog`): Structured logging

When adding new components, explicitly document if they are "pure" or "utility" in their CLAUDE.md file.

## Import Path Conventions

### Internal Organization

```go
// Core packages (minimal dependencies)
import "haproxy-template-ic/pkg/core/config"
import "haproxy-template-ic/pkg/core/logging"

// Infrastructure (no domain knowledge)
import "haproxy-template-ic/pkg/events"

// Domain packages (depends on core + infrastructure)
import "haproxy-template-ic/pkg/templating"
import "haproxy-template-ic/pkg/k8s"
import "haproxy-template-ic/pkg/dataplane"

// Coordination (depends on everything)
import "haproxy-template-ic/pkg/controller"
import "haproxy-template-ic/pkg/controller/events"  // Event type catalog
```

### Dependency Rules

- `pkg/events` should have no dependencies on other pkg/ packages
- `pkg/templating`, `pkg/k8s`, `pkg/dataplane` should be pure libraries (no cross-dependencies)
- `pkg/controller` can import everything (coordination layer)
- `pkg/core` provides shared primitives (config types, logging setup)
- Domain-specific event types go in `pkg/controller/events`, not `pkg/events`

## Testing Strategy

### Unit Tests

Test pure components without event infrastructure:

```go
func TestTemplateEngine_Render(t *testing.T) {
    tests := []struct {
        name     string
        template string
        context  map[string]interface{}
        want     string
        wantErr  bool
    }{
        {
            name:     "simple variable",
            template: "Hello {{ name }}",
            context:  map[string]interface{}{"name": "World"},
            want:     "Hello World",
        },
        // More test cases...
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            engine, err := templating.New(templating.EngineTypeGonja,
                map[string]string{"test": tt.template})
            require.NoError(t, err)

            got, err := engine.Render("test", tt.context)
            if tt.wantErr {
                require.Error(t, err)
                return
            }

            require.NoError(t, err)
            assert.Equal(t, tt.want, got)
        })
    }
}
```

### Integration Tests

Located in `tests/` directory. Require kind cluster:

```bash
# Run integration tests
make test-integration

# Run specific integration test
KEEP_CLUSTER=true go test ./tests/... -run TestSyncFrontendAdd -v
```

Tests use real Kubernetes clusters (kind) and HAProxy pods. The `KEEP_CLUSTER=true` environment variable prevents cluster cleanup for debugging.

### Event-Driven Component Tests

Test event adapters with mock EventBus:

```go
func TestRendererComponent(t *testing.T) {
    bus := events.NewEventBus(100)
    engine, _ := templating.New(templating.EngineTypeGonja, templates)
    renderer := NewRendererComponent(bus, engine)

    // Subscribe to output events
    eventChan := bus.Subscribe(10)
    bus.Start()

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    // Start component
    go renderer.Run(ctx)

    // Trigger reconciliation
    bus.Publish(ReconciliationTriggeredEvent{Context: testContext})

    // Verify output event
    select {
    case event := <-eventChan:
        if completed, ok := event.(RenderCompletedEvent); ok {
            assert.Contains(t, completed.Output, "expected content")
        } else {
            t.Fatalf("expected RenderCompletedEvent, got %T", event)
        }
    case <-time.After(1 * time.Second):
        t.Fatal("timeout waiting for event")
    }
}
```

## Build Commands

```bash
# Build binary
make build

# Run all tests
make test

# Run linting (golangci-lint)
make lint

# Run all checks (tests + linting)
make check-all

# Integration tests (requires kind)
make test-integration

# Coverage report
make test-coverage

# Build Docker image
make docker-build

# Test template libraries (Helm charts)
# IMPORTANT: Use this script when testing template changes to ensure
# proper helm rendering. Do NOT test library files directly.
./scripts/test-templates.sh

# Test specific template validation test
./scripts/test-templates.sh --test test-httproute-method-matching

# Test with debug output
./scripts/test-templates.sh --test test-httproute-method-matching --dump-rendered --verbose
```

## Development Environment

### Local Kind Cluster

**IMPORTANT**: Always use the `kind-haproxy-template-ic-dev` context for development work.

```bash
# Verify you're using the correct cluster
kubectl config current-context
# Should output: kind-haproxy-template-ic-dev

# If not, switch to it
kubectl config use-context kind-haproxy-template-ic-dev

# Start the dev environment (creates cluster if needed)
./scripts/start-dev-env.sh

# Build and deploy changes to dev cluster
# IMPORTANT: Always use this script - do not run manual build commands
./scripts/start-dev-env.sh restart

# View controller logs
./scripts/start-dev-env.sh logs

# Check deployment status
./scripts/start-dev-env.sh status

# Test ingress functionality
./scripts/start-dev-env.sh test

# Check HAProxy configuration
kubectl -n echo get pods -l app=haproxy
kubectl -n echo exec <haproxy-pod> -- cat /etc/haproxy/haproxy.cfg

# Clean up dev environment
./scripts/start-dev-env.sh down
```

**Cluster Names:**
- **Dev cluster**: `kind-haproxy-template-ic-dev` - Use this for development
- **Test cluster**: `kind-haproxy-test` - Used by integration tests only

## Common Patterns

### Graceful Shutdown

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    // Create components
    components := []Component{
        watcher,
        reconciler,
        executor,
    }

    // Start components with errgroup
    g, gCtx := errgroup.WithContext(ctx)
    for _, comp := range components {
        comp := comp  // Capture loop variable
        g.Go(func() error {
            return comp.Run(gCtx)
        })
    }

    // Wait for shutdown signal or error
    <-ctx.Done()
    log.Info("Shutdown signal received, stopping components...")

    // Wait for components to finish (with timeout)
    done := make(chan error)
    go func() {
        done <- g.Wait()
    }()

    select {
    case err := <-done:
        if err != nil {
            log.Error("Component error during shutdown", "error", err)
        }
    case <-time.After(30 * time.Second):
        log.Error("Shutdown timeout exceeded")
    }
}
```

### Structured Logging

```go
import "log/slog"

// Create logger with structured fields
logger := slog.Default().With(
    "component", "reconciler",
    "namespace", resource.Namespace,
)

// Log with structured attributes
logger.Info("reconciliation started",
    "resource", resource.Name,
    "trigger", "config_change",
)

logger.Error("reconciliation failed",
    "error", err,
    "duration_ms", time.Since(start).Milliseconds(),
)
```

## Common Pitfalls

### Event Bus

- **Don't block in event handlers** - Process events quickly or spawn goroutines
- **Buffer sizing matters** - Small buffers (10-50) for control events, large buffers (200+) for high-volume events
- **Always call EventBus.Start()** after all components are created (prevents lost events during startup)
- **Subscribe in constructors, not in Start()** - ALL components must subscribe during construction (in `New()` functions) BEFORE `EventBus.Start()` is called. This ensures proper startup synchronization without race conditions or timing-based sleeps.
- **Never use timing-based solutions** - Do not use `time.Sleep()` or delays to fix event ordering issues. These are brittle, non-deterministic, and bad practice. Fix the root cause by ensuring proper subscription ordering.

### Proper Subscription Pattern

**CORRECT Pattern** (all components follow this):
```go
// Component constructor subscribes immediately
func New(eventBus *EventBus, ...) *Component {
    // Subscribe BEFORE returning the component
    eventChan := eventBus.Subscribe(EventBufferSize)

    return &Component{
        eventBus:  eventBus,
        eventChan: eventChan,  // Store channel for use in Start()
        // ...
    }
}

// Start() uses pre-subscribed channel
func (c *Component) Start(ctx context.Context) error {
    for {
        select {
        case event := <-c.eventChan:  // Use stored channel
            c.handleEvent(event)
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}

// Controller startup sequence
components := createAllComponents()  // All subscribe during construction
eventBus.Start()                      // Now safe to start bus
startAllComponents()                  // Start goroutines
```

**INCORRECT Pattern** (creates race conditions):
```go
// BAD - subscribes in Start()
func (c *Component) Start(ctx context.Context) error {
    eventChan := c.eventBus.Subscribe(EventBufferSize)  // TOO LATE!
    // May miss events published before this subscription
}
```

### Context

- **Always respect context cancellation** - Check `ctx.Done()` in loops
- **Don't ignore context timeout errors** - They indicate system overload
- **Pass context through the call chain** - Don't create new contexts except for timeouts

### Testing

- **Don't use real Kubernetes API in unit tests** - Use fake clients from `k8s.io/client-go/kubernetes/fake`
- **Integration tests are slow** - Keep them focused and minimal
- **Mock EventBus carefully** - Subscribe before publishing to avoid race conditions

### Kubernetes

- **Wait for initial sync** - Don't process resources before all informers sync
- **Handle resource versions** - They're not monotonic across resource types
- **Field selectors are limited** - Not all fields support field selectors (use label selectors instead)

### Development Practices

**CRITICAL - Task Completion Standards:**

- **NEVER mark a task as completed without verifying it actually works**
  - Run tests to confirm the implementation is correct
  - Test the actual behavior, not just that code compiles
  - Verify edge cases and error conditions

- **NEVER take shortcuts by skipping hard parts of implementation**
  - If a test fails, FIX the code to make it pass correctly
  - Do NOT change the test to match broken behavior
  - Do NOT add TODO comments for things that should be fixed immediately
  - If something is genuinely out of scope, discuss with the user first

- **ALWAYS inform the user about incomplete or buggy implementations**
  - If you discover a bug during implementation, fix it
  - If you can't complete a task, explain why and what remains
  - Do NOT hide problems behind TODO comments
  - Transparency builds trust

**Example of UNACCEPTABLE behavior:**
1. Test fails because routes are in wrong order
2. Change test to validate the wrong order
3. Add TODO comment saying "should fix this later"
4. Mark task as completed
5. Don't tell user about the bug

**Example of CORRECT behavior:**
1. Test fails because routes are in wrong order
2. Debug the sorting logic to find root cause
3. Fix the sorting implementation
4. Verify test passes with correct behavior
5. Mark task as completed

**Why this matters:** Taking shortcuts wastes time. The bug will eventually need to be fixed, and by then the context is lost, making it harder. Do it right the first time.

## Resources

- Architecture: `docs/development/design.md`
- Package READMEs: `pkg/*/README.md`
- Linting guidelines: `docs/development/linting.md`
- Configuration reference: `docs/supported-configuration.md`

## Package-Specific Context

For detailed development context on specific packages, see:
- `pkg/CLAUDE.md` - Package organization principles
- `pkg/events/CLAUDE.md` - Event bus infrastructure
- `pkg/controller/CLAUDE.md` - Controller orchestration
- `pkg/controller/leaderelection/CLAUDE.md` - Leader election event adapter
- `pkg/controller/metrics/CLAUDE.md` - Metrics collection
- `pkg/k8s/CLAUDE.md` - Kubernetes integration
- `pkg/k8s/leaderelection/CLAUDE.md` - Pure leader election component
- `pkg/dataplane/CLAUDE.md` - HAProxy integration
- `pkg/templating/CLAUDE.md` - Template engine
- `pkg/core/CLAUDE.md` - Core functionality
- `cmd/controller/CLAUDE.md` - Entry point and startup
