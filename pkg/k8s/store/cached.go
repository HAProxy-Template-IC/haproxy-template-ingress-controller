package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// cacheEntry holds a cached resource with its expiration time.
type cacheEntry struct {
	resource  any
	expiresAt time.Time
}

// resourceRef holds a reference to a Kubernetes resource for API fetching.
// Stores both the unique identifier (namespace+name) and the index keys.
type resourceRef struct {
	namespace string   // Resource namespace (empty for cluster-scoped)
	name      string   // Resource name
	indexKeys []string // Index key values for this resource
}

// CachedStore stores only resource references in memory and fetches resources from
// the Kubernetes API on access. Fetched resources are cached with a TTL.
//
// This reduces memory usage for large resources (e.g., Secrets) at the cost
// of API latency on cache misses.
//
// Supports non-unique index keys by storing multiple resource references per composite key.
//
// Thread-safe for concurrent access.
type CachedStore struct {
	mu        sync.RWMutex
	refs      map[string][]resourceRef        // Composite key -> slice of resource references
	cache     *lru.Cache[string, *cacheEntry] // LRU cache: namespace/name -> cached resource
	numKeys   int                             // Number of index keys
	cacheTTL  time.Duration                   // Cache entry TTL
	client    dynamic.Interface               // Kubernetes dynamic client
	gvr       schema.GroupVersionResource     // Resource type to fetch
	namespace string                          // Namespace for fetching (empty = all)
	indexer   *indexer.Indexer                // Indexer for processing fetched resources
	logger    *slog.Logger                    // Logger for debug and warning messages
	projected bool                            // Informer delivers body-stripped objects (see Projected)
}

// DefaultMaxCacheSize is the default maximum number of entries in the LRU cache.
const DefaultMaxCacheSize = 256

// CachedStoreConfig configures a CachedStore.
type CachedStoreConfig struct {
	// NumKeys is the number of index keys (must match indexer configuration)
	NumKeys int

	// CacheTTL is the cache entry time-to-live
	CacheTTL time.Duration

	// MaxCacheSize is the maximum number of entries in the LRU cache.
	// When exceeded, the least recently used entry is evicted.
	// Default: 256
	MaxCacheSize int

	// Client is the Kubernetes dynamic client for fetching resources
	Client dynamic.Interface

	// GVR identifies the resource type to fetch
	GVR schema.GroupVersionResource

	// Namespace restricts fetching to a specific namespace (empty = all namespaces)
	Namespace string

	// Indexer processes fetched resources (field filtering)
	Indexer *indexer.Indexer

	// Logger for debug and warning messages (optional, uses slog.Default if nil)
	Logger *slog.Logger

	// Projected indicates the informer feeding this store delivers
	// body-stripped (projected) objects — only identity, indexBy, and
	// fieldSelector fields survive (see pkg/k8s/watcher projection, ADR-0012).
	//
	// In projected mode the store must NOT cache the projected body on
	// Add/Update (a warm render read would otherwise be served a husk).
	// Instead the value cache is populated only by the live API GET in
	// fetchResourceByRef (full body), and Update/Delete invalidate any stale
	// cached body so the next read re-fetches. Off by default; on only for
	// `store: on-demand` (CachedStore) watchers.
	Projected bool
}

// NewCachedStore creates a new API-backed store with caching.
func NewCachedStore(cfg *CachedStoreConfig) (*CachedStore, error) {
	if cfg.NumKeys < 1 {
		return nil, errors.New("numKeys must be at least 1")
	}
	if cfg.Client == nil {
		return nil, errors.New("client is required")
	}
	if cfg.Indexer == nil {
		return nil, errors.New("indexer is required")
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = 2*time.Minute + 10*time.Second
	}
	if cfg.MaxCacheSize <= 0 {
		cfg.MaxCacheSize = DefaultMaxCacheSize
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	cache, err := lru.New[string, *cacheEntry](cfg.MaxCacheSize)
	if err != nil {
		return nil, fmt.Errorf("creating LRU cache: %w", err)
	}

	return &CachedStore{
		refs:      make(map[string][]resourceRef),
		cache:     cache,
		numKeys:   cfg.NumKeys,
		cacheTTL:  cfg.CacheTTL,
		client:    cfg.Client,
		gvr:       cfg.GVR,
		namespace: cfg.Namespace,
		indexer:   cfg.Indexer,
		logger:    logger,
		projected: cfg.Projected,
	}, nil
}

// Get retrieves all resources matching the provided index keys.
func (s *CachedStore) Get(keys ...string) ([]any, error) {
	if len(keys) == 0 {
		return nil, &StoreError{
			Operation: opGet,
			Keys:      keys,
			Cause:     errors.New("at least one key required"),
		}
	}

	if len(keys) > s.numKeys {
		return nil, &StoreError{
			Operation: opGet,
			Keys:      keys,
			Cause:     fmt.Errorf("too many keys: got %d, expected %d", len(keys), s.numKeys),
		}
	}

	// Find matching resource references while holding RLock
	s.mu.RLock()
	var matchingRefs []resourceRef

	if len(keys) == s.numKeys {
		// Exact match
		keyStr := makeKeyString(keys)
		if refs, ok := s.refs[keyStr]; ok {
			matchingRefs = append(matchingRefs, refs...)
		}
	} else {
		// Partial match
		prefix := makeKeyString(keys) + "/"
		for keyStr, refs := range s.refs {
			if len(keyStr) >= len(prefix) && keyStr[:len(prefix)] == prefix {
				matchingRefs = append(matchingRefs, refs...)
			}
		}
	}
	s.mu.RUnlock()

	// Fetch resources using namespace+name from references
	// IMPORTANT: Don't hold any locks while calling fetchResourceByRef,
	// as it may need to acquire a Lock to reset TTL
	results := make([]any, 0, len(matchingRefs))
	for _, ref := range matchingRefs {
		resource, err := s.fetchResourceByRef(ref)
		if err != nil {
			// Skip resources that can't be fetched (may be deleted)
			continue
		}
		results = append(results, resource)
	}

	return results, nil
}

// ListCached returns only resources currently warm in the LRU cache —
// no API fetches. Used by callers that want to prime a per-render
// snapshot with whatever's free, without paying for the full
// store-wide List() fan-out. The slice may be a small subset of
// what's in the cluster (the LRU is `MaxCacheSize` entries; unaccessed
// references contribute nothing). Expired entries are skipped.
//
// Callers that need cluster-wide iteration should still call List(),
// accepting the WARN and per-reference API fetch cost.
func (s *CachedStore) ListCached() ([]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]any, 0, s.cache.Len())
	now := time.Now()
	for _, cacheKey := range s.cache.Keys() {
		entry, ok := s.cache.Peek(cacheKey)
		if !ok || now.After(entry.expiresAt) {
			continue
		}
		results = append(results, entry.resource)
	}
	return results, nil
}

// List returns all resources in the store.
func (s *CachedStore) List() ([]any, error) {
	s.mu.RLock()
	var allRefs []resourceRef
	for _, refs := range s.refs {
		allRefs = append(allRefs, refs...)
	}
	s.mu.RUnlock()

	// Warn about potential performance impact
	s.logger.Warn("Listing cached store causes individual API lookups which may be expensive",
		"gvr", s.gvr.String(),
		"resource_count", len(allRefs),
		"recommendation", "consider using store=full for frequently listed resources")

	// Fetch all resources
	results := make([]any, 0, len(allRefs))
	for _, ref := range allRefs {
		resource, err := s.fetchResourceByRef(ref)
		if err != nil {
			// Skip resources that can't be fetched
			continue
		}
		results = append(results, resource)
	}

	return results, nil
}

// Add inserts a new resource into the store.
func (s *CachedStore) Add(resource any, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateKeyCount("add", keys, s.numKeys); err != nil {
		return err
	}

	ns, name := extractNamespaceName(resource)
	keyStr := makeKeyString(keys)
	s.refs[keyStr] = append(s.refs[keyStr], resourceRef{namespace: ns, name: name, indexKeys: keys})
	if !s.projected {
		// Non-projected: the informer delivered a full body, so cache it for
		// free. Projected: the body is a husk — leave the value cache to be
		// populated by the live API GET in fetchResourceByRef.
		s.cacheResource(ns, name, resource)
	}

	return nil
}

// Update modifies an existing resource or adds it if it doesn't exist.
func (s *CachedStore) Update(resource any, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateKeyCount("update", keys, s.numKeys); err != nil {
		return err
	}

	ns, name := extractNamespaceName(resource)
	keyStr := makeKeyString(keys)
	refs := s.refs[keyStr]

	updated := false
	for i, existingRef := range refs {
		if existingRef.namespace == ns && existingRef.name == name {
			// Update index keys in case they changed
			refs[i].indexKeys = keys
			updated = true
			break
		}
	}
	if !updated {
		refs = append(refs, resourceRef{namespace: ns, name: name, indexKeys: keys})
	}
	s.refs[keyStr] = refs

	if s.projected {
		// Projected: the new body is a husk. Invalidate any stale full body
		// so the next render read re-fetches the current object live.
		s.cache.Remove(ns + "/" + name)
	} else {
		s.cacheResource(ns, name, resource)
	}

	return nil
}

// Delete removes a resource from the store.
// NOTE: With non-unique index keys, this removes ALL resources matching the provided keys.
func (s *CachedStore) Delete(keys ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateKeyCount("delete", keys, s.numKeys); err != nil {
		return err
	}

	keyStr := makeKeyString(keys)
	refs, ok := s.refs[keyStr]
	if !ok {
		return nil
	}

	// Delete cache entries for all matching resources
	for _, ref := range refs {
		cacheKey := ref.namespace + "/" + ref.name
		s.cache.Remove(cacheKey)
	}

	// Delete the refs entry
	delete(s.refs, keyStr)

	return nil
}

// Clear removes all resources from the store.
func (s *CachedStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.refs = make(map[string][]resourceRef)
	s.cache.Purge()

	return nil
}

// fetchResourceByRef fetches a resource from cache or API using a resource reference.
func (s *CachedStore) fetchResourceByRef(ref resourceRef) (any, error) {
	cacheKey := ref.namespace + "/" + ref.name

	// Check cache first using Peek to avoid promoting before TTL check
	s.mu.RLock()
	entry, ok := s.cache.Peek(cacheKey)
	now := time.Now()
	if ok && now.Before(entry.expiresAt) {
		resource := entry.resource
		s.mu.RUnlock()
		// Reset TTL by re-adding with new expiration (Get promotes, but we also need new TTL)
		s.mu.Lock()
		s.cacheResource(ref.namespace, ref.name, resource)
		s.mu.Unlock()
		return resource, nil
	}
	s.mu.RUnlock()

	// Cache miss - fetch from API using namespace+name
	var resource *unstructured.Unstructured
	var err error

	fetchStart := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if s.namespace != "" || ref.namespace != "" {
		// Namespaced resource
		ns := s.namespace
		if ns == "" {
			ns = ref.namespace
		}
		resource, err = s.client.Resource(s.gvr).Namespace(ns).Get(ctx, ref.name, metav1.GetOptions{})
	} else {
		// Cluster-scoped resource
		resource, err = s.client.Resource(s.gvr).Get(ctx, ref.name, metav1.GetOptions{})
	}

	fetchDuration := time.Since(fetchStart)

	if err != nil {
		return nil, &StoreError{
			Operation: "fetch",
			Keys:      []string{ref.namespace, ref.name},
			Cause:     err,
		}
	}

	// Log cache miss with timing info
	s.logger.Debug("Fetching uncached resource from API",
		"gvr", s.gvr.String(),
		"namespace", ref.namespace,
		"name", ref.name,
		"duration_ms", fetchDuration.Milliseconds(),
	)

	// Process resource (field filtering and conversion)
	result, err := s.indexer.Process(resource)
	if err != nil {
		return nil, &StoreError{
			Operation: "process",
			Keys:      []string{ref.namespace, ref.name},
			Cause:     err,
		}
	}

	// Update cache with converted resource
	s.mu.Lock()
	s.cacheResource(ref.namespace, ref.name, result.ConvertedResource)
	s.mu.Unlock()

	return result.ConvertedResource, nil
}

// cacheResource stores a resource in the LRU cache keyed by namespace/name with a fresh TTL.
// The caller must hold s.mu (for write).
func (s *CachedStore) cacheResource(namespace, name string, resource any) {
	s.cache.Add(namespace+"/"+name, &cacheEntry{
		resource:  resource,
		expiresAt: time.Now().Add(s.cacheTTL),
	})
}

// Size returns the number of tracked resources in the store.
func (s *CachedStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, refs := range s.refs {
		count += len(refs)
	}
	return count
}

// Ensure CachedStore implements types.Store interface.
var _ types.Store = (*CachedStore)(nil)
