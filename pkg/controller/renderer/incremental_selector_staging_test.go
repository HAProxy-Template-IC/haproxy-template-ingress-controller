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
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func acceptEveryRevision(context.Context, []incremental.InputRevision) (bool, error) {
	return true, nil
}

// A session copies its group indexes from the generation it began on. A
// commit that lands afterwards can remove the last reader of a cell's
// selector input from the graph's current generation; a publication into
// that cell must still reach this session's graph view, or a consumer this
// session evaluates observes the base value against a moved index.
func TestSelectorStagingJudgesDependentsOnTheSessionBase(t *testing.T) {
	inputKey := incrementalSelectorValuesInputKey("group", "ranked")
	consumer := incremental.NewQueryKey("consumer")
	graph, err := incremental.New(incremental.Definition{
		Key: consumer,
		Run: func(_ context.Context, reader incremental.Reader) ([]byte, error) {
			value, _, readErr := reader.Input(inputKey)
			return value, readErr
		},
	})
	require.NoError(t, err)
	first, err := graph.Begin()
	require.NoError(t, err)
	require.NoError(t, first.ApplyInputs(incremental.Input{
		Key: inputKey, Revision: incremental.NewRevision("r1"), Found: true, Value: []byte(`["v1"]`),
	}))
	_, err = first.Evaluate(t.Context(), consumer)
	require.NoError(t, err)
	require.NoError(t, first.Commit(t.Context(), acceptEveryRevision))

	pinned, err := graph.Begin()
	require.NoError(t, err)
	defer pinned.Abort()

	removal, err := graph.Begin()
	require.NoError(t, err)
	require.NoError(t, removal.RemoveQueries(consumer))
	require.NoError(t, removal.Commit(t.Context(), acceptEveryRevision))
	require.False(t, graph.HasInputDependents(inputKey))
	dependents, err := pinned.HasInputDependents(inputKey)
	require.NoError(t, err)
	require.True(t, dependents)

	component := incrementalComponent{name: "producer", group: "group", publishValue: true}
	instance := unrankedEmptyBatchTestInstance(t, &component, "a", "v2")
	previous := newIncrementalGroupIndex()
	next, err := previous.replace(&instance, nil)
	require.NoError(t, err)
	id := incrementalGroupInstanceID{
		component: instance.component, source: instance.source,
		namespace: instance.namespace, name: instance.name,
	}
	values := incrementalSelectorIdentity{group: "group", cell: "ranked"}

	pending := map[incrementalSelectorIdentity]incremental.Input{}
	require.NoError(t, stageIncrementalSelectorReplacementInto(
		pending, func(key incremental.InputKey) (bool, error) { return graph.HasInputDependents(key), nil },
		"group", previous, next, id, &instance.result,
	))
	require.NotContains(t, pending, values, "the graph's current generation has no reader left")

	pending = map[incrementalSelectorIdentity]incremental.Input{}
	require.NoError(t, stageIncrementalSelectorReplacementInto(
		pending, pinned.HasInputDependents, "group", previous, next, id, &instance.result,
	))
	require.Contains(t, pending, values)
}
