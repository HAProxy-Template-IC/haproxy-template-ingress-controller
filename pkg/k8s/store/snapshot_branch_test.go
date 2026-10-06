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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
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
