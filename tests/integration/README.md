# tests/integration

Integration tests using real Kubernetes cluster (Kind) and HAProxy instances.

## Overview

Integration tests verify component behavior against actual infrastructure:

- Real Kubernetes cluster (Kind)
- Real HAProxy pods with Dataplane API
- Actual configuration synchronization
- Multi-component interactions

**Framework**: [fixenv](https://github.com/rekby/fixenv) for fixture management + [Kind](https://kind.sigs.k8s.io/) for Kubernetes

## Quick Start

```bash
# Run all integration tests
make test-integration

# Run specific test
go test -tags=integration ./tests/integration -run TestSyncFrontendAdd -v

# Keep resources for debugging
KEEP_CLUSTER=true go test -tags=integration ./tests/integration -run TestSyncFrontendAdd -v
```

## File Structure

```
tests/integration/
├── env.go                   # Test fixtures (cluster, HAProxy, clients)
├── kind_cluster.go          # Kind cluster management
├── haproxy.go              # HAProxy deployment helpers
├── testutil.go             # Test utilities
├── env_test.go             # Fixture tests
├── sync_test.go            # HAProxy sync tests
├── auxiliaryfiles_test.go  # Auxiliary file tests
└── testdata/               # Test configurations
```

## Test Fixtures

Fixtures provide test resources with automatic dependency resolution and cleanup.

### Available Fixtures

```go
import "github.com/rekby/fixenv"

func TestExample(t *testing.T) {
    env := fixenv.New(t)

    // Get fixtures (dependencies resolved automatically)
    cluster := SharedCluster(env)                    // Kind cluster (shared)
    namespace := TestNamespace(env)                   // Test namespace (isolated)
    haproxy := TestHAProxy(env)                       // HAProxy pod
    client := TestDataplaneHighLevelClient(env)       // High-level dataplane client
    lowLevelClient := TestDataplaneClient(env)        // Low-level client
    parser := TestParser(env)                         // Config parser
    comparator := TestComparator(env)                 // Config comparator
}
```

### Fixture Dependency Chain

```
TestDataplaneHighLevelClient
    └→ TestHAProxy
        └→ TestNamespace
            └→ SharedCluster
```

Dependencies are resolved automatically when you request a fixture.

## Writing Integration Tests

### Basic Test

```go
//go:build integration

package integration

import (
    "testing"
    "github.com/rekby/fixenv"
    "github.com/stretchr/testify/assert"
)

func TestMyFeature(t *testing.T) {
    env := fixenv.New(t)

    // Get high-level dataplane client
    client := TestDataplaneHighLevelClient(env)

    // Prepare configuration
    config := `
global
    maxconn 2000

frontend http-in
    bind *:80
    default_backend servers
`

    // Sync configuration
    err := client.Sync(context.Background(), dataplane.SyncRequest{
        Config:         config,
        AuxiliaryFiles: &dataplane.AuxiliaryFiles{},
    })

    assert.NoError(t, err)

    // Verify deployed configuration
    deployed, err := client.GetDeployedConfig(context.Background())
    assert.NoError(t, err)
    assert.Contains(t, deployed, "frontend http-in")
}
```

### Table-Driven Tests

```go
func TestMultipleConfigs(t *testing.T) {
    tests := []struct {
        name   string
        config string
        check  func(t *testing.T, deployed string)
    }{
        {
            name: "simple frontend",
            config: `frontend http\n  bind *:80`,
            check: func(t *testing.T, deployed string) {
                assert.Contains(t, deployed, "frontend http")
            },
        },
        // More cases...
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            env := fixenv.New(t)
            client := TestDataplaneHighLevelClient(env)

            err := client.Sync(context.Background(), dataplane.SyncRequest{
                Config: tt.config,
            })
            assert.NoError(t, err)

            deployed, _ := client.GetDeployedConfig(context.Background())
            tt.check(t, deployed)
        })
    }
}
```

## Running Tests

### All Integration Tests

```bash
make test-integration
```

Equivalent to:

```bash
go test -tags=integration ./tests/integration/...
```

### Specific Test

```bash
go test -tags=integration ./tests/integration -run TestSyncFrontendAdd
```

### With Verbose Output

```bash
go test -tags=integration -v ./tests/integration -run TestSyncFrontendAdd
```

### Parallel Execution

```bash
go test -tags=integration -parallel=4 ./tests/integration/...
```

## Cluster Management

### Default Behavior (Recommended)

Cluster is created once and reused:

```bash
# First run: creates cluster (~2 min)
make test-integration

# Subsequent runs: reuses cluster (~30 sec)
make test-integration

# Manual cleanup when needed
kind delete cluster --name=haproxy-test
```

### Force Cleanup

```bash
KEEP_CLUSTER=false make test-integration
```

This recreates the cluster on every run (slower).

### Custom Kubernetes Version

```bash
KIND_NODE_VERSION=v1.29.0 make test-integration
```

## Environment Variables

- **KEEP_CLUSTER** - Keep cluster after tests (default: true)
  - `true`: Keep cluster for fast subsequent runs
  - `false`: Delete cluster after tests

- **KIND_NODE_VERSION** - Kubernetes version for Kind
  - Example: `v1.29.0`, `v1.28.0`
  - Default: Kind's default version

## Debugging

### Keep Resources

```bash
# Run test and keep all resources
KEEP_CLUSTER=true go test -tags=integration ./tests/integration -run TestSyncFrontendAdd -v

# Find test namespace
kubectl get namespaces | grep test-sync-frontend-add

# Set namespace variable
export NS=test-sync-frontend-add-a1b2c3d4

# Inspect HAProxy pod
kubectl get pods -n $NS
kubectl logs -n $NS haproxy-xxx
kubectl exec -n $NS haproxy-xxx -- cat /etc/haproxy/haproxy.cfg

# Access Dataplane API
kubectl port-forward -n $NS haproxy-xxx 5555:5555
curl -u admin:password http://localhost:5555/v2/services/haproxy/configuration/frontends
```

### View Real-Time Logs

```bash
# In one terminal: follow logs
kubectl logs -n $NS haproxy-xxx -f

# In another terminal: run test
KEEP_CLUSTER=true go test -tags=integration ./tests/integration -run TestSyncFrontendAdd -v
```

## Namespace Naming

Test namespaces are auto-generated from test names:

**Pattern**: `test-<normalized-test-name>-<hash>`

**Examples**:

- `TestSyncFrontendAdd` → `test-sync-frontend-add-a1b2c3d4`
- `TestSyncBackendAddHTTPResponseRule` → `test-sync-backend-add-http-response-rule-a1b2c3d4`

**Constraints**:

- Maximum 63 characters (Kubernetes limit)
- Lowercase only
- Alphanumeric and hyphens
- Hash suffix ensures uniqueness

## Common Patterns

### Testing with Auxiliary Files

```go
func TestSSLCertificate(t *testing.T) {
    env := fixenv.New(t)
    client := TestDataplaneHighLevelClient(env)

    config := `
frontend https
    bind *:443 ssl crt /etc/haproxy/ssl/server.pem
`

    auxFiles := &dataplane.AuxiliaryFiles{
        SSLCertificates: []dataplane.File{
            {
                Name:    "server.pem",
                Content: testCertificate,
                Path:    "/etc/haproxy/ssl/server.pem",
            },
        },
    }

    err := client.Sync(context.Background(), dataplane.SyncRequest{
        Config:         config,
        AuxiliaryFiles: auxFiles,
    })

    assert.NoError(t, err)
}
```

### Low-Level API Testing

```go
func TestLowLevelAPI(t *testing.T) {
    env := fixenv.New(t)
    client := TestDataplaneClient(env)

    // Use low-level API directly
    frontends, err := client.Frontend().GetAll(context.Background())
    assert.NoError(t, err)

    // Create frontend
    err = client.Frontend().Create(context.Background(), &models.Frontend{
        Name: "http-in",
        Mode: "http",
    })
    assert.NoError(t, err)
}
```

## Troubleshooting

### Tests Don't Run

**Problem**: `go test ./tests/integration/...` shows no tests.

**Solution**: Add `-tags=integration` flag:

```bash
go test -tags=integration ./tests/integration/...
```

### Cluster Creation Fails

**Possible causes**:

- Docker not running
- Port 6443 already in use
- Insufficient resources

**Solutions**:

```bash
# Check Docker
docker ps

# Delete existing cluster
kind delete cluster --name=haproxy-test

# Try again
make test-integration
```

### Namespace Name Too Long

**Problem**: Test name results in namespace > 63 characters.

**Solution**: Automatic - `generateSafeNamespaceName` truncates and adds hash.

### Resources Accumulate

**Problem**: Many test namespaces left behind.

**Solution**: Background cleanup runs automatically, or manual:

```bash
# Delete all test namespaces
kubectl delete ns -l 'kubernetes.io/metadata.name~=test-'

# Or delete entire cluster
kind delete cluster --name=haproxy-test
```

## Prerequisites

- Go 1.23+
- Docker (for Kind)
- Kind (installed automatically)

## Performance Tips

1. **Keep cluster** (default) - Faster subsequent runs
2. **Run in parallel** - Use `-parallel=N` flag
3. **Focus tests** - Run specific tests with `-run`
4. **Manual cleanup** - Only when needed

## Example Tests

- **sync_test.go** - Configuration synchronization tests
- **auxiliaryfiles_test.go** - SSL certificates, map files, general files
- **env_test.go** - Fixture initialization tests

## Resources

- fixenv: <https://github.com/rekby/fixenv>
- Kind: <https://kind.sigs.k8s.io/>
- Development context: `CLAUDE.md`
