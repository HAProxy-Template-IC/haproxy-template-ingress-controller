# pkg/core

Core business logic packages for the HAProxy Template Ingress Controller.

## Packages

### config

The `config` package provides configuration loading, validation, and default value management for the controller.

## Usage

### Basic Configuration Loading

```go
import (
    "log"
    "haptic/pkg/core/config"
)

// 1. Parse YAML configuration
cfg, err := config.ParseConfig(configMapData["config"])
if err != nil {
    log.Fatalf("Failed to parse config: %v", err)
}

// 2. Apply default values
config.SetDefaults(cfg)

// 3. Validate structure
if err := config.ValidateStructure(cfg); err != nil {
    log.Fatalf("Invalid config: %v", err)
}

// Configuration is ready to use
```

### Loading Credentials

```go
import (
    "log"
    "haptic/pkg/core/config"
)

// Load credentials from Kubernetes Secret
creds, err := config.LoadCredentials(secret.Data)
if err != nil {
    log.Fatalf("Failed to load credentials: %v", err)
}

// Validate credentials
if err := config.ValidateCredentials(creds); err != nil {
    log.Fatalf("Invalid credentials: %v", err)
}

// Credentials are ready to use
```

### Complete Workflow

```go
import (
    "context"
    "log"

    "haptic/pkg/core/config"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/kubernetes"
)

func loadConfiguration(ctx context.Context, clientset *kubernetes.Clientset, namespace string) error {
    // Load ConfigMap
    configMap, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, "haproxy-template-config", metav1.GetOptions{})
    if err != nil {
        return fmt.Errorf("failed to get ConfigMap: %w", err)
    }

    // Parse configuration
    cfg, err := config.ParseConfig(configMap.Data["config"])
    if err != nil {
        return fmt.Errorf("failed to parse config: %w", err)
    }

    // Apply defaults
    config.SetDefaults(cfg)

    // Validate structure
    if err := config.ValidateStructure(cfg); err != nil {
        return fmt.Errorf("invalid config: %w", err)
    }

    // Load Secret
    secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, "haproxy-template-credentials", metav1.GetOptions{})
    if err != nil {
        return fmt.Errorf("failed to get Secret: %w", err)
    }

    // Load credentials
    creds, err := config.LoadCredentials(secret.Data)
    if err != nil {
        return fmt.Errorf("failed to load credentials: %w", err)
    }

    // Validate credentials
    if err := config.ValidateCredentials(creds); err != nil {
        return fmt.Errorf("invalid credentials: %w", err)
    }

    log.Printf("Configuration loaded successfully")
    return nil
}
```

## API Reference

### config.ParseConfig

```go
func ParseConfig(configYAML string) (*config.Config, error)
```

Parses YAML configuration string into a Config struct. This is a pure function that only performs YAML parsing without loading from Kubernetes, applying defaults, or validating.

**Parameters:**

- `configYAML`: The raw YAML configuration string from the ConfigMap

**Returns:**

- Parsed Config struct or error if YAML parsing fails

### config.SetDefaults

```go
func SetDefaults(cfg *config.Config)
```

Applies default values to unset configuration fields. Modifies the config in-place. Should be called after parsing and before validation.

**Default Values:**

- `controller.healthz_port`: 8080
- `controller.metrics_port`: 9090
- `validation.dataplane_host`: "localhost"
- `validation.dataplane_port`: 5555
- `logging.verbose`: 0 (no default, 0 is valid WARNING level)

**Parameters:**

- `cfg`: The configuration to modify

### config.ValidateStructure

```go
func ValidateStructure(cfg *config.Config) error
```

Performs structural validation on the configuration. Validates required fields, field types, port ranges, and non-empty slices.

**Does NOT validate:**

- Template syntax (validated by controller via scatter-gather)
- JSONPath expressions (validated by controller via scatter-gather)

**Parameters:**

- `cfg`: The configuration to validate

**Returns:**

- Error describing the first validation failure, or nil if valid

### config.LoadCredentials

```go
func LoadCredentials(secretData map[string][]byte) (*config.Credentials, error)
```

Parses Kubernetes Secret data into a Credentials struct. This is a pure function that extracts credentials without loading from Kubernetes or performing validation.

**Expected Secret Keys:**

- `dataplane_username`: Username for HAProxy Data Plane API
- `dataplane_password`: Password for HAProxy Data Plane API
- `validation_username`: Username for validation dataplane
- `validation_password`: Password for validation dataplane

**Parameters:**

- `secretData`: The Secret's data map (keys to base64-decoded byte arrays)

**Returns:**

- Parsed Credentials struct or error if required keys are missing

### config.ValidateCredentials

```go
func ValidateCredentials(creds *config.Credentials) error
```

Performs basic validation on credentials. Ensures all required credential fields are present and non-empty.

**Parameters:**

- `creds`: The credentials to validate

**Returns:**

- Error if validation fails, or nil if valid

## Configuration Workflow

The recommended workflow for configuration management:

1. **Parse**: Use `ParseConfig()` to convert YAML to struct
2. **Default**: Use `SetDefaults()` to fill in missing values
3. **Validate**: Use `ValidateStructure()` to ensure correctness
4. **Use**: Configuration is ready for controller use

For credentials:

1. **Load**: Use `LoadCredentials()` to extract from Secret
2. **Validate**: Use `ValidateCredentials()` to ensure completeness
3. **Use**: Credentials are ready for controller use

## Error Handling

All functions return descriptive errors that can be logged or returned to users. Errors include field names and context to aid troubleshooting.

Example error messages:

```
pod_selector: match_labels cannot be empty
controller: healthz_port must be between 1 and 65535, got 0
validation: dataplane_host cannot be empty
watched_resources: ingresses: api_version cannot be empty
```

## Testing

The config package includes comprehensive unit tests. Run tests with:

```bash
go test ./pkg/core/config/...
```

See test files for usage examples:

- `loader_test.go`: Configuration parsing examples
- `defaults_test.go`: Default value application examples
- `validator_test.go`: Validation scenarios and error cases
