# tests/acceptance - Acceptance Tests

Development context for end-to-end acceptance testing.

**API Documentation**: See `tests/acceptance/README.md`

## When to Work Here

Work in this directory when:

- Writing end-to-end regression tests
- Testing critical user-facing functionality
- Verifying controller lifecycle behavior
- Testing CRD/Secret reload functionality
- Validating controller internal state via debug endpoints

**DO NOT** work here for:

- Unit tests → Place in pkg/ alongside code
- Component integration tests → Use `tests/integration/`
- Quick feedback tests → Use unit tests
- Performance benchmarks → Create separate `tests/performance/`

## Package Purpose

Provides end-to-end acceptance testing infrastructure using the kubernetes-sigs/e2e-framework. Tests verify complete controller behavior including:

- Full controller deployment in Kubernetes
- ConfigMap and Secret watching
- Configuration reload on changes
- Template rendering
- Debug endpoint accessibility

Key differences from integration tests:

- **Integration tests**: Component-level with fixtures (dataplane, parser, etc.)
- **Acceptance tests**: Full controller deployment, user-facing features

## Architecture

```
E2E Framework (kubernetes-sigs/e2e-framework)
    ├── Environment Setup
    │   └── Kind Cluster Creation
    │
    ├── Feature Definition
    │   ├── Setup (create resources)
    │   ├── Assess (verify behavior)
    │   └── Teardown (cleanup)
    │
    └── Test Infrastructure
        ├── DebugClient (NodePort + HTTP client)
        ├── Fixtures (ConfigMap, Secret, Deployment, Services)
        └── Helpers (pod finding, waiting, NodePort access)
```

### Kubernetes API Server Proxy

Tests access controller debug and metrics endpoints via the Kubernetes API server proxy rather than port-forwarding or NodePort services. This is a deliberate design decision:

**Why API Server Proxy?**

- Port-forwarding uses SPDY protocol which breaks under parallel test execution (EOF, connection reset errors)
- NodePort requires `extraPortMappings` in Kind configuration, which doesn't work in DinD environments
- API server proxy routes requests through the existing API server connection
- Works reliably in all environments including DinD (Docker-in-Docker) on GitLab CI
- Uses built-in client-go `ProxyGet` method - first-party Kubernetes API

**How it works:**

1. Tests create ClusterIP services for debug and metrics endpoints
2. `SetupDebugClient()` and `SetupMetricsAccess()` create clients that use `ProxyGet`
3. Requests are routed: `client → API server → service → pod`

**Helper functions:**

- `SetupDebugClient()` - creates debug service and returns DebugClient using API proxy
- `SetupMetricsAccess()` - creates metrics service and returns MetricsClient using API proxy
- `WaitForServiceEndpoints()` - waits for service endpoints to be ready

## Key Components

### Environment Setup

Uses e2e-framework for test orchestration:

```go
// env.go
func Setup(t *testing.T) env.Environment {
    if testEnv != nil {
        return testEnv
    }

    testEnv = env.New()

    // Create Kind cluster
    kindCluster := kind.NewProvider().WithName("haproxy-test")

    testEnv.Setup(
        func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
            kubeconfigPath, err := kindCluster.Create(ctx)
            if err != nil {
                return ctx, fmt.Errorf("failed to create kind cluster: %w", err)
            }

            cfg.WithKubeconfigFile(kubeconfigPath)
            return ctx, nil
        },
        func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
            // Create test namespace
            // ...
        },
    )

    testEnv.Finish(
        func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
            // Cleanup namespace
            // ...
        },
        func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
            // Destroy Kind cluster
            // ...
        },
    )

    return testEnv
}
```

**Features**:

- Shared environment across acceptance tests
- Automatic setup/teardown
- Kind cluster lifecycle management

### DebugClient

HTTP client for accessing controller debug endpoints via Kubernetes API server proxy:

```go
// debug_client.go
type DebugClient struct {
    clientset   kubernetes.Interface
    namespace   string
    serviceName string
    port        string
}

// Create via SetupDebugClient - uses ProxyGet to access the service
func NewDebugClient(clientset kubernetes.Interface, namespace, serviceName string, port int32) *DebugClient {
    return &DebugClient{
        clientset:   clientset,
        namespace:   namespace,
        serviceName: serviceName,
        port:        strconv.Itoa(int(port)),
    }
}

func (dc *DebugClient) GetConfig(ctx context.Context) (map[string]interface{}, error) {
    // Uses ProxyGet to fetch /debug/vars/config
}

func (dc *DebugClient) GetRenderedConfig(ctx context.Context) (string, error) {
    // Uses ProxyGet to fetch /debug/vars/rendered
}

func (dc *DebugClient) WaitForConfigVersion(ctx context.Context, expectedVersion string, timeout time.Duration) error {
    // Polls until config version matches
}
```

**Purpose**: Access controller internal state without log parsing.

**Why API server proxy instead of NodePort or port-forwarding?**

- Port-forwarding uses SPDY which breaks under parallel test execution (EOF errors)
- NodePort requires extraPortMappings which don't work in DinD environments
- API proxy uses existing API server connection - always works
- Uses built-in `ProxyGet` method from client-go
- No connection lifecycle management needed

**Why not logs?**

- Logs are brittle (format changes break tests)
- Logs don't provide structured state
- Debug endpoints are stable API
- Can query specific state (JSONPath field selection)

### Test Fixtures

Factory functions for creating test resources:

```go
// fixtures.go

// NewConfigMap creates ConfigMap with given configuration
func NewConfigMap(namespace, name, configYAML string) *corev1.ConfigMap

// NewSecret creates Secret with HAProxy credentials
func NewSecret(namespace, name string) *corev1.Secret

// NewControllerDeployment creates controller deployment
func NewControllerDeployment(namespace, configMapName, secretName string, debugPort int32) *appsv1.Deployment

// NewDebugService creates a ClusterIP Service for accessing the debug endpoint via API proxy
func NewDebugService(namespace, deploymentName string, debugPort int32) *corev1.Service

// NewMetricsService creates a ClusterIP Service for accessing the metrics endpoint via API proxy
func NewMetricsService(namespace, deploymentName string, metricsPort int32) *corev1.Service
```

**Predefined configs**:

- `InitialConfigYAML`: Version 1 config (maxconn 2000)
- `UpdatedConfigYAML`: Version 2 config (maxconn 4000)

## Usage Patterns

### Basic Acceptance Test

```go
package acceptance

import (
    "testing"
    "sigs.k8s.io/e2e-framework/pkg/features"
)

func TestMyFeature(t *testing.T) {
    testEnv := Setup(t)

    feature := features.New("My Feature").
        Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
            client, err := cfg.NewClient()
            require.NoError(t, err)

            // Create test resources
            cm := NewConfigMap(namespace, "my-config", InitialConfigYAML)
            err = client.Resources().Create(ctx, cm)
            require.NoError(t, err)

            // ... create Secret, Deployment

            return ctx
        }).
        Assess("Feature works", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
            client, _ := cfg.NewClient()

            // Setup debug and metrics access via API proxy
            debugClient, err := SetupDebugClient(ctx, client, namespace, 30*time.Second)
            require.NoError(t, err)

            metricsClient, err := SetupMetricsAccess(ctx, client, namespace, 30*time.Second)
            require.NoError(t, err)

            // Wait for controller to complete startup reconciliation
            _, err = WaitForControllerReadyWithMetrics(ctx, client, namespace, metricsClient, 2*time.Minute)
            require.NoError(t, err)

            // Use debug client - uses API proxy, no Start() needed!
            config, err := debugClient.GetConfig(ctx)
            require.NoError(t, err)
            assert.NotNil(t, config)

            return ctx
        }).
        Feature()

    testEnv.Test(t, feature)
}
```

## Common Patterns

### Waiting for Resources

```go
// Wait for pod ready
err := WaitForPodReady(ctx, client, namespace, "app=my-app", 2*time.Minute)
require.NoError(t, err)

// Find specific pod
pod, err := GetControllerPod(ctx, client, namespace)
require.NoError(t, err)
```

### Using Debug Endpoints

```go
// Setup debug client via API proxy - no Start() needed
debugClient, err := SetupDebugClient(ctx, client, namespace, 30*time.Second)
require.NoError(t, err)

// Get full config
config, err := debugClient.GetConfig(ctx)
require.NoError(t, err)

// Get specific field
version := config["version"].(string)

// Get rendered HAProxy config
rendered, err := debugClient.GetRenderedConfig(ctx)
require.NoError(t, err)
assert.Contains(t, rendered, "frontend http")

// Wait for specific version
err = debugClient.WaitForConfigVersion(ctx, "v2", 30*time.Second)
require.NoError(t, err)

// Get pipeline status (rendering, validation, deployment phases)
status, err := debugClient.GetPipelineStatus(ctx)
require.NoError(t, err)
```

### Resource Creation

```go
client, _ := cfg.NewClient()

// Create ConfigMap
cm := NewConfigMap(namespace, "config", configYAML)
err := client.Resources().Create(ctx, cm)
require.NoError(t, err)

// Create Secret
secret := NewSecret(namespace, "credentials")
err = client.Resources().Create(ctx, secret)
require.NoError(t, err)

// Create Deployment
deployment := NewControllerDeployment(namespace, "config", "credentials", 6060)
err = client.Resources().Create(ctx, deployment)
require.NoError(t, err)
```

## Common Pitfalls

### Not Waiting for Controller Ready

**Problem**: Test tries to access debug endpoints before controller is fully initialized.

```go
// Bad - controller might not be ready
debugClient, _ := SetupDebugClient(ctx, client, namespace, 30*time.Second)
config, _ := debugClient.GetConfig(ctx)  // Might fail or return incomplete data!
```

**Solution**: Wait for controller to complete startup reconciliation using metrics.

```go
// Good - wait for controller ready using metrics
metricsClient, _ := SetupMetricsAccess(ctx, client, namespace, 30*time.Second)
_, err := WaitForControllerReadyWithMetrics(ctx, client, namespace, metricsClient, 2*time.Minute)
require.NoError(t, err)

debugClient, _ := SetupDebugClient(ctx, client, namespace, 30*time.Second)
config, _ := debugClient.GetConfig(ctx)  // Works!
```

### Using Old Port-Forward or NodePort Pattern

**Problem**: Old code uses port-forwarding or NodePort which are unreliable.

```go
// Bad - port-forwarding breaks under load (SPDY EOF errors)
debugClient := NewDebugClient(restConfig, pod, DebugPort)
debugClient.Start(ctx)  // OLD PATTERN - don't use!
defer debugClient.Stop()

// Bad - NodePort doesn't work in DinD without extraPortMappings
debugClient := NewDebugClient(nodeHost, nodePort)  // OLD PATTERN
```

**Solution**: Use API proxy-based SetupDebugClient - no Start/Stop needed.

```go
// Good - API proxy is reliable in all environments
debugClient, err := SetupDebugClient(ctx, client, namespace, 30*time.Second)
require.NoError(t, err)
// No Start() or Stop() needed - client uses API proxy
```

### Not Waiting for Config Reload

**Problem**: Test checks config immediately after update, sees old version.

```go
// Bad - doesn't wait for reload
client.Resources().Update(ctx, &cm)
config, _ := debugClient.GetConfig(ctx)  // Still old version!
```

**Solution**: Use WaitForConfigVersion to poll until reloaded.

```go
// Good - wait for reload
client.Resources().Update(ctx, &cm)
err := debugClient.WaitForConfigVersion(ctx, cm.ResourceVersion, 30*time.Second)
require.NoError(t, err)

config, _ := debugClient.GetConfig(ctx)  // New version!
```

### Hardcoding Configuration Values

**Problem**: Test breaks when config format changes.

```go
// Bad - hardcoded field paths
maxconn := config["config"].(map[string]interface{})["templates"].(map[string]interface{})["main"]
```

**Solution**: Use string matching or JSONPath via debug client.

```go
// Good - flexible matching
configStr := fmt.Sprint(config)
assert.Contains(t, configStr, "maxconn 2000")

// Or use rendered config
rendered, _ := debugClient.GetRenderedConfig(ctx)
assert.Contains(t, rendered, "maxconn 2000")
```

## Docker Image Requirements

**CRITICAL**: Acceptance tests require the Docker image to be tagged as `haproxy-template-ic:test` (NOT `:dev` or any other tag).

The test framework automatically loads this image into the kind cluster during test setup. If you make code changes, you must rebuild the image with the correct tag:

```bash
# Standard rebuild (uses Docker cache for faster builds)
docker build -t haproxy-template-ic:test -f Dockerfile .

# Force complete rebuild (necessary if cached layers are stale)
docker build --no-cache -t haproxy-template-ic:test -f Dockerfile .
```

**When to use `--no-cache`:**

- After making code changes that aren't reflected in test behavior
- When you suspect Docker is using old cached layers
- When debugging mysterious test failures that don't match your code changes

**Common mistake**: Building with the wrong tag and wondering why tests don't use your latest code:

```bash
# WRONG - tests won't use this image
docker build -t haproxy-template-ic:dev -f Dockerfile .

# CORRECT - tests will use this image
docker build -t haproxy-template-ic:test -f Dockerfile .
```

## Debugging Acceptance Tests

### Keep Resources After Test

E2E framework manages cluster lifecycle. To inspect:

```bash
# Run test
go test -v ./tests/acceptance

# While test is running or failed, inspect
kubectl config use-context kind-haproxy-test
kubectl get pods -n haproxy-test
kubectl logs -n haproxy-test haproxy-template-ic-xxx

# Access debug endpoint via NodePort (tests create the service automatically)
NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
NODE_PORT=$(kubectl get svc -n haproxy-test haproxy-template-ic-debug -o jsonpath='{.spec.ports[0].nodePort}')
curl http://$NODE_IP:$NODE_PORT/debug/vars/config

# Or for quick manual testing, use port-forward (not used in actual tests)
kubectl port-forward -n haproxy-test pod/haproxy-template-ic-xxx 6060:6060
curl http://localhost:6060/debug/vars/config
```

### View Controller Logs

```bash
# Follow logs during test
kubectl logs -n haproxy-test haproxy-template-ic-xxx -f
```

### Manual Test Execution

**IMPORTANT**: Always use the `:test` tag for acceptance tests.

```bash
# Create cluster manually
kind create cluster --name haproxy-test

# Build controller image with CORRECT tag
docker build -t haproxy-template-ic:test -f Dockerfile .

# Load image into kind cluster
kind load docker-image haproxy-template-ic:test --name haproxy-test

# Run test
go test -v ./tests/acceptance

# Cleanup
kind delete cluster --name haproxy-test
```

**Troubleshooting tip**: If tests fail after code changes, ensure the image was rebuilt with `--no-cache`:

```bash
# Rebuild without cache to ensure latest code is included
docker build --no-cache -t haproxy-template-ic:test -f Dockerfile .

# Load into kind cluster
kind load docker-image haproxy-template-ic:test --name haproxy-test

# Run test again
go test -v ./tests/acceptance
```

## Resources

- E2E Framework: <https://github.com/kubernetes-sigs/e2e-framework>
- Kind: <https://kind.sigs.k8s.io/>
- Debug endpoints: `pkg/introspection/README.md`
