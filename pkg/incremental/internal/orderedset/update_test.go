package orderedset_test

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental/internal/orderedset"
)

func TestUpdateMatchesMembershipAssignmentsWithoutChangingSnapshots(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(fmt.Sprintf("packed=%t", packed), func(t *testing.T) {
			authority := orderedset.NewAuthority()
			build := orderedset.BuildSorted
			if packed {
				build = orderedset.BuildPackedSorted
			}
			root, err := build(authority, testScope, []string{"key-00", "key-10", "key-20"})
			require.NoError(t, err)
			want := map[string]bool{"key-00": true, "key-10": true, "key-20": true}
			random := rand.New(rand.NewPCG(13, 29))
			for range 200 {
				previous := slices.Sorted(maps.Keys(want))
				membership := randomMembershipAssignments(random, want)
				next, err := root.Update(authority, testScope, membership)
				require.NoError(t, err)
				clear(membership)
				membership["poison"] = true
				assertValues(t, root, authority, previous)
				assertValues(t, next, authority, slices.Sorted(maps.Keys(want)))
				root = next
			}
			membership := make(map[string]bool, len(want))
			for key := range want {
				membership[key] = true
			}
			membership["absent"] = false
			unchanged, err := root.Update(authority, testScope, membership)
			require.NoError(t, err)
			same, err := root.SameRoot(authority, testScope, unchanged)
			require.NoError(t, err)
			require.True(t, same)
			for key := range membership {
				membership[key] = false
			}
			empty, err := root.Update(authority, testScope, membership)
			require.NoError(t, err)
			same, err = empty.SameRoot(authority, testScope, authority.Empty())
			require.NoError(t, err)
			require.True(t, same)
		})
	}
}

func TestUpdateRejectsInvalidOwnershipAndEmptyKeys(t *testing.T) {
	authority := orderedset.NewAuthority()
	root, err := orderedset.BuildPackedSorted(authority, testScope, []string{"alpha"})
	require.NoError(t, err)
	_, err = root.Update(orderedset.NewAuthority(), testScope, nil)
	require.Error(t, err)
	_, err = root.Update(authority, orderedset.Scope{Domain: 1, Key: "foreign"}, nil)
	require.Error(t, err)
	_, err = (orderedset.Root{}).Update(authority, testScope, nil)
	require.Error(t, err)
	for _, present := range []bool{false, true} {
		_, err = root.Update(authority, testScope, map[string]bool{"": present})
		require.Error(t, err)
	}
	assertValues(t, root, authority, []string{"alpha"})
}

func randomMembershipAssignments(random *rand.Rand, want map[string]bool) map[string]bool {
	membership := make(map[string]bool)
	for range 12 {
		key := fmt.Sprintf("key-%02d", random.IntN(50))
		present := random.IntN(2) == 0
		membership[key] = present
		if present {
			want[key] = true
		} else {
			delete(want, key)
		}
	}
	return membership
}
