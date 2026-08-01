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
	// activatedChecksum is the on-disk config this endpoint was last PROVEN to
	// be running (see dataplane.SyncOptions.LastActivatedConfigChecksum). Empty
	// means "never proven", which the orchestrator treats as "force a reload
	// before trusting an empty diff" — not as "unchanged".
	activatedChecksum string
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

// activated returns the checksum of the config this endpoint was last proven to
// be running, or "" when nothing has been proven.
func (c *configVersionCache) activated(endpointURL string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[endpointURL]
	if !ok {
		return ""
	}
	return entry.activatedChecksum
}

// setActivated records a fresh activation proof without disturbing the version
// cache, and clears it when proof is "" — the sync proved nothing, so the next
// empty diff must not be trusted.
//
// Clearing matters most on the error path: a skip_version push writes its body
// to disk even when the runtime actions fail, so a failed apply leaves content
// on disk that no worker loaded. Keeping a stale proof there would let the next
// sync short-circuit an empty diff over parked content — the #112 stall.
func (c *configVersionCache) setActivated(endpointURL, proof string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[endpointURL]
	if !ok {
		if proof == "" {
			return
		}
		c.entries[endpointURL] = &configVersionEntry{activatedChecksum: proof}
		return
	}
	entry.activatedChecksum = proof
}

// set stores the post-sync version, parsed config, and content checksum for the given endpoint URL.
func (c *configVersionCache) set(endpointURL string, version int64, parsedConfig *parserconfig.StructuredConfig, contentChecksum string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Preserve the activation proof across a version-cache update: set() is
	// about the version/parse cache, and the two have different lifetimes.
	activated := ""
	if existing, ok := c.entries[endpointURL]; ok {
		activated = existing.activatedChecksum
	}
	c.entries[endpointURL] = &configVersionEntry{
		version:           version,
		parsedConfig:      parsedConfig,
		contentChecksum:   contentChecksum,
		activatedChecksum: activated,
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
