# cmd/controller - Main Entry Point

Development context for the controller application entry point.

**Architecture**: See `/docs/development/design.md` (Startup and Initialization section)

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
controller validate -f config.yaml [flags]
```

**Observability Flags:**

```bash
# Show rendered content preview for failed assertions (first 200 chars)
controller validate -f config.yaml --verbose

# Dump complete rendered content (haproxy.cfg, maps, files, certs)
controller validate -f config.yaml --dump-rendered

# Show template execution trace with timing
controller validate -f config.yaml --trace-templates

# Combine flags for comprehensive debugging
controller validate -f config.yaml --verbose --dump-rendered --trace-templates
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
controller validate -f config.yaml
# Output: "pattern X not found in map:foo.map (target size: 61 bytes). Hint: Use --verbose"

# 2. Enable verbose mode to see content preview
controller validate -f config.yaml --verbose
# Output: Shows first 200 chars of map:foo.map

# 3. See full content if needed
controller validate -f config.yaml --dump-rendered
# Output: Complete content of all rendered files

# 4. Identify slow templates
controller validate -f config.yaml --trace-templates
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
  - ConfigWatcher (watches ConfigMap)
  - ConfigValidator (validates config)
  - EventBus.Start() (replay buffered events)

Stage 2: Wait for Valid Config
  - Block until ConfigValidatedEvent received
  - Publish ControllerStartedEvent

Stage 3: Resource Watchers
  - Create ResourceWatcher for each watched resource
  - Start IndexSynchronizationTracker

Stage 4: Wait for Index Sync
  - Block until IndexSynchronizedEvent received

Stage 5: Reconciliation Components
  - Reconciler (debounces changes)
  - Executor (orchestrates rendering/deployment)

All components running → Controller operational
```

**Why staged?**

- Prevents reconciliation before config is valid
- Ensures all resources loaded before first reconciliation
- Clear startup progression for debugging
- Testable stages

## Environment Variables

```go
// Configuration location
ENV_VAR: CONTROLLER_NAMESPACE
Default: auto-detect from service account
Purpose: Namespace where controller runs

ENV_VAR: CONFIG_CONFIGMAP_NAME
Default: haproxy-template-ic-config
Purpose: ConfigMap name containing configuration

ENV_VAR: CREDENTIALS_SECRET_NAME
Default: haproxy-template-ic-credentials
Purpose: Secret name containing credentials

// Logging
ENV_VAR: LOG_LEVEL
Default: info
Values: debug, info, warn, error
Purpose: Logging verbosity

ENV_VAR: LOG_FORMAT
Default: json
Values: json, text
Purpose: Log output format

// Metrics/Profiling
ENV_VAR: METRICS_PORT
Default: 9090
Purpose: Prometheus metrics endpoint port

ENV_VAR: HEALTH_PORT
Default: 8080
Purpose: Health check endpoint port

ENV_VAR: ENABLE_PPROF
Default: false
Purpose: Enable Go profiling endpoints
```

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

```go
// Health check handler
http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    // Check if controller is operational
    if !controller.IsReady() {
        w.WriteHeader(http.StatusServiceUnavailable)
        w.Write([]byte("not ready"))
        return
    }

    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ok"))
})

// Readiness check
http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
    // Check if all stages completed
    if !controller.AllStagesComplete() {
        w.WriteHeader(http.StatusServiceUnavailable)
        w.Write([]byte("not ready"))
        return
    }

    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ready"))
})

// Start health server
go func() {
    log.Info("Starting health server", "port", healthPort)
    if err := http.ListenAndServe(fmt.Sprintf(":%d", healthPort), nil); err != nil {
        log.Error("Health server failed", "error", err)
    }
}()
```

### Metrics Endpoint

```go
import "github.com/prometheus/client_golang/prometheus/promhttp"

// Register metrics
prometheus.MustRegister(reconCounter)
prometheus.MustRegister(syncDuration)
prometheus.MustRegister(errorCounter)

// Metrics handler
http.Handle("/metrics", promhttp.Handler())

// Start metrics server
go func() {
    log.Info("Starting metrics server", "port", metricsPort)
    if err := http.ListenAndServe(fmt.Sprintf(":%d", metricsPort), nil); err != nil {
        log.Error("Metrics server failed", "error", err)
    }
}()
```

### Profiling (Development)

```go
import _ "net/http/pprof"

if enablePprof {
    go func() {
        log.Info("Starting pprof server", "port", 6060)
        if err := http.ListenAndServe(":6060", nil); err != nil {
            log.Error("Pprof server failed", "error", err)
        }
    }()
}

// Access profiling:
// http://localhost:6060/debug/pprof/
// go tool pprof http://localhost:6060/debug/pprof/heap
```

## Testing Approach

### Integration Tests

Test full startup sequence:

```go
func TestController_Startup(t *testing.T) {
    // Create fake Kubernetes cluster
    fakeClient := fake.NewSimpleClientset()

    // Create test ConfigMap
    configMap := &corev1.ConfigMap{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "haproxy-config",
            Namespace: "default",
        },
        Data: map[string]string{
            "config.yaml": validConfigYAML,
        },
    }
    fakeClient.CoreV1().ConfigMaps("default").Create(ctx, configMap, metav1.CreateOptions{})

    // Create test Secret
    secret := &corev1.Secret{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "haproxy-creds",
            Namespace: "default",
        },
        Data: map[string][]byte{
            "dataplane_username": []byte("admin"),
            "dataplane_password": []byte("pass"),
            // ... other credentials
        },
    }
    fakeClient.CoreV1().Secrets("default").Create(ctx, secret, metav1.CreateOptions{})

    // Start controller
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    controller := NewController(fakeClient)

    done := make(chan error)
    go func() {
        done <- controller.Run(ctx)
    }()

    // Verify startup progresses through all stages
    select {
    case err := <-done:
        require.NoError(t, err)
    case <-ctx.Done():
        t.Fatal("startup timeout")
    }

    // Verify controller is operational
    assert.True(t, controller.IsReady())
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
// Bad - race condition
resourceWatcher := createResourceWatcher()  // Needs config
go resourceWatcher.Run(ctx)

// Config might not be loaded yet!
configWatcher := createConfigWatcher()
go configWatcher.Run(ctx)
```

**Solution**: Follow staged startup pattern.

```go
// Good - stages ensure dependencies
// Stage 1: Config components
configWatcher := createConfigWatcher()
go configWatcher.Run(ctx)

// Stage 2: Wait for valid config
config := waitForConfig(ctx)

// Stage 3: Resource watchers (now config is available)
resourceWatcher := createResourceWatcher(config)
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
    metricsCollector := metrics.New(c.eventBus)
    go metricsCollector.Run(ctx)

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
# Set environment variable
export LOG_LEVEL=debug

# Or in Kubernetes
kubectl set env deployment/haproxy-template-ic-controller LOG_LEVEL=debug
```

### Check Stage Progress

```bash
# Watch logs for stage messages
kubectl logs -f deployment/haproxy-template-ic-controller | grep "Stage"

# Expected output:
# Stage 1: Config management
# Stage 2: Waiting for valid config
# Stage 3: Resource watchers
# Stage 4: Waiting for index sync
# Stage 5: Reconciliation components
# Controller fully operational
```

### Identify Stuck Stage

```bash
# If startup hangs, check which stage
kubectl logs deployment/haproxy-template-ic-controller | tail -1

# Stage 2 stuck? → Check ConfigMap
kubectl get configmap haproxy-template-ic-config

# Stage 4 stuck? → Check resource syncing
kubectl logs deployment/haproxy-template-ic-controller | grep "sync"
```

### Enable Profiling

```bash
# Set environment variable
kubectl set env deployment/haproxy-template-ic-controller ENABLE_PPROF=true

# Port-forward profiling endpoint
kubectl port-forward deployment/haproxy-template-ic-controller 6060:6060

# Profile CPU
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# Profile memory
go tool pprof http://localhost:6060/debug/pprof/heap
```

## Kubernetes Deployment

### RBAC Requirements

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: haproxy-template-ic-controller
rules:
  # Read ConfigMap (configuration)
  - apiGroups: [""]
    resources: ["configmaps"]
    verbs: ["get", "watch", "list"]

  # Read Secret (credentials)
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "watch", "list"]

  # Watch resources (Ingress, Service, etc.)
  - apiGroups: ["networking.k8s.io"]
    resources: ["ingresses"]
    verbs: ["get", "watch", "list"]

  - apiGroups: [""]
    resources: ["services", "pods"]
    verbs: ["get", "watch", "list"]

  # Add more resources as configured
```

### Deployment Manifest

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: haproxy-template-ic-controller
  namespace: default
spec:
  replicas: 1  # Single replica (no leader election yet)
  selector:
    matchLabels:
      app: haproxy-template-ic
  template:
    metadata:
      labels:
        app: haproxy-template-ic
    spec:
      serviceAccountName: haproxy-template-ic-controller
      containers:
      - name: controller
        image: haproxy-template-ic:latest
        env:
        - name: LOG_LEVEL
          value: "info"
        - name: LOG_FORMAT
          value: "json"
        - name: CONFIG_CONFIGMAP_NAME
          value: "haproxy-template-ic-config"
        - name: CREDENTIALS_SECRET_NAME
          value: "haproxy-template-ic-credentials"
        ports:
        - name: health
          containerPort: 8080
        - name: metrics
          containerPort: 9090
        livenessProbe:
          httpGet:
            path: /healthz
            port: health
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /readyz
            port: health
          initialDelaySeconds: 5
          periodSeconds: 5
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
```

## Resources

- Architecture: `/docs/development/design.md`
- Controller orchestration: `pkg/controller/CLAUDE.md`
- Configuration: `pkg/core/CLAUDE.md`
- Helm chart: `charts/haproxy-template-ic/`
