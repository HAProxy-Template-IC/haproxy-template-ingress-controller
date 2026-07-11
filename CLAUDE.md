# HAProxy Template Ingress Controller - Development Context

This file contains cross-cutting development context for working on this codebase. For package-specific context, see CLAUDE.md files in relevant subdirectories.

## Project Overview

Event-driven Kubernetes operator that manages HAProxy configurations through template-driven approaches. Uses pure components wrapped in event adapters for clean separation of concerns.

Architecture documentation: `docs/site/docs/development/design.md`

## Resource-Agnostic Design (RULE #1)

**The Go code must be agnostic to every Kubernetes resource an operator may want to watch.** This is HAPTIC's reason for existing as a separate project; it must hold across every design decision.

**The litmus test.** If an operator switched their setup from Gateway/Ingress to a custom CRD, they should only need to touch HAPTIC templates and config — **no Go code**. Writing templates for the operator's CRD must be just as comfortable as writing them for Ingress or Gateway API resources. **There must be no preferential treatment for well-known resources.**

**What this rules out in Go:**

- Pre-generated wrappers, helpers, or filters bound to specific kinds (`Service`, `Ingress`, `Gateway`, `HTTPRoute`, `BackendTLSPolicy`, etc.).
- Function signatures that *encode* a resource path even when the body is technically generic (e.g. `listenerTransitionTime(gateway, listenerName, status)` is wrong — `gateway.status.listeners[byName].conditions` is baked into the parameters).
- "Chart-render-time runtime context" types whose field names embed specific resource knowledge — e.g. `GlobalFeatures.TLSCertificates`, `SSLPassthroughBackend`, `GatewayListenerMTLSConfig`. These look generic at a glance but only make sense for charts that watch Secrets and Gateways. A different operator's chart wouldn't use them.
- Anything that requires controller-side build-time knowledge of the resource set. Schemas arrive from the kube-apiserver (live) or `--schema-dir` (offline) at runtime.

**What stays acceptable:**

- Generic engine utilities operating on the dig/typed-struct surface: `dig`, `dig_string`, `fallback`, `toSlice`, `to_str_map`, `tostring`, etc.
- Typed access to *any* watched resource via `pkg/k8s/typegen`, because typegen consumes the schema at runtime; it knows nothing about which resources will be there until the controller starts.
- Resource shape stored as `map[string]any` plus the cost of `.(map[string]any)` casts in chart code — this is the price of generality, accept it rather than carving Go-side exceptions for the bundled chart's specific resources.

**Scope — what RULE #1 does NOT govern (the operational-identity exception).** The rule is about the resources an operator *watches as template inputs* — the routing/application objects whose shape the chart consumes (Ingress, Gateway, HTTPRoute, a custom CRD, …). It does **not** govern the controller's own operational identity: the fixed inputs HAPTIC needs to bootstrap and run *itself*. Concrete, kind-specific Go for these is correct — **not** a violation, and must not be "fixed" into config-derived indirection:

- **HAPTIC's credentials source.** The Dataplane API credentials Secret — its hardcoded GVR, a `SecretResourceChangedEvent`, parsing its `.data` — is the controller's own config input, not something the chart templates over. Hardcoding "Secret" here is fine.
- **The HAProxy fleet HAPTIC manages.** The auto-injected `haproxy-pods` self-watch — its store key, the store separation, discovery filtering — is the controller observing the pods it deploys to. Operational plumbing, not a user-watched routing resource.
- **HAPTIC's own CRDs.** The `HAProxyTemplateConfig` input CRD and the output CRDs (`HAProxyCfg`, `HAProxyMapFile`, …) are HAPTIC's own API surface; typed clients and kind-specific code for them are expected.

The test for the exception: *could an operator plausibly swap this resource out as part of describing their routing?* If yes (a routing/application object the templates consume), it must stay generic. If it's a fixed input HAPTIC needs to function at all (its credentials, the fleet it manages, its own CRDs), concrete Go is allowed — generalising it only adds config knobs and indirection for zero real portability gain. Do not file these as RULE #1 violations.

**Sweep rule.** If you touch one resource-coupled helper anywhere in `pkg/`, sweep ALL helpers in that package and remove every other one too. The rule is per-package, not per-helper. See `pkg/templating/CLAUDE.md` for the in-engine version.

**Corollary on the chart side.** Resource-specific behaviour lives in resource-specific libraries (`ingress.yaml`, `gateway/*.yaml`, `haproxytech.yaml`, etc.), never in `base.yaml`. Vendor annotation libraries (`haproxy-ingress/`, `nginx-ingress/`, etc.) follow the same pattern. See `charts/CLAUDE.md` for the chart-side version.

## Coding Standards

### Go Idioms

- Follow standard Go conventions (effective Go, Go proverbs)
- Use `gofmt` and `goimports` for formatting
- Run linters before commits: `make lint`
- Table-driven tests for multiple scenarios
- Early returns for error cases
- **NEVER add lint rules to the global ignore list** in `.golangci.yml` `excludes` - Use localized per-file exclusions in the `exclusions.rules` section instead
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
glab api --method GET "projects/haproxy-haptic%2Fhaptic/jobs/<JOB_ID>/artifacts" > artifacts.zip
unzip artifacts.zip

# View job trace/log
glab api --method GET "projects/haproxy-haptic%2Fhaptic/jobs/<JOB_ID>/trace"

# Get job details (status, artifacts info, ref, etc)
glab api projects/haproxy-haptic%2Fhaptic/jobs/<JOB_ID>
```

## Event-Driven Architecture Principles

### Pure Components Pattern

Business logic should be pure (no event dependencies):

```go
// pkg/templating/engine_interface.go - Pure component
type Engine interface {
    Render(ctx context.Context, templateName string, templateContext map[string]any) (string, error)
}
```

### Event Adapter Pattern

Only controller package contains event adapters:

```go
// Illustrative event-adapter shape — not a real package.
// The production renderer is the synchronous `renderer.RenderService`
// driven by `pkg/controller/pipeline.Pipeline` (no event hop, see ADR-0001).
// For the production event-adapter scaffold, see `pkg/controller/component.Base`.
type RendererComponent struct {
    engine    templating.Engine     // Pure component
    eventBus  *events.EventBus
    eventChan <-chan events.Event   // Subscribed in constructor
}

func NewRendererComponent(bus *events.EventBus, engine templating.Engine) *RendererComponent {
    return &RendererComponent{
        engine:    engine,
        eventBus:  bus,
        eventChan: bus.Subscribe("renderer", 100),  // Subscribe in constructor, before Start()
    }
}

// Method name is Start (not Run) — matches the lifecycle.Component contract.
func (r *RendererComponent) Start(ctx context.Context) error {
    for {
        select {
        case event := <-r.eventChan:
            if req, ok := event.(ReconciliationTriggeredEvent); ok {
                // Call pure component
                output, err := r.engine.Render(ctx, "haproxy.cfg", req.Context)

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
- Examples: EventBus, StoreProvider, Metrics, RestMapper
- Provide services used by multiple components
- No domain-specific business logic
- Can be injected and called directly without events

#### Examples of Direct Utility Calls

```go
// Good - direct utility component calls (illustrative pseudocode)
func (c *SomeComponent) handleAdmission(resourceType string, obj runtime.Object) {
    // StoreProvider is a utility component - building a dry-run overlay is a direct call
    overlay := stores.NewStoreOverlayForCreate(obj)
    provider := stores.NewOverlayStoreProvider(c.baseProvider,
        stores.NewValidationContext(map[string]*stores.StoreOverlay{resourceType: overlay}))

    // Metrics is a utility component - direct call
    if c.metrics != nil {
        c.metrics.RecordValidation(true)
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
    │   └─ Examples: EventBus.Publish(), StoreProvider.GetStore(), Metrics.Record()
    │
    └─ MAYBE → Review with team
        └─ Ask: "Could this become reusable business logic?"
```

#### Utility Components Registry

Current utility components that can be called directly:

- **EventBus** (`pkg/events`): Event infrastructure
- **StoreProvider** (`pkg/stores`): Resource store provider + dry-run overlay (`OverlayStoreProvider`, `StoreOverlay`); the watcher-side store lives in `pkg/k8s/store`
- **Metrics** (`pkg/controller/metrics`): Prometheus metrics recording
- **RestMapper** (`k8s.io/apimachinery/pkg/api/meta`): Kubernetes API mapping
- **Logger** (`log/slog`): Structured logging

When adding new components, explicitly document if they are "pure" or "utility" in their CLAUDE.md file.

## Import Path Conventions

### Internal Organization

```go
// Core packages (minimal dependencies)
import "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
import "gitlab.com/haproxy-haptic/haptic/pkg/core/logging"

// Infrastructure (no domain knowledge)
import "gitlab.com/haproxy-haptic/haptic/pkg/events"

// Domain packages (depends on core + infrastructure)
import "gitlab.com/haproxy-haptic/haptic/pkg/templating"
import "gitlab.com/haproxy-haptic/haptic/pkg/k8s"
import "gitlab.com/haproxy-haptic/haptic/pkg/dataplane"

// Coordination (depends on everything)
import "gitlab.com/haproxy-haptic/haptic/pkg/controller"
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"  // Event type catalog
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
func TestEngine_Render(t *testing.T) {
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
            engine, err := templating.New(
                map[string]string{"test": tt.template}, nil)
            require.NoError(t, err)

            got, err := engine.Render(context.Background(), "test", tt.context)
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

Test event adapters with mock EventBus. The example below uses the
hypothetical `RendererComponent` from the Event Adapter Pattern section
above; the real renderer is a synchronous `RenderService` (ADR-0001) and
isn't tested via this shape. For an actual event-adapter test, see
`pkg/controller/configloader/loader_test.go`.

```go
func TestRendererComponent(t *testing.T) {
    bus := events.NewEventBus(100)
    engine, _ := templating.New(templates, nil)
    renderer := NewRendererComponent(bus, engine)

    // Subscribe to output events
    eventChan := bus.Subscribe("test", 10)
    bus.Start()

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    // Start component (method is Start, matching the lifecycle.Component contract)
    go renderer.Start(ctx)

    // NOTE: RendererComponent / RenderCompletedEvent / RenderFailedEvent are
    // illustrative placeholders. In production, rendering is synchronous
    // (renderer.RenderService, ADR-0001) and its real result event is
    // *events.TemplateRenderedEvent — there is no RenderCompletedEvent type.
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

**IMPORTANT**: Always use the `kind-haptic-dev` context for development work.

```bash
# Verify you're using the correct cluster
kubectl config current-context
# Should output: kind-haptic-dev

# If not, switch to it
kubectl config use-context kind-haptic-dev

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

- **Dev cluster**: `kind-haptic-dev` - Use this for development
- **Test cluster**: `kind-haproxy-test` - Used by integration tests only

### Verifying Dev Environment Code

The dev environment uses source file hashing to verify the running code matches local files:

```bash
# Check if dev environment is running current code
./scripts/start-dev-env.sh status

# Output shows IN SYNC or OUT OF SYNC
# Source Code Sync:
#   Local source hash:   a1b2c3d4e5f6
#   Running source hash: a1b2c3d4e5f6
# ✔ IN SYNC - dev environment is running current code
```

The source hash is calculated from all `.go` files in `pkg/` and `cmd/`. It changes whenever any source file is modified (committed or not).

**Always run `status` before debugging** to confirm you're testing the right code. If OUT OF SYNC, run `./scripts/start-dev-env.sh restart`.

### HAProxy Version Management

**Build-time** (what CI builds):

- `versions.env` defines supported versions (`HAPROXY_VERSIONS="3.0 3.1 3.2 3.3 3.4"`)
- `Dockerfile` uses `DEFAULT_HAPROXY` for local builds
- CI builds controller images for each version: `0.1.0-haproxy3.0`, `0.1.0-haproxy3.1`, `0.1.0-haproxy3.2`, `0.1.0-haproxy3.3`, `0.1.0-haproxy3.4`

**Deploy-time** (what the chart uses):

- `haproxyVersion` in `values.yaml` selects the version (must be one CI builds)
- This single value drives both:
    - Controller image tag: `registry.gitlab.com/.../haptic:0.1.0-haproxy3.4`
    - HAProxy image tag: `haproxytech/haproxy-debian:3.4`
- This guarantees the controller and HAProxy pod always use matching versions

When updating default versions, update both:

1. `versions.env` / `Dockerfile` - for build defaults
2. `charts/haptic/values.yaml` - for deploy defaults

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
            return comp.Start(gCtx)
        })
    }

    // Wait for shutdown signal or error
    <-ctx.Done()
    slog.Info("Shutdown signal received, stopping components...")

    // Wait for components to finish (with timeout)
    done := make(chan error)
    go func() {
        done <- g.Wait()
    }()

    select {
    case err := <-done:
        if err != nil {
            slog.Error("Component error during shutdown", "error", err)
        }
    case <-time.After(30 * time.Second):
        slog.Error("Shutdown timeout exceeded")
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
    // Subscribe BEFORE returning the component.
    // Subscribe(name, bufferSize) — name is required for drop accounting.
    eventChan := eventBus.Subscribe(ComponentName, EventBufferSize)

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
    eventChan := c.eventBus.Subscribe(ComponentName, EventBufferSize)  // TOO LATE!
    // May miss events published before this subscription
}
```

### Context

- **Always respect context cancellation** - Check `ctx.Done()` in loops
- **Don't ignore context timeout errors** - They indicate system overload
- **Pass context through the call chain** - Don't create new contexts except for timeouts

### Testing

- **Don't use real Kubernetes API in unit tests** - Use fake clients from `k8s.io/client-go/kubernetes/fake`
- **Never shell out to external binaries in unit tests** - Mock through a seam instead; for haproxy use `pkg/dataplane/dataplanetest.InstallFakeHAProxy` in the package's `TestMain` (see `pkg/dataplane/CLAUDE.md`). Real-binary verdicts belong in integration tests
- **Tests must only listen on loopback** - Bind `127.0.0.1`/`localhost:0`, never `:port`/`0.0.0.0` (the `httptest` package does this right by default). All-interface binds trigger a Windows Firewall whitelist prompt for every freshly built test binary
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

- **FIX ALL test failures, lint errors, and runtime warnings/errors immediately**
    - Do NOT speculate about whether your changes caused the issue
    - Do NOT dismiss issues as "pre-existing" or "unrelated"
    - If tests fail or logs show warnings/errors after your changes, fix them
    - The dev environment must work correctly with no errors before task completion
    - Run the full test suite and verify dev environment logs are clean

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

- Architecture: `docs/site/docs/development/design.md`
- Package READMEs: `pkg/*/README.md`
- Linting guidelines: `docs/site/docs/development/linting.md`
- Configuration reference: `docs/site/docs/supported-configuration.md`

## User Documentation Authoring

Applies to **all** user-facing docs — the merged docs site (`docs/site/docs/`,
served at `/docs/`, covering both the controller and the Helm chart) and the
landing page (`docs/landing/`). The reader is often new to HAPTIC and takes every
sentence literally.

- **Examine prerequisites vs. procedure for contradictions.** Cross-check every
  "Before you start" / "Prerequisites" item against the steps that follow. If a
  step performs it, it's not a prerequisite (e.g. don't list "HAPTIC is installed"
  when step 1 installs it).
- **Don't state a restriction unless it's actually true.** Prohibitions ("don't
  install it beforehand", "must be X", "only works with Y") must be technically
  necessary, not just the path you happened to describe. Ask *what actually breaks
  if the reader does the opposite?* If nothing breaks, describe the alternative
  path instead of forbidding it.
- **Everything must be copy-pastable.** Never tell the reader to hand-edit a
  command ("swap `install` for `upgrade`", "drop the `--create-namespace` flag").
  Give the complete runnable command for each case, even if two commands overlap.
- **Don't assume the reader's state.** They may have already created Ingresses
  with HAPTIC's class, installed HAPTIC, or set custom values. Don't write
  sentences that only hold under an assumed state; branch explicitly ("if X, do A;
  otherwise B") instead.
- **Never add sentences that carry no value.** Every sentence must instruct, warn,
  or inform. Cut reassurances and filler ("anyway", "as you'd expect", "simply") —
  they don't help and can confuse readers who read them as load-bearing.

### Distilled style rules (Google/Microsoft/Diátaxis)

- **Second person, active voice, present tense.** "You configure the class" /
  "The controller writes status" — not "the class is configured" or "will write".
- **Don't mix doc types on one page** (Diátaxis). A how-to gives steps; a
  reference lists facts; an explanation gives background. Keep them separate — a
  tutorial that also explains theory serves no one.
- **One action per step.** Numbered steps are imperative and single-purpose
  ("Run X.", "Set Y."). Put results/notes in a separate sentence, not the step.
- **Front-load.** Lead each section/paragraph with what it's about; put the most
  important information first. Descriptive headings, sentence case.
- **Descriptive link text** — link the thing ("[Getting Started](...)"), never
  "click [here]" or a bare URL.
- **Define an acronym on first use**, then reuse it. Don't assume jargon.
- **Contractions are fine and preferred** (`isn't`, `don't`) for a warm, clear
  tone — but stay consistent (don't mix `isn't` and "is not" for the same kind of
  statement). Prefer negative contractions so readers don't miss a "not".
- **Write for a global audience.** Short sentences, common words, no idioms/slang;
  the serial (Oxford) comma; spell out ambiguous dates (2026-07-01, not 07/01).
- **Show, then tell.** Prefer a runnable example over a paragraph describing it.

### Interactive playground examples

The docs embed a live playground (`docs/shared/playground-embed.js`, the `.pg-embed`
component). When you author or edit a runnable example:

- **Default to typed resource access, not `dig()`.** Every non-scriggo embed loads
  the schema bundle, so a real cluster's typed access works: write
  `svc.metadata.name`, `ingress.spec.rules`, `path.backend.service.port.number` —
  not `dig(svc, "metadata", "name")`. Typed access is the idiom the controller
  gives you against a live apiserver, so it's what examples should teach. Reserve
  `dig()` for the three cases where it's genuinely right: the `dig`/`fallback`
  lesson itself, inline `map[string]any` literals (no watched resource), and a
  schema-less custom resource with no bundled schema. (`dig()` is *required* in the
  chart libraries — that's RULE #1, resource-agnostic code — but that's the chart,
  not user templates. Don't let chart `dig()` usage set the default for examples.)
  Typed gotchas: range optional typed slices **directly** (`range ingress.spec.rules`)
  — wrapping them in `fallback(x, []any{})` boxes elements to `interface{}` and
  breaks the following typed field access; a nil typed slice ranges zero times.
  Check presence with `len(x.paths) > 0` (a value-struct field can't compare to nil).
  Show a `map` with `... | toJSON()`.
- **Use the minimal `data-scriggo` embed for template-language demos.** When the
  point is a helper function, a control-flow construct, or whitespace control —
  anything that doesn't need watched resources — use `<div class="pg-embed"
  data-scriggo>` with a bare template. It skips the whole `HAProxyTemplateConfig`
  wrapper, so the reader sees only the template and its output. Reserve full-config
  embeds (with `watchedResources`/`haproxyConfig`) for examples that genuinely
  render resources into config.
- **Teach before you test.** Put a challenge (`data-difficulty`, a `pg-solution`)
  *after* the concept it exercises, never before — a reader can't solve a task for
  something the page hasn't shown yet. Every task/challenge intro is one
  `<p class="pg-task">` callout (the component styles it); don't leave it as plain
  prose.

Full guides: [Google](https://developers.google.com/style),
[Microsoft](https://learn.microsoft.com/en-us/style-guide/welcome/),
[Diátaxis](https://diataxis.fr/).

## Changelog Guidelines

There is ONE changelog: the root `CHANGELOG.md`. It documents user-facing changes to the controller and the Helm chart. Chart changes (values, templates, chart defaults, values migrations) go under a `### Helm chart` subsection of each release (with `#### Added`/`#### Changed`/… sub-headings); controller changes go in the release's top-level `### Added`/`### Changed`/… sections. `charts/haptic/CHANGELOG.md` is only a pointer to the root changelog.

**Every notable change must have a changelog entry.** Keep entries concise - one line per change, focus on what changed, not implementation details. Avoid verbose justifications or explanations in parentheses.

**Include:**

- New features and capabilities (what the controller can do)
- User-facing commands and APIs
- Behavior changes and bug fixes
- Metrics and observability features
- Security-related changes
- Helm chart values, defaults, and template library changes (in the `### Helm chart` subsection)

**Exclude (not notable):**

- Test additions or fixes
- Lint fixes
- CI/CD pipeline fixes
- Development scripts and tooling
- Internal testing infrastructure

**Don't call changes "BREAKING" when the feature being broken was itself introduced after the last release.** The CHANGELOG is read by operators upgrading between released versions; if the affected behavior never shipped to a real release, the only people impacted are snapshot/main consumers — note the change but don't tag it as a `BREAKING` migration. Check `git tag -l | sort -V | tail` for the latest released version and `git log <last-tag>..HEAD -- <files>` to see what's actually post-release.

## Release Process

The controller and the Helm chart are released together: one version, one `v<version>` tag, one changelog. `VERSION` is the single source of truth; `charts/haptic/Chart.yaml` `version` and `appVersion` must equal it (CI refuses to tag otherwise).

To prepare a release:

1. **Curate `CHANGELOG.md` [Unreleased]** - Rewrite the accumulated entries into user-facing release notes (merge related entries, drop items that never shipped in a release); also update the hand-curated `artifacthub.io/changes` annotation in `charts/haptic/Chart.yaml`
2. **Run `./scripts/release.sh <version>`** - Promotes `[Unreleased]` to the version section and updates every version-bearing file (VERSION, Chart.yaml, install examples) in one commit; the docs-site changelog page is generated from `CHANGELOG.md` at mkdocs build time and needs no release-time sync
3. **Merge the release MR to main** - CI creates the `v<version>` tag after the full post-merge pipeline passes; the tag pipeline publishes binaries, images, the chart, and versioned docs

See `docs/site/docs/development/releasing.md` for the full process.

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

## Agent skills

### Issue tracker

Issues live as GitLab issues on `gitlab.com:haproxy-haptic/haptic`. Use the `glab` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical triage roles use their default label strings (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context layout: one `CONTEXT.md` and `docs/adr/` at the repo root, created lazily by `/grill-with-docs`. See `docs/agents/domain.md`.
