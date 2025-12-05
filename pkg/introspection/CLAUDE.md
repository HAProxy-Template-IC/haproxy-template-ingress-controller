# pkg/introspection - Debug HTTP Server Infrastructure

Development context for the introspection package.

**API Documentation**: See `pkg/introspection/README.md`

## When to Use This Package

Use this package when you need to:

- Expose internal application state via HTTP endpoints
- Create debug/introspection endpoints for production debugging
- Implement `/debug/vars` style introspection (similar to expvar)
- Support JSONPath field selection on debug variables
- Provide Go profiling endpoints (`/debug/pprof`)

**DO NOT** use this package for:

- Business logic → Use appropriate domain package
- Metrics collection → Use Prometheus/metrics package
- Production APIs → Use proper REST API framework

## Package Purpose

Provides a generic, reusable HTTP server infrastructure for exposing internal application state. This is a **pure infrastructure package** with no domain knowledge - it can be reused in any Go application.

Key features:

- Instance-based registry (not global like `expvar`)
- JSONPath field selection (kubectl-style syntax)
- HTTP handlers for listing and querying variables
- Go profiling integration (`pprof`)
- Graceful shutdown support

## Architecture

```
Registry (instance-based)
    ├── Var interface (extensible)
    │   ├── Simple types (IntVar, StringVar, MapVar)
    │   ├── Computed values (Func)
    │   └── Custom implementations
    ├── JSONPath field selection
    └── HTTP Server
        ├── GET /debug/vars (list all)
        ├── GET /debug/vars/{path} (get variable)
        ├── GET /debug/vars/{path}?field={.jsonpath}
        └── GET /debug/pprof/* (Go profiling)
```

## Key Types

### Registry

Instance-based registry for debug variables:

```go
type Registry struct {
    mu   sync.RWMutex
    vars map[string]Var
}

// Create new registry (lives with application lifecycle)
registry := introspection.NewRegistry()

// Publish variables
registry.Publish("config", &ConfigVar{...})
registry.Publish("uptime", introspection.Func(func() (interface{}, error) {
    return time.Since(startTime).Seconds(), nil
}))
```

**Why instance-based?** Allows registry to be garbage collected when application reinitializes, preventing stale references.

### Var Interface

Extensible interface for debug variables:

```go
type Var interface {
    Get() (interface{}, error)
}
```

Built-in implementations:

- `IntVar`, `StringVar`, `FloatVar`, `MapVar` - Simple atomic types
- `Func` - Computed on-demand values

### Server

HTTP server with graceful shutdown:

```go
server := introspection.NewServer(":6060", registry)
go server.Start(ctx)  // Starts HTTP server, blocks until ctx cancelled
```

Features:

- Binds to 0.0.0.0 (compatible with kubectl port-forward)
- Automatic pprof integration
- JSON responses
- Graceful shutdown on context cancellation

## Usage Patterns

### Basic Setup

```go
// Create instance-based registry
registry := introspection.NewRegistry()

// Publish simple variables
counter := &introspection.IntVar{}
counter.Add(1)
registry.Publish("request_count", counter)

// Publish computed variables
startTime := time.Now()
registry.Publish("uptime", introspection.Func(func() (interface{}, error) {
    return map[string]interface{}{
        "started": startTime,
        "uptime_seconds": time.Since(startTime).Seconds(),
    }, nil
}))

// Start HTTP server
server := introspection.NewServer(":6060", registry)
go server.Start(ctx)
```

### Custom Var Implementation

```go
type ConfigVar struct {
    config *Config
    mu     sync.RWMutex
}

func (v *ConfigVar) Get() (interface{}, error) {
    v.mu.RLock()
    defer v.mu.RUnlock()

    if v.config == nil {
        return nil, fmt.Errorf("config not loaded")
    }

    return map[string]interface{}{
        "version": v.config.Version,
        "templates": v.config.Templates,
    }, nil
}

// Publish
registry.Publish("config", &ConfigVar{config: cfg})
```

### JSONPath Field Selection

Clients can use JSONPath to extract specific fields:

```bash
# Get full variable
curl http://localhost:6060/debug/vars/config

# Extract specific field
curl 'http://localhost:6060/debug/vars/config?field={.version}'

# Extract nested field
curl 'http://localhost:6060/debug/vars/config?field={.templates.main}'
```

Uses `k8s.io/client-go/util/jsonpath` (same as kubectl).

### Application Reinitialization

For applications that reinitialize on config changes:

```go
func runIteration(ctx context.Context) {
    // Create NEW registry per iteration
    registry := introspection.NewRegistry()

    // Publish iteration-specific variables
    registry.Publish("config", getCurrentConfig())

    // Start server (or reuse existing server with new registry)
    server := introspection.NewServer(":6060", registry)
    go server.Start(ctx)

    // ... run iteration ...

    // When iteration ends, context cancels, server stops
    // Registry is garbage collected (no stale references!)
}
```

## Integration with Other Packages

This package is **pure infrastructure** - it has no dependencies on other application packages.

Other packages integrate by:

1. Implementing the `Var` interface
2. Registering variables with the registry
3. Starting the HTTP server

Example (controller integration):

```
pkg/introspection (infrastructure)
       ↑
pkg/controller/debug (domain-specific Var implementations)
       ↑
pkg/controller (registers variables, starts server)
```

## Common Pitfalls

### Using Global Registry

**Problem**: Global registry causes stale references during app reinitialization.

```go
// Bad - global registry
var globalRegistry = introspection.NewRegistry()

func reinitialize() {
    // Old variables still in globalRegistry!
    // Stale references to previous iteration
}
```

**Solution**: Create new registry per iteration.

```go
// Good - instance-based
func runIteration(ctx context.Context) {
    registry := introspection.NewRegistry()
    // Registry lives/dies with iteration
}
```

### Exposing Sensitive Data

**Problem**: Accidentally exposing passwords, keys, etc.

```go
// Bad - exposes actual credentials
type CredsVar struct {
    creds *Credentials
}
func (v *CredsVar) Get() (interface{}, error) {
    return v.creds, nil  // Exposes password!
}
```

**Solution**: Return metadata only.

```go
// Good - metadata only
func (v *CredsVar) Get() (interface{}, error) {
    return map[string]interface{}{
        "username": v.creds.Username,
        "has_password": v.creds.Password != "",
        // DON'T expose actual password
    }, nil
}
```

### Not Handling Nil Values

**Problem**: Panics when variable not initialized.

```go
// Bad - panics if config == nil
func (v *ConfigVar) Get() (interface{}, error) {
    return v.config.Version, nil  // Panic!
}
```

**Solution**: Check nil and return error.

```go
// Good - return error
func (v *ConfigVar) Get() (interface{}, error) {
    if v.config == nil {
        return nil, fmt.Errorf("config not loaded yet")
    }
    return v.config, nil
}
```

## Testing Approaches

### Unit Tests

Test Var implementations in isolation:

```go
func TestConfigVar_Get(t *testing.T) {
    // Test with loaded config
    configVar := &ConfigVar{config: testConfig}
    data, err := configVar.Get()
    require.NoError(t, err)
    assert.Equal(t, "v1", data.(map[string]interface{})["version"])

    // Test with nil config
    nilVar := &ConfigVar{config: nil}
    _, err = nilVar.Get()
    require.Error(t, err)
}
```

### Integration Tests

Test HTTP endpoints:

```go
func TestServer_GetVar(t *testing.T) {
    registry := introspection.NewRegistry()
    registry.Publish("test", introspection.Func(func() (interface{}, error) {
        return map[string]string{"key": "value"}, nil
    }))

    server := introspection.NewServer(":0", registry)  // Random port
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go server.Start(ctx)
    time.Sleep(100 * time.Millisecond)  // Wait for server start

    // Test GET /debug/vars/test
    resp, err := http.Get(fmt.Sprintf("http://localhost:%d/debug/vars/test", port))
    require.NoError(t, err)
    assert.Equal(t, http.StatusOK, resp.StatusCode)

    var data map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&data)
    assert.Equal(t, "value", data["key"])
}
```

## Resources

- JSONPath syntax: <https://kubernetes.io/docs/reference/kubectl/jsonpath/>
- Go pprof: <https://pkg.go.dev/net/http/pprof>
- expvar (stdlib inspiration): <https://pkg.go.dev/expvar>
- Controller integration: `pkg/controller/CLAUDE.md`
- Debug variables: `pkg/controller/debug/CLAUDE.md`
