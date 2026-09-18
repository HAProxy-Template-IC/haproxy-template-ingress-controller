package watcher

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
)

func BenchmarkWatcherResourceOwnership(b *testing.B) {
	for _, updated := range []bool{false, true} {
		for _, shared := range []bool{false, true} {
			b.Run(fmt.Sprintf("updated=%t/shared=%t", updated, shared), func(b *testing.B) {
				payload := resourceOwnershipBenchmarkJSON(b)
				var retained uint64
				for range b.N {
					b.StopTimer()
					runtime.GC()
					var before runtime.MemStats
					runtime.ReadMemStats(&before)
					b.StartTimer()
					informer, resourceStore, transform := populateResourceOwnershipBenchmark(b, payload, shared, updated)
					b.StopTimer()
					runtime.GC()
					var after runtime.MemStats
					runtime.ReadMemStats(&after)
					retained += after.HeapAlloc - before.HeapAlloc
					runtime.KeepAlive(informer)
					runtime.KeepAlive(resourceStore)
					runtime.KeepAlive(transform)
				}
				b.ReportMetric(float64(retained)/float64(b.N), "retained-B")
			})
		}
	}
}

func populateResourceOwnershipBenchmark(b *testing.B, payload []byte, shared, updated bool) (cache.Store, *store.MemoryStore, cache.TransformFunc) {
	b.Helper()
	idx, err := indexer.New(indexer.Config{IndexBy: []string{"metadata.namespace", "metadata.name"}})
	require.NoError(b, err)
	resourceStore := store.NewMemoryStore(2)
	w := &Watcher{
		indexer: idx, store: resourceStore,
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		debouncer: &Debouncer{stopped: true},
	}
	informer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	transform := newNormalizeTransform(idx)
	for index := range 5000 {
		resource := &unstructured.Unstructured{}
		require.NoError(b, json.Unmarshal(payload, &resource.Object))
		resource.SetName(fmt.Sprintf("item-%05d", index))
		var cached any = resource
		if shared {
			cached, err = transform(resource)
			require.NoError(b, err)
		} else {
			normalizeInPlace(resource, idx)
		}
		require.NoError(b, informer.Add(cached))
		w.handleAdd(cached)
	}
	if updated {
		for index := range 5000 {
			resource := &unstructured.Unstructured{}
			require.NoError(b, json.Unmarshal(payload, &resource.Object))
			resource.SetName(fmt.Sprintf("item-%05d", index))
			resource.SetResourceVersion("2")
			old, found, getErr := informer.Get(resource)
			require.NoError(b, getErr)
			require.True(b, found)
			var cached any = resource
			if shared {
				cached, err = transform(resource)
				require.NoError(b, err)
			} else {
				normalizeInPlace(resource, idx)
			}
			require.NoError(b, informer.Update(cached))
			w.handleUpdate(old, cached)
		}
	}
	require.Equal(b, 5000, resourceStore.Size())
	require.Len(b, informer.List(), 5000)
	return informer, resourceStore, transform
}

func resourceOwnershipBenchmarkJSON(b *testing.B) []byte {
	b.Helper()
	entries := make([]any, 20)
	for index := range entries {
		entries[index] = map[string]any{
			"name":    fmt.Sprintf("entry-%d", index),
			"match":   map[string]any{"path": fmt.Sprintf("/path/%d", index), "method": "GET"},
			"targets": []any{map[string]any{"name": "upstream", "port": 8080, "weight": 100}},
		}
	}
	encoded, err := json.Marshal(map[string]any{
		"apiVersion": "example.test/v1", "kind": "Record",
		"metadata": map[string]any{"namespace": "default", "name": "item", "resourceVersion": "1"},
		"spec":     map[string]any{"entries": entries},
	})
	require.NoError(b, err)
	return encoded
}
