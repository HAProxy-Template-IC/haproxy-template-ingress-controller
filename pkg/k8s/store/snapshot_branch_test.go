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

package store

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/haproxy-haptic/HAPTIC/pkg/stores"
)

func TestSnapshotBranchRetainsSelectedRevisionsAndExactJournal(t *testing.T) {
	live := NewMemoryStore(2)
	first := cachedSnapshotResource("default", "first", "1", "accepted")
	second := cachedSnapshotResource("default", "second", "1", "before")
	require.NoError(t, live.Add(first, []string{"default", "first"}))
	require.NoError(t, live.Add(second, []string{"default", "second"}))
	base := NewSnapshotBranch(2)
	snapshot, err := live.Pin()
	require.NoError(t, err)
	changes, err := base.Changes(t.Context(), snapshot, []string{"metadata.namespace", "metadata.name"})
	require.NoError(t, err)
	accepted := base.Apply(changes)

	first.SetResourceVersion("2")
	first.Object["data"] = map[string]any{"key": "rejected"}
	second.SetResourceVersion("2")
	second.Object["data"] = map[string]any{"key": "after"}
	require.NoError(t, live.Update(first, []string{"default", "first"}))
	require.NoError(t, live.Update(second, []string{"default", "second"}))
	snapshot, err = live.Pin()
	require.NoError(t, err)
	changes, err = accepted.Changes(t.Context(), snapshot, []string{"metadata.namespace", "metadata.name"})
	require.NoError(t, err)
	require.Len(t, changes, 2)
	candidate := accepted.Apply(changes)
	healthy := accepted.Apply(changes[1:])
	items, err := healthy.Get("default", "first")
	require.NoError(t, err)
	assert.Equal(t, "accepted", cachedSnapshotDataValue(t, items[0]))
	items, err = healthy.Get("default", "second")
	require.NoError(t, err)
	assert.Equal(t, "after", cachedSnapshotDataValue(t, items[0]))
	assert.Equal(t, accepted.GetRevision("default", "first"), healthy.GetRevision("default", "first"))
	assert.NotEqual(t, accepted.GetRevision("default", "second"), healthy.GetRevision("default", "second"))
	_, journal, complete := healthy.ChangesSince(accepted.Sequence())
	require.True(t, complete)
	require.Len(t, journal, 1)
	assert.Equal(t, "second", journal[0].Name)
	_, _, complete = healthy.ChangesSince(candidate.Sequence())
	assert.False(t, complete, "a sibling trial is not an ancestor")
	setCachedSnapshotValue(t, items[0], "poison")
	items, err = healthy.Get("default", "second")
	require.NoError(t, err)
	assert.Equal(t, "after", cachedSnapshotDataValue(t, items[0]))
}

func TestSnapshotBranchPreservesValidatedLazyInputAfterRotation(t *testing.T) {
	resource := cachedSnapshotResource("default", "target", "1", "accepted")
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), resource)
	var gets atomic.Int32
	client.PrependReactor("get", "configmaps", countCachedSnapshotGets(&gets))
	live := newProjectedSnapshotStore(t, client)
	require.NoError(t, live.Add(cachedSnapshotRef(resource), []string{"default", "target"}))
	snapshot, err := live.Pin()
	require.NoError(t, err)
	base := NewSnapshotBranch(2)
	changes, err := base.Changes(t.Context(), snapshot, []string{"metadata.namespace", "metadata.name"})
	require.NoError(t, err)
	accepted := base.Apply(changes)
	diff, complete, err := accepted.ChangesFrom(t.Context(), base)
	require.NoError(t, err)
	require.True(t, complete)
	require.Len(t, diff, 1)
	assert.Zero(t, gets.Load(), "capturing and comparing references must not fetch their bodies")
	items, err := accepted.GetContext(t.Context(), "default", "target")
	require.NoError(t, err)
	assert.Equal(t, "accepted", cachedSnapshotDataValue(t, items[0]))
	assert.EqualValues(t, 1, gets.Load())

	resource.SetResourceVersion("2")
	resource.Object["data"] = map[string]any{"key": "rejected"}
	_, err = client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}).Namespace("default").Update(t.Context(), resource, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.NoError(t, live.Update(cachedSnapshotRef(resource), []string{"default", "target"}))
	snapshot, err = live.Pin()
	require.NoError(t, err)
	changes, err = accepted.Changes(t.Context(), snapshot, []string{"metadata.namespace", "metadata.name"})
	require.NoError(t, err)
	candidate := accepted.Apply(changes)
	items, err = candidate.GetContext(t.Context(), "default", "target")
	require.NoError(t, err)
	assert.Equal(t, "rejected", cachedSnapshotDataValue(t, items[0]))
	items, err = accepted.GetContext(t.Context(), "default", "target")
	require.NoError(t, err)
	assert.Equal(t, "accepted", cachedSnapshotDataValue(t, items[0]))
	assert.EqualValues(t, 2, gets.Load(), "the validated value must not be fetched again")
}

func TestSnapshotBranchRemovesDeletedIdentityAndIndexState(t *testing.T) {
	live := NewMemoryStore(2)
	value := cachedSnapshotResource("default", "target", "1", "accepted")
	require.NoError(t, live.Add(value, []string{"default", "target"}))
	snapshot, err := live.Pin()
	require.NoError(t, err)
	base := NewSnapshotBranch(2)
	absent := base.IdentityRevision("default", "target")
	changes, err := base.Changes(t.Context(), snapshot, []string{"metadata.namespace", "metadata.name"})
	require.NoError(t, err)
	accepted := base.Apply(changes)
	require.NoError(t, live.Delete("default", "target", []string{"default", "target"}))
	snapshot, err = live.Pin()
	require.NoError(t, err)
	changes, err = accepted.Changes(t.Context(), snapshot, []string{"metadata.namespace", "metadata.name"})
	require.NoError(t, err)
	deleted := accepted.Apply(changes)
	require.Zero(t, deleted.keys.Len(), "deleted index keys must not accumulate across endpoint churn")
	require.Zero(t, deleted.identities.Len(), "deleted identities must not accumulate")
	require.Equal(t, absent, deleted.IdentityRevision("default", "target"))
	_, journal, complete := deleted.ChangesSince(accepted.Sequence())
	require.True(t, complete)
	require.Len(t, journal, 1)
	require.True(t, journal[0].Deleted)
	_, supported, err := ProjectImmutableSnapshotGet(t.Context(), deleted)
	require.True(t, supported)
	require.Error(t, err)
}

func TestSnapshotBranchRetriesTransientLazyReadFailure(t *testing.T) {
	resource := cachedSnapshotResource("default", "target", "1", "accepted")
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), resource)
	var gets atomic.Int32
	client.PrependReactor("get", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		if gets.Add(1) == 1 {
			return true, nil, errors.New("API temporarily unavailable")
		}
		return false, nil, nil
	})
	live := newProjectedSnapshotStore(t, client)
	require.NoError(t, live.Add(cachedSnapshotRef(resource), []string{"default", "target"}))
	snapshot, err := live.Pin()
	require.NoError(t, err)
	base := NewSnapshotBranch(2)
	changes, err := base.Changes(t.Context(), snapshot, []string{"metadata.namespace", "metadata.name"})
	require.NoError(t, err)
	retained := base.Apply(changes)
	_, err = retained.GetContext(t.Context(), "default", "target")
	require.ErrorContains(t, err, "temporarily unavailable")
	items, err := retained.GetContext(t.Context(), "default", "target")
	require.NoError(t, err)
	require.Equal(t, "accepted", cachedSnapshotDataValue(t, items[0]))
	require.EqualValues(t, 2, gets.Load())
}

func TestSnapshotBranchRepeatedCapturePreservesRejectedChanges(t *testing.T) {
	for _, backend := range []string{"memory", "cached"} {
		t.Run(backend, func(t *testing.T) {
			live := newBranchCaptureStore(t, backend)
			capture := func(base *SnapshotBranch) []BranchChange {
				snapshot, err := live.Pin()
				require.NoError(t, err)
				changes, err := base.Changes(t.Context(), snapshot, []string{"metadata.namespace", "metadata.name"})
				require.NoError(t, err)
				return changes
			}
			for _, name := range []string{"changed", "deleted", "healthy", "stable"} {
				require.NoError(t, live.Add(cachedSnapshotResource("default", name, "1", "before"), []string{"default", name}))
			}
			base := NewSnapshotBranch(2)
			base = base.Apply(capture(base))
			for _, name := range []string{"changed", "healthy"} {
				require.NoError(t, live.Update(cachedSnapshotResource("default", name, "2", "after"), []string{"default", name}))
			}
			require.NoError(t, live.Delete("default", "deleted", []string{"default", "deleted"}))
			changes := capture(base)
			require.Len(t, changes, 3)
			require.Equal(t, "healthy", changes[2].Name())
			accepted := base.Apply(changes[2:])
			for range 3 {
				pending := capture(accepted)
				require.Len(t, pending, 2)
				require.Equal(t, "changed", pending[0].Name())
				require.False(t, pending[0].Deleted())
				require.Equal(t, "deleted", pending[1].Name())
				require.True(t, pending[1].Deleted())
			}
			complete := accepted.Apply(capture(accepted))
			require.Empty(t, capture(complete))
			require.Len(t, capture(base), 3, "a sibling trial must not advance the accepted baseline")
		})
	}
}

func TestSnapshotBranchCaptureDistinguishesStoreSources(t *testing.T) {
	for _, backend := range []string{"memory", "cached"} {
		t.Run(backend, func(t *testing.T) {
			base := NewSnapshotBranch(2)
			var oldRevision stores.Revision
			for _, value := range []string{"first", "replacement"} {
				live := newBranchCaptureStore(t, backend)
				require.NoError(t, live.Add(cachedSnapshotResource("default", "target", "1", value), []string{"default", "target"}))
				snapshot, err := live.Pin()
				require.NoError(t, err)
				changes, err := base.Changes(t.Context(), snapshot, []string{"metadata.namespace", "metadata.name"})
				require.NoError(t, err)
				require.Len(t, changes, 1, "equal sequence numbers from different stores are different inputs")
				require.NotEqual(t, oldRevision, changes[0].Revision())
				oldRevision = changes[0].Revision()
				base = base.Apply(changes)
			}
		})
	}
}

type branchCaptureStore interface {
	stores.SnapshotProvider
	Add(any, []string) error
	Update(any, []string) error
	Delete(string, string, []string) error
}

func newBranchCaptureStore(t *testing.T, backend string) branchCaptureStore {
	t.Helper()
	if backend == "memory" {
		return NewMemoryStore(2)
	}
	client := fake.NewSimpleDynamicClient(runtime.NewScheme())
	client.PrependReactor("get", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		t.Error("capturing revisions must not fetch resource bodies")
		return true, nil, errors.New("unexpected body fetch")
	})
	return newProjectedSnapshotStore(t, client)
}

func TestSnapshotBranchCaptureInspectsOnlyJournaledIdentities(t *testing.T) {
	indexBy := []string{"metadata.namespace", "metadata.name"}
	for _, backend := range []string{"memory", "cached"} {
		t.Run(backend, func(t *testing.T) {
			live := newBranchCaptureStore(t, backend)
			for _, name := range []string{"a", "b", "c"} {
				require.NoError(t, live.Add(cachedSnapshotResource("default", name, "1", "before"), []string{"default", name}))
			}
			capture := func(base *SnapshotBranch) []BranchChange {
				snapshot, err := live.Pin()
				require.NoError(t, err)
				changes, err := base.Changes(t.Context(), snapshot, indexBy)
				require.NoError(t, err)
				return changes
			}
			base := NewSnapshotBranch(2)
			accepted := base.Apply(capture(base))
			require.Empty(t, capture(accepted))
			_, incremental := accepted.captureCandidates(mustPin(t, live), indexBy)
			require.True(t, incremental, "a captured branch must not rescan the watched set")

			require.NoError(t, live.Update(cachedSnapshotResource("default", "a", "2", "after"), []string{"default", "a"}))
			require.NoError(t, live.Delete("default", "b", []string{"default", "b"}))
			require.NoError(t, live.Add(cachedSnapshotResource("default", "d", "1", "new"), []string{"default", "d"}))
			candidates, incremental := accepted.captureCandidates(mustPin(t, live), indexBy)
			require.True(t, incremental)
			assert.Len(t, candidates, 3)

			changes := capture(accepted)
			require.Len(t, changes, 3)
			assert.Equal(t, []string{"a", "b", "d"}, []string{changes[0].Name(), changes[1].Name(), changes[2].Name()})
			assert.True(t, changes[1].Deleted())
			partial := accepted.Apply(changes[:1])
			pending := capture(partial)
			require.Len(t, pending, 2, "unaccepted changes stay pending without a rescan")
			assert.Equal(t, "b", pending[0].Name())
			assert.Equal(t, "d", pending[1].Name())
			assert.Empty(t, capture(partial.Apply(pending)))
		})
	}
}

func TestSnapshotBranchCaptureRescansWhenJournalIsIncomplete(t *testing.T) {
	indexBy := []string{"metadata.namespace", "metadata.name"}
	live := NewMemoryStore(2)
	require.NoError(t, live.Add(cachedSnapshotResource("default", "target", "1", "before"), []string{"default", "target"}))
	base := NewSnapshotBranch(2)
	changes, err := base.Changes(t.Context(), mustPin(t, live), indexBy)
	require.NoError(t, err)
	accepted := base.Apply(changes)
	_, err = accepted.Changes(t.Context(), mustPin(t, live), indexBy)
	require.NoError(t, err)
	for revision := range defaultRevisionJournalCapacity + 1 {
		value := cachedSnapshotResource("default", "target", fmt.Sprint(revision+2), "after")
		require.NoError(t, live.Update(value, []string{"default", "target"}))
	}
	snapshot := mustPin(t, live)
	_, incremental := accepted.captureCandidates(snapshot, indexBy)
	require.False(t, incremental)
	changes, err = accepted.Changes(t.Context(), snapshot, indexBy)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	_, incremental = accepted.captureCandidates(snapshot, []string{"metadata.name", "metadata.namespace"})
	assert.False(t, incremental, "a different index configuration needs a full scan")
}

func mustPin(t *testing.T, live stores.SnapshotProvider) stores.ReadSnapshot {
	t.Helper()
	snapshot, err := live.Pin()
	require.NoError(t, err)
	return snapshot
}
