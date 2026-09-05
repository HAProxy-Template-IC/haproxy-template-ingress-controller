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
	"testing"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

func TestPreparedEmptyGroupBatchSupportsPristineAndExplicitResetIndexes(t *testing.T) {
	component := incrementalComponent{name: "producer", group: "group", publishValue: true}
	tests := map[string]func() *incrementalGroupIndex{
		"pristine": newIncrementalGroupIndex,
		"explicit reset": func() *incrementalGroupIndex {
			base := newIncrementalStateSnapshot()
			base.groupIndexes[component.group] = populatedEmptyBatchTestIndex(t, &component)
			session := &incrementalRenderSession{
				state: &incrementalRenderState{
					components: map[string]incrementalComponent{component.name: component},
					groups:     map[string][]incrementalComponent{component.group: {component}},
				},
				base: base,
			}
			session.resetTransactions(true)
			return session.groupIndexes[component.group]
		},
	}
	for name, newIndex := range tests {
		t.Run(name, func(t *testing.T) {
			index := newIndex()
			empty, err := index.authenticatedStructurallyEmpty()
			require.NoError(t, err)
			require.True(t, empty)
			instance := rankedEmptyBatchTestInstance(t, &component, "route", "100", "value")
			updated, owned, err := index.addPreparedBatch([]incrementalPreparedGroupInstance{
				preparedMemoBatchCandidate(t, &component, &instance),
			})
			require.NoError(t, err)
			require.Len(t, owned, 1)
			assert.Equal(t, "value", emptyBatchTestWinner(t, updated))
			require.NoError(t, updated.validateAuthentication())
		})
	}
}

func TestPreparedEmptyGroupBatchRejectsPoisonedOrNonemptyProvenance(t *testing.T) {
	component := incrementalComponent{name: "producer", group: "group", publishValue: true}
	tests := map[string]func(*incrementalGroupIndex) *incrementalGroupIndex{
		"authentication": func(index *incrementalGroupIndex) *incrementalGroupIndex {
			poisoned := *index
			poisoned.outputs = iradix.New[incrementalComponentChunks]()
			return &poisoned
		},
		"structural emptiness": func(index *incrementalGroupIndex) *incrementalGroupIndex {
			poisoned := *index
			output, err := rendercontent.Empty().WithText("orphan", "poison")
			require.NoError(t, err)
			outputs := poisoned.outputs.Txn()
			outputs.Insert([]byte(component.name), incrementalComponentChunks{output: output})
			poisoned.outputs = outputs.Commit()
			poisoned.authenticate()
			return &poisoned
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			base := newIncrementalGroupIndex()
			instance := rankedEmptyBatchTestInstance(t, &component, "route", "100", "value")
			candidate := preparedMemoBatchCandidate(t, &component, &instance)
			batch, err := prepareIncrementalGroupBatch(base, []incrementalPreparedGroupInstance{candidate})
			require.NoError(t, err)
			poisoned := poison(base)
			before := *poisoned

			_, err = poisoned.addPreparedEmptyBatch(batch)
			require.Error(t, err)
			require.NoError(t, validateAuthenticatedFreshComponentResult(
				candidate.fresh, candidate.queryKey, candidate.encoded,
			))
			assertIncrementalGroupIndexSameRoots(t, &before, poisoned)
		})
	}
}

func TestPreparedEmptyGroupBatchLateFailureIsAtomic(t *testing.T) {
	component := incrementalComponent{name: "producer", group: "group", publishValue: true}
	first := rankedEmptyBatchTestInstance(t, &component, "a", "100", "first")
	second := unrankedEmptyBatchTestInstance(t, &component, "z", "second")
	candidates := []incrementalPreparedGroupInstance{
		preparedMemoBatchCandidate(t, &component, &first),
		preparedMemoBatchCandidate(t, &component, &second),
	}
	base := newIncrementalGroupIndex()

	_, _, err := base.addPreparedBatch(candidates)
	require.ErrorContains(t, err, "mixes ranked and unranked")
	require.NoError(t, base.validateAuthentication())
	empty, emptyErr := base.authenticatedStructurallyEmpty()
	require.NoError(t, emptyErr)
	assert.True(t, empty)
	for index := range candidates {
		require.NoError(t, validateAuthenticatedFreshComponentResult(
			candidates[index].fresh, candidates[index].queryKey, candidates[index].encoded,
		))
	}
}

func TestPreparedEmptyGroupBatchColdThenWarmLifecycle(t *testing.T) {
	component := incrementalComponent{name: "producer", group: "group", publishValue: true}
	coldB := rankedEmptyBatchTestInstance(t, &component, "b", "200", "b")
	coldZ := rankedEmptyBatchTestInstance(t, &component, "z", "300", "z")
	index, _, err := newIncrementalGroupIndex().addPreparedBatch([]incrementalPreparedGroupInstance{
		preparedMemoBatchCandidate(t, &component, &coldZ),
		preparedMemoBatchCandidate(t, &component, &coldB),
	})
	require.NoError(t, err)
	assert.Equal(t, "b", emptyBatchTestWinner(t, index))

	warmA := rankedEmptyBatchTestInstance(t, &component, "a", "100", "a")
	index, err = index.replace(&warmA, nil)
	require.NoError(t, err)
	assert.Equal(t, "a", emptyBatchTestWinner(t, index))

	warmA.result = rankedEmptyBatchTestResult(t, "400", "a-changed")
	index, err = index.replace(&warmA, nil)
	require.NoError(t, err)
	assert.Equal(t, "b", emptyBatchTestWinner(t, index))

	index, err = index.remove(component.name, coldB.source, coldB.namespace, coldB.name)
	require.NoError(t, err)
	assert.Equal(t, "z", emptyBatchTestWinner(t, index))

	index, err = index.remove(component.name, coldZ.source, coldZ.namespace, coldZ.name)
	require.NoError(t, err)
	assert.Equal(t, "a-changed", emptyBatchTestWinner(t, index))

	index, err = index.remove(component.name, warmA.source, warmA.namespace, warmA.name)
	require.NoError(t, err)
	empty, err := index.authenticatedStructurallyEmpty()
	require.NoError(t, err)
	require.True(t, empty)

	warmFirst := rankedEmptyBatchTestInstance(t, &component, "y", "050", "warm-first")
	index, _, err = index.addPreparedBatch([]incrementalPreparedGroupInstance{
		preparedMemoBatchCandidate(t, &component, &warmFirst),
	})
	require.NoError(t, err)
	assert.Equal(t, "warm-first", emptyBatchTestWinner(t, index))
	require.NoError(t, index.validateAuthentication())
}

func populatedEmptyBatchTestIndex(
	t *testing.T,
	component *incrementalComponent,
) *incrementalGroupIndex {
	t.Helper()
	instance := rankedEmptyBatchTestInstance(t, component, "old", "100", "old")
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	return index
}

func rankedEmptyBatchTestInstance(
	t *testing.T,
	component *incrementalComponent,
	name, rank, value string,
) incrementalInstanceResult {
	t.Helper()
	return incrementalInstanceResult{
		component: component.name,
		source:    "routes",
		namespace: "default",
		name:      name,
		result:    rankedEmptyBatchTestResult(t, rank, value),
	}
}

func rankedEmptyBatchTestResult(t *testing.T, rank, value string) incrementalComponentResult {
	t.Helper()
	recorder := &incrementalRecorder{}
	recorder.PublishRanked("ranked", "shared", rank, value)
	result, err := recorder.result("")
	require.NoError(t, err)
	return result
}

func unrankedEmptyBatchTestInstance(
	t *testing.T,
	component *incrementalComponent,
	name, value string,
) incrementalInstanceResult {
	t.Helper()
	recorder := &incrementalRecorder{}
	recorder.Publish("ranked", "shared", value)
	result, err := recorder.result("")
	require.NoError(t, err)
	return incrementalInstanceResult{
		component: component.name,
		source:    "routes",
		namespace: "default",
		name:      name,
		result:    result,
	}
}

func emptyBatchTestWinner(t *testing.T, index *incrementalGroupIndex) string {
	t.Helper()
	values, _, err := index.certifiedPublishedValues("ranked")
	require.NoError(t, err)
	require.Len(t, values, 1)
	value, ok := values[0].(string)
	require.True(t, ok)
	require.NoError(t, index.validateAuthentication())
	return value
}

func assertIncrementalGroupIndexSameRoots(
	t *testing.T,
	want, got *incrementalGroupIndex,
) {
	t.Helper()
	assert.Same(t, want.instances, got.instances)
	assert.Same(t, want.contributors, got.contributors)
	assert.Same(t, want.publications, got.publications)
	assert.Same(t, want.publicationWinnersByLocation, got.publicationWinnersByLocation)
	assert.Same(t, want.publicationWinnersByRank, got.publicationWinnersByRank)
	assert.Same(t, want.publicationCounts, got.publicationCounts)
	assert.Same(t, want.events, got.events)
	assert.Same(t, want.status, got.status)
	assert.Same(t, want.http, got.http)
	assert.Same(t, want.outputs, got.outputs)
	assert.Same(t, want.rankedText, got.rankedText)
	assert.Same(t, want.memo, got.memo)
}
