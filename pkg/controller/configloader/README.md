# pkg/controller/configloader

ConfigLoader component - parses ConfigMap data into configuration structures.

## Overview

Event-driven component that subscribes to ConfigResourceChangedEvent, extracts YAML configuration from ConfigMap resources, and publishes ConfigParsedEvent for valid configurations.

**Part of**: Stage 1 (Config Management) in controller lifecycle

## Installation

```go
import "haptic/pkg/controller/configloader"
```

## Quick Start

```go
import (
    "haptic/pkg/controller/configloader"
    "haptic/pkg/events"
)

// Create component
loader := configloader.NewConfigLoaderComponent(bus, logger)

// Start component (blocks until context cancelled)
go loader.Start(ctx)

// Component automatically processes ConfigResourceChangedEvent
// and publishes ConfigParsedEvent
```

## API

### NewConfigLoaderComponent

```go
func NewConfigLoaderComponent(bus *busevents.EventBus, logger *slog.Logger) *ConfigLoaderComponent
```

Creates a new ConfigLoader component.

**Parameters**:

- `bus`: EventBus for subscribing and publishing
- `logger`: Structured logger for diagnostics

**Returns**: ConfigLoaderComponent ready to start

### Start

```go
func (c *ConfigLoaderComponent) Start(ctx context.Context) error
```

Starts the component's event loop. Blocks until context is cancelled. Returns nil on graceful shutdown.

**Process**:

1. Subscribes to EventBus
2. Waits for ConfigResourceChangedEvent
3. Extracts ConfigMap data["config"]
4. Parses YAML using config.ParseConfig
5. Publishes ConfigParsedEvent on success

### Stop

```go
func (c *ConfigLoaderComponent) Stop()
```

Gracefully stops the component.

## Events

### Subscribes To

**ConfigResourceChangedEvent**:

```go
type ConfigResourceChangedEvent struct {
    Resource interface{}  // *unstructured.Unstructured (ConfigMap)
}
```

Published by ConfigChange watcher when ConfigMap changes.

### Publishes

**ConfigParsedEvent** (on success):

```go
type ConfigParsedEvent struct {
    Config        *config.Config
    ConfigVersion string  // ConfigMap resourceVersion
    SecretVersion string  // Empty at this stage
}
```

Published when YAML successfully parsed into config.Config.

## ConfigMap Format

Expected ConfigMap structure:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: haproxy-config
  namespace: default
data:
  config: |
    templates:
      main: |
        global
          maxconn 2000
        defaults
          mode http
    watched_resources:
      - name: ingresses
        group: networking.k8s.io
        version: v1
        resource: ingresses
```

**Required**:

- `data.config`: YAML configuration string

## Error Handling

Component logs errors but does not publish events for failures:

**Missing "config" key**:

```
Error: ConfigMap data missing 'config' key
```

**Invalid YAML**:

```
Error: Failed to parse configuration YAML
```

**Invalid resource type**:

```
Error: ConfigResourceChangedEvent contains invalid resource type
```

**No data field**:

```
Error: ConfigMap has no data field
```

## Example Usage

### Basic Setup

```go
func main() {
    bus := events.NewEventBus(100)
    logger := slog.Default()

    loader := configloader.NewConfigLoaderComponent(bus, logger)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go loader.Start(ctx)

    // Wait for ConfigParsedEvent
    eventChan := bus.Subscribe(10)
    for event := range eventChan {
        if parsed, ok := event.(*events.ConfigParsedEvent); ok {
            fmt.Printf("Config loaded: version %s\n", parsed.ConfigVersion)
        }
    }
}
```

## Integration

Controller creates ConfigLoader in Stage 1:

```go
// Stage 1: Config Management
configLoader := configloader.NewConfigLoaderComponent(bus, logger)
go configLoader.Start(ctx)
```

Component runs for entire controller lifecycle, processing ConfigMap updates.

## License

See main repository for license information.
