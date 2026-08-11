package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
)

type indexTransitionStore interface {
	Add(resource any, keys []string) error
	Update(resource any, keys []string) error
	Delete(namespace, name string, keys []string) error
	Get(keys ...string) ([]any, error)
	Size() int
}

func indexTransitionResource(name, indexValue, revision string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"namespace": "default",
			"name":      name,
		},
		"spec": map[string]any{
			"index":    indexValue,
			"revision": revision,
		},
	}
}

func indexTransitionStores(t *testing.T) map[string]indexTransitionStore {
	t.Helper()

	dynamicClient := fake.NewSimpleDynamicClient(runtime.NewScheme())
	idx, err := indexer.New(indexer.Config{IndexBy: []string{"spec.index"}})
	require.NoError(t, err)
	cached, err := NewCachedStore(&CachedStoreConfig{
		NumKeys:  1,
		CacheTTL: time.Minute,
		Client:   dynamicClient,
		GVR:      configMapGVR,
		Indexer:  idx,
	})
	require.NoError(t, err)

	return map[string]indexTransitionStore{
		"memory": NewMemoryStore(1),
		"cached": cached,
	}
}

func TestStoresMoveResourceBetweenIndexBuckets(t *testing.T) {
	for name, resourceStore := range indexTransitionStores(t) {
		t.Run(name, func(t *testing.T) {
			target := indexTransitionResource("target", "A", "old")
			sibling := indexTransitionResource("sibling", "A", "stable")
			require.NoError(t, resourceStore.Add(target, []string{"A"}))
			require.NoError(t, resourceStore.Add(sibling, []string{"A"}))

			updated := indexTransitionResource("target", "B", "new")
			require.NoError(t, resourceStore.Update(updated, []string{"B"}))

			oldBucket, err := resourceStore.Get("A")
			require.NoError(t, err)
			require.Len(t, oldBucket, 1)
			require.Equal(t, "sibling", resourceName(t, oldBucket[0]))

			newBucket, err := resourceStore.Get("B")
			require.NoError(t, err)
			require.Len(t, newBucket, 1)
			require.Equal(t, "target", resourceName(t, newBucket[0]))
			require.Equal(t, "new", resourceRevision(t, newBucket[0]))
			require.Equal(t, 2, resourceStore.Size())
		})
	}
}

func TestStoresDeleteAndRecreateResourceWithoutStaleIndex(t *testing.T) {
	for name, resourceStore := range indexTransitionStores(t) {
		t.Run(name, func(t *testing.T) {
			oldResource := indexTransitionResource("target", "A", "old")
			require.NoError(t, resourceStore.Add(oldResource, []string{"A"}))
			require.NoError(t, resourceStore.Delete("default", "target", []string{"A"}))

			recreated := indexTransitionResource("target", "B", "recreated")
			require.NoError(t, resourceStore.Add(recreated, []string{"B"}))

			oldBucket, err := resourceStore.Get("A")
			require.NoError(t, err)
			require.Empty(t, oldBucket)

			newBucket, err := resourceStore.Get("B")
			require.NoError(t, err)
			require.Len(t, newBucket, 1)
			require.Equal(t, "recreated", resourceRevision(t, newBucket[0]))
			require.Equal(t, 1, resourceStore.Size())
		})
	}
}

func TestStoresRejectDeleteWithoutResourceName(t *testing.T) {
	for name, resourceStore := range indexTransitionStores(t) {
		t.Run(name, func(t *testing.T) {
			resource := indexTransitionResource("target", "A", "old")
			require.NoError(t, resourceStore.Add(resource, []string{"A"}))

			err := resourceStore.Delete("default", "", []string{"A"})
			require.ErrorIs(t, err, errResourceNameRequired)

			bucket, getErr := resourceStore.Get("A")
			require.NoError(t, getErr)
			require.Len(t, bucket, 1)
			require.Equal(t, "target", resourceName(t, bucket[0]))
		})
	}
}

func resourceName(t *testing.T, resource any) string {
	t.Helper()
	namespace, name := extractNamespaceName(resource)
	require.Equal(t, "default", namespace)
	return name
}

func resourceRevision(t *testing.T, resource any) string {
	t.Helper()
	object, ok := resource.(map[string]any)
	if !ok {
		unstructuredResource, isUnstructured := resource.(*unstructured.Unstructured)
		require.True(t, isUnstructured)
		object = unstructuredResource.Object
	}
	spec, ok := object["spec"].(map[string]any)
	require.True(t, ok)
	revision, ok := spec["revision"].(string)
	require.True(t, ok)
	return revision
}
