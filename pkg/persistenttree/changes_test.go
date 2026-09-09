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

package persistenttree

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func changesBetween(t *testing.T, current, previous *Tree[int]) map[string]Change[int] {
	t.Helper()
	got := map[string]Change[int]{}
	current.Root().WalkChanges(previous.Root(), func(a, b int) bool { return a == b }, func(change Change[int]) bool {
		_, duplicate := got[change.Key]
		require.False(t, duplicate, "duplicate key %q", change.Key)
		got[change.Key] = change
		return false
	})
	return got
}

func TestWalkChangesAcrossBranchedSnapshots(t *testing.T) {
	type snapshot struct {
		tree   *Tree[int]
		values map[string]int
	}
	initial := map[string]int{}
	entries := make([]Entry[int], 128)
	for index := range entries {
		key := fmt.Sprintf("key-%04d", index)
		entries[index] = Entry[int]{Key: key, Value: index}
		initial[key] = index
	}
	frozen, err := NewFrom(entries)
	require.NoError(t, err)
	history := make([]snapshot, 0, 302)
	history = append(history, snapshot{New[int](), map[string]int{}}, snapshot{frozen, initial})
	random := rand.New(rand.NewPCG(17, 29))
	for range 300 {
		base := history[random.IntN(len(history))]
		txn, values := base.tree.Txn(), maps.Clone(base.values)
		for range 12 {
			key := fmt.Sprintf("key-%04d", random.IntN(256))
			if random.IntN(4) == 0 {
				txn.Delete([]byte(key))
				delete(values, key)
			} else {
				value := random.IntN(128)
				txn.Insert([]byte(key), value)
				values[key] = value
			}
		}
		current := snapshot{txn.Commit(), values}
		for _, previous := range []snapshot{base, history[random.IntN(len(history))]} {
			want := expectedChanges(previous.values, values)
			assert.Equal(t, want, changesBetween(t, current.tree, previous.tree))
			assert.Equal(t, expectedChanges(values, previous.values), changesBetween(t, previous.tree, current.tree))
		}
		history = append(history, current)
	}
}

func expectedChanges(before, after map[string]int) map[string]Change[int] {
	result := map[string]Change[int]{}
	for key, value := range after {
		previous, exists := before[key]
		if !exists || value != previous {
			result[key] = Change[int]{Key: key, Before: previous, After: value, BeforePresent: exists, AfterPresent: true}
		}
	}
	for key, previous := range before {
		if _, exists := after[key]; !exists {
			result[key] = Change[int]{Key: key, Before: previous, BeforePresent: true}
		}
	}
	return result
}

func TestWalkChangesRestoresAndIndependentBases(t *testing.T) {
	base, err := NewFrom([]Entry[int]{{"a", 1}, {"b", 2}})
	require.NoError(t, err)
	removed, _, _ := base.Delete([]byte("a"))
	restored, _, _ := removed.Insert([]byte("a"), 1)
	require.Empty(t, changesBetween(t, restored, base))
	independent, err := NewFrom([]Entry[int]{{"a", 1}, {"b", 2}})
	require.NoError(t, err)
	require.Empty(t, changesBetween(t, independent, restored))
	var empty *Tree[int]
	require.Empty(t, changesBetween(t, empty, New[int]()))
	count := 0
	stopped := base.Root().WalkChanges(nil, func(a, b int) bool { return a == b }, func(Change[int]) bool {
		count++
		return true
	})
	assert.True(t, stopped)
	assert.Equal(t, 1, count)
}

func TestWalkChangesSkipsSharedEntries(t *testing.T) {
	for _, count := range []int{100, 10000} {
		base := New[int]()
		entries := make([]Entry[int], count)
		for index := range count {
			key := fmt.Sprintf("key-%06d", index)
			base, _, _ = base.Insert([]byte(key), index)
			entries[index] = Entry[int]{Key: key, Value: index}
		}
		frozen, err := NewFromSorted(entries)
		require.NoError(t, err)
		assertOneChangedEntry(t, base)
		assertOneChangedEntry(t, frozen)
	}
}

func assertOneChangedEntry(t *testing.T, base *Tree[int]) {
	t.Helper()
	current, _, _ := base.Insert([]byte("key-000042"), -1)
	comparisons, visits := 0, 0
	current.Root().WalkChanges(base.Root(), func(a, b int) bool {
		comparisons++
		return a == b
	}, func(change Change[int]) bool {
		visits++
		assert.Equal(t, "key-000042", change.Key)
		return false
	})
	assert.Equal(t, 1, comparisons)
	assert.Equal(t, 1, visits)
	require.Empty(t, changesBetween(t, current, current))
}

func TestChangedHashLeavesPreserveCollisions(t *testing.T) {
	first := &deltaEntry[int]{key: "first", value: 1, present: true}
	second := &deltaEntry[int]{key: "second", value: 2, present: true}
	last := &deltaEntry[int]{key: "last", value: 3, present: true}
	before := &deltaHashLeaf[int]{hash: 1, entry: first, collisions: []*deltaEntry[int]{second}}
	after := &deltaHashLeaf[int]{hash: 1, entry: second, collisions: []*deltaEntry[int]{last}}
	var keys []string
	stopped := walkChangedHashLeaves(after, before, func(key string) bool {
		keys = append(keys, key)
		return false
	})
	assert.False(t, stopped)
	assert.ElementsMatch(t, []string{"first", "last"}, keys)
	assert.True(t, walkChangedHashLeaves(after, before, func(string) bool { return true }))
}
