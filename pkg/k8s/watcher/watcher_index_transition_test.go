package watcher

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

func TestWatcherMovesUpdatedResourceBetweenIndexBuckets(t *testing.T) {
	for name, w := range indexTransitionWatchers(t) {
		t.Run(name, func(t *testing.T) {
			target := endpointSlice("target", "A")
			sibling := endpointSlice("sibling", "A")
			w.processAdd(target)
			w.processAdd(sibling)

			updated := endpointSlice("target", "B")
			w.handleUpdate(target, updated)

			oldBucket, err := w.store.Get("default", "A")
			require.NoError(t, err)
			require.Len(t, oldBucket, 1)
			require.Equal(t, "sibling", watchedResourceName(t, oldBucket[0]))

			newBucket, err := w.store.Get("default", "B")
			require.NoError(t, err)
			require.Len(t, newBucket, 1)
			require.Equal(t, "target", watchedResourceName(t, newBucket[0]))
		})
	}
}

func TestWatcherRemovesOldResourceWhenUpdatedIndexIsMissing(t *testing.T) {
	for name, w := range indexTransitionWatchers(t) {
		t.Run(name, func(t *testing.T) {
			oldResource := endpointSlice("target", "A")
			w.processAdd(oldResource)

			updated := endpointSlice("target", "A")
			metadata := updated.Object["metadata"].(map[string]any)
			delete(metadata, "labels")
			w.handleUpdate(oldResource, updated)

			oldBucket, err := w.store.Get("default", "A")
			require.NoError(t, err)
			require.Empty(t, oldBucket)
			stats := watcherChangeStats(w)
			require.Equal(t, 1, stats.Created)
			require.Equal(t, 1, stats.Deleted)
			require.Zero(t, stats.Modified)
		})
	}
}

func TestWatcherAddsResourceWhenUpdatedIndexBecomesComplete(t *testing.T) {
	for name, w := range indexTransitionWatchers(t) {
		t.Run(name, func(t *testing.T) {
			oldResource := endpointSlice("target", "A")
			metadata := oldResource.Object["metadata"].(map[string]any)
			delete(metadata, "labels")
			w.handleAdd(oldResource)

			updated := endpointSlice("target", "B")
			w.handleUpdate(oldResource, updated)

			newBucket, err := w.store.Get("default", "B")
			require.NoError(t, err)
			require.Len(t, newBucket, 1)
			require.Equal(t, "target", watchedResourceName(t, newBucket[0]))
			stats := watcherChangeStats(w)
			require.Equal(t, 1, stats.Created)
			require.Zero(t, stats.Modified)
			require.Zero(t, stats.Deleted)
		})
	}
}

func TestWatcherIgnoresUpdateWhenBothIndexesAreIncomplete(t *testing.T) {
	for name, w := range indexTransitionWatchers(t) {
		t.Run(name, func(t *testing.T) {
			oldResource := endpointSlice("target", "A")
			oldMetadata := oldResource.Object["metadata"].(map[string]any)
			delete(oldMetadata, "labels")
			updated := endpointSlice("target", "B")
			updatedMetadata := updated.Object["metadata"].(map[string]any)
			delete(updatedMetadata, "labels")

			w.handleUpdate(oldResource, updated)

			resources, err := w.store.List()
			require.NoError(t, err)
			require.Empty(t, resources)
			require.True(t, watcherChangeStats(w).IsEmpty())
		})
	}
}

func TestWatcherDeleteAndRecreateUsesOnlyNewIndex(t *testing.T) {
	for name, w := range indexTransitionWatchers(t) {
		t.Run(name, func(t *testing.T) {
			oldResource := endpointSlice("target", "A")
			w.handleAdd(oldResource)
			w.handleDelete(oldResource)

			recreated := endpointSlice("target", "B")
			w.handleAdd(recreated)

			oldBucket, err := w.store.Get("default", "A")
			require.NoError(t, err)
			require.Empty(t, oldBucket)

			newBucket, err := w.store.Get("default", "B")
			require.NoError(t, err)
			require.Len(t, newBucket, 1)
			require.Equal(t, "target", watchedResourceName(t, newBucket[0]))
		})
	}
}

func indexTransitionWatchers(t *testing.T) map[string]*Watcher {
	t.Helper()
	indexBy := []string{"metadata.namespace", `metadata.labels.kubernetes\.io/service-name`}
	idx, err := indexer.New(indexer.Config{IndexBy: indexBy})
	require.NoError(t, err)

	dynamicClient := fake.NewSimpleDynamicClient(runtime.NewScheme())
	cachedStore, err := store.NewCachedStore(&store.CachedStoreConfig{
		NumKeys:  len(indexBy),
		CacheTTL: time.Minute,
		Client:   dynamicClient,
		GVR: schema.GroupVersionResource{
			Group:    "discovery.k8s.io",
			Version:  "v1",
			Resource: "endpointslices",
		},
		Indexer: idx,
	})
	require.NoError(t, err)

	stores := map[string]types.Store{
		"memory": store.NewMemoryStore(len(indexBy)),
		"cached": cachedStore,
	}
	watchers := make(map[string]*Watcher, len(stores))
	for name, resourceStore := range stores {
		debouncer := NewDebouncer(time.Hour, func(types.Store, types.ChangeStats) {}, resourceStore, true)
		t.Cleanup(debouncer.Stop)
		watchers[name] = &Watcher{
			logger:    slog.Default(),
			indexer:   idx,
			store:     resourceStore,
			debouncer: debouncer,
			config:    types.WatcherConfig{IndexBy: indexBy},
		}
	}
	return watchers
}

func watchedResourceName(t *testing.T, resource any) string {
	t.Helper()
	object, ok := resource.(map[string]any)
	require.True(t, ok)
	unstructuredResource := &unstructured.Unstructured{Object: object}
	return unstructuredResource.GetName()
}

func watcherChangeStats(w *Watcher) types.ChangeStats {
	w.debouncer.mu.Lock()
	defer w.debouncer.mu.Unlock()
	return w.debouncer.stats
}
