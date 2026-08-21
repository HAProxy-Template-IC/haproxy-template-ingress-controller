# pkg/core - Core Functionality

Development context for core shared functionality.

**API Documentation**: See `pkg/core/README.md`
**Architecture**: See `/docs/site/docs/development/design/package-structure.md` (package organization)

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

Defines configuration types and provides loading functions:

```go
// Apply defaults to a parsed config (mutates in place)
config.SetDefaults(cfg)

// Load credentials from Secret data
creds, err := config.LoadCredentials(secretData)
```

There's no YAML-loading entry point here — the controller is CRD-driven. The `pkg/controller/conversion` package converts an `*unstructured.Unstructured` `HAProxyTemplateConfig` directly into the `*Config` this package defines.

**Responsibilities:**

- Define `Config` struct and all nested types
- Basic structural validation (required fields, port ranges)
- Credentials loading and validation
- NOT: Template validation (done in `pkg/controller/validator.TemplateValidator`)
- NOT: JSONPath validation (done in `pkg/controller/validator.JSONPathValidator`)
- NOT: Watching the CRD/Secret (done in `pkg/k8s/watcher` via `SingleWatcher`)

### logging/ - Structured Logging

Sets up structured logging with slog. The package only exposes a handful of plain functions — there's no `Config` struct, no `Format` option, and no JSON output (everything is logfmt to stdout):

```go
// Dynamic logger whose level can be bumped at runtime
logger := logging.NewDynamicLogger(os.Getenv("LOG_LEVEL"))
slog.SetDefault(logger)
logging.SetLevel("DEBUG") // updates the package-global slog.LevelVar

// Use throughout application
slog.Info("controller started",
    "namespace", namespace,
    "watched_resources", len(watchedResources))
```

Levels are case-insensitive strings (`TRACE`, `DEBUG`, `INFO`, `WARN`/`WARNING`, `ERROR`); unknown values fall back to `INFO`. `TRACE` is a non-standard `slog.Level(-8)` used by filter-debug logging.

## Configuration Schema

### Core Types

The full surface lives in `pkg/core/config/types.go`; the most important shapes are:

```go
// Main configuration (selected fields — see types.go for the full list)
type Config struct {
    PodSelector          PodSelector
    Controller           ControllerConfig
    Logging              LoggingConfig
    Dataplane            DataplaneConfig
    TemplatingSettings   TemplatingSettings
    WatchedResources     map[string]WatchedResource
    TemplateSnippets     map[string]TemplateSnippet
    Maps                 map[string]MapFile
    Files                map[string]GeneralFile
    SSLCertificates      map[string]SSLCertificate
    CRTLists             map[string]CRTListFile
    HAProxyConfig        HAProxyConfig          // single template, not "Spec"
    ValidationTests      map[string]ValidationTest
}

// Watched resource definition (selected fields — see types.go for the full shape).
// There is intentionally no Kind field; the controller derives the kind from
// the GroupVersionResource at watch time. DebounceInterval is optional —
// when empty or unparseable, the watcher falls back to
// pkg/k8s/types.DefaultDebounceInterval (100ms); the field exists so noisy
// resources (HTTPRoute, EndpointSlice) can override the global window
// (the chart sets it to "0" on EndpointSlice for instant rolling-restart
// reaction).
type WatchedResource struct {
    APIVersion              string            `yaml:"api_version"`
    Resources               string            `yaml:"resources"`
    IndexBy                 []string          `yaml:"index_by"`
    LabelSelector           map[string]string `yaml:"label_selector,omitempty"`
    FieldSelector           string            `yaml:"field_selector,omitempty"`
    Store                   string            `yaml:"store,omitempty"`              // "full" or "on-demand"
    EnableValidationWebhook bool              `yaml:"enable_validation_webhook"`
    DebounceInterval        string            `yaml:"debounce_interval,omitempty"`  // Go duration string, e.g. "10s"
}

// Auxiliary file definitions — every "file template" type carries the same
// two fields. There is no Path field, no embedded cert/key block, and no
// per-type variant; the shape is uniform on purpose so the renderer can treat
// every kind through pkg/dataplane/auxiliaryfiles.FileItem.
type (
    MapFile        struct{ Template string; PostProcessing []PostProcessorConfig }
    GeneralFile    struct{ Template string; PostProcessing []PostProcessorConfig }
    SSLCertificate struct{ Template string; PostProcessing []PostProcessorConfig }
    CRTListFile    struct{ Template string; PostProcessing []PostProcessorConfig }
)
type HAProxyConfig struct{ Template string; PostProcessing []PostProcessorConfig }
```

There is no `HAProxyConfigSpec`, `MapDefinition`, `FileDefinition`, or `DataplaneAPIConfig` — those names appeared in older drafts of this doc and never matched the source. Use the real types above.

### Validation Layers

**Basic Validation (pkg/core/config):**

- Required fields present
- Port numbers in valid range (1-65535)
- Enum values are valid (e.g., the `Store` field is "full" or "on-demand"; the schema-side enum lives on the CRD type and is enforced by the apiserver, not by pkg/core/config)
- Non-empty credentials

**Advanced Validation (pkg/controller/validator):**

- Template syntax validation
- JSONPath expression validation
- Cross-field validation
- Business rule validation

## Testing Approach

### Test Defaults and Basic Validation

```go
func TestSetDefaults_AppliesDataplanePort(t *testing.T) {
    cfg := &config.Config{}

    config.SetDefaults(cfg)

    assert.Equal(t, config.DefaultDataplanePort, cfg.Dataplane.Port)
    require.NoError(t, config.ValidateStructure(cfg))
}
```

### Test Credentials Loading

```go
func TestLoadCredentials_Valid(t *testing.T) {
    secretData := map[string][]byte{
        "dataplane_username": []byte("admin"),
        "dataplane_password": []byte("secret123"),
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

// Advanced validation in pkg/controller/validator
package validator

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
        if res.Resources == "" {
            return fmt.Errorf("watched_resources.%s.resources is required", name)
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
    debounce := 5 * time.Second   // Default hardcoded here
    // ...
}

func anotherPlace(cfg WatchedResource) {
    debounce := 3 * time.Second   // Different default!
    // ...
}
```

**Solution**: Define defaults in the canonical package and have callers depend on it. The actual debounce default lives in `pkg/k8s/types.DefaultDebounceInterval`, not in `pkg/core/config` — config-side defaults like `DefaultMinDeploymentInterval` live here.

```go
// Good - centralized defaults (real example from pkg/core/config/defaults.go)
package config

const (
    DefaultMinDeploymentInterval   = 2 * time.Second
    DefaultDriftPreventionInterval = 60 * time.Second
    // The watcher debounce window is intentionally NOT redefined here;
    // reuse pkg/k8s/types.DefaultDebounceInterval (100 * time.Millisecond).
)

// There is no reconciler-level debounce default here. The reconciler fires
// immediately on every event, so pkg/core/config does not mirror the
// per-watcher debounce default, and there is no
// spec.controller.reconciliationDebounceInterval CRD knob. The single
// timing default that matters for batching lives in
// pkg/k8s/types.DefaultDebounceInterval (the per-watcher window);
// reload throttling lives in DefaultMinDeploymentInterval (the deployer).

// Each Get* accessor parses the user's duration string and falls back to
// the constant when the field is empty or invalid.
func (d *DataplaneConfig) GetMinDeploymentInterval() time.Duration {
    return parseDurationOr(d.MinDeploymentInterval, DefaultMinDeploymentInterval)
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
- Use TLS for agent connections
- Rotate credentials regularly

**DON'T:**

- Log credentials
- Store credentials in the CRD spec or any non-Secret resource
- Hardcode credentials
- Pass credentials as environment variables (use Secret instead)

### Handling Credentials

```go
// Good - secure credential handling
type Credentials struct {
    DataplaneUsername string
    DataplanePassword string
}

// No String() method - prevents accidental logging

// Redact in logs
func (c Credentials) Redacted() map[string]string {
    return map[string]string{
        "dataplane_username": c.DataplaneUsername,
        "dataplane_password": "***REDACTED***",
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
    HAProxyConfig HAProxyConfig // real name; HAProxyConfigSpec doesn't exist

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

// Add a version check after the CRD has been mapped onto *Config
// (pkg/controller/conversion.ParseCRD).
func checkVersion(cfg *Config) error {
    if cfg.Version != "" && cfg.Version != "v2" {
        return fmt.Errorf("unsupported config version %s, expected v2", cfg.Version)
    }
    return nil
}
```

## Troubleshooting

### Configuration Not Loading

**Diagnosis:**

1. Check the `HAProxyTemplateConfig` CRD exists and matches `spec.podSelector`
2. Verify YAML inside `spec` parses (the CRD validation only does shallow checks)
3. Check for required fields
4. Review controller logs

```bash
# Verify CRD instance
kubectl get haproxytemplateconfig -A

# Check controller logs
kubectl logs deployment/haptic-controller | grep -i "config"
```

### Credentials Not Loading

**Diagnosis:**

1. Check Secret referenced by `spec.credentialsSecretRef` exists
2. Verify both `dataplane_username` and `dataplane_password` keys are present and non-empty
3. Review controller RBAC permissions

```bash
# Verify Secret exists (don't print values)
kubectl get secret <credentialsSecretRef.name>

# Check Secret keys
kubectl get secret <credentialsSecretRef.name> -o json | jq '.data | keys'

# Verify RBAC
kubectl auth can-i get secrets --as=system:serviceaccount:<ns>:<controller-sa>
```

### Validation Errors

**Diagnosis:**

1. Check error message for specific field
2. Review configuration schema
3. Verify field types and values
4. Check for typos in YAML keys

```go
// Debug validation
if err := config.ValidateStructure(cfg); err != nil {
    slog.Error("config validation failed", "error", err)
}
```

## Resources

- API documentation: `pkg/core/README.md`
- Configuration reference: `/docs/site/docs/supported-configuration.md`
- Architecture: `/docs/site/docs/development/design.md`
- slog documentation: <https://pkg.go.dev/log/slog>
