# Package Structure

The application is organized into focused Go packages following clean architecture principles:

```
haproxy-template-ic/
├── cmd/
│   └── controller/          # Main entry point
│       └── main.go
├── pkg/
│   ├── core/                # Core functionality
│   │   ├── config/          # Configuration loading and validation
│   │   └── logging/         # Structured logging setup
│   ├── dataplane/           # HAProxy Dataplane API integration
│   │   ├── auxiliaryfiles/  # Auxiliary file management (general, SSL, maps)
│   │   ├── client/          # API client with transactions and retries
│   │   ├── comparator/      # Fine-grained config comparison
│   │   │   └── sections/    # Section-specific comparators (30+ files)
│   │   ├── discovery/       # HAProxy endpoint discovery
│   │   ├── parser/          # Config parser using client-native
│   │   ├── synchronizer/    # Operation execution logic
│   │   ├── transform/       # Model transformation (client-native ↔ Dataplane API)
│   │   ├── types/           # Public types (Endpoint, SyncOptions, etc.)
│   │   ├── config.go        # Public types (Endpoint, SyncOptions)
│   │   ├── dataplane.go     # Public API (Client, Sync, DryRun, Diff)
│   │   ├── errors.go        # Structured error types
│   │   ├── orchestrator.go  # Sync workflow orchestration
│   │   └── result.go        # Result types
│   ├── events/              # Event bus infrastructure
│   │   ├── bus.go           # EventBus with startup coordination
│   │   ├── request.go       # Scatter-gather pattern
│   │   └── ringbuffer/      # Generic ring buffer for event history
│   │       ├── ringbuffer.go         # Thread-safe circular buffer with generics
│   │       ├── ringbuffer_test.go    # Unit tests
│   │       ├── README.md             # API documentation
│   │       └── CLAUDE.md             # Development context
│   ├── introspection/       # Generic debug HTTP server infrastructure
│   │   ├── handlers.go      # HTTP handlers for /debug/vars endpoints
│   │   ├── http.go          # HTTP client utilities
│   │   ├── jsonpath.go      # JSONPath field selection
│   │   ├── registry.go      # Instance-based variable registry
│   │   ├── registry_test.go # Registry tests
│   │   ├── server.go        # HTTP server with graceful shutdown
│   │   ├── types.go         # Var interface and types
│   │   ├── var.go           # Built-in Var implementations
│   │   ├── README.md        # API documentation
│   │   └── CLAUDE.md        # Development context
│   ├── metrics/             # Prometheus metrics infrastructure
│   │   ├── server.go        # HTTP server for /metrics endpoint
│   │   ├── helpers.go       # Metric creation helpers
│   │   ├── server_test.go   # Server tests
│   │   ├── helpers_test.go  # Helper tests
│   │   ├── README.md        # API documentation
│   │   └── CLAUDE.md        # Development context
│   ├── k8s/                 # Kubernetes integration
│   │   ├── types/           # Core interfaces and types
│   │   ├── client/          # Kubernetes client wrapper with dynamic client
│   │   ├── indexer/         # JSONPath key extraction and field filtering
│   │   ├── store/           # Memory and cached store implementations
│   │   └── watcher/         # Resource watching with debouncing and sync tracking
│   ├── controller/          # Controller lifecycle
│   │   ├── commentator/     # Event commentator for domain-aware logging
│   │   ├── configchange/    # Configuration change handler
│   │   ├── configloader/    # Config parsing and loading
│   │   ├── credentialsloader/ # Credentials parsing and loading
│   │   ├── debug/           # Controller-specific debug variables
│   │   │   ├── events.go    # Event buffer implementation
│   │   │   ├── setup.go     # Variable registration
│   │   │   ├── state.go     # State snapshot variables
│   │   │   ├── vars.go      # Debug Var implementations (Config, Credentials, etc.)
│   │   │   ├── README.md    # API documentation
│   │   │   └── CLAUDE.md    # Development context
│   │   ├── deployer/        # Deployment orchestration (scheduler, executor, drift monitor)
│   │   ├── discovery/       # HAProxy pod discovery and endpoint management
│   │   ├── events/          # Domain-specific event types
│   │   ├── executor/        # Reconciliation orchestrator
│   │   ├── indextracker/    # Index synchronization tracking
│   │   ├── metrics/         # Prometheus metrics (event adapter)
│   │   │   ├── metrics.go   # Domain metric definitions
│   │   │   ├── component.go # Event adapter for metrics
│   │   │   ├── metrics_test.go
│   │   │   ├── component_test.go
│   │   │   ├── README.md    # API documentation
│   │   │   └── CLAUDE.md    # Development context
│   │   ├── reconciler/      # Reconciliation debouncer and trigger
│   │   ├── renderer/        # Template rendering component
│   │   ├── resourcewatcher/ # Resource watcher lifecycle management
│   │   ├── validator/       # Config validation (basic, template, jsonpath)
│   │   ├── controller.go    # Event coordination and startup orchestration
│   │   └── statecache.go    # Event-driven state tracking for debug endpoints
│   └── templating/          # Template engine library
│       ├── engine.go        # TemplateEngine with pre-compilation and rendering
│       ├── types.go         # Engine type definitions (EngineTypeGonja)
│       ├── errors.go        # Custom error types
│       ├── loader.go        # Template loading utilities
│       ├── loader_test.go   # Loader tests
│       ├── engine_test.go   # Unit tests
│       └── README.md        # Usage documentation
├── tests/                   # End-to-end tests
│   ├── acceptance/          # Acceptance tests with debug endpoint validation
│   │   ├── configmap_reload_test.go  # ConfigMap reload validation
│   │   ├── metrics_test.go  # Metrics endpoint validation
│   │   ├── debug_client.go  # Debug endpoint client for tests
│   │   ├── env.go           # Test environment setup
│   │   ├── fixtures.go      # Test resource management
│   │   ├── main_test.go     # Test suite entry point
│   │   ├── README.md        # Testing documentation
│   │   └── CLAUDE.md        # Development context
│   └── integration/         # Integration tests
│       ├── README.md        # Integration test documentation
│       └── CLAUDE.md        # Development context
└── tools/                   # Development tools
    └── linters/             # Custom linters
        └── eventimmutability/  # Event pointer receiver linter
            ├── analyzer.go  # Custom golangci-lint analyzer
            ├── analyzer_test.go
            └── README.md
```

## Package Responsibilities

**Core Packages:**

- `pkg/core/config`: Provides pure functions for parsing configuration from YAML strings and loading credentials from Kubernetes Secret data. Includes basic structural validation (port ranges, required fields). Does NOT implement watchers - the controller package instantiates watchers from pkg/k8s and coordinates validation using scatter-gather pattern.
- `pkg/core/logging`: Structured logging setup using Go's standard library `log/slog` package with configurable levels and formats

**Event Bus Infrastructure:**

- `pkg/events`: Generic event bus providing pub/sub and request-response (scatter-gather) patterns for component coordination. Domain-agnostic infrastructure that could be extracted as standalone library.
- `pkg/events/ringbuffer`: Generic thread-safe ring buffer implementation using Go generics. Fixed-size circular buffer with automatic old-item eviction. O(1) add, O(n) retrieval. Used by EventCommentator and EventBuffer for event history tracking. Domain-agnostic, can be used with any type.

**Runtime Introspection Infrastructure:**

- `pkg/introspection`: Generic HTTP debug server infrastructure for exposing internal application state. Provides instance-based variable registry (not global like expvar), HTTP handlers for `/debug/vars` endpoints, JSONPath field selection support, Go profiling integration (`/debug/pprof`), and graceful shutdown. Domain-agnostic package that can be reused in any Go application.

**Observability - Metrics Infrastructure:**

- `pkg/metrics`: Generic Prometheus metrics infrastructure providing HTTP server for `/metrics` endpoint and metric creation helpers. Uses instance-based `prometheus.Registry` (not global DefaultRegisterer) for clean lifecycle management. Provides `NewServer()` for metrics HTTP server and helper functions for creating counters, histograms, gauges, and gauge vectors. Domain-agnostic package that can be reused in any Go application.
- `pkg/controller/metrics`: Domain-specific Prometheus metrics for controller operations. Implements event adapter pattern to track reconciliation, deployment, validation, resource counts, and event activity. Exposes 11 metrics including operation counters, error counters, duration histograms, and resource gauges. Started via explicit `Start()` method after EventBus initialization to prevent race conditions during startup.

**Dataplane Integration:**

- `pkg/dataplane`: Public API providing Client interface and convenience functions (Sync, DryRun, Diff)
- `pkg/dataplane/orchestrator`: Coordinates complete sync workflow (parse → compare → sync → auxiliary files)
- `pkg/dataplane/parser`: Wraps haproxytech/client-native for syntax validation and structured config parsing
- `pkg/dataplane` (validator.go): Pure validation functions implementing two-phase HAProxy configuration validation: Phase 1 (syntax validation using client-native parser) and Phase 2 (semantic validation using haproxy binary with -c flag). Writes auxiliary files to actual HAProxy directories (with mutex locking to prevent concurrent writes) to validate file references exactly as the Dataplane API does. Requires ValidationPaths parameter matching Dataplane API resource configuration. Provides ValidateConfiguration(mainConfig, auxFiles, paths) as pure function with no event dependencies.
- `pkg/dataplane/comparator`: Performs fine-grained section-by-section comparison to generate minimal change operations
- `pkg/dataplane/comparator/sections`: Section-specific comparison logic for all HAProxy config sections (global, defaults, frontends, backends, servers, ACLs, rules, binds, filters, checks, etc.)
- `pkg/dataplane/transform`: Converts client-native parser models to Dataplane API models using JSON marshaling. Provides 35+ type-specific transformation functions (ToAPIACL, ToAPIBackend, ToAPIFrontend, etc.) that eliminate ~77 duplicate inline conversions previously scattered across comparator sections. Uses generic `transform[T]` function for nil-safe JSON-based type conversion. Performance: ~10µs per transformation, acceptable for reconciliation workflow.
- `pkg/dataplane/discovery`: HAProxy endpoint discovery utilities for identifying dataplane API endpoints
- `pkg/dataplane/synchronizer`: Executes operations with transaction management and retry logic
- `pkg/dataplane/auxiliaryfiles`: Manages auxiliary files (general files, SSL certificates, map files) with 3-phase sync: pre-config (create/update), config sync, post-config (delete)
- `pkg/dataplane/client`: HTTP client wrapper for Dataplane API with version conflict handling, transaction lifecycle, and storage API integration
- `pkg/dataplane/types`: Public types used across dataplane package (Endpoint, SyncOptions, Result types)

**Kubernetes Integration:**

- `pkg/k8s/types`: Core interfaces and types for the k8s package
  - `Store` interface for resource indexing (Get, List, Add, Update, Delete, Clear)
  - `WatcherConfig` for configuring bulk resource watching with filters, indexing, and callbacks
  - `SingleWatcherConfig` for configuring single named resource watching (ConfigMap, Secret, etc.)
  - `OnChangeCallback` and `OnSyncCompleteCallback` for bulk watcher change notifications
  - `OnResourceChangeCallback` for single resource watcher immediate callbacks
  - `ChangeStats` for tracking resource changes with initial sync context
  - `StoreType` enum for memory vs cached store selection
- `pkg/k8s/client`: Kubernetes client wrapper with dynamic client support
  - Wraps kubernetes.Interface and dynamic.Interface
  - Auto-detects in-cluster vs out-of-cluster configuration
  - Provides namespace detection from service account token
- `pkg/k8s/indexer`: JSONPath evaluation and field filtering
  - Extracts index keys from resources using JSONPath expressions (e.g., `metadata.namespace`, `metadata.labels['key']`)
  - Removes unnecessary fields to reduce memory usage (e.g., `metadata.managedFields`)
  - Fail-fast validation of JSONPath expressions at startup
- `pkg/k8s/store`: Store implementations for indexed resource storage
  - `MemoryStore`: Fast in-memory storage with complete resources (default)
  - `CachedStore`: Memory-efficient storage with API-backed fetches and TTL caching (for large resources like Secrets)
  - Thread-safe with RWMutex for concurrent access
  - O(1) lookups using composite keys from multiple index expressions
- `pkg/k8s/watcher`: High-level resource watching with two watcher types
  - **Watcher** (bulk resource watching): For watching collections of resources (Ingress, Service, EndpointSlice)
    - Uses SharedInformerFactory for efficient resource watching
    - Supports namespace and label selector filtering
    - Debounces rapid changes with configurable interval (default 500ms)
    - Indexed storage with O(1) lookups using JSONPath expressions
    - Tracks initial sync state with `OnSyncComplete` callback and `IsInitialSync` flag
    - Provides `WaitForSync()` and `IsSynced()` for manual synchronization control
    - Supports incremental or bulk processing during initial sync via `CallOnChangeDuringSync`
  - **SingleWatcher** (single resource watching): For watching one specific named resource (ConfigMap, Secret)
    - Lightweight implementation with no indexing or store overhead
    - Watches single resource by namespace + name using field selector
    - Immediate callbacks (no debouncing) with `OnResourceChangeCallback`
    - Ideal for controller configuration (ConfigMap) and credentials (Secret)
    - Provides `WaitForSync()` and `IsSynced()` for startup coordination

**Initial Synchronization Handling:**

The k8s package provides comprehensive support for distinguishing between initial bulk loading of pre-existing resources and real-time changes:

- **Sync Tracking**: Watcher tracks initial sync state internally and provides sync status via `IsSynced()` method
- **OnSyncComplete Callback**: Called once after initial sync completes with the fully populated store and resource count
- **IsInitialSync Flag**: ChangeStats includes `IsInitialSync` field to distinguish bulk load events from real-time changes
- **Callback Control**: `CallOnChangeDuringSync` flag allows choosing between:
  - Incremental processing: Receive callbacks during sync with `IsInitialSync=true` for progressive resource handling
  - Bulk processing: Suppress callbacks during sync, receive only `OnSyncComplete` with final state
- **Manual Synchronization**: `WaitForSync(ctx)` blocks until initial sync completes, useful for staged startup
- **Non-Blocking Status**: `IsSynced()` provides non-blocking sync status check

This prevents common pitfalls like rendering HAProxy configuration before all ingresses are loaded, ensuring the system always has complete data before taking action.

**Configuration and Credentials Management:**

The controller monitors two critical single resources using `SingleWatcher`:

1. **Controller Configuration (ConfigMap)**:
   - Contains templates, watched resource definitions, and controller settings
   - Watched using `SingleWatcher` for immediate re-parsing and validation on changes
   - Name and namespace configured via environment variables or auto-detected
   - Parsed using `ParseConfig(configMapData map[string][]byte)` from pkg/core/config
   - Changes trigger scatter-gather validation before becoming active

2. **Controller Credentials (Secret)**:
   - Secret name: Configurable via environment variable (default: haproxy-template-ic-credentials)
   - Contains 4 required keys:
     - `dataplane_username`: Username for HAProxy Dataplane API
     - `dataplane_password`: Password for HAProxy Dataplane API
     - `validation_username`: Username for validation endpoint
     - `validation_password`: Password for validation endpoint
   - Watched using `SingleWatcher` for immediate credential rotation on changes
   - Loaded using `LoadCredentials(secretData map[string][]byte)` from pkg/core/config
   - All fields are required and validated to be non-empty

Both watchers use the event-driven architecture: changes publish events to EventBus, triggering validation (ConfigMap) or credential updates (Secret).

**Controller Logic:**

- `pkg/controller`: Main controller package implementing reinitialization loop pattern. Coordinates startup orchestration and component lifecycle via EventBus, responds to configuration changes by cleanly restarting iterations with new settings
- `pkg/controller/commentator`: Event commentator that subscribes to all events and produces domain-aware log messages with contextual insights using ring buffer for event correlation
- `pkg/controller/configloader`: Loads and parses controller configuration from ConfigMap resources, publishes ConfigParsedEvent
- `pkg/controller/credentialsloader`: Loads and validates credentials from Secret resources, publishes CredentialsUpdatedEvent
- `pkg/controller/configchange`: Handles configuration change events and coordinates reloading of resources
- `pkg/controller/deployer`: Deployment orchestration package (Stage 5) implementing three-component architecture:
  - **DeploymentScheduler**: Coordinates WHEN deployments happen. Maintains state (last validated config, current endpoints), enforces minimum deployment interval (default 2s) for rate limiting, implements "latest wins" queueing for concurrent changes. Subscribes to TemplateRenderedEvent, ValidationCompletedEvent, HAProxyPodsDiscoveredEvent, DriftPreventionTriggeredEvent, DeploymentCompletedEvent. Publishes DeploymentScheduledEvent.
  - **Deployer**: Stateless executor that performs deployments. Subscribes to DeploymentScheduledEvent, executes parallel deployments to multiple HAProxy endpoints. Publishes DeploymentStartedEvent, InstanceDeployedEvent, InstanceDeploymentFailedEvent, DeploymentCompletedEvent.
  - **DriftPreventionMonitor**: Prevents configuration drift from external changes. Monitors deployment activity and triggers periodic deployments (default 60s) when system is idle. Subscribes to DeploymentCompletedEvent. Publishes DriftPreventionTriggeredEvent.
- `pkg/controller/discovery`: HAProxy pod discovery component (Stage 5). Discovers HAProxy pods in the cluster and provides endpoint information to the Deployer. Publishes HAProxyPodsDiscoveredEvent with discovered endpoints.
- `pkg/controller/executor`: Orchestrates reconciliation cycles by handling events from pure components. Subscribes to ReconciliationTriggeredEvent, TemplateRenderedEvent, TemplateRenderFailedEvent, ValidationCompletedEvent, and ValidationFailedEvent. Publishes ReconciliationStartedEvent, ReconciliationCompletedEvent, and ReconciliationFailedEvent. Coordinates the event-driven flow: Renderer → Validator → Deployer. Measures reconciliation duration for observability and handles validation failures by publishing ReconciliationFailedEvent.
- `pkg/controller/indextracker`: Tracks synchronization state across multiple resource types, publishes IndexSynchronizedEvent when all resources complete initial sync, enabling staged controller startup with clear initialization checkpoints
- `pkg/controller/metrics`: Prometheus metrics event adapter. Subscribes to controller lifecycle events (reconciliation, deployment, validation, discovery) and exports 11 metrics including operation counters (reconciliation_total, deployment_total, validation_total), error counters, duration histograms (reconciliation_duration_seconds, deployment_duration_seconds), resource gauges (resource_count with type labels), and event bus metrics (event_subscribers, events_published_total). Uses instance-based prometheus.Registry and explicit Start() method to prevent race conditions during startup. Metrics exposed on configurable port (default 9090) via pkg/metrics HTTP server.
- `pkg/controller/reconciler`: Debounces resource change events and triggers reconciliation cycles. Subscribes to ResourceIndexUpdatedEvent (applies debouncing with configurable interval, default 500ms) and ConfigValidatedEvent (triggers immediately without debouncing). Publishes ReconciliationTriggeredEvent when conditions are met. Filters initial sync events to prevent premature reconciliation. First Stage 5 component enabling controlled reconciliation trigger logic.
- `pkg/controller/resourcewatcher`: Manages lifecycle of all Kubernetes resource watchers defined in configuration, provides centralized WaitForAllSync() method for coordinated initialization, publishes ResourceIndexUpdatedEvent with detailed change statistics
- `pkg/controller/validator`: Contains validation components for controller configuration validation (basic structural validation, template syntax validation, JSONPath expression validation) that respond to ConfigValidationRequest events using scatter-gather pattern
- `pkg/controller/validator/haproxy_validator.go`: HAProxy configuration validator component (Stage 5). Subscribes to TemplateRenderedEvent and validates rendered HAProxy configurations using two-phase validation: syntax validation with client-native parser and semantic validation with haproxy binary. Publishes ValidationCompletedEvent on success or ValidationFailedEvent with detailed error messages on failure. Integrates pkg/dataplane validation logic into the event-driven architecture.
- `pkg/controller/renderer`: Template rendering component (Stage 5). Subscribes to ReconciliationTriggeredEvent and renders HAProxy configuration and auxiliary files from templates using the templating engine. Publishes TemplateRenderedEvent with rendered configuration and auxiliary files, or TemplateRenderFailedEvent on rendering errors.
- `pkg/controller/events`: Domain-specific event type definitions (~50 event types covering complete controller lifecycle including validation events)
- `pkg/controller/debug`: Controller-specific debug variable implementations for introspection HTTP server. Implements `introspection.Var` interface for controller data including ConfigVar, CredentialsVar (metadata only, not actual passwords), RenderedVar, ResourcesVar, and EventsVar. Provides EventBuffer for independent event tracking separate from EventCommentator. Exposes StateProvider interface for accessing controller state in a thread-safe manner.
- `pkg/controller/statecache.go`: Event-driven state cache implementing StateProvider interface. Subscribes to validation, rendering, and resource events to maintain current state snapshot in memory with thread-safe RWMutex-protected access. Provides debug endpoints with access to current configuration, credentials (metadata), rendered output, and resource counts without querying EventBus for historical state.

**Testing Infrastructure:**

- `tests/acceptance`: End-to-end acceptance testing framework with debug endpoint and metrics validation. Provides fixture management for test resources (ConfigMaps, Services, Endpoints), debug client for querying introspection endpoints during tests via kubectl port-forward, environment helpers for test cluster setup and teardown, ConfigMap reload tests, and metrics endpoint validation tests. Tests verify metrics endpoint accessibility, presence of all 11 expected metrics, non-zero operational values, and histogram structure. Enables true end-to-end testing without parsing logs or relying on timing heuristics.
- `tests/integration`: Integration testing infrastructure for component interaction tests with documentation of testing strategies and environment setup.

**Template Engine:**

- `pkg/templating`: Low-level template engine library providing template compilation and rendering
  - Pre-compiles templates at initialization for optimal runtime performance
  - Wraps Gonja v2 for Jinja2-compatible template syntax
  - Provides TemplateEngine with Render(templateName, context) API
  - Custom error types for compilation, rendering, and template-not-found scenarios
  - Custom filters (b64decode) and context methods (pathResolver) are integrated in controller rendering components

**Development Tools:**

- `tools/linters/eventimmutability`: Custom golangci-lint analyzer that enforces event immutability contract
  - Checks that all Event interface method implementations use pointer receivers
  - Prevents accidental struct copying (events often exceed 200 bytes)
  - Integrated into `make lint` and CI pipeline via golangci-lint
  - Provides clear error messages with file locations when violations detected
  - See `tools/linters/eventimmutability/README.md` for details

## Key Interfaces

```go
// Resource storage (pkg/k8s/types)
type Store interface {
    // Get retrieves all resources matching the provided index keys
    Get(keys ...string) ([]interface{}, error)

    // List returns all resources in the store
    List() ([]interface{}, error)

    // Add inserts a new resource with the provided index keys
    Add(resource interface{}, keys []string) error

    // Update modifies an existing resource
    Update(resource interface{}, keys []string) error

    // Delete removes a resource using its index keys
    Delete(keys ...string) error

    // Clear removes all resources from the store
    Clear() error
}

// Change notification callbacks (pkg/k8s/types)
type OnChangeCallback func(store Store, stats ChangeStats)

type OnSyncCompleteCallback func(store Store, initialCount int)

type ChangeStats struct {
    Created  int  // Number of resources added
    Modified int  // Number of resources updated
    Deleted  int  // Number of resources removed
    IsInitialSync bool  // True during initial synchronization
}

// Template rendering
type TemplateRenderer interface {
    Render(template string, context interface{}) (string, error)
    RenderAll(config models.Config, resources ResourceCollection) (RenderedContext, error)
}

// Configuration validation
type ConfigValidator interface {
    ValidateSyntax(config string) error
    ValidateSemantics(config string) error
}

// Dataplane operations
type DataplaneClient interface {
    GetVersion() (VersionInfo, error)
    DeployConfiguration(config string) (DeploymentResult, error)
    FetchStructuredConfig() (StructuredConfig, error)
    SyncMaps(maps map[string]string) error
    SyncCertificates(certs map[string]string) error
}

// Configuration synchronization
type ConfigSynchronizer interface {
    SyncConfiguration(ctx context.Context, config RenderedContext) (SyncResult, error)
    UpdateEndpoints(endpoints []DataplaneEndpoint) error
}
```
