package deployer

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

func newTestConfig() *parserconfig.StructuredConfig {
	return &parserconfig.StructuredConfig{}
}

func cacheEndpoint(url string) *dataplane.Endpoint {
	return &dataplane.Endpoint{URL: url}
}

func TestConfigVersionCache_GetEmpty(t *testing.T) {
	cache := newConfigVersionCache()

	version, config, checksum := cache.get(cacheEndpoint("http://pod1:5555"))
	assert.Equal(t, int64(0), version)
	assert.Nil(t, config)
	assert.Empty(t, checksum)
}

func TestConfigVersionCache_SetAndGet(t *testing.T) {
	cache := newConfigVersionCache()
	parsed := newTestConfig()

	endpoint := cacheEndpoint("http://pod1:5555")
	cache.set(endpoint, 42, parsed, "abc123")

	version, config, checksum := cache.get(endpoint)
	assert.Equal(t, int64(42), version)
	require.NotNil(t, config)
	assert.Same(t, parsed, config)
	assert.Equal(t, "abc123", checksum)
}

func TestConfigVersionCache_SetOverwrite(t *testing.T) {
	cache := newConfigVersionCache()
	parsed1 := newTestConfig()
	parsed2 := newTestConfig()

	endpoint := cacheEndpoint("http://pod1:5555")
	cache.set(endpoint, 42, parsed1, "hash1")
	cache.set(endpoint, 43, parsed2, "hash2")

	version, config, checksum := cache.get(endpoint)
	assert.Equal(t, int64(43), version)
	assert.Same(t, parsed2, config)
	assert.Equal(t, "hash2", checksum)
}

func TestConfigVersionCache_MultipleEndpoints(t *testing.T) {
	cache := newConfigVersionCache()
	parsed1 := newTestConfig()
	parsed2 := newTestConfig()

	endpoint1 := cacheEndpoint("http://pod1:5555")
	endpoint2 := cacheEndpoint("http://pod2:5555")
	cache.set(endpoint1, 10, parsed1, "hash1")
	cache.set(endpoint2, 20, parsed2, "hash2")

	v1, c1, cs1 := cache.get(endpoint1)
	v2, c2, cs2 := cache.get(endpoint2)

	assert.Equal(t, int64(10), v1)
	assert.Same(t, parsed1, c1)
	assert.Equal(t, "hash1", cs1)
	assert.Equal(t, int64(20), v2)
	assert.Same(t, parsed2, c2)
	assert.Equal(t, "hash2", cs2)
}

func TestConfigVersionCache_Invalidate(t *testing.T) {
	cache := newConfigVersionCache()
	parsed := newTestConfig()

	endpoint1 := cacheEndpoint("http://pod1:5555")
	endpoint2 := cacheEndpoint("http://pod2:5555")
	cache.set(endpoint1, 42, parsed, "hash1")
	cache.set(endpoint2, 43, parsed, "hash2")

	cache.invalidate(endpoint1)

	v1, c1, cs1 := cache.get(endpoint1)
	assert.Equal(t, int64(0), v1)
	assert.Nil(t, c1)
	assert.Empty(t, cs1)

	// pod2 should be unaffected
	v2, c2, cs2 := cache.get(endpoint2)
	assert.Equal(t, int64(43), v2)
	assert.NotNil(t, c2)
	assert.Equal(t, "hash2", cs2)
}

func TestConfigVersionCache_InvalidateNonExistent(t *testing.T) {
	cache := newConfigVersionCache()
	// Should not panic
	cache.invalidate(cacheEndpoint("http://nonexistent:5555"))
}

func TestConfigVersionCache_DoesNotCrossPodUIDAtSameURL(t *testing.T) {
	cache := newConfigVersionCache()
	oldEndpoint := &dataplane.Endpoint{
		URL: "http://10.0.0.1:5555/v3", Username: "admin", Password: "secret",
		PodName: "haproxy-0", PodNamespace: "haptic", PodUID: "uid-old",
		DetectedMajorVersion: 3, DetectedMinorVersion: 2, DetectedFullVersion: "3.2.1",
	}
	cache.set(oldEndpoint, 42, newTestConfig(), "old-checksum")
	cache.setActivated(oldEndpoint, "old-proof")

	replacement := *oldEndpoint
	replacement.PodUID = "uid-new"
	version, parsed, checksum := cache.get(&replacement)
	assert.Zero(t, version)
	assert.Nil(t, parsed)
	assert.Empty(t, checksum)
	assert.Empty(t, cache.activated(&replacement))

	cache.retain([]dataplane.Endpoint{replacement})
	cache.set(oldEndpoint, 43, newTestConfig(), "late-old-checksum")
	cache.setActivated(oldEndpoint, "late-old-proof")
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	assert.Empty(t, cache.entries)
}

func TestConfigVersionCache_Clear(t *testing.T) {
	cache := newConfigVersionCache()
	parsed := newTestConfig()

	endpoint1 := cacheEndpoint("http://pod1:5555")
	endpoint2 := cacheEndpoint("http://pod2:5555")
	cache.set(endpoint1, 42, parsed, "hash1")
	cache.set(endpoint2, 43, parsed, "hash2")

	cache.clear()

	v1, c1, cs1 := cache.get(endpoint1)
	v2, c2, cs2 := cache.get(endpoint2)

	assert.Equal(t, int64(0), v1)
	assert.Nil(t, c1)
	assert.Empty(t, cs1)
	assert.Equal(t, int64(0), v2)
	assert.Nil(t, c2)
	assert.Empty(t, cs2)
}

func TestConfigVersionCache_ConcurrentAccess(t *testing.T) {
	cache := newConfigVersionCache()
	parsed := newTestConfig()

	var wg sync.WaitGroup
	endpoints := []dataplane.Endpoint{
		{URL: "http://pod1:5555"},
		{URL: "http://pod2:5555"},
		{URL: "http://pod3:5555"},
		{URL: "http://pod4:5555"},
	}

	// Concurrent writes
	for i, ep := range endpoints {
		wg.Add(1)
		go func(endpoint dataplane.Endpoint, version int64) {
			defer wg.Done()
			for j := range 100 {
				cache.set(&endpoint, version+int64(j), parsed, "hash")
			}
		}(ep, int64(i*100))
	}

	// Concurrent reads
	for _, ep := range endpoints {
		wg.Add(1)
		go func(endpoint dataplane.Endpoint) {
			defer wg.Done()
			for range 100 {
				cache.get(&endpoint)
			}
		}(ep)
	}

	// Concurrent invalidations
	wg.Go(func() {
		for range 50 {
			cache.invalidate(&endpoints[0])
		}
	})

	// Concurrent clear
	wg.Go(func() {
		for range 10 {
			cache.clear()
		}
	})

	// Should not race or panic
	wg.Wait()
}
