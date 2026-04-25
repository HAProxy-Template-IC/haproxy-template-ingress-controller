# cmd/controller - Main Entry Point

Development context for the controller application entry point.

**Architecture**: See `/docs/controller/docs/development/design.md` (Startup and Initialization section)

## When to Work Here

Modify this package when:

- Changing startup sequence
- Adding new command-line flags
- Modifying environment variable handling
- Changing signal handling
- Adding health/metrics endpoints
- Modifying graceful shutdown logic

**DO NOT** modify this package for:

- Business logic → Use appropriate `pkg/` package
- Event coordination → Use `pkg/controller`
- Configuration parsing → Use `pkg/core/config`

## Package Structure

```
cmd/controller/
├── main.go            # Main entry point (controller daemon)
├── validate.go        # Validate command (CLI tool)
├── flags.go           # Command-line flags (if separated)
└── CLAUDE.md          # This file
```

## Commands

### Main Controller (main.go)

The primary controller daemon that watches Kubernetes resources and manages HAProxy configuration.

### Validate Command (validate.go)

CLI tool for validating HAProxyTemplateConfig CRDs with embedded validation tests.

**Usage:**

```bash
haptic-controller validate -f config.yaml [flags]
```

**Observability Flags:**

```bash
# Show rendered content preview for failed assertions (first 200 chars)
haptic-controller validate -f config.yaml --verbose

# Dump complete rendered content (haproxy.cfg, maps, files, certs)
haptic-controller validate -f config.yaml --dump-rendered

# Show template execution trace with timing
haptic-controller validate -f config.yaml --trace-templates

# Combine flags for comprehensive debugging
haptic-controller validate -f config.yaml --verbose --dump-rendered --trace-templates
```

**Flag Details:**

- `--verbose` - Shows content preview for failed assertions
  - Displays target name and size
  - Shows first 200 characters of content
  - Includes hints for further debugging
  - Default: false

- `--dump-rendered` - Dumps all rendered content
  - HAProxy configuration (haproxy.cfg)
  - Map files with full content
  - General files with full content
  - SSL certificates with full content
  - Shown after test results
  - Default: false

- `--trace-templates` - Shows template execution trace
  - Template names and render order
  - Timing information in milliseconds
  - Useful for identifying slow templates
  - Default: false
  - Note: Shows top-level renders only. Use with `--profile-includes` for full call tree

**Enhanced Error Messages:**

All validation errors include helpful context by default (no flags needed):

```
Error: pattern "backend api-.*" not found in haproxy.cfg (target size: 1234 bytes).
       Hint: Use --verbose to see content preview
```

**Implementation:**

The validate command uses `pkg/controller/testrunner` to execute tests and format results. It creates a temporary directory for HAProxy validation and cleans up afterward.

**Example Debugging Workflow:**

```bash
# 1. Run tests and see enhanced error messages
haptic-controller validate -f config.yaml
# Output: "pattern X not found in map:foo.map (target size: 61 bytes). Hint: Use --verbose"

# 2. Enable verbose mode to see content preview
haptic-controller validate -f config.yaml --verbose
# Output: Shows first 200 chars of map:foo.map

# 3. See full content if needed
haptic-controller validate -f config.yaml --dump-rendered
# Output: Complete content of all rendered files

# 4. Identify slow templates
haptic-controller validate -f config.yaml --trace-templates
# Output: Template execution trace with timing
```

## Key Responsibilities

1. **Initialize logging**: Set up structured logging
2. **Parse flags/env vars**: Load configuration from environment
3. **Create Kubernetes client**: Connect to cluster
4. **Create EventBus**: Initialize event infrastructure
5. **Start components**: Boot components in correct order (5 stages)
6. **Handle signals**: Graceful shutdown on SIGTERM/SIGINT
7. **Expose endpoints**: Health checks, metrics, profiling

**Not responsible for:**

- Configuration validation (done in pkg/controller/validators)
- Resource watching (done in pkg/k8s)
- Template rendering (done in pkg/templating)
- Event coordination (done in pkg/controller)

## Five-Stage Startup

The controller uses event-driven staged startup:

```
Stage 1: Config Management Components
  - ConfigLoader (parses HAProxyTemplateConfig CRD)
  - CredentialsLoader (parses credentials Secret)
  - ConfigValidator (basic + template + jsonpath validators via scatter-gather)
  - Commentator (subscribes to all events for domain-aware logging)

Stage 2: Wait for Valid Config
  - Fetch HAProxyTemplateConfig CRD and credentials Secret synchronously
  - Block until ConfigValidatedEvent received

Stage 3: Resource Watchers
  - Create a watcher.Watcher for each spec.watchedResources entry
  - Start the CRD and credentials SingleWatchers (immediate-callback mode)
  - IndexSynchronizationTracker waits for every store's initial sync

Stage 4: EventBus.Start()
  - Replays the pre-start buffer to all components that subscribed during construction
  - Without this step, leader-only components miss the events that fired during init

Stage 5: Reconciliation & Observability Components
  - Reconciler (debounces resource-index updates)
  - Coordinator (drives the render → validate → publish pipeline; leader-only)
  - Renderer, HAProxyValidator (all-replica)
  - DeploymentScheduler, Deployer, DriftMonitor (leader-only)
  - Discovery, ConfigPublisher, Metrics, Webhook
  - Initial ReconciliationTriggeredEvent is published

Controller operational: subsequent CRD or Secret changes cancel the iteration
context, components shut down, and the loop restarts with the new config (no pod
restart required).
```

**Why staged?**

- Prevents reconciliation before config is valid
- Ensures all resources loaded before first reconciliation
- Clear startup progression for debugging
- Testable stages

## Flags and Environment Variables

Authoritative source: `cmd/controller/run.go` (`init()` registers flags) and `cmd/controller/main.go` (package doc). Each flag falls back to its env var, then to the listed default.

| Flag | Env var | Default | Purpose |
|------|---------|---------|---------|
| `--crd-name` | `CRD_NAME` | `haproxy-config` | Name of the `HAProxyTemplateConfig` CRD the controller reads. |
| `--secret-name` | `SECRET_NAME` | `haproxy-credentials` | Name of the `Secret` with `dataplane_username` / `dataplane_password`. |
| `--webhook-cert-secret-name` | `WEBHOOK_CERT_SECRET_NAME` | `""` (disabled) | TLS Secret for the validating-admission-webhook server. Empty disables the webhook entirely. |
| `--debug-port` | `DEBUG_PORT` | `0` (disabled) | Port for the introspection HTTP server (`/healthz` + `/debug/vars` + `/debug/pprof`). The Helm chart sets this to `8080` by default. |
| `--kubeconfig` | — | (in-cluster) | Out-of-cluster development. |
| — | `LOG_LEVEL` | `INFO` | Initial log level: `TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR` (case-insensitive; `WARNING` accepted as alias for `WARN`). The CRD's `spec.logging.level`, when non-empty, takes over at runtime via the dynamic logger. |

The controller's namespace is auto-detected from the in-cluster service-account token mount; there is no `CONTROLLER_NAMESPACE` env var. There is no `LOG_FORMAT`, `METRICS_PORT`, `HEALTH_PORT`, or `ENABLE_PPROF` env var either — log output is always structured slog (logfmt-ish in the default handler), and the metrics / healthz / pprof ports come from the CRD (`spec.controller.metricsPort`, `spec.controller.healthzPort`) and the `--debug-port` flag respectively.

## Signal Handling

```go
// main.go
func main() {
    // Create context that cancels on signals
    ctx, stop := signal.NotifyContext(context.Background(),
        os.Interrupt,    // SIGINT (Ctrl+C)
        syscall.SIGTERM, // SIGTERM (Kubernetes pod termination)
    )
    defer stop()

    // Start components with context
    g, gCtx := errgroup.WithContext(ctx)

    g.Go(func() error { return component1.Run(gCtx) })
    g.Go(func() error { return component2.Run(gCtx) })

    // Wait for signal or component error
    select {
    case <-ctx.Done():
        log.Info("Shutdown signal received")
    case <-gCtx.Done():
        log.Error("Component error", "error", gCtx.Err())
    }

    // Graceful shutdown with timeout
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    done := make(chan error)
    go func() {
        done <- g.Wait()
    }()

    select {
    case err := <-done:
        if err != nil {
            log.Error("Shutdown error", "error", err)
            os.Exit(1)
        }
    case <-shutdownCtx.Done():
        log.Error("Shutdown timeout exceeded")
        os.Exit(1)
    }

    log.Info("Controller stopped")
}
```

## Health and Metrics

### Health Endpoint

The controller doesn't hand-roll its HTTP servers — three reusable infra packages own the surfaces:

- **`pkg/introspection`** — `/healthz` (also aliased as `/health`), `/debug/vars`, `/debug/vars/<name>?field={…}`, `/debug/events`, `/debug/pprof/*`. Backed by an instance-based registry of `Var` implementations. Listening port comes from `--debug-port` / `DEBUG_PORT` (default 0 = disabled; the Helm chart sets 8080). Setting it to 0 disables `/debug/*` and moves `/healthz` to the port specified by `controller.ports.healthz`. There is no separate `/readyz` — Kubernetes readiness probes hit `/healthz` too.
- **`pkg/metrics`** — `/metrics` via Prometheus `promhttp` against an instance-based `prometheus.Registerer`. Port comes from `spec.controller.metricsPort` (default 9090, set to 0 to disable). The instance-scoped registry is critical: every reinitialization iteration creates a fresh registry so metrics get GC'd cleanly when the iteration ends.
- **`pkg/webhook`** — admission webhook HTTPS (`/validate`) and a sidecar `/healthz`. Disabled when `--webhook-cert-secret-name` is empty. Port comes from `spec.controller.webhookPort` (default 9443).

Don't add new HTTP surfaces in `cmd/controller`. Add a `Var` to `pkg/introspection`, a metric to `pkg/controller/metrics`, or a handler on the existing webhook server.

## Testing Approach

### Integration Tests

Test full startup sequence:

```go
func TestController_Startup(t *testing.T) {
    // Create fake clients: typed for Secrets, dynamic for the haproxy-haptic.org CRD.
    // The controller reads its config from an HAProxyTemplateConfig CRD via the
    // dynamic client; CRDs aren't part of the typed clientset.
    fakeKube := fake.NewSimpleClientset()
    scheme := runtime.NewScheme()
    require.NoError(t, haproxyv1alpha1.AddToScheme(scheme))
    fakeDynamic := dynamicfake.NewSimpleDynamicClient(scheme, &haproxyv1alpha1.HAProxyTemplateConfig{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "haproxy-config",
            Namespace: "default",
        },
        Spec: haproxyv1alpha1.HAProxyTemplateConfigSpec{
            // … fill in podSelector, watchedResources, haproxyConfig.template …
        },
    })

    // Credentials Secret — only dataplane_username and dataplane_password;
    // there are no validation_* keys.
    secret := &corev1.Secret{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "haproxy-credentials",
            Namespace: "default",
        },
        Data: map[string][]byte{
            "dataplane_username": []byte("admin"),
            "dataplane_password": []byte("pass"),
        },
    }
    fakeKube.CoreV1().Secrets("default").Create(ctx, secret, metav1.CreateOptions{})

    // Start controller. There is no struct + NewController() — the entry point
    // is a package-level function `controller.Run(ctx, k8sClient, crdName,
    // secretName, webhookCertSecretName, debugPort)`. Build a `*client.Client`
    // around the fake clients and pass it in.
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    k8sClient := &client.Client{
        Clientset: fakeKube,
        Dynamic:   fakeDynamic,
    }

    done := make(chan error)
    go func() {
        done <- controller.Run(ctx, k8sClient, "haproxy-config", "haproxy-credentials", "", 0)
    }()

    // Wait for the iteration to publish ControllerStartedEvent (subscribed via
    // the EventBus) — there is no public `IsReady()` method.
    waitForEvent[*events.ControllerStartedEvent](t, ctx, eventBus, 5*time.Second)

    // Trigger shutdown
    cancel()
    require.NoError(t, <-done)
}
```

### End-to-End Tests

Test complete workflow with kind cluster:

```bash
# Run e2e test with real cluster
KEEP_CLUSTER=true go test ./cmd/controller/... -tags=e2e -v
```

## Common Pitfalls

### Starting Components Out of Order

**Problem**: Components started before dependencies ready.

```go
// Bad — race condition: resource watchers spin up before the CRD has loaded
resourceWatcher := resourcewatcher.New(eventBus, ...)
go resourceWatcher.Run(ctx)

// CRD might not be loaded yet — the configloader hasn't published
// ConfigValidatedEvent.
configLoader := configloader.NewConfigLoaderComponent(eventBus, logger)
go configLoader.Run(ctx)
```

**Solution**: Follow staged startup pattern.

```go
// Good — stages ensure dependencies (mirrors pkg/controller/iteration.go)

// Stage 1–2: configloader + credentialsloader run, the iteration blocks
// on the synchronous fetch+validate of the CRD and Secret before any
// resource watchers exist.
configLoader := configloader.NewConfigLoaderComponent(eventBus, logger)
go configLoader.Run(ctx)
config := <-validatedConfigCh   // ConfigValidatedEvent

// Stage 3: only after the config is in hand, build resource watchers
// from spec.watchedResources and wait for their initial sync.
resourceWatcher := resourcewatcher.New(eventBus, config, ...)
go resourceWatcher.Run(ctx)
```

### Not Handling Shutdown Timeout

**Problem**: Components don't stop within deadline.

```go
// Bad - no timeout
<-ctx.Done()
g.Wait()  // Might hang forever
```

**Solution**: Enforce shutdown timeout.

```go
// Good - timeout enforced
<-ctx.Done()

shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

done := make(chan error)
go func() {
    done <- g.Wait()
}()

select {
case <-done:
    // Clean shutdown
case <-shutdownCtx.Done():
    log.Error("Shutdown timeout - forcing exit")
    os.Exit(1)
}
```

### Logging Before Logger Initialized

**Problem**: Using logger before setup.

```go
// Bad - logger not initialized
slog.Info("starting")  // Might not use configured format/level

logger := logging.New(config)
slog.SetDefault(logger)
```

**Solution**: Initialize logger early.

```go
// Good - logger first
logger := logging.New(logging.Config{
    Level:  slog.LevelInfo,
    Format: logging.FormatJSON,
})
slog.SetDefault(logger)

slog.Info("controller starting")  // Uses configured logger
```

### Ignoring Component Errors

**Problem**: Component error doesn't stop controller.

```go
// Bad - errors ignored
go component1.Run(ctx)  // Error lost
go component2.Run(ctx)  // Error lost
```

**Solution**: Use errgroup to propagate errors.

```go
// Good - errors propagate
g, gCtx := errgroup.WithContext(ctx)

g.Go(func() error { return component1.Run(gCtx) })
g.Go(func() error { return component2.Run(gCtx) })

if err := g.Wait(); err != nil {
    log.Error("component error", "error", err)
    os.Exit(1)
}
```

## Adding New Startup Stage

If you need to add a new stage:

1. Determine stage position (before/after existing stages)
2. Create component
3. Add to startup sequence
4. Add wait condition (if needed)
5. Update tests
6. Document new stage

### Example: Adding Metrics Initialization Stage

```go
// Add before Stage 5 (reconciliation)
func (c *Controller) Run(ctx context.Context) error {
    // ... Stages 1-4 ...

    // New Stage 5: Metrics
    log.Info("Stage 5: Metrics initialization")
    domainMetrics := metrics.NewMetrics(registry)
    metricsCollector := metrics.New(domainMetrics, c.eventBus)
    go metricsCollector.Start(ctx)

    // Wait for metrics ready (optional)
    if err := metricsCollector.WaitForReady(ctx); err != nil {
        return fmt.Errorf("metrics initialization failed: %w", err)
    }

    // Original Stage 5 becomes Stage 6
    log.Info("Stage 6: Reconciliation components")
    // ... reconciliation setup ...
}
```

## Debugging Startup Issues

### Enable Debug Logging

```bash
# Local (running the binary directly)
export LOG_LEVEL=DEBUG  # also: TRACE, INFO (default), WARN, ERROR

# In a Kubernetes deployment installed via the chart
kubectl set env -n haptic deployment/haptic-controller LOG_LEVEL=DEBUG
```

For runtime changes without a pod restart, set `spec.logging.level` on the `HAProxyTemplateConfig` CRD instead — the configloader picks it up live and the dynamic logger switches without re-init.

### Check Stage Progress

```bash
kubectl logs -f -n haptic deployment/haptic-controller | grep -i "stage\|operational"

# Expected progression:
# Stage 1: Config management
# Stage 2: Waiting for valid config
# Stage 3: Resource watchers
# Stage 4: Waiting for index sync
# Stage 5: Reconciliation components
# Controller fully operational
```

### Identify Stuck Stage

```bash
# If startup hangs, look at the last log line
kubectl logs -n haptic deployment/haptic-controller | tail -1

# Stage 2 stuck → check the HAProxyTemplateConfig CRD exists and validates
kubectl get htplcfg -n haptic
kubectl get htplcfg -n haptic haproxy-config -o yaml | yq '.status'

# Stage 4 stuck → at least one watcher's initial sync isn't completing
kubectl logs -n haptic deployment/haptic-controller | grep -i "sync\|watcher"
```

### Enable Profiling

`pprof` is part of the introspection HTTP server, not a separate binary. The Helm chart already enables it on port 8080 by default (same port as `/healthz`); no env-var toggle is needed.

```bash
# Port-forward the introspection port
kubectl port-forward -n haptic deployment/haptic-controller 8080:8080

# Profile CPU
go tool pprof http://localhost:8080/debug/pprof/profile?seconds=30

# Profile memory
go tool pprof http://localhost:8080/debug/pprof/heap
```

To disable profiling in production, set `controller.debugPort: 0` (the chart then moves `/healthz` to `controller.ports.healthz`). To move it to a dedicated port, set `controller.debugPort: <port>`.

## Kubernetes Deployment

### RBAC Requirements

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: haptic-controller
rules:
  # Primary CRD (input)
  - apiGroups: ["haproxy-haptic.org"]
    resources: ["haproxytemplateconfigs"]
    verbs: ["get", "watch", "list"]

  # Output CRDs (rendered config + auxiliary files)
  - apiGroups: ["haproxy-haptic.org"]
    resources:
      - "haproxycfgs"
      - "haproxygeneralfiles"
      - "haproxycrtlistfiles"
      - "haproxymapfiles"
    verbs: ["get", "watch", "list", "create", "update", "patch", "delete"]

  # Credentials Secret + watched-resource Secrets (TLS)
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "watch", "list"]

  # Leader election
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "create", "update"]

  # Watched resources (Ingress, Service, EndpointSlice, etc.)
  - apiGroups: ["networking.k8s.io"]
    resources: ["ingresses"]
    verbs: ["get", "watch", "list"]

  - apiGroups: [""]
    resources: ["services", "pods", "namespaces"]
    verbs: ["get", "watch", "list"]

  # Add per-watched-resource rules as `spec.watchedResources` grows;
  # the Helm chart auto-generates these. See operations/security.md.
```

### Deployment Manifest

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: haptic-controller
  namespace: default
spec:
  replicas: 2  # Default; leader election handles deploy-side exclusivity
  selector:
    matchLabels:
      app: haptic
  template:
    metadata:
      labels:
        app: haptic
    spec:
      serviceAccountName: haptic-controller
      containers:
      - name: controller
        image: haptic:latest
        args:
          - run
          - --crd-name=haproxy-config
          - --secret-name=haproxy-credentials
          - --debug-port=8080
        env:
        - name: LOG_LEVEL
          value: "INFO"  # TRACE / DEBUG / INFO / WARN / ERROR
        ports:
        - name: healthz
          containerPort: 8080  # also serves /debug/* — see --debug-port
        - name: metrics
          containerPort: 9090
        livenessProbe:
          httpGet:
            path: /healthz
            port: healthz
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /healthz       # the controller exposes /healthz only;
            port: healthz        # /readyz is not served separately
          initialDelaySeconds: 5
          periodSeconds: 5
        resources:
          requests:
            cpu: 100m
            memory: 512Mi        # request = limit gives Guaranteed QoS
          limits:
            memory: 512Mi        # CPU limit deliberately omitted
```

## Resources

- Architecture: `/docs/controller/docs/development/design.md`
- Controller orchestration: `pkg/controller/CLAUDE.md`
- Configuration: `pkg/core/CLAUDE.md`
- Helm chart: `charts/haptic/`
