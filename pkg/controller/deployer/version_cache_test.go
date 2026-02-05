package deployer

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

func newTestConfig() *parserconfig.StructuredConfig {
	return &parserconfig.StructuredConfig{}
}

func TestConfigVersionCache_GetEmpty(t *testing.T) {
	cache := newConfigVersionCache()

	version, config := cache.get("http://pod1:5555")
	assert.Equal(t, int64(0), version)
	assert.Nil(t, config)
}

func TestConfigVersionCache_SetAndGet(t *testing.T) {
	cache := newConfigVersionCache()
	parsed := newTestConfig()

	cache.set("http://pod1:5555", 42, parsed)

	version, config := cache.get("http://pod1:5555")
	assert.Equal(t, int64(42), version)
	require.NotNil(t, config)
	assert.Same(t, parsed, config)
}

func TestConfigVersionCache_SetOverwrite(t *testing.T) {
	cache := newConfigVersionCache()
	parsed1 := newTestConfig()
	parsed2 := newTestConfig()

	cache.set("http://pod1:5555", 42, parsed1)
	cache.set("http://pod1:5555", 43, parsed2)

	version, config := cache.get("http://pod1:5555")
	assert.Equal(t, int64(43), version)
	assert.Same(t, parsed2, config)
}

func TestConfigVersionCache_MultipleEndpoints(t *testing.T) {
	cache := newConfigVersionCache()
	parsed1 := newTestConfig()
	parsed2 := newTestConfig()

	cache.set("http://pod1:5555", 10, parsed1)
	cache.set("http://pod2:5555", 20, parsed2)

	v1, c1 := cache.get("http://pod1:5555")
	v2, c2 := cache.get("http://pod2:5555")

	assert.Equal(t, int64(10), v1)
	assert.Same(t, parsed1, c1)
	assert.Equal(t, int64(20), v2)
	assert.Same(t, parsed2, c2)
}

func TestConfigVersionCache_Invalidate(t *testing.T) {
	cache := newConfigVersionCache()
	parsed := newTestConfig()

	cache.set("http://pod1:5555", 42, parsed)
	cache.set("http://pod2:5555", 43, parsed)

	cache.invalidate("http://pod1:5555")

	v1, c1 := cache.get("http://pod1:5555")
	assert.Equal(t, int64(0), v1)
	assert.Nil(t, c1)

	// pod2 should be unaffected
	v2, c2 := cache.get("http://pod2:5555")
	assert.Equal(t, int64(43), v2)
	assert.NotNil(t, c2)
}

func TestConfigVersionCache_InvalidateNonExistent(t *testing.T) {
	cache := newConfigVersionCache()
	// Should not panic
	cache.invalidate("http://nonexistent:5555")
}

func TestConfigVersionCache_Clear(t *testing.T) {
	cache := newConfigVersionCache()
	parsed := newTestConfig()

	cache.set("http://pod1:5555", 42, parsed)
	cache.set("http://pod2:5555", 43, parsed)

	cache.clear()

	v1, c1 := cache.get("http://pod1:5555")
	v2, c2 := cache.get("http://pod2:5555")

	assert.Equal(t, int64(0), v1)
	assert.Nil(t, c1)
	assert.Equal(t, int64(0), v2)
	assert.Nil(t, c2)
}

func TestConfigVersionCache_ConcurrentAccess(t *testing.T) {
	cache := newConfigVersionCache()
	parsed := newTestConfig()

	var wg sync.WaitGroup
	endpoints := []string{
		"http://pod1:5555",
		"http://pod2:5555",
		"http://pod3:5555",
		"http://pod4:5555",
	}

	// Concurrent writes
	for i, ep := range endpoints {
		wg.Add(1)
		go func(url string, version int64) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cache.set(url, version+int64(j), parsed)
			}
		}(ep, int64(i*100))
	}

	// Concurrent reads
	for _, ep := range endpoints {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cache.get(url)
			}
		}(ep)
	}

	// Concurrent invalidations
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			cache.invalidate(endpoints[0])
		}
	}()

	// Concurrent clear
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 10; j++ {
			cache.clear()
		}
	}()

	// Should not race or panic
	wg.Wait()
}
