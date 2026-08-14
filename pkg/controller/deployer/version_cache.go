package deployer

import (
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

type configVersionEntry struct {
	version               int64
	parsedConfig          *parserconfig.StructuredConfig
	currentConfigChecksum string
	contentChecksum       string
	activatedChecksum     string
}

type configVersionSnapshot struct {
	generation            uint64
	version               int64
	parsedConfig          *parserconfig.StructuredConfig
	currentConfigChecksum string
	contentChecksum       string
	activatedChecksum     string
}

// configVersionCache keeps each endpoint observation and its write fence under
// one lock. A generation changes before and after every runtime-bypass write.
type configVersionCache struct {
	mu             sync.Mutex
	entries        map[endpointAuthority]*configVersionEntry
	generations    map[endpointAuthority]uint64
	nextGeneration uint64
	authorities    map[endpointAuthority]struct{}
	authoritiesSet bool
}

func newConfigVersionCache() *configVersionCache {
	return &configVersionCache{
		entries:     make(map[endpointAuthority]*configVersionEntry),
		generations: make(map[endpointAuthority]uint64),
		authorities: make(map[endpointAuthority]struct{}),
	}
}

func (c *configVersionCache) snapshot(endpoint *dataplane.Endpoint) configVersionSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	authority := endpointAuthorityOf(endpoint)
	if !c.authorityCurrentLocked(&authority) {
		return configVersionSnapshot{}
	}
	generation, ok := c.generations[authority]
	if !ok {
		generation = c.advanceLocked(&authority)
	}
	snapshot := configVersionSnapshot{generation: generation}
	if entry := c.entries[authority]; entry != nil {
		snapshot.version = entry.version
		snapshot.parsedConfig = entry.parsedConfig
		snapshot.currentConfigChecksum = entry.currentConfigChecksum
		snapshot.contentChecksum = entry.contentChecksum
		snapshot.activatedChecksum = entry.activatedChecksum
	}
	return snapshot
}

func (c *configVersionCache) commitSync(
	endpoint *dataplane.Endpoint,
	generation uint64,
	version int64,
	parsedConfig *parserconfig.StructuredConfig,
	currentConfigChecksum string,
	contentChecksum string,
	activatedChecksum string,
) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	authority := endpointAuthorityOf(endpoint)
	if generation == 0 || !c.authorityCurrentLocked(&authority) || c.generations[authority] != generation {
		return false
	}
	entry := &configVersionEntry{activatedChecksum: activatedChecksum}
	if version > 1 && parsedConfig != nil && currentConfigChecksum != "" && currentConfigChecksum == activatedChecksum {
		entry.version = version
		entry.parsedConfig = parsedConfig
		entry.currentConfigChecksum = currentConfigChecksum
		entry.contentChecksum = contentChecksum
	}
	if entry.parsedConfig == nil && entry.activatedChecksum == "" {
		delete(c.entries, authority)
	} else {
		c.entries[authority] = entry
	}
	return true
}

func (c *configVersionCache) abortSync(endpoint *dataplane.Endpoint, generation uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	authority := endpointAuthorityOf(endpoint)
	if generation == 0 || !c.authorityCurrentLocked(&authority) || c.generations[authority] != generation {
		return false
	}
	delete(c.entries, authority)
	return true
}

// beginRuntimeMutation removes every reusable observation before the pod write.
func (c *configVersionCache) beginRuntimeMutation(endpoint *dataplane.Endpoint) (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	authority := endpointAuthorityOf(endpoint)
	if !c.authorityCurrentLocked(&authority) {
		return 0, false
	}
	generation := c.advanceLocked(&authority)
	delete(c.entries, authority)
	return generation, true
}

// finishRuntimeMutation fences structural snapshots taken while the write ran.
func (c *configVersionCache) finishRuntimeMutation(endpoint *dataplane.Endpoint, generation uint64, proof string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	authority := endpointAuthorityOf(endpoint)
	if !c.authorityCurrentLocked(&authority) || c.generations[authority] != generation {
		return false
	}
	c.advanceLocked(&authority)
	if proof == "" {
		delete(c.entries, authority)
	} else {
		c.entries[authority] = &configVersionEntry{activatedChecksum: proof}
	}
	return true
}

func (c *configVersionCache) retain(endpoints []dataplane.Endpoint) {
	live := endpointAuthoritySet(endpoints)
	c.mu.Lock()
	defer c.mu.Unlock()

	c.authorities = live
	c.authoritiesSet = true
	for authority := range c.generations {
		if _, ok := live[authority]; ok {
			continue
		}
		delete(c.generations, authority)
		delete(c.entries, authority)
	}
}

func (c *configVersionCache) authorityCurrentLocked(authority *endpointAuthority) bool {
	if !c.authoritiesSet {
		return true
	}
	_, ok := c.authorities[*authority]
	return ok
}

func (c *configVersionCache) advanceLocked(authority *endpointAuthority) uint64 {
	c.nextGeneration++
	c.generations[*authority] = c.nextGeneration
	return c.nextGeneration
}

func (c *configVersionCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[endpointAuthority]*configVersionEntry)
	c.generations = make(map[endpointAuthority]uint64)
	c.authorities = make(map[endpointAuthority]struct{})
	c.authoritiesSet = false
}
