# Runtime Introspection and Debugging

The controller exposes its live state — config, rendered output, resources, events — over an HTTP debug server, so you can debug production issues and drive acceptance tests without parsing logs.

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

The debug server exposes controller state via HTTP. The port comes from the `--debug-port` flag or the `DEBUG_PORT` environment variable (the Helm chart sets both via the `controller.debugPort` value, defaulting to `8080`; `/healthz` shares the same listener, so setting the port to `0` breaks Kubernetes probes). The endpoint reference — every `/debug/vars/*` path, JSONPath field selection, `/debug/events` correlation-ID search, and `pprof` usage — lives in the [Debugging Guide](../../operations/debugging.md).

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

Acceptance tests drive the controller and assert on its state through these endpoints. `tests/acceptance/debug_client.go` provides a `*DebugClient` that talks to the controller via the Kubernetes API server's service-proxy, so tests don't need to manage `kubectl port-forward` themselves:

```go
import "gitlab.com/haproxy-haptic/haptic/tests/acceptance"

// Most tests use the helper that waits for the pod and the debug service
// endpoints before constructing the client (handles pod restarts cleanly).
debugClient, err := acceptance.EnsureDebugClientReady(
    ctx, t, client, clientset, namespace, 30*time.Second,
)
require.NoError(t, err)

// Patch the HAProxyTemplateConfig CRD via the dynamic client (real tests
// use the t.Update / t.Patch helpers from sigs.k8s.io/e2e-framework).
patchHAProxyTemplateConfig(ctx, /* ... */)

// Wait for the controller to roll over to the new spec.
err = debugClient.WaitForConfigVersion(ctx, "<new resourceVersion>", 30*time.Second)
require.NoError(t, err)

// Inspect the rendered config (with retry while the new revision propagates).
rendered, err := debugClient.GetRenderedConfigWithRetry(ctx, 30*time.Second)
require.NoError(t, err)
assert.Contains(t, rendered, "expected-content")
```

`DebugClient` also exposes `GetConfig`, `GetPipelineStatus`, `GetErrors`, and `GetAuxiliaryFiles`. To inspect the recent-events buffer, fetch the `/debug/vars/events` endpoint directly (there is no typed `GetEvents` helper).

If you need to construct the client yourself (typically only inside `EnsureDebugClientReady`), the constructor takes the clientset, the namespace, the *service* name (not a pod name), and the port:

```go
client := acceptance.NewDebugClient(clientset, namespace, serviceName, acceptance.DebugPort)
```

Tests observe controller state directly — no log parsing, no timing heuristics.

## Security and Configuration

Two design constraints matter here; everything operational about them lives elsewhere:

- **Debug variables never expose secret material.** Credential variables return metadata only (`version`, `has_dataplane_creds`) — `pkg/controller/debug/setup.go` enforces this. Access control and NetworkPolicy examples: [Security — Network Exposure](../../operations/security.md#network-exposure).
- **The server binds `0.0.0.0:<port>` deliberately**, so kubelet health probes and the Kubernetes API service-proxy (used by acceptance tests) can reach it on the pod IP; restrict access via NetworkPolicy, not bind-address filtering. Port configuration, the shared `/healthz` listener, and the probe-breaking caveat of `controller.debugPort: 0`: [Debugging — Accessing the Server](../../operations/debugging.md#accessing-the-server).

For detailed implementation and API documentation, see:

- `pkg/introspection/README.md` - Generic debug HTTP server
- `pkg/events/ringbuffer/README.md` - Ring buffer implementation
- `pkg/controller/debug/README.md` - Controller-specific debug variables
