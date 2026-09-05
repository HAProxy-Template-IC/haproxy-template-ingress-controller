// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores/storetest"
)

func TestPinIncrementalStoreSnapshotsProjectsArbitraryOverlayIndex(t *testing.T) {
	cfg := &config.Config{WatchedResources: map[string]config.WatchedResource{
		"routes": {IndexBy: []string{"spec.tenant"}},
	}}
	base := k8sstore.NewMemoryStore(1)
	require.NoError(t, base.Add(
		incrementalTestResource("tenant-ns", "route", map[string]any{"tenant": "blue", "value": "old"}),
		[]string{"blue"},
	))
	require.NoError(t, base.Add(
		incrementalTestResource("tenant-ns", "sibling", map[string]any{"tenant": "blue", "value": "winner"}),
		[]string{"blue"},
	))
	updated := &unstructured.Unstructured{Object: incrementalTestResource(
		"tenant-ns",
		"route",
		map[string]any{"tenant": "green", "value": "new"},
	)}
	overlay := stores.NewStoreOverlayForUpdate(updated)
	provider := stores.NewOverlayStoreProvider(
		stores.NewRealStoreProvider(map[string]stores.Store{"routes": base}),
		stores.NewValidationContext(map[string]*stores.StoreOverlay{"routes": overlay}),
	)

	snapshots, err := pinIncrementalStoreSnapshots(
		cfg,
		map[string]struct{}{"routes": {}},
		provider,
	)
	require.NoError(t, err)
	require.Len(t, snapshots.overlayChanges["routes"], 1)
	assert.Equal(t, []string{"blue"}, snapshots.overlayChanges["routes"][0].OldKeys)
	assert.Equal(t, []string{"green"}, snapshots.overlayChanges["routes"][0].NewKeys)

	blue, err := snapshots.render["routes"].Get("blue")
	require.NoError(t, err)
	require.Len(t, blue, 1)
	assert.Equal(t, "sibling", blue[0].(map[string]any)["metadata"].(map[string]any)["name"])
	green, err := snapshots.render["routes"].Get("green")
	require.NoError(t, err)
	require.Len(t, green, 1)
	assert.Equal(t, "new", green[0].(map[string]any)["spec"].(map[string]any)["value"])

	green[0].(map[string]any)["spec"].(map[string]any)["value"] = "poison"
	again, err := snapshots.render["routes"].Get("green")
	require.NoError(t, err)
	assert.Equal(t, "new", again[0].(map[string]any)["spec"].(map[string]any)["value"])

	updated.Object["spec"].(map[string]any)["value"] = "external mutation"
	require.NoError(t, base.Update(
		incrementalTestResource("tenant-ns", "route", map[string]any{"tenant": "red", "value": "live"}),
		[]string{"red"},
	))
	current, err := base.Pin()
	require.NoError(t, err)
	rebased, err := rebaseIncrementalOverlaySnapshot(
		t.Context(),
		cfg.WatchedResources["routes"].IndexBy,
		current,
		snapshots.overlayChanges["routes"],
	)
	require.NoError(t, err)
	green, err = rebased.Get("green")
	require.NoError(t, err)
	assert.Equal(t, "new", green[0].(map[string]any)["spec"].(map[string]any)["value"])
	red, err := rebased.Get("red")
	require.NoError(t, err)
	assert.Empty(t, red)
}

func TestPinIncrementalStoreSnapshotsDeletionKeepsNonUniqueWinner(t *testing.T) {
	cfg := &config.Config{WatchedResources: map[string]config.WatchedResource{
		"routes": {IndexBy: []string{"spec.tenant"}},
	}}
	base := k8sstore.NewMemoryStore(1)
	require.NoError(t, base.Add(
		incrementalTestResource("tenant-ns", "deleted", map[string]any{"tenant": "blue"}),
		[]string{"blue"},
	))
	require.NoError(t, base.Add(
		incrementalTestResource("tenant-ns", "winner", map[string]any{"tenant": "blue"}),
		[]string{"blue"},
	))
	provider := stores.NewOverlayStoreProvider(
		stores.NewRealStoreProvider(map[string]stores.Store{"routes": base}),
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"routes": stores.NewStoreOverlayForDelete("tenant-ns", "deleted"),
		}),
	)

	snapshots, err := pinIncrementalStoreSnapshots(
		cfg,
		map[string]struct{}{"routes": {}},
		provider,
	)
	require.NoError(t, err)
	require.Len(t, snapshots.overlayChanges["routes"], 1)
	assert.Equal(t, []string{"blue"}, snapshots.overlayChanges["routes"][0].OldKeys)
	assert.Empty(t, snapshots.overlayChanges["routes"][0].NewKeys)

	blue, err := snapshots.render["routes"].Get("blue")
	require.NoError(t, err)
	require.Len(t, blue, 1)
	assert.Equal(t, "winner", blue[0].(map[string]any)["metadata"].(map[string]any)["name"])
	_, found, err := snapshots.render["routes"].GetIdentity("tenant-ns", "deleted")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestPinIncrementalStoreSnapshotsColdFallsBackForAnyAvailableUnsupportedStore(t *testing.T) {
	cfg := &config.Config{WatchedResources: map[string]config.WatchedResource{
		"routes":  {IndexBy: []string{"metadata.namespace", "metadata.name"}},
		"secrets": {IndexBy: []string{"metadata.namespace", "metadata.name"}},
	}}
	routes := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{
		"routes":  routes,
		"secrets": &storetest.MockStore{},
	})

	_, err := pinIncrementalStoreSnapshots(
		cfg,
		map[string]struct{}{"routes": {}, "secrets": {}},
		provider,
	)
	require.ErrorIs(t, err, errIncrementalUnsupported)

	_, err = pinIncrementalStoreSnapshots(
		cfg,
		map[string]struct{}{"routes": {}},
		provider,
	)
	require.ErrorIs(t, err, errIncrementalUnsupported)

	snapshots, err := pinIncrementalStoreSnapshots(
		cfg,
		map[string]struct{}{"routes": {}},
		stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes}),
	)
	require.NoError(t, err)
	assert.Contains(t, snapshots.render, "routes")
	assert.NotContains(t, snapshots.render, "secrets")

	_, err = pinIncrementalStoreSnapshots(
		cfg,
		map[string]struct{}{"routes": {}, "secrets": {}},
		stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes}),
	)
	require.ErrorIs(t, err, errIncrementalUnsupported)
}

func TestPinIncrementalStoreSnapshotsColdFallsBackForUnsupportedNonRequiredOverlay(t *testing.T) {
	cfg := &config.Config{WatchedResources: map[string]config.WatchedResource{
		"routes":  {IndexBy: []string{"metadata.namespace", "metadata.name"}},
		"secrets": {IndexBy: []string{"metadata.namespace", "metadata.name"}},
	}}
	routes := k8sstore.NewMemoryStore(2)
	secrets := &storetest.MockStore{}
	provider := stores.NewOverlayStoreProvider(
		stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes, "secrets": secrets}),
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"secrets": stores.NewStoreOverlayForCreate(&unstructured.Unstructured{
				Object: incrementalTestResource("default", "candidate", map[string]any{}),
			}),
		}),
	)

	_, err := pinIncrementalStoreSnapshots(
		cfg,
		map[string]struct{}{"routes": {}},
		provider,
	)
	require.ErrorIs(t, err, errIncrementalUnsupported)
}

type fixedRevisionJournal struct {
	current  uint64
	changes  []stores.RevisionChange
	complete bool
}

func (j *fixedRevisionJournal) ListSnapshot() (items []any, sequence uint64, err error) {
	return nil, j.current, nil
}

func (j *fixedRevisionJournal) ChangesSince(uint64) (uint64, []stores.RevisionChange, bool) {
	return j.current, j.changes, j.complete
}

func TestJournalChangesThroughStopsAtPinnedSequence(t *testing.T) {
	journal := &fixedRevisionJournal{
		current:  4,
		complete: true,
		changes: []stores.RevisionChange{
			{Sequence: 2, Name: "two"},
			{Sequence: 3, Name: "three"},
			{Sequence: 4, Name: "four"},
		},
	}

	changes, complete := journalChangesThrough(journal, 1, 3)
	require.True(t, complete)
	require.Len(t, changes, 2)
	assert.Equal(t, uint64(2), changes[0].Sequence)
	assert.Equal(t, uint64(3), changes[1].Sequence)

	journal.changes = journal.changes[1:]
	_, complete = journalChangesThrough(journal, 1, 3)
	assert.False(t, complete)
}

type mutateAfterPinStore struct {
	*k8sstore.MemoryStore
	once   sync.Once
	mutate func() error
	err    error
}

func (s *mutateAfterPinStore) Pin() (stores.ReadSnapshot, error) {
	snapshot, err := s.MemoryStore.Pin()
	if err != nil {
		return nil, err
	}
	s.once.Do(func() {
		s.err = s.mutate()
	})
	if s.err != nil {
		return nil, s.err
	}
	return snapshot, nil
}

func TestPinnedOrdinaryAndIncrementalReadsShareOneRoot(t *testing.T) {
	inner := k8sstore.NewMemoryStore(2)
	require.NoError(t, inner.Add(
		incrementalTestResource("default", "route", map[string]any{"value": "old"}),
		[]string{"default", "route"},
	))
	store := &mutateAfterPinStore{MemoryStore: inner}
	store.mutate = func() error {
		return store.Update(
			incrementalTestResource("default", "route", map[string]any{"value": "new"}),
			[]string{"default", "route"},
		)
	}
	cfg := &config.Config{WatchedResources: map[string]config.WatchedResource{
		"routes": {IndexBy: []string{"metadata.namespace", "metadata.name"}},
	}}
	snapshots, err := pinIncrementalStoreSnapshots(
		cfg,
		map[string]struct{}{"routes": {}},
		stores.NewRealStoreProvider(map[string]stores.Store{"routes": store}),
	)
	require.NoError(t, err)

	session := &incrementalRenderSession{
		baseStores:              snapshots.baseStores,
		baseSnapshots:           snapshots.base,
		renderSnapshots:         snapshots.render,
		membershipPins:          map[string]incrementalStoreCursor{},
		cursors:                 map[string]incrementalStoreCursor{},
		resourceProofs:          map[incremental.InputKey]incremental.Input{},
		commitAcceptsCandidates: true,
	}
	session.resetCatalog(nil)
	view := &incrementalPinnedResourceView{session: session}
	ordinary, err := view.Get("routes", store, "default", "route")
	require.NoError(t, err)
	require.Len(t, ordinary, 1)
	assert.Equal(t, "old", ordinary[0].(map[string]any)["spec"].(map[string]any)["value"])

	input, err := session.readResourceInput(snapshots.render["routes"], &resourceInputSpec{
		resourceType: "routes",
		scope:        resourceInputGet,
		keys:         []string{"default", "route"},
	})
	require.NoError(t, err)
	decoded, err := decodeResourceValue(input.Value)
	require.NoError(t, err)
	items := decoded.([]any)
	assert.Equal(t, "old", items[0].(map[string]any)["spec"].(map[string]any)["value"])

	verified, err := session.verifyResources(t.Context(), nil)
	require.NoError(t, err)
	assert.False(t, verified)
}

// The background builder publishes after the render, when an input has usually
// moved on a busy cluster; a memo entry records the revision it read and the
// next render invalidates it. Only a commit whose effect outlives the next
// render refuses.
func TestCachePublicationSurvivesAnInputMovingAfterThePin(t *testing.T) {
	inner := k8sstore.NewMemoryStore(2)
	require.NoError(t, inner.Add(
		incrementalTestResource("default", "route", map[string]any{"value": "old"}),
		[]string{"default", "route"},
	))
	store := &mutateAfterPinStore{MemoryStore: inner}
	store.mutate = func() error {
		return store.Update(
			incrementalTestResource("default", "route", map[string]any{"value": "new"}),
			[]string{"default", "route"},
		)
	}
	cfg := &config.Config{WatchedResources: map[string]config.WatchedResource{
		"routes": {IndexBy: []string{"metadata.namespace", "metadata.name"}},
	}}
	snapshots, err := pinIncrementalStoreSnapshots(
		cfg,
		map[string]struct{}{"routes": {}},
		stores.NewRealStoreProvider(map[string]stores.Store{"routes": store}),
	)
	require.NoError(t, err)

	session := &incrementalRenderSession{
		baseStores:              snapshots.baseStores,
		baseSnapshots:           snapshots.base,
		renderSnapshots:         snapshots.render,
		membershipPins:          map[string]incrementalStoreCursor{},
		cursors:                 map[string]incrementalStoreCursor{},
		resourceProofs:          map[incremental.InputKey]incremental.Input{},
		commitAcceptsCandidates: true,
	}
	session.resetCatalog(nil)
	view := &incrementalPinnedResourceView{session: session}
	_, err = view.Get("routes", store, "default", "route")
	require.NoError(t, err)

	strict, err := session.verifyResources(t.Context(), nil)
	require.NoError(t, err)
	require.False(t, strict, "a commit accepting fetched content must still refuse a moved input")

	session.commitAcceptsCandidates = false
	session.renderMode = rendercontext.RenderModeAdmission
	admission, err := session.verifyResources(t.Context(), nil)
	require.NoError(t, err)
	require.False(t, admission, "an admission verdict must still refuse a moved input")

	session.renderMode = rendercontext.RenderModeReconcile
	plain, err := session.verifyResources(t.Context(), nil)
	require.NoError(t, err)
	require.True(t, plain, "a reconcile commit publishes at the cursor it pinned")

	cachePublishable, err := session.verifyCachePublicationResources(t.Context(), nil)
	require.NoError(t, err)
	assert.True(t, cachePublishable, "the cache must publish a memo whose inputs moved after the pin")
}
