# pkg/controller/debug - Controller Debug Variables

Development context for controller-specific debug variable implementations.

**API Documentation**: See `pkg/controller/debug/README.md`

## When to Use This Package

Use this package when you need to:

- Expose controller internal state via debug HTTP endpoints
- Implement new debug variables for controller data
- Access controller state from tests or debugging tools
- Track recent events independently of EventCommentator

**DO NOT** use this package for:

- Generic debug infrastructure → Use `pkg/introspection`
- Event bus infrastructure → Use `pkg/events`
- Ring buffer implementation → Use `pkg/events/ringbuffer`
- Production APIs → Use proper REST API framework

## Package Purpose

Provides controller-specific implementations of the generic `pkg/introspection.Var` interface. This package bridges the gap between the controller's internal state and the debug HTTP server.

Key features:

- **StateProvider interface** - Abstracts controller state access
- **Debug variable implementations** - Config, credentials, rendered output, resources, events
- **EventBuffer** - Separate event tracking for debug purposes
- **Registration logic** - Centralized variable registration

## Architecture

```
Controller (pkg/controller)
    ↓ implements
StateProvider interface (pkg/controller/debug)
    ↓ used by
Debug Variables (ConfigVar, RenderedVar, etc.)
    ↓ registered with
Registry (pkg/introspection)
    ↓ served by
HTTP Server (pkg/introspection)
```

Flow:

1. Controller implements StateProvider by caching state from events
2. Debug variables call StateProvider methods to get current state
3. Variables are registered with introspection.Registry
4. HTTP server exposes variables via /debug/vars endpoints

## Key Types

### StateProvider

Interface for accessing controller state in a thread-safe manner:

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

**Implementation Pattern** (in pkg/controller):

```go
type StateCache struct {
    bus             *events.EventBus
    resourceWatcher *resourcewatcher.ResourceWatcherComponent
    mu              sync.RWMutex

    // Cached state
    currentConfig        *config.Config
    currentConfigVersion string
    lastRendered         string
    lastRenderedTime     time.Time
    // ...
}

func (sc *StateCache) GetConfig() (*config.Config, string, error) {
    sc.mu.RLock()
    defer sc.mu.RUnlock()

    if sc.currentConfig == nil {
        return nil, "", fmt.Errorf("config not loaded yet")
    }

    return sc.currentConfig, sc.currentConfigVersion, nil
}

// State is updated by subscribing to events:
func (sc *StateCache) handleEvent(event interface{}) {
    switch e := event.(type) {
    case *events.ConfigValidatedEvent:
        sc.mu.Lock()
        sc.currentConfig = e.Config
        sc.currentConfigVersion = e.Version
        sc.mu.Unlock()

    case *events.TemplateRenderedEvent:
        sc.mu.Lock()
        sc.lastRendered = e.Output
        sc.lastRenderedTime = time.Now()
        sc.mu.Unlock()
    }
}
```

### Debug Variables

Implementations of introspection.Var for controller-specific data:

**ConfigVar** - Current configuration:

```go
type ConfigVar struct {
    provider StateProvider
}

func (v *ConfigVar) Get() (interface{}, error) {
    cfg, version, err := v.provider.GetConfig()
    if err != nil {
        return nil, err
    }

    return map[string]interface{}{
        "config":  cfg,
        "version": version,
        "updated": time.Now(),
    }, nil
}
```

**CredentialsVar** - Credential metadata (NOT actual passwords):

```go
type CredentialsVar struct {
    provider StateProvider
}

func (v *CredentialsVar) Get() (interface{}, error) {
    creds, version, err := v.provider.GetCredentials()
    if err != nil {
        return nil, err
    }

    // Return metadata only - NEVER expose actual passwords
    return map[string]interface{}{
        "version":             version,
        "updated":             time.Now(),
        "has_dataplane_creds": creds != nil && creds.DataplaneUsername != "",
    }, nil
}
```

**RenderedVar** - Last rendered HAProxy config:

```go
type RenderedVar struct {
    provider StateProvider
}

func (v *RenderedVar) Get() (interface{}, error) {
    rendered, timestamp, err := v.provider.GetRenderedConfig()
    if err != nil {
        return nil, err
    }

    return map[string]interface{}{
        "config":    rendered,
        "timestamp": timestamp,
        "size":      len(rendered),
    }, nil
}
```

### EventBuffer

Separate event tracking for debug purposes:

```go
type EventBuffer struct {
    buffer *ringbuffer.RingBuffer[Event]
    bus    *events.EventBus
}

func NewEventBuffer(size int, bus *events.EventBus) *EventBuffer {
    return &EventBuffer{
        buffer: ringbuffer.New[Event](size),
        bus:    bus,
    }
}

func (eb *EventBuffer) Start(ctx context.Context) error {
    eventChan := eb.bus.Subscribe("debug-events", 1000)

    for {
        select {
        case event := <-eventChan:
            debugEvent := eb.convertEvent(event)
            eb.buffer.Add(debugEvent)

        case <-ctx.Done():
            return nil
        }
    }
}
```

**Why separate from EventCommentator?**

- EventCommentator is for logging and observability (domain-specific)
- EventBuffer is for debug endpoints (domain-agnostic simplified events)
- Avoids coupling debug infrastructure to logging component
- Different buffer sizes and retention policies

## Usage Patterns

### Controller Integration

In pkg/controller/controller.go:

```go
func (c *Controller) runIteration(ctx context.Context, debugPort int) error {
    // ... setup event bus, components ...

    // Create instance-based introspection registry
    registry := introspection.NewRegistry()

    // Create state cache implementing StateProvider
    stateCache := NewStateCache(bus, resourceWatcher)
    go stateCache.Start(ctx)

    // Create event buffer
    eventBuffer := debug.NewEventBuffer(1000, bus)
    go eventBuffer.Start(ctx)

    // Register all debug variables
    debug.RegisterVariables(registry, stateCache, eventBuffer)

    // Start debug HTTP server
    if debugPort > 0 {
        debugServer := introspection.NewServer(fmt.Sprintf(":%d", debugPort), registry)
        go debugServer.Start(ctx)
    }

    // ... continue with controller operation ...
}
```

### Accessing Debug Endpoints

```bash
# Get current config
curl http://localhost:8080/debug/vars/config

# Get just the version field
curl 'http://localhost:8080/debug/vars/config?field={.version}'

# Get rendered HAProxy config
curl http://localhost:8080/debug/vars/rendered

# Get resource counts
curl http://localhost:8080/debug/vars/resources

# Get recent events
curl http://localhost:8080/debug/vars/events

# Get full state dump (large!)
curl http://localhost:8080/debug/vars/state
```

### Accessing from Tests

```go
// tests/acceptance/debug_client.go
type DebugClient struct {
    podName      string
    debugPort    int
    restConfig   *rest.Config
}

func (dc *DebugClient) GetConfig(ctx context.Context) (map[string]interface{}, error) {
    // Sets up port-forward and makes HTTP request
    resp, err := http.Get(dc.buildURL("/debug/vars/config"))
    // ...
}

func (dc *DebugClient) WaitForConfigVersion(ctx context.Context, expectedVersion string, timeout time.Duration) error {
    // Polls /debug/vars/config?field={.version} until version matches
}

// In test:
debugClient := NewDebugClient(cfg.Client().RESTConfig(), pod, 6060)
debugClient.Start(ctx)

config, err := debugClient.GetConfig(ctx)
assert.Equal(t, "v1", config["version"])
```

## Integration with Other Packages

### Dependencies

```
pkg/controller/debug
    ├── pkg/introspection (Var interface, Registry)
    ├── pkg/events/ringbuffer (Event storage)
    ├── pkg/events (EventBus)
    ├── pkg/core/config (Config, Credentials types)
    └── pkg/dataplane (AuxiliaryFiles type)
```

### Usage

```
pkg/controller/debug (StateProvider, debug vars)
       ↑ used by
pkg/controller (implements StateProvider via StateCache)
       ↑ tested by
tests/acceptance/ (uses DebugClient to verify state)
```

## Common Pitfalls

### Exposing Sensitive Data

**Problem**: Accidentally exposing passwords, API keys, etc.

```go
// Bad - exposes actual password!
func (v *CredentialsVar) Get() (interface{}, error) {
    creds, _, _ := v.provider.GetCredentials()
    return creds, nil  // Contains password field!
}
```

**Solution**: Return metadata only.

```go
// Good - metadata only
func (v *CredentialsVar) Get() (interface{}, error) {
    creds, version, err := v.provider.GetCredentials()
    if err != nil {
        return nil, err
    }

    return map[string]interface{}{
        "version":             version,
        "has_dataplane_creds": creds.DataplanePassword != "",
        // DON'T include actual password
    }, nil
}
```

### Not Handling Nil State

**Problem**: Panics when state not yet loaded.

```go
// Bad - panics if config is nil
func (v *ConfigVar) Get() (interface{}, error) {
    cfg, _, _ := v.provider.GetConfig()
    return cfg.Templates, nil  // Panic if cfg is nil!
}
```

**Solution**: Check errors from StateProvider.

```go
// Good - handle errors
func (v *ConfigVar) Get() (interface{}, error) {
    cfg, version, err := v.provider.GetConfig()
    if err != nil {
        return nil, err  // Returns "config not loaded yet"
    }

    return map[string]interface{}{
        "config":  cfg,
        "version": version,
    }, nil
}
```

### StateProvider Not Thread-Safe

**Problem**: StateProvider implementation not using locks.

```go
// Bad - race condition
type StateCache struct {
    currentConfig *config.Config  // No lock!
}

func (sc *StateCache) GetConfig() (*config.Config, string, error) {
    return sc.currentConfig, "", nil  // Race!
}

func (sc *StateCache) handleEvent(e *events.ConfigValidatedEvent) {
    sc.currentConfig = e.Config  // Race!
}
```

**Solution**: Use RWMutex for state access.

```go
// Good - thread-safe
type StateCache struct {
    mu            sync.RWMutex
    currentConfig *config.Config
}

func (sc *StateCache) GetConfig() (*config.Config, string, error) {
    sc.mu.RLock()
    defer sc.mu.RUnlock()
    return sc.currentConfig, "", nil  // Safe
}

func (sc *StateCache) handleEvent(e *events.ConfigValidatedEvent) {
    sc.mu.Lock()
    defer sc.mu.Unlock()
    sc.currentConfig = e.Config  // Safe
}
```

### Forgetting to Start EventBuffer

**Problem**: EventBuffer not started, no events captured.

```go
// Bad - buffer created but not started
eventBuffer := debug.NewEventBuffer(1000, bus)
debug.RegisterVariables(registry, stateCache, eventBuffer)
// Events not being captured!
```

**Solution**: Start buffer goroutine.

```go
// Good - buffer started
eventBuffer := debug.NewEventBuffer(1000, bus)
go eventBuffer.Start(ctx)  // Start capturing events
debug.RegisterVariables(registry, stateCache, eventBuffer)
```

## Testing Approaches

### Testing Debug Variables

```go
func TestConfigVar_Get(t *testing.T) {
    // Create mock StateProvider
    provider := &MockStateProvider{
        config:        testConfig,
        configVersion: "v1",
    }

    configVar := &ConfigVar{provider: provider}

    // Get value
    value, err := configVar.Get()
    require.NoError(t, err)

    // Verify structure
    data := value.(map[string]interface{})
    assert.Equal(t, testConfig, data["config"])
    assert.Equal(t, "v1", data["version"])
}

func TestCredentialsVar_NoPasswordLeak(t *testing.T) {
    provider := &MockStateProvider{
        credentials: &config.Credentials{
            DataplaneUsername: "admin",
            DataplanePassword: "secret123",
        },
    }

    credVar := &CredentialsVar{provider: provider}

    value, err := credVar.Get()
    require.NoError(t, err)

    // Verify password is NOT in response
    data := value.(map[string]interface{})
    assert.NotContains(t, data, "password")
    assert.NotContains(t, fmt.Sprint(data), "secret123")
    assert.True(t, data["has_dataplane_creds"].(bool))
}
```

### Testing EventBuffer

```go
func TestEventBuffer(t *testing.T) {
    bus := events.NewEventBus(100)
    buffer := debug.NewEventBuffer(10, bus)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Start buffer
    go buffer.Start(ctx)
    bus.Start()

    // Publish test events
    bus.Publish(&events.ConfigParsedEvent{Version: "v1"})
    bus.Publish(&events.ConfigValidatedEvent{Version: "v1"})

    // Allow time for processing
    time.Sleep(100 * time.Millisecond)

    // Verify events captured
    events := buffer.GetAll()
    assert.GreaterOrEqual(t, len(events), 2)

    // Verify event structure
    assert.NotEmpty(t, events[0].Type)
    assert.NotEmpty(t, events[0].Summary)
    assert.NotZero(t, events[0].Timestamp)
}
```

### Integration Testing

See `tests/acceptance/configmap_reload_test.go` for examples of testing debug endpoints from outside the controller pod.

## Adding New Debug Variables

### Checklist

1. **Identify data source**: What StateProvider method provides this data?
2. **Define variable struct**: Implement introspection.Var interface
3. **Handle errors**: Return error if data not available yet
4. **Security check**: Don't expose sensitive data
5. **Register variable**: Add to RegisterVariables()
6. **Write tests**: Test Get() method with mock StateProvider
7. **Update README.md**: Document new variable

### Example: Adding ComponentStatusVar

```go
// Step 1: Add method to StateProvider interface
type StateProvider interface {
    // ... existing methods ...
    GetComponentStatus(component string) (*ComponentStatus, error)
}

// Step 2: Implement in StateCache (pkg/controller)
func (sc *StateCache) GetComponentStatus(component string) (*ComponentStatus, error) {
    sc.mu.RLock()
    defer sc.mu.RUnlock()

    status, exists := sc.componentStatus[component]
    if !exists {
        return nil, fmt.Errorf("component %s not found", component)
    }

    return status, nil
}

// Step 3: Create debug variable
type ComponentStatusVar struct {
    provider      StateProvider
    componentName string
}

func (v *ComponentStatusVar) Get() (interface{}, error) {
    status, err := v.provider.GetComponentStatus(v.componentName)
    if err != nil {
        return nil, err
    }

    return map[string]interface{}{
        "component":   v.componentName,
        "running":     status.Running,
        "last_seen":   status.LastSeen,
        "error_rate":  status.ErrorRate,
    }, nil
}

// Step 4: Register in RegisterVariables()
func RegisterVariables(registry *introspection.Registry, provider StateProvider, eventBuffer *EventBuffer) {
    // ... existing registrations ...

    // Register component status variables
    for _, component := range []string{"reconciler", "executor", "deployer"} {
        path := fmt.Sprintf("components/%s", component)
        registry.Publish(path, &ComponentStatusVar{
            provider:      provider,
            componentName: component,
        })
    }
}

// Step 5: Use it
// curl http://localhost:8080/debug/vars/components/reconciler
```

## Performance Characteristics

- **Variable Get()**: O(1) - just reads cached state (with RLock)
- **EventBuffer.Add()**: O(1) - ring buffer append
- **EventBuffer.GetLast(n)**: O(n) - copies n events
- **StateCache updates**: O(1) - event handler updates cached state

Memory:

- EventBuffer: O(buffer_size × event_size) - fixed (e.g., 1000 events × ~200 bytes ≈ 200KB)
- StateCache: O(state_size) - varies based on config/resources
- Debug variables: O(1) - just struct pointers

## Resources

- Generic introspection infrastructure: `pkg/introspection/CLAUDE.md`
- Ring buffer: `pkg/events/ringbuffer/CLAUDE.md`
- Controller integration: `pkg/controller/CLAUDE.md`
- Acceptance testing: `tests/acceptance/configmap_reload_test.go`
