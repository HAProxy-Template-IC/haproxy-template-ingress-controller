package store

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"haptic/pkg/k8s/indexer"
	"haptic/pkg/k8s/types"
)

// cacheEntry holds a cached resource with its expiration time.
type cacheEntry struct {
	resource  interface{}
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
	refs      map[string][]resourceRef    // Composite key -> slice of resource references
	cache     map[string]*cacheEntry      // Cache key (namespace/name) -> cached resource
	numKeys   int                         // Number of index keys
	cacheTTL  time.Duration               // Cache entry TTL
	client    dynamic.Interface           // Kubernetes dynamic client
	gvr       schema.GroupVersionResource // Resource type to fetch
	namespace string                      // Namespace for fetching (empty = all)
	indexer   *indexer.Indexer            // Indexer for processing fetched resources
	logger    *slog.Logger                // Logger for debug and warning messages
}

// CachedStoreConfig configures a CachedStore.
type CachedStoreConfig struct {
	// NumKeys is the number of index keys (must match indexer configuration)
	NumKeys int

	// CacheTTL is the cache entry time-to-live
	CacheTTL time.Duration

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
}

// NewCachedStore creates a new API-backed store with caching.
func NewCachedStore(cfg *CachedStoreConfig) (*CachedStore, error) {
	if cfg.NumKeys < 1 {
		return nil, fmt.Errorf("numKeys must be at least 1")
	}
	if cfg.Client == nil {
		return nil, fmt.Errorf("client is required")
	}
	if cfg.Indexer == nil {
		return nil, fmt.Errorf("indexer is required")
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = 2*time.Minute + 10*time.Second
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &CachedStore{
		refs:      make(map[string][]resourceRef),
		cache:     make(map[string]*cacheEntry),
		numKeys:   cfg.NumKeys,
		cacheTTL:  cfg.CacheTTL,
		client:    cfg.Client,
		gvr:       cfg.GVR,
		namespace: cfg.Namespace,
		indexer:   cfg.Indexer,
		logger:    logger,
	}, nil
}

// Get retrieves all resources matching the provided index keys.
func (s *CachedStore) Get(keys ...string) ([]interface{}, error) {
	if len(keys) == 0 {
		return nil, &StoreError{
			Operation: "get",
			Keys:      keys,
			Cause:     fmt.Errorf("at least one key required"),
		}
	}

	if len(keys) > s.numKeys {
		return nil, &StoreError{
			Operation: "get",
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
	results := make([]interface{}, 0, len(matchingRefs))
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

// List returns all resources in the store.
func (s *CachedStore) List() ([]interface{}, error) {
	s.mu.RLock()
	allRefs := make([]resourceRef, 0)
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
	results := make([]interface{}, 0, len(allRefs))
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
func (s *CachedStore) Add(resource interface{}, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(keys) != s.numKeys {
		return &StoreError{
			Operation: "add",
			Keys:      keys,
			Cause:     fmt.Errorf("wrong number of keys: got %d, expected %d", len(keys), s.numKeys),
		}
	}

	// Extract namespace and name from resource
	ns, name := extractNamespaceName(resource)

	// Create resource reference
	ref := resourceRef{
		namespace: ns,
		name:      name,
		indexKeys: keys,
	}

	keyStr := makeKeyString(keys)
	s.refs[keyStr] = append(s.refs[keyStr], ref)

	// Cache the resource using namespace/name as cache key
	cacheKey := ns + "/" + name
	s.cache[cacheKey] = &cacheEntry{
		resource:  resource,
		expiresAt: time.Now().Add(s.cacheTTL),
	}

	return nil
}

// Update modifies an existing resource or adds it if it doesn't exist.
func (s *CachedStore) Update(resource interface{}, keys []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(keys) != s.numKeys {
		return &StoreError{
			Operation: "update",
			Keys:      keys,
			Cause:     fmt.Errorf("wrong number of keys: got %d, expected %d", len(keys), s.numKeys),
		}
	}

	// Extract namespace and name from resource
	ns, name := extractNamespaceName(resource)

	keyStr := makeKeyString(keys)
	refs, ok := s.refs[keyStr]
	if !ok {
		// No resources with these keys - add new
		ref := resourceRef{
			namespace: ns,
			name:      name,
			indexKeys: keys,
		}
		s.refs[keyStr] = []resourceRef{ref}
	} else {
		// Try to find existing resource by namespace+name
		found := false
		for i, existingRef := range refs {
			if existingRef.namespace == ns && existingRef.name == name {
				// Update index keys (in case they changed)
				refs[i].indexKeys = keys
				s.refs[keyStr] = refs
				found = true
				break
			}
		}

		if !found {
			// Resource not found - append
			ref := resourceRef{
				namespace: ns,
				name:      name,
				indexKeys: keys,
			}
			s.refs[keyStr] = append(refs, ref)
		}
	}

	// Update cache using namespace/name as cache key
	cacheKey := ns + "/" + name
	s.cache[cacheKey] = &cacheEntry{
		resource:  resource,
		expiresAt: time.Now().Add(s.cacheTTL),
	}

	return nil
}

// Delete removes a resource from the store.
// NOTE: With non-unique index keys, this removes ALL resources matching the provided keys.
func (s *CachedStore) Delete(keys ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(keys) != s.numKeys {
		return &StoreError{
			Operation: "delete",
			Keys:      keys,
			Cause:     fmt.Errorf("wrong number of keys: got %d, expected %d", len(keys), s.numKeys),
		}
	}

	keyStr := makeKeyString(keys)
	refs, ok := s.refs[keyStr]
	if !ok {
		return nil
	}

	// Delete cache entries for all matching resources
	for _, ref := range refs {
		cacheKey := ref.namespace + "/" + ref.name
		delete(s.cache, cacheKey)
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
	s.cache = make(map[string]*cacheEntry)

	return nil
}

// fetchResourceByRef fetches a resource from cache or API using a resource reference.
func (s *CachedStore) fetchResourceByRef(ref resourceRef) (interface{}, error) {
	cacheKey := ref.namespace + "/" + ref.name

	// Check cache first
	s.mu.RLock()
	entry, ok := s.cache[cacheKey]
	now := time.Now()
	if ok && now.Before(entry.expiresAt) {
		// Cache hit - upgrade to write lock to reset TTL
		s.mu.RUnlock()
		s.mu.Lock()
		// Re-check after acquiring write lock (entry might have been modified)
		if entry, ok := s.cache[cacheKey]; ok && now.Before(entry.expiresAt) {
			// Reset TTL by extending expiration time
			entry.expiresAt = time.Now().Add(s.cacheTTL)
			resource := entry.resource
			s.mu.Unlock()
			return resource, nil
		}
		s.mu.Unlock()
		// If we get here, entry was evicted between locks - fall through to fetch from API
	} else {
		s.mu.RUnlock()
	}

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

	// Process resource (field filtering)
	if err := s.indexer.FilterFields(resource); err != nil {
		return nil, &StoreError{
			Operation: "process",
			Keys:      []string{ref.namespace, ref.name},
			Cause:     err,
		}
	}

	// Update cache
	s.mu.Lock()
	s.cache[cacheKey] = &cacheEntry{
		resource:  resource,
		expiresAt: time.Now().Add(s.cacheTTL),
	}
	s.mu.Unlock()

	return resource, nil
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

// CacheSize returns the number of cached resources.
func (s *CachedStore) CacheSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.cache)
}

// EvictExpired removes expired cache entries.
func (s *CachedStore) EvictExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	evicted := 0

	for key, entry := range s.cache {
		if now.After(entry.expiresAt) {
			delete(s.cache, key)
			evicted++
		}
	}

	return evicted
}

// Ensure CachedStore implements types.Store interface.
var _ types.Store = (*CachedStore)(nil)
