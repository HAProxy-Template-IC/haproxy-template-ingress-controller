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

func commitTestObservation(t *testing.T, cache *configVersionCache, endpoint *dataplane.Endpoint, version int64, parsed *parserconfig.StructuredConfig, currentChecksum, contentChecksum, proof string) configVersionSnapshot {
	t.Helper()
	snapshot := cache.snapshot(endpoint)
	require.True(t, cache.commitSync(endpoint, snapshot.generation, version, parsed, currentChecksum, contentChecksum, proof))
	return cache.snapshot(endpoint)
}

func TestConfigVersionCache_SnapshotIsAtomic(t *testing.T) {
	cache := newConfigVersionCache()
	endpoint := cacheEndpoint("http://pod1:5555")
	parsed := newTestConfig()

	snapshot := commitTestObservation(t, cache, endpoint, 42, parsed, "raw-42", "content-42", "raw-42")

	assert.NotZero(t, snapshot.generation)
	assert.Equal(t, int64(42), snapshot.version)
	assert.Same(t, parsed, snapshot.parsedConfig)
	assert.Equal(t, "raw-42", snapshot.currentConfigChecksum)
	assert.Equal(t, "content-42", snapshot.contentChecksum)
	assert.Equal(t, "raw-42", snapshot.activatedChecksum)
}

func TestConfigVersionCache_DoesNotCacheUnprovenTuple(t *testing.T) {
	cache := newConfigVersionCache()
	endpoint := cacheEndpoint("http://pod1:5555")

	snapshot := commitTestObservation(t, cache, endpoint, 42, newTestConfig(), "raw-42", "content-42", "other-proof")

	assert.Zero(t, snapshot.version)
	assert.Nil(t, snapshot.parsedConfig)
	assert.Empty(t, snapshot.currentConfigChecksum)
	assert.Empty(t, snapshot.contentChecksum)
	assert.Equal(t, "other-proof", snapshot.activatedChecksum)
}

func TestConfigVersionCache_RuntimeMutationLeavesProofOnly(t *testing.T) {
	cache := newConfigVersionCache()
	endpoint := cacheEndpoint("http://pod1:5555")
	commitTestObservation(t, cache, endpoint, 2, newTestConfig(), "config-a", "content-a", "config-a")

	generation, ok := cache.beginRuntimeMutation(endpoint)
	require.True(t, ok)
	require.True(t, cache.finishRuntimeMutation(endpoint, generation, "config-b"))

	snapshot := cache.snapshot(endpoint)
	assert.Zero(t, snapshot.version, "version 2 may now name a different body")
	assert.Nil(t, snapshot.parsedConfig)
	assert.Empty(t, snapshot.currentConfigChecksum)
	assert.Empty(t, snapshot.contentChecksum)
	assert.Equal(t, "config-b", snapshot.activatedChecksum)
}

func TestConfigVersionCache_RuntimeMutationFencesOlderStructuralCommit(t *testing.T) {
	cache := newConfigVersionCache()
	endpoint := cacheEndpoint("http://pod1:5555")
	structural := cache.snapshot(endpoint)

	generation, ok := cache.beginRuntimeMutation(endpoint)
	require.True(t, ok)
	require.True(t, cache.finishRuntimeMutation(endpoint, generation, "runtime-proof"))

	assert.False(t, cache.commitSync(endpoint, structural.generation, 3, newTestConfig(), "stale", "stale", "stale"))
	current := cache.snapshot(endpoint)
	assert.Nil(t, current.parsedConfig)
	assert.Equal(t, "runtime-proof", current.activatedChecksum)
}

func TestConfigVersionCache_RuntimeFinishFencesStructuralSnapshotTakenDuringWrite(t *testing.T) {
	cache := newConfigVersionCache()
	endpoint := cacheEndpoint("http://pod1:5555")

	generation, ok := cache.beginRuntimeMutation(endpoint)
	require.True(t, ok)
	structural := cache.snapshot(endpoint)
	require.True(t, cache.commitSync(endpoint, structural.generation, 3, newTestConfig(), "structural", "content", "structural"))
	require.True(t, cache.finishRuntimeMutation(endpoint, generation, "runtime"))

	current := cache.snapshot(endpoint)
	assert.Zero(t, current.version)
	assert.Nil(t, current.parsedConfig)
	assert.Equal(t, "runtime", current.activatedChecksum)
	assert.False(t, cache.commitSync(endpoint, structural.generation, 3, newTestConfig(), "stale", "stale", "stale"))
}

func TestConfigVersionCache_AbortSyncClearsObservation(t *testing.T) {
	cache := newConfigVersionCache()
	endpoint := cacheEndpoint("http://pod1:5555")
	commitTestObservation(t, cache, endpoint, 4, newTestConfig(), "raw", "content", "raw")
	snapshot := cache.snapshot(endpoint)

	require.True(t, cache.abortSync(endpoint, snapshot.generation))

	assert.Nil(t, cache.snapshot(endpoint).parsedConfig)
}

func TestConfigVersionCache_AbortSyncDoesNotClearNewerRuntimeProof(t *testing.T) {
	cache := newConfigVersionCache()
	endpoint := cacheEndpoint("http://pod1:5555")
	structural := cache.snapshot(endpoint)
	generation, ok := cache.beginRuntimeMutation(endpoint)
	require.True(t, ok)
	require.True(t, cache.finishRuntimeMutation(endpoint, generation, "runtime-proof"))

	assert.False(t, cache.abortSync(endpoint, structural.generation))
	assert.Equal(t, "runtime-proof", cache.snapshot(endpoint).activatedChecksum)
}

func TestConfigVersionCache_AbortSyncDuringRuntimeWriteKeepsFinishOpen(t *testing.T) {
	cache := newConfigVersionCache()
	endpoint := cacheEndpoint("http://pod1:5555")
	generation, ok := cache.beginRuntimeMutation(endpoint)
	require.True(t, ok)
	structural := cache.snapshot(endpoint)

	require.True(t, cache.abortSync(endpoint, structural.generation))
	require.True(t, cache.finishRuntimeMutation(endpoint, generation, "runtime-proof"))
	assert.Equal(t, "runtime-proof", cache.snapshot(endpoint).activatedChecksum)
}

func TestConfigVersionCache_RetiredAuthorityCannotResurrect(t *testing.T) {
	cache := newConfigVersionCache()
	oldEndpoint := &dataplane.Endpoint{
		URL: "http://10.0.0.1:5555/v3", Username: "admin", Password: "secret",
		PodName: "haproxy-0", PodNamespace: "haptic", PodUID: "uid-old",
		DetectedMajorVersion: 3, DetectedMinorVersion: 2, DetectedFullVersion: "3.2.1",
	}
	stale := cache.snapshot(oldEndpoint)
	replacement := *oldEndpoint
	replacement.PodUID = "uid-new"
	cache.retain([]dataplane.Endpoint{replacement})

	assert.False(t, cache.commitSync(oldEndpoint, stale.generation, 7, newTestConfig(), "old", "old", "old"))
	assert.Zero(t, cache.snapshot(oldEndpoint).generation)
	assert.NotZero(t, cache.snapshot(&replacement).generation)
}

func TestConfigVersionCache_ClearFencesSnapshot(t *testing.T) {
	cache := newConfigVersionCache()
	endpoint := cacheEndpoint("http://pod1:5555")
	stale := cache.snapshot(endpoint)

	cache.clear()

	assert.False(t, cache.commitSync(endpoint, stale.generation, 7, newTestConfig(), "old", "old", "old"))
	assert.NotZero(t, cache.snapshot(endpoint).generation)
}

func TestConfigVersionCache_ConcurrentAccess(t *testing.T) {
	cache := newConfigVersionCache()
	endpoints := []dataplane.Endpoint{{URL: "http://pod1:5555"}, {URL: "http://pod2:5555"}}

	var wg sync.WaitGroup
	for i := range endpoints {
		endpoint := &endpoints[i]
		wg.Go(func() {
			for range 100 {
				snapshot := cache.snapshot(endpoint)
				cache.commitSync(endpoint, snapshot.generation, 2, newTestConfig(), "raw", "content", "raw")
			}
		})
		wg.Go(func() {
			for range 100 {
				generation, ok := cache.beginRuntimeMutation(endpoint)
				if ok {
					cache.finishRuntimeMutation(endpoint, generation, "runtime")
				}
			}
		})
	}
	wg.Go(func() {
		for range 50 {
			cache.abortSync(&endpoints[0], cache.snapshot(&endpoints[0]).generation)
		}
	})
	wg.Wait()
}

func TestSelectCachedParsedConfig(t *testing.T) {
	desired := newTestConfig()
	actual := newTestConfig()

	tests := []struct {
		name       string
		result     *dataplane.SyncResult
		desired    *parserconfig.StructuredConfig
		want       *parserconfig.StructuredConfig
		wantShared bool
	}{
		{
			name: "comparator proof shares desired graph",
			result: &dataplane.SyncResult{
				PostSyncParsedConfig:         actual,
				PostSyncConfigMatchesDesired: true,
			},
			desired:    desired,
			want:       desired,
			wantShared: true,
		},
		{
			name: "runtime divergence retains actual graph",
			result: &dataplane.SyncResult{
				PostSyncParsedConfig: actual,
			},
			desired: desired,
			want:    actual,
		},
		{
			name: "proof without desired graph retains actual graph",
			result: &dataplane.SyncResult{
				PostSyncParsedConfig:         actual,
				PostSyncConfigMatchesDesired: true,
			},
			want: actual,
		},
		{
			name:    "missing actual graph falls back to desired",
			result:  &dataplane.SyncResult{},
			desired: desired,
			want:    desired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, shared := selectCachedParsedConfig(tt.result, tt.desired)
			assert.Same(t, tt.want, got)
			assert.Equal(t, tt.wantShared, shared)
		})
	}
}
