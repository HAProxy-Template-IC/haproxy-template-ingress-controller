# tests/acceptance

End-to-end acceptance tests for critical controller functionality.

## Overview

Acceptance tests verify complete controller behavior in a real Kubernetes environment:

- Full controller deployment
- ConfigMap/Secret watching
- Configuration reload on changes
- Template rendering
- Internal state verification via debug endpoints

**Framework**: [kubernetes-sigs/e2e-framework](https://github.com/kubernetes-sigs/e2e-framework) + [Kind](https://kind.sigs.k8s.io/)

**Purpose**: Regression tests for user-facing features.

## Quick Start

```bash
# Run all acceptance tests
make test-acceptance

# Run specific test
go test -v ./tests/acceptance -run TestConfigMapReload
```

## File Structure

```
tests/acceptance/
├── env.go                   # E2E framework setup (cluster, namespace, NodePort helpers)
├── fixtures.go              # Test resource factories (ConfigMap, Secret, Deployment, Services)
├── debug_client.go          # Debug HTTP client (NodePort + HTTP)
└── configmap_reload_test.go # ConfigMap reload regression test
```

## Writing Acceptance Tests

### Basic Test Structure

```go
package acceptance

import (
    "testing"
    "sigs.k8s.io/e2e-framework/pkg/features"
)

func TestMyFeature(t *testing.T) {
    // Get shared test environment
    testEnv := Setup(t)

    // Define feature test
    feature := features.New("My Feature Description").
        Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
            // Setup: Create resources (ConfigMap, Secret, Deployment)
            return ctx
        }).
        Assess("Verify behavior", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
            // Test: Verify expected behavior
            return ctx
        }).
        Feature()

    // Run test
    testEnv.Test(t, feature)
}
```

### Complete Example

```go
func TestConfigReload(t *testing.T) {
    testEnv := Setup(t)

    feature := features.New("Config Reload").
        Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
            client, err := cfg.NewClient()
            require.NoError(t, err)

            // Create ConfigMap
            cm := NewConfigMap(TestNamespace, ControllerConfigMapName, InitialConfigYAML)
            err = client.Resources().Create(ctx, cm)
            require.NoError(t, err)

            // Create Secret
            secret := NewSecret(TestNamespace, ControllerSecretName)
            err = client.Resources().Create(ctx, secret)
            require.NoError(t, err)

            // Deploy controller
            deployment := NewControllerDeployment(TestNamespace, ControllerConfigMapName, ControllerSecretName, DebugPort)
            err = client.Resources().Create(ctx, deployment)
            require.NoError(t, err)

            return ctx
        }).
        Assess("Initial config loaded", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
            client, _ := cfg.NewClient()

            // Setup debug and metrics access via NodePort
            debugClient, err := SetupDebugClient(ctx, client, TestNamespace, 30*time.Second)
            require.NoError(t, err)

            metricsHost, metricsPort, err := SetupMetricsAccess(ctx, client, TestNamespace, 30*time.Second)
            require.NoError(t, err)

            // Wait for controller to complete startup reconciliation
            _, err = WaitForControllerReadyWithMetrics(ctx, client, TestNamespace, metricsHost, metricsPort, 2*time.Minute)
            require.NoError(t, err)

            // Verify config - no Start() needed with NodePort!
            config, err := debugClient.GetConfig(ctx)
            require.NoError(t, err)
            assert.Contains(t, fmt.Sprint(config), "version 1")

            return ctx
        }).
        Feature()

    testEnv.Test(t, feature)
}
```

## Test Resources

### Fixtures

```go
// Create ConfigMap with controller configuration
cm := NewConfigMap(namespace, name, configYAML)

// Create Secret with HAProxy credentials
secret := NewSecret(namespace, name)

// Create controller Deployment
deployment := NewControllerDeployment(namespace, configMapName, secretName, debugPort)

// Create NodePort Service for debug endpoint
debugSvc := NewDebugService(namespace, deploymentName, debugPort)

// Create NodePort Service for metrics endpoint
metricsSvc := NewMetricsService(namespace, deploymentName, metricsPort)
```

### Predefined Configurations

```go
// Initial configuration (version 1, maxconn 2000)
InitialConfigYAML

// Updated configuration (version 2, maxconn 4000)
UpdatedConfigYAML
```

## Debug Client

Access controller internal state via debug HTTP endpoints using NodePort:

```go
// Setup debug client via NodePort (no Start() needed)
debugClient, err := SetupDebugClient(ctx, client, namespace, 30*time.Second)
require.NoError(t, err)

// Get current configuration
config, err := debugClient.GetConfig(ctx)
require.NoError(t, err)

// Get rendered HAProxy config
rendered, err := debugClient.GetRenderedConfig(ctx)
require.NoError(t, err)

// Wait for specific config version
err = debugClient.WaitForConfigVersion(ctx, "v2", 30*time.Second)
require.NoError(t, err)

// Get pipeline status (rendering, validation, deployment phases)
status, err := debugClient.GetPipelineStatus(ctx)
require.NoError(t, err)
```

**Why NodePort instead of port-forwarding?**

- Port-forwarding uses SPDY which breaks under parallel test execution (EOF errors)
- NodePort uses standard HTTP - reliable and well-tested
- No connection lifecycle management (Start/Stop) needed
- Works in DinD environments (GitLab CI) via DOCKER_HOST env var

**Why debug endpoints instead of logs?**

- Stable API (logs are brittle)
- Structured data (easy to query)
- JSONPath field selection (precise queries)
- Production-ready introspection

## Running Tests

### All Acceptance Tests

```bash
make test-acceptance
```

This:

1. Creates Kind cluster
2. Builds controller Docker image
3. Loads image into cluster
4. Runs all acceptance tests
5. Cleans up cluster

Duration: ~3-5 minutes

### Specific Test

```bash
go test -v ./tests/acceptance -run TestConfigMapReload
```

### With Custom Timeout

```bash
go test -v -timeout 15m ./tests/acceptance
```

## Debugging

### Preserve Namespace After Test

By default, test namespaces are deleted after each test completes. To preserve namespaces for debugging (especially useful for flaky tests), set `KEEP_NAMESPACE=true`:

```bash
# Run test with namespace preservation
KEEP_NAMESPACE=true go test -tags=acceptance -v ./tests/acceptance -run TestDataplaneUnreachable

# After test completes (or fails), inspect the namespace
kubectl --context kind-haproxy-test get pods -n test-dp-unreach-<hash>
kubectl --context kind-haproxy-test logs -n test-dp-unreach-<hash> -l app=haptic-controller

# Clean up manually when done
kubectl --context kind-haproxy-test delete namespace test-dp-unreach-<hash>
```

### View Controller Logs

```bash
# In one terminal: follow logs
kubectl logs -n haproxy-test haptic-xxx -f

# In another terminal: run test
go test -v ./tests/acceptance -run TestConfigMapReload
```

### Access Debug Endpoints

```bash
# During or after test - use NodePort (tests create the service automatically)
NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
NODE_PORT=$(kubectl get svc -n haproxy-test haptic-debug -o jsonpath='{.spec.ports[0].nodePort}')
curl http://$NODE_IP:$NODE_PORT/debug/vars/config

# Or use port-forward for manual debugging (not used in actual tests)
kubectl port-forward -n haproxy-test pod/haptic-xxx 6060:6060

# Query endpoints
curl http://localhost:6060/debug/vars
curl http://localhost:6060/debug/vars/config
curl http://localhost:6060/debug/vars/rendered
curl http://localhost:6060/debug/vars/events
```

### Inspect Resources

```bash
# List resources
kubectl get all -n haproxy-test

# Describe deployment
kubectl describe deployment -n haproxy-test haptic-controller

# Get ConfigMap
kubectl get configmap -n haproxy-test haproxy-config -o yaml

# Get Secret
kubectl get secret -n haproxy-test haproxy-credentials -o yaml
```

## Helper Functions

### SetupDebugClient

```go
debugClient, err := SetupDebugClient(ctx, client, namespace, timeout)
```

Creates the debug NodePort service and returns a ready-to-use debug client.

**Parameters**:

- `ctx`: Context
- `client`: Kubernetes client
- `namespace`: Namespace
- `timeout`: Maximum wait time for NodePort assignment

### SetupMetricsAccess

```go
metricsHost, metricsPort, err := SetupMetricsAccess(ctx, client, namespace, timeout)
```

Creates the metrics NodePort service and returns connection details.

### WaitForControllerReadyWithMetrics

```go
pod, err := WaitForControllerReadyWithMetrics(ctx, client, namespace, metricsHost, metricsPort, timeout)
```

Waits for pod ready AND controller reconciliation to complete (checks metrics).

### WaitForPodReady

```go
err := WaitForPodReady(ctx, client, namespace, labelSelector, timeout)
```

Waits for a pod matching the label selector to be ready.

### GetControllerPod

```go
pod, err := GetControllerPod(ctx, client, namespace)
```

Returns the controller pod in the specified namespace.

## Common Patterns

### Create and Update Resources

```go
// Setup: Create initial ConfigMap
cm := NewConfigMap(namespace, name, InitialConfigYAML)
err := client.Resources().Create(ctx, cm)
require.NoError(t, err)

// Assess: Update ConfigMap
var cm corev1.ConfigMap
err = client.Resources().Get(ctx, name, namespace, &cm)
require.NoError(t, err)

cm.Data["config"] = UpdatedConfigYAML
err = client.Resources().Update(ctx, &cm)
require.NoError(t, err)
```

### Wait for Config Reload

```go
// Update ConfigMap
err = client.Resources().Update(ctx, &cm)
require.NoError(t, err)

// Wait for controller to reload - debug client is already set up via NodePort
err = debugClient.WaitForConfigVersion(ctx, cm.ResourceVersion, 30*time.Second)
require.NoError(t, err)

// Verify new config loaded
config, _ := debugClient.GetConfig(ctx)
assert.Contains(t, fmt.Sprint(config), "version 2")
```

## Troubleshooting

### Test Timeout

**Problem**: Test times out waiting for pod ready.

**Causes**:

- Image pull issues
- Resource constraints
- Controller crash loop

**Debug**:

```bash
kubectl get pods -n haproxy-test
kubectl describe pod -n haproxy-test haptic-xxx
kubectl logs -n haproxy-test haptic-xxx
```

### Debug Client Access Fails

**Problem**: SetupDebugClient() fails or debug endpoint returns errors.

**Causes**:

- Pod not ready
- NodePort service not created
- Network issues

**Debug**:

```bash
# Check pod status
kubectl get pod -n haproxy-test haptic-xxx

# Check debug service exists and has NodePort
kubectl get svc -n haproxy-test haptic-debug

# Test direct access via NodePort
NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
NODE_PORT=$(kubectl get svc -n haproxy-test haptic-debug -o jsonpath='{.spec.ports[0].nodePort}')
curl -v http://$NODE_IP:$NODE_PORT/debug/vars/config
```

### Config Not Reloading

**Problem**: WaitForConfigVersion times out.

**Causes**:

- ConfigMap not updated
- Controller not watching ConfigMap
- Controller crashed

**Debug**:

```bash
# Check ConfigMap
kubectl get configmap -n haproxy-test haproxy-config -o yaml

# Check controller logs
kubectl logs -n haproxy-test haptic-xxx

# Check if pod restarted
kubectl get pod -n haproxy-test haptic-xxx -o jsonpath='{.status.containerStatuses[0].restartCount}'
```

## Constants

```go
const (
    TestNamespace           = "haproxy-test"
    ControllerDeploymentName = "haptic-controller"
    ControllerConfigMapName  = "haproxy-config"
    ControllerSecretName     = "haproxy-credentials"
    DebugPort               = 6060
    DefaultTimeout          = 2 * time.Minute
)
```

## Prerequisites

- Go 1.23+
- Docker (for building images and Kind)
- Kind (installed automatically)

## Example Tests

- **configmap_reload_test.go** - Regression test for ConfigMap reload functionality

## Resources

- E2E Framework: <https://github.com/kubernetes-sigs/e2e-framework>
- Kind: <https://kind.sigs.k8s.io/>
- Debug endpoints: `pkg/introspection/README.md`
- Development context: `CLAUDE.md`
