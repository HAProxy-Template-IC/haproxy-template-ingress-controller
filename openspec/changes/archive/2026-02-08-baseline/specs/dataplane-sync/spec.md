# Dataplane Sync

Orchestrates HAProxy configuration synchronization through a fetch-parse-compare-apply pipeline with three-phase auxiliary file management, transaction-based operations, retry logic, and automatic fallback to raw config push.

## ADDED Requirements

### Requirement: Orchestrator Sync Workflow

The orchestrator SHALL implement a multi-step sync workflow: (1) fetch current configuration from the Dataplane API, (2) parse current and desired configurations into structured form, (3) compare configurations to produce a ConfigDiff, (4) compare auxiliary files, (5) check if any changes exist, (6) decide sync mode (fine-grained or raw push), (7) execute the sync, and (8) verify reload if configured. When no configuration or auxiliary file changes are detected, the orchestrator SHALL return a success result with no applied operations and no reload.

#### Scenario: No changes detected returns early

WHEN the current and desired configurations are identical and all auxiliary files match
THEN the orchestrator SHALL return a successful SyncResult with no applied operations and ReloadTriggered=false.

#### Scenario: Parse error on current config produces ParseError

WHEN the current configuration fetched from the Dataplane API cannot be parsed
THEN the orchestrator SHALL return a SyncError at the "parse-current" stage containing the first 200 characters of the config as a snippet.

#### Scenario: Parse error on desired config produces ParseError

WHEN the desired configuration string cannot be parsed
THEN the orchestrator SHALL return a SyncError at the "parse-desired" stage containing the first 200 characters of the config as a snippet.

### Requirement: Configuration Version Caching

The orchestrator SHALL support version-based caching to skip expensive configuration fetch and parse operations. When CachedCurrentConfig and CachedConfigVersion are provided in SyncOptions, the orchestrator SHALL call GetVersion (a lightweight check) first. If the pod version matches CachedConfigVersion, the cached parsed configuration SHALL be used directly. On version mismatch or GetVersion failure, the orchestrator SHALL fall through to the full fetch-and-parse path. The GetVersion call SHALL use retry logic with exponential backoff for connection errors.

#### Scenario: Version cache hit skips full fetch

WHEN CachedCurrentConfig is provided and the pod version matches CachedConfigVersion
THEN the orchestrator SHALL use the cached config directly without calling GetRawConfiguration.

#### Scenario: Version cache miss triggers full fetch

WHEN CachedCurrentConfig is provided but the pod version differs from CachedConfigVersion
THEN the orchestrator SHALL fetch the full configuration via GetRawConfiguration and parse it.

#### Scenario: GetVersion failure falls through to full fetch

WHEN the GetVersion call fails with a connection error
THEN the orchestrator SHALL retry with exponential backoff and, if all retries fail, fall through to the full fetch path.

### Requirement: Content Checksum Optimization

When ContentChecksum and LastDeployedChecksum are both set in SyncOptions and match, AND the config diff shows no changes, the orchestrator SHALL skip the auxiliary file comparison entirely. This optimization is safe because the content checksum covers both configuration and all auxiliary file content.

#### Scenario: Matching checksums skip auxiliary file comparison

WHEN ContentChecksum equals LastDeployedChecksum and the config diff has no changes
THEN the orchestrator SHALL skip all auxiliary file comparison calls and return no-changes.

#### Scenario: Different checksums proceed with comparison

WHEN ContentChecksum differs from LastDeployedChecksum
THEN the orchestrator SHALL perform full auxiliary file comparison regardless of config diff state.

### Requirement: Fine-Grained Configuration Comparison

The Comparator SHALL perform attribute-level comparison between two parsed StructuredConfig instances. It SHALL compare global, defaults, frontends, backends, servers, and 15+ additional section types (resolvers, mailers, peers, caches, rings, userlists, programs, log-forwards, log-profiles, traces, acme-providers, enterprise sections, fcgi-apps, crt-stores, http-errors). The comparison SHALL produce a ConfigDiff containing an ordered list of Operations and a DiffSummary. Both current and desired configurations MUST be non-nil; nil input SHALL return an error.

#### Scenario: Single attribute change produces single update operation

WHEN a backend's balance algorithm changes from "roundrobin" to "leastconn" with no other changes
THEN the ConfigDiff SHALL contain exactly one Update operation for that backend.

#### Scenario: New frontend produces create operation

WHEN the desired configuration contains a frontend not present in the current configuration
THEN the ConfigDiff SHALL contain a Create operation for that frontend.

#### Scenario: Removed backend produces delete operation

WHEN the current configuration contains a backend not present in the desired configuration
THEN the ConfigDiff SHALL contain a Delete operation for that backend.

#### Scenario: Nil configuration rejected

WHEN either current or desired configuration is nil
THEN Compare SHALL return an error.

### Requirement: Operation Ordering

Operations produced by the Comparator SHALL be ordered for safe execution: deletes first (sorted by descending priority so children are deleted before parents), then creates (sorted by ascending priority so parents are created before children), then updates. Within each operation type, stable sort SHALL preserve the original order for operations with equal priority.

#### Scenario: Delete operations precede creates

WHEN the diff contains both delete and create operations
THEN all delete operations SHALL appear before all create operations in the ordered list.

#### Scenario: Parent created before child

WHEN a new frontend and its bind are both created
THEN the frontend create operation SHALL have lower priority than the bind create operation, placing it earlier in the ordered list.

### Requirement: DiffSummary

The DiffSummary SHALL track total creates, updates, and deletes; whether global and defaults sections changed; lists of added, modified, and deleted frontends and backends by name; and maps of server changes keyed by backend name. HasChanges SHALL return true when any operation count is positive. TotalOperations SHALL return the sum of creates, updates, and deletes. StructuralOperations SHALL return TotalOperations minus the count of server modifications (which are runtime-eligible).

#### Scenario: HasChanges false when no operations

WHEN the config diff produces zero operations
THEN DiffSummary.HasChanges() SHALL return false.

#### Scenario: StructuralOperations excludes server modifications

WHEN the diff contains 10 total operations including 3 server modifications
THEN StructuralOperations SHALL return 7.

### Requirement: Three-Phase Auxiliary File Sync

The orchestrator SHALL sync auxiliary files in three phases. Phase 1 (pre-config): create and update auxiliary files so they exist before HAProxy configuration references them. SSL certificates and CA files SHALL be synced first (synchronously) before other auxiliary file types, which MAY be synced in parallel. Phase 2: apply configuration changes. Phase 3 (post-config): delete obsolete auxiliary files that are no longer referenced. Post-config deletion failures SHALL be logged as warnings but SHALL NOT fail the overall sync.

#### Scenario: SSL certificates synced before other auxiliary files

WHEN the sync has both SSL certificate changes and map file changes
THEN SSL certificates SHALL be fully synced before map file sync begins.

#### Scenario: General files and maps synced in parallel

WHEN both general files and map files have changes and SSL certs are already synced
THEN general file sync and map file sync SHALL execute concurrently.

#### Scenario: Post-config deletion failure does not fail sync

WHEN config sync succeeds but deletion of an obsolete map file fails
THEN the overall sync SHALL still return success with the deletion error logged as a warning.

#### Scenario: CRT-list files stored as general files

WHEN CRT-list files need to be synced
THEN they SHALL be merged into the general files comparison and synced through the general file storage API to avoid reload-triggering native CRT-list API calls.

### Requirement: Auxiliary File Comparison

The orchestrator SHALL compare all auxiliary file types (general files, SSL certificates, SSL CA files, map files, CRT-list files) in parallel. Each comparison SHALL produce a diff with ToCreate, ToUpdate, and ToDelete lists. CRT-list ToDelete entries SHALL be cleared after comparison because CRT-list deletion is handled by the unified general files comparison.

#### Scenario: Parallel auxiliary file comparison

WHEN auxiliary files of all five types need comparison
THEN all five comparison operations SHALL execute concurrently via errgroup.

#### Scenario: CRT-list delete entries cleared

WHEN the CRT-list comparison produces ToDelete entries
THEN those entries SHALL be cleared to nil after comparison, delegating deletion to general file handling.

### Requirement: Auxiliary File Reload Verification

When VerifyReload is enabled in SyncOptions, the orchestrator SHALL verify that all auxiliary file reloads complete successfully BEFORE proceeding to configuration sync. Verification SHALL poll the reload status endpoint until the reload succeeds, fails, or times out. Transient status check failures SHALL be logged and retried. If any auxiliary file reload fails verification, the orchestrator SHALL return a SyncError at the "auxiliary_reload_verification" stage.

#### Scenario: Auxiliary reload verified before config sync

WHEN auxiliary file sync triggers reloads and VerifyReload is enabled
THEN the orchestrator SHALL verify all auxiliary reload IDs complete successfully before executing config operations.

#### Scenario: Reload verification timeout

WHEN a reload verification exceeds the configured timeout
THEN the orchestrator SHALL return a SyncError indicating the timeout.

### Requirement: Transaction-Based Config Sync with Retry

Configuration operations SHALL be executed within a Dataplane API transaction managed by the VersionAdapter. On version conflict, the VersionAdapter SHALL retry up to MaxRetries times. The transaction commit status SHALL determine whether a HAProxy reload was triggered (HTTP 202) or not (HTTP 200). The SyncResult SHALL include the ReloadID when a reload is triggered.

#### Scenario: Successful transaction with reload

WHEN configuration operations are applied and the transaction commit returns HTTP 202
THEN the SyncResult SHALL have ReloadTriggered=true and a non-empty ReloadID.

#### Scenario: Version conflict triggers retry

WHEN a transaction commit fails with a version conflict
THEN the VersionAdapter SHALL retry with a new transaction up to MaxRetries times.

#### Scenario: Version conflict exhausts retries

WHEN version conflicts persist beyond MaxRetries
THEN the orchestrator SHALL return a SyncError at the "commit" stage with a ConflictError cause.

### Requirement: Parallel Operation Execution by Priority

Operations within a transaction SHALL be grouped by priority and executed in priority order (ascending). Operations within the same priority group SHALL execute in parallel. A configurable MaxParallel limit SHALL control concurrency within each priority group. When ContinueOnError is false, execution SHALL stop on the first failure. When ContinueOnError is true, all operations SHALL be attempted and failures collected.

#### Scenario: Operations at same priority run in parallel

WHEN 5 operations share priority level 100 and MaxParallel is 0 (unlimited)
THEN all 5 operations SHALL execute concurrently.

#### Scenario: MaxParallel limits concurrency

WHEN 10 operations share a priority level and MaxParallel is 3
THEN at most 3 operations SHALL execute concurrently within that priority group.

#### Scenario: Stop on first error

WHEN ContinueOnError is false and one operation in a priority group fails
THEN remaining operations in the errgroup SHALL be cancelled and the error returned.

### Requirement: Runtime API Optimization

When all operations in a diff are server UPDATE operations, the orchestrator SHALL execute them via the Runtime API without a transaction, avoiding HAProxy reload. The Runtime API path SHALL use version caching: fetch the version once, pass it to each operation, and increment after success. On 409 version conflict, the orchestrator SHALL re-fetch the version and retry up to 3 times. Runtime operations that modify non-runtime-supported server fields SHALL trigger a reload (HTTP 202 response).

#### Scenario: All server updates use runtime path

WHEN the diff contains only server UPDATE operations
THEN the orchestrator SHALL execute via Runtime API without creating a transaction.

#### Scenario: Mixed operations use transaction path

WHEN the diff contains both server updates and frontend creates
THEN the orchestrator SHALL use the transaction path for all operations.

#### Scenario: Runtime version conflict retried

WHEN a runtime server update fails with a 409 version conflict
THEN the orchestrator SHALL re-fetch the version and retry the operation.

### Requirement: Raw Config Push

The orchestrator SHALL fall back to raw configuration push via PushRawConfiguration when: (a) the config version is 1 (initial deployment), (b) the total operation count exceeds RawPushThreshold, or (c) fine-grained sync fails and FallbackToRaw is enabled. Raw push SHALL always trigger a reload. The SyncResult SHALL record the SyncMode (RawInitial, RawThreshold, or RawFallback). When falling back after a failed fine-grained sync where Phase 1 (auxiliary files) completed, the raw push SHALL skip re-syncing auxiliary files.

#### Scenario: Initial deployment uses raw push

WHEN the current configuration version is 1
THEN the orchestrator SHALL use raw push with SyncMode=RawInitial.

#### Scenario: High operation count triggers raw push

WHEN RawPushThreshold is 50 and the diff contains 60 operations
THEN the orchestrator SHALL use raw push with SyncMode=RawThreshold.

#### Scenario: Fallback skips auxiliary re-sync

WHEN fine-grained sync fails after Phase 1 (auxiliary files) completed and FallbackToRaw is enabled
THEN the raw push fallback SHALL skip auxiliary file sync.

#### Scenario: Both fine-grained and fallback fail

WHEN fine-grained sync fails and the subsequent raw push fallback also fails
THEN the orchestrator SHALL return a FallbackError containing both the original and fallback errors.

### Requirement: Reload Verification

When VerifyReload is enabled and a reload is triggered, the orchestrator SHALL poll the reload status until it succeeds, fails, or times out (using ReloadVerificationTimeout). A successful reload SHALL set ReloadVerified=true. A failed reload SHALL set Success=false and ReloadVerificationError on the SyncResult and return a SyncError at the "reload_verification" stage.

#### Scenario: Reload verification succeeds

WHEN a reload is triggered, VerifyReload is enabled, and the reload completes successfully
THEN the SyncResult SHALL have ReloadVerified=true and Success=true.

#### Scenario: Reload verification fails

WHEN a reload is triggered, VerifyReload is enabled, and the reload fails
THEN the SyncResult SHALL have ReloadVerified=false, a non-empty ReloadVerificationError, and Success=false.

### Requirement: Retry Logic with Exponential Backoff

The retry utility SHALL support configurable MaxAttempts, a RetryCondition predicate, a BackoffStrategy (None, Linear, or Exponential), and a BaseDelay. Exponential backoff SHALL double the delay on each attempt (BaseDelay * 2^(attempt-1)). Context cancellation during backoff SHALL abort the retry loop. The IsConnectionError condition SHALL match connection refused, connection reset, dial failures, and DNS resolution failures.

#### Scenario: Exponential backoff doubles delay

WHEN BackoffExponential is used with BaseDelay 100ms
THEN delays SHALL be 100ms, 200ms, 400ms for attempts 1, 2, 3 respectively.

#### Scenario: Context cancellation aborts retry

WHEN the context is cancelled during a backoff delay
THEN WithRetry SHALL return immediately with a cancellation error.

#### Scenario: Connection refused triggers retry

WHEN an operation fails with ECONNREFUSED and RetryIf is IsConnectionError()
THEN the operation SHALL be retried.

### Requirement: Structured Error Types

The dataplane package SHALL define structured error types: SyncError (with Stage, Message, Cause, and Hints), ConnectionError (with Endpoint and Cause), ParseError (with ConfigType, ConfigSnippet, and Cause), ConflictError (with Retries, ExpectedVersion, and ActualVersion), OperationError (with OperationType, Section, Resource, and Cause), and FallbackError (with OriginalError and FallbackCause). All error types SHALL implement the error interface. SyncError, ConnectionError, ParseError, and OperationError SHALL implement Unwrap for error chain inspection.

#### Scenario: SyncError includes stage and hints

WHEN a sync operation fails at the "apply" stage
THEN the SyncError SHALL contain Stage="apply", a descriptive Message, the underlying Cause, and actionable Hints.

#### Scenario: Error chain unwrapping

WHEN a SyncError wraps a ConnectionError
THEN errors.As with *ConnectionError SHALL return true when applied to the SyncError.

### Requirement: Dataplane API Multi-Version Support

The client SHALL support Dataplane API versions v3.0, v3.1, and v3.2 simultaneously via runtime version detection. The Dispatch pattern SHALL route API calls to the appropriate version-specific client. DispatchWithCapability SHALL check a capability predicate before dispatching, returning an error if the capability is not available. The Capabilities struct SHALL expose boolean flags for feature availability based on the detected version.

#### Scenario: Version auto-detection

WHEN the client connects to a Dataplane API endpoint
THEN it SHALL detect the API version and configure the appropriate version-specific client.

#### Scenario: Capability-gated dispatch rejects unsupported feature

WHEN a CRT-list operation is attempted against a v3.0 endpoint
THEN DispatchWithCapability SHALL return an error indicating the feature requires v3.2+.

### Requirement: Enterprise Edition Auto-Detection

The orchestrator SHALL automatically select the Enterprise Edition parser when connected to HAProxy Enterprise and the Community Edition parser otherwise. The enterprise detection SHALL be based on the client's IsEnterprise() method.

#### Scenario: Enterprise parser selected for EE

WHEN the Dataplane API reports an Enterprise edition connection
THEN the orchestrator SHALL use the Enterprise parser for configuration parsing.

#### Scenario: Community parser selected for CE

WHEN the Dataplane API reports a Community edition connection
THEN the orchestrator SHALL use the standard parser for configuration parsing.

### Requirement: Post-Sync Version Capture

After a successful sync, the orchestrator SHALL capture the post-sync configuration version in the SyncResult.PostSyncVersion field for use in subsequent version cache checks. For raw push, the post-sync version SHALL be the pre-push version plus 1. For fine-grained sync, the version SHALL be fetched from the API after operations complete.

#### Scenario: Fine-grained sync captures post-sync version

WHEN a fine-grained sync completes successfully
THEN the SyncResult.PostSyncVersion SHALL be the version obtained from GetVersion after operations.

#### Scenario: Raw push calculates post-sync version

WHEN a raw push completes with pre-push version 5
THEN the SyncResult.PostSyncVersion SHALL be 6.
