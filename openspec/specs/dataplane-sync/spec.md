# Dataplane Sync

## Purpose

Orchestrates HAProxy configuration synchronization through a fetch-parse-compare-apply pipeline with three-phase auxiliary file management, full-config push (a runtime-action push with no reload, or a force-reload push), and connection-error retry logic.

## Requirements

### Requirement: Orchestrator Sync Workflow

The orchestrator SHALL implement a multi-step sync workflow: (1) fetch current configuration from the Dataplane API, (2) parse current and desired configurations into structured form, (3) compare configurations to produce a ConfigDiff, (4) compare auxiliary files, (5) check if any changes exist, (6) classify the changes to decide the sync mode (runtime or reload), (7) push the full configuration, and (8) verify reload if configured. When no configuration or auxiliary file changes are detected, the orchestrator SHALL return a success result with no applied operations and no reload.

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

For indexed rule types (HTTP request rules, HTTP response rules, TCP request rules, TCP response rules, stick rules, HTTP after-response rules, backend switching rules, server switching rules), the Comparator SHALL use LCS-based content matching via the Myers diff algorithm instead of index-based positional comparison. Two rules SHALL be considered equal when their `Equal()` method returns true. The diff SHALL produce INSERT (CREATE at index) and DELETE operations for rule additions and removals, rather than cascading UPDATE operations caused by index shifts. Rules present in both current and desired configurations at different positions SHALL produce no operations. Rules at the same LCS position with different content SHALL produce UPDATE operations.

The LCS diff positions SHALL be translated to correct Dataplane API indexes using a running offset that accounts for cumulative shifts from prior operations within the same rule section. DELETE operations SHALL use the current-config index. INSERT operations SHALL use the desired-config index (the target position in the final configuration). Within each rule section the operations SHALL be emitted as updates first, then deletes highest-index-first, then inserts lowest-index-first, so each index resolves to the intended rule when the edit script is read against the staged section.

The LCS-based comparison SHALL be implemented as a single generic function parameterized over rule type, accepting an equality function and producing abstract diff entries (keep/insert/delete). Each rule-type-specific comparison function SHALL wrap this generic function with its own operation factory calls.

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

#### Scenario: Rule insertion produces only insert operations

WHEN one HTTP request rule is inserted at position 5 in a frontend with 100 existing rules
THEN the ConfigDiff SHALL contain exactly one CREATE operation for that rule and zero UPDATE operations for the subsequent 95 rules.

#### Scenario: Rule deletion produces only delete operations

WHEN one HTTP request rule is deleted from position 5 in a frontend with 100 existing rules
THEN the ConfigDiff SHALL contain exactly one DELETE operation for that rule and zero UPDATE operations for the subsequent 94 rules.

#### Scenario: Rule content change produces update operation

WHEN an HTTP request rule at position 10 changes its action from "deny" to "allow" with all other rules unchanged
THEN the ConfigDiff SHALL contain exactly one UPDATE operation at index 10 and no INSERT or DELETE operations for that frontend's rules.

#### Scenario: Mixed insertions and deletions at different positions

WHEN one rule is deleted at position 3 and one different rule is inserted at position 7 in the same frontend
THEN the ConfigDiff SHALL contain exactly one DELETE and one CREATE operation, with no UPDATE operations for unchanged rules between or after the changed positions.

#### Scenario: LCS comparison applies to all eight indexed rule types

WHEN rules shift due to insertion in any of the eight indexed rule types (HTTP request, HTTP response, TCP request, TCP response, stick, HTTP after-response, backend switching, server switching)
THEN the Comparator SHALL use LCS-based content matching for that rule type, producing INSERT/DELETE operations instead of cascading UPDATEs.

#### Scenario: DELETE index uses current-config position

WHEN a rule at current-config index 5 is deleted
THEN the DELETE operation SHALL specify index 5.

#### Scenario: INSERT index uses desired-config position

WHEN a new rule should appear at desired-config index 8
THEN the CREATE operation SHALL specify index 8.

### Requirement: Operation Ordering

Operations produced by the Comparator are pure descriptors (Type, Section, Describe) with no priority field and SHALL NOT be sorted into a global delete/create/update execution sequence: the orchestrator does not execute operations one at a time, it pushes the full rendered config in a single request. The only ordering the Comparator SHALL enforce is within each indexed rule section, where the LCS-based diff SHALL emit operations as updates first, then deletes in descending index order, then inserts in ascending index order, so that each operation's index resolves to the intended rule when the edit script is read against the staged section.

#### Scenario: Deletes emitted in descending index order within a rule section

WHEN an indexed rule section produces multiple delete operations
THEN those deletes SHALL be emitted in descending index order so that each delete does not shift the index of a not-yet-processed delete.

#### Scenario: Inserts emitted in ascending index order within a rule section

WHEN an indexed rule section produces multiple insert operations
THEN those inserts SHALL be emitted in ascending index order so that each insert lands at its final-list position.

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

### Requirement: Full-Config Apply

Production configuration changes SHALL be applied by pushing the full rendered configuration to the Dataplane API in a single request; the orchestrator SHALL NOT execute per-operation Dataplane API transactions. From the ConfigDiff the orchestrator SHALL partition changes into runtime-eligible server-field updates and structural changes, and choose one of two apply shapes:

- Runtime path: when every change is a runtime-eligible server-field update, a single `PushRawConfigurationSkipReload` carrying an `X-Runtime-Actions` header, which writes the new config to disk and applies the server changes to the live worker without a reload.
- Reload path: otherwise, a `PushRawConfiguration` with `force_reload`; when runtime-eligible changes are also present, a best-effort skip-reload push MAY precede the reload to seed the running worker.

The SyncResult SHALL record the SyncMode as one of `no_changes`, `runtime`, or `reload`, and SHALL include the ReloadID when a reload is triggered.

#### Scenario: Runtime-eligible diff applies without reload

WHEN the diff contains only runtime-eligible server-field updates (e.g. address, port, maintenance, weight)
THEN the orchestrator SHALL apply them via a single skip-reload push with `X-Runtime-Actions` and SyncMode=runtime, with no reload.

#### Scenario: Structural change triggers reload

WHEN the diff contains any structural change (server creation/deletion, frontend/backend/bind/rule/filter changes)
THEN the orchestrator SHALL push the full config with `force_reload`, set SyncMode=reload, and the SyncResult SHALL have a non-empty ReloadID.

### Requirement: Connection-Error Retry

The orchestrator SHALL wrap its version resolution and config pushes in a bounded retry (`client.WithRetry`, 3 attempts) that retries only transient connection errors — the master socket is briefly unavailable while HAProxy re-execs on reload. Runtime-action pushes SHALL additionally retry across a concurrent reload, since runtime commands fail while the stats socket is momentarily down. The orchestrator SHALL re-resolve the config version on each sync rather than reusing a stale version across conflicts.

#### Scenario: Transient connection error is retried

WHEN fetching the config version or pushing the configuration fails with a transient connection error
THEN the orchestrator SHALL retry up to 3 times before surfacing the error.

#### Scenario: Runtime push retries across a concurrent reload

WHEN a runtime-action push fails because the stats socket is momentarily closed during a sibling reload
THEN the orchestrator SHALL retry the push until the socket is available again or the attempt budget is exhausted.

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

The dataplane package SHALL define structured error types: SyncError (with Stage, Message, Cause, and Hints), ConnectionError (with Endpoint and Cause), ParseError (with ConfigType, ConfigSnippet, and Cause), and ValidationError (with Phase, Message, and Cause). All error types SHALL implement the error interface and SHALL implement Unwrap for error chain inspection.

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

After a successful sync, the orchestrator SHALL capture the post-sync configuration version in the SyncResult.PostSyncVersion field for use in subsequent version cache checks. A config push that writes a new on-disk version header SHALL set the post-sync version to the pre-push version plus 1.

#### Scenario: Config push calculates post-sync version

WHEN a raw push completes with pre-push version 5
THEN the SyncResult.PostSyncVersion SHALL be 6.
