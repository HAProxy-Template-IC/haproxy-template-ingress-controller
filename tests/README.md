# tests/

Test organization and infrastructure for the HAProxy Template Ingress Controller.

## Overview

This directory contains all non-unit tests, organized by test type:

- **Architecture validation** - Enforces package dependency rules
- **Integration tests** - Component testing against real Kubernetes/HAProxy
- **Acceptance tests** - End-to-end regression tests

**Unit tests** are co-located with source code in `pkg/*/`.

## Directory Structure

```
tests/
├── architecture_test.go    # Architecture rule validation
├── integration/            # Integration tests (requires Kind cluster)
└── acceptance/             # Acceptance tests (requires Kind cluster + Docker image)
```

## Test Types

### Architecture Tests

Validates codebase architecture using [arch-go](https://github.com/arch-go/arch-go).

**What it validates**:

- Package dependencies follow clean architecture
- No circular dependencies
- Layer separation (libraries are independent, controller coordinates)

**Run**:

```bash
go test ./tests -run TestArchitecture
```

**Example failure**:

```
Architecture validation failed!
  Package: pkg/core/config
    - imports pkg/controller/events (forbidden)
```

**Fix**:

1. Remove forbidden import
2. Move shared types to appropriate location
3. Update `arch-go.yml` if rule is incorrect

### Integration Tests

Component-level tests against real Kubernetes and HAProxy.

**Location**: `tests/integration/`

**Framework**: [fixenv](https://github.com/rekby/fixenv) + [Kind](https://kind.sigs.k8s.io/)

**Run**:

```bash
make test-integration
```

**Features**:

- Shared Kind cluster across tests (fast)
- Test-scoped namespaces (isolated)
- Automatic cleanup (configurable)
- Real HAProxy instances

**See**: `tests/integration/README.md`

### Acceptance Tests

End-to-end regression tests for critical functionality.

**Location**: `tests/acceptance/`

**Framework**: [kubernetes-sigs/e2e-framework](https://github.com/kubernetes-sigs/e2e-framework) + Kind

**Run**:

```bash
make test-acceptance
```

**Features**:

- Full controller lifecycle testing
- Debug endpoint verification
- ConfigMap reload regression test
- Port-forward integration

**See**: `tests/acceptance/README.md`

## Running Tests

### Quick Feedback (Unit + Architecture)

```bash
make test
```

Runs in ~5-10 seconds. Includes:

- All package unit tests
- Architecture validation

Does NOT include integration or acceptance tests.

### Integration Tests

```bash
make test-integration
```

First run: ~2 minutes (creates cluster)
Subsequent runs: ~30 seconds (reuses cluster)

**Environment variables**:

```bash
# Always cleanup after tests
KEEP_CLUSTER=false make test-integration

# Use specific Kubernetes version
KIND_NODE_VERSION=v1.29.0 make test-integration
```

### Acceptance Tests

```bash
make test-acceptance
```

Duration: ~3-5 minutes (builds image, creates cluster, runs tests)

**Prerequisites**:

- Docker (for building controller image)
- Kind (installed automatically if missing)

### All Tests

```bash
make test-all
```

Runs everything:

- Unit tests
- Architecture tests
- Integration tests
- Acceptance tests

Duration: ~5-10 minutes

## Test Tags

Integration tests use build tags to prevent running by default:

```go
//go:build integration

package integration

func TestSyncFrontendAdd(t *testing.T) {
    // Integration test - only runs with -tags=integration
}
```

**Why?** Integration tests are slow and require external infrastructure.

**Run with tags**:

```bash
go test -tags=integration ./tests/integration/...
```

## Architecture Validation

Architecture rules are defined in `arch-go.yml` at project root:

```yaml
dependencies_rules:
  # Core packages are independent
  - package: "pkg/core"
    should_not_depend_on:
      - "pkg/controller"
      - "pkg/dataplane"
      # ...

  # Controller coordinates everything
  - package: "pkg/controller"
    may_depend_on:
      - "pkg/**"
```

**Test**: `architecture_test.go`

**When to update**:

- Adding new top-level package
- Changing package boundaries
- Refactoring package dependencies

## Common Commands

```bash
# Unit tests only (fast)
make test

# Integration tests (medium speed)
make test-integration

# Acceptance tests (slow)
make test-acceptance

# Everything
make test-all

# With coverage
make test-coverage

# Specific test
go test ./tests -run TestArchitecture
go test -tags=integration ./tests/integration -run TestSyncFrontendAdd

# Keep cluster for debugging
KEEP_CLUSTER=true go test -tags=integration ./tests/integration -run TestSyncFrontendAdd

# Cleanup manually
kind delete cluster --name=haproxy-test
```

## Debugging Tests

### Integration Tests

```bash
# Run specific test and keep cluster
KEEP_CLUSTER=true go test -tags=integration ./tests/integration -run TestSyncFrontendAdd -v

# Inspect cluster state
kubectl config use-context kind-haproxy-test
kubectl get pods -A
kubectl logs -n test-sync-frontend-add-xxx haproxy-xxx

# Cleanup when done
kind delete cluster --name=haproxy-test
```

### Acceptance Tests

```bash
# Run with verbose output
go test -v ./tests/acceptance -run TestConfigMapReload

# Access controller logs during test
kubectl logs -n haproxy-test haptic-xxx -f

# Access debug endpoint
kubectl port-forward -n haproxy-test pod/haptic-xxx 6060:6060
curl http://localhost:6060/debug/vars/config
```

## Test Organization Principles

1. **Unit tests** live with code (`pkg/*/`)
2. **Integration tests** live in `tests/integration/`
3. **Acceptance tests** live in `tests/acceptance/`
4. **Architecture tests** live in `tests/`

5. **Slow tests** use build tags
6. **Fast tests** run by default
7. **Test data** goes in `testdata/` subdirectories

## Adding New Tests

### Unit Test

```bash
# Create in same package as code
cat > pkg/mypackage/mycode_test.go <<EOF
package mypackage

import "testing"

func TestMyFunction(t *testing.T) {
    result := MyFunction()
    // assertions...
}
EOF
```

### Integration Test

```bash
# Create in tests/integration/
cat > tests/integration/myfeature_test.go <<EOF
//go:build integration

package integration

import "testing"

func TestMyFeature(t *testing.T) {
    env := fixenv.New(t)
    haproxy := TestHAProxy(env)
    // test against real HAProxy...
}
EOF
```

### Acceptance Test

```bash
# Create in tests/acceptance/
cat > tests/acceptance/myfeature_test.go <<EOF
package acceptance

import "testing"

func TestMyFeature(t *testing.T) {
    testEnv := Setup(t)

    feature := features.New("My Feature").
        Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
            // setup...
        }).
        Assess("Feature works", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
            // test...
        }).
        Feature()

    testEnv.Test(t, feature)
}
EOF
```

## Prerequisites

### For Unit Tests

- Go 1.23+

### For Integration Tests

- Go 1.23+
- Docker (for Kind)
- Kind (installed automatically)

### For Acceptance Tests

- Go 1.23+
- Docker (for Kind and building images)
- Kind (installed automatically)

## Troubleshooting

### "No tests found"

**Problem**: Running `go test ./tests/integration/` shows no tests.

**Cause**: Integration tests require `-tags=integration` flag.

**Fix**:

```bash
go test -tags=integration ./tests/integration/...
```

### Architecture test fails

**Problem**: `TestArchitecture` fails after adding import.

**Cause**: Import violates rules in `arch-go.yml`.

**Fix**:

1. Check error message for specific violation
2. Remove forbidden import or refactor
3. Update `arch-go.yml` if rule is incorrect

### Integration tests slow

**Problem**: Tests take 2+ minutes every run.

**Cause**: Cluster is recreated each run.

**Fix**: Cluster is kept by default. If forcing cleanup:

```bash
# Default - keeps cluster
make test-integration

# To force cleanup
KEEP_CLUSTER=false make test-integration
```

### Kind cluster issues

**Problem**: Kind cluster creation fails.

**Possible causes**:

- Docker not running
- Port conflicts (6443 already in use)
- Insufficient resources

**Fix**:

```bash
# Check Docker
docker ps

# Delete existing cluster
kind delete cluster --name=haproxy-test

# Try again
make test-integration
```

## CI Integration

Typical CI workflow:

```yaml
# .github/workflows/test.yml
jobs:
  unit:
    - run: make test

  integration:
    - run: make test-integration

  acceptance:
    - run: make test-acceptance
```

Tests run in parallel for faster CI.

## Resources

- Architecture validation: [arch-go](https://github.com/arch-go/arch-go)
- Integration testing: [fixenv](https://github.com/rekby/fixenv)
- Acceptance testing: [e2e-framework](https://github.com/kubernetes-sigs/e2e-framework)
- Kind: [kind.sigs.k8s.io](https://kind.sigs.k8s.io/)
- Integration tests: `tests/integration/README.md`
- Acceptance tests: `tests/acceptance/README.md`
