package store

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestMemoryStorePinKeepsOneRootAcrossMutations(t *testing.T) {
	resourceStore := NewMemoryStore(2)
	first := namedResource("default", "first")
	first["value"] = "before"
	require.NoError(t, resourceStore.Add(first, []string{"blue", "first"}))

	pinned, err := resourceStore.Pin()
	require.NoError(t, err)
	first["value"] = "caller poison"
	pinnedListRevision := pinned.ListRevision()
	pinnedBlueRevision := pinned.GetRevision("blue")
	pinnedRedRevision := pinned.GetRevision("red")
	pinnedIdentityRevision := pinned.IdentityRevision("default", "first")

	updated := namedResource("default", "first")
	updated["value"] = "after"
	require.NoError(t, resourceStore.Update(updated, []string{"red", "first"}))
	require.NoError(t, resourceStore.Add(namedResource("default", "second"), []string{"blue", "second"}))

	items, err := pinned.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "before", items[0].(map[string]any)["value"])
	items, err = pinned.Get("blue")
	require.NoError(t, err)
	require.Len(t, items, 1)
	items[0].(map[string]any)["value"] = "poison"
	items, err = pinned.Get("blue")
	require.NoError(t, err)
	require.Equal(t, "before", items[0].(map[string]any)["value"])
	items, err = pinned.Get("red")
	require.NoError(t, err)
	require.Empty(t, items)
	item, found, err := pinned.GetIdentity("default", "first")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "before", item.(map[string]any)["value"])
	require.Equal(t, pinnedListRevision, pinned.ListRevision())
	require.Equal(t, pinnedBlueRevision, pinned.GetRevision("blue"))
	require.Equal(t, pinnedRedRevision, pinned.GetRevision("red"))
	require.Equal(t, pinnedIdentityRevision, pinned.IdentityRevision("default", "first"))

	current, err := resourceStore.Pin()
	require.NoError(t, err)
	require.NotEqual(t, pinned.Sequence(), current.Sequence())
	require.NotEqual(t, pinned.ListRevision(), current.ListRevision())
	require.NotEqual(t, pinned.GetRevision("blue"), current.GetRevision("blue"))
	require.NotEqual(t, pinned.GetRevision("red"), current.GetRevision("red"))
	require.NotEqual(t,
		pinned.IdentityRevision("default", "first"),
		current.IdentityRevision("default", "first"),
	)
}

func TestMemoryStorePublicReadsCannotMutateImmutableSnapshot(t *testing.T) {
	resourceStore := NewMemoryStore(2)
	require.NoError(t, resourceStore.Add(
		map[string]any{
			"metadata": map[string]any{"namespace": "default", "name": "target"},
			"spec":     map[string]any{"value": "original"},
		},
		[]string{"default", "target"},
	))
	snapshot, err := resourceStore.Pin()
	require.NoError(t, err)
	immutable := snapshot.(*memoryReadSnapshot)

	get, err := resourceStore.Get("default", "target")
	require.NoError(t, err)
	get[0].(map[string]any)["spec"].(map[string]any)["value"] = "get poison"
	listed, err := resourceStore.List()
	require.NoError(t, err)
	listed[0].(map[string]any)["spec"].(map[string]any)["value"] = "list poison"
	identity, found, err := resourceStore.GetIdentity("default", "target")
	require.NoError(t, err)
	require.True(t, found)
	identity.(map[string]any)["spec"].(map[string]any)["value"] = "identity poison"

	owned, err := immutable.getImmutable(t.Context(), "default", "target")
	require.NoError(t, err)
	require.Equal(t, "original", owned[0].(map[string]any)["spec"].(map[string]any)["value"])
	again, err := immutable.getImmutable(t.Context(), "default", "target")
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(owned[0]).Pointer(), reflect.ValueOf(again[0]).Pointer())
}

func TestMemoryStorePinPreservesDeletedAndClearedRoots(t *testing.T) {
	resourceStore := NewMemoryStore(1)
	require.NoError(t, resourceStore.Add(namedResource("default", "first"), []string{"shared"}))
	require.NoError(t, resourceStore.Add(namedResource("default", "second"), []string{"shared"}))
	pinned, err := resourceStore.Pin()
	require.NoError(t, err)

	require.NoError(t, resourceStore.Delete("default", "first", []string{"shared"}))
	require.NoError(t, resourceStore.Clear())

	items, err := pinned.Get("shared")
	require.NoError(t, err)
	require.Len(t, items, 2)
	_, found, err := pinned.GetIdentity("default", "first")
	require.NoError(t, err)
	require.True(t, found)

	current, err := resourceStore.Pin()
	require.NoError(t, err)
	items, err = current.List()
	require.NoError(t, err)
	require.Empty(t, items)
	require.NotEqual(t, pinned.GetRevision("shared"), current.GetRevision("shared"))
}

func TestMemoryStorePinKeepsUnrelatedExactRevisions(t *testing.T) {
	resourceStore := NewMemoryStore(2)
	before, err := resourceStore.Pin()
	require.NoError(t, err)
	missingRevision := before.GetRevision("blue", "missing")
	missingIdentityRevision := before.IdentityRevision("default", "missing")

	require.NoError(t, resourceStore.Add(namedResource("other", "present"), []string{"red", "present"}))
	after, err := resourceStore.Pin()
	require.NoError(t, err)
	require.Equal(t, missingRevision, after.GetRevision("blue", "missing"))
	require.Equal(t, missingIdentityRevision, after.IdentityRevision("default", "missing"))
	require.NotEqual(t, before.ListRevision(), after.ListRevision())
}

func TestMemoryStorePinFailsClosedForUnidentifiedResources(t *testing.T) {
	resourceStore := NewMemoryStore(1)
	require.NoError(t, resourceStore.Add(map[string]any{"value": "unidentified"}, []string{"key"}))
	_, err := resourceStore.Pin()
	require.ErrorIs(t, err, stores.ErrSnapshotUnsupported)
}

func TestMemoryStorePinnedRootSupportsConcurrentLiveMutations(t *testing.T) {
	resourceStore := NewMemoryStore(1)
	for index := range 64 {
		resource := namedResource("default", fmt.Sprintf("item-%02d", index))
		resource["generation"] = int64(0)
		require.NoError(t, resourceStore.Add(resource, []string{"all"}))
	}
	pinned, err := resourceStore.Pin()
	require.NoError(t, err)

	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		for index := range 64 {
			resource := namedResource("default", fmt.Sprintf("item-%02d", index))
			resource["generation"] = int64(1)
			require.NoError(t, resourceStore.Update(resource, []string{"all"}))
		}
	}()
	for range 64 {
		items, readErr := pinned.Get("all")
		require.NoError(t, readErr)
		require.Len(t, items, 64)
		for _, item := range items {
			require.Equal(t, int64(0), item.(map[string]any)["generation"])
		}
	}
	wait.Wait()
}
