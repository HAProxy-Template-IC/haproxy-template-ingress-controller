package deployer

import (
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
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

// configVersionCache caches the last-synced config version and parsed config per endpoint authority.
// This allows subsequent syncs to skip the expensive GetRawConfiguration() + parse when
// the pod's config version hasn't changed since the last successful sync.
//
// Thread-safe: pods sync in parallel goroutines.
type configVersionCache struct {
	mu             sync.RWMutex
	entries        map[endpointAuthority]*configVersionEntry
	authorities    map[endpointAuthority]struct{}
	authoritiesSet bool
}

// newConfigVersionCache creates an empty config version cache.
func newConfigVersionCache() *configVersionCache {
	return &configVersionCache{
		entries:     make(map[endpointAuthority]*configVersionEntry),
		authorities: make(map[endpointAuthority]struct{}),
	}
}

// get returns the cached version, parsed config, and content checksum for the given endpoint authority.
// Returns (0, nil, "") if no cache entry exists.
func (c *configVersionCache) get(endpoint *dataplane.Endpoint) (version int64, parsedConfig *parserconfig.StructuredConfig, contentChecksum string) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[endpointAuthorityOf(endpoint)]
	if !ok {
		return 0, nil, ""
	}
	return entry.version, entry.parsedConfig, entry.contentChecksum
}

// activated returns the checksum of the config this endpoint was last proven to
// be running, or "" when nothing has been proven.
func (c *configVersionCache) activated(endpoint *dataplane.Endpoint) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[endpointAuthorityOf(endpoint)]
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
func (c *configVersionCache) setActivated(endpoint *dataplane.Endpoint, proof string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	authority := endpointAuthorityOf(endpoint)
	if !c.authorityCurrentLocked(&authority) {
		return
	}
	entry, ok := c.entries[authority]
	if !ok {
		if proof == "" {
			return
		}
		c.entries[authority] = &configVersionEntry{activatedChecksum: proof}
		return
	}
	entry.activatedChecksum = proof
}

// set stores the post-sync version, parsed config, and content checksum for the endpoint authority.
func (c *configVersionCache) set(endpoint *dataplane.Endpoint, version int64, parsedConfig *parserconfig.StructuredConfig, contentChecksum string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Preserve the activation proof across a version-cache update: set() is
	// about the version/parse cache, and the two have different lifetimes.
	activated := ""
	authority := endpointAuthorityOf(endpoint)
	if !c.authorityCurrentLocked(&authority) {
		return
	}
	if existing, ok := c.entries[authority]; ok {
		activated = existing.activatedChecksum
	}
	c.entries[authority] = &configVersionEntry{
		version:           version,
		parsedConfig:      parsedConfig,
		contentChecksum:   contentChecksum,
		activatedChecksum: activated,
	}
}

// invalidate removes the cache entry for the endpoint authority.
func (c *configVersionCache) invalidate(endpoint *dataplane.Endpoint) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, endpointAuthorityOf(endpoint))
}

// retain removes observations for endpoint authorities no longer present in
// the discovered fleet.
func (c *configVersionCache) retain(endpoints []dataplane.Endpoint) {
	live := endpointAuthoritySet(endpoints)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authorities = live
	c.authoritiesSet = true
	for authority := range c.entries {
		if _, ok := live[authority]; !ok {
			delete(c.entries, authority)
		}
	}
}

func (c *configVersionCache) authorityCurrentLocked(authority *endpointAuthority) bool {
	if !c.authoritiesSet {
		return true
	}
	_, ok := c.authorities[*authority]
	return ok
}

// clear removes all cache entries.
func (c *configVersionCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[endpointAuthority]*configVersionEntry)
	c.authorities = make(map[endpointAuthority]struct{})
	c.authoritiesSet = false
}
