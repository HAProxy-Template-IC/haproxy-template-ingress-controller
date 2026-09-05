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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func TestIncrementalProtocolInputsBackdateAwayAndBackExactly(t *testing.T) {
	t.Run("HTTP content", func(t *testing.T) {
		descriptor, err := purehttpstore.DescribeSource(purehttpstore.FetchOptions{Critical: true}, nil)
		require.NoError(t, err)
		state := newHTTPRegistryTestState()
		spec, key, err := state.acquireHTTPInput(httpInputIdentity{
			url: "https://example.test/data", descriptor: descriptor,
		})
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, state.finishHTTPInputs(map[uint64]struct{}{spec.id: {}}, nil, nil, false))
		})
		source := purehttpstore.SourceID(1)
		input := func(observation purehttpstore.Revision, value string) incremental.Input {
			snapshot := purehttpstore.ContentSnapshot{Found: true, Observation: observation}
			return incremental.Input{
				Key: key, Revision: httpInputRevision(source, &snapshot), Found: true, Value: []byte(value),
			}
		}
		assertProtocolInputAwayAndBack(t, input(1, "stable"), input(2, "away"), input(3, "stable"))
	})

	t.Run("derive owner", func(t *testing.T) {
		alpha := incrementalComponent{name: "alpha"}
		beta := incrementalComponent{name: "beta"}
		assertProtocolInputAwayAndBack(t,
			deriveOwnerInput("routes", &alpha, true),
			deriveOwnerInput("routes", &beta, true),
			deriveOwnerInput("routes", &alpha, true),
		)
	})

	t.Run("shared publication", func(t *testing.T) {
		input := func(value string) incremental.Input {
			instance := incrementalInstanceResult{
				component: "producer", source: "policies", namespace: "default", name: "policy",
				result: selectorRankedResult(t, "service", "1", value),
			}
			index, err := newIncrementalGroupIndex().replace(&instance, nil)
			require.NoError(t, err)
			selected, err := incrementalSelectorInput(index, "policies", "targets", "service")
			require.NoError(t, err)
			return selected
		}
		assertProtocolInputAwayAndBack(t, input("stable"), input("away"), input("stable"))
	})
}

func assertProtocolInputAwayAndBack(
	t *testing.T,
	initial, away, back incremental.Input,
) {
	t.Helper()
	require.Equal(t, initial.Key, away.Key)
	require.Equal(t, initial.Key, back.Key)
	var executions atomic.Uint64
	queryKey := incremental.NewQueryKey("consumer")
	graph, err := incremental.New(incremental.Definition{
		Key: queryKey,
		Run: func(_ context.Context, reader incremental.Reader) ([]byte, error) {
			executions.Add(1)
			value, _, readErr := reader.Input(initial.Key)
			return value, readErr
		},
	})
	require.NoError(t, err)

	session, err := graph.Begin()
	require.NoError(t, err)
	require.NoError(t, session.ApplyInputs(initial))
	_, err = session.Evaluate(t.Context(), queryKey)
	require.NoError(t, err)
	require.NoError(t, session.Commit(t.Context(), acceptIncrementalProtocolRevisions))

	session, err = graph.Begin()
	require.NoError(t, err)
	dirty, err := session.ApplyInputsWhileIdle(away)
	require.NoError(t, err)
	assert.Equal(t, []incremental.QueryKey{queryKey}, dirty)
	dirty, err = session.ApplyInputsWhileIdle(back)
	require.NoError(t, err)
	assert.Empty(t, dirty)
	value, err := session.Evaluate(t.Context(), queryKey)
	require.NoError(t, err)
	assert.Equal(t, initial.Value, value)
	require.NoError(t, session.Commit(t.Context(), acceptIncrementalProtocolRevisions))
	assert.Equal(t, uint64(1), executions.Load())

	poisoned := back
	poisoned.Value = []byte("poison")
	session, err = graph.Begin()
	require.NoError(t, err)
	_, err = session.ApplyInputsWhileIdle(away)
	require.NoError(t, err)
	_, err = session.ApplyInputsWhileIdle(poisoned)
	require.ErrorContains(t, err, "reused an exact revision")
	session.Abort()
}

func acceptIncrementalProtocolRevisions(context.Context, []incremental.InputRevision) (bool, error) {
	return true, nil
}
