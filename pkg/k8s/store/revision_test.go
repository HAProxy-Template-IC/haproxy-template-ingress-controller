package store

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"

	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type revisionStore interface {
	Add(resource any, keys []string) error
	Update(resource any, keys []string) error
	Delete(namespace, name string, keys []string) error
	Clear() error
	stores.Revisioned
	stores.RevisionJournal
	stores.IdentityGetter
}

func TestStoreClearRevisionsAreScoped(t *testing.T) {
	for name, resourceStore := range revisionStores(t) {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, resourceStore.Add(namedResource("default", "target"), []string{"blue", "target"}))
			_, sequence, err := resourceStore.ListSnapshot()
			require.NoError(t, err)
			listBefore := resourceStore.ListRevision()
			keyBefore := resourceStore.GetRevision("blue")
			targetBefore := resourceStore.IdentityRevision("default", "target")
			missingBefore := resourceStore.IdentityRevision("default", "missing")

			require.NoError(t, resourceStore.Clear())
			require.NotEqual(t, listBefore, resourceStore.ListRevision())
			require.NotEqual(t, keyBefore, resourceStore.GetRevision("blue"))
			require.NotEqual(t, targetBefore, resourceStore.IdentityRevision("default", "target"))
			require.Equal(t, missingBefore, resourceStore.IdentityRevision("default", "missing"))
			_, changes, complete := resourceStore.ChangesSince(sequence)
			require.True(t, complete)
			require.Equal(t, []stores.RevisionChange{{
				Sequence:  sequence + 1,
				Namespace: "default",
				Name:      "target",
				Deleted:   true,
				OldKeys:   []string{"blue", "target"},
			}}, changes)

			listAfter := resourceStore.ListRevision()
			require.NoError(t, resourceStore.Clear())
			require.Equal(t, listAfter, resourceStore.ListRevision())
		})
	}
}

func revisionStores(t *testing.T) map[string]revisionStore {
	t.Helper()
	return map[string]revisionStore{
		"memory": NewMemoryStore(2),
		"cached": newTestCachedStore(
			t,
			fake.NewSimpleDynamicClient(runtime.NewScheme()),
			createTestIndexer(),
			2,
			time.Minute,
		),
	}
}

func TestStoreRevisionsAreScoped(t *testing.T) {
	for name, resourceStore := range revisionStores(t) {
		t.Run(name, func(t *testing.T) {
			listBefore := resourceStore.ListRevision()
			targetBefore := resourceStore.IdentityRevision("default", "target")
			otherBefore := resourceStore.IdentityRevision("default", "other")
			blueBefore := resourceStore.GetRevision("blue")
			blueTargetBefore := resourceStore.GetRevision("blue", "target")
			redBefore := resourceStore.GetRevision("red")

			require.NoError(t, resourceStore.Add(namedResource("default", "target"), []string{"blue", "target"}))
			require.NotEqual(t, listBefore, resourceStore.ListRevision())
			require.NotEqual(t, targetBefore, resourceStore.IdentityRevision("default", "target"))
			require.Equal(t, otherBefore, resourceStore.IdentityRevision("default", "other"))
			require.NotEqual(t, blueBefore, resourceStore.GetRevision("blue"))
			require.NotEqual(t, blueTargetBefore, resourceStore.GetRevision("blue", "target"))
			require.Equal(t, redBefore, resourceStore.GetRevision("red"))
			listAfterCreate := resourceStore.ListRevision()
			targetAfterCreate := resourceStore.IdentityRevision("default", "target")
			require.NoError(t, resourceStore.Update(
				namedResource("default", "target"), []string{"blue", "target"},
			))
			require.Equal(t, listAfterCreate, resourceStore.ListRevision())
			require.Equal(t, targetAfterCreate, resourceStore.IdentityRevision("default", "target"))

			blueAfterCreate := resourceStore.GetRevision("blue")
			redBeforeCreate := resourceStore.GetRevision("red")
			require.NoError(t, resourceStore.Add(namedResource("default", "other"), []string{"red", "other"}))
			require.Equal(t, targetAfterCreate, resourceStore.IdentityRevision("default", "target"))
			require.Equal(t, blueAfterCreate, resourceStore.GetRevision("blue"))
			require.NotEqual(t, redBeforeCreate, resourceStore.GetRevision("red"))

			blueBeforeMove := resourceStore.GetRevision("blue")
			blueTargetBeforeMove := resourceStore.GetRevision("blue", "target")
			greenBeforeMove := resourceStore.GetRevision("green")
			redBeforeMove := resourceStore.GetRevision("red")
			otherBeforeMove := resourceStore.IdentityRevision("default", "other")
			require.NoError(t, resourceStore.Update(namedResource("default", "target"), []string{"green", "target"}))
			require.NotEqual(t, blueBeforeMove, resourceStore.GetRevision("blue"))
			require.NotEqual(t, blueTargetBeforeMove, resourceStore.GetRevision("blue", "target"))
			require.NotEqual(t, greenBeforeMove, resourceStore.GetRevision("green"))
			require.Equal(t, redBeforeMove, resourceStore.GetRevision("red"))
			require.Equal(t, otherBeforeMove, resourceStore.IdentityRevision("default", "other"))

			listBeforeNoop := resourceStore.ListRevision()
			missingBeforeNoop := resourceStore.IdentityRevision("default", "missing")
			require.NoError(t, resourceStore.Delete("default", "missing", []string{"green", "missing"}))
			require.Equal(t, listBeforeNoop, resourceStore.ListRevision())
			require.Equal(t, missingBeforeNoop, resourceStore.IdentityRevision("default", "missing"))

			beforeDelete := resourceStore.IdentityRevision("default", "target")
			require.NoError(t, resourceStore.Delete("default", "target", []string{"green", "target"}))
			afterDelete := resourceStore.IdentityRevision("default", "target")
			require.NotEqual(t, beforeDelete, afterDelete)
			require.NoError(t, resourceStore.Add(namedResource("default", "target"), []string{"green", "target"}))
			require.NotEqual(t, afterDelete, resourceStore.IdentityRevision("default", "target"))
		})
	}
}

func TestStoreRevisionsDistinguishInstancesAndIgnoreFailedMutations(t *testing.T) {
	first := NewMemoryStore(2)
	second := NewMemoryStore(2)
	sourceBefore := first.RevisionSource()
	require.NotZero(t, sourceBefore)
	require.NotEqual(t, sourceBefore, second.RevisionSource())
	require.NotEqual(t, first.ListRevision(), second.ListRevision())
	require.NotEqual(t,
		first.IdentityRevision("default", "missing"),
		second.IdentityRevision("default", "missing"),
	)

	listBefore := first.ListRevision()
	keyBefore := first.GetRevision("blue")
	require.Error(t, first.Add(namedResource("default", "target"), []string{"wrong"}))
	require.Equal(t, listBefore, first.ListRevision())
	require.Equal(t, keyBefore, first.GetRevision("blue"))
	require.Equal(t, sourceBefore, first.RevisionSource())
}

func TestMemoryStoreSnapshotsBindNegativeReads(t *testing.T) {
	resourceStore := NewMemoryStore(2)
	items, getRevision, sequence, err := resourceStore.GetSnapshot("blue", "target")
	require.NoError(t, err)
	require.Empty(t, items)
	require.NotEmpty(t, getRevision)
	resource, found, identityRevision, identitySequence, err :=
		resourceStore.IdentitySnapshot("default", "target")
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, resource)
	require.NotEmpty(t, identityRevision)
	require.Equal(t, sequence, identitySequence)

	require.NoError(t, resourceStore.Add(namedResource("default", "target"), []string{"blue", "target"}))
	items, afterGetRevision, afterSequence, err := resourceStore.GetSnapshot("blue", "target")
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotEqual(t, getRevision, afterGetRevision)
	resource, found, afterIdentityRevision, afterIdentitySequence, err :=
		resourceStore.IdentitySnapshot("default", "target")
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, resource)
	require.NotEqual(t, identityRevision, afterIdentityRevision)
	require.Equal(t, afterSequence, afterIdentitySequence)
}

func TestMemoryStoreSnapshotReaderReturnsDetachedValues(t *testing.T) {
	resourceStore := NewMemoryStore(2)
	require.NoError(t, resourceStore.Add(namedResource("default", "target"), []string{"blue", "target"}))
	listRevision := resourceStore.ListRevision()
	getRevision := resourceStore.GetRevision("blue", "target")
	identityRevision := resourceStore.IdentityRevision("default", "target")

	items, _, err := resourceStore.ListSnapshot()
	require.NoError(t, err)
	require.Len(t, items, 1)
	items[0].(map[string]any)["value"] = "list-poison"

	items, _, _, err = resourceStore.GetSnapshot("blue", "target")
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotEqual(t, "list-poison", items[0].(map[string]any)["value"])
	items[0].(map[string]any)["value"] = "get-poison"

	item, found, _, _, err := resourceStore.IdentitySnapshot("default", "target")
	require.NoError(t, err)
	require.True(t, found)
	require.NotEqual(t, "get-poison", item.(map[string]any)["value"])
	item.(map[string]any)["value"] = "identity-poison"

	items, _, err = resourceStore.ListSnapshot()
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotEqual(t, "identity-poison", items[0].(map[string]any)["value"])
	require.Equal(t, listRevision, resourceStore.ListRevision())
	require.Equal(t, getRevision, resourceStore.GetRevision("blue", "target"))
	require.Equal(t, identityRevision, resourceStore.IdentityRevision("default", "target"))
}

func TestMemoryStoreKeySnapshotsFailClosedForUnidentifiedResources(t *testing.T) {
	resourceStore := NewMemoryStore(1)
	require.NoError(t, resourceStore.Add(map[string]any{"value": "unidentified"}, []string{"key"}))
	require.Empty(t, resourceStore.GetRevision("key"))
	require.Empty(t, resourceStore.IdentityRevision("default", "missing"))
	items, revision, sequence, snapshotErr := resourceStore.GetSnapshot("key")
	require.Nil(t, items)
	require.Empty(t, revision)
	require.Equal(t, uint64(1), sequence)
	require.ErrorIs(t, snapshotErr, stores.ErrSnapshotUnsupported)
	items, sequence, err := resourceStore.ListSnapshot()
	require.Nil(t, items)
	require.Equal(t, uint64(1), sequence)
	require.ErrorIs(t, err, stores.ErrSnapshotUnsupported)
	resource, found, revision, sequence, err := resourceStore.IdentitySnapshot("default", "missing")
	require.Nil(t, resource)
	require.False(t, found)
	require.Empty(t, revision)
	require.Equal(t, uint64(1), sequence)
	require.ErrorIs(t, err, stores.ErrSnapshotUnsupported)
}

func TestRevisionCountersDoNotWrap(t *testing.T) {
	counter := &atomic.Uint64{}
	counter.Store(^uint64(0))
	require.PanicsWithValue(t, "store revision source exhausted", func() {
		allocateRevisionSource(counter)
	})
	require.Equal(t, ^uint64(0), counter.Load())

	revisions := revisionState{sequence: ^uint64(0)}
	require.PanicsWithValue(t, "store revision sequence exhausted", func() {
		revisions.nextSequence()
	})
	require.Equal(t, ^uint64(0), revisions.sequence)
}

func TestStoreRevisionJournalTracksIndexMove(t *testing.T) {
	for name, resourceStore := range revisionStores(t) {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, resourceStore.Add(namedResource("default", "target"), []string{"blue", "target"}))
			items, sequence, err := resourceStore.ListSnapshot()
			require.NoError(t, err)
			require.Len(t, items, 1)

			require.NoError(t, resourceStore.Update(namedResource("default", "target"), []string{"green", "target"}))
			current, changes, complete := resourceStore.ChangesSince(sequence)
			require.True(t, complete)
			require.Equal(t, sequence+1, current)
			require.Equal(t, []stores.RevisionChange{{
				Sequence:  current,
				Namespace: "default",
				Name:      "target",
				OldKeys:   []string{"blue", "target"},
				NewKeys:   []string{"green", "target"},
			}}, changes)

			resource, found, err := resourceStore.GetIdentity("default", "target")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, "target", resourceName(t, resource))
		})
	}
}

func TestStoreRevisionMetadataIsBoundedByLiveResources(t *testing.T) {
	for name, resourceStore := range revisionStores(t) {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, resourceStore.Add(
				namedResource("default", "stable"),
				[]string{"shared", "stable"},
			))
			for index := range 512 {
				resourceName := fmt.Sprintf("transient-%03d", index)
				keys := []string{"shared", resourceName}
				require.NoError(t, resourceStore.Add(namedResource("default", resourceName), keys))
				require.NoError(t, resourceStore.Delete("default", resourceName, keys))
			}

			switch typed := resourceStore.(type) {
			case *MemoryStore:
				require.Len(t, typed.revisions.identityKeys, 1)
				require.Len(t, typed.revisions.identityVersions, 1)
				require.Len(t, typed.revisions.keyCounts, 2)
				require.Len(t, typed.revisions.keyVersions, 2)
				root := typed.readRoot.Load()
				require.Equal(t, 1, root.locations.Len())
				require.Equal(t, 2, root.keyVersions.Len())
				require.Equal(t, 1, root.identityVersions.Len())
			case *CachedStore:
				require.Len(t, typed.revisions.identityKeys, 1)
				require.Len(t, typed.revisions.identityVersions, 1)
				require.Len(t, typed.revisions.keyCounts, 2)
				require.Len(t, typed.revisions.keyVersions, 2)
				root := typed.readRoot.Load()
				require.Equal(t, 1, root.locations.Len())
				require.Equal(t, 2, root.keyVersions.Len())
				require.Equal(t, 1, root.identityVersions.Len())
			default:
				t.Fatalf("unsupported revision store %T", resourceStore)
			}
		})
	}
}

func TestRevisionJournalReportsOverflow(t *testing.T) {
	resourceStore := NewMemoryStore(1)
	resourceStore.revisions = newRevisionState(2)
	for index := 1; index <= 3; index++ {
		name := fmt.Sprintf("item-%d", index)
		require.NoError(t, resourceStore.Add(namedResource("default", name), []string{name}))
	}

	current, changes, complete := resourceStore.ChangesSince(0)
	require.Equal(t, uint64(3), current)
	require.False(t, complete)
	require.Empty(t, changes)

	current, changes, complete = resourceStore.ChangesSince(1)
	require.True(t, complete)
	require.Equal(t, uint64(3), current)
	require.Len(t, changes, 2)
	changes[0].NewKeys[0] = "mutated"
	_, changes, complete = resourceStore.ChangesSince(1)
	require.True(t, complete)
	require.Equal(t, "item-2", changes[0].NewKeys[0])
}

func BenchmarkRevisionJournalOneChangeAfterHistory(b *testing.B) {
	for _, history := range []int{1, 1000, 4000} {
		b.Run(fmt.Sprintf("history=%d", history), func(b *testing.B) {
			resourceStore := NewMemoryStore(1)
			for index := range history {
				name := fmt.Sprintf("item-%04d", index)
				require.NoError(b, resourceStore.Add(namedResource("default", name), []string{name}))
			}
			_, sequence, err := resourceStore.ListSnapshot()
			require.NoError(b, err)
			updated := namedResource("default", "item-0000")
			updated["value"] = "changed"
			require.NoError(b, resourceStore.Update(updated, []string{"item-0000"}))
			_, changes, complete := resourceStore.ChangesSince(sequence)
			require.True(b, complete)
			require.Len(b, changes, 1)

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_, changes, complete = resourceStore.ChangesSince(sequence)
				if !complete || len(changes) != 1 {
					b.Fatalf("one-change suffix is unavailable: complete=%t changes=%d", complete, len(changes))
				}
			}
		})
	}
}

func TestMemoryStoreListSnapshotIsAtomicWithJournalSequence(t *testing.T) {
	resourceStore := NewMemoryStore(1)
	const resourceCount = 200

	writerDone := make(chan error, 1)
	go func() {
		for index := range resourceCount {
			name := fmt.Sprintf("item-%03d", index)
			if err := resourceStore.Add(namedResource("default", name), []string{name}); err != nil {
				writerDone <- err
				return
			}
		}
		writerDone <- nil
	}()

	for {
		items, sequence, err := resourceStore.ListSnapshot()
		require.NoError(t, err)
		require.Len(t, items, int(sequence))
		if sequence == resourceCount {
			break
		}
	}
	require.NoError(t, <-writerDone)
}

func TestMemoryStoreGetSnapshotIsStableWithMutation(t *testing.T) {
	resourceStore := NewMemoryStore(1)
	const resourceCount = 200
	writerDone := make(chan error, 1)
	go func() {
		for index := range resourceCount {
			name := fmt.Sprintf("item-%03d", resourceCount-index)
			if err := resourceStore.Add(namedResource("default", name), []string{"bucket"}); err != nil {
				writerDone <- err
				return
			}
		}
		writerDone <- nil
	}()

	for {
		items, revision, sequence, err := resourceStore.GetSnapshot("bucket")
		require.NoError(t, err)
		require.NotEmpty(t, revision)
		require.Len(t, items, int(sequence))
		for index := 1; index < len(items); index++ {
			require.Less(t, resourceName(t, items[index-1]), resourceName(t, items[index]))
		}
		if sequence == resourceCount {
			break
		}
	}
	require.NoError(t, <-writerDone)
}

func TestMemoryStoreIdentitySnapshotIsAtomicWithMutation(t *testing.T) {
	resourceStore := NewMemoryStore(1)
	require.NoError(t, resourceStore.Add(indexTransitionResource("target", "A", "0"), []string{"A"}))
	const updateCount = 200
	writerDone := make(chan error, 1)
	go func() {
		for index := 1; index <= updateCount; index++ {
			resource := indexTransitionResource("target", "A", strconv.Itoa(index))
			if err := resourceStore.Update(resource, []string{"A"}); err != nil {
				writerDone <- err
				return
			}
		}
		writerDone <- nil
	}()

	for {
		resource, found, revision, sequence, err := resourceStore.IdentitySnapshot("default", "target")
		require.NoError(t, err)
		require.True(t, found)
		require.NotEmpty(t, revision)
		value, err := strconv.Atoi(resourceRevision(t, resource))
		require.NoError(t, err)
		require.Equal(t, uint64(value+1), sequence)
		if value == updateCount {
			break
		}
	}
	require.NoError(t, <-writerDone)
}

func TestCachedStoreRevisionAPIUsesPinnedRoot(t *testing.T) {
	client := fake.NewSimpleDynamicClient(runtime.NewScheme())
	resourceStore := newTestCachedStore(t, client, createTestIndexer(), 2, time.Minute)
	_, revisioned := any(resourceStore).(stores.Revisioned)
	require.True(t, revisioned)
	_, journaled := any(resourceStore).(stores.RevisionJournal)
	require.True(t, journaled)
	_, snapshotReader := any(resourceStore).(stores.SnapshotReader)
	require.True(t, snapshotReader)

	redBefore := resourceStore.revisions.getRevision([]string{"red"})
	require.NoError(t, resourceStore.Add(namedResource("default", "target"), []string{"blue", "target"}))
	require.Equal(t, uint64(1), resourceStore.revisions.sequence)
	require.Equal(t, redBefore, resourceStore.revisions.getRevision([]string{"red"}))
	require.NoError(t, resourceStore.Update(namedResource("default", "target"), []string{"blue", "target"}))
	require.Equal(t, uint64(1), resourceStore.revisions.sequence)
	require.NoError(t, resourceStore.Update(namedResource("default", "target"), []string{"green", "target"}))
	require.Equal(t, uint64(2), resourceStore.revisions.sequence)
	require.NoError(t, resourceStore.Delete("default", "missing", []string{"green", "missing"}))
	require.Equal(t, uint64(2), resourceStore.revisions.sequence)

	adapter := &stores.TypesStoreAdapter{Inner: resourceStore}
	require.NotZero(t, adapter.RevisionSource())
	require.NotEmpty(t, adapter.ListRevision())
	require.NotEmpty(t, adapter.GetRevision("green", "target"))
	require.NotEmpty(t, adapter.IdentityRevision("default", "target"))
	listItems, listSequence, listErr := adapter.ListSnapshot()
	require.NoError(t, listErr)
	require.Len(t, listItems, 1)
	require.Equal(t, uint64(2), listSequence)
	_, changes, complete := adapter.ChangesSince(0)
	require.True(t, complete)
	require.Len(t, changes, 2)
	items, revision, sequence, snapshotErr := adapter.GetSnapshot("green", "target")
	require.NoError(t, snapshotErr)
	require.Len(t, items, 1)
	require.NotEmpty(t, revision)
	require.Equal(t, uint64(2), sequence)
	resource, found, err := adapter.GetIdentity("default", "target")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "target", resourceName(t, resource))
}

// A watch echo of the controller's own status write differs from the stored
// object only in metadata.resourceVersion once the written fields are
// ignored. That is not a change: nothing a template can read moved.
func TestMemoryStoreResourceVersionAloneIsNotAChange(t *testing.T) {
	resourceStore := NewMemoryStore(2)
	versioned := func(version string, spec any) map[string]any {
		return map[string]any{
			"metadata": map[string]any{"namespace": "default", "name": "target", "resourceVersion": version},
			"spec":     spec,
		}
	}
	require.NoError(t, resourceStore.Add(versioned("1", "a"), []string{"blue", "target"}))
	list := resourceStore.ListRevision()
	identity := resourceStore.IdentityRevision("default", "target")

	require.NoError(t, resourceStore.Update(versioned("2", "a"), []string{"blue", "target"}))
	require.Equal(t, list, resourceStore.ListRevision())
	require.Equal(t, identity, resourceStore.IdentityRevision("default", "target"))

	require.NoError(t, resourceStore.Update(versioned("3", "b"), []string{"blue", "target"}))
	require.NotEqual(t, identity, resourceStore.IdentityRevision("default", "target"))
	items, err := resourceStore.Get("blue", "target")
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "3", items[0].(map[string]any)["metadata"].(map[string]any)["resourceVersion"])
}
