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

package rendercycle

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOccurrenceBindsOneExactCycleExecution(t *testing.T) {
	fixture := newCycleFixture(t)
	effects := newEffectSnapshots(t, "stable", 1)
	snapshot := mustCycleSnapshot(
		t, fixture.cycleAuthority, fixture.newOutput(t, "global\n", nil), effects, nil,
	)

	first, err := NewOccurrence(snapshot)
	require.NoError(t, err)
	second, err := NewOccurrence(snapshot)
	require.NoError(t, err)
	require.NoError(t, first.ValidateAuthentication())

	bound, err := first.Snapshot()
	require.NoError(t, err)
	assert.Same(t, snapshot, bound)
	firstProof, err := first.Proof()
	require.NoError(t, err)
	secondProof, err := second.Proof()
	require.NoError(t, err)
	assert.NotEmpty(t, firstProof)
	assert.NotEqual(t, firstProof, secondProof)

	same, err := first.Same(first)
	require.NoError(t, err)
	assert.True(t, same)
	same, err = first.Same(second)
	require.NoError(t, err)
	assert.False(t, same, "two executions of the same cycle are different occurrences")
}

func TestOccurrenceRejectsCopiesAndSubstitution(t *testing.T) {
	fixture := newCycleFixture(t)
	aEffects := newEffectSnapshots(t, "a", 1)
	bEffects := newEffectSnapshots(t, "b", 1)
	a := mustCycleSnapshot(
		t, fixture.cycleAuthority, fixture.newOutput(t, "global\n", nil), aEffects, nil,
	)
	b := mustCycleSnapshot(
		t, fixture.cycleAuthority, fixture.newOutput(t, "global\n  daemon\n", nil), bEffects, nil,
	)
	occurrence, err := NewOccurrence(a)
	require.NoError(t, err)

	shallow := *occurrence
	require.ErrorIs(t, shallow.ValidateAuthentication(), errInvalidOccurrence)
	require.ErrorIs(t, (*Occurrence)(nil).ValidateAuthentication(), errInvalidOccurrence)

	originalSnapshot := occurrence.snapshot
	occurrence.snapshot = b
	require.ErrorIs(t, occurrence.ValidateAuthentication(), errInvalidOccurrence)
	occurrence.snapshot = originalSnapshot
	require.NoError(t, occurrence.ValidateAuthentication())

	originalProof := occurrence.proof
	occurrence.proof = "r:poison"
	require.ErrorIs(t, occurrence.ValidateAuthentication(), errInvalidOccurrence)
	occurrence.proof = originalProof
	require.NoError(t, occurrence.ValidateAuthentication())

	originalSeal := occurrence.seal
	occurrence.seal = nil
	require.ErrorIs(t, occurrence.ValidateAuthentication(), errInvalidOccurrence)
	occurrence.seal = originalSeal
	require.NoError(t, occurrence.ValidateAuthentication())
}

func TestOccurrenceProofsAreUniqueUnderConcurrency(t *testing.T) {
	fixture := newCycleFixture(t)
	effects := newEffectSnapshots(t, "stable", 1)
	snapshot := mustCycleSnapshot(
		t, fixture.cycleAuthority, fixture.newOutput(t, "global\n", nil), effects, nil,
	)

	const count = 1024
	proofs := make(chan string, count)
	errors := make(chan error, count)
	var group sync.WaitGroup
	group.Add(count)
	for range count {
		go func() {
			defer group.Done()
			occurrence, err := NewOccurrence(snapshot)
			if err != nil {
				errors <- err
				return
			}
			proof, err := occurrence.Proof()
			if err != nil {
				errors <- err
				return
			}
			proofs <- proof
		}()
	}
	group.Wait()
	close(proofs)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}

	seen := make(map[string]struct{}, count)
	for proof := range proofs {
		_, duplicate := seen[proof]
		assert.False(t, duplicate)
		seen[proof] = struct{}{}
	}
	assert.Len(t, seen, count)
}

func TestNewOccurrenceRejectsInvalidCycle(t *testing.T) {
	_, err := NewOccurrence(nil)
	require.ErrorIs(t, err, errInvalidOccurrence)
	_, err = NewOccurrence(&Snapshot{})
	require.ErrorIs(t, err, errInvalidOccurrence)
}
