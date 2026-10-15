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

func BenchmarkSnapshotBranchChurn(b *testing.B) {
	for _, backend := range []string{"memory", "cached"} {
		for _, size := range []int{100, 10000} {
			b.Run(fmt.Sprintf("%s/%d", backend, size), func(b *testing.B) {
				benchmarkSnapshotBranchChurn(b, backend, size)
			})
		}
	}
}

// Each iteration is one reconcile: one resource changed, captured, and accepted.
func benchmarkSnapshotBranchChurn(b *testing.B, backend string, size int) {
	b.Helper()
	indexBy := []string{"metadata.namespace", "metadata.name"}
	live := newBenchmarkBranchStore(b, backend, indexBy)
	for index := range size {
		name := fmt.Sprintf("item-%05d", index)
		require.NoError(b, live.Add(cachedSnapshotResource("default", name, "1", "before"), []string{"default", name}))
	}
	accepted := NewSnapshotBranch(2)
	revision := 1
	b.ResetTimer()
	for b.Loop() {
		revision++
		name := fmt.Sprintf("item-%05d", revision%size)
		require.NoError(b, live.Update(cachedSnapshotResource("default", name, fmt.Sprint(revision), "after"), []string{"default", name}))
		snapshot, err := live.Pin()
		require.NoError(b, err)
		changes, err := accepted.Changes(b.Context(), snapshot, indexBy)
		if err != nil || len(changes) == 0 {
			b.Fatalf("capture returned %d changes: %v", len(changes), err)
		}
		accepted = accepted.Apply(changes)
	}
}

type benchmarkBranchStore interface {
	stores.SnapshotProvider
	Add(any, []string) error
	Update(any, []string) error
}

func newBenchmarkBranchStore(b *testing.B, backend string, indexBy []string) benchmarkBranchStore {
	b.Helper()
	if backend == "memory" {
		return NewMemoryStore(2)
	}
	idx, err := indexer.New(indexer.Config{IndexBy: indexBy})
	require.NoError(b, err)
	live, err := NewCachedStore(&CachedStoreConfig{
		NumKeys: 2, CacheTTL: time.Minute, Projected: true, Indexer: idx,
		Client: fake.NewSimpleDynamicClient(runtime.NewScheme()),
		GVR:    schema.GroupVersionResource{Group: "example.test", Version: "v1", Resource: "widgets"},
	})
	require.NoError(b, err)
	return live
}

func benchmarkSnapshotBranchChanges(b *testing.B, backend string, size int) {
	b.Helper()
	indexBy := []string{"metadata.namespace", "metadata.name"}
	live := newBenchmarkBranchStore(b, backend, indexBy)
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
