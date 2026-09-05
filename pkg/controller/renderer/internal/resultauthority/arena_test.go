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

package resultauthority

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func TestArenaInitializesOwnedRefsInExactContiguousSlots(t *testing.T) {
	arena, err := NewArena[testValue, testMetadata](3, 17)
	require.NoError(t, err)
	require.Len(t, arena.slots, 3)
	assert.Equal(t, 3, cap(arena.slots))

	key := incremental.NewQueryKey("query")
	metadata := testMetadata{component: "component", allowed: true}
	ref, err := arena.InitializeOwned(
		2, key, "encoded", testValue{text: "owned", bytes: []byte("owned")}, &metadata,
	)
	require.NoError(t, err)
	assert.Same(t, &arena.slots[2].ref, ref)
	assert.Equal(t, arenaSlotPending, arena.slots[2].state)
	assert.Equal(t, 2, ref.slot)
	assert.Equal(t, uint64(17), ref.generation)
	require.NoError(t, ref.Pending(key, "encoded", incremental.ExactValueRoot{}))

	_, err = arena.InitializeOwned(2, key, "encoded", testValue{}, nil)
	require.ErrorContains(t, err, "unavailable")
	_, err = arena.InitializeOwned(-1, key, "encoded", testValue{}, nil)
	require.ErrorContains(t, err, "out of range")
	_, err = arena.InitializeOwned(3, key, "encoded", testValue{}, nil)
	require.ErrorContains(t, err, "out of range")
	_, err = arena.InitializeOwned(0, incremental.QueryKey{}, "encoded", testValue{}, nil)
	require.ErrorContains(t, err, "key is empty")
}

func TestArenaInitializeOwnedManyPublishesOnlyAfterCompletePreflight(t *testing.T) {
	arena, err := NewArena[testValue, testMetadata](3, 23)
	require.NoError(t, err)
	metadata := testMetadata{component: "right", allowed: true}
	requests := []InitializeRequest[testValue, testMetadata]{
		{
			Index: 2, Key: incremental.NewQueryKey("right"), Encoded: "right",
			Value: testValue{text: "right", bytes: []byte("right")}, Metadata: &metadata,
		},
		{
			Index: 0, Key: incremental.NewQueryKey("left"), Encoded: "left",
			Value: testValue{text: "left", bytes: []byte("left")},
		},
	}

	refs, err := arena.InitializeOwnedMany(requests)
	require.NoError(t, err)
	require.Len(t, refs, len(requests))
	assert.Same(t, &arena.slots[2].ref, refs[0])
	assert.Same(t, &arena.slots[0].ref, refs[1])
	assert.Equal(t, arenaSlotPending, arena.slots[2].state)
	assert.Equal(t, arenaSlotPending, arena.slots[0].state)
	assert.Equal(t, arenaSlotEmpty, arena.slots[1].state)
	require.NoError(t, refs[0].Pending(
		requests[0].Key,
		requests[0].Encoded,
		incremental.ExactValueRoot{},
	))
	assert.True(t, arena.slots[2].hasMetadata)
	assert.Equal(t, metadata, arena.slots[2].metadata)
}

func TestArenaInitializeOwnedManyPoisonLeavesEveryRequestedSlotEmpty(t *testing.T) {
	tests := map[string]func([]InitializeRequest[testValue, testMetadata]){
		"empty final key": func(requests []InitializeRequest[testValue, testMetadata]) {
			requests[len(requests)-1].Key = incremental.QueryKey{}
		},
		"duplicate final slot": func(requests []InitializeRequest[testValue, testMetadata]) {
			requests[len(requests)-1].Index = requests[0].Index
		},
		"out-of-range final slot": func(requests []InitializeRequest[testValue, testMetadata]) {
			requests[len(requests)-1].Index = len(requests)
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			arena, err := NewArena[testValue, testMetadata](2, 29)
			require.NoError(t, err)
			requests := []InitializeRequest[testValue, testMetadata]{
				{
					Index: 0, Key: incremental.NewQueryKey("left"), Encoded: "left",
					Value: testValue{text: "left", bytes: []byte("left")},
				},
				{
					Index: 1, Key: incremental.NewQueryKey("right"), Encoded: "right",
					Value: testValue{text: "right", bytes: []byte("right")},
				},
			}
			poison(requests)

			refs, err := arena.InitializeOwnedMany(requests)
			require.Error(t, err)
			assert.Nil(t, refs)
			for index := range arena.slots {
				assert.Equal(t, arenaSlotEmpty, arena.slots[index].state)
				assert.Equal(t, testValue{}, arena.slots[index].value)
				assert.Equal(t, incremental.QueryKey{}, arena.slots[index].key)
			}
		})
	}
}

func TestArenaInitializeOwnedManyUnavailableFinalSlotLeavesPrefixEmpty(t *testing.T) {
	arena, err := NewArena[testValue, testMetadata](2, 30)
	require.NoError(t, err)
	existingKey := incremental.NewQueryKey("existing")
	existing, err := arena.InitializeOwned(
		1,
		existingKey,
		"existing",
		testValue{text: "existing", bytes: []byte("existing")},
		nil,
	)
	require.NoError(t, err)
	requests := []InitializeRequest[testValue, testMetadata]{
		{
			Index: 0, Key: incremental.NewQueryKey("left"), Encoded: "left",
			Value: testValue{text: "left", bytes: []byte("left")},
		},
		{
			Index: 1, Key: incremental.NewQueryKey("right"), Encoded: "right",
			Value: testValue{text: "right", bytes: []byte("right")},
		},
	}

	refs, err := arena.InitializeOwnedMany(requests)
	require.ErrorContains(t, err, "unavailable")
	assert.Nil(t, refs)
	assert.Equal(t, arenaSlotEmpty, arena.slots[0].state)
	assert.Equal(t, testValue{}, arena.slots[0].value)
	assert.Equal(t, arenaSlotPending, arena.slots[1].state)
	assert.Same(t, existing, &arena.slots[1].ref)
	assert.Equal(t, "existing", arena.slots[1].value.text)
}

func TestArenaConcurrentInitializeOwnedManyPublishesOneCompleteRange(t *testing.T) {
	arena, err := NewArena[testValue, testMetadata](2, 32)
	require.NoError(t, err)
	waves := [][]InitializeRequest[testValue, testMetadata]{
		{
			{Index: 0, Key: incremental.NewQueryKey("first-left"), Value: testValue{text: "first-left"}},
			{Index: 1, Key: incremental.NewQueryKey("first-right"), Value: testValue{text: "first-right"}},
		},
		{
			{Index: 1, Key: incremental.NewQueryKey("second-right"), Value: testValue{text: "second-right"}},
			{Index: 0, Key: incremental.NewQueryKey("second-left"), Value: testValue{text: "second-left"}},
		},
	}
	start := make(chan struct{})
	var successes atomic.Int64
	var wait sync.WaitGroup
	for waveIndex := range waves {
		wait.Go(func() {
			<-start
			if _, initializeErr := arena.InitializeOwnedMany(waves[waveIndex]); initializeErr == nil {
				successes.Add(1)
			}
		})
	}
	close(start)
	wait.Wait()
	require.Equal(t, int64(1), successes.Load())
	left := arena.slots[0].value.text
	right := arena.slots[1].value.text
	assert.True(t,
		left == "first-left" && right == "first-right" ||
			left == "second-left" && right == "second-right",
	)
}

func TestArenaRejectsInvalidConstruction(t *testing.T) {
	_, err := NewArena[testValue, testMetadata](0, 1)
	require.ErrorContains(t, err, "capacity")
	_, err = NewArena[testValue, testMetadata](-1, 1)
	require.ErrorContains(t, err, "capacity")
	_, err = NewArena[testValue, testMetadata](1, 0)
	require.ErrorContains(t, err, "generation")

	var arena *Arena[testValue, testMetadata]
	_, err = arena.InitializeOwned(
		0, incremental.NewQueryKey("query"), "encoded", testValue{}, nil,
	)
	require.ErrorContains(t, err, "unavailable")
	arena.Revoke()
}

func TestArenaRefLifecycleAndDetachedMaterialization(t *testing.T) {
	key := incremental.NewQueryKey("query")
	root := testExactRoot(t, key, "encoded")
	metadata := testMetadata{component: "component", allowed: true}
	arena, err := NewArena[testValue, testMetadata](1, 1)
	require.NoError(t, err)
	ref, err := arena.InitializeOwned(
		0,
		key,
		"encoded",
		testValue{text: "original", bytes: []byte("original")},
		&metadata,
	)
	require.NoError(t, err)

	require.NoError(t, ref.Pending(key, "encoded", incremental.ExactValueRoot{}))
	require.Error(t, ref.Validate(key, "encoded", incremental.ExactValueRoot{}, root))
	require.NoError(t, ref.Bind(key, "encoded", incremental.ExactValueRoot{}, root))
	require.NoError(t, ref.Bind(key, "encoded", root, root))
	require.NoError(t, ref.Validate(key, "encoded", root, root))
	require.NoError(t, ref.MetadataMatches(key, "encoded", root, root, metadata))

	first, err := ref.Materialize(key, "encoded", root, root, cloneTestValue)
	require.NoError(t, err)
	first.bytes[0] = 'p'
	second, err := ref.Materialize(key, "encoded", root, root, cloneTestValue)
	require.NoError(t, err)
	assert.Equal(t, "original", string(second.bytes))

	owned, err := ref.Take(key, "encoded", root, root)
	require.NoError(t, err)
	assert.Equal(t, "original", owned.text)
	assert.Equal(t, "original", string(owned.bytes))
	assert.Equal(t, arenaSlotTaken, arena.slots[0].state)
	require.NoError(t, ref.Validate(key, "encoded", root, root))
	require.NoError(t, ref.MetadataMatches(key, "encoded", root, root, metadata))
	_, err = ref.Take(key, "encoded", root, root)
	require.ErrorContains(t, err, "already transferred")
	_, err = ref.Materialize(key, "encoded", root, root, cloneTestValue)
	require.ErrorContains(t, err, "already transferred")
}

func TestArenaRefMetadataAndRootPoisonFailClosed(t *testing.T) {
	key := incremental.NewQueryKey("query")
	root := testExactRoot(t, key, "encoded")
	foreignRoot := testExactRoot(t, key, "encoded")
	mismatchedValueRoot := testExactRoot(t, key, "poison")
	arena, err := NewArena[testValue, testMetadata](1, 1)
	require.NoError(t, err)
	ref, err := arena.InitializeOwned(0, key, "encoded", testValue{}, nil)
	require.NoError(t, err)

	require.Error(t, ref.Pending(key, "encoded", root))
	require.Error(t, ref.Bind(key, "encoded", incremental.ExactValueRoot{}, mismatchedValueRoot))
	require.NoError(t, ref.Bind(key, "encoded", incremental.ExactValueRoot{}, root))
	require.Error(t, ref.Validate(key, "encoded", foreignRoot, root))
	require.Error(t, ref.Validate(key, "encoded", root, foreignRoot))
	require.Error(t, ref.Validate(incremental.NewQueryKey("other"), "encoded", root, root))
	require.Error(t, ref.Validate(key, "poison", root, root))
	require.ErrorIs(
		t, ref.MetadataMatches(key, "encoded", root, root, testMetadata{}), ErrMetadataUnavailable,
	)
	_, err = ref.Materialize(key, "encoded", root, root, nil)
	require.ErrorContains(t, err, "clone function is nil")
}

func TestArenaCopiedForgedAndCorruptedRefsFailClosed(t *testing.T) {
	tests := map[string]func(*Arena[testValue, testMetadata], *Ref[testValue, testMetadata]) *Ref[testValue, testMetadata]{
		"copied ref": func(_ *Arena[testValue, testMetadata], ref *Ref[testValue, testMetadata]) *Ref[testValue, testMetadata] {
			copied := *ref
			return &copied
		},
		"resealed copy": func(_ *Arena[testValue, testMetadata], ref *Ref[testValue, testMetadata]) *Ref[testValue, testMetadata] {
			copied := *ref
			copied.seal = &copied
			return &copied
		},
		"wrong slot": func(_ *Arena[testValue, testMetadata], ref *Ref[testValue, testMetadata]) *Ref[testValue, testMetadata] {
			copied := *ref
			copied.slot = 1
			copied.seal = &copied
			return &copied
		},
		"wrong generation": func(_ *Arena[testValue, testMetadata], ref *Ref[testValue, testMetadata]) *Ref[testValue, testMetadata] {
			copied := *ref
			copied.generation++
			copied.seal = &copied
			return &copied
		},
		"wrong arena": func(arena *Arena[testValue, testMetadata], ref *Ref[testValue, testMetadata]) *Ref[testValue, testMetadata] {
			foreign := &Arena[testValue, testMetadata]{generation: arena.generation, slots: arena.slots}
			foreign.seal = foreign
			copied := *ref
			copied.arena = foreign
			copied.seal = &copied
			return &copied
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			key := incremental.NewQueryKey("query")
			root := testExactRoot(t, key, "encoded")
			arena, err := NewArena[testValue, testMetadata](2, 9)
			require.NoError(t, err)
			ref, err := arena.InitializeOwned(0, key, "encoded", testValue{}, nil)
			require.NoError(t, err)
			require.NoError(t, ref.Bind(key, "encoded", incremental.ExactValueRoot{}, root))

			forged := poison(arena, ref)
			require.Error(t, forged.Validate(key, "encoded", root, root))
		})
	}
}

func TestArenaSlotCorruptionFailsClosed(t *testing.T) {
	tests := map[string]func(*arenaSlot[testValue, testMetadata]){
		"owner":      func(slot *arenaSlot[testValue, testMetadata]) { slot.owner = nil },
		"index":      func(slot *arenaSlot[testValue, testMetadata]) { slot.index++ },
		"generation": func(slot *arenaSlot[testValue, testMetadata]) { slot.generation++ },
		"key":        func(slot *arenaSlot[testValue, testMetadata]) { slot.key = incremental.NewQueryKey("other") },
		"encoded":    func(slot *arenaSlot[testValue, testMetadata]) { slot.encoded = "poison" },
		"state":      func(slot *arenaSlot[testValue, testMetadata]) { slot.state = arenaSlotEmpty },
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			key := incremental.NewQueryKey("query")
			root := testExactRoot(t, key, "encoded")
			arena, err := NewArena[testValue, testMetadata](1, 4)
			require.NoError(t, err)
			ref, err := arena.InitializeOwned(0, key, "encoded", testValue{}, nil)
			require.NoError(t, err)
			require.NoError(t, ref.Bind(key, "encoded", incremental.ExactValueRoot{}, root))

			poison(&arena.slots[0])
			require.Error(t, ref.Validate(key, "encoded", root, root))
		})
	}
}

func TestArenaRevocationClearsOwnedSlotsAndInvalidatesEveryRef(t *testing.T) {
	arena, err := NewArena[testValue, testMetadata](2, 3)
	require.NoError(t, err)
	metadata := testMetadata{component: "component", allowed: true}
	refs := make([]*Ref[testValue, testMetadata], 2)
	roots := make([]incremental.ExactValueRoot, 2)
	for index := range refs {
		key := incremental.NewQueryKey(string(rune('a' + index)))
		encoded := string(rune('x' + index))
		roots[index] = testExactRoot(t, key, encoded)
		refs[index], err = arena.InitializeOwned(
			index,
			key,
			encoded,
			testValue{text: "owned", bytes: []byte("owned")},
			&metadata,
		)
		require.NoError(t, err)
		require.NoError(t, refs[index].Bind(
			key, encoded, incremental.ExactValueRoot{}, roots[index],
		))
	}

	arena.Revoke()
	arena.Revoke()
	assert.True(t, arena.revoked)
	for index := range arena.slots {
		slot := &arena.slots[index]
		assert.Equal(t, arenaSlotEmpty, slot.state)
		assert.Equal(t, testValue{}, slot.value)
		assert.Equal(t, testMetadata{}, slot.metadata)
		assert.Equal(t, incremental.ExactValueRoot{}, slot.root)
		assert.False(t, slot.hasMetadata)
		key := incremental.NewQueryKey(string(rune('a' + index)))
		encoded := string(rune('x' + index))
		require.Error(t, refs[index].Validate(key, encoded, roots[index], roots[index]))
	}
	_, err = arena.InitializeOwned(
		0, incremental.NewQueryKey("new"), "new", testValue{}, nil,
	)
	require.Error(t, err)
}

func TestArenaConcurrentInitializationHasOneSlotWinner(t *testing.T) {
	arena, err := NewArena[testValue, testMetadata](1, 1)
	require.NoError(t, err)
	key := incremental.NewQueryKey("query")
	start := make(chan struct{})
	results := make(chan *Ref[testValue, testMetadata], 64)
	errorsFound := make(chan error, 64)
	var wait sync.WaitGroup
	for index := range 64 {
		wait.Go(func() {
			<-start
			ref, initializeErr := arena.InitializeOwned(
				0, key, "encoded", testValue{text: string(rune(index + 1))}, nil,
			)
			results <- ref
			errorsFound <- initializeErr
		})
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsFound)

	winners := 0
	var winner *Ref[testValue, testMetadata]
	for ref := range results {
		if ref != nil {
			winners++
			winner = ref
		}
	}
	failures := 0
	for initializeErr := range errorsFound {
		if initializeErr != nil {
			failures++
		}
	}
	assert.Equal(t, 1, winners)
	assert.Equal(t, 63, failures)
	assert.Same(t, &arena.slots[0].ref, winner)
}

func TestArenaConcurrentTakeTransfersOwnershipOnce(t *testing.T) {
	key := incremental.NewQueryKey("query")
	root := testExactRoot(t, key, "encoded")
	arena, err := NewArena[testValue, testMetadata](1, 1)
	require.NoError(t, err)
	ref, err := arena.InitializeOwned(
		0, key, "encoded", testValue{text: "owned", bytes: []byte("owned")}, nil,
	)
	require.NoError(t, err)
	require.NoError(t, ref.Bind(key, "encoded", incremental.ExactValueRoot{}, root))

	start := make(chan struct{})
	var successes atomic.Int64
	var failures atomic.Int64
	var wait sync.WaitGroup
	for range 64 {
		wait.Go(func() {
			<-start
			value, takeErr := ref.Take(key, "encoded", root, root)
			if takeErr != nil {
				failures.Add(1)
				return
			}
			if value.text == "owned" && string(value.bytes) == "owned" {
				successes.Add(1)
			}
		})
	}
	close(start)
	wait.Wait()
	assert.Equal(t, int64(1), successes.Load())
	assert.Equal(t, int64(63), failures.Load())
}

func TestArenaTakeManyTransfersCompleteRangeAtomically(t *testing.T) {
	arena, err := NewArena[testValue, testMetadata](3, 11)
	require.NoError(t, err)
	requests := make([]TakeRequest[testValue, testMetadata], 3)
	for index := range requests {
		key := incremental.NewQueryKey(string(rune('a' + index)))
		encoded := string(rune('x' + index))
		root := testExactRoot(t, key, encoded)
		ref, initializeErr := arena.InitializeOwned(
			index,
			key,
			encoded,
			testValue{text: encoded, bytes: []byte(encoded)},
			nil,
		)
		require.NoError(t, initializeErr)
		require.NoError(t, ref.Bind(key, encoded, incremental.ExactValueRoot{}, root))
		requests[index] = TakeRequest[testValue, testMetadata]{
			Ref: ref, Key: key, Encoded: encoded, OwnerRoot: root, Root: root,
		}
	}
	requests[0], requests[2] = requests[2], requests[0]

	values, err := TakeMany(requests)
	require.NoError(t, err)
	assert.Equal(t, []string{"z", "y", "x"}, []string{values[0].text, values[1].text, values[2].text})
	for _, request := range requests {
		_, err := request.Ref.Take(
			request.Key,
			request.Encoded,
			request.OwnerRoot,
			request.Root,
		)
		require.ErrorContains(t, err, "already transferred")
	}
}

func TestArenaTakeManyPoisonLeavesEverySlotOwned(t *testing.T) {
	tests := map[string]func([]TakeRequest[testValue, testMetadata]){
		"wrong root": func(requests []TakeRequest[testValue, testMetadata]) {
			requests[1].Root = testExactRoot(t, requests[1].Key, requests[1].Encoded)
		},
		"duplicate slot": func(requests []TakeRequest[testValue, testMetadata]) {
			requests[1] = requests[0]
		},
		"foreign arena": func(requests []TakeRequest[testValue, testMetadata]) {
			foreign, err := NewArena[testValue, testMetadata](1, 13)
			require.NoError(t, err)
			ref, err := foreign.InitializeOwned(
				0,
				requests[1].Key,
				requests[1].Encoded,
				testValue{text: "foreign"},
				nil,
			)
			require.NoError(t, err)
			requests[1].Ref = ref
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			arena, err := NewArena[testValue, testMetadata](2, 12)
			require.NoError(t, err)
			requests := make([]TakeRequest[testValue, testMetadata], 2)
			for index := range requests {
				key := incremental.NewQueryKey(string(rune('a' + index)))
				encoded := string(rune('x' + index))
				root := testExactRoot(t, key, encoded)
				ref, err := arena.InitializeOwned(
					index,
					key,
					encoded,
					testValue{text: encoded, bytes: []byte(encoded)},
					nil,
				)
				require.NoError(t, err)
				require.NoError(t, ref.Bind(key, encoded, incremental.ExactValueRoot{}, root))
				requests[index] = TakeRequest[testValue, testMetadata]{
					Ref: ref, Key: key, Encoded: encoded, OwnerRoot: root, Root: root,
				}
			}
			poison(requests)
			_, err = TakeMany(requests)
			require.Error(t, err)
			for index := range arena.slots {
				assert.Equal(t, arenaSlotBound, arena.slots[index].state)
				assert.NotEqual(t, testValue{}, arena.slots[index].value)
			}
		})
	}
}

func TestArenaConcurrentBindPublishesOneExactRoot(t *testing.T) {
	key := incremental.NewQueryKey("query")
	roots := []incremental.ExactValueRoot{
		testExactRoot(t, key, "encoded"),
		testExactRoot(t, key, "encoded"),
	}
	arena, err := NewArena[testValue, testMetadata](1, 1)
	require.NoError(t, err)
	ref, err := arena.InitializeOwned(0, key, "encoded", testValue{}, nil)
	require.NoError(t, err)

	start := make(chan struct{})
	winners := make(chan incremental.ExactValueRoot, 64)
	var wait sync.WaitGroup
	for index := range 64 {
		wait.Go(func() {
			<-start
			root := roots[index%len(roots)]
			if bindErr := ref.Bind(key, "encoded", incremental.ExactValueRoot{}, root); bindErr == nil {
				winners <- root
			}
		})
	}
	close(start)
	wait.Wait()
	close(winners)

	var winner incremental.ExactValueRoot
	winnerCount := 0
	for root := range winners {
		winner = root
		winnerCount++
	}
	require.Equal(t, 1, winnerCount)
	require.NoError(t, ref.Validate(key, "encoded", winner, winner))
	loser := roots[0]
	same, err := loser.SameRoot(winner)
	require.NoError(t, err)
	if same {
		loser = roots[1]
	}
	require.Error(t, ref.Validate(key, "encoded", winner, loser))
}

func TestArenaBindManyPoisonLeavesEveryRequestedSlotPending(t *testing.T) {
	arena, err := NewArena[testValue, testMetadata](2, 31)
	require.NoError(t, err)
	initialize := []InitializeRequest[testValue, testMetadata]{
		{
			Index: 0, Key: incremental.NewQueryKey("left"), Encoded: "left",
			Value: testValue{text: "left", bytes: []byte("left")},
		},
		{
			Index: 1, Key: incremental.NewQueryKey("right"), Encoded: "right",
			Value: testValue{text: "right", bytes: []byte("right")},
		},
	}
	refs, err := arena.InitializeOwnedMany(initialize)
	require.NoError(t, err)
	requests := make([]BindRequest[testValue, testMetadata], len(refs))
	for index := range refs {
		requests[index] = BindRequest[testValue, testMetadata]{
			Ref: refs[index], Key: initialize[index].Key, Encoded: initialize[index].Encoded,
			Root: testExactRoot(t, initialize[index].Key, initialize[index].Encoded),
		}
	}
	validRightRoot := requests[1].Root
	requests[1].Root = testExactRoot(t, requests[1].Key, "poison")

	require.Error(t, BindMany(requests))
	for index := range arena.slots {
		assert.Equal(t, arenaSlotPending, arena.slots[index].state)
		assert.Equal(t, incremental.ExactValueRoot{}, arena.slots[index].root)
	}

	requests[1].Root = validRightRoot
	require.NoError(t, BindMany(requests))
	for index := range arena.slots {
		assert.Equal(t, arenaSlotBound, arena.slots[index].state)
		requests[index].OwnerRoot = requests[index].Root
	}
	require.NoError(t, BindMany(requests))
}

func TestArenaBindManyForgedFinalRefLeavesEveryRequestedSlotPending(t *testing.T) {
	arena, err := NewArena[testValue, testMetadata](2, 35)
	require.NoError(t, err)
	initialize := []InitializeRequest[testValue, testMetadata]{
		{Index: 0, Key: incremental.NewQueryKey("left"), Encoded: "left"},
		{Index: 1, Key: incremental.NewQueryKey("right"), Encoded: "right"},
	}
	refs, err := arena.InitializeOwnedMany(initialize)
	require.NoError(t, err)
	requests := make([]BindRequest[testValue, testMetadata], len(refs))
	for index := range refs {
		requests[index] = BindRequest[testValue, testMetadata]{
			Ref: refs[index], Key: initialize[index].Key, Encoded: initialize[index].Encoded,
			Root: testExactRoot(t, initialize[index].Key, initialize[index].Encoded),
		}
	}
	forged := *refs[1]
	forged.seal = &forged
	requests[1].Ref = &forged

	require.ErrorContains(t, BindMany(requests), "invalid provenance")
	for index := range arena.slots {
		assert.Equal(t, arenaSlotPending, arena.slots[index].state)
		assert.Equal(t, incremental.ExactValueRoot{}, arena.slots[index].root)
	}
}

func TestArenaConcurrentBindManyPublishesOneCompleteWave(t *testing.T) {
	arena, err := NewArena[testValue, testMetadata](2, 37)
	require.NoError(t, err)
	initialize := []InitializeRequest[testValue, testMetadata]{
		{Index: 0, Key: incremental.NewQueryKey("left"), Encoded: "left"},
		{Index: 1, Key: incremental.NewQueryKey("right"), Encoded: "right"},
	}
	refs, err := arena.InitializeOwnedMany(initialize)
	require.NoError(t, err)
	waves := make([][]BindRequest[testValue, testMetadata], 2)
	for waveIndex := range waves {
		waves[waveIndex] = make([]BindRequest[testValue, testMetadata], len(refs))
		for index := range refs {
			waves[waveIndex][index] = BindRequest[testValue, testMetadata]{
				Ref: refs[index], Key: initialize[index].Key, Encoded: initialize[index].Encoded,
				Root: testExactRoot(t, initialize[index].Key, initialize[index].Encoded),
			}
		}
	}

	start := make(chan struct{})
	var successes atomic.Int64
	var wait sync.WaitGroup
	for waveIndex := range waves {
		wait.Go(func() {
			<-start
			if BindMany(waves[waveIndex]) == nil {
				successes.Add(1)
			}
		})
	}
	close(start)
	wait.Wait()
	require.Equal(t, int64(1), successes.Load())

	firstWaveLeft, err := arena.slots[0].root.SameRoot(waves[0][0].Root)
	require.NoError(t, err)
	firstWaveRight, err := arena.slots[1].root.SameRoot(waves[0][1].Root)
	require.NoError(t, err)
	secondWaveLeft, err := arena.slots[0].root.SameRoot(waves[1][0].Root)
	require.NoError(t, err)
	secondWaveRight, err := arena.slots[1].root.SameRoot(waves[1][1].Root)
	require.NoError(t, err)
	assert.True(t, firstWaveLeft && firstWaveRight || secondWaveLeft && secondWaveRight)
}

func TestArenaRevocationFencesActiveMaterialization(t *testing.T) {
	key := incremental.NewQueryKey("query")
	root := testExactRoot(t, key, "encoded")
	arena, err := NewArena[testValue, testMetadata](1, 1)
	require.NoError(t, err)
	ref, err := arena.InitializeOwned(
		0, key, "encoded", testValue{text: "owned", bytes: []byte("owned")}, nil,
	)
	require.NoError(t, err)
	require.NoError(t, ref.Bind(key, "encoded", incremental.ExactValueRoot{}, root))

	entered := make(chan struct{})
	release := make(chan struct{})
	materialized := make(chan testValue, 1)
	materializeErr := make(chan error, 1)
	go func() {
		value, cloneErr := ref.Materialize(
			key,
			"encoded",
			root,
			root,
			func(value *testValue) testValue {
				close(entered)
				<-release
				return cloneTestValue(value)
			},
		)
		materialized <- value
		materializeErr <- cloneErr
	}()
	<-entered
	revoked := make(chan struct{})
	go func() {
		arena.Revoke()
		close(revoked)
	}()
	close(release)
	value := <-materialized
	require.NoError(t, <-materializeErr)
	assert.Equal(t, "owned", value.text)
	assert.Equal(t, "owned", string(value.bytes))
	<-revoked
	require.Error(t, ref.Validate(key, "encoded", root, root))
	assert.Equal(t, testValue{}, arena.slots[0].value)
}

func TestArenaConcurrentReadersAndRevocationFailClosed(t *testing.T) {
	key := incremental.NewQueryKey("query")
	root := testExactRoot(t, key, "encoded")
	arena, err := NewArena[testValue, testMetadata](1, 1)
	require.NoError(t, err)
	ref, err := arena.InitializeOwned(
		0, key, "encoded", testValue{text: "owned", bytes: []byte("owned")}, nil,
	)
	require.NoError(t, err)
	require.NoError(t, ref.Bind(key, "encoded", incremental.ExactValueRoot{}, root))

	start := make(chan struct{})
	errorsFound := make(chan error, 128)
	var wait sync.WaitGroup
	for range 64 {
		wait.Go(func() {
			<-start
			errorsFound <- ref.Validate(key, "encoded", root, root)
		})
		wait.Go(func() {
			<-start
			_, materializeErr := ref.Materialize(key, "encoded", root, root, cloneTestValue)
			errorsFound <- materializeErr
		})
	}
	close(start)
	arena.Revoke()
	wait.Wait()
	close(errorsFound)
	for operationErr := range errorsFound {
		if operationErr != nil {
			assert.ErrorContains(t, operationErr, "invalid provenance")
		}
	}
	require.Error(t, ref.Validate(key, "encoded", root, root))
}
