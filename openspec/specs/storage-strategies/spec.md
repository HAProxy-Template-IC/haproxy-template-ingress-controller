# Storage Strategies

Provides indexed storage for Kubernetes resources with multiple backend strategies, thread-safe access, composite key matching, and overlay-based dry-run simulation for validation.

## ADDED Requirements

### Requirement: Unified Store Interface

All store implementations SHALL conform to a common Store interface providing Get, List, Add, Update, Delete, and Clear operations. The interface SHALL accept resources as `interface{}` and index keys as string slices. All implementations SHALL be thread-safe for concurrent access using sync.RWMutex (read-write lock).

#### Scenario: Store interface methods available on all implementations

WHEN any Store implementation (MemoryStore, CachedStore, CompositeStore) is used
THEN it SHALL support Get, List, Add, Update, Delete, and Clear with the same signatures.

#### Scenario: Concurrent reads do not block each other

WHEN multiple goroutines call Get or List simultaneously on a MemoryStore
THEN all reads SHALL proceed concurrently without blocking each other.

#### Scenario: Write blocks concurrent reads and writes

WHEN a goroutine is performing Add, Update, Delete, or Clear
THEN concurrent Get, List, Add, Update, Delete, and Clear calls SHALL block until the write completes.

### Requirement: MemoryStore

MemoryStore SHALL store complete pre-converted resources in memory using a flat map from composite key string to a slice of resources. It SHALL provide O(1) exact-match lookups via the composite key. It SHALL support non-unique index keys by storing multiple resources per composite key. Stored slices SHALL be maintained in sorted order (by namespace, then name) at insert time to enable zero-copy reads.

#### Scenario: Exact match Get returns direct reference

WHEN Get is called with the full number of index keys matching an existing composite key
THEN it SHALL return a direct reference to the internal pre-sorted slice without copying.

#### Scenario: Partial match Get returns aggregated results

WHEN Get is called with fewer keys than the index configuration (prefix match)
THEN it SHALL return a new slice containing all resources whose composite key starts with the provided prefix, sorted by namespace and name.

#### Scenario: Add with non-unique keys appends to existing slice

WHEN Add is called with keys that already have resources stored
THEN the new resource SHALL be appended to the existing slice and the slice SHALL be re-sorted.

#### Scenario: Update replaces resource with matching namespace and name

WHEN Update is called with keys matching an existing entry and the resource has the same namespace and name as an existing resource in that key's slice
THEN the existing resource SHALL be replaced in-place.

#### Scenario: Update adds resource when not found

WHEN Update is called with keys matching an existing entry but no resource in the slice has the same namespace and name
THEN the resource SHALL be appended and the slice re-sorted.

#### Scenario: Delete removes all resources for the composite key

WHEN Delete is called with a full set of index keys
THEN all resources stored under that composite key SHALL be removed.

#### Scenario: List returns a fresh copy sorted by namespace and name

WHEN List is called
THEN it SHALL return a newly allocated slice containing all resources, sorted by namespace then name, independent of the internal data map.

#### Scenario: Key count validation

WHEN Add, Update, or Delete is called with a number of keys that does not match the configured numKeys
THEN a StoreError SHALL be returned indicating the key count mismatch.

### Requirement: CachedStore

CachedStore SHALL store only resource references (namespace, name, index keys) in memory and fetch full resources from the Kubernetes API on access. Fetched resources SHALL be cached using an LRU cache with a configurable TTL (default 2m10s). The maximum cache size SHALL default to 256 entries. Cache entries that are accessed SHALL have their TTL reset. API fetches SHALL use a 10-second timeout. Fetched resources SHALL be processed through the indexer (field filtering, float-to-int conversion) before caching.

#### Scenario: Cache hit returns resource without API call

WHEN Get is called for a resource that is in the LRU cache and the cache entry has not expired
THEN the cached resource SHALL be returned and no API call SHALL be made.

#### Scenario: Cache miss triggers API fetch

WHEN Get is called for a resource that is not in the cache or whose cache entry has expired
THEN the store SHALL fetch the resource from the Kubernetes API, process it through the indexer, cache the result, and return it.

#### Scenario: LRU eviction when cache is full

WHEN the cache reaches its maximum size and a new entry is added
THEN the least recently used entry SHALL be evicted.

#### Scenario: Cache TTL reset on access

WHEN a cached resource is accessed via Get before its TTL expires
THEN the cache entry's expiration time SHALL be reset to now plus the configured TTL.

#### Scenario: API fetch failure skips resource silently

WHEN a resource cannot be fetched from the API (e.g., it has been deleted)
THEN the resource SHALL be omitted from the Get results without returning an error.

#### Scenario: List warns about performance impact

WHEN List is called on a CachedStore
THEN a warning SHALL be logged indicating that individual API lookups may be expensive.

### Requirement: Modification Tracking

MemoryStore and CachedStore SHALL implement the ModCounter interface. The ModCount method SHALL return a monotonically increasing counter that is incremented on every mutation (Add, Update, Delete, Clear) and a boolean true indicating tracking is supported. This enables external caching layers to detect store changes without polling.

#### Scenario: ModCount increments on Add

WHEN Add is called successfully on a MemoryStore
THEN ModCount SHALL return a value one greater than before the Add.

#### Scenario: ModCount increments on Clear

WHEN Clear is called on a CachedStore
THEN ModCount SHALL return a value one greater than before the Clear.

#### Scenario: ModCount stable across reads

WHEN only Get and List operations are performed
THEN ModCount SHALL return the same value as before those operations.

### Requirement: Composite Key Matching

Stores SHALL construct composite keys by joining index key values with "/" as separator. Exact-match lookups SHALL use the full composite key. Partial-match lookups SHALL match all entries whose composite key starts with the provided prefix followed by "/".

#### Scenario: Two-key composite key formed correctly

WHEN keys `["default", "my-ingress"]` are used
THEN the composite key SHALL be `"default/my-ingress"`.

#### Scenario: Prefix match returns all resources in namespace

WHEN Get is called with keys `["default"]` on a store with numKeys=2
THEN all resources whose composite key starts with `"default/"` SHALL be returned.

### Requirement: Resource Immutability Contract

Resources stored in MemoryStore are pre-converted at storage time. Callers SHALL NOT mutate resources returned by Get. For exact-match Get, the returned slice is a direct reference to internal data. For List, the returned slice is a fresh copy but the resource objects within are still references to internal data and SHALL NOT be mutated.

#### Scenario: Get exact match returns internal reference

WHEN Get is called with full keys on a MemoryStore
THEN the returned slice SHALL be the same slice object held internally (pointer equality for exact match).

#### Scenario: List returns new slice with shared resource references

WHEN List is called on a MemoryStore
THEN the returned slice SHALL be a newly allocated slice, but the individual resource objects SHALL be references to the same objects stored internally.

### Requirement: StoreOverlay for Dry-Run Simulation

StoreOverlay SHALL represent proposed changes (Additions, Modifications, Deletions) to a store without modifying actual state. Resources in Additions and Modifications SHALL be pre-converted to template-friendly format (floats to ints) at construction time. Factory methods SHALL exist for CREATE, UPDATE, and DELETE operations.

#### Scenario: Create overlay pre-converts the resource

WHEN NewStoreOverlayForCreate is called with an unstructured resource
THEN the overlay SHALL contain the original resource in Additions and its float-to-int converted form in the internal convertedAdditions slice.

#### Scenario: Delete overlay stores namespaced name

WHEN NewStoreOverlayForDelete is called with namespace "default" and name "my-ingress"
THEN the overlay SHALL contain a single NamespacedName deletion entry.

#### Scenario: IsEmpty returns true for empty overlay

WHEN a StoreOverlay has no additions, modifications, or deletions
THEN IsEmpty SHALL return true.

### Requirement: CompositeStore

CompositeStore SHALL wrap a base store with a StoreOverlay to provide a merged read-only view. Get and List SHALL return base store resources excluding those marked for deletion or modification, plus the overlay's converted modifications and additions. Write operations (Add, Update, Delete, Clear) SHALL return a ReadOnlyStoreError.

#### Scenario: Get excludes deleted resources

WHEN the base store contains resource "default/my-ingress" and the overlay marks it for deletion
THEN Get("default", "my-ingress") SHALL return an empty result.

#### Scenario: Get includes overlay additions matching the keys

WHEN the overlay contains an addition with namespace "default" and name "new-ingress"
THEN Get("default", "new-ingress") SHALL return the pre-converted addition.

#### Scenario: Get replaces modified resources

WHEN the base store contains resource "default/my-ingress" and the overlay contains a modification for the same resource
THEN Get("default", "my-ingress") SHALL return the pre-converted modification instead of the base resource.

#### Scenario: List merges base and overlay

WHEN the base store has 3 resources, the overlay deletes 1, modifies 1, and adds 1
THEN List SHALL return 3 resources: 1 unmodified base resource, 1 modified resource, and 1 addition.

#### Scenario: Write operations return ReadOnlyStoreError

WHEN Add, Update, Delete, or Clear is called on a CompositeStore
THEN a ReadOnlyStoreError SHALL be returned identifying the attempted operation.

### Requirement: StoreProvider Abstraction

StoreProvider SHALL provide access to stores by name via GetStore and list available stores via StoreNames. RealStoreProvider SHALL return actual stores from a map. CompositeStoreProvider SHALL wrap a base provider with per-store overlays, returning a CompositeStore for stores that have an overlay and the base store for those that do not.

#### Scenario: CompositeStoreProvider returns CompositeStore when overlay exists

WHEN GetStore is called for a store name that has an associated overlay
THEN a CompositeStore wrapping the base store and overlay SHALL be returned.

#### Scenario: CompositeStoreProvider returns base store when no overlay exists

WHEN GetStore is called for a store name that has no associated overlay
THEN the base store SHALL be returned directly.

#### Scenario: Validate rejects overlays referencing non-existent stores

WHEN Validate is called on a CompositeStoreProvider where an overlay references a store name that does not exist in the base provider
THEN an error SHALL be returned identifying the non-existent store.

### Requirement: OverlayStoreProvider with ValidationContext

OverlayStoreProvider SHALL apply a ValidationContext containing both K8s store overlays and HTTP content overlays. For K8s stores, it SHALL behave like CompositeStoreProvider. IsValidationMode SHALL return true when the ValidationContext is non-empty. GetHTTPOverlay SHALL return the HTTP content overlay for use by the render service.

#### Scenario: IsValidationMode true when context has overlays

WHEN an OverlayStoreProvider is created with a ValidationContext containing at least one non-empty K8s overlay or HTTP overlay
THEN IsValidationMode SHALL return true.

#### Scenario: IsValidationMode false when context is nil

WHEN an OverlayStoreProvider is created with a nil ValidationContext
THEN IsValidationMode SHALL return false, and GetStore SHALL return base stores directly.

### Requirement: TypesStoreAdapter

TypesStoreAdapter SHALL bridge the structurally identical Store interfaces defined in pkg/k8s/types and pkg/stores by delegating all Store method calls to an inner store. If the inner store implements ModCount, the adapter SHALL delegate ModCount. Otherwise, it SHALL return (0, false) to signal that caching based on the count is not supported.

#### Scenario: Adapter delegates Get to inner store

WHEN Get is called on a TypesStoreAdapter
THEN the call SHALL be forwarded to the inner store's Get method with the same arguments.

#### Scenario: Adapter returns (0, false) for non-ModCounter inner

WHEN ModCount is called on a TypesStoreAdapter whose inner store does not implement ModCount
THEN it SHALL return (0, false).
