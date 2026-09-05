package stores

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type revisionMockStore struct {
	*mockStore
	source          RevisionSource
	identityVersion string
}

func newRevisionMockStore() *revisionMockStore {
	return &revisionMockStore{mockStore: newMockStore(), source: 42}
}

func (s *revisionMockStore) ListRevision() Revision {
	return "base-list"
}

func (s *revisionMockStore) GetRevision(keys ...string) Revision {
	return Revision("base-get:" + keyString(keys))
}

func (s *revisionMockStore) IdentityRevision(namespace, name string) Revision {
	return Revision("base-identity:" + namespace + "/" + name + ":" + s.identityVersion)
}

func (s *revisionMockStore) ListSnapshot() (items []any, sequence uint64, err error) {
	items, err = s.List()
	return items, 7, err
}

func (s *revisionMockStore) ChangesSince(uint64) (uint64, []RevisionChange, bool) {
	return 7, nil, true
}

func (s *revisionMockStore) GetIdentity(namespace, name string) (item any, found bool, err error) {
	items, err := s.Get(namespace, name)
	if err != nil || len(items) == 0 {
		return nil, false, err
	}
	return items[0], true, nil
}

func (s *revisionMockStore) RevisionSource() RevisionSource {
	return s.source
}

func (s *revisionMockStore) GetSnapshot(
	keys ...string,
) (items []any, revision Revision, sequence uint64, err error) {
	items, err = s.Get(keys...)
	return items, s.GetRevision(keys...), 7, err
}

func (s *revisionMockStore) IdentitySnapshot(
	namespace, name string,
) (item any, found bool, revision Revision, sequence uint64, err error) {
	item, found, err = s.GetIdentity(namespace, name)
	return item, found, s.IdentityRevision(namespace, name), 7, err
}

func overlayConfigMap(value string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "target"},
		Data:       map[string]string{"value": value},
	}
}

func TestExactOverlayRevisionPreservesStructuredIdentity(t *testing.T) {
	baseline := []overlayRevisionPart{{operation: "addition", payload: []byte("default/target")}}
	revisions := []struct {
		name  string
		parts []overlayRevisionPart
	}{
		{name: "operation boundary", parts: []overlayRevisionPart{{operation: "additiondefault/", payload: []byte("target")}}},
		{name: "payload boundary", parts: []overlayRevisionPart{{operation: "addition", payload: []byte("default")}, {operation: "/", payload: []byte("target")}}},
		{name: "operation", parts: []overlayRevisionPart{{operation: "modification", payload: []byte("default/target")}}},
		{name: "payload", parts: []overlayRevisionPart{{operation: "addition", payload: []byte("default/other")}}},
	}

	require.Equal(t, exactRevision(baseline), exactRevision(baseline))
	for _, test := range revisions {
		t.Run(test.name, func(t *testing.T) {
			require.NotEqual(t, exactRevision(baseline), exactRevision(test.parts))
		})
	}
	require.NotEqual(t, combineRevisions("scope", "a", "bc"), combineRevisions("scope", "ab", "c"))
	require.Empty(t, combineRevisions("scope", "a", ""))
}

func TestCompositeStoreIdentityRevisionsAreExact(t *testing.T) {
	base := newRevisionMockStore()
	first := NewCompositeStore(base, NewStoreOverlayForUpdate(overlayConfigMap("one")))
	identical := NewCompositeStore(base, NewStoreOverlayForUpdate(overlayConfigMap("one")))
	changed := NewCompositeStore(base, NewStoreOverlayForUpdate(overlayConfigMap("two")))

	require.Equal(t,
		base.IdentityRevision("default", "other"),
		first.IdentityRevision("default", "other"),
	)
	require.NotEqual(t,
		base.IdentityRevision("default", "target"),
		first.IdentityRevision("default", "target"),
	)
	require.Equal(t,
		first.IdentityRevision("default", "target"),
		identical.IdentityRevision("default", "target"),
	)
	require.NotEqual(t,
		first.IdentityRevision("default", "target"),
		changed.IdentityRevision("default", "target"),
	)
	require.NotEqual(t, first.ListRevision(), changed.ListRevision())
	require.Empty(t, first.GetRevision("default", "target"))

	targetRevision := first.IdentityRevision("default", "target")
	base.identityVersion = "changed"
	require.Equal(t, targetRevision, first.IdentityRevision("default", "target"))
	require.Equal(t,
		base.IdentityRevision("default", "other"),
		first.IdentityRevision("default", "other"),
	)

	addition := NewCompositeStore(base, NewStoreOverlayForCreate(overlayConfigMap("one")))
	additionRevision := addition.IdentityRevision("default", "target")
	base.identityVersion = "changed-again"
	require.NotEqual(t, additionRevision, addition.IdentityRevision("default", "target"))

	otherBase := newRevisionMockStore()
	otherBase.source = 43
	other := NewCompositeStore(otherBase, NewStoreOverlayForUpdate(overlayConfigMap("one")))
	require.NotEqual(t,
		first.IdentityRevision("default", "target"),
		other.IdentityRevision("default", "target"),
	)
}

func TestCompositeStoreEmptyOverlayDelegatesRevisionsAndJournal(t *testing.T) {
	base := newRevisionMockStore()
	composite := NewCompositeStore(base, NewStoreOverlay())

	require.Equal(t, base.ListRevision(), composite.ListRevision())
	require.Equal(t, base.GetRevision("default"), composite.GetRevision("default"))
	require.Equal(t,
		base.IdentityRevision("default", "target"),
		composite.IdentityRevision("default", "target"),
	)
	require.Equal(t, base.RevisionSource(), composite.RevisionSource())
	_, revision, snapshotSequence, err := composite.GetSnapshot("default", "target")
	require.NoError(t, err)
	require.Equal(t, base.GetRevision("default", "target"), revision)
	require.Equal(t, uint64(7), snapshotSequence)
	_, sequence, err := composite.ListSnapshot()
	require.NoError(t, err)
	require.Equal(t, uint64(7), sequence)
	current, changes, complete := composite.ChangesSince(sequence)
	require.True(t, complete)
	require.Equal(t, uint64(7), current)
	require.Empty(t, changes)
}

func TestCompositeStoreOverlayJournalRequiresNewSnapshot(t *testing.T) {
	base := newRevisionMockStore()
	composite := NewCompositeStore(base, NewStoreOverlayForCreate(overlayConfigMap("one")))

	_, sequence, err := composite.ListSnapshot()
	require.NoError(t, err)
	current, changes, complete := composite.ChangesSince(sequence)
	require.False(t, complete)
	require.Equal(t, uint64(7), current)
	require.Empty(t, changes)
	require.Zero(t, composite.RevisionSource())
	items, revision, snapshotSequence, snapshotErr := composite.GetSnapshot("default", "target")
	require.Nil(t, items)
	require.Empty(t, revision)
	require.Zero(t, snapshotSequence)
	require.ErrorIs(t, snapshotErr, ErrSnapshotUnsupported)
}

func TestTypesStoreAdapterDelegatesOptionalRevisionAPIs(t *testing.T) {
	inner := newRevisionMockStore()
	adapter := &TypesStoreAdapter{Inner: inner}
	require.Equal(t, inner.ListRevision(), adapter.ListRevision())
	require.Equal(t, inner.RevisionSource(), adapter.RevisionSource())
	require.Equal(t, inner.GetRevision("default"), adapter.GetRevision("default"))
	require.Equal(t,
		inner.IdentityRevision("default", "target"),
		adapter.IdentityRevision("default", "target"),
	)
	_, sequence, err := adapter.ListSnapshot()
	require.NoError(t, err)
	require.Equal(t, uint64(7), sequence)
	_, _, complete := adapter.ChangesSince(sequence)
	require.True(t, complete)

	unsupported := &TypesStoreAdapter{Inner: newMockStore()}
	require.Empty(t, unsupported.ListRevision())
	require.Zero(t, unsupported.RevisionSource())
	items, sequence, err := unsupported.ListSnapshot()
	require.Nil(t, items)
	require.Zero(t, sequence)
	require.ErrorIs(t, err, ErrSnapshotUnsupported)
	_, _, complete = unsupported.ChangesSince(0)
	require.False(t, complete)
	_, _, err = unsupported.GetIdentity("default", "target")
	require.True(t, errors.Is(err, ErrIdentityLookupUnsupported))
}
