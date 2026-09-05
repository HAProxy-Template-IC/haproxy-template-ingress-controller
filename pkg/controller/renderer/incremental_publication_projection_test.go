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
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/persistenttree"
)

func TestIncrementalPublicationWinnerProjectionsPromoteAndDeleteCollisions(t *testing.T) {
	tests := []struct {
		name      string
		cell      string
		first     incrementalInstanceResult
		second    incrementalInstanceResult
		wantFirst string
	}{
		{
			name: "location", cell: "hosts",
			first: incrementalInstanceResult{
				component: "100-first", source: "routes", namespace: "default", name: "first",
				result: publishedResult(t, publicationValue(t, "hosts", "shared", "first")),
			},
			second: incrementalInstanceResult{
				component: "200-second", source: "routes", namespace: "default", name: "second",
				result: publishedResult(t, publicationValue(t, "hosts", "shared", "second")),
			},
			wantFirst: "first",
		},
		{
			name: "rank", cell: "fragments",
			first: incrementalInstanceResult{
				component: "200-first", source: "routes", namespace: "default", name: "first",
				result: rankedFragmentResult(t,
					incrementalRankedFragment{"fragments", "shared", "100", "first"},
				),
			},
			second: incrementalInstanceResult{
				component: "100-second", source: "routes", namespace: "default", name: "second",
				result: rankedFragmentResult(t,
					incrementalRankedFragment{"fragments", "shared", "200", "second"},
				),
			},
			wantFirst: "first",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index, err := newIncrementalGroupIndex().replace(&test.second, nil)
			require.NoError(t, err)
			index, err = index.replace(&test.first, nil)
			require.NoError(t, err)
			assertPublicationProjectionCardinality(t, index, test.cell, 1)
			assert.Equal(t, test.wantFirst, publicationProjectionWinnerString(t, index, test.cell, test.name == "rank"))

			beforePromotion := index
			index, err = index.remove(
				test.first.component, test.first.source, test.first.namespace, test.first.name,
			)
			require.NoError(t, err)
			assert.Equal(t, "second", publicationProjectionWinnerString(t, index, test.cell, test.name == "rank"))
			assert.Equal(t, test.wantFirst,
				publicationProjectionWinnerString(t, beforePromotion, test.cell, test.name == "rank"))

			index, err = index.remove(
				test.second.component, test.second.source, test.second.namespace, test.second.name,
			)
			require.NoError(t, err)
			assertPublicationProjectionCardinality(t, index, test.cell, 0)
			require.NoError(t, index.validateAuthentication())
		})
	}
}

func TestIncrementalPublicationWinnerProjectionAdmissionAbortIsAtomic(t *testing.T) {
	first := incrementalInstanceResult{
		component: "100-first", source: "routes", namespace: "default", name: "first",
		result: publishedResult(t, publicationValue(t, "hosts", "shared", "first")),
	}
	second := incrementalInstanceResult{
		component: "200-second", source: "routes", namespace: "default", name: "second",
		result: publishedResult(t, publicationValue(t, "hosts", "shared", "second")),
	}
	index, err := newIncrementalGroupIndex().replace(&second, nil)
	require.NoError(t, err)
	index, err = index.replace(&first, nil)
	require.NoError(t, err)
	locationRoot := index.publicationWinnersByLocation
	rankRoot := index.publicationWinnersByRank

	rejected := first
	rejected.result = rankedFragmentResult(t,
		incrementalRankedFragment{"hosts", "shared", "050", "rejected"},
	)
	updated, err := index.replace(&rejected, nil)
	require.ErrorContains(t, err, "mixes ranked and unranked owners")
	assert.Nil(t, updated)
	assert.Same(t, locationRoot, index.publicationWinnersByLocation)
	assert.Same(t, rankRoot, index.publicationWinnersByRank)
	assert.Equal(t, "first", publicationProjectionWinnerString(t, index, "hosts", false))
	require.NoError(t, index.validateAuthentication())

	index, err = index.remove(first.component, first.source, first.namespace, first.name)
	require.NoError(t, err)
	assert.Equal(t, "second", publicationProjectionWinnerString(t, index, "hosts", false))
}

func TestIncrementalPublicationWinnerProjectionConcurrentAdmissionsAndReads(t *testing.T) {
	baseInstance := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "route",
		result: publishedResult(t, publicationValue(t, "hosts", "shared", "base")),
	}
	base, err := newIncrementalGroupIndex().replace(&baseInstance, nil)
	require.NoError(t, err)

	const workers = 8
	const operations = 100
	candidates := make([]incrementalInstanceResult, workers)
	for worker := range workers {
		candidates[worker] = baseInstance
		candidates[worker].result = publishedResult(t,
			publicationValue(t, "hosts", "shared", fmt.Sprintf("worker-%d", worker)),
		)
	}
	errorsByWorker := make(chan error, workers)
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := range workers {
		go func() {
			defer wait.Done()
			errorsByWorker <- exerciseConcurrentPublicationProjection(
				base, &candidates[worker], fmt.Sprintf("worker-%d", worker), operations,
			)
		}()
	}
	wait.Wait()
	close(errorsByWorker)
	for workerErr := range errorsByWorker {
		require.NoError(t, workerErr)
	}
	assert.Equal(t, "base", publicationProjectionWinnerString(t, base, "hosts", false))
	require.NoError(t, base.validateAuthentication())
}

func exerciseConcurrentPublicationProjection(
	base *incrementalGroupIndex,
	candidate *incrementalInstanceResult,
	want string,
	operations int,
) error {
	for range operations {
		got, err := publicationProjectionWinnerStringResult(base, "hosts", false)
		if err != nil {
			return fmt.Errorf("reading base winner %q: %w", got, err)
		}
		if got != "base" {
			return fmt.Errorf("reading base winner %q, want %q", got, "base")
		}
		admitted, err := base.replace(candidate, nil)
		if err != nil {
			return err
		}
		got, err = publicationProjectionWinnerStringResult(admitted, "hosts", false)
		if err != nil {
			return fmt.Errorf("reading admitted winner %q, want %q: %w", got, want, err)
		}
		if got != want {
			return fmt.Errorf("reading admitted winner %q, want %q", got, want)
		}
	}
	return nil
}

func TestIncrementalPublicationWinnerProjectionReturnsFreshDetachedValues(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "route",
		result: rankedFragmentResult(t,
			incrementalRankedFragment{"hosts", "shared", "100", "original"},
		),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	winners, err := index.rankedPublishedWinners("hosts")
	require.NoError(t, err)
	require.Len(t, winners, 1)
	expectedLocation := string(winners[0].location)
	winners[0].location[0] ^= 0xff
	winners[0].value.Value[0] = 'x'

	fresh, err := index.rankedPublishedWinners("hosts")
	require.NoError(t, err)
	require.Len(t, fresh, 1)
	assert.Equal(t, expectedLocation, string(fresh[0].location))
	value, err := decodeResourceValue(fresh[0].value.Value)
	require.NoError(t, err)
	assert.Equal(t, "original", value)
	output, err := decodeIncrementalRankedFragments(index, "hosts")
	require.NoError(t, err)
	assert.Equal(t, "original", output)
	require.NoError(t, index.validateAuthentication())
}

func TestIncrementalPublicationProjectionKeysPreserveExactOrdering(t *testing.T) {
	ranks := []string{"", "\x00", "a", "a\x00", "b", "\xff"}
	locations := []string{"", "\x00", "\x00\x00", "a", "a\x00", "b", "\xff"}
	for _, leftRank := range ranks {
		for _, rightRank := range ranks {
			for _, leftLocation := range locations {
				for _, rightLocation := range locations {
					left := incrementalIndexedPublication{rank: leftRank, location: leftLocation}
					right := incrementalIndexedPublication{rank: rightRank, location: rightLocation}
					wantRanked := strings.Compare(leftRank, rightRank)
					if wantRanked == 0 {
						wantRanked = strings.Compare(leftLocation, rightLocation)
					}
					assert.Equal(t, wantRanked, bytes.Compare(
						incrementalPublicationProjectionKey(&left, true),
						incrementalPublicationProjectionKey(&right, true),
					))
					assert.Equal(t, strings.Compare(leftLocation, rightLocation), bytes.Compare(
						incrementalPublicationProjectionKey(&left, false),
						incrementalPublicationProjectionKey(&right, false),
					))
				}
			}
		}
	}
}

func TestIncrementalPublicationWinnerProjectionRejectsRootPoison(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "route",
		result: rankedFragmentResult(t,
			incrementalRankedFragment{"hosts", "shared", "100", "first"},
		),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	tests := []struct {
		name   string
		mutate func(*incrementalGroupIndex)
	}{
		{
			name: "equivalent location root",
			mutate: func(poisoned *incrementalGroupIndex) {
				poisoned.publicationWinnersByLocation = cloneOrderedTree(index.publicationWinnersByLocation)
			},
		},
		{
			name: "equivalent rank root",
			mutate: func(poisoned *incrementalGroupIndex) {
				poisoned.publicationWinnersByRank = cloneOrderedTree(index.publicationWinnersByRank)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			poisoned := *index
			test.mutate(&poisoned)
			auditVisits := 0
			err := poisoned.validateAuthenticationWithAudit(&auditVisits)
			require.ErrorContains(t, err, "authentication seal")
			assert.Positive(t, auditVisits)
		})
	}

	second := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "second",
		result: rankedFragmentResult(t,
			incrementalRankedFragment{"hosts", "second", "200", "second"},
		),
	}
	updated, err := index.replace(&second, nil)
	require.NoError(t, err)
	staleTests := []struct {
		name   string
		mutate func(*incrementalGroupIndex)
	}{
		{
			name: "stale location root",
			mutate: func(poisoned *incrementalGroupIndex) {
				poisoned.publicationWinnersByLocation = index.publicationWinnersByLocation
			},
		},
		{
			name: "stale rank root",
			mutate: func(poisoned *incrementalGroupIndex) {
				poisoned.publicationWinnersByRank = index.publicationWinnersByRank
			},
		},
	}
	for _, test := range staleTests {
		t.Run(test.name, func(t *testing.T) {
			poisoned := *updated
			test.mutate(&poisoned)
			_, err := poisoned.publishedWinners("hosts")
			require.ErrorContains(t, err, "winner projection")
		})
	}
}

func BenchmarkIncrementalPublicationWinnerProjection(b *testing.B) {
	for _, size := range []int{300, 1000, 3000} {
		b.Run(fmt.Sprintf("winners-%d", size), func(b *testing.B) {
			benchmarkIncrementalPublicationWinnerProjectionSize(b, size)
		})
	}
}

func benchmarkIncrementalPublicationWinnerProjectionSize(b *testing.B, size int) {
	b.Helper()
	instances, index := incrementalPublicationProjectionBenchmarkFixture(b, size)
	b.Run("read", func(b *testing.B) {
		benchmarkIncrementalPublicationWinnerProjectionRead(b, index, size, false)
	})
	b.Run("ranked-read", func(b *testing.B) {
		benchmarkIncrementalPublicationWinnerProjectionRead(b, index, size, true)
	})
	b.Run("update", func(b *testing.B) {
		benchmarkIncrementalPublicationWinnerProjectionUpdate(b, &instances[0], index, size)
	})
}

func benchmarkIncrementalPublicationWinnerProjectionRead(
	b *testing.B,
	index *incrementalGroupIndex,
	size int,
	ranked bool,
) {
	b.Helper()
	b.ReportAllocs()
	b.ReportMetric(float64(size), "winners")
	for range b.N {
		var winners []incrementalPublishedWinner
		var err error
		if ranked {
			winners, err = index.rankedPublishedWinners("fragments")
		} else {
			winners, err = index.publishedWinners("fragments")
		}
		if err != nil || len(winners) != size {
			b.Fatalf("read %d winners: %v", len(winners), err)
		}
		incrementalPublicationWinnersSink = winners
	}
}

func benchmarkIncrementalPublicationWinnerProjectionUpdate(
	b *testing.B,
	instance *incrementalInstanceResult,
	index *incrementalGroupIndex,
	size int,
) {
	b.Helper()
	first := *instance
	second := first
	first.result = benchmarkRankedPublicationResult(b, "key-000000", "000000", "first")
	second.result = benchmarkRankedPublicationResult(b, "key-000000", "000000", "second")
	current := index
	b.ReportAllocs()
	b.ReportMetric(float64(size), "winners")
	b.ResetTimer()
	for operation := range b.N {
		next := &first
		if operation&1 != 0 {
			next = &second
		}
		var err error
		current, err = current.replace(next, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
	incrementalGroupIndexSink = current
}

func incrementalPublicationProjectionBenchmarkFixture(
	b *testing.B,
	size int,
) ([]incrementalInstanceResult, *incrementalGroupIndex) {
	b.Helper()
	instances := make([]incrementalInstanceResult, size)
	index := newIncrementalGroupIndex()
	for item := range size {
		key := fmt.Sprintf("key-%06d", item)
		instances[item] = incrementalInstanceResult{
			component: "component", source: "routes", namespace: "default", name: fmt.Sprintf("route-%06d", item),
			result: benchmarkRankedPublicationResult(b, key, fmt.Sprintf("%06d", item), key),
		}
		var err error
		index, err = index.replace(&instances[item], nil)
		if err != nil {
			b.Fatal(err)
		}
	}
	return instances, index
}

func benchmarkRankedPublicationResult(
	tb testing.TB,
	key, rank, value string,
) incrementalComponentResult {
	tb.Helper()
	recorder := &incrementalRecorder{}
	recorder.PublishRanked("fragments", key, rank, value)
	result, err := recorder.result("")
	if err != nil {
		tb.Fatal(err)
	}
	return result
}

func assertPublicationProjectionCardinality(
	t *testing.T,
	index *incrementalGroupIndex,
	cell string,
	want int,
) {
	t.Helper()
	for name, projection := range map[string]*incrementalPublicationProjection{
		"location": index.publicationWinnersByLocation,
		"rank":     index.publicationWinnersByRank,
	} {
		winners, exists := projection.Root().Get(incrementalOrderedTuple(cell))
		if want == 0 {
			assert.False(t, exists, name)
			continue
		}
		require.True(t, exists, name)
		require.NotNil(t, winners, name)
		assert.Equal(t, want, winners.Len(), name)
	}
}

type incrementalPublicationProjection = persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]]

func publicationProjectionWinnerString(
	t *testing.T,
	index *incrementalGroupIndex,
	cell string,
	ranked bool,
) string {
	t.Helper()
	value, err := publicationProjectionWinnerStringResult(index, cell, ranked)
	require.NoError(t, err)
	return value
}

func publicationProjectionWinnerStringResult(
	index *incrementalGroupIndex,
	cell string,
	ranked bool,
) (string, error) {
	var winners []incrementalPublishedWinner
	var err error
	if ranked {
		winners, err = index.rankedPublishedWinners(cell)
	} else {
		winners, err = index.publishedWinners(cell)
	}
	if err != nil {
		return "", err
	}
	if len(winners) != 1 {
		return "", fmt.Errorf("read %d publication winners", len(winners))
	}
	value, err := decodeResourceValue(winners[0].value.Value)
	if err != nil {
		return "", err
	}
	switch decoded := value.(type) {
	case string:
		return decoded, nil
	case map[string]any:
		result, ok := decoded["name"].(string)
		if !ok {
			return "", fmt.Errorf("publication winner name is %T", decoded["name"])
		}
		return result, nil
	default:
		return "", fmt.Errorf("publication winner is %T", value)
	}
}

var incrementalPublicationWinnersSink []incrementalPublishedWinner
