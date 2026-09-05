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
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type reorderedExactCycleSnapshot struct {
	stores.ReadSnapshot
	sequence uint64
}

func (s *reorderedExactCycleSnapshot) Sequence() uint64 {
	if s.sequence != 0 {
		return s.sequence
	}
	return s.ReadSnapshot.Sequence()
}

func (s *reorderedExactCycleSnapshot) List() ([]any, error) {
	items, err := s.ReadSnapshot.List()
	if err == nil {
		slices.Reverse(items)
	}
	return items, err
}

func (s *reorderedExactCycleSnapshot) Get(keys ...string) ([]any, error) {
	items, err := s.ReadSnapshot.Get(keys...)
	if err == nil {
		slices.Reverse(items)
	}
	return items, err
}

type reorderedExactCycleJournal struct {
	stores.Store
	source   stores.RevisionSource
	current  uint64
	changes  []stores.RevisionChange
	complete bool
}

func (s *reorderedExactCycleJournal) ListSnapshot() (items []any, revision uint64, err error) {
	items, err = s.List()
	return items, s.current, err
}

func (s *reorderedExactCycleJournal) ChangesSince(uint64) (uint64, []stores.RevisionChange, bool) {
	return s.current, slices.Clone(s.changes), s.complete
}

func (s *reorderedExactCycleJournal) ExactRevisionJournalSource() stores.RevisionSource {
	return s.source
}

func TestExactCycleListObservationUsesObservableOrder(t *testing.T) {
	store := k8sstore.NewMemoryStore(2)
	for _, name := range []string{"first", "second"} {
		require.NoError(t, store.Add(
			lateResourceProofValue(name, "stable"),
			[]string{"blue", name},
		))
	}
	original, err := store.Pin()
	require.NoError(t, err)
	spec := resourceInputSpec{resourceType: "resources", scope: resourceInputList}
	input, err := readResourceSnapshotInput(t.Context(), original, &spec)
	require.NoError(t, err)
	session := &incrementalRenderSession{
		baseStores:      map[string]stores.Store{"resources": store},
		renderSnapshots: map[string]stores.ReadSnapshot{"resources": original},
		rootResourceProofs: map[incremental.InputKey]incremental.InputRevision{
			input.Key: {Key: input.Key, Revision: input.Revision, Found: input.Found},
		},
		cachePublicationEnabled: true,
	}
	observations, err := session.captureExactCycleResourceObservations()
	require.NoError(t, err)
	require.NotNil(t, observations)

	require.NoError(t, store.Update(
		lateResourceProofValue("first", "stable"),
		[]string{"green", "first"},
	))
	current, err := store.Pin()
	require.NoError(t, err)
	session.renderSnapshots["resources"] = current

	matched, err := observations.matches(t.Context(), session)
	require.NoError(t, err)
	require.True(t, matched)

	session.renderSnapshots["resources"] = &reorderedExactCycleSnapshot{ReadSnapshot: current}
	matched, err = observations.matches(t.Context(), session)
	require.NoError(t, err)
	require.False(t, matched)
}

func TestExactCycleCollectionObservationDetectsSameKeyReorder(t *testing.T) {
	for _, test := range []struct {
		name string
		spec resourceInputSpec
	}{
		{name: "list", spec: resourceInputSpec{resourceType: "resources", scope: resourceInputList}},
		{name: "get", spec: resourceInputSpec{
			resourceType: "resources", scope: resourceInputGet, keys: []string{"blue"},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := test.spec
			store := k8sstore.NewMemoryStore(2)
			for _, name := range []string{"first", "second"} {
				require.NoError(t, store.Add(
					lateResourceProofValue(name, "stable"),
					[]string{"blue", name},
				))
			}
			original, err := store.Pin()
			require.NoError(t, err)
			input, err := readResourceSnapshotInput(t.Context(), original, &spec)
			require.NoError(t, err)
			journal := &reorderedExactCycleJournal{
				Store: store, source: original.RevisionSource(), current: original.Sequence() + 1, complete: true,
				changes: []stores.RevisionChange{{
					Sequence:  original.Sequence() + 1,
					Namespace: "default", Name: "first",
					OldKeys: []string{"blue", "first"}, NewKeys: []string{"blue", "first"},
				}},
			}
			session := &incrementalRenderSession{
				baseStores:      map[string]stores.Store{"resources": journal},
				renderSnapshots: map[string]stores.ReadSnapshot{"resources": original},
				rootResourceProofs: map[incremental.InputKey]incremental.InputRevision{
					input.Key: {Key: input.Key, Revision: input.Revision, Found: input.Found},
				},
				cachePublicationEnabled: true,
			}
			observations, err := session.captureExactCycleResourceObservations()
			require.NoError(t, err)
			require.NotNil(t, observations)

			session.renderSnapshots["resources"] = &reorderedExactCycleSnapshot{
				ReadSnapshot: original, sequence: journal.current,
			}
			matched, err := observations.matches(t.Context(), session)
			require.NoError(t, err)
			require.False(t, matched)
		})
	}
}

func TestExactCycleLegacySharedRequiresEveryStoreRoot(t *testing.T) {
	observed := k8sstore.NewMemoryStore(1)
	unobserved := k8sstore.NewMemoryStore(1)
	require.NoError(t, observed.Add(lateResourceProofValue("observed", "stable"), []string{"observed"}))
	require.NoError(t, unobserved.Add(lateResourceProofValue("unobserved", "stable"), []string{"unobserved"}))
	observedRoot, err := observed.Pin()
	require.NoError(t, err)
	unobservedRoot, err := unobserved.Pin()
	require.NoError(t, err)
	session := &incrementalRenderSession{
		renderSnapshots: map[string]stores.ReadSnapshot{
			"observed": observedRoot, "unobserved": unobservedRoot,
		},
		cachePublicationEnabled: true,
	}
	roots, err := session.captureExactCycleStoreRoots()
	require.NoError(t, err)
	require.NotNil(t, roots)

	matched, err := roots.matches(session)
	require.NoError(t, err)
	require.True(t, matched)

	require.NoError(t, unobserved.Update(
		lateResourceProofValue("unobserved", "changed"), []string{"unobserved"},
	))
	unobservedRoot, err = unobserved.Pin()
	require.NoError(t, err)
	session.renderSnapshots["unobserved"] = unobservedRoot
	matched, err = roots.matches(session)
	require.NoError(t, err)
	require.False(t, matched)

	delete(session.renderSnapshots, "unobserved")
	matched, err = roots.matches(session)
	require.NoError(t, err)
	require.False(t, matched)

	roots.rootsLen++
	_, err = roots.matches(session)
	require.ErrorContains(t, err, "invalid provenance")
}
