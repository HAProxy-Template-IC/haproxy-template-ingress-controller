# tests/integration - Integration Tests

Development context for integration testing infrastructure.

**API Documentation**: See `tests/integration/README.md`

## When to Work Here

Work in this directory when:

- Writing integration tests against real Kubernetes/HAProxy
- Testing dataplane synchronization logic
- Verifying HAProxy configuration changes
- Testing multi-component interactions

**DO NOT** work here for:

- Unit tests → Place in pkg/ alongside code
- Quick tests → Use unit tests instead
- End-to-end acceptance tests → Use `tests/acceptance/`
- Architecture validation → Use `tests/architecture_test.go`

## Package Purpose

Provides integration testing infrastructure using real Kubernetes cluster (Kind) and HAProxy instances. Tests verify component behavior against actual infrastructure rather than mocks.

Key features:

- **Fixture-based testing** - Shared resources via fixenv
- **Real infrastructure** - Kind cluster + HAProxy pods
- **Fast test iteration** - Cluster reuse between runs
- **Test isolation** - Per-test namespaces
- **Automatic cleanup** - Configurable resource cleanup

## Architecture

```
Test Fixtures (fixenv)
    ├── SharedCluster (package-scoped, reused)
    │   └── KindCluster
    │       └── Kubernetes API
    │
    ├── TestNamespace (test-scoped, isolated)
    │   └── Created per test
    │
    ├── TestHAProxy (test-scoped)
    │   └── HAProxyInstance (pod + dataplane API)
    │
    ├── TestDataplaneClient
    │   └── Low-level client-native client
    │
    └── TestDataplaneHighLevelClient
        └── High-level dataplane.Client (Sync API)
```

Fixture dependency chain:

```
TestDataplaneHighLevelClient
    → TestHAProxy
        → TestNamespace
            → SharedCluster
```

## Key Components

### Fixture System (fixenv)

Uses [fixenv](https://github.com/rekby/fixenv) for declarative fixture management:

```go
func TestSyncFrontendAdd(t *testing.T) {
    env := fixenv.New(t)

    // Request fixture - dependencies resolved automatically
    haproxy := TestHAProxy(env)
    client := TestDataplaneHighLevelClient(env)

    // Test logic...
}
```

**Benefits**:

- **Declarative dependencies** - Fixtures declare what they need
- **Automatic ordering** - Dependencies created in correct order
- **Caching** - Expensive resources shared when possible
- **Scoping** - Package-scoped vs test-scoped fixtures
- **Cleanup** - Automatic cleanup on test completion

### SharedCluster Fixture

Package-scoped Kind cluster shared across all tests:

```go
// env.go
func SharedCluster(env fixenv.Env) *KindCluster {
    return fixenv.CacheResult(env, func() (*fixenv.GenericResult[*KindCluster], error) {
        cluster, err := SetupKindCluster(&KindClusterConfig{
            Name: "haproxy-test",
        })
        if err != nil {
            return nil, err
        }

        return fixenv.NewGenericResultWithCleanup(cluster, func() {
            if ShouldKeepCluster() == "true" {
                // Keep cluster for next run
                return
            }
            _ = cluster.Teardown()
        }), nil
    }, fixenv.CacheOptions{Scope: fixenv.ScopePackage})
}
```

**Scope**: Package (one per test run)
**Lifetime**: Entire test session
**Default**: Kept between runs (KEEP_CLUSTER=true)

### TestNamespace Fixture

Test-scoped namespace for resource isolation:

```go
func TestNamespace(env fixenv.Env) *Namespace {
    cluster := SharedCluster(env)  // Declare dependency

    return fixenv.CacheResult(env, func() (*fixenv.GenericResult[*Namespace], error) {
        name := generateSafeNamespaceName(env.T().Name())
        ns, err := cluster.CreateNamespace(name)
        if err != nil {
            return nil, err
        }

        return fixenv.NewGenericResultWithCleanup(ns, func() {
            if ShouldKeepCluster() == "true" {
                return  // Keep namespace
            }
            _ = ns.Delete()
        }), nil
    })
}
```

**Scope**: Test (one per test)
**Lifetime**: Single test execution
**Naming**: Auto-generated from test name with hash suffix

**Example namespace name**:

```
test-sync-frontend-add-a1b2c3d4
```

### TestHAProxy Fixture

Test-scoped HAProxy deployment:

```go
func TestHAProxy(env fixenv.Env) *HAProxyInstance {
    ns := TestNamespace(env)  // Dependency on namespace

    return fixenv.CacheResult(env, func() (*fixenv.GenericResult[*HAProxyInstance], error) {
        haproxy, err := DeployHAProxy(ns, DefaultHAProxyConfig())
        if err != nil {
            return nil, err
        }

        return fixenv.NewGenericResultWithCleanup(haproxy, func() {
            if ShouldKeepCluster() == "true" {
                return
            }
            _ = haproxy.Delete()
        }), nil
    })
}
```

**Provides**:

- HAProxy pod with dataplane API enabled
- Default credentials (admin/password)
- Minimal initial configuration
- Dataplane endpoint (URL, username, password)

### Client Fixtures

**Low-level client** (client-native):

```go
func TestDataplaneClient(env fixenv.Env) *client.DataplaneClient {
    haproxy := TestHAProxy(env)

    return fixenv.CacheResult(env, func() (*fixenv.GenericResult[*client.DataplaneClient], error) {
        endpoint := haproxy.GetDataplaneEndpoint()

        dataplaneClient, err := client.New(client.Config{
            BaseURL:  endpoint.URL,
            Username: endpoint.Username,
            Password: endpoint.Password,
        })
        // ...
    })
}
```

**High-level client** (dataplane.Client - Sync API):

```go
func TestDataplaneHighLevelClient(env fixenv.Env) *dataplane.Client {
    haproxy := TestHAProxy(env)

    return fixenv.CacheResult(env, func() (*fixenv.GenericResult[*dataplane.Client], error) {
        endpoint := haproxy.GetDataplaneEndpoint()

        client, err := dataplane.NewClient(context.Background(), dataplane.Endpoint{
            URL:      endpoint.URL,
            Username: endpoint.Username,
            Password: endpoint.Password,
        })
        // ...
    })
}
```

## Usage Patterns

### Basic Integration Test

```go
//go:build integration

package integration

import (
    "testing"
    "github.com/rekby/fixenv"
    "github.com/stretchr/testify/assert"
)

func TestSyncFrontendAdd(t *testing.T) {
    env := fixenv.New(t)

    // Get fixtures (dependencies resolved automatically)
    client := TestDataplaneHighLevelClient(env)

    // Test logic
    newConfig := `
global
    maxconn 2000

frontend http-in
    bind *:80
    default_backend servers
`

    err := client.Sync(context.Background(), dataplane.SyncRequest{
        Config:          newConfig,
        AuxiliaryFiles:  &dataplane.AuxiliaryFiles{},
    })

    assert.NoError(t, err)

    // Verify configuration deployed
    deployed, err := client.GetDeployedConfig(context.Background())
    assert.NoError(t, err)
    assert.Contains(t, deployed, "frontend http-in")
}
```

### Testing with Auxiliary Files

```go
func TestAuxiliaryFiles(t *testing.T) {
    env := fixenv.New(t)
    client := TestDataplaneHighLevelClient(env)

    // Create config using SSL certificate
    config := `
global
    maxconn 2000

frontend https-in
    bind *:443 ssl crt /etc/haproxy/ssl/server.pem
    default_backend servers
`

    // Provide SSL certificate as auxiliary file
    auxFiles := &dataplane.AuxiliaryFiles{
        SSLCertificates: []dataplane.File{
            {
                Name:    "server.pem",
                Content: testSSLCertificate,
                Path:    "/etc/haproxy/ssl/server.pem",
            },
        },
    }

    err := client.Sync(context.Background(), dataplane.SyncRequest{
        Config:         config,
        AuxiliaryFiles: auxFiles,
    })

    assert.NoError(t, err)

    // Verify SSL file deployed
    deployed, err := client.GetDeployedAuxiliaryFiles(context.Background())
    assert.NoError(t, err)
    assert.Len(t, deployed.SSLCertificates, 1)
}
```

### Low-Level API Testing

```go
func TestLowLevelAPI(t *testing.T) {
    env := fixenv.New(t)
    lowLevelClient := TestDataplaneClient(env)
    parser := TestParser(env)

    // Parse configuration
    parsed, err := parser.Parse(config)
    assert.NoError(t, err)

    // Use low-level API
    frontends, err := lowLevelClient.Frontend().GetAll(context.Background())
    assert.NoError(t, err)

    // Add frontend using parsed data
    err = lowLevelClient.Frontend().Create(context.Background(), parsed.Frontends[0])
    assert.NoError(t, err)
}
```

## Common Patterns

### Cluster Lifecycle Management

**Default behavior** (recommended):

```bash
# First run: creates cluster (~2 min)
go test -tags=integration ./tests/integration -run TestSyncFrontendAdd

# Subsequent runs: reuses cluster (~30 sec)
go test -tags=integration ./tests/integration -run TestSyncFrontendAdd

# Manual cleanup when needed
kind delete cluster --name=haproxy-test
```

**Force cleanup** (slower):

```bash
KEEP_CLUSTER=false go test -tags=integration ./tests/integration -run TestSyncFrontendAdd
```

### Namespace Cleanup

Namespaces are automatically cleaned up:

**During tests**:

- Old test namespaces cleaned in background on cluster creation

**After tests**:

- If KEEP_CLUSTER=false: immediate cleanup
- If KEEP_CLUSTER=true: kept for inspection, cleaned on next run

**Manual cleanup**:

```bash
# Delete all test namespaces
kubectl delete ns -l 'kubernetes.io/metadata.name~=test-'
```

### Safe Namespace Naming

Kubernetes namespace names must:

- Be ≤ 63 characters
- Be lowercase
- Contain only alphanumeric and hyphens

**generateSafeNamespaceName** handles this:

```go
// Long test name gets truncated intelligently
testName := "TestSyncBackendAddHTTPResponseRule"
namespace := generateSafeNamespaceName(testName)
// Result: "test-sync-backend-add-http-response-rule-a1b2c3d4"
```

Strategy:

1. Normalize: lowercase, replace "/" with "-"
2. Truncate if needed: keep meaningful part
3. Add hash suffix: ensure uniqueness
4. Verify: never exceeds 63 chars

## Common Pitfalls

### Not Using Build Tags

**Problem**: Integration tests don't run.

```bash
go test ./tests/integration/...
# No tests run!
```

**Solution**: Add `-tags=integration`.

```bash
go test -tags=integration ./tests/integration/...
```

### Fixture Dependency Not Declared

**Problem**: Test accesses resource that wasn't requested.

```go
// Bad - client not requested from env
func TestSomething(t *testing.T) {
    env := fixenv.New(t)
    namespace := TestNamespace(env)

    // Trying to use client without requesting it
    client.Sync(...)  // Where did client come from?
}
```

**Solution**: Request all fixtures from env.

```go
// Good - declare all dependencies
func TestSomething(t *testing.T) {
    env := fixenv.New(t)
    client := TestDataplaneHighLevelClient(env)  // Requests client fixture

    client.Sync(...)  // Works!
}
```

### Modifying Shared Cluster State

**Problem**: Test modifies cluster-level resources, affecting other tests.

```go
// Bad - modifies cluster-wide resource
func TestSomething(t *testing.T) {
    env := fixenv.New(t)
    cluster := SharedCluster(env)

    // Creates cluster-wide CustomResourceDefinition
    cluster.Clientset().ApiextensionsV1().CustomResourceDefinitions().Create(...)
}
```

**Solution**: Only modify namespace-scoped resources.

```go
// Good - test-scoped resources only
func TestSomething(t *testing.T) {
    env := fixenv.New(t)
    namespace := TestNamespace(env)

    // Create resources in test namespace only
    namespace.Clientset().CoreV1().ConfigMaps(namespace.Name).Create(...)
}
```

### Long Namespace Names

**Problem**: Test name too long, namespace creation fails.

```go
// Bad - test name results in namespace name > 63 chars
func TestSyncBackendAddHTTPResponseRuleWithVeryLongDescriptiveName(t *testing.T) {
    // generateSafeNamespaceName would truncate and add hash
}
```

**Solution**: generateSafeNamespaceName handles this automatically. No action needed.

### Not Checking KEEP_CLUSTER

**Problem**: Resources accumulate when debugging.

```bash
# Runs test, keeps all resources
KEEP_CLUSTER=true go test -tags=integration ./tests/integration -run TestX

# Runs another test, more resources accumulate
KEEP_CLUSTER=true go test -tags=integration ./tests/integration -run TestY

# Cluster has namespaces from both tests
```

**Solution**: Background cleanup handles this automatically, or manual cleanup:

```bash
# Cleanup namespaces
kubectl delete ns -l 'kubernetes.io/metadata.name~=test-'

# Or cleanup entire cluster
kind delete cluster --name=haproxy-test
```

## Testing Strategies

### Table-Driven Tests

```go
func TestSyncVariousConfigs(t *testing.T) {
    tests := []struct {
        name   string
        config string
        check  func(t *testing.T, deployed string)
    }{
        {
            name: "frontend with ACL",
            config: `
frontend http
    acl is_api path_beg /api
    use_backend api if is_api
`,
            check: func(t *testing.T, deployed string) {
                assert.Contains(t, deployed, "acl is_api")
            },
        },
        // More test cases...
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

### Parallel Tests

Fixtures support parallel execution:

```go
func TestParallelSyncs(t *testing.T) {
    tests := []struct {
        name   string
        config string
    }{
        {"config1", config1},
        {"config2", config2},
    }

    for _, tt := range tests {
        tt := tt  // Capture
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()  // Run in parallel

            env := fixenv.New(t)
            client := TestDataplaneHighLevelClient(env)

            // Each test gets isolated namespace
            err := client.Sync(context.Background(), dataplane.SyncRequest{
                Config: tt.config,
            })
            assert.NoError(t, err)
        })
    }
}
```

**How it works**:

- Each parallel test gets its own TestNamespace
- All share the same SharedCluster
- Complete isolation via namespaces

## Debugging Integration Tests

### Keep Resources for Inspection

```bash
# Run test and keep all resources
KEEP_CLUSTER=true go test -tags=integration ./tests/integration -run TestSyncFrontendAdd -v

# Inspect cluster
kubectl config use-context kind-haproxy-test
kubectl get namespaces | grep test-

# Find test namespace
NS=$(kubectl get namespaces | grep test-sync-frontend-add | awk '{print $1}')

# Inspect HAProxy pod
kubectl get pods -n $NS
kubectl logs -n $NS haproxy-xxx
kubectl exec -n $NS haproxy-xxx -- cat /etc/haproxy/haproxy.cfg

# Cleanup when done
kind delete cluster --name=haproxy-test
```

### Access Dataplane API

```bash
# Forward dataplane API port
kubectl port-forward -n $NS haproxy-xxx 5555:5555

# Access API
curl -u admin:password http://localhost:5555/v2/services/haproxy/configuration/frontends
```

### View Real-Time Logs

```bash
# Follow HAProxy logs during test
kubectl logs -n $NS haproxy-xxx -f
```

## Performance Optimization

### Cluster Reuse

**Default** (fast):

```bash
# Creates cluster once
make test-integration
# Subsequent runs reuse cluster
make test-integration
```

**Always recreate** (slow):

```bash
KEEP_CLUSTER=false make test-integration
```

### Parallel Execution

Tests using fixtures can run in parallel:

```bash
# Run integration tests in parallel
go test -tags=integration -parallel=4 ./tests/integration/...
```

Each test gets isolated namespace, so parallel execution is safe.

## Enterprise Testing

### Overview

Enterprise-specific tests are located in `enterprise_*_test.go` files and exercise HAProxy Enterprise-only features. These tests gracefully skip when running against HAProxy Community edition.

### Test Files

| File | Features Tested |
|------|-----------------|
| `enterprise_waf_test.go` | WAF rulesets, body rules, global config, profiles |
| `enterprise_keepalived_test.go` | VRRP instances, sync groups, track scripts, transactions |
| `enterprise_udplb_test.go` | UDP load balancers, dgram binds, log targets, ACLs |
| `enterprise_botmgmt_test.go` | Bot management profiles, CAPTCHAs |
| `enterprise_misc_test.go` | Facts, Ping, Structured Config, Git, Logging, Dynamic Update, ALOHA |

### Skip Helpers

All enterprise tests use capability-based skip helpers from `env.go`:

```go
func TestWAFRulesets(t *testing.T) {
    env := fixenv.New(t)
    skipIfWAFNotSupported(t, env)  // Skips if WAF not available

    // Test proceeds only on enterprise edition
    ctx := context.Background()
    dataplaneClient := TestDataplaneClient(env)
    wafOps := enterprise.NewWAFOperations(dataplaneClient)
    // ...
}
```

**Available skip helpers:**

| Helper | Skip Condition |
|--------|----------------|
| `skipIfNotEnterprise` | Not HAProxy Enterprise |
| `skipIfWAFNotSupported` | No WAF support |
| `skipIfWAFGlobalNotSupported` | No WAF global config (v3.2+ only) |
| `skipIfWAFProfilesNotSupported` | No WAF profiles (v3.2+ only) |
| `skipIfKeepalivedNotSupported` | No Keepalived/VRRP support |
| `skipIfUDPLBNotSupported` | No UDP load balancing support |
| `skipIfUDPLBACLsNotSupported` | No UDP LB ACLs (v3.2+ only) |
| `skipIfBotManagementNotSupported` | No bot management support |
| `skipIfGitIntegrationNotSupported` | No Git integration support |
| `skipIfAdvancedLoggingNotSupported` | No advanced logging support |
| `skipIfDynamicUpdateNotSupported` | No dynamic update support |
| `skipIfALOHANotSupported` | No ALOHA support |
| `skipIfPingNotSupported` | No Ping endpoint (v3.2+ only) |

### Running Enterprise Tests

Enterprise tests require HAProxy Enterprise. In CI, these tests are skipped automatically.

```bash
# Run all integration tests (enterprise tests skip on community)
make test-integration

# Run specific enterprise test (requires enterprise HAProxy)
KEEP_CLUSTER=true go test -tags=integration ./tests/integration -run TestWAF -v

# Run with verbose skip messages to see which tests are skipped
go test -tags=integration ./tests/integration -v 2>&1 | grep -E "(SKIP|PASS|FAIL)"
```

### Version-Gated Features

Some enterprise features require specific versions:

| Feature | Required Version |
|---------|-----------------|
| WAF rulesets, body rules | All Enterprise versions |
| WAF global config | v3.2+ Enterprise |
| WAF profiles | v3.2+ Enterprise |
| UDP LB ACLs | v3.2+ Enterprise |
| Ping endpoint | v3.2+ Enterprise |

Tests automatically check capabilities and skip appropriately:

```go
func TestWAFProfiles(t *testing.T) {
    env := fixenv.New(t)
    skipIfWAFProfilesNotSupported(t, env)  // Checks SupportsWAFProfiles capability

    // Test proceeds only on v3.2+ enterprise
}
```

### Adding New Enterprise Tests

1. **Create test file**: `enterprise_<feature>_test.go`
2. **Add skip helper** to `env.go` if new capability check needed
3. **Use appropriate skip helper** at test start
4. **Follow existing patterns** for transaction management and error handling

Example pattern:

```go
//go:build integration

package integration

import (
    "context"
    "testing"

    "github.com/rekby/fixenv"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    "haproxy-template-ic/pkg/dataplane/client/enterprise"
)

func TestNewEnterpriseFeature(t *testing.T) {
    env := fixenv.New(t)
    skipIfNewFeatureNotSupported(t, env)

    ctx := context.Background()
    dataplaneClient := TestDataplaneClient(env)
    featureOps := enterprise.NewFeatureOperations(dataplaneClient)

    t.Run("list-items", func(t *testing.T) {
        items, err := featureOps.GetAllItems(ctx)
        require.NoError(t, err)
        assert.NotNil(t, items)
    })
}
```

## Resources

- fixenv documentation: <https://github.com/rekby/fixenv>
- Kind documentation: <https://kind.sigs.k8s.io/>
- Test examples: `sync_test.go`, `auxiliaryfiles_test.go`
- Enterprise tests: `enterprise_waf_test.go`, `enterprise_keepalived_test.go`, etc.
- Fixture definitions: `env.go`
- Kind cluster management: `kind_cluster.go`
- HAProxy deployment: `haproxy.go`
