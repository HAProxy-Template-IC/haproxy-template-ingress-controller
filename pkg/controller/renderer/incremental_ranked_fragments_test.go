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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIncrementalRankedFragmentsOrderPromotionAndStableOwnerTie(t *testing.T) {
	instances := []incrementalInstanceResult{
		{
			component: "200-grpc", source: "grpc", namespace: "default", name: "route-b",
			result: rankedFragmentResult(t,
				incrementalRankedFragment{"lines", "shared", "200", "grpc-shared\n"},
				incrementalRankedFragment{"lines", "grpc", "100", "grpc\n"},
			),
		},
		{
			component: "100-http", source: "http", namespace: "default", name: "route-a",
			result: rankedFragmentResult(t,
				incrementalRankedFragment{"lines", "shared", "050", "http-shared\n"},
				incrementalRankedFragment{"lines", "http", "100", "http\n"},
			),
		},
	}
	index := newIncrementalGroupIndex()
	var err error
	for instanceIndex := range instances {
		index, err = index.replace(&instances[instanceIndex], nil)
		require.NoError(t, err)
	}

	output, err := decodeIncrementalRankedFragments(index, "lines")
	require.NoError(t, err)
	assert.Equal(t, "http-shared\nhttp\ngrpc\n", output)

	index, err = index.remove("100-http", "http", "default", "route-a")
	require.NoError(t, err)
	output, err = decodeIncrementalRankedFragments(index, "lines")
	require.NoError(t, err)
	assert.Equal(t, "grpc\ngrpc-shared\n", output)
}

func TestIncrementalRankedFragmentsJoinPreservesDelimiterBytes(t *testing.T) {
	index := newIncrementalGroupIndex()
	output, err := decodeIncrementalRankedFragmentsJoin(index, "documents", "\n---\x00\n")
	require.NoError(t, err)
	assert.Empty(t, output)

	one := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "one",
		result: rankedFragmentResult(t,
			incrementalRankedFragment{"documents", "one", "100", "one\n"},
		),
	}
	index, err = index.replace(&one, nil)
	require.NoError(t, err)
	output, err = decodeIncrementalRankedFragmentsJoin(index, "documents", "\n---\x00\n")
	require.NoError(t, err)
	assert.Equal(t, "one\n", output)

	many := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "many",
		result: rankedFragmentResult(t,
			incrementalRankedFragment{"documents", "three", "300", "three\n"},
			incrementalRankedFragment{"documents", "two", "200", "two\n"},
		),
	}
	index, err = index.replace(&many, nil)
	require.NoError(t, err)
	output, err = decodeIncrementalRankedFragmentsJoin(index, "documents", "\n---\x00\n")
	require.NoError(t, err)
	assert.Equal(t, "one\n\n---\x00\ntwo\n\n---\x00\nthree\n", output)
}

func TestIncrementalRankedFragmentsRejectsUnrankedAndNonStringValues(t *testing.T) {
	tests := []struct {
		name   string
		result incrementalComponentResult
		want   string
	}{
		{
			name: "unranked",
			result: publishedResult(t, incrementalPublishedValue{
				Cell: "lines", Key: "line", Value: encodedResourceValue(t, "line\n"),
			}),
			want: "has no rank",
		},
		{
			name: "non-string",
			result: rankedFragmentResult(t,
				incrementalRankedFragment{"lines", "line", "100", map[string]any{"line": "line\n"}},
			),
			want: "must be a string",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance := incrementalInstanceResult{
				component: "producer", source: "routes", namespace: "default", name: "route",
				result: test.result,
			}
			index, err := newIncrementalGroupIndex().replace(&instance, nil)
			require.NoError(t, err)
			_, err = decodeIncrementalRankedFragments(index, "lines")
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestIncrementalRankedFragmentsRejectsUnauthenticatedIndex(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: rankedFragmentResult(t,
			incrementalRankedFragment{"lines", "line", "100", "line\n"},
		),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	poisoned := *index
	poisoned.publications = cloneOrderedTree(index.publications)

	_, err = decodeIncrementalRankedFragments(&poisoned, "lines")
	require.ErrorContains(t, err, "authentication seal")
}

func TestIncrementalRankedTextFragmentRetainsExactChangedRoot(t *testing.T) {
	instances := []incrementalInstanceResult{
		{
			component: "producer", source: "routes", namespace: "default", name: "a",
			result: rankedFragmentResult(t,
				incrementalRankedFragment{"lines", "a", "100", "alpha"},
			),
		},
		{
			component: "producer", source: "routes", namespace: "default", name: "b",
			result: rankedFragmentResult(t,
				incrementalRankedFragment{"lines", "b", "200", "beta"},
			),
		},
		{
			component: "producer", source: "routes", namespace: "default", name: "c",
			result: rankedFragmentResult(t,
				incrementalRankedFragment{"lines", "c", "300", "gamma"},
			),
		},
	}
	index := newIncrementalGroupIndex()
	var err error
	for position := range instances {
		index, err = index.replace(&instances[position], nil)
		require.NoError(t, err)
	}
	original, err := index.rankedTextFragment("lines", "|")
	require.NoError(t, err)
	require.NoError(t, original.ValidateAuthentication())
	originalText, err := original.String()
	require.NoError(t, err)
	assert.Equal(t, "alpha|beta|gamma", originalText)

	unchanged, err := index.replace(&instances[1], nil)
	require.NoError(t, err)
	unchangedFragment, err := unchanged.rankedTextFragment("lines", "|")
	require.NoError(t, err)
	same, err := original.SameRoot(unchangedFragment)
	require.NoError(t, err)
	assert.True(t, same)

	instances[1].result = rankedFragmentResult(t,
		incrementalRankedFragment{"lines", "b", "200", "changed"},
	)
	changed, err := unchanged.replace(&instances[1], nil)
	require.NoError(t, err)
	changedFragment, err := changed.rankedTextFragment("lines", "|")
	require.NoError(t, err)
	same, err = original.SameRoot(changedFragment)
	require.NoError(t, err)
	assert.False(t, same)
	changedText, err := changedFragment.String()
	require.NoError(t, err)
	assert.Equal(t, "alpha|changed|gamma", changedText)
	retainedText, err := original.String()
	require.NoError(t, err)
	assert.Equal(t, "alpha|beta|gamma", retainedText)
}

func TestIncrementalRankedTextFragmentRejectsDetachedProjection(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: rankedFragmentResult(t,
			incrementalRankedFragment{"lines", "line", "100", "line\n"},
		),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	projection, exists := index.publicationWinnersByRank.Root().Get(incrementalOrderedTuple("lines"))
	require.True(t, exists)
	outer := index.publicationWinnersByRank.Txn()
	outer.Insert(incrementalOrderedTuple("lines"), cloneOrderedTree(projection))
	poisoned := *index
	poisoned.publicationWinnersByRank = outer.Commit()
	poisoned.authenticate()

	_, err = poisoned.rankedTextFragment("lines", "")
	require.ErrorContains(t, err, "invalid provenance")
}

type incrementalRankedFragment struct {
	cell  string
	key   string
	rank  string
	value any
}

func rankedFragmentResult(t *testing.T, fragments ...incrementalRankedFragment) incrementalComponentResult {
	t.Helper()
	recorder := &incrementalRecorder{}
	for _, fragment := range fragments {
		recorder.PublishRanked(fragment.cell, fragment.key, fragment.rank, fragment.value)
	}
	result, err := recorder.result("")
	require.NoError(t, err)
	return result
}

func encodedResourceValue(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := encodeResourceValue(value)
	require.NoError(t, err)
	return encoded
}
