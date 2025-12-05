# pkg/controller/debug

Controller-specific debug variable implementations for introspection HTTP server.

## Overview

This package provides controller-specific implementations of the generic `pkg/introspection.Var` interface, exposing internal controller state via HTTP debug endpoints.

**Use cases:**

- Production debugging without logs
- Acceptance testing with state verification
- Operational visibility into controller internals
- Development troubleshooting

## Installation

```go
import "haproxy-template-ic/pkg/controller/debug"
```

## Quick Start

```go
package main

import (
    "context"
    "haproxy-template-ic/pkg/controller/debug"
    "haproxy-template-ic/pkg/introspection"
)

func main() {
    // Create introspection registry
    registry := introspection.NewRegistry()

    // Create state provider (controller implements this)
    stateCache := NewStateCache(bus, resourceWatcher)
    go stateCache.Start(ctx)

    // Create event buffer
    eventBuffer := debug.NewEventBuffer(1000, bus)
    go eventBuffer.Start(ctx)

    // Register all debug variables
    debug.RegisterVariables(registry, stateCache, eventBuffer)

    // Start HTTP server
    server := introspection.NewServer(":6060", registry)
    go server.Start(ctx)

    // Access via:
    // curl http://localhost:6060/debug/vars/config
    // curl http://localhost:6060/debug/vars/rendered
}
```

## API Reference

### StateProvider Interface

Interface for accessing controller internal state in a thread-safe manner.

```go
type StateProvider interface {
    GetConfig() (*config.Config, string, error)
    GetCredentials() (*config.Credentials, string, error)
    GetRenderedConfig() (string, time.Time, error)
    GetAuxiliaryFiles() (*dataplane.AuxiliaryFiles, time.Time, error)
    GetResourceCounts() (map[string]int, error)
    GetResourcesByType(resourceType string) ([]interface{}, error)
}
```

**Implementation**: Controller implements this via StateCache component that subscribes to events and caches state.

**Thread Safety**: All methods must be thread-safe (called from HTTP handlers).

#### GetConfig

```go
GetConfig() (*config.Config, string, error)
```

Returns the current validated configuration and its version (ConfigMap resource version).

**Returns**:

- `config`: The current Config struct
- `version`: ConfigMap resource version (e.g., "12345")
- `error`: Non-nil if config not loaded yet

**Example**:

```go
cfg, version, err := provider.GetConfig()
if err != nil {
    return nil, fmt.Errorf("config not ready: %w", err)
}
fmt.Printf("Config version: %s\n", version)
```

#### GetCredentials

```go
GetCredentials() (*config.Credentials, string, error)
```

Returns the current credentials and their version (Secret resource version).

**Security**: Debug variables should NOT expose actual credential values.

**Returns**:

- `creds`: Credentials struct
- `version`: Secret resource version
- `error`: Non-nil if credentials not loaded yet

#### GetRenderedConfig

```go
GetRenderedConfig() (string, time.Time, error)
```

Returns the most recently rendered HAProxy configuration and when it was rendered.

**Returns**:

- `config`: Rendered HAProxy config as string
- `timestamp`: When this config was rendered
- `error`: Non-nil if no config rendered yet

#### GetAuxiliaryFiles

```go
GetAuxiliaryFiles() (*dataplane.AuxiliaryFiles, time.Time, error)
```

Returns the most recently used auxiliary files (SSL certificates, map files, general files).

**Returns**:

- `auxFiles`: AuxiliaryFiles struct with SSL, maps, general files
- `timestamp`: When these files were last used
- `error`: Non-nil if no files cached yet

#### GetResourceCounts

```go
GetResourceCounts() (map[string]int, error)
```

Returns a map of resource type → count.

**Returns**:

```go
{
    "ingresses": 5,
    "services": 12,
    "haproxy-pods": 2
}
```

#### GetResourcesByType

```go
GetResourcesByType(resourceType string) ([]interface{}, error)
```

Returns all resources of a specific type.

**Parameters**:

- `resourceType`: Key from GetResourceCounts() (e.g., "ingresses")

**Returns**:

- `resources`: Slice of resource objects
- `error`: Non-nil if resource type not found

### Debug Variables

#### ConfigVar

Exposes current controller configuration.

```go
type ConfigVar struct {
    provider StateProvider
}
```

**Endpoint**: `GET /debug/vars/config`

**Response**:

```json
{
  "config": {
    "templates": {
      "main": "global\n  maxconn {{ maxconn }}\n..."
    },
    "watched_resources": [...]
  },
  "version": "12345",
  "updated": "2025-01-15T10:30:45Z"
}
```

**JSONPath Examples**:

```bash
# Get just the version
curl 'http://localhost:6060/debug/vars/config?field={.version}'

# Get template names
curl 'http://localhost:6060/debug/vars/config?field={.config.templates}'
```

#### CredentialsVar

Exposes credential metadata (NOT actual passwords).

```go
type CredentialsVar struct {
    provider StateProvider
}
```

**Endpoint**: `GET /debug/vars/credentials`

**Response**:

```json
{
  "version": "67890",
  "updated": "2025-01-15T10:30:45Z",
  "has_dataplane_creds": true
}
```

**Security**: Does NOT expose actual username/password values.

#### RenderedVar

Exposes most recently rendered HAProxy configuration.

```go
type RenderedVar struct {
    provider StateProvider
}
```

**Endpoint**: `GET /debug/vars/rendered`

**Response**:

```json
{
  "config": "global\n  maxconn 2000\n  log stdout local0\n\ndefaults\n...",
  "timestamp": "2025-01-15T10:30:45Z",
  "size": 4567
}
```

**Usage**:

```bash
# Get full rendered config
curl http://localhost:6060/debug/vars/rendered

# Get just the config text
curl 'http://localhost:6060/debug/vars/rendered?field={.config}'

# Get just the timestamp
curl 'http://localhost:6060/debug/vars/rendered?field={.timestamp}'
```

#### AuxFilesVar

Exposes auxiliary files used in last deployment.

```go
type AuxFilesVar struct {
    provider StateProvider
}
```

**Endpoint**: `GET /debug/vars/auxfiles`

**Response**:

```json
{
  "files": {
    "ssl_certificates": [
      {
        "name": "tls-cert",
        "content": "-----BEGIN CERTIFICATE-----\n...",
        "path": "/etc/haproxy/ssl/tls-cert.pem"
      }
    ],
    "map_files": [...],
    "general_files": [...]
  },
  "timestamp": "2025-01-15T10:30:45Z",
  "summary": {
    "ssl_count": 2,
    "map_count": 1,
    "general_count": 3
  }
}
```

#### ResourcesVar

Exposes resource counts by type.

```go
type ResourcesVar struct {
    provider StateProvider
}
```

**Endpoint**: `GET /debug/vars/resources`

**Response**:

```json
{
  "ingresses": 5,
  "services": 12,
  "haproxy-pods": 2
}
```

**Usage**:

```bash
# Get all resource counts
curl http://localhost:6060/debug/vars/resources

# Get specific resource type count
curl 'http://localhost:6060/debug/vars/resources?field={.ingresses}'
```

#### EventsVar

Exposes recent events from event buffer.

```go
type EventsVar struct {
    buffer       *EventBuffer
    defaultLimit int
}
```

**Endpoint**: `GET /debug/vars/events`

**Response** (array of recent events):

```json
[
  {
    "timestamp": "2025-01-15T10:30:45Z",
    "type": "config.validated",
    "summary": "config.validated"
  },
  {
    "timestamp": "2025-01-15T10:30:46Z",
    "type": "reconciliation.triggered",
    "summary": "reconciliation.triggered"
  }
]
```

**Default Limit**: 100 events (configurable via EventsVar.defaultLimit)

#### FullStateVar

Exposes all controller state in a single response.

```go
type FullStateVar struct {
    provider    StateProvider
    eventBuffer *EventBuffer
}
```

**Endpoint**: `GET /debug/vars/state`

**Warning**: Returns large response containing all state. Use with caution.

**Response**:

```json
{
  "config": {
    "config": {...},
    "version": "12345"
  },
  "rendered": {
    "config": "...",
    "timestamp": "2025-01-15T10:30:45Z"
  },
  "auxfiles": {
    "files": {...},
    "timestamp": "2025-01-15T10:30:45Z"
  },
  "resources": {
    "ingresses": 5,
    "services": 12
  },
  "recent_events": [...],
  "snapshot_time": "2025-01-15T10:31:00Z"
}
```

**Prefer**: Use specific variables or JSONPath field selection instead of full state dump.

### EventBuffer

Ring buffer for tracking recent events independently of EventCommentator.

```go
type EventBuffer struct {
    buffer *ringbuffer.RingBuffer[Event]
    bus    *events.EventBus
}

func NewEventBuffer(size int, bus *events.EventBus) *EventBuffer
```

Creates a new event buffer with the specified capacity.

**Parameters**:

- `size`: Maximum number of events to store (e.g., 1000)
- `bus`: EventBus to subscribe to

**Example**:

```go
eventBuffer := debug.NewEventBuffer(1000, bus)
go eventBuffer.Start(ctx)
```

#### Start

```go
func (eb *EventBuffer) Start(ctx context.Context) error
```

Starts collecting events from the EventBus. Blocks until context is cancelled.

**Usage**:

```go
go eventBuffer.Start(ctx)
```

#### GetLast

```go
func (eb *EventBuffer) GetLast(n int) []Event
```

Returns the last N events in chronological order (oldest first).

**Example**:

```go
recent := eventBuffer.GetLast(100)
for _, event := range recent {
    fmt.Printf("%s: %s\n", event.Timestamp, event.Type)
}
```

#### GetAll

```go
func (eb *EventBuffer) GetAll() []Event
```

Returns all events currently in the buffer.

#### Len

```go
func (eb *EventBuffer) Len() int
```

Returns the current number of events in the buffer.

### Event Type

Simplified event representation for debug purposes.

```go
type Event struct {
    Timestamp time.Time   `json:"timestamp"`
    Type      string      `json:"type"`
    Summary   string      `json:"summary"`
    Details   interface{} `json:"details,omitempty"`
}
```

**Fields**:

- `Timestamp`: When the event occurred
- `Type`: Event type string (e.g., "config.validated")
- `Summary`: Human-readable summary
- `Details`: Optional additional information (currently nil)

### RegisterVariables

```go
func RegisterVariables(
    registry *introspection.Registry,
    provider StateProvider,
    eventBuffer *EventBuffer,
)
```

Registers all controller debug variables with the introspection registry.

**Registered Variables**:

- `config`: ConfigVar
- `credentials`: CredentialsVar
- `rendered`: RenderedVar
- `auxfiles`: AuxFilesVar
- `resources`: ResourcesVar
- `events`: EventsVar (last 100 events)
- `state`: FullStateVar (full dump)
- `uptime`: Func (controller uptime)

**Example**:

```go
registry := introspection.NewRegistry()
eventBuffer := debug.NewEventBuffer(1000, bus)
go eventBuffer.Start(ctx)

debug.RegisterVariables(registry, stateCache, eventBuffer)

server := introspection.NewServer(":6060", registry)
go server.Start(ctx)
```

## HTTP Endpoints

### List All Variables

```
GET /debug/vars
```

Lists all registered debug variable paths.

**Response**:

```json
{
  "vars": [
    "config",
    "credentials",
    "rendered",
    "auxfiles",
    "resources",
    "events",
    "state",
    "uptime"
  ]
}
```

### Get Variable

```
GET /debug/vars/{path}
```

Retrieves a specific variable's value.

**Examples**:

```bash
curl http://localhost:6060/debug/vars/config
curl http://localhost:6060/debug/vars/rendered
curl http://localhost:6060/debug/vars/resources
```

### Get Variable Field

```
GET /debug/vars/{path}?field={.jsonpath}
```

Extracts a specific field using JSONPath (kubectl syntax).

**Examples**:

```bash
# Get config version
curl 'http://localhost:6060/debug/vars/config?field={.version}'

# Get rendered config size
curl 'http://localhost:6060/debug/vars/rendered?field={.size}'

# Get ingress count
curl 'http://localhost:6060/debug/vars/resources?field={.ingresses}'
```

**JSONPath Syntax**: See <https://kubernetes.io/docs/reference/kubectl/jsonpath/>

## Access from Kubernetes

For controller running in Kubernetes pod:

```bash
# Forward debug port from pod
kubectl port-forward -n haproxy-test pod/haproxy-template-ic-xxx 6060:6060

# Access endpoints
curl http://localhost:6060/debug/vars/config
curl http://localhost:6060/debug/vars/rendered
curl http://localhost:6060/debug/pprof/heap  # Go profiling
```

## Security Considerations

1. **Credentials**: CredentialsVar exposes metadata only, NOT actual passwords
2. **Bind Address**: Server binds to 0.0.0.0 (all interfaces) for kubectl port-forward compatibility
3. **Access Control**: No built-in authentication - use kubectl port-forward or reverse proxy
4. **Large Responses**: FullStateVar can return very large responses - use with caution

**Best Practices**:

- Use kubectl port-forward for production access
- Don't expose debug port directly to internet
- Prefer specific variables over full state dump
- Use JSONPath field selection to reduce response size

## Examples

### Controller Integration

See `pkg/controller/controller.go`:

```go
// Stage 6: Debug infrastructure
registry := introspection.NewRegistry()
stateCache := NewStateCache(bus, resourceWatcher)
go stateCache.Start(ctx)

eventBuffer := debug.NewEventBuffer(1000, bus)
go eventBuffer.Start(ctx)

debug.RegisterVariables(registry, stateCache, eventBuffer)

if debugPort > 0 {
    server := introspection.NewServer(fmt.Sprintf(":%d", debugPort), registry)
    go server.Start(ctx)
}
```

### Acceptance Testing

See `tests/acceptance/debug_client.go`:

```go
// Create debug client
debugClient := NewDebugClient(cfg.Client().RESTConfig(), pod, 6060)
debugClient.Start(ctx)

// Get current config
config, err := debugClient.GetConfig(ctx)
require.NoError(t, err)

// Wait for specific config version
err = debugClient.WaitForConfigVersion(ctx, "v2", 30*time.Second)
require.NoError(t, err)

// Verify rendered config contains expected values
rendered, err := debugClient.GetRenderedConfig(ctx)
require.NoError(t, err)
assert.Contains(t, rendered, "maxconn 4000")
```

## License

See main repository for license information.
