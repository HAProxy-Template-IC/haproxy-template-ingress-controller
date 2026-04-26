# pkg/introspection

Generic HTTP server infrastructure for exposing internal application state via debug endpoints.

## Overview

The introspection package provides a reusable framework for creating debug HTTP servers with:

- Instance-based variable registry
- JSONPath field selection
- Built-in Go profiling (pprof)
- Graceful shutdown

This is a pure infrastructure package with no domain dependencies - it can be used in any Go application.

## Installation

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/introspection"
```

## Quick Start

```go
package main

import (
    "context"
    "time"
    "gitlab.com/haproxy-haptic/haptic/pkg/introspection"
)

func main() {
    // Create registry
    registry := introspection.NewRegistry()

    // Publish variables
    counter := &introspection.IntVar{}
    registry.Publish("requests", counter)

    startTime := time.Now()
    registry.Publish("uptime", introspection.Func(func() (any, error) {
        return time.Since(startTime).Seconds(), nil
    }))

    // Start HTTP server
    server := introspection.NewServer(":6060", registry)
    ctx := context.Background()
    go server.Start(ctx)

    // Access via:
    // curl http://localhost:6060/debug/vars
    // curl http://localhost:6060/debug/vars/uptime
    // curl http://localhost:6060/debug/pprof/
}
```

## API Reference

### Registry

```go
type Registry struct {
    // Thread-safe variable registry
}

func NewRegistry() *Registry
```

Creates a new instance-based registry. Each application iteration should create its own registry to avoid stale references.

```go
func (r *Registry) Publish(path string, v Var)
```

Registers a variable at the given path (e.g., "config", "metrics/requests").

```go
func (r *Registry) Get(path string) (any, error)
```

Retrieves a variable's value by path.

```go
func (r *Registry) GetWithField(path, field string) (any, error)
```

Retrieves a variable and extracts a specific field using JSONPath.

```go
func (r *Registry) Paths() []string
func (r *Registry) All() (map[string]any, error)
func (r *Registry) Len() int
func (r *Registry) Clear()
```

`Paths()` returns the sorted list of registered variable paths (used by the
`/debug/vars` index handler). `All()` returns a `path → value` map by calling
`Get()` on every registered variable; the first failure aborts and bubbles up.
`Len()` is the size of the registry. `Clear()` empties the registry without
tearing down the HTTP server — used between controller iterations so stale Vars
from a previous iteration get garbage-collected.

### Var Interface

```go
type Var interface {
    Get() (any, error)
}
```

Interface for debug variables. Implementations should be thread-safe and return JSON-serializable values.

### Built-in Variable Types

#### IntVar

```go
type IntVar struct {
    value atomic.Int64
}

func (v *IntVar) Add(delta int64)
func (v *IntVar) Set(value int64)
func (v *IntVar) Get() (any, error)
```

Thread-safe integer variable.

#### StringVar

```go
type StringVar struct {
    value atomic.Value  // stores string
}

func (v *StringVar) Set(value string)
func (v *StringVar) Get() (any, error)
```

Thread-safe string variable.

#### FloatVar

```go
type FloatVar struct {
    mu    sync.RWMutex
    value float64
}

func (v *FloatVar) Set(value float64)
func (v *FloatVar) Get() (any, error)
```

Thread-safe float64 variable.

#### MapVar

```go
type MapVar struct {
    mu   sync.RWMutex
    data map[string]any
}

func (v *MapVar) Set(key string, value any)
func (v *MapVar) Get() (any, error)
```

Thread-safe map variable.

#### Func

```go
type Func func() (any, error)

func (f Func) Get() (any, error)
```

Computed variable - value is calculated on-demand when requested.

Example:

```go
registry.Publish("uptime", introspection.Func(func() (any, error) {
    return map[string]any{
        "seconds": time.Since(startTime).Seconds(),
        "started": startTime,
    }, nil
}))
```

### Server

```go
type Server struct {
    addr     string
    registry *Registry
}

func NewServer(addr string, registry *Registry) *Server
```

Creates a new HTTP server bound to `addr` (e.g., ":6060"). Server binds to 0.0.0.0 for compatibility with kubectl port-forward.

```go
func (s *Server) Start(ctx context.Context) error
```

Starts the HTTP server. Blocks until context is cancelled. Performs graceful shutdown with 30s timeout.

Exposes endpoints:

- `GET /debug/vars` - List all variables
- `GET /debug/vars/{path}` - Get variable value
- `GET /debug/vars/{path}?field={.jsonpath}` - Extract specific field
- `GET /debug/pprof/*` - Go profiling (heap, goroutine, CPU, etc.)

### HTTP Helpers

```go
func WriteJSON(w http.ResponseWriter, data any)
```

Writes JSON response with proper content-type.

```go
func WriteJSONWithStatus(w http.ResponseWriter, statusCode int, data any)
```

Writes JSON response with an explicit HTTP status code (for handlers that need 4xx responses).

```go
func WriteError(w http.ResponseWriter, code int, message string)
```

Writes error response as JSON.

To narrow a payload to a single field, call `ExtractField` (below) and then `WriteJSON`. There is no combined `WriteJSONField` helper.

### JSONPath

```go
func ExtractField(data any, jsonPathExpr string) (any, error)
```

Extracts a field from data using JSONPath expression (kubectl syntax).

```go
func ParseFieldQuery(r *http.Request) string
```

Parses `?field={.path}` query parameter from HTTP request.

## HTTP Endpoints

### GET /debug/vars

Lists all registered variable paths.

**Response:**

```json
{
  "vars": ["config", "uptime", "metrics"]
}
```

### GET /debug/vars/{path}

Retrieves variable value.

**Examples:**

```bash
curl http://localhost:6060/debug/vars/uptime
```

**Response:**

```json
{
  "seconds": 123.45,
  "started": "2025-01-15T10:30:00Z"
}
```

### GET /debug/vars/{path}?field={.jsonpath}

Extracts specific field using JSONPath.

**Examples:**

```bash
# Get just the seconds
curl 'http://localhost:6060/debug/vars/uptime?field={.seconds}'
# Response: 123.45

# Get nested field
curl 'http://localhost:6060/debug/vars/config?field={.templates.main}'
```

**JSONPath Syntax:**

- `{.field}` - Top-level field
- `{.nested.field}` - Nested field
- `{.array[0]}` - Array element
- `{.array[*]}` - All array elements

See: <https://kubernetes.io/docs/reference/kubectl/jsonpath/>

### GET /debug/pprof/

Go profiling endpoints (automatically included):

- `/debug/pprof/` - Index
- `/debug/pprof/heap` - Memory allocations
- `/debug/pprof/goroutine` - Goroutine stacks
- `/debug/pprof/profile?seconds=30` - CPU profile
- `/debug/pprof/trace?seconds=5` - Execution trace

**Usage:**

```bash
# Interactive profiling
go tool pprof http://localhost:6060/debug/pprof/heap

# Save profile
curl http://localhost:6060/debug/pprof/profile?seconds=30 > cpu.prof
go tool pprof cpu.prof
```

## Custom Variable Implementation

Implement the `Var` interface for custom debug variables:

```go
type MyVar struct {
    data *MyData
    mu   sync.RWMutex
}

func (v *MyVar) Get() (any, error) {
    v.mu.RLock()
    defer v.mu.RUnlock()

    if v.data == nil {
        return nil, fmt.Errorf("data not loaded")
    }

    return map[string]any{
        "field1": v.data.Field1,
        "field2": v.data.Field2,
    }, nil
}

// Register
registry.Publish("myvar", &MyVar{data: myData})
```

## Security Considerations

1. **Bind Address**: Server binds to 0.0.0.0 by default. In Kubernetes pods, this is safe (private network). For other deployments, consider firewall rules.

2. **Sensitive Data**: Do NOT expose passwords, keys, or tokens. Return metadata only:

   ```go
   // Good
   return map[string]any{
       "has_password": creds.Password != "",
       "username": creds.Username,
   }

   // Bad
   return creds  // Exposes password!
   ```

3. **Access Control**: No built-in authentication. Use kubectl port-forward or reverse proxy with auth for production access.

4. **Performance**: `/debug/pprof/profile` can impact performance. Use with caution in production.

## Access via kubectl

For Kubernetes deployments:

```bash
# Forward debug port from pod
kubectl port-forward pod/my-app-xxx 6060:6060

# Access endpoints
curl http://localhost:6060/debug/vars
curl http://localhost:6060/debug/pprof/heap
```

## Examples

See:

- Controller integration: `pkg/controller/controller.go` (`persistentInfra.IntrospectionServer` — the registry and server are created once before the iteration loop and reused; `Registry.Clear()` is called at the top of each iteration so stale references from the previous run drop out)
- Debug variables: `pkg/controller/debug/`
- Acceptance tests: `tests/acceptance/debug_client.go`

## License

See main repository for license information.
