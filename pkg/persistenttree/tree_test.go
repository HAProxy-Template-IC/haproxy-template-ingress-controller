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

package persistenttree

import (
	"fmt"
	"hash/maphash"
	"math/bits"
	"math/rand"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTreeRandomizedIradixDifferential(t *testing.T) {
	random := rand.New(rand.NewSource(0x187))
	got := New[int]()
	want := iradix.New[int]()
	type snapshot struct {
		got  *Tree[int]
		want *iradix.Tree[int]
	}
	snapshots := []snapshot{{got: got, want: want}}

	for operation := range 10_000 {
		key := randomAuditKey(random)
		if random.Intn(3) == 0 {
			var gotPrevious, wantPrevious int
			var gotRemoved, wantRemoved bool
			got, gotPrevious, gotRemoved = got.Delete(key)
			want, wantPrevious, wantRemoved = want.Delete(key)
			assert.Equal(t, wantRemoved, gotRemoved)
			assert.Equal(t, wantPrevious, gotPrevious)
		} else {
			value := random.Int()
			var gotPrevious, wantPrevious int
			var gotReplaced, wantReplaced bool
			got, gotPrevious, gotReplaced = got.Insert(key, value)
			want, wantPrevious, wantReplaced = want.Insert(key, value)
			assert.Equal(t, wantReplaced, gotReplaced)
			assert.Equal(t, wantPrevious, gotPrevious)
		}

		assertTreeGetMatchesIradix(t, got, want, random)
		if operation%41 == 0 {
			assertTreeMatchesIradix(t, got, want, random)
		}
		if operation%97 == 0 {
			snapshots = append(snapshots, snapshot{got: got, want: want})
		}
		if operation%211 == 0 {
			for range min(8, len(snapshots)) {
				candidate := snapshots[random.Intn(len(snapshots))]
				assertTreeMatchesIradix(t, candidate.got, candidate.want, random)
			}
		}
	}
	assertTreeMatchesIradix(t, got, want, random)
}

func TestTreeRandomizedFrozenBaseDeltaIradixDifferential(t *testing.T) {
	random := rand.New(rand.NewSource(0x187c01d))
	entries := make([]Entry[int], 3_000)
	wantTxn := iradix.New[int]().Txn()
	for index := range entries {
		key := fmt.Sprintf("%c/%06d/%c", byte(index%251), index, byte(255-index%253))
		entries[index] = Entry[int]{Key: key, Value: index}
		wantTxn.Insert([]byte(key), index)
	}
	slices.SortFunc(entries, func(left, right Entry[int]) int {
		return strings.Compare(left.Key, right.Key)
	})
	got, err := NewFromSorted(entries)
	require.NoError(t, err)
	want := wantTxn.Commit()
	assertTreeMatchesIradix(t, got, want, random)

	for operation := range 5_000 {
		var key []byte
		if random.Intn(4) == 0 {
			key = randomAuditKey(random)
		} else {
			key = []byte(entries[random.Intn(len(entries))].Key)
		}
		if random.Intn(3) == 0 {
			var gotPrevious, wantPrevious int
			var gotRemoved, wantRemoved bool
			got, gotPrevious, gotRemoved = got.Delete(key)
			want, wantPrevious, wantRemoved = want.Delete(key)
			assert.Equal(t, wantRemoved, gotRemoved)
			assert.Equal(t, wantPrevious, gotPrevious)
		} else {
			value := random.Int()
			var gotPrevious, wantPrevious int
			var gotReplaced, wantReplaced bool
			got, gotPrevious, gotReplaced = got.Insert(key, value)
			want, wantPrevious, wantReplaced = want.Insert(key, value)
			assert.Equal(t, wantReplaced, gotReplaced)
			assert.Equal(t, wantPrevious, gotPrevious)
		}
		assertTreeGetMatchesIradix(t, got, want, random)
		if operation%37 == 0 {
			assertTreeMatchesIradix(t, got, want, random)
		}
	}
	assertTreeMatchesIradix(t, got, want, random)
}

func TestTreeBulkConstructionAndKeyOwnership(t *testing.T) {
	firstKey := []byte("a")
	secondKey := []byte("b")
	entries := []Entry[int]{NewEntry(firstKey, 1), NewEntry(secondKey, 2)}
	tree, err := NewFromSorted(entries)
	require.NoError(t, err)

	firstKey[0] = 'x'
	secondKey[0] = 'y'
	entries[0] = Entry[int]{Key: "poison", Value: 9}
	entries[1].Key = "poison-2"
	entries[1].Value = 10

	assert.Equal(t, []auditEntry{{key: "a", value: 1}, {key: "b", value: 2}}, auditEntries(tree.Root(), nil))
	assertTreeInvariants(t, tree)

	insertKey := []byte("c")
	updated, _, replaced := tree.Insert(insertKey, 3)
	require.False(t, replaced)
	insertKey[0] = 'z'
	value, exists := updated.Root().Get([]byte("c"))
	require.True(t, exists)
	assert.Equal(t, 3, value)
	_, exists = updated.Root().Get(insertKey)
	assert.False(t, exists)
	assert.Equal(t, []auditEntry{{key: "a", value: 1}, {key: "b", value: 2}}, auditEntries(tree.Root(), nil))
	assertTreeInvariants(t, updated)
}

func TestTreeBulkConstructorClonesEntryStrings(t *testing.T) {
	keyBytes := []byte("caller-owned")
	key := unsafe.String(unsafe.SliceData(keyBytes), len(keyBytes))
	tree, err := NewFromSorted([]Entry[int]{{Key: key, Value: 1}})
	require.NoError(t, err)

	keyBytes[0] = 'p'
	value, exists := tree.Root().Get([]byte("caller-owned"))
	require.True(t, exists)
	assert.Equal(t, 1, value)
	_, exists = tree.Root().Get(keyBytes)
	assert.False(t, exists)
}

func TestTreeBulkConstructorsValidateWithoutMutatingInput(t *testing.T) {
	unsorted := []Entry[int]{{Key: "z", Value: 2}, {Key: "", Value: 0}, {Key: "a", Value: 1}}
	wantInput := slices.Clone(unsorted)
	tree, err := NewFrom(unsorted)
	require.NoError(t, err)
	assert.Equal(t, wantInput, unsorted)
	assert.Equal(t, []auditEntry{{key: "", value: 0}, {key: "a", value: 1}, {key: "z", value: 2}}, auditEntries(tree.Root(), nil))

	_, err = NewFromSorted(unsorted)
	require.ErrorContains(t, err, "not strictly ordered")
	_, err = NewFrom([]Entry[int]{{Key: "same"}, {Key: "same"}})
	require.ErrorContains(t, err, "not strictly ordered")
	empty, err := NewFromSorted([]Entry[int]{})
	require.NoError(t, err)
	assert.Zero(t, empty.Len())
	assert.Nil(t, empty.Root())
}

func TestTreePrefixAndExtremaBoundaries(t *testing.T) {
	entries := []Entry[int]{
		{Key: "", Value: 0},
		{Key: "a", Value: 1},
		{Key: "a\x00", Value: 2},
		{Key: "a\xff", Value: 3},
		{Key: "b", Value: 4},
		{Key: "\xff", Value: 5},
		{Key: "\xff\xff", Value: 6},
	}
	tree, err := NewFromSorted(entries)
	require.NoError(t, err)
	tree, _, _ = tree.Delete([]byte(""))
	tree, _, _ = tree.Delete([]byte("\xff\xff"))
	tree, _, _ = tree.Insert([]byte("a\x00x"), 7)
	tree, _, _ = tree.Insert([]byte("\xff\xff\x00"), 8)

	for _, prefix := range [][]byte{nil, {}, {'a'}, {'a', 0}, {0xff}, {0xff, 0xff}, {'x'}} {
		got := auditEntries(tree.Root(), &auditWalk{prefix: prefix, stopAfter: -1})
		all := auditEntries(tree.Root(), nil)
		want := make([]auditEntry, 0, len(all))
		for _, entry := range all {
			if len(prefix) <= len(entry.key) && entry.key[:len(prefix)] == string(prefix) {
				want = append(want, entry)
			}
		}
		assert.Equal(t, want, got)
	}
	minimumKey, minimumValue, exists := tree.Root().Minimum()
	require.True(t, exists)
	assert.Equal(t, "a", minimumKey)
	assert.Equal(t, 1, minimumValue)
	maximumKey, maximumValue, exists := tree.Root().Maximum()
	require.True(t, exists)
	assert.Equal(t, "\xff\xff\x00", maximumKey)
	assert.Equal(t, 8, maximumValue)
	assertTreeInvariants(t, tree)
}

func TestTreeMinimumDoesNotAllocate(t *testing.T) {
	tree, err := NewFromSorted([]Entry[int]{{Key: "a", Value: 1}, {Key: "b", Value: 2}, {Key: "c", Value: 3}})
	require.NoError(t, err)
	tree, _, _ = tree.Delete([]byte("a"))
	tree, _, _ = tree.Insert([]byte("b"), 20)
	tree, _, _ = tree.Insert([]byte("0"), 0)

	var key string
	var value int
	var exists bool
	allocations := testing.AllocsPerRun(1_000, func() {
		key, value, exists = tree.Root().Minimum()
	})
	assert.Zero(t, allocations)
	assert.Equal(t, "0", key)
	assert.Zero(t, value)
	assert.True(t, exists)
}

func TestTreePersistentTransactionSnapshots(t *testing.T) {
	base, err := NewFromSorted([]Entry[int]{{Key: "a", Value: 1}, {Key: "b", Value: 2}, {Key: "c", Value: 3}})
	require.NoError(t, err)
	baseRoot := base.Root()
	txn := base.Txn()
	previous, replaced := txn.Insert([]byte("b"), 20)
	assert.True(t, replaced)
	assert.Equal(t, 2, previous)
	previous, removed := txn.Delete([]byte("a"))
	assert.True(t, removed)
	assert.Equal(t, 1, previous)
	_, replaced = txn.Insert([]byte("d"), 4)
	assert.False(t, replaced)
	updated := txn.Commit()

	assert.Same(t, baseRoot, base.Root())
	assert.NotSame(t, base.Root(), updated.Root())
	assert.Equal(t, []auditEntry{{key: "a", value: 1}, {key: "b", value: 2}, {key: "c", value: 3}}, auditEntries(base.Root(), nil))
	assert.Equal(t, []auditEntry{{key: "b", value: 20}, {key: "c", value: 3}, {key: "d", value: 4}}, auditEntries(updated.Root(), nil))
	assertTreeInvariants(t, base)
	assertTreeInvariants(t, updated)
}

func TestTreeNoOpTransactionPreservesIdentity(t *testing.T) {
	base, err := NewFromSorted([]Entry[int]{{Key: "a", Value: 1}, {Key: "b", Value: 2}})
	require.NoError(t, err)

	txn := base.Txn()
	assert.Same(t, base.Root(), txn.Root())
	_, removed := txn.Delete([]byte("missing"))
	assert.False(t, removed)
	assert.Same(t, base.Root(), txn.Root())
	assert.Same(t, base, txn.Commit())

	txn = base.Txn()
	_, replaced := txn.Insert([]byte("temporary"), 3)
	assert.False(t, replaced)
	_, removed = txn.Delete([]byte("temporary"))
	assert.True(t, removed)
	assert.Same(t, base.Root(), txn.Root())
	assert.Same(t, base, txn.Commit())

	unchanged, _, removed := base.Delete([]byte("missing"))
	assert.False(t, removed)
	assert.Same(t, base, unchanged)
}

func TestDeltaHashPersistentCollisionsAndDeepBranches(t *testing.T) {
	const collisionHash = uint64(0x1871871871871871)
	first := &deltaEntry[int]{key: "first", value: 1, present: true}
	second := &deltaEntry[int]{key: "second", value: 2, present: true}
	third := &deltaEntry[int]{key: "third", value: 3, present: true}

	firstRoot, previous, replaced := insertDeltaHash[int](nil, collisionHash, first)
	assert.Nil(t, previous)
	assert.False(t, replaced)
	secondRoot, previous, replaced := insertDeltaHash(firstRoot, collisionHash, second)
	assert.Nil(t, previous)
	assert.False(t, replaced)
	thirdRoot, previous, replaced := insertDeltaHash(secondRoot, collisionHash, third)
	assert.Nil(t, previous)
	assert.False(t, replaced)

	_, exists := getDeltaHash(firstRoot, []byte("second"), collisionHash)
	assert.False(t, exists)
	for key, want := range map[string]*deltaEntry[int]{
		"first": first, "second": second, "third": third,
	} {
		got, found := getDeltaHash(thirdRoot, []byte(key), collisionHash)
		require.True(t, found)
		assert.Same(t, want, got)
	}

	replacement := &deltaEntry[int]{key: "second", value: 20, present: true}
	replacedRoot, previous, replaced := insertDeltaHash(thirdRoot, collisionHash, replacement)
	require.True(t, replaced)
	assert.Same(t, second, previous)
	got, exists := getDeltaHash(replacedRoot, []byte("second"), collisionHash)
	require.True(t, exists)
	assert.Same(t, replacement, got)
	got, exists = getDeltaHash(thirdRoot, []byte("second"), collisionHash)
	require.True(t, exists)
	assert.Same(t, second, got)

	withoutFirst, previous, removed := deleteDeltaHash(replacedRoot, []byte("first"), collisionHash)
	require.True(t, removed)
	assert.Same(t, first, previous)
	_, exists = getDeltaHash(withoutFirst, []byte("first"), collisionHash)
	assert.False(t, exists)
	got, exists = getDeltaHash(withoutFirst, []byte("second"), collisionHash)
	require.True(t, exists)
	assert.Same(t, replacement, got)

	deepFirst := &deltaEntry[int]{key: "deep-first", value: 1, present: true}
	deepSecond := &deltaEntry[int]{key: "deep-second", value: 2, present: true}
	deepRoot, _, _ := insertDeltaHash[int](nil, 0, deepFirst)
	deepRoot, _, _ = insertDeltaHash(deepRoot, uint64(1)<<63, deepSecond)
	got, exists = getDeltaHash(deepRoot, []byte("deep-second"), uint64(1)<<63)
	require.True(t, exists)
	assert.Same(t, deepSecond, got)
	deepRoot, previous, removed = deleteDeltaHash(deepRoot, []byte("deep-first"), 0)
	require.True(t, removed)
	assert.Same(t, deepFirst, previous)
	got, exists = getDeltaHash(deepRoot, []byte("deep-second"), uint64(1)<<63)
	require.True(t, exists)
	assert.Same(t, deepSecond, got)
}

func TestTreeDistinctDeltaSnapshots(t *testing.T) {
	entries, keys := benchmarkFixture()
	base, err := NewFromSorted(entries)
	require.NoError(t, err)
	type snapshot struct {
		changed int
		tree    *Tree[int]
	}
	snapshots := []snapshot{{changed: 0, tree: base}}
	tree := base
	thresholds := map[int]struct{}{1: {}, 16: {}, 256: {}, 3_000: {}}
	for changed := 1; changed <= 3_000; changed++ {
		tree, _, _ = tree.Insert(keys[changed-1], -changed)
		if _, capture := thresholds[changed]; capture {
			snapshots = append(snapshots, snapshot{changed: changed, tree: tree})
		}
	}

	for _, snapshot := range snapshots {
		for index, key := range keys {
			value, exists := snapshot.tree.Root().Get(key)
			require.True(t, exists)
			if index < snapshot.changed {
				assert.Equal(t, -index-1, value)
			} else {
				assert.Equal(t, index, value)
			}
		}
		assertTreeInvariants(t, snapshot.tree)
	}
}

func TestTreeConcurrentReadersAndPersistentWriter(t *testing.T) {
	entries := make([]Entry[int], 2_000)
	for index := range entries {
		entries[index] = Entry[int]{Key: fmt.Sprintf("key-%06d", index), Value: index}
	}
	base, err := NewFromSorted(entries)
	require.NoError(t, err)
	var latest atomic.Pointer[Tree[int]]
	latest.Store(base)

	const readers = 24
	const updates = 4_000
	stop := make(chan struct{})
	var group sync.WaitGroup
	for reader := range readers {
		group.Add(1)
		go func(seed int64) {
			defer group.Done()
			random := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				snapshot := latest.Load()
				key := []byte(fmt.Sprintf("key-%06d", random.Intn(len(entries))))
				value, exists := snapshot.Root().Get(key)
				if !exists || value < 0 {
					t.Errorf("reader observed invalid value %d, exists %t", value, exists)
					return
				}
				snapshot.Root().WalkPrefix([]byte("key-00"), func(_ string, _ int) bool {
					return random.Intn(100) == 0
				})
			}
		}(int64(reader + 1))
	}

	current := base
	for update := range updates {
		key := []byte(fmt.Sprintf("key-%06d", update%len(entries)))
		current, _, _ = current.Insert(key, len(entries)+update)
		latest.Store(current)
	}
	close(stop)
	group.Wait()
	runtime.KeepAlive(current)
	assertTreeInvariants(t, base)
	assertTreeInvariants(t, current)
}

func BenchmarkTreeColdBuild(b *testing.B) {
	entries, byteKeys := benchmarkFixture()
	b.Run("persistent-tree", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			tree, err := NewFromSorted(entries)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkTreeSink = tree
		}
	})
	b.Run("iradix-transaction", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			txn := iradix.New[int]().Txn()
			for index := range byteKeys {
				txn.Insert(byteKeys[index], index)
			}
			benchmarkRadixSink = txn.Commit()
		}
	})
}

func BenchmarkTreeWarmUpdate(b *testing.B) {
	entries, byteKeys := benchmarkFixture()
	tree, err := NewFromSorted(entries)
	if err != nil {
		b.Fatal(err)
	}
	radixTxn := iradix.New[int]().Txn()
	for index := range byteKeys {
		radixTxn.Insert(byteKeys[index], index)
	}
	radix := radixTxn.Commit()
	key := byteKeys[len(byteKeys)/2]
	b.Run("persistent-tree", func(b *testing.B) {
		b.ReportAllocs()
		for operation := range b.N {
			updated, _, replaced := tree.Insert(key, operation)
			if !replaced {
				b.Fatal("warm key was not replaced")
			}
			benchmarkTreeSink = updated
		}
	})
	b.Run("iradix", func(b *testing.B) {
		b.ReportAllocs()
		for operation := range b.N {
			updated, _, replaced := radix.Insert(key, operation)
			if !replaced {
				b.Fatal("warm key was not replaced")
			}
			benchmarkRadixSink = updated
		}
	})
}

func BenchmarkTreeIntegratedReads(b *testing.B) {
	entries, byteKeys := benchmarkFixture()
	tree, err := NewFromSorted(entries)
	if err != nil {
		b.Fatal(err)
	}
	radixTxn := iradix.New[int]().Txn()
	for index := range byteKeys {
		radixTxn.Insert(byteKeys[index], index)
	}
	radix := radixTxn.Commit()
	treeWithDelta, _, _ := tree.Insert(byteKeys[len(byteKeys)/2], -1)
	radixWithDelta, _, _ := radix.Insert(byteKeys[len(byteKeys)/2], -1)

	for _, fixture := range []struct {
		name  string
		tree  *Tree[int]
		radix *iradix.Tree[int]
	}{
		{name: "frozen", tree: tree, radix: radix},
		{name: "one-delta", tree: treeWithDelta, radix: radixWithDelta},
	} {
		b.Run(fixture.name+"/get-all/persistent-tree", func(b *testing.B) {
			benchmarkTreeGetAll(b, fixture.tree, byteKeys)
		})
		b.Run(fixture.name+"/get-all/iradix", func(b *testing.B) {
			benchmarkRadixGetAll(b, fixture.radix, byteKeys)
		})
		b.Run(fixture.name+"/walk-all/persistent-tree", func(b *testing.B) {
			benchmarkTreeWalkAll(b, fixture.tree)
		})
		b.Run(fixture.name+"/walk-all/iradix", func(b *testing.B) {
			benchmarkRadixWalkAll(b, fixture.radix)
		})
	}
}

func benchmarkTreeGetAll(b *testing.B, tree *Tree[int], byteKeys [][]byte) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		total := 0
		for _, key := range byteKeys {
			value, exists := tree.Root().Get(key)
			if !exists {
				b.Fatal("missing fixture key")
			}
			total += value
		}
		benchmarkIntSink = total
	}
}

func benchmarkRadixGetAll(b *testing.B, radix *iradix.Tree[int], byteKeys [][]byte) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		total := 0
		for _, key := range byteKeys {
			value, exists := radix.Root().Get(key)
			if !exists {
				b.Fatal("missing fixture key")
			}
			total += value
		}
		benchmarkIntSink = total
	}
}

func benchmarkTreeWalkAll(b *testing.B, tree *Tree[int]) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		total := 0
		tree.Root().Walk(func(_ string, value int) bool {
			total += value
			return false
		})
		benchmarkIntSink = total
	}
}

func benchmarkRadixWalkAll(b *testing.B, radix *iradix.Tree[int]) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		total := 0
		radix.Root().Walk(func(_ []byte, value int) bool {
			total += value
			return false
		})
		benchmarkIntSink = total
	}
}

func BenchmarkTreeDeltaScale(b *testing.B) {
	entries, byteKeys := benchmarkFixture()
	base, err := NewFromSorted(entries)
	if err != nil {
		b.Fatal(err)
	}
	for _, changed := range []int{0, 1, 16, 256, 3_000} {
		tree := base
		for index := range changed {
			tree, _, _ = tree.Insert(byteKeys[index], -index-1)
		}
		b.Run(fmt.Sprintf("delta-%d/get-all", changed), func(b *testing.B) {
			benchmarkTreeGetAll(b, tree, byteKeys)
		})
		b.Run(fmt.Sprintf("delta-%d/warm-update", changed), func(b *testing.B) {
			benchmarkTreeWarmUpdate(b, tree, byteKeys[len(byteKeys)/2])
		})
	}
}

func benchmarkTreeWarmUpdate(b *testing.B, tree *Tree[int], key []byte) {
	b.Helper()
	b.ReportAllocs()
	for operation := range b.N {
		updated, _, replaced := tree.Insert(key, operation)
		if !replaced {
			b.Fatal("warm key was not replaced")
		}
		benchmarkTreeSink = updated
	}
}

type auditEntry struct {
	key   string
	value int
}

type auditWalk struct {
	prefix    []byte
	stopAfter int
}

func assertTreeGetMatchesIradix(
	t *testing.T,
	got *Tree[int],
	want *iradix.Tree[int],
	random *rand.Rand,
) {
	t.Helper()
	require.Equal(t, want.Len(), got.Len())
	for range 16 {
		key := randomAuditKey(random)
		gotValue, gotExists := got.Root().Get(key)
		wantValue, wantExists := want.Root().Get(key)
		assert.Equal(t, wantExists, gotExists)
		assert.Equal(t, wantValue, gotValue)
	}
}

func assertTreeMatchesIradix(
	t *testing.T,
	got *Tree[int],
	want *iradix.Tree[int],
	random *rand.Rand,
) {
	t.Helper()
	assertTreeGetMatchesIradix(t, got, want, random)
	wantEntries := auditRadixEntries(want.Root(), nil)
	gotEntries := auditEntries(got.Root(), nil)
	assert.Equal(t, wantEntries, gotEntries)

	gotMinimumKey, gotMinimumValue, gotMinimumExists := got.Root().Minimum()
	wantMinimumKey, wantMinimumValue, wantMinimumExists := want.Root().Minimum()
	assert.Equal(t, wantMinimumExists, gotMinimumExists)
	assert.Equal(t, string(wantMinimumKey), gotMinimumKey)
	assert.Equal(t, wantMinimumValue, gotMinimumValue)
	gotMaximumKey, gotMaximumValue, gotMaximumExists := got.Root().Maximum()
	wantMaximumKey, wantMaximumValue, wantMaximumExists := want.Root().Maximum()
	assert.Equal(t, wantMaximumExists, gotMaximumExists)
	assert.Equal(t, string(wantMaximumKey), gotMaximumKey)
	assert.Equal(t, wantMaximumValue, gotMaximumValue)

	for range 12 {
		prefix := randomAuditPrefix(random, wantEntries)
		stopAfter := random.Intn(8)
		walk := &auditWalk{prefix: prefix, stopAfter: stopAfter}
		gotPrefix := auditEntries(got.Root(), walk)
		wantPrefix := auditRadixEntries(want.Root(), walk)
		assert.Equal(t, wantPrefix, gotPrefix)
		all := auditRadixEntries(want.Root(), &auditWalk{prefix: prefix, stopAfter: -1})
		stopped := got.Root().WalkPrefix(prefix, auditStopVisitor(stopAfter))
		assert.Equal(t, stopAfter < len(all), stopped)
	}
	assertTreeInvariants(t, got)
}

func auditEntries(root *Node[int], walk *auditWalk) []auditEntry {
	entries := []auditEntry{}
	visit := func(key string, value int) bool {
		entries = append(entries, auditEntry{key: key, value: value})
		return walk != nil && walk.stopAfter >= 0 && len(entries) > walk.stopAfter
	}
	if walk == nil {
		root.Walk(visit)
	} else {
		root.WalkPrefix(walk.prefix, visit)
	}
	return entries
}

func auditRadixEntries(root *iradix.Node[int], walk *auditWalk) []auditEntry {
	entries := []auditEntry{}
	visit := func(key []byte, value int) bool {
		entries = append(entries, auditEntry{key: string(key), value: value})
		return walk != nil && walk.stopAfter >= 0 && len(entries) > walk.stopAfter
	}
	if walk == nil {
		root.Walk(visit)
	} else {
		root.WalkPrefix(walk.prefix, visit)
	}
	return entries
}

func auditStopVisitor(stopAfter int) func(string, int) bool {
	visited := 0
	return func(_ string, _ int) bool {
		visited++
		return visited > stopAfter
	}
}

func assertTreeInvariants(t *testing.T, tree *Tree[int]) {
	t.Helper()
	if tree == nil || tree.root == nil {
		assert.Zero(t, tree.Len())
		return
	}
	for index := 1; index < len(tree.root.base); index++ {
		assert.Less(t, tree.root.base[index-1].key, tree.root.base[index].key)
	}
	if len(tree.root.base) < frozenLookupThreshold {
		assert.Empty(t, tree.root.lookup.slots)
	} else {
		require.NotEmpty(t, tree.root.lookup.slots)
		assert.Zero(t, len(tree.root.lookup.slots)&(len(tree.root.lookup.slots)-1))
		indexed := make(map[int]struct{}, len(tree.root.base))
		for _, stored := range tree.root.lookup.slots {
			if stored == 0 {
				continue
			}
			index := int(stored - 1)
			require.Less(t, index, len(tree.root.base))
			_, duplicate := indexed[index]
			assert.False(t, duplicate)
			indexed[index] = struct{}{}
			gotIndex, exists := getFrozenLookup(
				tree.root.lookup, tree.root.base, []byte(tree.root.base[index].key),
			)
			require.True(t, exists)
			assert.Equal(t, index, gotIndex)
		}
		assert.Len(t, indexed, len(tree.root.base))
	}
	deltaCount, _, minimum, maximum := assertDeltaInvariants(t, tree.root.delta)
	assert.Equal(t, tree.root.deltaSize, deltaCount)
	if deltaCount > 0 {
		assert.LessOrEqual(t, minimum, maximum)
	}
	orderedEntries := make(map[string]*deltaEntry[int], deltaCount)
	collectOrderedDeltaEntries(tree.root.delta, orderedEntries)
	hashedEntries := make(map[string]*deltaEntry[int], deltaCount)
	hashCount := assertDeltaHashInvariants(t, tree.root.deltaHash, 0, hashedEntries)
	assert.Equal(t, deltaCount, hashCount)
	assert.Len(t, hashedEntries, len(orderedEntries))
	for key, orderedEntry := range orderedEntries {
		hashedEntry, exists := hashedEntries[key]
		require.True(t, exists)
		assert.Same(t, orderedEntry, hashedEntry)
		indexedEntry, indexed := getDeltaHashString(
			tree.root.deltaHash,
			key,
			maphash.String(frozenLookupSeed, key),
		)
		require.True(t, indexed)
		assert.Same(t, orderedEntry, indexedEntry)
	}
	entries := auditEntries(tree.Root(), nil)
	assert.Equal(t, tree.Len(), len(entries))
	for index := 1; index < len(entries); index++ {
		assert.Less(t, entries[index-1].key, entries[index].key)
	}
}

func collectOrderedDeltaEntries[V any](node *deltaNode[V], entries map[string]*deltaEntry[V]) {
	if node == nil {
		return
	}
	collectOrderedDeltaEntries(node.left, entries)
	entries[node.entry.key] = node.entry
	collectOrderedDeltaEntries(node.right, entries)
}

func assertDeltaHashInvariants(
	t *testing.T,
	node *deltaHashNode[int],
	shift uint,
	entries map[string]*deltaEntry[int],
) int {
	t.Helper()
	if node == nil {
		return 0
	}
	assert.Equal(t, bits.OnesCount32(node.bitmap), len(node.slots))
	count := 0
	slotIndex := 0
	for fragment := uint32(0); fragment < 32; fragment++ {
		bit := uint32(1) << fragment
		if node.bitmap&bit == 0 {
			continue
		}
		require.Less(t, slotIndex, len(node.slots))
		slot := node.slots[slotIndex]
		slotIndex++
		assert.NotEqual(t, slot.child == nil, slot.leaf == nil)
		if slot.child != nil {
			count += assertDeltaHashInvariants(t, slot.child, shift+deltaHashBits, entries)
			continue
		}
		assert.Equal(t, bit, deltaHashBit(slot.leaf.hash, shift))
		leafEntries := make([]*deltaEntry[int], 0, len(slot.leaf.collisions)+1)
		leafEntries = append(leafEntries, slot.leaf.entry)
		leafEntries = append(leafEntries, slot.leaf.collisions...)
		for _, entry := range leafEntries {
			require.NotNil(t, entry)
			assert.Equal(t, slot.leaf.hash, maphash.String(frozenLookupSeed, entry.key))
			_, duplicate := entries[entry.key]
			assert.False(t, duplicate)
			entries[entry.key] = entry
			count++
		}
	}
	assert.Equal(t, len(node.slots), slotIndex)
	return count
}

func assertDeltaInvariants(
	t *testing.T,
	node *deltaNode[int],
) (count, height int, minimum, maximum string) {
	t.Helper()
	if node == nil {
		return 0, 0, "", ""
	}
	leftCount, leftHeight, leftMinimum, leftMaximum := assertDeltaInvariants(t, node.left)
	rightCount, rightHeight, rightMinimum, rightMaximum := assertDeltaInvariants(t, node.right)
	assert.Equal(t, max(leftHeight, rightHeight)+1, node.height)
	assert.LessOrEqual(t, leftHeight-rightHeight, 1)
	assert.GreaterOrEqual(t, leftHeight-rightHeight, -1)
	if node.left != nil {
		assert.Less(t, leftMaximum, node.entry.key)
	}
	if node.right != nil {
		assert.Less(t, node.entry.key, rightMinimum)
	}
	minimum, maximum = node.entry.key, node.entry.key
	if node.left != nil {
		minimum = leftMinimum
	}
	if node.right != nil {
		maximum = rightMaximum
	}
	return leftCount + rightCount + 1, node.height, minimum, maximum
}

func randomAuditKey(random *rand.Rand) []byte {
	length := random.Intn(6)
	key := make([]byte, length)
	alphabet := [...]byte{0, 1, 2, 3, 0x7f, 0xfe, 0xff}
	for index := range key {
		key[index] = alphabet[random.Intn(len(alphabet))]
	}
	return key
}

func randomAuditPrefix(random *rand.Rand, entries []auditEntry) []byte {
	if len(entries) == 0 || random.Intn(4) == 0 {
		return randomAuditKey(random)
	}
	key := entries[random.Intn(len(entries))].key
	return []byte(key[:random.Intn(len(key)+1)])
}

const benchmarkFixtureEntryCount = 39_012

func benchmarkFixture() (entries []Entry[int], keys [][]byte) {
	entries = make([]Entry[int], benchmarkFixtureEntryCount)
	keys = make([][]byte, benchmarkFixtureEntryCount)
	for index := range entries {
		key := fmt.Sprintf("component/routes/default/route-%06d", index)
		entries[index] = Entry[int]{Key: key, Value: index}
		keys[index] = []byte(key)
	}
	return entries, keys
}

var benchmarkTreeSink *Tree[int]
var benchmarkRadixSink *iradix.Tree[int]
var benchmarkIntSink int
