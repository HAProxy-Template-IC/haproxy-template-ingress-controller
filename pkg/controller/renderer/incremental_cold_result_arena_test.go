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
	"encoding/json"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer/internal/resultauthority"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func TestColdResultArenaAuthenticatesSlotsAndHTTPEffects(t *testing.T) {
	keys := []incremental.QueryKey{incremental.NewQueryKey("b")}
	arena, session := newTestIncrementalColdResultArena(t, []int{7}, keys)
	effects := authenticatedFreshComponentEffects{component: "component", source: "routes", name: "route"}
	httpEffects := []incrementalHTTPEffect{{inputID: 17}}
	fresh, err := arena.initialize(
		0,
		keys[0],
		&incrementalComponentResult{Text: "value\n"},
		effects,
		httpEffects,
	)
	require.NoError(t, err)
	require.Same(t, &arena.fresh[0], fresh)
	require.NoError(t, validatePendingAuthenticatedFreshComponentResult(fresh, keys[0]))

	root := testExactRoot(t, keys[0], []byte(fresh.encoded))
	require.NoError(t, bindAuthenticatedFreshComponentResult(fresh, keys[0], root))
	require.NoError(t, validateAuthenticatedFreshComponentResult(fresh, keys[0], root))
	certified, err := validateAuthenticatedFreshComponentEffects(
		fresh,
		keys[0],
		root,
		&incrementalComponent{name: "component"},
		"routes",
		"",
		"route",
	)
	require.NoError(t, err)
	assert.True(t, certified)

	transferred, err := arena.takeHTTPEffectsMany()
	require.NoError(t, err)
	require.Len(t, transferred, 1)
	require.Len(t, transferred[0], 1)
	assert.Equal(t, uint64(17), transferred[0][0].inputID)
	_, err = arena.takeHTTPEffectsMany()
	require.ErrorContains(t, err, "unavailable")

	session.graphSession = nil
	require.ErrorContains(t,
		validateAuthenticatedFreshComponentResult(fresh, keys[0], root),
		"arena",
	)
}

func TestColdResultArenaRejectsOwnedValuePoison(t *testing.T) {
	tests := map[string]struct {
		result incrementalComponentResult
		poison func(*incrementalComponentResult)
	}{
		"text": {
			result: incrementalComponentResult{Text: "original\n"},
			poison: func(result *incrementalComponentResult) { result.Text = "poison\n" },
		},
		"published bytes": {
			result: incrementalComponentResult{Published: []incrementalPublishedValue{{
				Cell: "cell", Key: "key", Value: json.RawMessage(`{"value":"original"}`),
			}}},
			poison: func(result *incrementalComponentResult) { result.Published[0].Value[10] = 'p' },
		},
		"backend plan nested keys": {
			result: incrementalComponentResult{BackendPlan: []incrementalBackendPlanCall{{
				WhenAny: &incrementalBackendPlanCondition{Cell: "cell", Keys: []string{"original"}},
			}}},
			poison: func(result *incrementalComponentResult) { result.BackendPlan[0].WhenAny.Keys[0] = "poison" },
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			key := incremental.NewQueryKey("query")
			arena, _ := newTestIncrementalColdResultArena(t, []int{0}, []incremental.QueryKey{key})
			fresh, err := arena.initialize(
				0, key, &test.result, authenticatedFreshComponentEffects{}, nil,
			)
			require.NoError(t, err)
			root := testExactRoot(t, key, []byte(fresh.encoded))
			require.NoError(t, bindAuthenticatedFreshComponentResult(fresh, key, root))

			arena.ownershipMu.Lock()
			test.poison(&arena.owned[0].result)
			arena.ownershipMu.Unlock()

			require.NoError(t, validateAuthenticatedFreshComponentResult(fresh, key, root))
			_, err = takeAuthenticatedFreshComponentResult(fresh, key, root)
			require.ErrorContains(t, err, "value changed after encoding")
		})
	}
}

func TestColdResultArenaRejectsForgedMembership(t *testing.T) {
	keys := []incremental.QueryKey{incremental.NewQueryKey("a"), incremental.NewQueryKey("b")}
	tests := map[string]func(*authenticatedFreshComponentResult){
		"slot":       func(fresh *authenticatedFreshComponentResult) { fresh.arenaSlot = 1 },
		"generation": func(fresh *authenticatedFreshComponentResult) { fresh.arenaGen++ },
		"key":        func(fresh *authenticatedFreshComponentResult) { fresh.key = keys[1] },
		"ref":        func(fresh *authenticatedFreshComponentResult) { fresh.arenaRef = nil },
		"authority": func(fresh *authenticatedFreshComponentResult) {
			fresh.authority = resultauthority.NewOwned[
				incrementalComponentResult,
				authenticatedFreshComponentEffects,
			](
				incremental.NewQueryKey("poison"),
				"poison",
				incrementalComponentResult{},
				nil,
			)
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			arena, _ := newTestIncrementalColdResultArena(t, []int{0, 1}, keys)
			fresh, err := arena.initialize(
				0,
				keys[0],
				&incrementalComponentResult{Text: "value\n"},
				authenticatedFreshComponentEffects{},
				nil,
			)
			require.NoError(t, err)
			poison(fresh)
			require.Error(t, validatePendingAuthenticatedFreshComponentResult(fresh, keys[0]))
		})
	}
}

func TestColdResultArenaRevocationInvalidatesPartialWave(t *testing.T) {
	keys := []incremental.QueryKey{incremental.NewQueryKey("a"), incremental.NewQueryKey("b")}
	arena, _ := newTestIncrementalColdResultArena(t, []int{0, 1}, keys)
	fresh, err := arena.initialize(
		0,
		keys[0],
		&incrementalComponentResult{Text: "value\n"},
		authenticatedFreshComponentEffects{},
		nil,
	)
	require.NoError(t, err)

	arena.revoke()
	arena.revoke()
	require.Error(t, validatePendingAuthenticatedFreshComponentResult(fresh, keys[0]))
	_, err = arena.initialize(
		1,
		keys[1],
		&incrementalComponentResult{},
		authenticatedFreshComponentEffects{},
		nil,
	)
	require.ErrorContains(t, err, "provenance")
}

func TestColdResultArenaConcurrentInitializationAndRevocationPublishesNothing(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		key := incremental.NewQueryKey("query")
		arena, _ := newTestIncrementalColdResultArena(t, []int{0}, []incremental.QueryKey{key})
		start := make(chan struct{})
		result := make(chan *authenticatedFreshComponentResult, 1)
		errResult := make(chan error, 1)
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			fresh, err := arena.initialize(
				0,
				key,
				&incrementalComponentResult{Text: "value"},
				authenticatedFreshComponentEffects{},
				[]incrementalHTTPEffect{{inputID: 17}},
			)
			result <- fresh
			errResult <- err
		}()
		go func() {
			defer workers.Done()
			<-start
			arena.revoke()
		}()
		close(start)
		workers.Wait()

		fresh := <-result
		err := <-errResult
		if err == nil {
			require.Error(t, validatePendingAuthenticatedFreshComponentResult(fresh, key))
		}
		assert.True(t, arena.revoked.Load())
		assert.Equal(t, incrementalComponentResult{}, arena.owned[0].result)
		assert.Nil(t, arena.owned[0].httpEffects)
	}
}

func TestColdResultArenaRejectsDuplicateQueries(t *testing.T) {
	key := incremental.NewQueryKey("query")
	graph, err := incremental.New()
	require.NoError(t, err)
	graphSession, err := graph.BeginColdReset()
	require.NoError(t, err)
	session := &incrementalRenderSession{graphSession: graphSession}

	_, err = newIncrementalColdResultArena(
		session,
		0,
		[]int{0, 1},
		[]incremental.QueryKey{key, key},
	)
	require.ErrorContains(t, err, "repeats a query")
}

func TestColdResultArenaInstallPreflightsEverySlot(t *testing.T) {
	keys := []incremental.QueryKey{incremental.NewQueryKey("a"), incremental.NewQueryKey("b")}
	arena, session := newTestIncrementalColdResultArena(t, []int{0, 1}, keys)
	for index := range keys {
		fresh, err := arena.initialize(
			index,
			keys[index],
			&incrementalComponentResult{Text: keys[index].Opaque()},
			authenticatedFreshComponentEffects{},
			[]incrementalHTTPEffect{{inputID: uint64(index + 1)}},
		)
		require.NoError(t, err)
		root := testExactRoot(t, keys[index], []byte(fresh.encoded))
		require.NoError(t, bindAuthenticatedFreshComponentResult(fresh, keys[index], root))
	}
	arena.fresh[1].arenaGen++
	err := session.installColdResultArena(arena)
	require.ErrorContains(t, err, "provenance")
	assert.Empty(t, session.freshResults)
	assert.Empty(t, session.httpExecuted)
}

func TestColdResultArenaBulkTransferPreflightsDestination(t *testing.T) {
	keys := []incremental.QueryKey{incremental.NewQueryKey("a"), incremental.NewQueryKey("b")}
	arena, _ := newTestIncrementalColdResultArena(t, []int{0, 1}, keys)
	fresh := make([]*authenticatedFreshComponentResult, len(keys))
	roots := make([]incremental.ExactValueRoot, len(keys))
	for index := range keys {
		var err error
		fresh[index], err = arena.initialize(
			index,
			keys[index],
			&incrementalComponentResult{Text: keys[index].Opaque()},
			authenticatedFreshComponentEffects{},
			nil,
		)
		require.NoError(t, err)
		roots[index] = testExactRoot(t, keys[index], []byte(fresh[index].encoded))
		require.NoError(t, bindAuthenticatedFreshComponentResult(fresh[index], keys[index], roots[index]))
	}
	destination := make([]incrementalComponentResult, 3)

	err := arena.takeManyInto(fresh, keys, roots, destination, []int{2, 2})
	require.ErrorContains(t, err, "destination")
	assert.Equal(t, make([]incrementalComponentResult, 3), destination)
	for index := range keys {
		require.NoError(t, validateAuthenticatedFreshComponentResult(fresh[index], keys[index], roots[index]))
	}
	originalEncoded := fresh[1].encoded
	fresh[1].encoded = "poison"
	err = arena.takeManyInto(fresh, keys, roots, destination, []int{0, 2})
	require.Error(t, err)
	fresh[1].encoded = originalEncoded
	assert.Equal(t, make([]incrementalComponentResult, 3), destination)
	for index := range keys {
		require.NoError(t, validateAuthenticatedFreshComponentResult(fresh[index], keys[index], roots[index]))
	}

	require.NoError(t, arena.takeManyInto(fresh, keys, roots, destination, []int{0, 2}))
	assert.Equal(t, "a", destination[0].Text)
	assert.Equal(t, incrementalComponentResult{}, destination[1])
	assert.Equal(t, "b", destination[2].Text)
	for index := range keys {
		_, err := takeAuthenticatedFreshComponentResult(fresh[index], keys[index], roots[index])
		require.ErrorContains(t, err, "already transferred")
	}
}

func TestColdResultArenaBindCompletedPoisonLeavesEverySlotPending(t *testing.T) {
	keys := []incremental.QueryKey{incremental.NewQueryKey("a"), incremental.NewQueryKey("b")}
	arena, _ := newTestIncrementalColdResultArena(t, []int{0, 1}, keys)
	values := make(map[incremental.QueryKey]string, len(keys))
	for index := range keys {
		fresh, err := arena.initialize(
			index,
			keys[index],
			&incrementalComponentResult{Text: keys[index].Opaque()},
			authenticatedFreshComponentEffects{},
			nil,
		)
		require.NoError(t, err)
		values[keys[index]] = fresh.encoded
	}
	_, roots := testExactRoots(t, values)
	poison := testExactRoot(t, keys[1], []byte("poison"))
	results := []incremental.ExactResult{
		{Key: keys[0], Value: roots[keys[0]]},
		{Key: keys[1], Value: poison},
	}

	require.ErrorContains(t, arena.bindCompleted(results), "authoritative value")
	for index := range keys {
		assert.Equal(t, incremental.ExactValueRoot{}, arena.fresh[index].root)
		require.NoError(t, validatePendingAuthenticatedFreshComponentResult(&arena.fresh[index], keys[index]))
	}

	results[1].Value = roots[keys[1]]
	require.NoError(t, arena.bindCompleted(results))
	for index := range keys {
		require.NoError(t, validateAuthenticatedFreshComponentResult(
			&arena.fresh[index], keys[index], results[index].Value,
		))
	}
}

func TestColdResultArenaInitializeStagedManyRejectsPartialWave(t *testing.T) {
	keys := []incremental.QueryKey{incremental.NewQueryKey("a"), incremental.NewQueryKey("b")}
	_, _, arena := newColdResultArenaFixture(t, keys)
	result := incrementalComponentResult{Text: "a"}
	require.NoError(t, arena.stageResult(
		0, keys[0], &result, authenticatedFreshComponentEffects{}, nil,
	))

	require.ErrorContains(t, arena.initializeStagedMany(), "slot 1")
	for index := range keys {
		assert.Equal(t, authenticatedFreshComponentResult{}, arena.fresh[index])
	}

	result = incrementalComponentResult{Text: "b"}
	require.NoError(t, arena.stageResult(
		1, keys[1], &result, authenticatedFreshComponentEffects{}, nil,
	))
	require.NoError(t, arena.initializeStagedMany())
	for index := range keys {
		require.NoError(t, validatePendingAuthenticatedFreshComponentResult(&arena.fresh[index], keys[index]))
	}
}

func TestColdResultArenaInitializeStagedManyRejectsForgedFinalSlot(t *testing.T) {
	keys := []incremental.QueryKey{incremental.NewQueryKey("a"), incremental.NewQueryKey("b")}
	_, _, arena := newStagedColdResultArenaCompletionFixture(t, keys)
	generation := arena.stage[1].generation
	arena.stage[1].generation++

	require.ErrorContains(t, arena.initializeStagedMany(), "slot 1")
	for index := range keys {
		assert.Equal(t, authenticatedFreshComponentResult{}, arena.fresh[index])
	}

	arena.stage[1].generation = generation
	require.NoError(t, arena.initializeStagedMany())
	for index := range keys {
		require.NoError(t, validatePendingAuthenticatedFreshComponentResult(&arena.fresh[index], keys[index]))
	}
}

func TestColdResultArenaRevocationClearsPartialStaging(t *testing.T) {
	keys := []incremental.QueryKey{incremental.NewQueryKey("a"), incremental.NewQueryKey("b")}
	_, _, arena := newColdResultArenaFixture(t, keys)
	result := incrementalComponentResult{Text: "a"}
	require.NoError(t, arena.stageResult(
		0,
		keys[0],
		&result,
		authenticatedFreshComponentEffects{},
		[]incrementalHTTPEffect{{inputID: 17}},
	))

	arena.revoke()
	assert.Equal(t, incrementalColdResultArenaValue{}, arena.owned[0])
	assert.Empty(t, arena.encoded[0])
	assert.Equal(t, authenticatedFreshComponentEffects{}, arena.metadata[0])
	assert.Equal(t, uint32(incrementalColdResultArenaSlotEmpty), arena.states[0].Load())
	require.ErrorContains(t, arena.initializeStagedMany(), "provenance")
}

func TestColdResultArenaCompleteWavePoisonHasNoValidPrefix(t *testing.T) {
	keys := []incremental.QueryKey{incremental.NewQueryKey("a"), incremental.NewQueryKey("b")}
	graph, session, arena := newColdResultArenaCompletionFixture(t, keys)
	var completed []incremental.ExactResult

	results, err := session.graphSession.EvaluateAllColdExactBatch(t.Context(), func(
		_ context.Context,
		batch incremental.ColdExactBatch,
	) error {
		generation := arena.fresh[1].arenaGen
		arena.fresh[1].arenaGen++
		_, poisonErr := session.completeColdResultArenaWave(batch, arena, make([]bool, batch.Len()))
		require.ErrorContains(t, poisonErr, "provenance")
		arena.fresh[1].arenaGen = generation
		for index := range keys {
			require.NoError(t, validatePendingAuthenticatedFreshComponentResult(&arena.fresh[index], keys[index]))
		}

		var completeErr error
		completed, completeErr = session.completeColdResultArenaWave(batch, arena, make([]bool, batch.Len()))
		if completeErr != nil {
			return completeErr
		}
		return batch.SealWave(completed...)
	}, keys...)
	require.NoError(t, err)
	require.Len(t, results, len(keys))
	for index := range keys {
		same, sameErr := results[index].Value.SameRoot(completed[index].Value)
		require.NoError(t, sameErr)
		assert.True(t, same)
		require.NoError(t, graph.ValidateExactValue(keys[index], results[index].Value))
		require.NoError(t, validateAuthenticatedFreshComponentResult(
			&arena.fresh[index], keys[index], results[index].Value,
		))
	}
}

func TestColdResultArenaCompleteWaveConcurrentWinnerIsWhole(t *testing.T) {
	keys := []incremental.QueryKey{incremental.NewQueryKey("a"), incremental.NewQueryKey("b")}
	graph, session, arena := newColdResultArenaCompletionFixture(t, keys)
	var winner []incremental.ExactResult

	results, err := session.graphSession.EvaluateAllColdExactBatch(t.Context(), func(
		_ context.Context,
		batch incremental.ColdExactBatch,
	) error {
		type completion struct {
			results []incremental.ExactResult
			err     error
		}
		attempts := make([]completion, 2)
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(len(attempts))
		for index := range attempts {
			go func() {
				defer workers.Done()
				<-start
				attempts[index].results, attempts[index].err = session.completeColdResultArenaWave(
					batch, arena, make([]bool, batch.Len()),
				)
			}()
		}
		close(start)
		workers.Wait()

		successes := 0
		for index := range attempts {
			if attempts[index].err == nil {
				successes++
				winner = attempts[index].results
				continue
			}
			require.ErrorContains(t, attempts[index].err, "already has a value")
		}
		require.Equal(t, 1, successes)
		return batch.SealWave(winner...)
	}, keys...)
	require.NoError(t, err)
	require.Len(t, results, len(keys))
	require.Len(t, winner, len(keys))
	for index := range keys {
		same, sameErr := results[index].Value.SameRoot(winner[index].Value)
		require.NoError(t, sameErr)
		assert.True(t, same)
		require.NoError(t, graph.ValidateExactValue(keys[index], results[index].Value))
		require.NoError(t, validateAuthenticatedFreshComponentResult(
			&arena.fresh[index], keys[index], results[index].Value,
		))
	}
}

func TestColdResultArenaCompleteStagedWavePoisonHasNoValidPrefix(t *testing.T) {
	keys := []incremental.QueryKey{incremental.NewQueryKey("a"), incremental.NewQueryKey("b")}
	graph, session, arena := newStagedColdResultArenaCompletionFixture(t, keys)
	var completed []incremental.ExactResult

	results, err := session.graphSession.EvaluateAllColdExactBatch(t.Context(), func(
		_ context.Context,
		batch incremental.ColdExactBatch,
	) error {
		completedQueries := make([]bool, batch.Len())
		completedQueries[1] = true
		_, poisonErr := session.completeStagedColdResultArenaWave(batch, arena, completedQueries)
		require.ErrorContains(t, poisonErr, "unavailable")
		for index := range keys {
			assert.Equal(t, authenticatedFreshComponentResult{}, arena.fresh[index])
			assert.Equal(t, uint32(incrementalColdResultArenaSlotStaged), arena.states[index].Load())
		}

		var completeErr error
		completed, completeErr = session.completeStagedColdResultArenaWave(
			batch, arena, make([]bool, batch.Len()),
		)
		if completeErr != nil {
			return completeErr
		}
		return batch.SealWave(completed...)
	}, keys...)
	require.NoError(t, err)
	require.Len(t, results, len(keys))
	for index := range keys {
		same, sameErr := results[index].Value.SameRoot(completed[index].Value)
		require.NoError(t, sameErr)
		assert.True(t, same)
		require.NoError(t, graph.ValidateExactValue(keys[index], results[index].Value))
		require.NoError(t, validateAuthenticatedFreshComponentResult(
			&arena.fresh[index], keys[index], results[index].Value,
		))
	}
}

func TestColdResultArenaCompleteStagedWaveConcurrentWinnerIsWhole(t *testing.T) {
	keys := []incremental.QueryKey{incremental.NewQueryKey("a"), incremental.NewQueryKey("b")}
	graph, session, arena := newStagedColdResultArenaCompletionFixture(t, keys)
	var winner []incremental.ExactResult

	results, err := session.graphSession.EvaluateAllColdExactBatch(t.Context(), func(
		_ context.Context,
		batch incremental.ColdExactBatch,
	) error {
		type completion struct {
			results []incremental.ExactResult
			err     error
		}
		attempts := make([]completion, 2)
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(len(attempts))
		for index := range attempts {
			go func() {
				defer workers.Done()
				<-start
				attempts[index].results, attempts[index].err = session.completeStagedColdResultArenaWave(
					batch, arena, make([]bool, batch.Len()),
				)
			}()
		}
		close(start)
		workers.Wait()

		successes := 0
		for index := range attempts {
			if attempts[index].err == nil {
				successes++
				winner = attempts[index].results
				continue
			}
			require.Error(t, attempts[index].err)
		}
		require.Equal(t, 1, successes)
		return batch.SealWave(winner...)
	}, keys...)
	require.NoError(t, err)
	require.Len(t, results, len(keys))
	require.Len(t, winner, len(keys))
	for index := range keys {
		same, sameErr := results[index].Value.SameRoot(winner[index].Value)
		require.NoError(t, sameErr)
		assert.True(t, same)
		require.NoError(t, graph.ValidateExactValue(keys[index], results[index].Value))
		require.NoError(t, validateAuthenticatedFreshComponentResult(
			&arena.fresh[index], keys[index], results[index].Value,
		))
	}
}

func newColdResultArenaCompletionFixture(
	tb testing.TB,
	keys []incremental.QueryKey,
) (*incremental.Graph, *incrementalRenderSession, *incrementalColdResultArena) {
	tb.Helper()
	graph, session, arena := newColdResultArenaFixture(tb, keys)
	for index := range keys {
		_, err := arena.initialize(
			index,
			keys[index],
			&incrementalComponentResult{Text: keys[index].Opaque()},
			authenticatedFreshComponentEffects{},
			nil,
		)
		require.NoError(tb, err)
	}
	return graph, session, arena
}

func newStagedColdResultArenaCompletionFixture(
	tb testing.TB,
	keys []incremental.QueryKey,
) (*incremental.Graph, *incrementalRenderSession, *incrementalColdResultArena) {
	tb.Helper()
	graph, session, arena := newColdResultArenaFixture(tb, keys)
	for index := range keys {
		result := incrementalComponentResult{Text: keys[index].Opaque()}
		require.NoError(tb, arena.stageResult(
			index,
			keys[index],
			&result,
			authenticatedFreshComponentEffects{},
			nil,
		))
	}
	return graph, session, arena
}

func newColdResultArenaFixture(
	tb testing.TB,
	keys []incremental.QueryKey,
) (*incremental.Graph, *incrementalRenderSession, *incrementalColdResultArena) {
	tb.Helper()
	definitions := make([]incremental.Definition, len(keys))
	batchIndexes := make([]int, len(keys))
	for index := range keys {
		definitions[index] = incremental.Definition{
			Key: keys[index],
			Run: func(context.Context, incremental.Reader) ([]byte, error) {
				return nil, nil
			},
		}
		batchIndexes[index] = index
	}
	graph, err := incremental.New(definitions...)
	require.NoError(tb, err)
	graphSession, err := graph.BeginColdReset()
	require.NoError(tb, err)
	tb.Cleanup(graphSession.Abort)
	session := &incrementalRenderSession{
		state:        &incrementalRenderState{graph: graph},
		graphSession: graphSession,
		freshResults: map[incremental.QueryKey]*authenticatedFreshComponentResult{},
		httpExecuted: map[incremental.QueryKey][]incrementalHTTPEffect{},
	}
	arena, err := newIncrementalColdResultArena(session, 0, batchIndexes, keys)
	require.NoError(tb, err)
	return graph, session, arena
}

func newTestIncrementalColdResultArena(
	tb testing.TB,
	batchIndexes []int,
	keys []incremental.QueryKey,
) (*incrementalColdResultArena, *incrementalRenderSession) {
	tb.Helper()
	graph, err := incremental.New()
	require.NoError(tb, err)
	graphSession, err := graph.BeginColdReset()
	require.NoError(tb, err)
	session := &incrementalRenderSession{
		graphSession: graphSession,
		freshResults: map[incremental.QueryKey]*authenticatedFreshComponentResult{},
		httpExecuted: map[incremental.QueryKey][]incrementalHTTPEffect{},
	}
	arena, err := newIncrementalColdResultArena(session, 0, batchIndexes, keys)
	require.NoError(tb, err)
	return arena, session
}

func BenchmarkColdResultArenaSourceInitialization(b *testing.B) {
	const count = 256
	keys := make([]incremental.QueryKey, count)
	batchIndexes := make([]int, count)
	for index := range count {
		keys[index] = incremental.NewQueryKey("source-" + strconv.Itoa(index))
		batchIndexes[index] = index
	}
	graph, err := incremental.New()
	if err != nil {
		b.Fatal(err)
	}
	graphSession, err := graph.BeginColdReset()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(graphSession.Abort)
	session := &incrementalRenderSession{graphSession: graphSession}
	effects := authenticatedFreshComponentEffects{component: "component", source: "source"}

	b.Run("per_child", func(b *testing.B) {
		benchmarkColdResultArenaPerChild(b, session, batchIndexes, keys, effects)
	})
	b.Run("bulk", func(b *testing.B) {
		benchmarkColdResultArenaBulk(b, session, batchIndexes, keys, effects)
	})
}

func benchmarkColdResultArenaPerChild(
	b *testing.B,
	session *incrementalRenderSession,
	batchIndexes []int,
	keys []incremental.QueryKey,
	effects authenticatedFreshComponentEffects,
) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		arena, arenaErr := newIncrementalColdResultArena(session, 0, batchIndexes, keys)
		if arenaErr != nil {
			b.Fatal(arenaErr)
		}
		for index := range keys {
			_, initializeErr := arena.initialize(
				index,
				keys[index],
				&incrementalComponentResult{Text: "value"},
				effects,
				nil,
			)
			if initializeErr != nil {
				b.Fatal(initializeErr)
			}
		}
		arena.revoke()
	}
}

func benchmarkColdResultArenaBulk(
	b *testing.B,
	session *incrementalRenderSession,
	batchIndexes []int,
	keys []incremental.QueryKey,
	effects authenticatedFreshComponentEffects,
) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		arena, arenaErr := newIncrementalColdResultArena(session, 0, batchIndexes, keys)
		if arenaErr != nil {
			b.Fatal(arenaErr)
		}
		for index := range keys {
			result := incrementalComponentResult{Text: "value"}
			if stageErr := arena.stageResult(index, keys[index], &result, effects, nil); stageErr != nil {
				b.Fatal(stageErr)
			}
		}
		if initializeErr := arena.initializeStagedMany(); initializeErr != nil {
			b.Fatal(initializeErr)
		}
		arena.revoke()
	}
}
