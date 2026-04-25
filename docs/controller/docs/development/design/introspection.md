# Runtime Introspection and Debugging

The controller provides comprehensive runtime introspection capabilities through an HTTP debug server, enabling production debugging, operational visibility, and acceptance testing without relying solely on logs.

## Architecture Overview

```mermaid
graph TB
    subgraph "Controller Process"
        EB[EventBus]
        SC[StateCache<br/>Event-Driven State Tracking]
        EVB[EventBuffer<br/>Ring Buffer]

        subgraph "Debug Infrastructure"
            REG[Introspection Registry]
            HTTP[HTTP Debug Server<br/>Configurable Port]

            VARS[Debug Variables]
            CONFIG[ConfigVar]
            CREDS[CredentialsVar]
            REND[RenderedVar]
            AUX[AuxFilesVar]
            RES[ResourcesVar]
            EVENTS[EventsVar]
            STATE[FullStateVar]
            PIPE[PipelineVar / ValidatedVar / ErrorsVar]
        end
    end

    EB -->|Subscribe| SC
    EB -->|Subscribe| EVB
    SC -->|Implements| SP[StateProvider]
    SP -->|Used by| VARS
    EVB -->|Events History| EVENTS

    VARS --> CONFIG
    VARS --> CREDS
    VARS --> REND
    VARS --> AUX
    VARS --> RES
    VARS --> EVENTS
    VARS --> STATE
    VARS --> PIPE

    CONFIG --> REG
    CREDS --> REG
    REND --> REG
    AUX --> REG
    RES --> REG
    EVENTS --> REG
    STATE --> REG
    PIPE --> REG

    REG --> HTTP

    EXT[External Clients<br/>Tests, Debug Tools] -->|HTTP| HTTP

    style HTTP fill:#4CAF50
    style SC fill:#2196F3
    style EVB fill:#FF9800
    style REG fill:#9C27B0
```

## Key Components

**pkg/introspection** - Generic debug HTTP server infrastructure:

- Instance-based variable registry (not global like expvar)
- HTTP handlers for `/debug/vars` endpoints
- JSONPath field selection support (kubectl-style syntax)
- Go profiling integration (`/debug/pprof`)
- Graceful shutdown with context

**pkg/events/ringbuffer** - Event history storage:

- Thread-safe circular buffer using Go generics
- Fixed-size with automatic old-item eviction
- O(1) add, O(n) retrieval performance
- Used by both EventCommentator and EventBuffer

**pkg/controller/debug** - Controller-specific debug variables:

- Implements `introspection.Var` interface for controller data
- Core state vars: `ConfigVar`, `CredentialsVar` (metadata only), `RenderedVar`, `AuxFilesVar`, `ResourcesVar`
- Pipeline status vars (used by acceptance tests): `PipelineVar`, `ValidatedVar`, `ErrorsVar`
- `EventsVar` for the event-buffer view, `FullStateVar` for the catch-all `/debug/vars/state` payload
- `EventBuffer` for independent event tracking
- `StateProvider` interface for accessing controller state without coupling to specific event types

**StateCache** - Event-driven state tracking:

- Subscribes to validation, rendering, and resource events
- Maintains current state snapshot in memory
- Thread-safe RWMutex-protected access
- Implements StateProvider interface for debug endpoints
- Prevents need to query EventBus for historical state

## HTTP Endpoints

The debug server exposes controller state via HTTP. The port comes from the `--debug-port` flag or the `DEBUG_PORT` environment variable (the Helm chart sets both via the `controller.debugPort` value, defaulting to `8080`); set it to `0` to disable the server entirely.

```bash
# List all available variables
curl http://localhost:8080/debug/vars

# Get current configuration
curl http://localhost:8080/debug/vars/config

# Get just the config version using JSONPath
curl 'http://localhost:8080/debug/vars/config?field={.version}'

# Get rendered HAProxy configuration
curl http://localhost:8080/debug/vars/rendered

# Get resource counts
curl http://localhost:8080/debug/vars/resources

# Get recent events (last 1000)
curl http://localhost:8080/debug/vars/events

# Get recent 100 events
curl 'http://localhost:8080/debug/vars/events?field={.last_100}'

# Search events by correlation ID — separate /debug/events endpoint
curl 'http://localhost:8080/debug/events?correlation_id=<id>'
curl 'http://localhost:8080/debug/events?limit=500'

# Get complete state dump
curl http://localhost:8080/debug/vars/state

# Go profiling
curl http://localhost:8080/debug/pprof/
curl http://localhost:8080/debug/pprof/heap
curl http://localhost:8080/debug/pprof/goroutine
```

## Event History

Two independent event tracking mechanisms:

**EventCommentator** (observability):

- Subscribes to all events for domain-aware logging
- Ring buffer for event correlation in log messages
- Produces rich contextual log output
- Lives in pkg/controller/commentator

**EventBuffer** (debugging):

- Subscribes to all events for debug endpoint access
- Simplified event representation for HTTP API
- Exposes last N events via `/debug/vars/events`
- Lives in pkg/controller/debug

This separation allows different buffer sizes, retention policies, and use cases without coupling logging to debugging infrastructure.

## Integration with Acceptance Testing

The debug endpoints enable powerful acceptance testing:

```go
// tests/acceptance/debug_client.go
type DebugClient struct {
    podName   string
    debugPort int
}

// In test
func TestHAProxyTemplateConfigReload(t *testing.T) {
    // Create debug client with port-forward
    debugClient := NewDebugClient(cfg.RESTConfig(), "controller-pod", 8080)
    debugClient.Start(ctx)

    // Patch the HAProxyTemplateConfig CRD to a new template revision
    UpdateHAProxyTemplateConfig(ctx, "new-template")

    // Wait for controller to process change
    err := debugClient.WaitForConfigVersion(ctx, "v2", 30*time.Second)
    require.NoError(t, err)

    // Verify rendered config includes changes
    rendered, err := debugClient.GetRenderedConfig(ctx)
    require.NoError(t, err)
    assert.Contains(t, rendered, "expected-content")

    // Verify event history
    events, err := debugClient.GetEvents(ctx)
    require.NoError(t, err)
    assert.Contains(t, events, "config.validated")
}
```

This enables true end-to-end testing without parsing logs or relying on timing heuristics.

## Security Considerations

Debug variables implement careful filtering:

```go
// CredentialsVar returns metadata only
func (v *CredentialsVar) Get() (interface{}, error) {
    creds, version, err := v.provider.GetCredentials()
    if err != nil {
        return nil, err
    }

    return map[string]interface{}{
        "version":             version,
        "has_dataplane_creds": creds.DataplanePassword != "",
        // NEVER expose actual passwords
    }, nil
}
```

The debug server should be:

- Bound to localhost in production (kubectl port-forward for access)
- Protected by network policies
- Disabled or restricted in multi-tenant environments

## Configuration

The debug server is configured by the controller binary at startup, not via the `HAProxyTemplateConfig` CRD:

| Setting | Source | Notes |
|---------|--------|-------|
| Port | `--debug-port` flag, `DEBUG_PORT` env, or Helm `controller.debugPort` value | Default `0` = disabled; the chart sets it to `8080` by default |
| Bind address | Hardcoded `0.0.0.0:<port>` | So that `kubectl port-forward` can reach it |
| Event-buffer size | Compile-time constant (`pkg/controller/debug`) | Not tunable per-deployment |
| Go profiling | Always mounted at `/debug/pprof/*` when the debug port is enabled | See [Debugging Guide](../../operations/debugging.md#go-profiling) |

Disable the debug server entirely by setting `controller.debugPort: 0` in Helm values; the `/healthz` endpoint then moves to `controller.ports.healthz` (see [Security — Network Exposure](../../operations/security.md#network-exposure)).

For detailed implementation and API documentation, see:

- `pkg/introspection/README.md` - Generic debug HTTP server
- `pkg/events/ringbuffer/README.md` - Ring buffer implementation
- `pkg/controller/debug/README.md` - Controller-specific debug variables
