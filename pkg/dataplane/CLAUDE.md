# pkg/dataplane - HAProxy Integration

Development context for HAProxy Dataplane API integration.

**API Documentation**: See `pkg/dataplane/README.md`
**Architecture**: See `/docs/development/design.md` (Dataplane Integration section)

## When to Work Here

Modify this package when:

- Adding support for new HAProxy configuration sections
- Implementing new comparison logic for existing sections
- Fixing synchronization bugs
- Adding auxiliary file types (maps, certificates, general files)
- Improving transaction management or retry logic
- Updating client-native library integration

**DO NOT** modify this package for:

- Template rendering → Use `pkg/templating`
- Event coordination → Use `pkg/controller`
- Kubernetes integration → Use `pkg/k8s`
- Configuration parsing → Use `pkg/core/config`

## Package Structure

```
pkg/dataplane/
├── auxiliaryfiles/      # Auxiliary file management (maps, SSL, general files)
├── client/              # Dataplane API client with transactions
├── comparator/          # Fine-grained configuration comparison
│   └── sections/        # 30+ section-specific comparators
├── parser/              # Config parsing using client-native
├── synchronizer/        # Operation execution with retries
├── transform/           # Model transformation (client-native ↔ Dataplane API)
├── types/               # Public types (Endpoint, SyncOptions)
├── config.go            # Public configuration types
├── dataplane.go         # Public API (Sync, DryRun, Diff)
├── errors.go            # Structured error types
├── orchestrator.go      # Sync workflow coordination
└── result.go            # Result types
```

## Key Concepts

### Three-Phase Sync

```
Phase 1: Pre-Config Sync
  - Create/update auxiliary files
  - Upload certificates
  - Update map files
  - Ensure dependencies exist before config references them

Phase 2: Config Sync
  - Parse rendered HAProxy config
  - Compare with current config
  - Generate minimal operations (add/update/delete)
  - Execute via transactions
  - Leverage runtime API when possible (zero-reload)

Phase 3: Post-Config Sync
  - Delete unused auxiliary files
  - Clean up orphaned resources
  - Cannot be done before config sync (config might still reference them)
```

**Why three phases?**

HAProxy config can reference auxiliary files. We must ensure:

1. Files exist before config references them (pre-config)
2. Config is validated and applied (config)
3. Orphaned files are cleaned up (post-config)

### client-native Library

This package wraps `github.com/haproxytech/client-native` for HAProxy configuration parsing and API access.

**Limitations:**

- Not all HAProxy directives are supported
- Some sections require specific API versions
- Parsing errors don't always provide helpful context
- Transaction handling requires careful management

**Workarounds:**

- Validate config with `haproxy -c` binary before parsing
- Wrap parsing errors with additional context
- Implement transaction retry logic
- Use structured comparison to minimize API calls

### Zero-Reload Optimization

Some changes can be applied without HAProxy reload:

**Runtime operations (no reload):**

- Server add/remove
- Server state changes (enable/disable)
- Map updates
- ACL updates
- SSL certificate updates

**Structural changes (requires reload):**

- Frontend/backend creation/deletion
- Bind address changes
- Global/defaults modifications

The comparator detects which type of changes occurred and optimizes deployment strategy.

### Server Field Runtime Support

The Dataplane API can update only specific server fields at runtime without triggering a HAProxy reload:

**Runtime-supported fields (HTTP 200 - no reload):**

- `Weight`, `Address`, `Port` - Core server properties
- `Maintenance` - Server admin state (`enabled`/`disabled`)
- `AgentCheck`, `AgentAddr`, `AgentSend`, `HealthCheckPort` - Agent checks

**Important:** The `disabled` and `enabled` options on server lines do NOT cause reloads. This enables the reserved slots pattern where unused slots are `disabled` and enabled at runtime when pods scale up.

**Fields that trigger reload (HTTP 202):**

- `Check` - Health check configuration
- `Proto` - Protocol (h2, etc.)
- `SSL`, `Verify`, `CaFile`, `Crt` - TLS configuration
- All other server options

**Template Implication:** To maximize runtime API usage, templates should:

1. Place all server options (`check`, `proto`, SSL settings) in `default-server` directive
2. Keep individual `server` lines to only `address:port` plus `enabled` or `disabled`

Example:

```haproxy
backend my-backend
    default-server check proto h2
    server SRV_1 10.0.0.1:8080 enabled      # Active server
    server SRV_2 10.0.0.2:8080 enabled      # Active server
    server SRV_3 127.0.0.1:1 disabled       # Reserved slot
```

This allows endpoint changes (pod IP/port) and server state changes (enabled/disabled) to be applied via runtime API without reloading HAProxy.

## Multi-Version API Support

The client supports HAProxy DataPlane API versions v3.0, v3.1, and v3.2 simultaneously through runtime version detection and a centralized dispatcher pattern.

### Version Detection

The client automatically detects the API version on initialization:

```go
client, err := client.New(ctx, &client.Config{
    BaseURL:  "http://haproxy:5555",
    Username: "admin",
    Password: "password",
})
// Client detects version by calling /v3/info endpoint
// Detected version: "v3.2.6 87ad0bcf"
```

### Capability Matrix

Different API versions support different features. The client provides capability detection:

| Feature | v3.0 | v3.1 | v3.2 | Capability Flag |
|---------|------|------|------|-----------------|
| **Storage** |
| General files | ✅ | ✅ | ✅ | `SupportsGeneralStorage` |
| Map files | ✅ | ✅ | ✅ | `SupportsMapStorage` |
| SSL certificates | ✅ | ✅ | ✅ | _(always available)_ |
| CRT-list files | ❌ | ❌ | ✅ | `SupportsCrtList` |
| **Protocol Support** |
| HTTP/2 | ✅ | ✅ | ✅ | `SupportsHTTP2` |
| QUIC/HTTP3 | ✅ | ✅ | ✅ | `SupportsQUIC` |
| **Runtime** |
| Runtime maps | ✅ | ✅ | ✅ | `SupportsRuntimeMaps` |
| Runtime servers | ✅ | ✅ | ✅ | `SupportsRuntimeServers` |

#### Enterprise-Only Capabilities

Enterprise editions have additional capabilities not available in Community:

| Feature | v3.0ee | v3.1ee | v3.2ee | Capability Flag |
|---------|--------|--------|--------|-----------------|
| **WAF** |
| WAF body rules, rulesets | ✅ | ✅ | ✅ | `SupportsWAF` |
| WAF global config | ❌ | ❌ | ✅ | `SupportsWAFGlobal` |
| WAF profiles | ❌ | ❌ | ✅ | `SupportsWAFProfiles` |
| **Security** |
| Bot management | ✅ | ✅ | ✅ | `SupportsBotManagement` |
| **Load Balancing** |
| UDP load balancing | ✅ | ✅ | ✅ | `SupportsUDPLoadBalancing` |
| UDP LB ACLs | ❌ | ❌ | ✅ | `SupportsUDPLBACLs` |
| UDP LB server switching | ❌ | ❌ | ✅ | `SupportsUDPLBServerSwitchingRules` |
| **High Availability** |
| Keepalived/VRRP | ✅ | ✅ | ✅ | `SupportsKeepalived` |
| **Configuration** |
| Dynamic updates | ✅ | ✅ | ✅ | `SupportsDynamicUpdate` |
| Git integration | ✅ | ✅ | ✅ | `SupportsGitIntegration` |
| Advanced logging | ✅ | ✅ | ✅ | `SupportsAdvancedLogging` |
| ALOHA features | ✅ | ✅ | ✅ | `SupportsALOHA` |
| **Miscellaneous** |
| Ping endpoint | ❌ | ❌ | ✅ | `SupportsPing` |

**Usage with DataPlane API Client:**

```go
if client.Clientset().Capabilities().SupportsCrtList {
    // Use crt-list storage (v3.2+ only)
    err := client.CreateCRTListFile(ctx, "example.crtlist", content)
}
```

### Capabilities Type Export

The `Capabilities` struct is exported from `pkg/dataplane` for use in components that need to check HAProxy feature availability without a DataPlane API connection (e.g., local CLI validation, template rendering).

**Type Alias:**

```go
// pkg/dataplane/capabilities.go
package dataplane

import "haptic/pkg/dataplane/client"

// Capabilities represents HAProxy feature availability based on version.
// This is a type alias for client.Capabilities, exported for use by
// controller components that need capability information.
type Capabilities = client.Capabilities
```

**Creating Capabilities from Local HAProxy Version:**

When the controller runs alongside HAProxy (e.g., in sidecar mode), use `CapabilitiesFromVersion()` to detect capabilities from the local HAProxy binary:

```go
// Detect local HAProxy version
localVersion, err := dataplane.GetLocalVersion(ctx)
if err != nil {
    return fmt.Errorf("failed to detect local HAProxy: %w", err)
}

// Create capabilities from detected version
capabilities := dataplane.CapabilitiesFromVersion(localVersion)

// Use capabilities for template rendering, CLI validation, etc.
if capabilities.SupportsCrtList {
    // Configure CRT-list based SSL certificate paths
}
```

**Capabilities Fields:**

| Field | Description | Version |
|-------|-------------|---------|
| `SupportsCrtList` | CRT-list file storage support | v3.2+ |
| `SupportsMapStorage` | Map file storage support | v3.0+ |
| `SupportsGeneralStorage` | General file storage support | v3.0+ |
| `SupportsHTTP2` | HTTP/2 protocol support | v3.0+ |
| `SupportsQUIC` | QUIC/HTTP3 protocol support | v3.0+ |
| `SupportsRuntimeMaps` | Runtime map updates | v3.0+ |
| `SupportsRuntimeServers` | Runtime server updates | v3.0+ |

**Safe Defaults:**

When version is unknown (nil), `CapabilitiesFromVersion()` returns all capabilities as `false` - the safest default that prevents using features that might not be available.

```go
// Safe handling of unknown version
var version *dataplane.Version // nil - unknown
caps := dataplane.CapabilitiesFromVersion(version)
// All caps.Supports* fields are false - safe fallback behavior
```

### Dispatcher Pattern

All client methods use a centralized dispatcher to route calls to the appropriate version-specific client. This eliminates repetitive switch-case logic across 26+ methods.

**Architecture:**

```
┌─────────────────────┐
│  Public Methods     │  GetAllMapFiles(), CreateSSLCertificate(), etc.
│  (26 methods)       │
└──────────┬──────────┘
           │ All delegate to
           ▼
┌─────────────────────┐
│  Dispatcher         │  Dispatch() or DispatchWithCapability()
│  (Single point)     │
└──────────┬──────────┘
           │ Routes based on detected version
           ▼
┌─────────────────────┐
│  Version Clients    │  v30.Client, v31.Client, v32.Client
│  (Generated code)   │
└─────────────────────┘
```

**Basic dispatch (all versions):**

```go
func (c *DataplaneClient) GetAllMapFiles(ctx context.Context) ([]string, error) {
    resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
        V32: func(c *v32.Client) (*http.Response, error) { return c.GetAllStorageMapFiles(ctx) },
        V31: func(c *v31.Client) (*http.Response, error) { return c.GetAllStorageMapFiles(ctx) },
        V30: func(c *v30.Client) (*http.Response, error) { return c.GetAllStorageMapFiles(ctx) },
    })

    if err != nil {
        return nil, fmt.Errorf("failed to get all map files: %w", err)
    }
    defer resp.Body.Close()

    // ... process response
}
```

**Dispatch with capability check (version-specific features):**

```go
func (c *DataplaneClient) GetAllCRTListFiles(ctx context.Context) ([]string, error) {
    resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
        V32: func(c *v32.Client) (*http.Response, error) {
            return c.GetAllStorageSSLCrtListFiles(ctx)
        },
        // V31 and V30 omitted - not supported
    }, func(caps Capabilities) error {
        if !caps.SupportsCrtList {
            return fmt.Errorf("crt-list storage requires DataPlane API v3.2+")
        }
        return nil
    })

    if err != nil {
        return nil, fmt.Errorf("failed to get all crt-list files: %w", err)
    }
    defer resp.Body.Close()

    // ... process response
}
```

**Generic dispatch (non-HTTP return types):**

```go
version, err := DispatchGeneric[int64](ctx, c.Clientset(), CallFunc[int64]{
    V32: func(c *v32.Client) (int64, error) {
        resp, err := c.GetConfigurationVersion(ctx, &v32.GetConfigurationVersionParams{})
        if err != nil {
            return 0, err
        }
        defer resp.Body.Close()
        // ... parse version from response
        return parsedVersion, nil
    },
    // ... similar for V31 and V30
})
```

### Adding New Methods

When adding support for new DataPlane API endpoints:

1. **Check version support**: Determine which API versions support the feature
2. **Choose dispatch method**:
   - Use `Dispatch()` if all versions support it
   - Use `DispatchWithCapability()` if only some versions support it
   - Use `DispatchGeneric[T]()` if return type is not `*http.Response`
3. **Implement version-specific calls**:
   - Provide function for each supported version
   - Omit versions that don't support the feature

**Example - Adding new feature (v3.2+ only):**

```go
func (c *DataplaneClient) GetNewFeature(ctx context.Context, name string) (string, error) {
    resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
        V32: func(c *v32.Client) (*http.Response, error) {
            return c.GetNewFeatureEndpoint(ctx, name, &v32.GetNewFeatureParams{})
        },
        // V31 and V30 omitted - feature not available
    }, func(caps Capabilities) error {
        // Add capability check if needed
        if !caps.SupportsNewFeature {
            return fmt.Errorf("new feature requires DataPlane API v3.2+")
        }
        return nil
    })

    if err != nil {
        return "", fmt.Errorf("failed to get new feature: %w", err)
    }
    defer resp.Body.Close()

    // Process response...
    return result, nil
}
```

**Benefits of dispatcher pattern:**

- ✅ **DRY**: Version-switching logic centralized in one place (~200 lines)
- ✅ **Type-safe**: Compile-time checking via Go generics
- ✅ **Maintainable**: Adding v3.3+ requires updating only dispatcher.go
- ✅ **Testable**: Single point to test version routing logic
- ✅ **Readable**: Clear intent with minimal boilerplate

## Sub-Package Guidelines

### client/ - Dataplane API Client

Manages HTTP client and transaction lifecycle:

```go
// Create client with endpoints
endpoints := []types.Endpoint{
    {URL: "http://haproxy-0:5555", Username: "admin", Password: "pass"},
    {URL: "http://haproxy-1:5555", Username: "admin", Password: "pass"},
}

client := client.New(endpoints, client.Options{
    Timeout:        30 * time.Second,
    RetryAttempts:  3,
    RetryInterval:  1 * time.Second,
})

// Execute operations with transaction
tx, err := client.StartTransaction()
defer tx.Commit()  // Or Rollback()

err = client.CreateBackend(tx, backend)
err = client.UpdateFrontend(tx, frontend)
```

**When to modify:**

- Adding new Dataplane API endpoint support
- Changing retry logic
- Improving error handling
- Adding request/response logging

**Common pitfall**: Forgetting to commit/rollback transactions leads to hung transactions on Dataplane API.

### parser/ - Configuration Parser

Wraps client-native for parsing and validation:

```go
// Parse configuration string into structured format
parsed, err := parser.Parse(configString)

if err != nil {
    // Common errors:
    // - Unsupported directive
    // - Syntax error
    // - Missing section
    return fmt.Errorf("parse failed: %w", err)
}

// Access parsed configuration
frontends := parsed.Frontends
backends := parsed.Backends
```

**Validation strategy:**

1. **Syntax validation**: client-native parser
2. **Semantic validation**: `haproxy -c -f config` (done before parsing)

**When to modify:**

- Supporting new configuration sections
- Improving error messages
- Adding validation logic

### comparator/ - Configuration Comparison

Compares two parsed configurations and generates operations:

```go
// Compare current vs desired config
result := comparator.Compare(currentConfig, desiredConfig)

// Result contains operations
for _, op := range result.Operations {
    switch op.Type {
    case comparator.OperationCreate:
        // Create new resource
    case comparator.OperationUpdate:
        // Update existing resource
    case comparator.OperationDelete:
        // Delete resource
    }
}

// Categorize operations by reload requirement
runtimeOps := result.RuntimeOperations()  // Can apply without reload
structuralOps := result.StructuralOperations()  // Requires reload
```

**Section-specific comparators** (`comparator/sections/`):

Each HAProxy section has dedicated comparison logic:

- `frontend.go` - Frontend comparison
- `backend.go` - Backend comparison
- `server.go` - Server comparison
- `acl.go` - ACL comparison
- `bind.go` - Bind address comparison
- ... 30+ more section comparators

**Adding new section comparator:**

```go
// comparator/sections/mycustomsection.go
package sections

import "github.com/haproxytech/client-native/v5/models"

type MyCustomSectionComparator struct{}

func (c *MyCustomSectionComparator) Compare(current, desired *models.MyCustomSection) []Operation {
    var ops []Operation

    // Compare fields
    if current.Field1 != desired.Field1 {
        ops = append(ops, Operation{
            Type:     OperationUpdate,
            Section:  "mycustomsection",
            Resource: desired,
            Field:    "field1",
        })
    }

    return ops
}

// Register in comparator/comparator.go
comparators["mycustomsection"] = &MyCustomSectionComparator{}
```

**Comparator Section Implementation:**

All section operations (Create, Delete, Update) use the Dispatch pattern to support multiple HAProxy versions. There are two implementation approaches:

**Helper-based pattern** (for backend, frontend, defaults):

Uses generic helper functions from `execute_helpers.go` to reduce boilerplate. Helpers handle validation, transformation, JSON marshaling, response checking, and error wrapping. Callers provide version-specific dispatch logic as inline callbacks.

```go
// comparator/sections/backend.go
func (op *CreateBackendOperation) Execute(ctx context.Context, c *client.DataplaneClient, transactionID string) error {
    return executeCreateTransactionOnlyHelper(
        ctx, c, transactionID,
        op.Backend,                                           // Model to create
        func(m *models.Backend) string { return m.Name },     // Name extractor
        transform.ToAPIBackend,                               // Transform function
        func(ctx context.Context, c *client.DataplaneClient, jsonData []byte, txID string) (*http.Response, error) {
            params := &dataplaneapi.CreateBackendParams{TransactionId: &txID}
            return c.Dispatch(ctx, client.CallFunc[*http.Response]{
                V32: func(c *v32.Client) (*http.Response, error) {
                    var backend v32.Backend
                    json.Unmarshal(jsonData, &backend)
                    return c.CreateBackend(ctx, (*v32.CreateBackendParams)(params), backend)
                },
                V31: func(c *v31.Client) (*http.Response, error) {
                    var backend v31.Backend
                    json.Unmarshal(jsonData, &backend)
                    return c.CreateBackend(ctx, (*v31.CreateBackendParams)(params), backend)
                },
                V30: func(c *v30.Client) (*http.Response, error) {
                    var backend v30.Backend
                    json.Unmarshal(jsonData, &backend)
                    return c.CreateBackend(ctx, (*v30.CreateBackendParams)(params), backend)
                },
            })
        },
        "backend",  // Resource type for error messages
    )
}
```

**Direct Dispatch pattern** (for most other sections):

Implements full dispatch logic inline. Used for operations that require additional parameters (parent names, custom validation) or don't fit the helper pattern.

```go
// comparator/sections/binds.go
func (op *CreateBindFrontendOperation) Execute(ctx context.Context, c *client.DataplaneClient, transactionID string) error {
    // Marshal to JSON for version-specific unmarshaling
    jsonData, err := json.Marshal(op.Bind)
    if err != nil {
        return fmt.Errorf("failed to marshal bind: %w", err)
    }

    params := &dataplaneapi.CreateBindFrontendParams{
        TransactionId: &transactionID,
    }

    resp, err := c.Dispatch(ctx, client.CallFunc[*http.Response]{
        V32: func(c *v32.Client) (*http.Response, error) {
            var bind v32.Bind
            if err := json.Unmarshal(jsonData, &bind); err != nil {
                return nil, fmt.Errorf("failed to unmarshal bind for v3.2: %w", err)
            }
            return c.CreateBindFrontend(ctx, op.FrontendName, (*v32.CreateBindFrontendParams)(params), bind)
        },
        V31: func(c *v31.Client) (*http.Response, error) {
            var bind v31.Bind
            if err := json.Unmarshal(jsonData, &bind); err != nil {
                return nil, fmt.Errorf("failed to unmarshal bind for v3.1: %w", err)
            }
            return c.CreateBindFrontend(ctx, op.FrontendName, (*v31.CreateBindFrontendParams)(params), bind)
        },
        V30: func(c *v30.Client) (*http.Response, error) {
            var bind v30.Bind
            if err := json.Unmarshal(jsonData, &bind); err != nil {
                return nil, fmt.Errorf("failed to unmarshal bind for v3.0: %w", err)
            }
            return c.CreateBindFrontend(ctx, op.FrontendName, (*v30.CreateBindFrontendParams)(params), bind)
        },
    })
    if err != nil {
        return fmt.Errorf("failed to create bind '%s' in frontend '%s': %w", op.BindName, op.FrontendName, err)
    }
    defer resp.Body.Close()

    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return fmt.Errorf("create bind failed with status %d", resp.StatusCode)
    }

    return nil
}
```

**When to use each pattern:**

- **Helper-based**: Transaction-only operations (backend, frontend, defaults) where API calls follow standard pattern: `Create<Type>(params, object)`, `Replace<Type>(name, params, object)`, `Delete<Type>(name, params)`
- **Direct Dispatch**: Operations requiring parent identifiers (binds need frontend name), custom parameters, or API calls with non-standard signatures

**Why JSON marshaling is required:**

HAProxy DataPlane API version-specific types (v30.Backend, v31.Backend, v32.Backend) are structurally incompatible - newer versions add fields that older versions lack. Direct struct conversion fails at compile time. JSON marshaling provides type conversion:

1. Marshal unified model (dataplaneapi.Backend) to JSON
2. Unmarshal JSON into version-specific type (v32.Backend, v31.Backend, v30.Backend)
3. Missing fields in older versions are ignored during unmarshaling
4. Extra fields in newer versions get zero values when converting from older formats

This pattern trades ~10µs per operation for type safety and compatibility across all HAProxy versions.

### transform/ - Model Transformation

Converts client-native parser models to Dataplane API models using JSON marshaling:

```go
// Transform client-native model to API model
import "haptic/pkg/dataplane/transform"

// Client-native model from parser
clientACL := &models.ACL{
    ACLName:   "is_api",
    Criterion: "path_beg",
    Value:     "/api",
}

// Transform to Dataplane API model
apiACL := transform.ToAPIACL(clientACL)

// Now apiACL is *dataplaneapi.Acl, ready for API calls
err := client.CreateACL(tx, apiACL)
```

**Why this package exists:**

Before the transform package, every section comparator had inline conversions:

```go
// Old approach (duplicated 77 times)
data, _ := json.Marshal(clientModel)
var apiModel dataplaneapi.ACL
json.Unmarshal(data, &apiModel)
```

Now we have centralized, tested transformations:

```go
// New approach
apiModel := transform.ToAPIACL(clientModel)
```

**Design:**

- Generic `transform[T]` function handles JSON marshaling/unmarshaling
- 35+ type-specific wrapper functions for each HAProxy section
- Nil-safe (returns nil for nil input)
- Performance: ~10µs per transformation (acceptable for reconciliation)

**When to modify:**

- Adding support for new HAProxy configuration sections
- Client-native or Dataplane API models change structure
- Fixing transformation bugs

**Usage in comparators:**

```go
// comparator/sections/backend.go
func (c *BackendComparator) Compare(current, desired *models.Backend) []Operation {
    // Transform to API models for comparison
    currentAPI := transform.ToAPIBackend(current)
    desiredAPI := transform.ToAPIBackend(desired)

    // Compare and generate operations
    if !reflect.DeepEqual(currentAPI, desiredAPI) {
        return []Operation{{
            Type:     OperationUpdate,
            Resource: desiredAPI,
        }}
    }

    return nil
}
```

See `pkg/dataplane/transform/README.md` for complete API reference and `pkg/dataplane/transform/CLAUDE.md` for development context.

### synchronizer/ - Operation Execution

Executes operations with transaction management:

```go
// Execute operations in transaction
result, err := synchronizer.Execute(ctx, operations, endpoints)

if err != nil {
    // Common errors:
    // - Transaction conflict (version mismatch)
    // - API timeout
    // - Network error
    // - Validation error from HAProxy
}

// Result contains per-endpoint status
for endpoint, status := range result.EndpointResults {
    if status.Error != nil {
        log.Error("sync failed", "endpoint", endpoint, "error", status.Error)
    }
}
```

**Transaction retry logic:**

```go
maxAttempts := 3
for attempt := 1; attempt <= maxAttempts; attempt++ {
    tx, err := client.StartTransaction()
    if err != nil {
        continue
    }

    err = executeOperations(tx, operations)
    if err != nil {
        tx.Rollback()
        if isVersionConflict(err) && attempt < maxAttempts {
            // Reload config and retry
            continue
        }
        return err
    }

    if err := tx.Commit(); err == nil {
        return nil
    }

    // Commit failed, retry
}
```

### auxiliaryfiles/ - Auxiliary File Management

Manages maps, certificates, and general files:

```go
// Define auxiliary files
files := auxiliaryfiles.AuxiliaryFiles{
    Maps: map[string]string{
        "host.map": "example.com backend1\n",
    },
    SSLCerts: map[string]string{
        "cert.pem": "-----BEGIN CERTIFICATE-----\n...",
    },
    GeneralFiles: map[string]string{
        "500.http": "HTTP/1.0 500 Internal Server Error\n...",
    },
}

// Sync with three-phase approach
syncer := auxiliaryfiles.NewSyncer(client)

// Phase 1: Pre-config (create/update)
err := syncer.SyncPreConfig(ctx, files, endpoints)

// Phase 2: Apply HAProxy config (not in this package)

// Phase 3: Post-config (delete orphaned files)
err := syncer.SyncPostConfig(ctx, files, endpoints)
```

**Storage locations:**

- Maps: `/etc/haproxy/maps/`
- SSL certs: `/etc/haproxy/ssl/`
- General files: `/etc/haproxy/files/`

**When to modify:**

- Adding new file type
- Changing storage locations
- Improving sync logic

## Public API

### Main Entry Points

```go
// Synchronize configuration to endpoints
result, err := dataplane.Sync(ctx, config, endpoints, options)

// Dry run (compare only, no changes)
diff, err := dataplane.DryRun(ctx, config, endpoints)

// Get detailed diff
diff, err := dataplane.Diff(ctx, currentConfig, desiredConfig)
```

### Client Interface

```go
// Create client for multiple endpoints
client := dataplane.NewClient(endpoints, options)

// Sync configuration
result, err := client.Sync(ctx, renderedConfig)

// Fetch current configuration
currentConfig, err := client.FetchConfiguration(ctx, endpoint)
```

## Testing Strategies

### Unit Tests

Test individual components in isolation:

```go
func TestComparator_CompareBackends(t *testing.T) {
    current := &models.Backend{
        Name:    "api",
        Balance: "roundrobin",
    }

    desired := &models.Backend{
        Name:    "api",
        Balance: "leastconn",
    }

    ops := comparator.CompareBackends(current, desired)

    require.Len(t, ops, 1)
    assert.Equal(t, OperationUpdate, ops[0].Type)
    assert.Equal(t, "balance", ops[0].Field)
}
```

### Integration Tests

Test with real HAProxy and Dataplane API:

```go
func TestSync_Integration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    // Requires running HAProxy with Dataplane API
    endpoint := types.Endpoint{
        URL:      "http://localhost:5555",
        Username: "admin",
        Password: "adminpass",
    }

    config := `
    global
        daemon

    defaults
        mode http

    frontend http
        bind :80
    `

    result, err := dataplane.Sync(ctx, config, []types.Endpoint{endpoint}, nil)

    require.NoError(t, err)
    assert.True(t, result.Success)
}
```

### Mock Testing

Mock Dataplane API for controller tests:

```go
type MockDataplaneClient struct {
    SyncFunc func(ctx context.Context, config string) error
}

func (m *MockDataplaneClient) Sync(ctx context.Context, config string) error {
    if m.SyncFunc != nil {
        return m.SyncFunc(ctx, config)
    }
    return nil
}

func TestController_UsesDataplane(t *testing.T) {
    var syncedConfig string

    mockClient := &MockDataplaneClient{
        SyncFunc: func(ctx context.Context, config string) error {
            syncedConfig = config
            return nil
        },
    }

    controller := NewController(mockClient)
    controller.Reconcile(ctx)

    assert.Contains(t, syncedConfig, "frontend http")
}
```

## Error Simplification Pattern

The dataplane package provides helper functions to extract user-friendly error messages from complex library errors. This is especially important at component boundaries where raw errors from HAProxy or template rendering contain implementation details.

### SimplifyValidationError

Extracts meaningful messages from HAProxy validation errors.

**Handles two types of validation errors:**

1. **Schema validation errors** - OpenAPI spec violations from client-native library
2. **Semantic validation errors** - HAProxy binary validation failures

```go
// pkg/dataplane/errors.go
func SimplifyValidationError(err error) string {
    if err == nil {
        return ""
    }

    errStr := err.Error()

    // Try semantic validation error first (preserves context from parseHAProxyError)
    if strings.Contains(errStr, "semantic validation failed") {
        return simplifySemanticError(errStr)
    }

    // Try schema validation error
    if strings.Contains(errStr, "schema validation failed") {
        return simplifySchemaError(errStr)
    }

    // Unknown error type, return as-is
    return errStr
}
```

**Usage example:**

```go
// pkg/controller/dryrunvalidator/component.go
_, err := c.validator.ValidateConfig(ctx, haproxyConfig, nil, 0)
if err != nil {
    // Extract user-friendly message for webhook response
    simplified := dataplane.SimplifyValidationError(err)

    c.eventBus.Publish(events.NewWebhookValidationResponse(
        req.RequestID,
        "dryrun",
        false,  // denied
        simplified,  // User-friendly error message
    ))
    return
}
```

**Input/Output examples:**

```go
// Schema validation error (field constraint violation)
Input:  "schema validation failed: configuration violates API schema constraints: Error at \"/maxconn\": must be >= 1\nValue:\n  \"0\""
Output: "maxconn must be >= 1 (got 0)"

// Semantic validation error (HAProxy binary rejection)
Input:  "semantic validation failed: configuration has semantic errors: haproxy validation failed: [ALERT] (1) : parsing [/tmp/haproxy123.cfg:45] : 'bind' : cannot find SSL certificate '/etc/haproxy/ssl/missing.pem'\n"
Output: "[ALERT] (1) : parsing [/tmp/haproxy123.cfg:45] : 'bind' : cannot find SSL certificate '/etc/haproxy/ssl/missing.pem'"
```

### SimplifyRenderingError

Extracts meaningful messages from template rendering failures, particularly template-level validation errors from the `fail()` function.

```go
// pkg/dataplane/errors.go
func SimplifyRenderingError(err error) string {
    if err == nil {
        return ""
    }

    errStr := err.Error()

    // Look for the fail() function error pattern
    marker := "invalid call to function 'fail': "
    idx := strings.Index(errStr, marker)
    if idx == -1 {
        // Not a fail() error, return original (could be syntax error, missing variable, etc.)
        return errStr
    }

    // Extract everything after the marker (the user-provided message)
    message := errStr[idx+len(marker):]
    return strings.TrimSpace(message)
}
```

**Usage example:**

```go
// pkg/controller/dryrunvalidator/component.go
haproxyConfig, err := c.engine.Render("haproxy.cfg", templateContext)
if err != nil {
    // Extract user-friendly message for webhook response
    simplified := dataplane.SimplifyRenderingError(err)

    c.eventBus.Publish(events.NewWebhookValidationResponse(
        req.RequestID,
        "dryrun",
        false,
        simplified,
    ))
    return
}
```

**Input/Output examples:**

```go
// Template-level validation error (from fail() function)
Input:  "failed to render haproxy.cfg: failed to render template 'haproxy.cfg': unable to execute template: ... invalid call to function 'fail': Service 'api-backend' not found in namespace 'default'"
Output: "Service 'api-backend' not found in namespace 'default'"

// Syntax error (not from fail() - returned as-is)
Input:  "failed to render haproxy.cfg: syntax error at line 42"
Output: "failed to render haproxy.cfg: syntax error at line 42"
```

### When to Use Error Simplification

**Use at component boundaries:**

- Webhook validation responses (user-facing)
- Dry-run validation results (API responses)
- Log messages for end users
- Prometheus alert descriptions

**Don't use for:**

- Internal logging (want full stack trace)
- Debugging scenarios (need implementation details)
- Error wrapping (preserve error chain)
- Metrics labels (keep structured)

**Pattern:**

```go
// Internal error handling - keep full error
if err := syncConfig(cfg); err != nil {
    logger.Error("sync failed", "error", err)  // Full error for debugging
    metrics.RecordError(err.Error())            // Full error for metrics
    return fmt.Errorf("sync failed: %w", err)   // Wrap for error chain
}

// User-facing error - simplify
if err := validateConfig(cfg); err != nil {
    simplified := dataplane.SimplifyValidationError(err)
    return &ValidationResult{
        Valid:  false,
        Reason: simplified,  // User-friendly message
    }
}
```

### Testing Error Simplification

```go
func TestSimplifyValidationError_SchemaError(t *testing.T) {
    rawError := errors.New(`schema validation failed: configuration violates API schema constraints:
Error at "/maxconn": must be >= 1
Value:
  "0"`)

    simplified := dataplane.SimplifyValidationError(rawError)

    assert.Equal(t, "maxconn must be >= 1 (got 0)", simplified)
}

func TestSimplifyRenderingError_FailFunction(t *testing.T) {
    rawError := errors.New(`failed to render haproxy.cfg: failed to render template 'haproxy.cfg': unable to execute template: invalid call to function 'fail': Service not found`)

    simplified := dataplane.SimplifyRenderingError(rawError)

    assert.Equal(t, "Service not found", simplified)
}
```

## Common Pitfalls

### Not Using Three-Phase Sync

**Problem**: Config references file before it exists.

```go
// Bad
client.Sync(ctx, haproxyConfig)  // References map file
client.SyncMaps(ctx, maps)       // Upload map file - too late!
```

**Solution**: Pre-config, config, post-config sequence.

```go
// Good
client.SyncMaps(ctx, maps)         // Phase 1: Upload maps
client.Sync(ctx, haproxyConfig)    // Phase 2: Config (references maps)
client.CleanupMaps(ctx, maps)      // Phase 3: Delete unused maps
```

### Forgetting Transaction Cleanup

**Problem**: Transaction not committed/rolled back.

```go
// Bad
tx, err := client.StartTransaction()
// ... operations ...
return nil  // Transaction leaked!
```

**Solution**: Always defer cleanup.

```go
// Good
tx, err := client.StartTransaction()
if err != nil {
    return err
}

success := false
defer func() {
    if success {
        tx.Commit()
    } else {
        tx.Rollback()
    }
}()

// ... operations ...

success = true
return nil
```

### Ignoring Version Conflicts

**Problem**: Concurrent modifications cause transaction failures.

```go
// Bad
tx, err := client.StartTransaction()
err = execute(tx)
tx.Commit()  // Might fail due to version conflict
```

**Solution**: Retry on version conflict.

```go
// Good
for attempt := 0; attempt < 3; attempt++ {
    tx, err := client.StartTransaction()
    if err != nil {
        continue
    }

    err = execute(tx)
    if err != nil {
        tx.Rollback()
        return err
    }

    err = tx.Commit()
    if err == nil {
        return nil
    }

    if isVersionConflict(err) && attempt < 2 {
        // Refresh and retry
        time.Sleep(100 * time.Millisecond)
        continue
    }

    return err
}
```

### Not Validating Before Parsing

**Problem**: client-native parser provides poor error messages.

```go
// Bad - cryptic parsing error
parsed, err := parser.Parse(config)
// Error: "unexpected token at line 45"
```

**Solution**: Validate with haproxy binary first.

```go
// Good - detailed error from haproxy binary
cmd := exec.Command("haproxy", "-c", "-f", "-")
cmd.Stdin = strings.NewReader(config)
if output, err := cmd.CombinedOutput(); err != nil {
    return fmt.Errorf("validation failed: %s", output)
}

// Now parse with detailed context
parsed, err := parser.Parse(config)
```

## Extending HAProxy Support

### Adding New Configuration Section

1. **Check client-native support**: Does `github.com/haproxytech/client-native` support it?
2. **Add section comparator**: Create `comparator/sections/newsection.go`
3. **Register comparator**: Add to `comparator/comparator.go`
4. **Add API methods**: If needed, extend client with new methods
5. **Add tests**: Unit tests for comparison logic
6. **Document**: Update README.md with examples

### Example: Adding HTTP Error Files Support

```go
// Step 1: Check client-native
// models.HTTPErrorFiles exists in client-native v5

// Step 2: Create section comparator
// comparator/sections/httperrorfiles.go
package sections

type HTTPErrorFilesComparator struct{}

func (c *HTTPErrorFilesComparator) Compare(current, desired []*models.HTTPErrorFile) []Operation {
    var ops []Operation

    // Compare error files
    for _, d := range desired {
        found := false
        for _, cur := range current {
            if cur.Code == d.Code {
                found = true
                if cur.File != d.File {
                    ops = append(ops, Operation{
                        Type:     OperationUpdate,
                        Section:  "errorfile",
                        Resource: d,
                    })
                }
                break
            }
        }

        if !found {
            ops = append(ops, Operation{
                Type:     OperationCreate,
                Section:  "errorfile",
                Resource: d,
            })
        }
    }

    // Find deletions
    for _, cur := range current {
        found := false
        for _, d := range desired {
            if d.Code == cur.Code {
                found = true
                break
            }
        }

        if !found {
            ops = append(ops, Operation{
                Type:     OperationDelete,
                Section:  "errorfile",
                Resource: cur,
            })
        }
    }

    return ops
}

// Step 3: Register
// comparator/comparator.go
comparators["errorfile"] = &HTTPErrorFilesComparator{}

// Step 4: Client methods (if needed)
// client/errorfiles.go
func (c *Client) CreateErrorFile(tx Transaction, ef *models.HTTPErrorFile) error {
    // Implementation
}

// Step 5: Tests
// comparator/sections/httperrorfiles_test.go
func TestHTTPErrorFilesComparator(t *testing.T) {
    // Test cases
}
```

## Performance Optimization

### Minimize API Calls

```go
// Bad - one API call per operation
for _, backend := range backends {
    client.CreateBackend(backend)
}

// Good - batch in single transaction
tx := client.StartTransaction()
for _, backend := range backends {
    client.CreateBackendInTransaction(tx, backend)
}
tx.Commit()
```

### Parallel Endpoint Sync

```go
// Sync multiple endpoints in parallel
var wg sync.WaitGroup
results := make(chan EndpointResult, len(endpoints))

for _, endpoint := range endpoints {
    wg.Add(1)
    go func(ep Endpoint) {
        defer wg.Done()
        result := syncEndpoint(ctx, config, ep)
        results <- result
    }(endpoint)
}

wg.Wait()
close(results)

// Collect results
for result := range results {
    if result.Error != nil {
        log.Error("sync failed", "endpoint", result.Endpoint, "error", result.Error)
    }
}
```

### Cache Parsed Configurations

```go
// Bad - reparse same config multiple times
for _, endpoint := range endpoints {
    parsed, _ := parser.Parse(config)
    sync(endpoint, parsed)
}

// Good - parse once
parsed, err := parser.Parse(config)
if err != nil {
    return err
}

for _, endpoint := range endpoints {
    sync(endpoint, parsed)
}
```

## Troubleshooting

### Transaction Timeouts

**Diagnosis:**

1. Check Dataplane API health
2. Verify network connectivity
3. Review HAProxy logs
4. Check for stuck transactions

```bash
# Check Dataplane API health
curl http://haproxy-endpoint:5555/v2/info

# View active transactions
curl http://haproxy-endpoint:5555/v2/transactions

# HAProxy logs
kubectl logs haproxy-pod -c haproxy
```

### Parsing Failures

**Diagnosis:**

1. Validate config with haproxy binary
2. Check for unsupported directives
3. Review client-native version compatibility
4. Inspect full error context

```go
// Debug parsing
log.Info("attempting to parse config", "size", len(config))

parsed, err := parser.Parse(config)
if err != nil {
    // Save failed config for analysis
    ioutil.WriteFile("/tmp/failed-config.cfg", []byte(config), 0644)
    log.Error("parse failed", "error", err, "config_file", "/tmp/failed-config.cfg")
}
```

### Version Conflicts

**Diagnosis:**

1. Check for concurrent modifications
2. Verify transaction commit order
3. Review retry logic
4. Check API version compatibility

```go
// Log version conflicts
if isVersionConflict(err) {
    log.Warn("version conflict detected",
        "attempt", attempt,
        "endpoint", endpoint,
        "error", err,
    )
}
```

## Resources

- API documentation: `pkg/dataplane/README.md`
- client-native docs: <https://github.com/haproxytech/client-native>
- Dataplane API docs: <https://www.haproxy.com/documentation/haproxy-data-plane-api/>
- HAProxy config manual: <https://www.haproxy.com/documentation/haproxy-configuration-manual/latest/>
