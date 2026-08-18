# tests/integration - Integration Tests

Development context for integration testing infrastructure.

**API Documentation**: See `tests/integration/README.md`

## When to Work Here

Work in this directory when:

- Writing integration tests against real Kubernetes/HAProxy
- Testing what the agent does to a pod's file tree and HAProxy's runtime state
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
    ├── AgentImage (package-scoped)
    │   └── HAProxy image + the haptic binary, loaded into the Kind node
    │
    ├── TestNamespace (test-scoped, isolated)
    │   └── Created per test
    │
    ├── TestHAProxy (test-scoped)
    │   └── HAProxyInstance (pod: haproxy + agent containers)
    │
    └── TestAgentClient
        └── pkg/dataplane/agent/client.Client through a forwarded port
```

Fixture dependency chain:

```
TestAgentClient
    → TestHAProxy
        → TestNamespace + AgentImage
            → SharedCluster
```

## Key Components

### Fixture System (fixenv)

Uses [fixenv](https://github.com/rekby/fixenv) for declarative fixture management:

```go
func TestSyncMyFeature(t *testing.T) {
    env := fixenv.New(t)

    // Request fixture - dependencies resolved automatically
    haproxy := TestHAProxy(env)
    session := NewSession(t, env)

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
    ns := TestNamespace(env)      // Dependency on namespace
    image := AgentImage(env)      // Dependency on the built pod image

    return fixenv.CacheResult(env, func() (*fixenv.GenericResult[*HAProxyInstance], error) {
        haproxy, err := DeployHAProxy(ns, DefaultHAProxyConfig(image))
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

- An HAProxy pod with two containers, `haproxy` (master-worker) and `agent`
- The bootstrap configuration, with the worker stats socket the agent commands
- Default credentials (admin/adminpwd) in `DATAPLANE_USERNAME` / `DATAPLANE_PASSWORD`
- A forwarded local port for the agent's API

### Session

`NewSession(t, env)` is the controller's side of one pod. It holds the desired
file set, renders it as a `renderplan.Plan`, asks `deployplan.Diff` what the pod
has to do, and sends the apply with the fencing token the deployer would send.

```go
session := NewSession(t, env)
session.SetConfig(LoadTestConfig(t, "basic/one-backend.cfg"))
session.Set("maps/domains.map", LoadTestFileContent(t, "map-files/domains.map"))
decision := session.MustApply(ctx)   // fails the test on a NACK
```

A plan built here declares the whole configuration as one core section, because
nothing in this suite parses HAProxy syntax. So any configuration change is a
reload, and only a change confined to auxiliary files can be reload-free — which
is exactly what makes the map and certificate cases worth asserting.

### Reading the pod

The agent serves `/v1/state` and `/v1/apply` and nothing else, so every
assertion about files or runtime state reads the pod directly:

```go
config, err := haproxy.ReadFile(ctx, ConfigPath)          // kubectl exec … cat
entries, err := haproxy.RuntimeMapEntries(ctx, mapPath)   // socat … show map
pid, err := haproxy.WorkerPID(ctx)                        // socat … show info
inventory := session.State(ctx).Inventory                 // GET /v1/state
```

The worker PID is the reload witness: a runtime apply must leave it alone.

## Usage Patterns

### Basic Integration Test

```go
//go:build integration

package integration

import (
    "context"
    "testing"

    "github.com/rekby/fixenv"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestMyFeature(t *testing.T) {
    env := fixenv.New(t)
    ctx := context.Background()
    session := NewSession(t, env)

    session.SetConfig(LoadTestConfig(t, "basic/one-backend.cfg"))
    require.Equal(t, deployplan.VerdictReload, session.MustApply(ctx).Verdict)

    onDisk, err := session.haproxy.ReadFile(ctx, ConfigPath)
    require.NoError(t, err)
    assert.Equal(t, session.Content(ConfigPath), onDisk)
}
```

`LoadTestConfig` adds the pod's `global` lines — the worker stats socket,
`default-path origin` and `crt-base` — to the fixture, so a fixture references
auxiliary files the way the chart renders them: `maps/x.map`, `general/x.http`,
and a bare filename for a certificate.

### Testing with auxiliary files

```go
func TestCertificateRotation(t *testing.T) {
    env := fixenv.New(t)
    ctx := context.Background()
    session := NewSession(t, env)

    session.SetConfig(LoadTestConfig(t, "ssl-frontend/with-ssl.cfg"))
    session.Set("ssl/example_com.pem", LoadTestFileContent(t, "ssl-certs/example.com.pem"))
    session.MustApply(ctx)

    // Same path, new bytes, identical configuration: runtime, no reload.
    session.Set("ssl/example_com.pem", LoadTestFileContent(t, "ssl-certs/updated.com.pem"))
    assert.Equal(t, deployplan.VerdictRuntime, session.MustApply(ctx).Verdict)
}
```

A CA bundle and a crt-list live beside ordinary files, so their kind cannot be
derived from the directory — declare it with `SetOfKind(path, content, kind)`.

### Table cases

`syncTestCase` (in `sync_common_test.go`) declares two file sets and what the
pod must end up with. It states no operation counts and no operation names: the
controller composes commands from two plans, not from a comparison of two
configuration texts, and a case that only declares configuration text cannot
describe them.

## Common Patterns

### Cluster Lifecycle Management

**Default behavior** (recommended):

```bash
# First run: creates cluster (~2 min)
go test -tags=integration ./tests/integration -run TestSyncFrontends

# Subsequent runs: reuses cluster (~30 sec)
go test -tags=integration ./tests/integration -run TestSyncFrontends

# Manual cleanup when needed
kind delete cluster --name=haproxy-test
```

**Force cleanup** (slower):

```bash
KEEP_CLUSTER=false go test -tags=integration ./tests/integration -run TestSyncFrontends
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
// Bad - session not built from env
func TestSomething(t *testing.T) {
    env := fixenv.New(t)
    namespace := TestNamespace(env)

    // Trying to use a session without requesting its fixtures
    session.MustApply(ctx)  // Where did session come from?
}
```

**Solution**: Request all fixtures from env.

```go
// Good - declare all dependencies
func TestSomething(t *testing.T) {
    env := fixenv.New(t)
    session := NewSession(t, env)  // Requests the pod and client fixtures

    session.MustApply(ctx)  // Works!
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

### Full Suite + KEEP_CLUSTER=true on Memory-Capped Docker

**Problem**: The full suite fails in batches on Docker Desktop (Windows/macOS) or
any Docker environment with a hard memory ceiling, with symptoms that look like
infrastructure flakiness: apiserver `TLS handshake timeout`, kube-controller-manager
`leaderelection lost`, pods rejected with `serviceaccount "default" not found`.

The cause is not parallelism, the race detector, or etcd fsync latency (all ruled
out experimentally). `KEEP_CLUSTER=true` — the local default — keeps every finished
test's namespace **with its HAProxy pod still running**, so load grows linearly
during the run (~130 pods ≈ 20 GiB by mid-suite). Once the Docker VM's memory
ceiling is hit, the kernel enters reclaim thrash: measured in-VM scheduler stalls
of 1–10 s (idle baseline ~6 ms), which blows the controller-manager's 10 s
leader-election renew deadline and stalls the apiserver. Tests then die in batches
in the second half of the run. CI is immune because it sets `KEEP_CLUSTER: "false"`
(per-test cleanup keeps it at ~4 concurrent pods); a Linux host without a VM
memory ceiling is immune because the accumulated idle pods just sit in native RAM.

**Solution**: For full-suite runs on memory-capped Docker, disable keep-alive:

```bash
KEEP_CLUSTER=false make test-integration
```

Reserve `KEEP_CLUSTER=true` for debugging individual tests, where the accumulation
stays small and the kept namespace is actually useful.

## Testing Strategies

### Table-Driven Tests

```go
func TestSyncVariousConfigs(t *testing.T) {
    tests := []syncTestCase{
        {
            name:              "frontend-with-acl",
            initialConfigFile: "frontends/basic.cfg",
            desiredConfigFile: "frontends/with-acl.cfg",
        },
        // More test cases...
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            t.Parallel()
            runSyncTest(t, tt)
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
            session := NewSession(t, env)

            // Each test gets isolated namespace
            session.SetConfig(tt.config)
            session.MustApply(context.Background())
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
KEEP_CLUSTER=true go test -tags=integration ./tests/integration -run TestSyncFrontends -v

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

### Ask the agent what the pod holds

```bash
# Forward the agent's port
kubectl port-forward -n $NS haproxy-test 5555:5555

# Applied plan, file digests, runtime inventory, last apply
curl -u admin:adminpwd http://localhost:5555/v1/state | jq
```

### Ask HAProxy what it is running

```bash
kubectl exec -n $NS haproxy-test -c haproxy -- \
    sh -c 'printf "show map\n" | socat stdio unix-connect:/etc/haproxy/haproxy-worker.sock'
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

## HAProxy version gates

The HAProxy release under test comes from `HAPROXY_VERSION`, which also selects
the image, so a gate and the pod can never disagree. Use `minHAProxy` on a table
case, or `skipBelowHAProxy(t, "3.1")` in a hand-written test. There is no
Enterprise pod: the bot-management suite was dropped with the Data Plane API and
the resulting coverage gap is recorded in ADR-0022.

## Resources

- fixenv documentation: <https://github.com/rekby/fixenv>
- Kind documentation: <https://kind.sigs.k8s.io/>
- Test examples: `sync_*_test.go` (split by section: backends, frontends, servers, global-defaults, sections, observability, auxiliary, idempotency, ca-file), plus `auxiliaryfiles_test.go`
- Fixture definitions: `env.go`; the controller side: `session.go`
- Reading the pod: `exec.go`
- Kind cluster management: `kind_cluster.go`; pod image: `image.go`
- HAProxy deployment: `haproxy.go`
- The same contract without a cluster: `tests/agent/`
- The wire contract: `docs/site/docs/development/agent.md`
