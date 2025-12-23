# pkg/core - Core Functionality

Development context for core shared functionality.

**API Documentation**: See `pkg/core/README.md`
**Architecture**: See `/docs/development/design.md` (Core Packages section)

## When to Work Here

Modify this package when:

- Extending configuration schema
- Adding new validation rules
- Changing credential handling
- Modifying logging setup
- Adding shared primitive types

**DO NOT** modify this package for:

- Event coordination → Use `pkg/controller`
- Template rendering → Use `pkg/templating`
- Kubernetes integration → Use `pkg/k8s`
- HAProxy sync → Use `pkg/dataplane`

## Package Structure

```
pkg/core/
├── config/         # Configuration types, parsing, and validation
└── logging/        # Structured logging setup
```

## Key Design Principle

This package provides **shared primitives** with minimal dependencies. It defines types and functions used across the codebase without importing other pkg/ packages (except standard library).

Dependencies: Only standard library (encoding/json, log/slog, etc.)

## Sub-Packages

### config/ - Configuration Management

Defines configuration types and provides parsing functions:

```go
// Parse configuration from ConfigMap data
config, err := config.ParseConfig(configMapData)

// Load credentials from Secret data
creds, err := config.LoadCredentials(secretData)
```

**Responsibilities:**

- Define Config struct and all nested types
- Parse YAML configuration
- Basic structural validation (required fields, port ranges)
- Credentials loading and validation
- NOT: Template validation (done in pkg/controller/validators)
- NOT: JSONPath validation (done in pkg/controller/validators)
- NOT: Watching ConfigMap/Secret (done in pkg/k8s)

### logging/ - Structured Logging

Sets up structured logging with slog:

```go
// Initialize logger
logger := logging.New(logging.Config{
    Level:  slog.LevelInfo,
    Format: logging.FormatJSON,
})

slog.SetDefault(logger)

// Use throughout application
slog.Info("controller started",
    "namespace", namespace,
    "watched_resources", len(watchedResources))
```

## Configuration Schema

### Core Types

```go
// Main configuration
type Config struct {
    WatchedResources map[string]WatchedResource
    HAProxyConfig    HAProxyConfigSpec
    TemplateSnippets map[string]TemplateSnippet
    Maps             map[string]MapDefinition
    Files            map[string]FileDefinition
    DataplaneAPI     DataplaneAPIConfig
}

// Watched resource definition
type WatchedResource struct {
    APIVersion    string
    Kind          string
    Namespace     string
    LabelSelector string
    IndexBy       []string
    StoreType     string
}

// HAProxy configuration
type HAProxyConfigSpec struct {
    Template string
}

// Template snippets
type TemplateSnippet struct {
    Template string
}

// Map definitions
type MapDefinition struct {
    Template string
}

// File definitions
type FileDefinition struct {
    Template string
    Path     string
}

// Dataplane API configuration
type DataplaneAPIConfig struct {
    DiscoveryMode string
    StaticURLs    []string
    PodSelector   map[string]string
}
```

### Validation Layers

**Basic Validation (pkg/core/config):**

- Required fields present
- Port numbers in valid range (1-65535)
- Enum values are valid (e.g., StoreType = "memory" or "cached")
- Non-empty credentials

**Advanced Validation (pkg/controller/validators):**

- Template syntax validation
- JSONPath expression validation
- Cross-field validation
- Business rule validation

## Testing Approach

### Test Parsing and Basic Validation

```go
func TestParseConfig_Valid(t *testing.T) {
    configYAML := `
watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    index_by:
      - metadata.namespace
      - metadata.name

haproxy_config:
  template: |
    global
        daemon
    `

    configMapData := map[string][]byte{
        "config.yaml": []byte(configYAML),
    }

    config, err := config.ParseConfig(configMapData)

    require.NoError(t, err)
    assert.Len(t, config.WatchedResources, 1)
    assert.Equal(t, "Ingress", config.WatchedResources["ingresses"].Kind)
}

func TestParseConfig_InvalidPortRange(t *testing.T) {
    configYAML := `
dataplane_api:
  static_urls:
    - "http://haproxy:99999"  # Invalid port
    `

    configMapData := map[string][]byte{
        "config.yaml": []byte(configYAML),
    }

    _, err := config.ParseConfig(configMapData)

    require.Error(t, err)
    assert.Contains(t, err.Error(), "invalid port")
}
```

### Test Credentials Loading

```go
func TestLoadCredentials_Valid(t *testing.T) {
    secretData := map[string][]byte{
        "dataplane_username":   []byte("admin"),
        "dataplane_password":   []byte("secret123"),
        "validation_username":  []byte("validator"),
        "validation_password":  []byte("valpass"),
    }

    creds, err := config.LoadCredentials(secretData)

    require.NoError(t, err)
    assert.Equal(t, "admin", creds.DataplaneUsername)
    assert.Equal(t, "secret123", creds.DataplanePassword)
}

func TestLoadCredentials_MissingRequired(t *testing.T) {
    secretData := map[string][]byte{
        "dataplane_username": []byte("admin"),
        // Missing other required fields
    }

    _, err := config.LoadCredentials(secretData)

    require.Error(t, err)
    assert.Contains(t, err.Error(), "required")
}
```

## Common Pitfalls

### Adding Business Logic to Config Package

**Problem**: Config package contains validation logic that depends on other packages.

```go
// Bad - config package importing other packages
package config

import "haptic/pkg/templating"

func (c *Config) ValidateTemplates() error {
    engine, err := templating.New(...)  // DON'T DO THIS
    // ...
}
```

**Solution**: Keep validation in controller/validators.

```go
// Good - config package stays pure
package config

func (c *Config) ValidateStructure() error {
    // Only check structure, not semantics
    if c.HAProxyConfig.Template == "" {
        return errors.New("template is required")
    }
    return nil
}

// Advanced validation in pkg/controller/validators
package validators

func ValidateTemplates(cfg config.Config) error {
    engine, err := templating.New(...)
    // ...
}
```

### Not Using Structured Validation Errors

**Problem**: Generic error messages without context.

```go
// Bad - unclear what's invalid
func ValidateConfig(cfg Config) error {
    if cfg.HAProxyConfig.Template == "" {
        return errors.New("invalid config")
    }
    return nil
}
```

**Solution**: Provide context in errors.

```go
// Good - clear what field is invalid
func ValidateConfig(cfg Config) error {
    if cfg.HAProxyConfig.Template == "" {
        return fmt.Errorf("haproxy_config.template is required")
    }

    for name, res := range cfg.WatchedResources {
        if res.Kind == "" {
            return fmt.Errorf("watched_resources.%s.kind is required", name)
        }
        if res.APIVersion == "" {
            return fmt.Errorf("watched_resources.%s.api_version is required", name)
        }
    }

    return nil
}
```

### Hardcoding Configuration Defaults

**Problem**: Defaults scattered throughout codebase.

```go
// Bad - default in multiple places
func createWatcher(cfg WatchedResource) {
    debounce := 500 * time.Millisecond  // Default hardcoded here
    // ...
}

func anotherPlace(cfg WatchedResource) {
    debounce := 300 * time.Millisecond  // Different default!
    // ...
}
```

**Solution**: Define defaults in config package.

```go
// Good - centralized defaults
package config

const (
    DefaultDebounceInterval = 500 * time.Millisecond
    DefaultStoreType        = "memory"
)

func (r *WatchedResource) GetDebounceInterval() time.Duration {
    if r.DebounceInterval != "" {
        duration, _ := time.ParseDuration(r.DebounceInterval)
        return duration
    }
    return DefaultDebounceInterval
}
```

## Extending Configuration Schema

### Checklist

1. Add field to Config struct (or nested struct)
2. Add YAML tag for unmarshaling
3. Add basic validation
4. Add default value (if applicable)
5. Update Config parsing
6. Add tests for new field
7. Update documentation
8. Consider backward compatibility

### Example: Adding Reconciliation Interval

```go
// Step 1: Add field to Config
type Config struct {
    // ... existing fields ...

    ReconciliationInterval string `yaml:"reconciliation_interval"`
}

// Step 2: Add validation
func (c *Config) Validate() error {
    // ... existing validation ...

    if c.ReconciliationInterval != "" {
        if _, err := time.ParseDuration(c.ReconciliationInterval); err != nil {
            return fmt.Errorf("invalid reconciliation_interval: %w", err)
        }
    }

    return nil
}

// Step 3: Add default
const DefaultReconciliationInterval = 5 * time.Minute

func (c *Config) GetReconciliationInterval() time.Duration {
    if c.ReconciliationInterval != "" {
        duration, _ := time.ParseDuration(c.ReconciliationInterval)
        return duration
    }
    return DefaultReconciliationInterval
}

// Step 4: Add tests
func TestConfig_GetReconciliationInterval(t *testing.T) {
    tests := []struct {
        name   string
        config Config
        want   time.Duration
    }{
        {
            name:   "default",
            config: Config{},
            want:   5 * time.Minute,
        },
        {
            name:   "custom",
            config: Config{ReconciliationInterval: "10m"},
            want:   10 * time.Minute,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := tt.config.GetReconciliationInterval()
            assert.Equal(t, tt.want, got)
        })
    }
}
```

## Credentials Security

### Best Practices

**DO:**

- Load credentials from Kubernetes Secret
- Validate all required fields are present
- Use TLS for Dataplane API connections
- Rotate credentials regularly

**DON'T:**

- Log credentials
- Store credentials in ConfigMap
- Hardcode credentials
- Pass credentials as environment variables (use Secret instead)

### Handling Credentials

```go
// Good - secure credential handling
type Credentials struct {
    DataplaneUsername   string
    DataplanePassword   string
    ValidationUsername  string
    ValidationPassword  string
}

// No String() method - prevents accidental logging

// Redact in logs
func (c Credentials) Redacted() map[string]string {
    return map[string]string{
        "dataplane_username":  c.DataplaneUsername,
        "dataplane_password":  "***REDACTED***",
        "validation_username": c.ValidationUsername,
        "validation_password": "***REDACTED***",
    }
}

// Usage
slog.Info("credentials loaded", "creds", creds.Redacted())
```

## Logging Standards

### Log Levels

```go
// Debug - verbose diagnostic information
slog.Debug("Resource indexed",
    "resource", resourceName,
    "keys", indexKeys)

// Info - general operational information
slog.Info("Reconciliation started",
    "trigger", trigger,
    "duration_ms", duration)

// Warn - non-critical issues
slog.Warn("Retry attempt",
    "attempt", attempt,
    "max_attempts", maxAttempts)

// Error - error conditions
slog.Error("Sync failed",
    "endpoint", endpoint,
    "error", err)
```

### Message Capitalization

Log messages must start with a capital letter:

```go
// Good - capitalized
slog.Info("Reconciliation started", "trigger", trigger)
slog.Error("Failed to sync configuration", "error", err)

// Bad - lowercase
slog.Info("reconciliation started", "trigger", trigger)
slog.Error("failed to sync configuration", "error", err)
```

### Structured Attributes

```go
// Good - structured key-value pairs
slog.Info("template rendered",
    "template", templateName,
    "size_bytes", len(output),
    "duration_ms", duration.Milliseconds())

// Bad - unstructured string formatting
slog.Info(fmt.Sprintf("Rendered template %s (%d bytes) in %dms",
    templateName, len(output), duration.Milliseconds()))
```

### Context Logger

```go
// Create logger with context
logger := slog.Default().With(
    "component", "reconciler",
    "namespace", namespace,
)

// All logs from this logger include context
logger.Info("starting reconciliation")  // Includes component=reconciler
logger.Error("reconciliation failed")   // Includes component=reconciler
```

## Configuration Versioning

### Forward Compatibility

When adding new fields, consider backward compatibility:

```go
// Good - optional new field with default
type Config struct {
    // Existing fields
    HAProxyConfig HAProxyConfigSpec

    // New optional field (v1.1.0+)
    NewFeature *NewFeatureConfig `yaml:"new_feature,omitempty"`
}

// Provide sensible default
func (c *Config) GetNewFeature() NewFeatureConfig {
    if c.NewFeature != nil {
        return *c.NewFeature
    }
    return NewFeatureConfig{
        Enabled: false,  // Safe default
    }
}
```

### Breaking Changes

If you must make breaking changes:

1. Document in changelog
2. Provide migration guide
3. Consider version check:

```go
type Config struct {
    Version string `yaml:"version"`  // e.g., "v1", "v2"
    // ...
}

func ParseConfig(data map[string][]byte) (*Config, error) {
    config := &Config{}
    // ... parse ...

    if config.Version != "" && config.Version != "v2" {
        return nil, fmt.Errorf("unsupported config version %s, expected v2", config.Version)
    }

    return config, nil
}
```

## Troubleshooting

### Configuration Not Loading

**Diagnosis:**

1. Check ConfigMap exists and has correct name
2. Verify YAML syntax
3. Check for required fields
4. Review parsing errors

```bash
# Verify ConfigMap
kubectl get configmap haptic-config -o yaml

# Check controller logs
kubectl logs deployment/haptic-controller | grep "config"
```

### Credentials Not Loading

**Diagnosis:**

1. Check Secret exists
2. Verify all required keys present
3. Check for empty values
4. Review controller RBAC permissions

```bash
# Verify Secret exists (don't print values)
kubectl get secret haptic-credentials

# Check Secret keys
kubectl get secret haptic-credentials -o json | jq '.data | keys'

# Verify RBAC
kubectl auth can-i get secrets --as=system:serviceaccount:default:haptic
```

### Validation Errors

**Diagnosis:**

1. Check error message for specific field
2. Review configuration schema
3. Verify field types and values
4. Check for typos in YAML keys

```go
// Debug validation
config, err := config.ParseConfig(configMapData)
if err != nil {
    log.Error("config validation failed", "error", err)

    // Print config structure for debugging (without credentials)
    configJSON, _ := json.MarshalIndent(config, "", "  ")
    log.Debug("config structure", "json", string(configJSON))
}
```

## Resources

- API documentation: `pkg/core/README.md`
- Configuration reference: `/docs/supported-configuration.md`
- Architecture: `/docs/development/design.md`
- slog documentation: <https://pkg.go.dev/log/slog>
