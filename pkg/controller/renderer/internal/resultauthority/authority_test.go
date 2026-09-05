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
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

type testValue struct {
	text  string
	bytes []byte
}

type testMetadata struct {
	component string
	allowed   bool
}

func TestUntrustedValueAndMaterializationsAreDetached(t *testing.T) {
	key := incremental.NewQueryKey("query")
	value := testValue{text: "original", bytes: []byte("original")}
	metadata := testMetadata{component: "component", allowed: true}
	handle := New(key, "encoded", value, &metadata, cloneTestValue)
	value.bytes[0] = 'p'
	root := testExactRoot(t, key, "encoded")
	require.NoError(t, Bind(handle, key, "encoded", incremental.ExactValueRoot{}, root))

	first, err := Materialize(handle, key, "encoded", root, root, cloneTestValue)
	require.NoError(t, err)
	assert.Equal(t, "original", string(first.bytes))
	first.bytes[0] = 'p'
	second, err := Materialize(handle, key, "encoded", root, root, cloneTestValue)
	require.NoError(t, err)
	assert.Equal(t, "original", string(second.bytes))
	require.NoError(t, MetadataMatches(handle, key, "encoded", root, root, metadata))
}

func TestCopiedAndMismatchedHandlesFailClosed(t *testing.T) {
	key := incremental.NewQueryKey("query")
	root := testExactRoot(t, key, "encoded")
	handle := New[testValue, testMetadata](key, "encoded", testValue{}, nil, cloneTestValue)
	require.NoError(t, Bind(handle, key, "encoded", incremental.ExactValueRoot{}, root))

	copyOfHandle := *handle
	tests := []struct {
		name    string
		handle  *Handle[testValue, testMetadata]
		key     incremental.QueryKey
		encoded string
		root    incremental.ExactValueRoot
	}{
		{name: "copied handle", handle: &copyOfHandle, key: key, encoded: "encoded", root: root},
		{name: "wrong key", handle: handle, key: incremental.NewQueryKey("other"), encoded: "encoded", root: root},
		{name: "wrong bytes", handle: handle, key: key, encoded: "poison", root: root},
		{name: "zero root", handle: handle, key: key, encoded: "encoded"},
		{name: "foreign root", handle: handle, key: key, encoded: "encoded", root: testExactRoot(t, key, "encoded")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, Validate(test.handle, test.key, test.encoded, root, test.root))
		})
	}
}

func TestOwnedValueTransfersExactlyOnce(t *testing.T) {
	key := incremental.NewQueryKey("query")
	root := testExactRoot(t, key, "encoded")
	metadata := testMetadata{component: "component", allowed: true}
	handle := NewOwned(
		key,
		"encoded",
		testValue{text: "original", bytes: []byte("original")},
		&metadata,
	)
	require.NoError(t, Bind(handle, key, "encoded", incremental.ExactValueRoot{}, root))
	require.NoError(t, MetadataMatches(handle, key, "encoded", root, root, metadata))

	value, err := Take(handle, key, "encoded", root, root)
	require.NoError(t, err)
	assert.Equal(t, "original", string(value.bytes))
	require.NoError(t, Validate(handle, key, "encoded", root, root))
	_, err = Take(handle, key, "encoded", root, root)
	require.ErrorContains(t, err, "already transferred")
	_, err = Materialize(handle, key, "encoded", root, root, cloneTestValue)
	require.ErrorContains(t, err, "already transferred")
}

func TestConcurrentMaterializationCannotMutateAuthority(t *testing.T) {
	key := incremental.NewQueryKey("query")
	root := testExactRoot(t, key, "encoded")
	handle := New[testValue, testMetadata](
		key,
		"encoded",
		testValue{text: "original", bytes: []byte("original")},
		nil,
		cloneTestValue,
	)
	require.NoError(t, Bind(handle, key, "encoded", incremental.ExactValueRoot{}, root))

	var wait sync.WaitGroup
	for range 64 {
		wait.Go(func() {
			value, err := Materialize(handle, key, "encoded", root, root, cloneTestValue)
			assert.NoError(t, err)
			assert.Equal(t, "original", string(value.bytes))
			value.bytes[0] = 'p'
		})
	}
	wait.Wait()
	value, err := Materialize(handle, key, "encoded", root, root, cloneTestValue)
	require.NoError(t, err)
	assert.Equal(t, "original", string(value.bytes))
}

func cloneTestValue(value *testValue) testValue {
	if value == nil {
		return testValue{}
	}
	return testValue{text: value.text, bytes: append([]byte(nil), value.bytes...)}
}

func testExactRoot(
	t *testing.T,
	key incremental.QueryKey,
	value string,
) incremental.ExactValueRoot {
	t.Helper()
	graph, err := incremental.New(incremental.Definition{
		Key: key,
		Run: func(context.Context, incremental.Reader) ([]byte, error) {
			return nil, nil
		},
	})
	require.NoError(t, err)
	session, err := graph.Begin()
	require.NoError(t, err)
	results, err := session.EvaluateAllExactBatch(t.Context(), func(
		_ context.Context,
		queries []incremental.BatchQuery,
	) ([]incremental.ExactBatchValue, error) {
		root, rootErr := queries[0].NewExactValue(value)
		return []incremental.ExactBatchValue{{Value: root, Err: rootErr}}, nil
	}, key)
	require.NoError(t, err)
	session.Abort()
	return results[0].Value
}
