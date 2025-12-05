# Task 1: Structured Config - Parsing, Comparison, and Intelligent Synchronization

## Overview

This task implements the core functionality for intelligent HAProxy configuration management through structured parsing, fine-grained comparison, and optimal synchronization via the Dataplane API. This is the trickiest and most critical part of the project, taken on first to validate the approach risk-first.

**Why this matters:**

- Enables zero-reload deployments by using specific Dataplane API endpoints
- Avoids unnecessary HAProxy process restarts through fine-grained updates
- Provides foundation for all configuration management operations

## Objectives

Implement six key capabilities:

1. **Parse HAProxy configurations** from strings into structured representations
2. **Fetch current configurations** from Dataplane API servers
3. **Compare structured configurations** and extract differences
4. **Generate fine-grained API operations** for optimal synchronization
5. **Execute operations** with transaction management and error handling
6. **Validate with integration tests** using Kind clusters and real HAProxy instances

## Architecture

### Package Structure

Following the design document's architecture:

```
pkg/
├── validation/
│   └── parser/                    # Configuration parsing
│       ├── parser.go              # Parse config string → structured
│       └── types.go               # Parser configuration
│
├── dataplane/
│   ├── client/                    # Dataplane API client
│   │   ├── client.go              # Main HTTP client wrapper
│   │   ├── adapter.go             # Version mgmt + 409 retry logic
│   │   ├── transaction.go         # Transaction lifecycle
│   │   ├── config.go              # Config fetch/push operations
│   │   └── operations/            # Fine-grained API operations
│   │       ├── backend.go         # Backend operations
│   │       ├── server.go          # Server operations
│   │       ├── frontend.go        # Frontend operations
│   │       ├── acl.go             # ACL operations
│   │       ├── rule.go            # Rule operations
│   │       └── ...                # Other section operations
│   │
│   ├── comparator/                # Configuration comparison
│   │   ├── comparator.go          # Main comparison engine
│   │   ├── diff.go                # Diff types and operations
│   │   ├── sections/              # Per-section comparators
│   │   │   ├── global.go          # Global section comparison
│   │   │   ├── defaults.go        # Defaults comparison
│   │   │   ├── frontend.go        # Frontend comparison
│   │   │   ├── backend.go         # Backend comparison
│   │   │   ├── server.go          # Server comparison
│   │   │   └── ...                # Other sections
│   │   └── operations.go          # Operation types
│   │
│   ├── synchronizer/              # Deployment orchestration
│   │   ├── synchronizer.go        # Sync coordinator
│   │   ├── policy.go              # BestEffort vs AllOrNothing
│   │   ├── executor.go            # Operation execution
│   │   └── ordering.go            # Dependency ordering
│   │
│   └── types/                     # Shared types
│       ├── config.go              # Configuration types
│       ├── endpoint.go            # DataplaneEndpoint
│       ├── operation.go           # Operation interfaces
│       └── result.go              # Sync results
│
└── tests/
    └── integration/               # Integration tests
        ├── kind_cluster.go        # Kind cluster management
        ├── haproxy_setup.go       # HAProxy pod deployment
        ├── sync_basic_test.go     # Basic sync scenarios
        ├── sync_complex_test.go   # Complex config scenarios
        ├── sync_errors_test.go    # Error handling tests
        └── testdata/              # Test configurations
            ├── basic.cfg
            ├── complex.cfg
            └── ...
```

### Component Relationships

```
┌─────────────────────────────────────────────────────────────┐
│                      Synchronizer                           │
│  (Orchestrates deployment, handles policies, ordering)      │
└────────────────┬───────────────────────┬────────────────────┘
                 │                       │
    ┌────────────▼────────────┐   ┌─────▼──────────┐
    │      Comparator         │   │  Client/Adapter│
    │ (Generate operations)   │   │ (Execute ops)  │
    └────────────┬────────────┘   └────────────────┘
                 │                         │
                 │                         │
          ┌──────▼─────────┐       ┌──────▼──────────┐
          │    Parser      │       │ Dataplane API   │
          │ (client-native)│       │  (HTTP/REST)    │
          └────────────────┘       └─────────────────┘
```

## Implementation Steps

### Step 1: Configuration Parsing

**Location**: `pkg/validation/parser/`

**Dependencies**:

```go
import (
    "github.com/haproxytech/client-native/v6/configuration"
    "github.com/haproxytech/client-native/v6/models"
)
```

**Files to create**:

#### `parser.go`

```go
type Parser struct {
    parser parser.Parser
}

// ParseFromString parses HAProxy config from string → structured StructuredConfig
// Syntax validation happens automatically during parsing
func (p *Parser) ParseFromString(config string) (*StructuredConfig, error)
```

**Key functionality**:

- Parse config strings (not files) into `StructuredConfig`
- Use client-native's parser (do NOT implement custom parser)
- Return structured representation suitable for comparison
- Syntax validation is automatic during parsing (semantic validation via Dataplane API)

---

### Step 2: Dataplane API Client

**Location**: `pkg/dataplane/client/`

**Dependencies**:

- **Option A**: `github.com/form3tech-oss/haproxy-data-plane-api-client`
- **Option B**: Generate from OpenAPI spec at <https://www.haproxy.com/documentation/dataplaneapi/community/v3_specification.yaml>
- **Decision**: Research both options, choose based on completeness and active maintenance

**First**: Check if client-native provides HTTP client functionality. If yes, prefer that.

**Files to create**:

#### `client.go`

```go
type DataplaneClient struct {
    baseURL    string
    httpClient *http.Client
    username   string
    password   string
}

// FetchConfiguration retrieves current config via /v2/services/haproxy/configuration/raw
func (c *DataplaneClient) FetchConfiguration() (string, error)

// GetVersion retrieves current configuration version
func (c *DataplaneClient) GetVersion() (int64, error)
```

#### `adapter.go`

```go
// VersionAdapter wraps client with automatic version management and 409 retry
type VersionAdapter struct {
    client *DataplaneClient
    maxRetries int
}

// ExecuteWithRetry executes operation with automatic 409 retry
// Extracts version from "Configuration-Version" header on 409 response
func (a *VersionAdapter) ExecuteWithRetry(op Operation) error
```

#### `transaction.go`

```go
type Transaction struct {
    id      string
    version int64
    client  *DataplaneClient
}

// CreateTransaction creates new transaction with current version
// POST /v2/services/haproxy/transactions?version={version}
func CreateTransaction(client *DataplaneClient, version int64) (*Transaction, error)

// Commit commits all changes in transaction
// PUT /v2/services/haproxy/transactions/{id}
func (t *Transaction) Commit() error

// Abort rolls back all changes in transaction
// DELETE /v2/services/haproxy/transactions/{id}
func (t *Transaction) Abort() error
```

#### `operations/*.go`

Comprehensive endpoint coverage for ALL config sections:

**Core Sections**:

- `global.go` - Global settings operations
- `defaults.go` - Defaults section operations
- `frontend.go` - Frontend CRUD operations
- `backend.go` - Backend CRUD operations
- `server.go` - Server add/update/delete in backends

**Routing & Logic**:

- `bind.go` - Frontend bind operations
- `acl.go` - ACL definition operations
- `http_request_rule.go` - HTTP request rule operations
- `http_response_rule.go` - HTTP response rule operations
- `tcp_request_rule.go` - TCP request rule operations
- `tcp_response_rule.go` - TCP response rule operations
- `switching_rule.go` - Backend switching rules
- `use_backend.go` - Use backend rules

**Runtime Operations**:

- `map.go` - Runtime map operations
- `stick_table.go` - Stick table operations
- `server_template.go` - Server template operations

**Advanced Features**:

- `filter.go` - HTTP filter operations (compression, cache, etc.)
- `cache.go` - Cache section operations
- `mailer.go` - Mailer section operations
- `peer.go` - Peer section operations
- `resolver.go` - DNS resolver operations
- `ring.go` - Ring buffer operations

**Logging & Errors**:

- `log_target.go` - Log target operations
- `http_errors.go` - HTTP error file operations

**Each operation file provides**:

```go
type BackendOperation interface {
    Execute(client *DataplaneClient, txID string) error
}

type CreateBackend struct {
    Backend *models.Backend
}

type UpdateBackend struct {
    Name    string
    Backend *models.Backend
}

type DeleteBackend struct {
    Name string
}
```

**Key functionality**:

- Fetch current config via raw endpoint: `GET /v2/services/haproxy/configuration/raw`
- Get current version: `GET /v2/services/haproxy/configuration/version`
- Auto-manage version parameter
- **409 handling**: Retry automatically, extract new version from `Configuration-Version` response header
- Transaction lifecycle: create → operations → commit/abort
- Comprehensive API endpoint coverage for all config sections

---

### Step 3: Configuration Comparison

**Location**: `pkg/dataplane/comparator/`

**Files to create**:

#### `comparator.go`

```go
type Comparator struct {
    sectionComparators map[string]SectionComparator
}

// Compare performs deep structural comparison
// Returns minimal set of operations needed to transform current → desired
func (c *Comparator) Compare(current, desired *models.Configuration) (*ConfigDiff, error)
```

#### `diff.go`

```go
type ConfigDiff struct {
    Operations []Operation
    Summary    DiffSummary
}

type DiffSummary struct {
    GlobalChanged      bool
    DefaultsChanged    bool
    FrontendsAdded     []string
    FrontendsModified  []string
    FrontendsDeleted   []string
    BackendsAdded      []string
    BackendsModified   []string
    BackendsDeleted    []string
    ServersAdded       map[string][]string  // backend → server names
    ServersModified    map[string][]string
    ServersDeleted     map[string][]string
    // ... similar for all sections
}
```

#### `sections/*.go`

Per-section comparison logic with **attribute-level granularity**:

**`sections/backend.go`** - Example implementation:

```go
type BackendComparator struct{}

func (bc *BackendComparator) Compare(current, desired *models.Backend) []Operation {
    ops := []Operation{}

    // Compare each attribute individually
    if current.Balance.Algorithm != desired.Balance.Algorithm {
        ops = append(ops, &UpdateBackendBalance{
            Name:      desired.Name,
            Algorithm: desired.Balance.Algorithm,
        })
    }

    if current.Mode != desired.Mode {
        ops = append(ops, &UpdateBackendMode{
            Name: desired.Name,
            Mode: desired.Mode,
        })
    }

    // Compare timeouts
    if !equalTimeouts(current, desired) {
        ops = append(ops, &UpdateBackendTimeouts{
            Name:     desired.Name,
            Timeouts: desired.Timeouts,
        })
    }

    // Compare servers (delegate to ServerComparator)
    serverOps := compareServers(desired.Name, current.Servers, desired.Servers)
    ops = append(ops, serverOps...)

    return ops
}
```

**`sections/server.go`** - Server-level comparison:

```go
type ServerComparator struct{}

func (sc *ServerComparator) Compare(backend string, current, desired []*models.Server) []Operation {
    ops := []Operation{}

    currentMap := indexByName(current)
    desiredMap := indexByName(desired)

    // Additions
    for name, server := range desiredMap {
        if _, exists := currentMap[name]; !exists {
            ops = append(ops, &CreateServer{
                Backend: backend,
                Server:  server,
            })
        }
    }

    // Modifications (attribute-level)
    for name, desiredServer := range desiredMap {
        if currentServer, exists := currentMap[name]; exists {
            if currentServer.Address != desiredServer.Address {
                ops = append(ops, &UpdateServerAddress{
                    Backend: backend,
                    Name:    name,
                    Address: desiredServer.Address,
                })
            }
            if currentServer.Weight != desiredServer.Weight {
                ops = append(ops, &UpdateServerWeight{
                    Backend: backend,
                    Name:    name,
                    Weight:  desiredServer.Weight,
                })
            }
            // ... compare all server attributes
        }
    }

    // Deletions
    for name := range currentMap {
        if _, exists := desiredMap[name]; !exists {
            ops = append(ops, &DeleteServer{
                Backend: backend,
                Name:    name,
            })
        }
    }

    return ops
}
```

**Complete section coverage**:

- `global.go` - Global section comparison
- `defaults.go` - Defaults section comparison
- `frontend.go` - Frontend comparison (settings + binds + ACLs + rules)
- `backend.go` - Backend comparison (settings + servers + rules)
- `server.go` - Server comparison (all server attributes)
- `bind.go` - Bind comparison
- `acl.go` - ACL comparison
- `rule.go` - HTTP/TCP rule comparison
- `filter.go` - Filter comparison
- `cache.go`, `mailer.go`, `peer.go`, `resolver.go`, `ring.go`
- `log.go`, `http_errors.go`

**Key functionality**:

- **Fine-grained comparison** at attribute level (not section level)
- Deep equality checks for all config elements
- Generate minimal change set - only changed attributes
- Map differences to specific API operations
- Handle nested structures (e.g., backend → servers → server.weight)

---

### Step 4: Operation Generation

**Location**: `pkg/dataplane/comparator/operations.go`

**Operation type system**:

```go
type OperationType int

const (
    OperationCreate OperationType = iota
    OperationUpdate
    OperationDelete
)

type Operation interface {
    Type() OperationType
    Section() string              // "backend", "server", "acl", etc.
    Priority() int                // For dependency ordering
    Execute(client *DataplaneClient, txID string) error
    Describe() string             // Human-readable description
}
```

**Example operations**:

```go
// Backend operations
type CreateBackend struct {
    Backend *models.Backend
}

type UpdateBackendBalance struct {
    Name      string
    Algorithm string
}

type DeleteBackend struct {
    Name string
}

// Server operations
type CreateServer struct {
    Backend string
    Server  *models.Server
}

type UpdateServerWeight struct {
    Backend string
    Name    string
    Weight  *int64
}

type UpdateServerAddress struct {
    Backend string
    Name    string
    Address string
    Port    int
}

type DeleteServer struct {
    Backend string
    Name    string
}

// Frontend operations
type CreateFrontend struct {
    Frontend *models.Frontend
}

type AddBind struct {
    Frontend string
    Bind     *models.Bind
}

// ACL operations
type CreateACL struct {
    Parent    string  // frontend or backend name
    ParentType string // "frontend" or "backend"
    ACL       *models.ACL
}

// ... similar for all config sections
```

**Mapping to Dataplane API endpoints**:

Each operation maps to the most specific endpoint available:

| Operation | Endpoint Example |
|-----------|------------------|
| CreateBackend | `POST /v2/services/haproxy/configuration/backends` |
| UpdateBackendBalance | `PUT /v2/services/haproxy/configuration/backends/{name}` (partial update) |
| CreateServer | `POST /v2/services/haproxy/configuration/backends/{backend}/servers` |
| UpdateServerWeight | `PUT /v2/services/haproxy/configuration/backends/{backend}/servers/{name}` |
| CreateACL | `POST /v2/services/haproxy/configuration/acls?parent_type=frontend&parent_name={frontend}` |
| CreateSwitchingRule | `POST /v2/services/haproxy/configuration/backend_switching_rules?frontend={name}` |

**Key functionality**:

- Map every config difference to most specific API endpoint
- Never use `/raw` endpoint (causes reload) unless fallback scenario
- Provide clear operation descriptions for logging
- Support priority-based ordering for dependency management

---

### Step 5: Synchronization Engine

**Location**: `pkg/dataplane/synchronizer/`

**Files to create**:

#### `synchronizer.go`

```go
type Synchronizer struct {
    client      *DataplaneClient
    adapter     *VersionAdapter
    comparator  *Comparator
    policy      DeploymentPolicy
}

// Sync orchestrates complete configuration synchronization
func (s *Synchronizer) Sync(ctx context.Context, desired *models.Configuration, endpoints []DataplaneEndpoint) (*SyncResult, error) {
    // For each endpoint:
    // 1. Fetch current config
    // 2. Parse both configs
    // 3. Compare → generate operations
    // 4. Order operations by dependencies
    // 5. Execute with transaction
}
```

#### `policy.go`

```go
type DeploymentPolicy int

const (
    // BestEffort: Continue deploying to remaining instances on failure
    BestEffort DeploymentPolicy = iota

    // AllOrNothing: Stop immediately on first failure
    AllOrNothing
)
```

#### `executor.go`

```go
// ExecuteOperations runs operations within a transaction
func ExecuteOperations(ctx context.Context, client *DataplaneClient, ops []Operation) error {
    // 1. Get current version
    version, err := client.GetVersion()

    // 2. Create transaction
    tx, err := CreateTransaction(client, version)
    // Handle 409: retry with new version

    // 3. Execute all operations with transaction_id
    for _, op := range ops {
        if err := op.Execute(client, tx.id); err != nil {
            tx.Abort()
            return err
        }
    }

    // 4. Commit transaction
    if err := tx.Commit(); err != nil {
        tx.Abort()
        return err
    }

    return nil
}
```

#### `ordering.go`

```go
// OrderOperations sorts operations by dependency requirements
func OrderOperations(ops []Operation) []Operation {
    // Priority-based sorting:
    // 1. Deletes (reverse order: servers → backends → frontends)
    // 2. Creates (forward order: frontends → backends → servers)
    // 3. Updates (any order - resources exist)

    sorted := make([]Operation, len(ops))

    // Group by type
    creates := filterByType(ops, OperationCreate)
    updates := filterByType(ops, OperationUpdate)
    deletes := filterByType(ops, OperationDelete)

    // Sort creates by priority (parents before children)
    sort.Slice(creates, func(i, j int) bool {
        return creates[i].Priority() < creates[j].Priority()
    })

    // Sort deletes by reverse priority (children before parents)
    sort.Slice(deletes, func(i, j int) bool {
        return deletes[i].Priority() > deletes[j].Priority()
    })

    // Combine: deletes → creates → updates
    copy(sorted, deletes)
    copy(sorted[len(deletes):], creates)
    copy(sorted[len(deletes)+len(creates):], updates)

    return sorted
}
```

**Transaction flow**:

```
1. Fetch current version from API
   ↓
2. Create transaction with version → get transaction_id
   ↓ (on 409: extract new version from header, retry)
   ↓
3. Execute operations with transaction_id
   ↓
4a. Success → Commit transaction
    ↓
    All changes applied atomically

4b. Error → Abort transaction
    ↓
    Rollback all changes
    ↓
    Return error or fallback to raw endpoint
```

**Error handling**:

| Error Type | Handling Strategy |
|------------|-------------------|
| 409 (version conflict) | Retry automatically with new version from header (up to N attempts) |
| 400 (validation error) | Abort transaction, return error with details |
| 500 (server error) | Abort transaction, return error |
| Unrecoverable | Abort transaction, optionally fallback to raw endpoint |

**Raw endpoint fallback** (last resort):

```go
func (s *Synchronizer) SyncWithRawEndpoint(client *DataplaneClient, config string) error {
    // Only used when:
    // 1. Config features not supported by structured API
    // 2. Transaction commit fails validation (semantic errors)
    // 3. All retry attempts exhausted

    // WARNING: This triggers HAProxy reload
    return client.PushRawConfiguration(config)
}
```

**Multi-instance deployment**:

```go
func (s *Synchronizer) SyncMultiple(ctx context.Context, desired *models.Configuration, endpoints []DataplaneEndpoint) (*MultiSyncResult, error) {
    results := make([]*SyncResult, len(endpoints))

    switch s.policy {
    case BestEffort:
        // Deploy to all instances, collect all results
        var wg sync.WaitGroup
        for i, ep := range endpoints {
            wg.Add(1)
            go func(idx int, endpoint DataplaneEndpoint) {
                defer wg.Done()
                results[idx] = s.syncSingle(ctx, desired, endpoint)
            }(i, ep)
        }
        wg.Wait()

    case AllOrNothing:
        // Stop on first failure
        for i, ep := range endpoints {
            result := s.syncSingle(ctx, desired, ep)
            results[i] = result
            if !result.Success {
                return &MultiSyncResult{Results: results}, result.Error
            }
        }
    }

    return &MultiSyncResult{Results: results}, nil
}
```

**Key functionality**:

- Orchestrate complete sync workflow
- Handle transaction lifecycle
- Order operations by dependencies
- Error propagation through chain
- Raw endpoint as fallback only
- 409 automatic retry
- Multi-instance deployment with policies
- Comprehensive result reporting

---

### Step 6: Integration Tests

**Location**: `tests/integration/`

**Dependencies**:

```go
import (
    "sigs.k8s.io/kind/pkg/cluster"
    "sigs.k8s.io/kind/pkg/apis/config/v1alpha4"
)
```

**Files to create**:

#### `kind_cluster.go`

```go
// SetupKindCluster creates a Kind cluster for testing
func SetupKindCluster(t *testing.T) (*KindCluster, error)

// TeardownKindCluster destroys the test cluster
func (kc *KindCluster) Teardown() error
```

#### `haproxy_setup.go`

```go
// DeployHAProxy deploys HAProxy pod with Dataplane API sidecar
// Reference the Python project for container images and configuration
func DeployHAProxy(t *testing.T, cluster *KindCluster) (*HAProxyInstance, error)

// GetDataplaneEndpoint returns endpoint for Dataplane API
func (hi *HAProxyInstance) GetDataplaneEndpoint() DataplaneEndpoint
```

#### Test scenarios

**`sync_basic_test.go`** - Fundamental operations:

```go
func TestSyncEmptyToSimple(t *testing.T) {
    // Empty config → 1 backend with 2 servers
}

func TestSyncAddServer(t *testing.T) {
    // Start with 1 backend, 1 server
    // Add second server
    // Verify only 1 API call (AddServer)
}

func TestSyncUpdateServerWeight(t *testing.T) {
    // Modify only server weight
    // Verify only weight update call
}

func TestSyncDeleteServer(t *testing.T) {
    // Remove server
    // Verify deletion call
}

func TestSyncModifyBackendBalance(t *testing.T) {
    // Change balance algorithm
    // Verify only backend balance update
}
```

**`sync_complex_test.go`** - Comprehensive config coverage:

```go
func TestSyncCompleteHAProxyConfig(t *testing.T) {
    // Full config with:
    // - Multiple frontends with different binds
    // - Multiple backends with server pools
    // - ACLs and routing rules
    // - HTTP request/response rules
    // - Filters, caches
    // - Custom error pages
}

func TestSyncAllConfigSections(t *testing.T) {
    // Test every supported config section:
    // global, defaults, frontends, backends, servers,
    // binds, ACLs, rules, filters, caches, mailers,
    // peers, resolvers, rings, logs, HTTP errors
}

func TestSyncMultipleFrontends(t *testing.T) {
    // HTTP and HTTPS frontends with different configs
}

func TestSyncMultipleBackends(t *testing.T) {
    // Various balance algorithms, health checks, timeouts
}

func TestSyncACLsAndRules(t *testing.T) {
    // Complex routing logic with ACLs and switching rules
}
```

**`sync_errors_test.go`** - Error handling:

```go
func TestSync409Retry(t *testing.T) {
    // Simulate concurrent modification
    // Verify automatic retry with new version
}

func TestSyncTransactionAbort(t *testing.T) {
    // Force validation error mid-transaction
    // Verify transaction is aborted
    // Verify no partial changes applied
}

func TestSyncInvalidConfig(t *testing.T) {
    // Submit invalid config
    // Verify Dataplane API rejects it
    // Verify transaction is aborted
}

func TestSyncRawFallback(t *testing.T) {
    // Use config feature not supported by structured API
    // Verify fallback to raw endpoint
}
```

**`sync_multi_instance_test.go`** - Multi-instance deployment:

```go
func TestSyncBestEffort(t *testing.T) {
    // Deploy to 3 instances, make 2nd fail
    // Verify 1st and 3rd succeed
    // Verify all results collected
}

func TestSyncAllOrNothing(t *testing.T) {
    // Deploy to 3 instances, make 2nd fail
    // Verify deployment stops at 2nd
    // Verify 3rd not attempted
}

func TestSyncParallelDeployment(t *testing.T) {
    // Deploy to 10 instances in parallel
    // Verify all succeed
    // Verify timing is parallel (not sequential)
}
```

**Test data structure**:

```
tests/integration/testdata/
├── basic/
│   ├── empty.cfg
│   ├── one-backend.cfg
│   └── two-backends.cfg
├── complex/
│   ├── full-production.cfg
│   ├── multi-frontend.cfg
│   ├── acl-routing.cfg
│   └── ssl-termination.cfg
├── invalid/
│   ├── syntax-error.cfg
│   └── semantic-error.cfg
└── edge-cases/
    ├── large-server-pool.cfg
    ├── nested-acls.cfg
    └── complex-rules.cfg
```

**Test flow example**:

```go
func TestCompleteSyncWorkflow(t *testing.T) {
    // 1. Setup Kind cluster
    cluster, err := SetupKindCluster(t)
    require.NoError(t, err)
    defer cluster.Teardown()

    // 2. Deploy HAProxy pod
    haproxy, err := DeployHAProxy(t, cluster)
    require.NoError(t, err)

    // 3. Load desired config
    desiredConfig := loadTestConfig(t, "testdata/complex/full-production.cfg")

    // 4. Parse desired config → structured
    parser := NewParser()
    desiredStructured, err := parser.ParseFromString(desiredConfig)
    require.NoError(t, err)

    // 5. Fetch current config from HAProxy
    client := NewDataplaneClient(haproxy.GetDataplaneEndpoint())
    currentConfig, err := client.FetchConfiguration()
    require.NoError(t, err)

    // 6. Parse current config → structured
    currentStructured, err := parser.ParseFromString(currentConfig)
    require.NoError(t, err)

    // 7. Compare configs → diff
    comparator := NewComparator()
    diff, err := comparator.Compare(currentStructured, desiredStructured)
    require.NoError(t, err)

    // 8. Sync via synchronizer
    synchronizer := NewSynchronizer(client, BestEffort)
    result, err := synchronizer.Sync(context.Background(), desiredStructured,
        []DataplaneEndpoint{haproxy.GetDataplaneEndpoint()})
    require.NoError(t, err)
    require.True(t, result.Success)

    // 9. Verify: fetch again and compare
    finalConfig, err := client.FetchConfiguration()
    require.NoError(t, err)

    finalStructured, err := parser.ParseFromString(finalConfig)
    require.NoError(t, err)

    // 10. Assert semantic equality
    assertConfigsEqual(t, desiredStructured, finalStructured)
}
```

**Key test requirements**:

- Spin up Kind Kubernetes cluster
- Deploy HAProxy pod with Dataplane API sidecar
- Test 100+ config variations
- Cover all supported config sections
- Test error scenarios (409, validation failures, etc.)
- Test multi-instance deployment
- Verify semantic equality after sync

---

## Technical Decisions

### Library Choices

**Configuration Parsing**:

- ✅ **client-native/v6**: Use `github.com/haproxytech/client-native/v6`
- Includes integrated config parser
- Actively maintained (v6 released May 2025)
- Same structs used by Dataplane API
- Do NOT implement custom parser

**API Client**:

- 🔍 **Research both options**:
  - Option A: `github.com/form3tech-oss/haproxy-data-plane-api-client` (third-party)
  - Option B: Generate from OpenAPI spec v3 using `oapi-codegen` or similar
- 🔍 **First check**: Does client-native provide HTTP client? If yes, prefer that.
- **Decision criteria**: Completeness, maintenance status, ease of use

**Testing**:

- ✅ **Kind**: `sigs.k8s.io/kind` for Kubernetes cluster
- ✅ **testify**: `github.com/stretchr/testify` for assertions
- 📋 **Container image**: Check Python project for HAProxy + Dataplane API image

### Version Management

**Optimistic locking pattern**:

1. Fetch current version: `GET /v2/services/haproxy/configuration/version`
2. Include version in transaction creation: `POST /v2/services/haproxy/transactions?version={version}`
3. On 409 response:
   - Extract new version from `Configuration-Version` response header
   - Retry transaction creation with new version
   - Maximum 3 retry attempts (configurable)

**Automatic injection**:

- Adapter automatically manages version parameter
- No transaction_id → inject version parameter
- Has transaction_id → use it (no version needed)

### Error Handling

**Recoverable errors**:

- **409 (Conflict)**: Automatic retry with new version
- **Transient failures**: Configurable retry policy

**Unrecoverable errors**:

- **400 (Bad Request)**: Validation error, abort transaction, return error
- **500 (Server Error)**: Abort transaction, return error
- **Unsupported features**: Abort transaction, optionally fallback to raw endpoint

**Raw endpoint fallback**:
Only use `/raw` endpoint (which triggers reload) for:

- Config features not supported by structured API
- Semantic validation failures after all retries
- NOT for 409 conflicts (those are recoverable)

### State Management

**Stateless operation**:

- Always fetch current config from Dataplane API
- No persistent tracking of last-applied state
- Comparison is always: current (fetched) vs. desired (rendered)
- More reliable, handles external changes

**Benefits**:

- No state synchronization issues
- Handles manual config changes via API
- Simpler implementation
- More robust

### Comparison Granularity

**Fine-grained attribute-level comparison**:

- Compare individual config attributes, not entire sections
- Example: If only server weight changed, generate single `UpdateServerWeight` operation
- Minimizes API calls and reload triggers
- Maps to most specific endpoint available

**Operation mapping**:

- Server weight change → `PUT /backends/{backend}/servers/{name}` (single attribute)
- NOT: Replace entire backend → `PUT /backends/{backend}` (all settings + all servers)

### Dependency Ordering

**Operation execution order**:

1. **Deletes** (reverse dependency order):
   - Delete servers first
   - Then delete backends/frontends

2. **Creates** (forward dependency order):
   - Create backends/frontends first
   - Then create servers

3. **Updates** (any order):
   - Resources already exist
   - No dependencies

**Priority system**:

```go
// Lower priority = executed first for creates
// Higher priority = executed first for deletes

const (
    PriorityGlobal     = 10
    PriorityDefaults   = 20
    PriorityFrontend   = 30
    PriorityBackend    = 30
    PriorityBind       = 40
    PriorityServer     = 40
    PriorityACL        = 50
    PriorityRule       = 60
)
```

### Multi-Instance Deployment

**Policies** (from design doc):

- **BestEffort**: Continue deploying to remaining instances on failure, collect all results
- **AllOrNothing**: Stop immediately on first failure

**Implementation**:

- Parallel deployment using goroutines for BestEffort
- Sequential with early exit for AllOrNothing
- Comprehensive result reporting per instance

---

## Success Criteria

### Deliverables

1. ✅ **Parser implementation** (`pkg/validation/parser/`)
   - Parse any valid HAProxy config from string
   - Return structured `models.Configuration`
   - Syntax validation (semantic validation via Dataplane API)

2. ✅ **API client with transaction support** (`pkg/dataplane/client/`)
   - Fetch current config
   - Version management and 409 retry
   - Transaction lifecycle (create/commit/abort)
   - Comprehensive API endpoint coverage

3. ✅ **Fine-grained comparator** (`pkg/dataplane/comparator/`)
   - Attribute-level comparison for all config sections
   - Generate minimal operation set
   - Handle nested structures

4. ✅ **Synchronizer with policies** (`pkg/dataplane/synchronizer/`)
   - Operation ordering by dependencies
   - Transaction-based execution
   - Error handling and raw fallback
   - Multi-instance deployment

5. ✅ **Comprehensive integration tests** (`tests/integration/`)
   - Kind cluster setup
   - 100+ test scenarios
   - All config sections covered
   - Error scenarios validated

### Validation

**The integration tests must prove**:

1. Parse diverse HAProxy configs correctly
2. Fetch current config from real HAProxy instances
3. Compare configs and generate correct operations
4. Sync successfully with transaction management
5. Handle 409 conflicts automatically
6. Result in semantically equivalent configs
7. Support all major config sections
8. Handle errors gracefully
9. Deploy to multiple instances correctly

### Metrics

- **Config sections supported**: 20+ (all major sections)
- **Test scenarios**: 100+ variations
- **API endpoint coverage**: Comprehensive (all specialized endpoints)
- **Error handling**: 409 retry, validation errors, transaction abort
- **Deployment policies**: BestEffort + AllOrNothing

---

## Progress

### Completed

- [ ] Step 1: Configuration parsing with client-native
- [ ] Step 2: Dataplane API client with transactions
- [ ] Step 3: Fine-grained configuration comparator
- [ ] Step 4: Operation generation and mapping
- [ ] Step 5: Synchronization engine with policies
- [ ] Step 6: Integration tests with Kind clusters

### Current Focus

- [ ] Research library options (client-native HTTP client vs. third-party vs. generated)
- [ ] Design operation type system
- [ ] Implement parser wrapper

### Blockers

None currently.

### Notes

- Task 1 is the foundation for all config management
- Focus on comprehensive coverage from the start (Option A)
- Integration tests are critical - they validate the entire workflow
- Reference Python/Rust implementations for patterns and container images
