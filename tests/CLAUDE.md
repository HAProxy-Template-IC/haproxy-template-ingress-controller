# tests/ - Test Organization

Development context for the test directory structure.

**API Documentation**: See `tests/README.md`

## When to Work Here

Work in this directory when:

- Writing architecture validation tests
- Organizing test infrastructure
- Adding new test types or frameworks
- Creating shared test utilities

**DO NOT** work here for:

- Unit tests → Place in same package as code (e.g., `pkg/templating/engine_test.go`)
- Integration tests → Use `tests/integration/`
- Acceptance tests → Use `tests/acceptance/`

## Directory Purpose

This directory serves as the root for all non-unit tests and contains:

- Architecture validation tests (arch-go)
- Test subdirectories for integration and acceptance tests
- Shared test infrastructure (in subdirectories)

## Directory Structure

```
tests/
├── architecture_test.go          # Architecture rule validation (arch-go)
├── integration/                  # Integration tests (fixenv + Kind)
│   ├── env.go                   # Test fixtures (cluster, HAProxy, clients)
│   ├── kind_cluster.go          # Kind cluster management
│   ├── haproxy.go              # HAProxy deployment helpers
│   ├── sync_test.go            # HAProxy sync integration tests
│   ├── auxiliaryfiles_test.go  # Auxiliary file tests
│   └── testdata/               # Test configuration files
└── acceptance/                   # Acceptance tests (e2e-framework)
    ├── env.go                   # E2E framework setup
    ├── fixtures.go              # Test resource factories
    ├── debug_client.go          # Debug HTTP client
    └── configmap_reload_test.go # ConfigMap reload test
```

## Test Types

### Architecture Tests

**File**: `architecture_test.go`

**Purpose**: Validates that the codebase follows architectural constraints defined in `arch-go.yml`.

**What it tests**:

- Package dependency rules
- No circular dependencies
- Layer separation (controller can depend on all, libraries are independent)

**Example constraints**:

```yaml
# arch-go.yml
dependencies_rules:
  - package: "pkg/core"
    should_not_depend_on:
      - "pkg/controller"
      - "pkg/dataplane"
      - "pkg/k8s"
      - "pkg/templating"

  - package: "pkg/controller"
    may_depend_on:
      - "pkg/**"  # Controller can depend on everything
```

**Running**:

```bash
go test ./tests -run TestArchitecture
```

**Output on failure**:

```
Architecture validation failed!
Dependencies rule violations:
  Rule: pkg/core should not depend on pkg/controller
    Package: pkg/core/config
      - imports pkg/controller/events (forbidden)
```

### Integration Tests

**Directory**: `tests/integration/`

**Framework**: fixenv + Kind

**Purpose**: Test components against real Kubernetes cluster and HAProxy instances.

**See**: `tests/integration/CLAUDE.md` for details

### Acceptance Tests

**Directory**: `tests/acceptance/`

**Framework**: kubernetes-sigs/e2e-framework + Kind

**Purpose**: End-to-end regression tests for critical user-facing functionality.

**See**: `tests/acceptance/CLAUDE.md` for details

## Test Tags

### integration

Integration tests are tagged with `//go:build integration`:

```go
//go:build integration

package integration

func TestSyncFrontendAdd(t *testing.T) {
    // Integration test...
}
```

**Run integration tests only**:

```bash
go test -tags=integration ./tests/integration/...
```

**Run without integration tests** (default):

```bash
go test ./...  # Skips integration tests
```

### Why tags?

- Integration tests are slow (create Kind cluster, deploy HAProxy)
- Should not run in quick feedback loops
- Separate CI steps for unit vs integration tests
- Developers can choose when to run slow tests

## Running Tests

### All Tests (Unit + Architecture)

```bash
make test
```

This runs:

- All package unit tests
- Architecture validation test

**Does NOT run**:

- Integration tests (requires `-tags=integration`)
- Acceptance tests (separate target)

### Integration Tests Only

```bash
make test-integration
```

This runs:

- Creates Kind cluster (or reuses existing)
- Runs all integration tests in `tests/integration/`
- Keeps cluster by default for faster subsequent runs

**Environment variables**:

```bash
# Force cleanup after tests
KEEP_CLUSTER=false make test-integration

# Use specific Kubernetes version
KIND_NODE_VERSION=v1.29.0 make test-integration
```

### Acceptance Tests Only

```bash
make test-acceptance
```

This runs:

- Creates Kind cluster
- Builds and loads controller Docker image
- Runs all acceptance tests in `tests/acceptance/`
- Each test is fully isolated (new namespace per test)

### All Tests (Including Integration and Acceptance)

```bash
make test-all
```

This runs:

- Unit tests
- Architecture tests
- Integration tests
- Acceptance tests

**Duration**: ~5-10 minutes depending on cluster state

## Common Patterns

### Architecture Validation

Tests enforce clean architecture via `arch-go.yml`:

```go
func TestArchitecture(t *testing.T) {
    moduleInfo := configuration.Load("haproxy-template-ic")
    config, err := configuration.LoadConfig("../arch-go.yml")
    require.NoError(t, err)

    result := api.CheckArchitecture(moduleInfo, *config)

    if !result.Pass {
        // Print detailed violations
        t.Fatal("Architecture validation failed")
    }
}
```

**When it fails**:

1. Check error output for specific violation
2. Either fix the dependency (move code to correct package)
3. Or update `arch-go.yml` if rule is incorrect

### Test Organization

**Unit tests**: Same package as implementation

```
pkg/templating/
    engine.go
    engine_test.go  # Unit tests for engine.go
```

**Integration tests**: Separate directory with fixtures

```
tests/integration/
    env.go          # Shared fixtures
    sync_test.go    # Integration tests using fixtures
```

**Acceptance tests**: Separate directory with framework

```
tests/acceptance/
    env.go                   # E2E framework setup
    configmap_reload_test.go # Feature tests
```

## Common Pitfalls

### Running Integration Tests Without Tag

**Problem**: Integration tests don't run.

```bash
go test ./tests/integration/...
# No tests run - all are tagged with //go:build integration
```

**Solution**: Add `-tags=integration` flag.

```bash
go test -tags=integration ./tests/integration/...
```

### Architecture Test Fails on New Dependency

**Problem**: Added new import, architecture test fails.

```
Package: pkg/core/config
  - imports pkg/controller/events (forbidden)
```

**Solution**: Either:

1. Remove the import (core shouldn't depend on controller)
2. Move event types to a shared location
3. Update `arch-go.yml` if rule is wrong

### Integration Tests Slow

**Problem**: Integration tests take 2+ minutes every run.

**Solution**: Keep cluster between runs (default behavior).

```bash
# First run: creates cluster (~2 min)
make test-integration

# Subsequent runs: reuses cluster (~30 sec)
make test-integration

# Manual cleanup when done
kind delete cluster --name=haproxy-test
```

Or set `KEEP_CLUSTER=false` to always cleanup:

```bash
KEEP_CLUSTER=false make test-integration
```

### Test Namespaces Left Behind

**Problem**: Many `test-*` namespaces accumulating.

**Solution**: Kind cluster automatically cleans up old test namespaces in background on startup. Or manually cleanup:

```bash
kubectl delete ns -l 'kubernetes.io/metadata.name~=test-'
# Or delete entire cluster
kind delete cluster --name=haproxy-test
```

## Adding New Test Types

### Checklist

1. **Identify test type**: Unit, integration, or acceptance?
2. **Choose location**: Same package (unit) or tests/ subdirectory?
3. **Select framework**: Standard testing, fixenv, or e2e-framework?
4. **Add build tags**: If slow tests, add `//go:build integration`
5. **Update Makefile**: Add target if needed
6. **Document**: Update relevant CLAUDE.md and README.md

### Example: Adding Performance Tests

```bash
# Create new subdirectory
mkdir tests/performance

# Create framework setup
cat > tests/performance/env.go <<EOF
//go:build performance

package performance

import "testing"

func Setup(t *testing.T) {
    // Performance test infrastructure
}
EOF

# Create test
cat > tests/performance/render_bench_test.go <<EOF
//go:build performance

package performance

func BenchmarkTemplateRendering(b *testing.B) {
    // Benchmark template rendering
}
EOF

# Add Makefile target
echo 'test-performance: ## Run performance tests' >> Makefile
echo '\tgo test -tags=performance -bench=. ./tests/performance/...' >> Makefile
```

## Test Infrastructure

### Shared Fixtures

Integration and acceptance tests use different fixture systems:

**Integration** (fixenv):

```go
// tests/integration/env.go
func SharedCluster(env fixenv.Env) *KindCluster
func TestNamespace(env fixenv.Env) *Namespace
func TestHAProxy(env fixenv.Env) *HAProxyInstance
```

**Acceptance** (e2e-framework):

```go
// tests/acceptance/env.go
func Setup(t *testing.T) env.Environment
func GetControllerPod(ctx context.Context, client klient.Client, namespace string) (*corev1.Pod, error)
```

### Test Data

Test data lives in subdirectories:

```
tests/integration/testdata/
    ├── configs/           # HAProxy configurations
    ├── templates/         # Template files
    └── resources/         # Kubernetes manifests
```

## Resources

- Architecture validation: `arch-go.yml` (project root)
- Integration tests: `tests/integration/CLAUDE.md`
- Acceptance tests: `tests/acceptance/CLAUDE.md`
- Makefile targets: `Makefile` (search for `test-*`)
