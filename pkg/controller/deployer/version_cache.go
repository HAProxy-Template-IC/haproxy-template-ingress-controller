package deployer

import (
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// configVersionEntry holds cached config version and parsed config for a single endpoint.
type configVersionEntry struct {
	version         int64
	parsedConfig    *parserconfig.StructuredConfig
	contentChecksum string // Content checksum from last successful sync
}

// configVersionCache caches the last-synced config version and parsed config per endpoint URL.
// This allows subsequent syncs to skip the expensive GetRawConfiguration() + parse when
// the pod's config version hasn't changed since the last successful sync.
//
// Thread-safe: pods sync in parallel goroutines.
type configVersionCache struct {
	mu      sync.RWMutex
	entries map[string]*configVersionEntry
}

// newConfigVersionCache creates an empty config version cache.
func newConfigVersionCache() *configVersionCache {
	return &configVersionCache{
		entries: make(map[string]*configVersionEntry),
	}
}

// get returns the cached version, parsed config, and content checksum for the given endpoint URL.
// Returns (0, nil, "") if no cache entry exists.
func (c *configVersionCache) get(endpointURL string) (version int64, parsedConfig *parserconfig.StructuredConfig, contentChecksum string) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[endpointURL]
	if !ok {
		return 0, nil, ""
	}
	return entry.version, entry.parsedConfig, entry.contentChecksum
}

// set stores the post-sync version, parsed config, and content checksum for the given endpoint URL.
func (c *configVersionCache) set(endpointURL string, version int64, parsedConfig *parserconfig.StructuredConfig, contentChecksum string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[endpointURL] = &configVersionEntry{
		version:         version,
		parsedConfig:    parsedConfig,
		contentChecksum: contentChecksum,
	}
}

// invalidate removes the cache entry for the given endpoint URL.
func (c *configVersionCache) invalidate(endpointURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, endpointURL)
}

// clear removes all cache entries.
func (c *configVersionCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*configVersionEntry)
}
