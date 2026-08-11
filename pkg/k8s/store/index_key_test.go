package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
)

type keyedStore interface {
	Add(resource any, keys []string) error
	Get(keys ...string) ([]any, error)
}

type indexFixture struct {
	name string
	keys []string
}

var ambiguousIndexFixtures = []indexFixture{
	{name: "slash-first", keys: []string{"a/b", "c"}},
	{name: "slash-second", keys: []string{"a", "b/c"}},
	{name: "empty", keys: []string{"", "tail"}},
	{name: "unicode-slash-first", keys: []string{"領域/一", "雪"}},
	{name: "unicode-slash-second", keys: []string{"領域", "一/雪"}},
}

func TestMemoryStore_IndexComponentsAreUnambiguous(t *testing.T) {
	assertIndexComponentsAreUnambiguous(t, NewMemoryStore(2))
}

func TestCachedStore_IndexComponentsAreUnambiguous(t *testing.T) {
	client := fake.NewSimpleDynamicClient(runtime.NewScheme())
	store := newTestCachedStore(t, client, createTestIndexer(), 2, 5*time.Minute)
	assertIndexComponentsAreUnambiguous(t, store)
}

func assertIndexComponentsAreUnambiguous(t *testing.T, store keyedStore) {
	t.Helper()

	for _, fixture := range ambiguousIndexFixtures {
		require.NoError(t, store.Add(namedResource("test", fixture.name), fixture.keys))
	}

	for _, fixture := range ambiguousIndexFixtures {
		assertStoreResourceNames(t, store, fixture.keys, fixture.name)
	}

	assertStoreResourceNames(t, store, []string{"a"}, "slash-second")
	assertStoreResourceNames(t, store, []string{"a/b"}, "slash-first")
	assertStoreResourceNames(t, store, []string{""}, "empty")
	assertStoreResourceNames(t, store, []string{"領域"}, "unicode-slash-second")
	assertStoreResourceNames(t, store, []string{"領域/一"}, "unicode-slash-first")
}

func assertStoreResourceNames(t *testing.T, store keyedStore, keys []string, want ...string) {
	t.Helper()

	resources, err := store.Get(keys...)
	require.NoError(t, err)
	names := make([]string, len(resources))
	for i, resource := range resources {
		_, names[i] = extractNamespaceName(resource)
	}
	assert.Equal(t, want, names)
}

func TestCachedStore_ResourceCacheIdentityIsUnambiguous(t *testing.T) {
	client := fake.NewSimpleDynamicClient(runtime.NewScheme())
	store := newTestCachedStore(t, client, createTestIndexer(), 2, 5*time.Minute)

	// Synthetic metadata isolates cache-key behavior from Kubernetes validation.
	first := createTestResource("a/b", "c")
	second := createTestResource("a", "b/c")
	require.NoError(t, store.Add(first, []string{"first", "resource"}))
	require.NoError(t, store.Add(second, []string{"second", "resource"}))

	resources, err := store.Get("first", "resource")
	require.NoError(t, err)
	require.Len(t, resources, 1)
	got := resources[0].(*unstructured.Unstructured)
	assert.Equal(t, "a/b", got.GetNamespace())
	assert.Equal(t, "c", got.GetName())
}
