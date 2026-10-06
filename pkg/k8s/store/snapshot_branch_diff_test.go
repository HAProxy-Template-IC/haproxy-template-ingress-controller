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
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestSnapshotBranchDifferencesAcrossDivergentRoots(t *testing.T) {
	live := NewMemoryStore(2)
	capture := func(base *SnapshotBranch) []BranchChange {
		pinned, err := live.Pin()
		require.NoError(t, err)
		changes, err := base.Changes(t.Context(), pinned, []string{"metadata.namespace", "data.key"})
		require.NoError(t, err)
		return changes
	}
	for _, name := range []string{"changed", "removed", "stable"} {
		require.NoError(t, live.Add(cachedSnapshotResource("default", name, "1", "old"), []string{"default", "old"}))
	}
	empty := NewSnapshotBranch(2)
	base := empty.Apply(capture(empty))
	require.NoError(t, live.Update(cachedSnapshotResource("default", "changed", "2", "new"), []string{"default", "new"}))
	require.NoError(t, live.Add(cachedSnapshotResource("default", "added", "1", "new"), []string{"default", "new"}))
	changes := capture(base)
	sibling := base.Apply(changes)
	sameValues := base.Apply(changes)
	diff, complete, err := sameValues.ChangesFrom(t.Context(), sibling)
	require.NoError(t, err)
	require.True(t, complete)
	require.Empty(t, diff, "equal observed revisions need no reevaluation even with distinct branch watermarks")
	target := base.Apply([]BranchChange{{identity: resourceIdentity{namespace: "default", name: "removed"}}})
	diff, complete, err = target.ChangesFrom(t.Context(), sibling)
	require.NoError(t, err)
	require.True(t, complete)
	require.Equal(t, []stores.RevisionChange{
		{Sequence: target.Sequence(), Namespace: "default", Name: "added", Deleted: true, OldKeys: []string{"default", "new"}},
		{Sequence: target.Sequence(), Namespace: "default", Name: "changed", OldKeys: []string{"default", "new"}, NewKeys: []string{"default", "old"}},
		{Sequence: target.Sequence(), Namespace: "default", Name: "removed", Deleted: true, OldKeys: []string{"default", "old"}},
	}, diff)
	diff[1].NewKeys[0] = "poison"
	reverse, complete, err := sibling.ChangesFrom(t.Context(), target)
	require.NoError(t, err)
	require.True(t, complete)
	require.Equal(t, []stores.RevisionChange{
		{Sequence: sibling.Sequence(), Namespace: "default", Name: "added", NewKeys: []string{"default", "new"}},
		{Sequence: sibling.Sequence(), Namespace: "default", Name: "changed", OldKeys: []string{"default", "old"}, NewKeys: []string{"default", "new"}},
		{Sequence: sibling.Sequence(), Namespace: "default", Name: "removed", NewKeys: []string{"default", "old"}},
	}, reverse)
	_, complete, err = target.ChangesFrom(t.Context(), NewSnapshotBranch(2))
	require.NoError(t, err)
	require.False(t, complete, "a different source cannot authorize graph reuse")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, complete, err = target.ChangesFrom(ctx, sibling)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, complete)
}
