# pkg/dataplane - HAProxy Integration

Development context for HAProxy Dataplane API integration.

**API Documentation**: See `pkg/dataplane/README.md`
**Architecture**: See `/docs/controller/docs/development/design.md` (design documentation index)

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
├── auxiliaryfiles/             # Auxiliary file management (maps, SSL, general, crt-list, SSL-CA)
├── client/                     # Dataplane API client + multi-version dispatch
├── comparator/                 # Fine-grained configuration comparison
│   └── sections/               # Per-section comparators (frontends, backends, rules, ...)
├── parser/                     # Config parsing using client-native
│   └── enterprise/             # Enterprise section parsing
├── validators/                 # OpenAPI schema validators with cached-result wrapping
├── capabilities.go             # HAProxy version → Capabilities (CRT lists, WAF, ...)
├── checksum.go                 # ComputeContentChecksum (config + auxFiles → SHA-256)
├── config.go                   # Public Endpoint / SyncOptions / SyncResult types
├── dataplane.go                # Public API (NewClient, Client.Sync, Client.SyncRuntimeFast)
├── errors.go                   # ParseError / ValidationError / ...
├── orchestrator.go             # Sync workflow coordination + result aggregation
│   orchestrator_*.go           #   (split across auxiliary / comparison / execution /
│                               #    rawpush / results / runtime files)
├── paths.go                    # ValidationPaths / DefaultValidationPaths
├── phases.go                   # Phase string constants
├── result.go                   # SyncResult + diagnostic helpers
├── validate_haproxy.go         # Three-phase validation
│   validate_schema.go          #   (haproxy -c, OpenAPI schema, syntax via client-native)
│   validate_syntax.go
├── validator.go                # ValidateConfiguration entry point
└── version.go                  # Version detection + parsing
```

There is no `transform/` or `types/` subpackage — earlier versions of this doc listed them but those directories never existed (or were removed). Use `go doc ./pkg/dataplane/...` for the authoritative export list as the layout grows.

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
  - Compare with current config to compute a fine-grained diff
  - Classify the diff: runtime-eligible server fields vs. structural
  - Push the full config in one request (no per-operation transactions):
    runtime-eligible-only -> skip-reload raw push + X-Runtime-Actions (zero-reload);
    otherwise -> force-reload raw push

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

- Server **field** updates limited to `weight`, `address`, `port`, `maintenance`, `agent-check`, `agent-addr`, `agent-send`, `health_check_port` (the canonical list lives in `serverRuntimeSupportedJSONFields` in `pkg/dataplane/comparator/sections/factory_server.go`). Server `enabled`/`disabled` is the `maintenance` field, so flipping reserved slots between active and disabled is reload-free.
- Frontend `Maxconn` updates
- Map file content updates (Storage API)
- SSL certificate content updates (Storage API + `set ssl cert`)

**Structural changes (requires reload):**

- Server **creation** and **deletion** (adding or removing a server triggers a reload — only field updates on existing servers are runtime-eligible)
- Frontend / backend creation, deletion, or attribute changes outside the runtime allow-list
- Bind address changes
- Global / defaults modifications
- ACLs, HTTP / TCP rules, filters, captures, stick rules, health checks (no runtime API support)

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
    server SRV_3 192.0.2.1:1 disabled       # Reserved slot
```

This allows endpoint changes (pod IP/port) and server state changes (enabled/disabled) to be applied via runtime API without reloading HAProxy.

## Multi-Version API Support

The client supports HAProxy DataPlane API versions v3.0, v3.1, v3.2, and v3.3 simultaneously through runtime version detection and a centralized dispatcher pattern. The capability matrix tables below predate v3.3; treat v3.3 as a superset of v3.2 unless the section comparator explicitly opts out — consult `pkg/dataplane/capabilities.go` and the per-version client packages (`pkg/generated/.../v33`) for the authoritative answer.

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
// Detect local HAProxy version (runs `haproxy -v` via the installed
// HAProxyExecutor — see haproxy_exec.go; no context arg).
localVersion, err := dataplane.DetectLocalVersion()
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

All client methods use a centralized dispatcher to route calls to the appropriate version-specific client. This eliminates repetitive switch-case logic across the ~60 public `*DataplaneClient` methods (`grep -E "^func \(c \*DataplaneClient\)" pkg/dataplane/client/*.go` for the current count).

**Architecture:**

```
┌─────────────────────┐
│  Public Methods     │  GetAllMapFiles(), CreateSSLCertificate(), etc.
│  (~60 methods)      │
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
│  Version Clients    │  v30.Client, v31.Client, v32.Client, v33.Client
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

Manages the HTTP client and multi-version dispatch:

```go
// DataplaneClient (one per HAProxy endpoint)
dpClient, err := client.New(ctx, &client.Config{
    BaseURL:  "http://haproxy:5555/v3",
    Username: "admin",
    Password: "pass",
})
defer dpClient.Close()
```

There is **no** transaction API — configuration changes are full-config pushes via `PushRawConfiguration` / `PushRawConfigurationSkipReload`.

**When to modify:**

- Adding new Dataplane API endpoint support → look at the dispatcher pattern below
- Changing connection-retry behavior → `client.WithRetry` / `client.RetryConfig` in `pkg/dataplane/client/retry.go`
- Improving error handling → `errors.go`

**Common pitfall**: Issuing per-section API calls for a configuration change. Production changes are full-config raw pushes — reach for `client.Sync` / `PushRawConfiguration` / `PushRawConfigurationSkipReload`.

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

All section operations (Create, Delete, Update) use a single generic operation descriptor defined in `operations_generic.go` and section-specific factories in `factory_*.go`. The current generation does **not** generate one struct per `(section, op)` pair — there is no `CreateBackendOperation` type. Instead each section's factory builds a `genericOp` with closures that supply the section-specific description.

Every operation except the data-carrying `ServerUpdateOp` is one `genericOp` (`opType`, `sectionName`, `describeFn`; no execute closure), built via `newOp(...)`. The distinction between section shapes lives only in the factories: the four CRUD builders in `crud_builders.go` (`TopLevelCRUD` / `ContainerChildCRUD` / `IndexChildCRUD` / `NameChildCRUD`) differ in call arity and the `nameFn`/describer they take, while singletons (global, traces, waf_global) call `newOp(...)` directly. None of them produces a distinct operation type.

Each section file (`factory_backend.go`-equivalent inside `factory_sections.go`, plus `factory_acl.go`, `factory_bind.go`, `factory_server.go`, `factory_http_rules.go`, `factory_filter_log.go`, `factory_switching.go`, `factory_tcp.go`, `factory_quic.go`, `factory_ee.go`) calls one of the generic constructors with:

- The model from `client-native` (e.g. `*models.Backend`)
- A transform that turns it into the unified `dataplaneapi.*` shape
Operations are pure descriptors (no execute closure). The orchestrator applies changes by pushing the full rendered config via `PushRawConfiguration` (for structural changes) or `PushRawConfigurationSkipReload` with runtime actions (for server field updates). There is no `executors/` subdirectory.

**Why JSON marshaling is required:**

HAProxy DataPlane API version-specific types (v30.Backend, v31.Backend, v32.Backend) are structurally incompatible - newer versions add fields that older versions lack. Direct struct conversion fails at compile time. JSON marshaling provides type conversion:

1. Marshal unified model (dataplaneapi.Backend) to JSON
2. Unmarshal JSON into version-specific type (v32.Backend, v31.Backend, v30.Backend)
3. Missing fields in older versions are ignored during unmarshaling
4. Extra fields in newer versions get zero values when converting from older formats

This pattern trades ~10µs per operation for type safety and compatibility across all HAProxy versions.

### Operation Execution (orchestrator)

The `synchronizer/` sub-package no longer exists. The orchestrator applies changes in two modes:

1. **Structural changes** (server creation/deletion, frontend/backend changes, etc.): pushed via `client.PushRawConfiguration`, which triggers a HAProxy reload.
2. **Runtime-eligible changes** (server address/port/maintenance/weight/agent-check fields): pushed via `client.PushRawConfigurationSkipReload` with X-Runtime-Actions header, which updates the live HAProxy instance without a reload.

All callers go through `client.Sync` / `PushRawConfiguration` / `PushRawConfigurationSkipReload`.

**Retry logic:**

The raw-push paths don't open Dataplane API transactions, so there's no 409 version-conflict retry loop. Instead the orchestrator wraps its version resolution and pushes in `client.WithRetry(ctx, client.RetryConfig{...})` with `RetryIf: client.IsConnectionError()` — transient connection failures (the master socket is briefly closed while HAProxy re-execs on reload) are retried; any other error propagates. `PushRawConfigurationSkipReload` additionally retries its runtime `set server …` actions across a concurrent reload, since those fail with a 500 / connection-refused while the `-S` stats socket is momentarily down.

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
- General files: `/etc/haproxy/general/`

**When to modify:**

- Adding new file type
- Changing storage locations
- Improving sync logic

## Public API

### Main Entry Points

All public entry points are per-endpoint — there's no built-in fan-out across endpoints in this package. Parallel deployment is the deployer's job in `pkg/controller/deployer`.

```go
// Create a long-lived client for an endpoint
client, err := dataplane.NewClient(ctx, endpoint)
defer client.Close()

// Sync configuration with auxiliary files and per-call options
result, err := client.Sync(ctx, desiredConfig, auxFiles, &dataplane.SyncOptions{...})
```

## Testing Strategies

### Unit Tests

Test individual components in isolation:

```go
// The comparator works over full *parser.StructuredConfig values; there is
// no CompareBackends helper. Parsing goes through *parser.Parser (no
// top-level parser.Parse function). Operation is an interface
// (Type / Section / Execute / Describe), not a struct with SectionName /
// Field fields.
func TestComparator_BackendBalanceUpdate(t *testing.T) {
    p, err := parser.New()
    require.NoError(t, err)
    current, err := p.ParseFromString(currentRaw)
    require.NoError(t, err)
    desired, err := p.ParseFromString(desiredRaw)
    require.NoError(t, err)

    cmp := comparator.New()
    diff, err := cmp.Compare(current, desired)
    require.NoError(t, err)

    require.NotEmpty(t, diff.Operations)
    op := diff.Operations[0]
    assert.Equal(t, sections.OperationUpdate, op.Type())
    assert.Contains(t, op.Section(), "backend")
    // For per-backend reasoning, the DiffSummary is more ergonomic than
    // walking the operations slice:
    assert.Contains(t, diff.Summary.BackendsModified, "api")
}
```

### Integration Tests

Test with real HAProxy and Dataplane API:

```go
func TestSync_Integration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    // Requires running HAProxy with Dataplane API. Endpoint lives on
    // pkg/dataplane (not a 'types' subpackage), and a Client syncs one
    // endpoint at a time; cross-endpoint fan-out is the deployer's
    // job in pkg/controller/deployer.
    endpoint := &dataplane.Endpoint{
        URL:      "http://localhost:5555/v3",
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

    client, err := dataplane.NewClient(t.Context(), endpoint)
    require.NoError(t, err)
    defer client.Close()

    result, err := client.Sync(t.Context(), config, nil, nil)
    require.NoError(t, err)
    assert.True(t, result.Success)
}
```

### Faking the HAProxy Binary (required in unit tests)

**Unit tests must never shell out to external binaries.** Both places this
package executes haproxy (`DetectLocalVersion` → `haproxy -v`, semantic
validation → `haproxy -c`) go through the `HAProxyExecutor` seam in
`haproxy_exec.go`. Any test package whose tests reach those paths installs the
fake once per package:

```go
func TestMain(m *testing.M) {
    restore := dataplanetest.InstallFakeHAProxy()
    code := m.Run()
    restore()
    os.Exit(code)
}
```

The default fake reports version 3.2.0 and accepts every config. Tests that
need haproxy to _reject_ a config simulate the verdict per test (safe only
because these packages don't use `t.Parallel`):

```go
t.Cleanup(dataplanetest.InstallFakeHAProxy(
    dataplanetest.WithRejectAll("parsing [haproxy.cfg:5] : unknown keyword 'invalid_directive'")))
```

Such tests verify error _plumbing_ (phase classification, message extraction,
caching), not haproxy's actual judgment — whether the real binary accepts or
rejects a given construct is integration-test territory. pkg/dataplane's own
internal tests can't import `dataplanetest` (cycle); they use the equivalent
local fake in `main_test.go` via `SetHAProxyExecutor`.

### Mock Testing

`pkg/dataplane` does not export a `Syncer`/`Deployer` interface for mocking; the consumer (typically `pkg/controller/deployer`) declares its own narrow interface at the use site and the test passes in a stub. The fake's `Sync` must mirror the real `(*Client).Sync` signature so the interface assertion compiles:

```go
// Define the interface where it's consumed (deployer-side, not here).
type DataplaneSyncer interface {
    Sync(ctx context.Context, desiredConfig string, auxFiles *dataplane.AuxiliaryFiles, opts *dataplane.SyncOptions) (*dataplane.SyncResult, error)
}

type fakeSyncer struct {
    sync func(ctx context.Context, desiredConfig string, auxFiles *dataplane.AuxiliaryFiles, opts *dataplane.SyncOptions) (*dataplane.SyncResult, error)
}

func (f *fakeSyncer) Sync(ctx context.Context, cfg string, aux *dataplane.AuxiliaryFiles, opts *dataplane.SyncOptions) (*dataplane.SyncResult, error) {
    return f.sync(ctx, cfg, aux, opts)
}
```

Wire the fake into the consuming component, then trigger reconciliation through the `EventBus`. The controller has no `NewController` constructor and no `Reconcile` method to call directly — coordination is event-driven. See `pkg/controller/deployer/component_test.go` for the real wiring.

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
// Called at the boundary between validation pipeline and the webhook
// response. The pipeline returns *PipelineError (with Phase + Cause);
// SimplifyValidationError unwraps and prettifies the underlying
// HAProxy / schema error for the user.
result, err := proposalValidator.ValidateSync(ctx, request)
if err != nil {
    simplified := dataplane.SimplifyValidationError(err)
    return false, simplified // (allowed, reason) for the webhook
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
// Same boundary as SimplifyValidationError, but for *PipelineError
// where Phase == "render". The fail() function inside templates is
// the most useful signal here — operators can put domain-specific
// messages in their templates and have them surface verbatim in the
// webhook response.
output, err := engine.Render(ctx, "haproxy.cfg", templateContext)
if err != nil {
    simplified := dataplane.SimplifyRenderingError(err)
    return false, simplified
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

### Bypassing Three-Phase Sync

**Problem**: Pushing the main config separately from its auxiliary files, then trying to upload referenced files later. HAProxy's semantic validation runs `haproxy -c` on the config that just came in — if that config references `maps/host.map` and the map isn't on disk yet, validation fails. The Dataplane API doesn't let you stage them: by the time the config lands, every referenced file already needs to exist.

```go
// Bad — pushes config standalone; aux files aren't staged
result, err := client.Sync(ctx, haproxyConfig, nil, nil)
// → semantic validation fails on the first map/cert reference
```

**Solution**: Build a single `*dataplane.AuxiliaryFiles` and pass it to `Sync` (or to `client.Sync`). The orchestrator sequences the three phases internally — pre-config aux upload, then config sync via a full-config raw push, then post-config cleanup of orphaned aux files.

```go
// Good — orchestrator handles all three phases
aux := &dataplane.AuxiliaryFiles{
    MapFiles:        []auxiliaryfiles.MapFile{...},
    SSLCertificates: []auxiliaryfiles.SSLCertificate{...},
    GeneralFiles:    []auxiliaryfiles.GeneralFile{...},
}
result, err := client.Sync(ctx, haproxyConfig, aux, nil)
```

There is no separate `client.SyncMaps` / `client.CleanupMaps` API; everything aux-related goes through `AuxiliaryFiles` + the orchestrator's three-phase workflow in `pkg/dataplane/orchestrator_*.go`. If you reach into `pkg/dataplane/auxiliaryfiles` directly, you're rebuilding what `client.Sync` already does.

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
// Bad — N standalone config pushes, each a separate HTTP round-trip that
// fetches the version and triggers its own reload. O(N) round-trips, O(N) reloads.
for _, backend := range backends {
    cfg := renderConfigWith(backend)
    dpClient.PushRawConfiguration(ctx, cfg, version) // a reload each time
}

// Good — render the full desired config once and push it in a single call;
// one round-trip, at most one reload. This is what the orchestrator's Sync does.
desired := renderFullConfig(backends)
_, err := dpClient.PushRawConfiguration(ctx, desired, version)
```

### Parallel Endpoint Sync

`dataplane.Client` is per-endpoint by design; cross-endpoint fan-out is the deployer's responsibility. Inside the controller, `pkg/controller/deployer.Component.deployToEndpoints` spawns one goroutine per endpoint via `sync.WaitGroup` (no across-endpoint cap — the raw-push model issues one config push per pod, so there is nothing to parallelize _within_ a single pod). For one-off scripts that need to push to multiple HAProxies in parallel with a global cap, an `errgroup` with `SetLimit(parallelism)` is the natural shape:

```go
g, gCtx := errgroup.WithContext(ctx)
g.SetLimit(parallelism)

results := make([]*dataplane.SyncResult, len(endpoints))
for i, endpoint := range endpoints {
    i, endpoint := i, endpoint
    g.Go(func() error {
        cli, err := dataplane.NewClient(gCtx, endpoint)
        if err != nil {
            return err
        }
        defer cli.Close()
        result, err := cli.Sync(gCtx, config, auxFiles, nil)
        results[i] = result
        return err
    })
}
if err := g.Wait(); err != nil {
    return err
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

### Dataplane API Connectivity

**Diagnosis:**

1. Check Dataplane API health
2. Verify network connectivity
3. Review HAProxy logs

```bash
# Check Dataplane API health (the controller talks v3 only — see pkg/dataplane/client/version.go)
curl -u admin:<password> http://haproxy-endpoint:5555/v3/info

# HAProxy logs (run from inside the haptic namespace)
kubectl logs -n haptic <haproxy-pod> -c haproxy
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
