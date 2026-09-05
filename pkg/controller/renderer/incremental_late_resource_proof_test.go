// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type lateResourceProofReadCounts struct {
	list     atomic.Uint64
	get      atomic.Uint64
	identity atomic.Uint64
}

func (c *lateResourceProofReadCounts) reset() {
	c.list.Store(0)
	c.get.Store(0)
	c.identity.Store(0)
}

type lateResourceProofStore struct {
	*k8sstore.MemoryStore
	reads *lateResourceProofReadCounts
}

func (s *lateResourceProofStore) Pin() (stores.ReadSnapshot, error) {
	snapshot, err := s.MemoryStore.Pin()
	if err != nil {
		return nil, err
	}
	return &lateResourceProofSnapshot{ReadSnapshot: snapshot, reads: s.reads}, nil
}

type lateResourceProofSnapshot struct {
	stores.ReadSnapshot
	reads *lateResourceProofReadCounts
}

func (s *lateResourceProofSnapshot) List() ([]any, error) {
	s.reads.list.Add(1)
	return s.ReadSnapshot.List()
}

func (s *lateResourceProofSnapshot) Get(keys ...string) ([]any, error) {
	s.reads.get.Add(1)
	return s.ReadSnapshot.Get(keys...)
}

func (s *lateResourceProofSnapshot) GetIdentity(
	namespace, name string,
) (item any, found bool, err error) {
	s.reads.identity.Add(1)
	return s.ReadSnapshot.GetIdentity(namespace, name)
}

func TestLateResourceCollectionProofIsIndependentOfStoreSize(t *testing.T) {
	for _, count := range []int{1, 1000, 10000} {
		for _, scope := range []resourceInputScope{resourceInputList, resourceInputGet} {
			t.Run(fmt.Sprintf("%d/%s", count, scope), func(t *testing.T) {
				store, target := newLateResourceProofStore(t, count)
				original, err := store.Pin()
				require.NoError(t, err)
				spec := resourceInputSpec{resourceType: "resources", scope: scope}
				if scope == resourceInputGet {
					spec.keys = []string{"blue"}
				}
				expected, err := readResourceSnapshotInput(t.Context(), original, &spec)
				require.NoError(t, err)

				deleteLateResourceProofTarget(t, store.MemoryStore, target)
				addLateResourceProofTarget(t, store.MemoryStore, target, "stable")
				assertLateResourceProof(t, store, original, &spec, expected, true)

				require.NoError(t, store.Update(
					lateResourceProofValue(target, "changed"),
					[]string{"blue", target},
				))
				assertLateResourceProof(t, store, original, &spec, expected, false)
			})
		}
	}
}

func TestLateOverlayMembershipProofIsIndependentOfStoreSize(t *testing.T) {
	for _, count := range []int{1, 1000, 10000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			store, target := newLateResourceProofStore(t, count)
			original, err := store.Pin()
			require.NoError(t, err)
			overlayChange := stores.SnapshotChange{
				Namespace: "default",
				Name:      "proposed",
				Value:     lateResourceProofValue("proposed", "overlay"),
				NewKeys:   []string{"blue", "proposed"},
			}
			originalOverlay, err := stores.OverlayReadSnapshot(original, []stores.SnapshotChange{overlayChange})
			require.NoError(t, err)

			require.NoError(t, store.Update(
				lateResourceProofValue(target, "changed"),
				[]string{"blue", target},
			))
			current, err := store.Pin()
			require.NoError(t, err)
			currentOverlay, err := stores.OverlayReadSnapshot(current, []stores.SnapshotChange{overlayChange})
			require.NoError(t, err)
			changes, complete := journalChangesThrough(store, original.Sequence(), current.Sequence())
			require.True(t, complete)
			deltas, exact := newResourceIdentityDeltas(changes)
			require.True(t, exact)

			store.reads.reset()
			verified, err := sameChangedResourceMembership(
				t.Context(), originalOverlay, currentOverlay, deltas,
			)
			require.NoError(t, err)
			require.True(t, verified)
			require.Zero(t, store.reads.list.Load())
			require.Zero(t, store.reads.get.Load())
			require.Equal(t, uint64(2), store.reads.identity.Load())

			require.NoError(t, store.Delete("default", target, []string{"blue", target}))
			deleted, err := store.Pin()
			require.NoError(t, err)
			deletedOverlay, err := stores.OverlayReadSnapshot(deleted, []stores.SnapshotChange{overlayChange})
			require.NoError(t, err)
			changes, complete = journalChangesThrough(store, original.Sequence(), deleted.Sequence())
			require.True(t, complete)
			deltas, exact = newResourceIdentityDeltas(changes)
			require.True(t, exact)

			store.reads.reset()
			verified, err = sameChangedResourceMembership(
				t.Context(), originalOverlay, deletedOverlay, deltas,
			)
			require.NoError(t, err)
			require.False(t, verified)
			require.Zero(t, store.reads.list.Load())
			require.Zero(t, store.reads.get.Load())
			require.Equal(t, uint64(2), store.reads.identity.Load())
		})
	}
}

func TestLateOverlayMembershipProofHonorsMaskedBaseChanges(t *testing.T) {
	store, _ := newLateResourceProofStore(t, 1000)
	original, err := store.Pin()
	require.NoError(t, err)
	proposed := lateResourceProofValue("proposed", "overlay")
	originalOverlay, err := stores.OverlayReadSnapshot(original, []stores.SnapshotChange{{
		Namespace: "default", Name: "proposed", Value: proposed,
		NewKeys: []string{"blue", "proposed"},
	}})
	require.NoError(t, err)
	require.NoError(t, store.Add(proposed, []string{"blue", "proposed"}))
	current, err := store.Pin()
	require.NoError(t, err)
	currentOverlay, err := stores.OverlayReadSnapshot(current, []stores.SnapshotChange{{
		Namespace: "default", Name: "proposed", Value: proposed,
		OldKeys: []string{"blue", "proposed"}, NewKeys: []string{"blue", "proposed"},
	}})
	require.NoError(t, err)
	changes, complete := journalChangesThrough(store, original.Sequence(), current.Sequence())
	require.True(t, complete)
	deltas, exact := newResourceIdentityDeltas(changes)
	require.True(t, exact)

	store.reads.reset()
	verified, err := sameChangedResourceMembership(t.Context(), originalOverlay, currentOverlay, deltas)
	require.NoError(t, err)
	require.True(t, verified)
	require.Zero(t, store.reads.list.Load())
	require.Zero(t, store.reads.get.Load())
	require.Equal(t, uint64(1), store.reads.identity.Load())
}

func TestResourceProofVerificationAuthenticatesBorrowedGraphInput(t *testing.T) {
	store, target := newLateResourceProofStore(t, 1)
	original, err := store.Pin()
	require.NoError(t, err)
	spec := resourceInputSpec{
		resourceType: "resources",
		scope:        resourceInputIdentity,
		namespace:    "default",
		name:         target,
	}
	expected, err := readResourceSnapshotInput(t.Context(), original, &spec)
	require.NoError(t, err)
	graph, err := incremental.New()
	require.NoError(t, err)
	graphSession, err := graph.Begin()
	require.NoError(t, err)
	t.Cleanup(graphSession.Abort)
	require.NoError(t, graphSession.ApplyInputs(expected))

	session := &incrementalRenderSession{
		graphSession:    graphSession,
		baseStores:      map[string]stores.Store{"resources": store},
		baseSnapshots:   map[string]stores.ReadSnapshot{"resources": original},
		renderSnapshots: map[string]stores.ReadSnapshot{"resources": original},
		cursors: map[string]incrementalStoreCursor{
			"resources": {source: original.RevisionSource(), sequence: original.Sequence()},
		},
		membershipPins:          map[string]incrementalStoreCursor{},
		resourceProofs:          map[incremental.InputKey]incremental.Input{},
		cachePublicationEnabled: true,
	}
	session.resetCatalog(nil)
	require.NoError(t, session.observeResourceProof(expected))
	expected.Value[0] ^= 0xff
	observed := []incremental.InputRevision{{
		Key: expected.Key, Revision: expected.Revision, Found: expected.Found,
	}}
	verified, err := session.verifyResourceInputs(t.Context(), observed, true)
	require.NoError(t, err)
	require.True(t, verified)

	poisoned := session.resourceProofs[expected.Key]
	poisoned.Value = []byte(`{"poison":true}`)
	session.resourceProofs[expected.Key] = poisoned
	verified, err = session.verifyResourceInputs(t.Context(), observed, true)
	require.NoError(t, err)
	require.False(t, verified)
}

func assertLateResourceProof(
	t *testing.T,
	store *lateResourceProofStore,
	original stores.ReadSnapshot,
	spec *resourceInputSpec,
	expected incremental.Input,
	want bool,
) {
	t.Helper()
	store.reads.reset()
	session := &incrementalRenderSession{
		baseStores:      map[string]stores.Store{"resources": store},
		baseSnapshots:   map[string]stores.ReadSnapshot{"resources": original},
		renderSnapshots: map[string]stores.ReadSnapshot{"resources": original},
		cursors: map[string]incrementalStoreCursor{
			"resources": {source: original.RevisionSource(), sequence: original.Sequence()},
		},
		membershipPins:          map[string]incrementalStoreCursor{},
		resourceProofs:          map[incremental.InputKey]incremental.Input{expected.Key: expected},
		cachePublicationEnabled: true,
	}
	session.resetCatalog(nil)
	verified, err := session.verifyResourceInputs(t.Context(), nil, true)
	require.NoError(t, err)
	require.Equal(t, want, verified)
	require.Zero(t, store.reads.list.Load())
	require.Zero(t, store.reads.get.Load())
	require.Equal(t, uint64(2), store.reads.identity.Load())

	if want {
		current, err := store.Pin()
		require.NoError(t, err)
		actual, err := readResourceSnapshotInput(t.Context(), current, spec)
		require.NoError(t, err)
		require.True(t, sameIncrementalInput(expected, actual))
	}
}

func newLateResourceProofStore(tb testing.TB, count int) (store *lateResourceProofStore, target string) {
	tb.Helper()
	store = &lateResourceProofStore{
		MemoryStore: k8sstore.NewMemoryStore(2),
		reads:       &lateResourceProofReadCounts{},
	}
	for index := range count {
		name := fmt.Sprintf("resource-%08d", index)
		require.NoError(tb, store.Add(
			lateResourceProofValue(name, "stable"),
			[]string{"blue", name},
		))
	}
	return store, fmt.Sprintf("resource-%08d", count-1)
}

func lateResourceProofValue(name, value string) map[string]any {
	return incrementalTestResource("default", name, map[string]any{"value": value})
}

func deleteLateResourceProofTarget(tb testing.TB, store *k8sstore.MemoryStore, name string) {
	tb.Helper()
	require.NoError(tb, store.Delete("default", name, []string{"blue", name}))
}

func addLateResourceProofTarget(tb testing.TB, store *k8sstore.MemoryStore, name, value string) {
	tb.Helper()
	require.NoError(tb, store.Add(lateResourceProofValue(name, value), []string{"blue", name}))
}

func BenchmarkLateResourceCollectionProof(b *testing.B) {
	for _, count := range []int{1, 1000, 10000} {
		for _, scope := range []resourceInputScope{resourceInputList, resourceInputGet} {
			b.Run(fmt.Sprintf("%d/%s", count, scope), func(b *testing.B) {
				benchmarkLateResourceCollectionProof(b, count, scope)
			})
		}
	}
}

func BenchmarkLateOverlayMembershipProof(b *testing.B) {
	for _, count := range []int{1, 1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			store, target := newLateResourceProofStore(b, count)
			original, err := store.Pin()
			require.NoError(b, err)
			overlayChange := stores.SnapshotChange{
				Namespace: "default", Name: "proposed",
				Value:   lateResourceProofValue("proposed", "overlay"),
				NewKeys: []string{"blue", "proposed"},
			}
			originalOverlay, err := stores.OverlayReadSnapshot(original, []stores.SnapshotChange{overlayChange})
			require.NoError(b, err)
			require.NoError(b, store.Update(
				lateResourceProofValue(target, "changed"), []string{"blue", target},
			))
			current, err := store.Pin()
			require.NoError(b, err)
			currentOverlay, err := stores.OverlayReadSnapshot(current, []stores.SnapshotChange{overlayChange})
			require.NoError(b, err)
			changes, complete := journalChangesThrough(store, original.Sequence(), current.Sequence())
			require.True(b, complete)
			deltas, exact := newResourceIdentityDeltas(changes)
			require.True(b, exact)

			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				verified, proofErr := sameChangedResourceMembership(
					ctx, originalOverlay, currentOverlay, deltas,
				)
				if proofErr != nil || !verified {
					b.Fatalf("late overlay membership proof = %v, %v", verified, proofErr)
				}
			}
		})
	}
}

func benchmarkLateResourceCollectionProof(b *testing.B, count int, scope resourceInputScope) {
	b.Helper()
	store, target := newLateResourceProofStore(b, count)
	original, err := store.MemoryStore.Pin()
	require.NoError(b, err)
	deleteLateResourceProofTarget(b, store.MemoryStore, target)
	addLateResourceProofTarget(b, store.MemoryStore, target, "stable")
	current, err := store.MemoryStore.Pin()
	require.NoError(b, err)
	changes, complete := journalChangesThrough(store, original.Sequence(), current.Sequence())
	require.True(b, complete)
	spec := resourceInputSpec{resourceType: "resources", scope: scope}
	if scope == resourceInputGet {
		spec.keys = []string{"blue"}
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		deltas, exact := newResourceIdentityDeltas(changes)
		if !exact {
			b.Fatal("late resource delta is inexact")
		}
		same, proofErr := deltas.sameScopeSemantics(ctx, original, current, &spec)
		if proofErr != nil || !same {
			b.Fatalf("late resource proof = %v, %v", same, proofErr)
		}
	}
}
