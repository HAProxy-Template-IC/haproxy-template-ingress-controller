package store

import (
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/cache"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
)

func TestImmutableResourceSharesOwnedGraphWithStoreAndPinnedSnapshots(t *testing.T) {
	first := namedResource("default", "target")
	first["spec"] = map[string]any{"values": []any{"original"}}
	owned, err := NewImmutableResource(first)
	require.NoError(t, err)
	resourceStore := NewMemoryStore(2)
	keys := []string{"default", "target"}
	require.NoError(t, resourceStore.Add(owned, keys))
	pinned, err := resourceStore.Pin()
	require.NoError(t, err)
	projection, supported, err := ProjectImmutableSnapshotList(t.Context(), pinned)
	require.NoError(t, err)
	require.True(t, supported)
	require.Len(t, projection.items, 1)
	assert.Equal(t, reflect.ValueOf(owned.resource().Object).Pointer(), reflect.ValueOf(projection.items[0]).Pointer())
	assert.NotEqual(t, reflect.ValueOf(first).Pointer(), reflect.ValueOf(projection.items[0]).Pointer())

	first["spec"].(map[string]any)["values"].([]any)[0] = "caller mutation"
	public, err := resourceStore.Get(keys...)
	require.NoError(t, err)
	public[0].(map[string]any)["spec"].(map[string]any)["values"].([]any)[0] = "read mutation"
	metadata, err := meta.Accessor(owned)
	require.NoError(t, err)
	metadata.SetName("metadata mutation")
	assert.Equal(t, "target", owned.GetName())
	key, err := cache.MetaNamespaceKeyFunc(owned)
	require.NoError(t, err)
	assert.Equal(t, "default/target", key)

	second, err := NewImmutableResource(first)
	require.NoError(t, err)
	require.NoError(t, resourceStore.Update(second, keys))
	require.NoError(t, resourceStore.Delete("default", "target", keys))
	old, err := pinned.List()
	require.NoError(t, err)
	assert.Equal(t, "original", old[0].(map[string]any)["spec"].(map[string]any)["values"].([]any)[0])
	current, err := resourceStore.List()
	require.NoError(t, err)
	assert.Empty(t, current)
}

func TestImmutableResourceVersionSharingAllowsConcurrentReads(t *testing.T) {
	source := namedResource("default", "target")
	source["metadata"].(map[string]any)["resourceVersion"] = "1"
	resourceStore := NewMemoryStore(1)
	require.NoError(t, resourceStore.Add(source, []string{"target"}))
	source["metadata"].(map[string]any)["resourceVersion"] = "2"
	owned, err := NewImmutableResource(source)
	require.NoError(t, err)
	var readers sync.WaitGroup
	readers.Go(func() {
		for range 1000 {
			assert.Equal(t, "2", owned.GetResourceVersion())
			assert.Equal(t, "target", owned.GetName())
			assert.Equal(t, "2", owned.GetObjectMeta().GetResourceVersion())
		}
	})
	for range 1000 {
		require.NoError(t, resourceStore.Update(owned, []string{"target"}))
	}
	readers.Wait()
}

func TestImmutableResourceEvaluatesConfiguredFields(t *testing.T) {
	source := namedResource("default", "target")
	source["spec"] = map[string]any{"group": "custom", "enabled": true}
	owned, err := NewImmutableResource(source)
	require.NoError(t, err)
	idx, err := indexer.New(indexer.Config{IndexBy: []string{"spec.group"}})
	require.NoError(t, err)
	keys, err := owned.ExtractKeys(idx)
	require.NoError(t, err)
	assert.Equal(t, []string{"custom"}, keys)
	matcher, err := indexer.NewFieldSelectorMatcher("spec.enabled=true")
	require.NoError(t, err)
	matches, err := owned.Matches(matcher)
	require.NoError(t, err)
	assert.True(t, matches)
}

func TestImmutableResourceRejectsInvalidGraphs(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	for name, source := range map[string]map[string]any{
		"nil":             nil,
		"cycle":           cycle,
		"native callback": {"callback": func() {}},
		"foreign map":     {"foreign": map[string]string{"key": "value"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewImmutableResource(source)
			require.Error(t, err)
		})
	}
	for _, invalid := range []*ImmutableResource{nil, {}} {
		require.Error(t, NewMemoryStore(1).Add(invalid, []string{"target"}))
	}
}

func TestImmutableResourceSharesVersionOnlyUpdatesWithoutChangingRevisions(t *testing.T) {
	for _, operation := range []string{"add", "update"} {
		t.Run(operation, func(t *testing.T) {
			source := namedResource("default", "target")
			source["metadata"].(map[string]any)["resourceVersion"] = "1"
			source["spec"] = map[string]any{"entries": []any{"payload"}}
			first, err := NewImmutableResource(source)
			require.NoError(t, err)
			resourceStore := NewMemoryStore(1)
			require.NoError(t, resourceStore.Add(first, []string{"target"}))
			revision := resourceStore.ListRevision()
			pinned, err := resourceStore.Pin()
			require.NoError(t, err)

			source["metadata"].(map[string]any)["resourceVersion"] = "2"
			second, err := NewImmutableResource(source)
			require.NoError(t, err)
			if operation == "add" {
				err = resourceStore.Add(second, []string{"target"})
			} else {
				err = resourceStore.Update(second, []string{"target"})
			}
			require.NoError(t, err)
			assert.Equal(t, revision, resourceStore.ListRevision())
			assert.Equal(t, source, second.resource().Object)
			assert.Equal(t, "2", second.GetResourceVersion())
			assert.Equal(t, "2", second.GetObjectMeta().GetResourceVersion())
			assert.Equal(t, "1", first.GetResourceVersion())
			assert.Equal(t,
				reflect.ValueOf(first.resource().Object["spec"]).Pointer(),
				reflect.ValueOf(second.resource().Object["spec"]).Pointer())
			items, err := pinned.List()
			require.NoError(t, err)
			assert.Equal(t, "1", items[0].(map[string]any)["metadata"].(map[string]any)["resourceVersion"])
			idx, err := indexer.New(indexer.Config{IndexBy: []string{"metadata.resourceVersion"}})
			require.NoError(t, err)
			keys, err := second.ExtractKeys(idx)
			require.NoError(t, err)
			assert.Equal(t, []string{"2"}, keys)
		})
	}
}
