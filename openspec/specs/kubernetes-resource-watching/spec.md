# Kubernetes Resource Watching

## Purpose

Watches Kubernetes resources using client-go informers, maintains indexed stores, and delivers change notifications with debouncing and initial sync tracking.

## Requirements

### Requirement: Bulk Watcher

The Bulk Watcher SHALL watch collections of Kubernetes resources identified by GroupVersionResource. It SHALL support namespace filtering, label selectors, and client-side field selectors. It SHALL use a DynamicSharedIndexInformer from client-go with no periodic resync (resync period 0). Resources received from the informer SHALL be processed through the indexer (field filtering, key extraction, float-to-int conversion) before being stored.

#### Scenario: Watcher receives resources matching label selector

WHEN a Watcher is configured with a LabelSelector and resources exist that match the selector
THEN only resources matching the label selector SHALL appear in the store after initial sync completes.

#### Scenario: Client-side field selector filters resources

WHEN a Watcher is configured with a FieldSelector (e.g., `spec.ingressClassName=haproxy`)
THEN resources that do not match the field selector SHALL NOT be added to the store, and resources that transition from matching to not matching SHALL be treated as deletions.

#### Scenario: Field selector transition from non-matching to matching

WHEN a resource is updated such that it now matches a previously non-matching field selector
THEN the Watcher SHALL treat the update as an addition and add the resource to the store.

#### Scenario: Resync events are filtered

WHEN the informer delivers an update event where the old and new resourceVersion are identical (resync)
THEN the Watcher SHALL skip the event without recording a change or invoking the debouncer.

### Requirement: Single Watcher

The Single Watcher SHALL watch exactly one named Kubernetes resource identified by GVR, namespace, and name. It SHALL use a field selector on `metadata.name` to restrict the watch server-side. It SHALL use a 30-second resync period for resilience against missed watch events. It SHALL NOT use indexing, stores, or debouncing. Callbacks SHALL be invoked immediately upon detecting a change.

#### Scenario: SingleWatcher delivers immediate callback on resource update

WHEN a watched resource is updated after initial sync completes
THEN the OnChange callback SHALL be invoked immediately with the updated resource object.

#### Scenario: SingleWatcher filters resync events by resourceVersion

WHEN the informer delivers an update event where both old and new resourceVersion are non-empty and identical
THEN the SingleWatcher SHALL skip the callback.

#### Scenario: SingleWatcher filters status-only updates by generation

WHEN the informer delivers an update event where the generation field has not changed between old and new
THEN the SingleWatcher SHALL skip the callback.

#### Scenario: SingleWatcher suppresses callbacks during initial sync

WHEN add, update, or delete events arrive before initial sync has completed
THEN the SingleWatcher SHALL NOT invoke the OnChange callback for those events.

#### Scenario: Start is idempotent

WHEN Start is called multiple times on a SingleWatcher
THEN the informer initialization and sync logic SHALL execute only once, and subsequent calls SHALL block until the context is cancelled.

### Requirement: Initial Sync Tracking

Both Watcher and SingleWatcher SHALL track whether initial synchronization has completed. The IsSynced method SHALL return false before sync completes and true afterward. WaitForSync SHALL block until sync is complete or the context is cancelled.

#### Scenario: IsSynced transitions after cache sync

WHEN the informer's HasSynced returns true
THEN IsSynced SHALL return true for all subsequent calls.

#### Scenario: WaitForSync blocks until sync completes

WHEN WaitForSync is called before the informer has synced
THEN it SHALL block until sync completes, then return the count of resources loaded (Watcher) or nil error (SingleWatcher).

#### Scenario: WaitForSync respects context cancellation

WHEN the context is cancelled before sync completes
THEN WaitForSync SHALL return an error.

### Requirement: OnSyncComplete Callback

Both Watcher and SingleWatcher SHALL support an optional OnSyncComplete callback. For the Bulk Watcher, OnSyncComplete SHALL receive the store and the count of resources loaded during initial sync. For the SingleWatcher, OnSyncComplete SHALL receive the current resource from the informer cache (or nil if the resource does not exist).

#### Scenario: Bulk Watcher OnSyncComplete fires with resource count

WHEN the Bulk Watcher finishes initial sync with 5 pre-existing resources
THEN OnSyncComplete SHALL be called once with the store and initialCount=5.

#### Scenario: SingleWatcher OnSyncComplete delivers current state

WHEN the SingleWatcher finishes initial sync and the resource exists in the cache
THEN OnSyncComplete SHALL be called once with the current resource object.

#### Scenario: SingleWatcher OnSyncComplete with missing resource

WHEN the SingleWatcher finishes initial sync and the resource does not exist
THEN OnSyncComplete SHALL be called once with a nil resource argument.

### Requirement: Change Debouncing

The Bulk Watcher SHALL debounce OnChange callbacks using a leading-edge triggering strategy with a configurable refractory period (default 2 seconds). The first change after the refractory period has elapsed SHALL trigger an immediate callback. Subsequent changes within the refractory period SHALL be batched until the interval expires.

#### Scenario: Leading-edge trigger fires immediately

WHEN no callback has fired within the debounce interval and a resource change occurs
THEN the OnChange callback SHALL be invoked immediately.

#### Scenario: Changes within refractory period are batched

WHEN a callback has fired within the debounce interval and additional resource changes occur
THEN the changes SHALL be accumulated and the OnChange callback SHALL be invoked once when the interval expires.

#### Scenario: Callbacks suppressed during sync when configured

WHEN CallOnChangeDuringSync is false (default) and changes occur during initial sync
THEN the debouncer SHALL suppress callbacks, and accumulated changes SHALL be flushed when sync mode ends.

#### Scenario: Flush delivers pending changes immediately

WHEN Flush is called with pending accumulated changes
THEN the OnChange callback SHALL be invoked immediately with the accumulated statistics, regardless of the suppressDuringSync setting.

### Requirement: Change Statistics

The OnChange callback SHALL receive a ChangeStats struct containing Created, Modified, and Deleted counts aggregated over the debounce window. The IsInitialSync field SHALL be true when the callback fires during sync mode and false afterward. The callback SHALL NOT be invoked when the statistics are empty (all counts zero).

#### Scenario: Statistics aggregate multiple changes

WHEN two resources are created and one is deleted within a single debounce window
THEN the OnChange callback SHALL receive ChangeStats with Created=2, Deleted=1, Modified=0.

#### Scenario: IsInitialSync reflects sync state

WHEN OnChange fires during initial sync (CallOnChangeDuringSync=true)
THEN ChangeStats.IsInitialSync SHALL be true. When OnChange fires after sync completes, IsInitialSync SHALL be false.

#### Scenario: Empty statistics do not trigger callback

WHEN no resource changes have occurred since the last callback
THEN the OnChange callback SHALL NOT be invoked.

### Requirement: JSONPath-Based Indexing

Resources SHALL be indexed using configurable JSONPath expressions (IndexBy). The indexer SHALL extract string keys from each expression in order, producing a composite key. JSONPath expressions SHALL be validated at indexer creation time (fail-fast). At least one index expression is required.

#### Scenario: Composite key extraction

WHEN a resource with namespace "default" and name "my-ingress" is processed with IndexBy `["metadata.namespace", "metadata.name"]`
THEN the extracted keys SHALL be `["default", "my-ingress"]`.

#### Scenario: Invalid JSONPath expression rejected at creation

WHEN an Indexer is created with an invalid JSONPath expression
THEN creation SHALL fail with an error identifying the invalid expression and its position.

### Requirement: Field Filtering

The indexer SHALL support removing fields from resources before storage using IgnoreFields JSONPath patterns. Filtering SHALL be performed in-place on the resource. Missing fields SHALL be silently ignored (not treated as errors).

#### Scenario: ManagedFields removed before storage

WHEN IgnoreFields includes "metadata.managedFields" and a resource has managedFields
THEN the stored resource SHALL NOT contain the managedFields key.

#### Scenario: Missing ignore field is silently skipped

WHEN IgnoreFields references a field that does not exist on the resource
THEN no error SHALL be returned and the resource SHALL be stored without modification for that pattern.

### Requirement: Float-to-Int Conversion

Resources SHALL be converted from their JSON-deserialized form (where all numbers are float64) to a template-friendly form where whole-number float64 values are converted to int64. This conversion SHALL happen once at storage time, not on every access. Fractional float64 values SHALL be preserved as-is.

#### Scenario: Integer port number converted

WHEN a resource contains a port field with value 80.0 (float64)
THEN the stored resource SHALL contain the value 80 (int64).

#### Scenario: Fractional value preserved

WHEN a resource contains a field with value 3.14 (float64)
THEN the stored resource SHALL retain 3.14 as float64.

### Requirement: DeletedFinalStateUnknown Handling

When the informer delivers a delete event wrapped in a DeletedFinalStateUnknown tombstone (indicating the resource's final state may be stale), the watcher SHALL unwrap the tombstone and process the deletion using the enclosed object.

#### Scenario: Tombstone delete event processed

WHEN a delete event arrives as a DeletedFinalStateUnknown containing a resource
THEN the Watcher SHALL extract the resource from the tombstone and delete it from the store using its index keys.

### Requirement: Watch Error Handling

The SingleWatcher and the Bulk Watcher SHALL register a watch error handler for observability. Watch errors SHALL be logged and the last error timestamp SHALL be recorded per watcher. The informer's built-in Reflector SHALL handle retry with exponential backoff automatically. Callback failures SHALL be logged but SHALL NOT stop the watcher.

#### Scenario: Watch error logged and timestamp recorded

- **WHEN** a watch connection error occurs on a SingleWatcher
- **THEN** the error SHALL be logged at warn level and LastWatchError SHALL return a non-zero timestamp.

#### Scenario: Callback failure does not stop watcher

- **WHEN** an OnChange callback returns an error
- **THEN** the SingleWatcher SHALL log the error and continue watching for further events.

#### Scenario: Bulk watcher surfaces watch errors

- **WHEN** a watch connection error occurs on a Bulk Watcher (for example because the watched API version stopped being served mid-run)
- **THEN** the error SHALL be logged at warn level with the watcher's GVR and the last error timestamp SHALL be recorded, instead of the failure being visible only in client-go's internal logging.

### Requirement: Informer Body Projection for On-Demand Stores

For resources backed by the cached (on-demand) store — and only those — the Bulk Watcher SHALL install a transform on the informer, before the informer starts, that strips each unstructured object down to a conservative set of top-level roots: always apiVersion, kind, and metadata, plus the top-level root of every IndexBy expression and the top-level root of the FieldSelector's field path. Retention SHALL be whole top-level blocks, never subtree-trimmed — over-retain, never under-retain — so index-key extraction, field-selector evaluation, and identity handling keep working on the projected copy. The retained metadata block SHALL additionally be passed through the indexer's field filtering so configured IgnoreFields (managedFields, the last-applied-configuration annotation) are stripped from the projected copy too. Non-unstructured inputs (deletion tombstones) SHALL pass through unchanged.

Renders SHALL never read a projected body: the cached store resolves reads through a live API GET of the full, un-projected object, and the store SHALL be constructed in projected mode so informer-delivered stripped bodies are never cached as values. The projection SHALL be resource-agnostic, derived only from the JSONPath configuration, never from a resource kind.

#### Scenario: Secret payload dropped from informer memory

- **WHEN** a Secret-shaped resource is watched with an on-demand store and default index expressions
- **THEN** the informer's cached copy SHALL NOT contain the data or stringData blocks, while apiVersion, kind, and metadata survive.

#### Scenario: Field-selector root survives projection

- **WHEN** an on-demand resource is configured with a FieldSelector on a spec field
- **THEN** the spec block SHALL be retained in the projected copy so client-side selector evaluation still works.

#### Scenario: Render reads the full body

- **WHEN** a template reads an on-demand resource whose informer copy was projected
- **THEN** the read SHALL be served from a live API GET of the full object, never from the projected copy.

#### Scenario: Full-store kinds are not projected

- **WHEN** a resource uses the full in-memory store
- **THEN** no projection transform SHALL be installed and complete resource bodies SHALL be stored.

### Requirement: Controller Watcher Wiring

For every configured watched resource, plus an auto-injected watcher for the HAProxy pods matching the pod selector, the controller SHALL create one Bulk Watcher wired as follows. The cache TTL for on-demand stores SHALL be 2.2 times the drift-prevention interval (132 seconds at the 60-second default), sized so one failed rendering cycle does not evict cached resources. The per-watcher debounce interval SHALL come from the resource's debounceInterval override; an empty or unparseable value SHALL fall back to the watcher default of 2 seconds. The global ignore-fields list SHALL be deduplicated (preserving first-occurrence order) and applied to every watcher. The store SHALL be the cached store when the resource declares store "on-demand" and the full in-memory store otherwise. CallOnChangeDuringSync SHALL be false, so per-resource change callbacks fire only after initial sync. Resource changes SHALL publish a resource-index-updated event and sync completion SHALL publish a resource-sync-complete event on the event bus.

An index synchronization tracker SHALL consume the per-resource sync-complete events and publish a single IndexSynchronized event exactly once, after every expected resource name — all configured watched-resource names plus the auto-injected haproxy-pods watcher — has completed initial sync.

#### Scenario: Cache TTL derived from drift interval

- **WHEN** the drift-prevention interval is the 60-second default
- **THEN** on-demand watchers SHALL use a cache TTL of 132 seconds.

#### Scenario: Debounce override falls back to default

- **WHEN** a watched resource declares no debounceInterval
- **THEN** its watcher SHALL use the 2-second default debounce window.

#### Scenario: IndexSynchronized waits for haproxy-pods

- **WHEN** every user-configured resource has synced but the auto-injected haproxy-pods watcher has not
- **THEN** the IndexSynchronized event SHALL NOT be published yet.

#### Scenario: IndexSynchronized published once

- **WHEN** all expected resources (including haproxy-pods) complete initial sync and a resource later re-reports sync completion
- **THEN** exactly one IndexSynchronized event SHALL be published in total.
