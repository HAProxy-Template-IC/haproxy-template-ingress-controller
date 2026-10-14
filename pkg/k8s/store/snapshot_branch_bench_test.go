package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func BenchmarkSnapshotBranchChanges(b *testing.B) {
	for _, backend := range []string{"memory", "cached"} {
		for _, size := range []int{100, 10000} {
			b.Run(fmt.Sprintf("%s/%d", backend, size), func(b *testing.B) {
				benchmarkSnapshotBranchChanges(b, backend, size)
			})
		}
	}
}

func benchmarkSnapshotBranchChanges(b *testing.B, backend string, size int) {
	b.Helper()
	indexBy := []string{"metadata.namespace", "metadata.name"}
	var live interface {
		stores.SnapshotProvider
		Add(any, []string) error
		Update(any, []string) error
	} = NewMemoryStore(2)
	if backend == "cached" {
		idx, err := indexer.New(indexer.Config{IndexBy: indexBy})
		require.NoError(b, err)
		live, err = NewCachedStore(&CachedStoreConfig{
			NumKeys: 2, CacheTTL: time.Minute, Projected: true, Indexer: idx,
			Client: fake.NewSimpleDynamicClient(runtime.NewScheme()),
			GVR:    schema.GroupVersionResource{Group: "example.test", Version: "v1", Resource: "widgets"},
		})
		require.NoError(b, err)
	}
	for index := range size {
		name := fmt.Sprintf("item-%05d", index)
		value := cachedSnapshotResource("default", name, "1", "before")
		require.NoError(b, live.Add(value, []string{"default", name}))
	}
	base := NewSnapshotBranch(2)
	snapshot, err := live.Pin()
	require.NoError(b, err)
	changes, err := base.Changes(b.Context(), snapshot, indexBy)
	require.NoError(b, err)
	base = base.Apply(changes)
	require.NoError(b, live.Update(cachedSnapshotResource("default", "item-00000", "2", "after"), []string{"default", "item-00000"}))
	snapshot, err = live.Pin()
	require.NoError(b, err)
	b.ResetTimer()
	for b.Loop() {
		changes, err := base.Changes(b.Context(), snapshot, indexBy)
		if err != nil || len(changes) != 1 {
			b.Fatalf("capture returned %d changes: %v", len(changes), err)
		}
	}
}
