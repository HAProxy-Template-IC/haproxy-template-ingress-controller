package store

import (
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

var (
	benchmarkCachedSnapshot stores.ReadSnapshot
	benchmarkCachedSequence uint64
)

func BenchmarkCachedStorePinStartup(b *testing.B) {
	for _, count := range []int{300, 1000, 3000} {
		b.Run(fmt.Sprintf("objects=%d/no-change", count), func(b *testing.B) {
			store := newCachedPinBenchmarkStore(b, count)
			benchmarkCachedStorePins(b, store, count)
		})
		b.Run(fmt.Sprintf("objects=%d/one-change", count), func(b *testing.B) {
			store := newCachedPinBenchmarkStore(b, count)
			changed := cachedSnapshotResource("default", "item-0000", "changed", "changed")
			if err := store.Update(changed, []string{"default", "item-0000"}); err != nil {
				b.Fatal(err)
			}
			benchmarkCachedStorePins(b, store, count)
		})
	}
}

func benchmarkCachedStorePins(b *testing.B, store *CachedStore, count int) {
	b.Helper()
	b.ReportAllocs()
	b.ReportMetric(float64(count), "warm_objects")
	b.ResetTimer()
	for b.Loop() {
		snapshot, err := store.Pin()
		if err != nil {
			b.Fatal(err)
		}
		benchmarkCachedSnapshot = snapshot
		benchmarkCachedSequence = snapshot.Sequence()
	}
}

func newCachedPinBenchmarkStore(b *testing.B, count int) *CachedStore {
	b.Helper()
	client := fake.NewSimpleDynamicClient(runtime.NewScheme())
	idx, err := indexer.New(indexer.Config{IndexBy: []string{"metadata.namespace", "metadata.name"}})
	if err != nil {
		b.Fatal(err)
	}
	store, err := NewCachedStore(&CachedStoreConfig{
		NumKeys:      2,
		CacheTTL:     time.Hour,
		MaxCacheSize: count,
		Client:       client,
		GVR:          configMapGVR,
		Indexer:      idx,
	})
	if err != nil {
		b.Fatal(err)
	}
	for index := range count {
		name := fmt.Sprintf("item-%04d", index)
		resource := cachedSnapshotResource("default", name, fmt.Sprintf("%d", index+1), "value")
		if err := store.Add(resource, []string{"default", name}); err != nil {
			b.Fatal(err)
		}
	}
	if warm := store.readRoot.Load().warm.Len(); warm != count {
		b.Fatalf("warm root has %d entries, want %d", warm, count)
	}
	return store
}
