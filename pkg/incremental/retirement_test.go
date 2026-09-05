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

package incremental

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestInputRetirementTracksChangedDependencyFrames(t *testing.T) {
	selectorKey := NewInputKey("selector")
	leftKey := NewInputKey("item/left")
	rightKey := NewInputKey("item/right")
	unusedKey := NewInputKey("unused")
	branchKey := NewQueryKey("branch")
	graph := mustRetiringGraph(t, Definition{Key: branchKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		selector, _, err := reader.Input(selectorKey)
		if err != nil {
			return nil, err
		}
		value, _, err := reader.Input(NewInputKey("item/" + string(selector)))
		return value, err
	}})
	resolver := failingResolver(t)

	session := mustBeginWithResolver(t, graph, resolver)
	mustApply(t, session,
		exactInput(selectorKey, "selector-1", "left"),
		exactInput(leftKey, "left-1", "left-old"),
		exactInput(rightKey, "right-unused", "right-unused"),
		exactInput(unusedKey, "unused-1", "unused"),
	)
	if got := string(mustEvaluate(t, session, branchKey)); got != "left-old" {
		t.Fatalf("initial branch value = %q", got)
	}
	mustCommit(t, session)
	assertCommittedInputs(t, graph, selectorKey, leftKey)

	var rightResolutions atomic.Int32
	session = mustBeginWithResolver(t, graph, func(_ context.Context, key InputKey) (Input, error) {
		if key != rightKey {
			return Input{}, fmt.Errorf("unexpected resolution of %q", key.Opaque())
		}
		rightResolutions.Add(1)
		return exactInput(rightKey, "right-1", "right"), nil
	})
	mustApply(t, session, exactInput(selectorKey, "selector-2", "right"))
	if got := string(mustEvaluate(t, session, branchKey)); got != "right" {
		t.Fatalf("changed branch value = %q", got)
	}
	mustCommit(t, session)
	if rightResolutions.Load() != 1 {
		t.Fatalf("right input resolutions = %d", rightResolutions.Load())
	}
	retired := session.RetiredInputs()
	if len(retired) != 1 || retired[0] != leftKey {
		t.Fatalf("retired inputs = %#v, want %q", retired, leftKey.Opaque())
	}
	assertCommittedInputs(t, graph, selectorKey, rightKey)

	before := graph.Counters(branchKey)
	session = mustBeginWithResolver(t, graph, resolver)
	mustApply(t, session, exactInput(leftKey, "left-unused", "stale"))
	mustCommit(t, session)
	assertCommittedInputs(t, graph, selectorKey, rightKey)
	if after := graph.Counters(branchKey); after != before {
		t.Fatalf("unused input changed branch counters: before=%+v after=%+v", before, after)
	}

	var leftResolutions atomic.Int32
	session = mustBeginWithResolver(t, graph, func(_ context.Context, key InputKey) (Input, error) {
		if key != leftKey {
			return Input{}, fmt.Errorf("unexpected resolution of %q", key.Opaque())
		}
		leftResolutions.Add(1)
		return exactInput(leftKey, "left-current", "left-current"), nil
	})
	mustApply(t, session, exactInput(selectorKey, "selector-3", "left"))
	if got := string(mustEvaluate(t, session, branchKey)); got != "left-current" {
		t.Fatalf("restored branch value = %q", got)
	}
	mustCommit(t, session)
	if leftResolutions.Load() != 1 {
		t.Fatalf("left input resolutions = %d", leftResolutions.Load())
	}
	assertCommittedInputs(t, graph, selectorKey, leftKey)
}

func TestInputRetirementChecksFinalEdgesAfterQueryRemoval(t *testing.T) {
	leftKey := NewInputKey("left")
	rightKey := NewInputKey("right")
	selectorKey := NewInputKey("selector")
	removedKey := NewQueryKey("removed")
	changingKey := NewQueryKey("changing")
	graph := mustRetiringGraph(t,
		Definition{Key: removedKey, Run: readInputQuery(leftKey)},
		Definition{Key: changingKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
			selector, _, err := reader.Input(selectorKey)
			if err != nil {
				return nil, err
			}
			inputKey := rightKey
			if string(selector) == "left" {
				inputKey = leftKey
			}
			value, _, err := reader.Input(inputKey)
			return value, err
		}},
	)
	resolver := failingResolver(t)
	session := mustBeginWithResolver(t, graph, resolver)
	mustApply(t, session,
		exactInput(leftKey, "left-1", "left"),
		exactInput(rightKey, "right-1", "right"),
		exactInput(selectorKey, "selector-1", "right"),
	)
	if _, err := session.EvaluateAll(context.Background(), removedKey, changingKey); err != nil {
		t.Fatalf("EvaluateAll() error = %v", err)
	}
	mustCommit(t, session)
	assertCommittedInputs(t, graph, leftKey, rightKey, selectorKey)

	session = mustBeginWithResolver(t, graph, resolver)
	mustApply(t, session, exactInput(selectorKey, "selector-2", "left"))
	if err := session.RemoveQueries(removedKey); err != nil {
		t.Fatalf("RemoveQueries() error = %v", err)
	}
	retained, err := session.HasInputDependents(leftKey)
	if err != nil {
		t.Fatalf("HasInputDependents() error = %v", err)
	}
	if retained {
		t.Fatal("removed query retained its input dependency")
	}
	if got := string(mustEvaluate(t, session, changingKey)); got != "left" {
		t.Fatalf("changed query value = %q", got)
	}
	retained, err = session.HasInputDependents(leftKey)
	if err != nil {
		t.Fatalf("HasInputDependents() error = %v", err)
	}
	if !retained {
		t.Fatal("staged query dependency was not retained")
	}
	mustCommit(t, session)
	assertCommittedInputs(t, graph, leftKey, selectorKey)
}

func TestRemoveQueriesWhileIdleIsTransactional(t *testing.T) {
	victim := NewQueryKey("victim")
	survivor := NewQueryKey("survivor")
	graph := mustRetiringGraph(t,
		Definition{Key: victim, Run: constantQuery("victim")},
		Definition{Key: survivor, Run: constantQuery("survivor")},
	)
	resolver := failingResolver(t)
	session := mustBeginWithResolver(t, graph, resolver)
	if _, err := session.EvaluateAll(context.Background(), victim, survivor); err != nil {
		t.Fatalf("EvaluateAll() error = %v", err)
	}
	mustCommit(t, session)

	session = mustBeginWithResolver(t, graph, resolver)
	mustEvaluate(t, session, survivor)
	if err := session.RemoveQueriesWhileIdle(victim); err != nil {
		t.Fatalf("RemoveQueriesWhileIdle() error = %v", err)
	}
	session.Abort()
	if value, found := graph.Value(victim); !found || string(value) != "victim" {
		t.Fatalf("aborted removal value = %q, found=%t", value, found)
	}

	session = mustBeginWithResolver(t, graph, resolver)
	mustEvaluate(t, session, survivor)
	if err := session.RemoveQueriesWhileIdle(victim); err != nil {
		t.Fatalf("RemoveQueriesWhileIdle() error = %v", err)
	}
	mustCommit(t, session)
	if value, found := graph.Value(victim); found {
		t.Fatalf("committed removal retained value %q", value)
	}
	if value, found := graph.Value(survivor); !found || string(value) != "survivor" {
		t.Fatalf("survivor value = %q, found=%t", value, found)
	}
}

func TestInputRetirementVerifierConflictPreservesCommittedFrames(t *testing.T) {
	fixture := newRetirementConflictFixture(t)
	err := fixture.session.Commit(context.Background(), func(context.Context, []InputRevision) (bool, error) {
		return false, nil
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("Commit() error = %v, want ErrRevisionConflict", err)
	}
	fixture.assertCommitted(t)
}

func TestInputRetirementGenerationConflictPreservesCommittedFrames(t *testing.T) {
	fixture := newRetirementConflictFixture(t)
	concurrent := mustBeginWithResolver(t, fixture.graph, failingResolver(t))
	mustCommit(t, concurrent)
	var verifierCalls atomic.Int32
	err := fixture.session.Commit(context.Background(), func(context.Context, []InputRevision) (bool, error) {
		verifierCalls.Add(1)
		return true, nil
	})
	if !errors.Is(err, ErrCommitConflict) {
		t.Fatalf("Commit() error = %v, want ErrCommitConflict", err)
	}
	if verifierCalls.Load() != 0 {
		t.Fatalf("generation-conflicted commit called verifier %d times", verifierCalls.Load())
	}
	fixture.assertCommitted(t)
}

func TestColdResetRetiresInputsAgainstReplacementGraph(t *testing.T) {
	oldInput := NewInputKey("old-input")
	usedInput := NewInputKey("used-input")
	unusedInput := NewInputKey("unused-input")
	oldQuery := NewQueryKey("old-query")
	usedQuery := NewQueryKey("used-query")
	graph := mustRetiringGraph(t,
		Definition{Key: oldQuery, Run: readInputQuery(oldInput)},
		Definition{Key: usedQuery, Run: readInputQuery(usedInput)},
	)
	resolver := failingResolver(t)
	session := mustBeginWithResolver(t, graph, resolver)
	mustApply(t, session, exactInput(oldInput, "old-1", "old"))
	mustEvaluate(t, session, oldQuery)
	mustCommit(t, session)

	cold, err := graph.BeginColdResetWithResolver(resolver,
		exactInput(usedInput, "used-1", "used"),
		exactInput(unusedInput, "unused-1", "unused"),
	)
	if err != nil {
		t.Fatalf("BeginColdResetWithResolver() error = %v", err)
	}
	if got := string(mustEvaluate(t, cold, usedQuery)); got != "used" {
		t.Fatalf("cold query value = %q", got)
	}
	mustCommit(t, cold)
	assertCommittedInputs(t, graph, usedInput)
	if _, exists := graph.Value(oldQuery); exists {
		t.Fatal("cold reset retained old query")
	}
}

func BenchmarkNoChangeCommitWithInputRetirement(b *testing.B) {
	for _, inputCount := range []int{1, 1_000, 100_000} {
		b.Run(strconv.Itoa(inputCount), func(b *testing.B) {
			graph := retiringBenchmarkGraph(b, inputCount)
			resolver := func(context.Context, InputKey) (Input, error) {
				return Input{}, errors.New("resolver must not run")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				session, err := graph.BeginWithResolver(resolver)
				if err != nil {
					b.Fatalf("BeginWithResolver() error = %v", err)
				}
				if err := session.Commit(context.Background(), acceptRevisions); err != nil {
					b.Fatalf("Commit() error = %v", err)
				}
			}
		})
	}
}

func mustRetiringGraph(t *testing.T, definitions ...Definition) *Graph {
	t.Helper()
	graph, err := NewWithProviderOptions(nil, Options{RetireUnreferencedInputs: true}, definitions...)
	if err != nil {
		t.Fatalf("NewWithProviderOptions() error = %v", err)
	}
	return graph
}

type retirementConflictFixture struct {
	graph       *Graph
	session     *Session
	selectorKey InputKey
	leftKey     InputKey
	queryKey    QueryKey
}

func newRetirementConflictFixture(t *testing.T) retirementConflictFixture {
	t.Helper()
	selectorKey := NewInputKey("selector")
	leftKey := NewInputKey("left")
	rightKey := NewInputKey("right")
	queryKey := NewQueryKey("branch")
	graph := mustRetiringGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		selector, _, err := reader.Input(selectorKey)
		if err != nil {
			return nil, err
		}
		inputKey := leftKey
		if string(selector) == "right" {
			inputKey = rightKey
		}
		value, _, err := reader.Input(inputKey)
		return value, err
	}})
	session := mustBeginWithResolver(t, graph, failingResolver(t))
	mustApply(t, session,
		exactInput(selectorKey, "selector-1", "left"),
		exactInput(leftKey, "left-1", "left"),
	)
	mustEvaluate(t, session, queryKey)
	mustCommit(t, session)

	session = mustBeginWithResolver(t, graph, func(_ context.Context, key InputKey) (Input, error) {
		if key != rightKey {
			return Input{}, fmt.Errorf("unexpected resolution of %q", key.Opaque())
		}
		return exactInput(rightKey, "right-1", "right"), nil
	})
	mustApply(t, session, exactInput(selectorKey, "selector-2", "right"))
	mustEvaluate(t, session, queryKey)
	return retirementConflictFixture{
		graph:       graph,
		session:     session,
		selectorKey: selectorKey,
		leftKey:     leftKey,
		queryKey:    queryKey,
	}
}

func (f retirementConflictFixture) assertCommitted(t *testing.T) {
	t.Helper()
	assertCommittedInputs(t, f.graph, f.selectorKey, f.leftKey)
	if got := stringValue(t, f.graph, f.queryKey); got != "left" {
		t.Fatalf("committed value after rollback = %q", got)
	}
}

func mustBeginWithResolver(t *testing.T, graph *Graph, resolver InputResolver) *Session {
	t.Helper()
	session, err := graph.BeginWithResolver(resolver)
	if err != nil {
		t.Fatalf("BeginWithResolver() error = %v", err)
	}
	return session
}

func failingResolver(t *testing.T) InputResolver {
	t.Helper()
	return func(_ context.Context, key InputKey) (Input, error) {
		return Input{}, fmt.Errorf("unexpected resolution of %q", key.Opaque())
	}
}

func retiringBenchmarkGraph(b *testing.B, inputCount int) *Graph {
	b.Helper()
	provider := func(key QueryKey) (QueryFunc, bool) {
		name, found := strings.CutPrefix(key.Opaque(), "query/")
		if !found {
			return nil, false
		}
		inputKey := NewInputKey("input/" + name)
		return readInputQuery(inputKey), true
	}
	graph, err := NewWithProviderOptions(provider, Options{RetireUnreferencedInputs: true})
	if err != nil {
		b.Fatalf("NewWithProviderOptions() error = %v", err)
	}
	inputs := make([]Input, inputCount)
	queries := make([]QueryKey, inputCount)
	for index := range inputCount {
		name := strconv.Itoa(index)
		inputs[index] = exactInput(NewInputKey("input/"+name), "revision", "value")
		queries[index] = NewQueryKey("query/" + name)
	}
	resolver := func(context.Context, InputKey) (Input, error) {
		return Input{}, errors.New("resolver must not run")
	}
	session, err := graph.BeginColdResetWithResolver(resolver, inputs...)
	if err != nil {
		b.Fatalf("BeginColdResetWithResolver() error = %v", err)
	}
	if _, err := session.EvaluateAll(context.Background(), queries...); err != nil {
		b.Fatalf("EvaluateAll() error = %v", err)
	}
	if err := session.Commit(context.Background(), acceptRevisions); err != nil {
		b.Fatalf("Commit() error = %v", err)
	}
	return graph
}
