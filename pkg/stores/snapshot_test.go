package stores

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type mockReadSnapshot struct {
	resources map[string]any
	source    RevisionSource
	sequence  uint64
}

func (s *revisionMockStore) Pin() (ReadSnapshot, error) {
	resources := make(map[string]any, len(s.resources))
	for key, value := range s.resources {
		if object, ok := value.(runtime.Object); ok {
			value = object.DeepCopyObject()
		}
		resources[key] = value
	}
	return &mockReadSnapshot{resources: resources, source: s.source, sequence: 7}, nil
}

func (s *mockReadSnapshot) RevisionSource() RevisionSource {
	return s.source
}

func (s *mockReadSnapshot) Sequence() uint64 {
	return s.sequence
}

func (s *mockReadSnapshot) ListRevision() Revision {
	return "base-list"
}

func (s *mockReadSnapshot) GetRevision(keys ...string) Revision {
	return Revision("base-get:" + keyString(keys))
}

func (s *mockReadSnapshot) IdentityRevision(namespace, name string) Revision {
	return Revision("base-identity:" + namespace + "/" + name)
}

func (s *mockReadSnapshot) Get(keys ...string) ([]any, error) {
	item, found := s.resources[keyString(keys)]
	if !found {
		return []any{}, nil
	}
	return []any{item}, nil
}

func (s *mockReadSnapshot) GetContext(ctx context.Context, keys ...string) ([]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.Get(keys...)
}

func (s *mockReadSnapshot) List() ([]any, error) {
	items := make([]any, 0, len(s.resources))
	for _, item := range s.resources {
		items = append(items, item)
	}
	return items, nil
}

func (s *mockReadSnapshot) ListContext(ctx context.Context) ([]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.List()
}

func (s *mockReadSnapshot) GetIdentity(namespace, name string) (item any, found bool, err error) {
	for _, candidate := range s.resources {
		identity := getResourceKey(candidate)
		if identity != nil && identity.Namespace == namespace && identity.Name == name {
			return candidate, true, nil
		}
	}
	return nil, false, nil
}

func (s *mockReadSnapshot) GetIdentityContext(
	ctx context.Context,
	namespace, name string,
) (item any, found bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return s.GetIdentity(namespace, name)
}

func TestOverlayReadSnapshotFreezesBaseAndProjectedChanges(t *testing.T) {
	base := newRevisionMockStore()
	require.NoError(t, base.Add(overlayConfigMap("accepted"), []string{"blue", "shared"}))
	modified := overlayConfigMap("pending")
	baseSnapshot, err := base.Pin()
	require.NoError(t, err)
	pinned, err := OverlayReadSnapshot(baseSnapshot, []SnapshotChange{{
		Namespace: "default",
		Name:      "target",
		Value:     modified,
		OldKeys:   []string{"blue", "shared"},
		NewKeys:   []string{"green", "shared"},
	}})
	require.NoError(t, err)
	pinnedListRevision := pinned.ListRevision()
	pinnedBlueRevision := pinned.GetRevision("blue")
	pinnedGreenRevision := pinned.GetRevision("green")
	pinnedIdentityRevision := pinned.IdentityRevision("default", "target")
	require.Equal(t, baseSnapshot.GetRevision("red"), pinned.GetRevision("red"))
	require.NotEqual(t, baseSnapshot.GetRevision("blue"), pinnedBlueRevision)
	require.NotEqual(t, baseSnapshot.GetRevision("green"), pinnedGreenRevision)

	require.NoError(t, base.Update(overlayConfigMap("new accepted"), []string{"blue", "shared"}))
	additionalBase := overlayConfigMap("new base")
	additionalBase.Name = "new-base"
	require.NoError(t, base.Add(additionalBase, []string{"green", "new-base"}))
	modified.Data["value"] = "changed pending"

	items, err := pinned.Get("blue")
	require.NoError(t, err)
	require.Empty(t, items)
	items, err = pinned.Get("green")
	require.NoError(t, err)
	require.Len(t, items, 1)
	resource, ok := items[0].(*corev1.ConfigMap)
	require.True(t, ok)
	require.Equal(t, "pending", resource.Data["value"])
	resource.Data["value"] = "poison"
	items, err = pinned.Get("green")
	require.NoError(t, err)
	resource, ok = items[0].(*corev1.ConfigMap)
	require.True(t, ok)
	require.Equal(t, "pending", resource.Data["value"])
	items, err = pinned.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, pinnedListRevision, pinned.ListRevision())
	require.Equal(t, pinnedBlueRevision, pinned.GetRevision("blue"))
	require.Equal(t, pinnedGreenRevision, pinned.GetRevision("green"))
	require.Equal(t, pinnedIdentityRevision, pinned.IdentityRevision("default", "target"))
}

func TestCompositeStorePinRequiresProjectedOverlayKeys(t *testing.T) {
	base := newRevisionMockStore()
	composite := NewCompositeStore(base, NewStoreOverlayForUpdate(overlayConfigMap("pending")))
	_, err := composite.Pin()
	require.ErrorIs(t, err, ErrSnapshotUnsupported)
}

func TestCompositeStorePinRejectsUnsupportedBase(t *testing.T) {
	composite := NewCompositeStore(newMockStore(), NewStoreOverlay())
	_, err := composite.Pin()
	require.ErrorIs(t, err, ErrSnapshotUnsupported)
}

func TestOverlayReadSnapshotRejectsIncorrectOldProjection(t *testing.T) {
	base := newRevisionMockStore()
	require.NoError(t, base.Add(overlayConfigMap("accepted"), []string{"blue", "shared"}))
	baseSnapshot, err := base.Pin()
	require.NoError(t, err)
	_, err = OverlayReadSnapshot(baseSnapshot, []SnapshotChange{{
		Namespace: "default",
		Name:      "target",
		Value:     overlayConfigMap("pending"),
		OldKeys:   []string{"wrong", "shared"},
		NewKeys:   []string{"green", "shared"},
	}})
	require.ErrorIs(t, err, ErrSnapshotUnsupported)
}

func TestOverlayReadSnapshotElidesSemanticNoOps(t *testing.T) {
	base := newRevisionMockStore()
	resource := overlayConfigMap("accepted")
	require.NoError(t, base.Add(resource, []string{"blue", "shared"}))
	baseSnapshot, err := base.Pin()
	require.NoError(t, err)
	pinned, err := OverlayReadSnapshot(baseSnapshot, []SnapshotChange{{
		Namespace: "default",
		Name:      "target",
		Value:     resource.DeepCopy(),
		OldKeys:   []string{"blue", "shared"},
		NewKeys:   []string{"blue", "shared"},
	}, {
		Namespace: "default",
		Name:      "missing",
		Deleted:   true,
	}})
	require.NoError(t, err)
	require.Same(t, baseSnapshot, pinned)
}

func TestTypesStoreAdapterDelegatesPin(t *testing.T) {
	inner := newRevisionMockStore()
	adapter := &TypesStoreAdapter{Inner: inner}
	pinned, err := adapter.Pin()
	require.NoError(t, err)
	require.Equal(t, inner.RevisionSource(), pinned.RevisionSource())
	require.Equal(t, uint64(7), pinned.Sequence())
	require.Implements(t, (*ContextReadSnapshot)(nil), pinned)

	unsupported := &TypesStoreAdapter{Inner: newMockStore()}
	_, err = unsupported.Pin()
	require.ErrorIs(t, err, ErrSnapshotUnsupported)
}
