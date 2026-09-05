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
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func TestIncrementalColdCarrierWavesHTTPRoute3000Shape(t *testing.T) {
	components := gatewayHTTPRouteCarrierComponents()
	state := newIncrementalCarrierTestState(components...)
	keys := make([]incremental.QueryKey, 0, 3000*13)
	for routeIndex := range 3000 {
		name := fmt.Sprintf("route-%04d", routeIndex)
		for _, component := range components[3:] {
			keys = append(keys, componentQueryKey(&component, "httproutes", "default", name))
		}
	}

	session, plan, graph := newIncrementalColdCarrierWaveTestPlan(t, state, keys)
	forward, err := session.planIncrementalColdCarrierWaves(plan, graph, 8)
	require.NoError(t, err)
	reversedKeys := slices.Clone(keys)
	slices.Reverse(reversedKeys)
	reversedSession, reversedPlan, reversedGraph := newIncrementalColdCarrierWaveTestPlan(t, state, reversedKeys)
	reversed, err := reversedSession.planIncrementalColdCarrierWaves(reversedPlan, reversedGraph, 8)
	require.NoError(t, err)
	assert.Equal(t, forward.waves, reversed.waves)
	assert.Equal(t, forward.workers, reversed.workers)

	require.Equal(t, 39000, forward.logicalQueries)
	require.Len(t, forward.waves, 4)
	require.Len(t, forward.workers, 8)
	for waveIndex, want := range []struct {
		groups        int
		lanes         int
		queries       int
		activeWorkers int
	}{
		{groups: 3, lanes: 0, queries: 0, activeWorkers: 0},
		{groups: 9, lanes: 9, queries: 27000, activeWorkers: 8},
		{groups: 1, lanes: 1, queries: 3000, activeWorkers: 8},
		{groups: 3, lanes: 3, queries: 9000, activeWorkers: 8},
	} {
		wave := forward.waves[waveIndex]
		assert.Len(t, wave.groups, want.groups)
		assert.Len(t, wave.lanes, want.lanes)
		assert.Equal(t, want.queries, wave.logicalQueries)
		assert.Equal(t, want.activeWorkers, wave.activeWorkers)
	}

	assertIncrementalColdCarrierWorkerAssignment(t, forward, graph)
	require.NoError(t, forward.validate(graph))

	copied := *forward
	require.ErrorContains(t, copied.validate(graph), "invalid authority")
	poisoned := cloneIncrementalColdCarrierWavesForTest(forward)
	poisoned.workers[0].waves[1].lanes[0].items[0].batchIndex++
	require.ErrorContains(t, poisoned.validate(graph), "invalid assignment")
	_, err = session.planIncrementalColdCarrierWaves(plan, graph, 0)
	require.ErrorContains(t, err, "incomplete")
}

func assertIncrementalColdCarrierWorkerAssignment(
	t *testing.T,
	forward *incrementalColdCarrierWaves,
	graph *incrementalColdCarrierGraphSchedule,
) {
	t.Helper()
	seen := make([]bool, len(graph.keys))
	for workerIndex := range forward.workers {
		worker := forward.workers[workerIndex]
		require.Len(t, worker.waves, 4)
		assert.Empty(t, worker.waves[0].lanes)
		assert.Len(t, worker.waves[1].lanes, 9)
		assert.Len(t, worker.waves[2].lanes, 1)
		assert.Len(t, worker.waves[3].lanes, 3)
		workerQueries := 0
		for waveIndex := 1; waveIndex < len(worker.waves); waveIndex++ {
			for _, lane := range worker.waves[waveIndex].lanes {
				assert.Len(t, lane.items, 375)
				for _, item := range lane.items {
					require.False(t, seen[item.batchIndex], "batch item %d repeated", item.batchIndex)
					seen[item.batchIndex] = true
					workerQueries++
				}
			}
		}
		assert.Equal(t, 4875, workerQueries)
	}
	assert.NotContains(t, seen, false)
}

func TestIncrementalColdCarrierWaveSourceWorkersAreDeterministic(t *testing.T) {
	_, wave := incrementalColdSourceTransactionHeterogeneousCRDWave()
	want, err := incrementalColdCarrierWaveSourceWorkers(&wave)
	require.NoError(t, err)

	reordered := cloneIncrementalColdCarrierPlannedWaveForTest(wave)
	slices.Reverse(reordered.lanes)
	for laneIndex := range reordered.lanes {
		slices.Reverse(reordered.lanes[laneIndex].items)
	}
	for range 32 {
		got, assignmentErr := incrementalColdCarrierWaveSourceWorkers(&reordered)
		require.NoError(t, assignmentErr)
		assert.Equal(t, want, got)
	}
}

func TestIncrementalColdCarrierWaveSourceWorkersLoseOrDuplicateNothing(t *testing.T) {
	_, wave := incrementalColdSourceTransactionHeterogeneousCRDWave()
	seenItems := make(map[int]int)
	sourceOwners := make(map[incrementalColdCarrierSourceKey]int)
	sourcesPerWorker := make([]int, wave.activeWorkers)
	for workerIndex := range wave.activeWorkers {
		lanes, err := incrementalColdCarrierExpectedWorkerWave(&wave, workerIndex)
		require.NoError(t, err)
		for laneIndex := range lanes {
			for itemIndex := range lanes[laneIndex].items {
				item := lanes[laneIndex].items[itemIndex]
				seenItems[item.batchIndex]++
				key := incrementalColdCarrierSourceKeyFor(item)
				owner, found := sourceOwners[key]
				if found {
					assert.Equal(t, owner, workerIndex, key)
					continue
				}
				sourceOwners[key] = workerIndex
				sourcesPerWorker[workerIndex]++
			}
		}
	}

	require.Len(t, seenItems, 202)
	for batchIndex := range 202 {
		assert.Equal(t, 1, seenItems[batchIndex], batchIndex)
	}
	assert.Len(t, sourceOwners, 200)
	assert.Equal(t, []int{100, 100}, sourcesPerWorker)
}

func TestIncrementalColdCarrierWaveSourceWorkersRejectMalformedTopology(t *testing.T) {
	_, err := incrementalColdCarrierExpectedWorkerWave(nil, 0)
	require.Error(t, err)
	for _, testCase := range []struct {
		name   string
		mutate func(*incrementalColdCarrierPlannedWave)
	}{
		{
			name: "malformed source",
			mutate: func(wave *incrementalColdCarrierPlannedWave) {
				wave.lanes[0].items[0].source = ""
			},
		},
		{
			name: "duplicate batch index",
			mutate: func(wave *incrementalColdCarrierPlannedWave) {
				wave.lanes[1].items[0].batchIndex = wave.lanes[0].items[0].batchIndex
			},
		},
		{
			name: "duplicate query",
			mutate: func(wave *incrementalColdCarrierPlannedWave) {
				duplicate := wave.lanes[0].items[0]
				duplicate.batchIndex = 202
				wave.lanes[0].items = append(wave.lanes[0].items, duplicate)
			},
		},
		{
			name: "more workers than sources",
			mutate: func(wave *incrementalColdCarrierPlannedWave) {
				wave.activeWorkers = 201
			},
		},
		{
			name: "negative workers",
			mutate: func(wave *incrementalColdCarrierPlannedWave) {
				wave.activeWorkers = -1
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, base := incrementalColdSourceTransactionHeterogeneousCRDWave()
			wave := cloneIncrementalColdCarrierPlannedWaveForTest(base)
			testCase.mutate(&wave)

			_, err := incrementalColdCarrierWaveSourceWorkers(&wave)
			require.Error(t, err)
			_, err = incrementalColdCarrierExpectedWorkerWave(&wave, 0)
			require.Error(t, err)
		})
	}
}

func TestIncrementalColdCarrierWavesKeepsEveryWorkerInEveryBarrier(t *testing.T) {
	producer := incrementalCarrierTestComponent(
		"producer", "producer", "routes", nil, nil, true, false, false, false,
	)
	consumer := incrementalCarrierTestComponent(
		"consumer", "consumer", "routes", []string{"producer"}, nil, false, false, false, false,
	)
	state := newIncrementalCarrierTestState(producer, consumer)
	keys := make([]incremental.QueryKey, 0, 801)
	keys = append(keys, componentQueryKey(&producer, "routes", "default", "producer"))
	for itemIndex := range 800 {
		keys = append(keys, componentQueryKey(
			&consumer,
			"routes",
			"default",
			fmt.Sprintf("consumer-%03d", itemIndex),
		))
	}
	session, plan, graph := newIncrementalColdCarrierWaveTestPlan(t, state, keys)
	waves, err := session.planIncrementalColdCarrierWaves(plan, graph, 8)
	require.NoError(t, err)
	require.Len(t, waves.waves, 2)
	assert.Equal(t, []int{1, 8}, []int{waves.waves[0].activeWorkers, waves.waves[1].activeWorkers})
	require.Len(t, waves.workers, 8)
	for workerIndex := range waves.workers {
		worker := waves.workers[workerIndex]
		require.Len(t, worker.waves, 2)
		if workerIndex == 0 {
			require.Len(t, worker.waves[0].lanes, 1)
			assert.Len(t, worker.waves[0].lanes[0].items, 1)
		} else {
			assert.Empty(t, worker.waves[0].lanes)
		}
		require.Len(t, worker.waves[1].lanes, 1)
		assert.Len(t, worker.waves[1].lanes[0].items, 100)
	}
	require.NoError(t, waves.validate(graph))
}

func cloneIncrementalColdCarrierPlannedWaveForTest(
	source incrementalColdCarrierPlannedWave,
) incrementalColdCarrierPlannedWave {
	cloned := source
	cloned.groups = slices.Clone(source.groups)
	cloned.lanes = slices.Clone(source.lanes)
	for laneIndex := range cloned.lanes {
		cloned.lanes[laneIndex].items = slices.Clone(source.lanes[laneIndex].items)
	}
	return cloned
}

func newIncrementalColdCarrierWaveTestPlan(
	tb testing.TB,
	state *incrementalRenderState,
	keys []incremental.QueryKey,
) (*incrementalRenderSession, *incrementalCarrierPlan, *incrementalColdCarrierGraphSchedule) {
	tb.Helper()
	session := &incrementalRenderSession{state: state}
	plan, err := session.planColdComponentCarrierKeys(keys)
	require.NoError(tb, err)
	groupOrder := make([]string, 0, len(state.groups))
	for _, stage := range plan.groupStages {
		groupOrder = append(groupOrder, stage.groups...)
	}
	keysByGroup := make(map[string][]incremental.QueryKey, len(groupOrder))
	for _, group := range groupOrder {
		keysByGroup[group] = nil
	}
	for _, key := range keys {
		component, ok := session.resolveQueryComponent(key)
		require.True(tb, ok)
		keysByGroup[component.group] = append(keysByGroup[component.group], key)
	}
	graph, err := newIncrementalColdCarrierGraphSchedule(plan, groupOrder, keys, keysByGroup)
	require.NoError(tb, err)
	return session, plan, graph
}

func cloneIncrementalColdCarrierWavesForTest(
	source *incrementalColdCarrierWaves,
) *incrementalColdCarrierWaves {
	cloned := *source
	cloned.seal = &cloned
	cloned.waves = slices.Clone(source.waves)
	for waveIndex := range cloned.waves {
		cloned.waves[waveIndex].groups = slices.Clone(source.waves[waveIndex].groups)
		cloned.waves[waveIndex].lanes = slices.Clone(source.waves[waveIndex].lanes)
		for laneIndex := range cloned.waves[waveIndex].lanes {
			cloned.waves[waveIndex].lanes[laneIndex].items = slices.Clone(source.waves[waveIndex].lanes[laneIndex].items)
		}
	}
	cloned.workers = slices.Clone(source.workers)
	for workerIndex := range cloned.workers {
		cloned.workers[workerIndex].waves = slices.Clone(source.workers[workerIndex].waves)
		for waveIndex := range cloned.workers[workerIndex].waves {
			cloned.workers[workerIndex].waves[waveIndex].lanes = slices.Clone(
				source.workers[workerIndex].waves[waveIndex].lanes,
			)
			for laneIndex := range cloned.workers[workerIndex].waves[waveIndex].lanes {
				cloned.workers[workerIndex].waves[waveIndex].lanes[laneIndex].items = slices.Clone(
					source.workers[workerIndex].waves[waveIndex].lanes[laneIndex].items,
				)
			}
		}
	}
	return &cloned
}
