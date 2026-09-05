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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestEvaluateAllBatchKeepsIndependentDependencies(t *testing.T) {
	leftInput := NewInputKey("left-input")
	rightInput := NewInputKey("right-input")
	leftQuery := NewQueryKey("left-query")
	rightQuery := NewQueryKey("right-query")
	graph := mustGraph(t,
		Definition{Key: leftQuery, Run: readInputQuery(leftInput)},
		Definition{Key: rightQuery, Run: readInputQuery(rightInput)},
	)
	inputs := map[QueryKey]InputKey{leftQuery: leftInput, rightQuery: rightInput}
	batch := func(ctx context.Context, queries []BatchQuery) ([]BatchValue, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		values := make([]BatchValue, len(queries))
		for index := range queries {
			value, _, err := queries[index].Reader.Input(inputs[queries[index].Key])
			values[index] = BatchValue{Value: value, Err: err}
		}
		return values, nil
	}

	session := mustBegin(t, graph)
	mustApply(t, session,
		exactInput(leftInput, "left-1", "left"),
		exactInput(rightInput, "right-1", "right"),
	)
	results, err := session.EvaluateAllBatch(context.Background(), batch, rightQuery, leftQuery)
	if err != nil {
		t.Fatalf("EvaluateAllBatch() error = %v", err)
	}
	if len(results) != 2 || results[0].Key != leftQuery || string(results[0].Value) != "left" ||
		results[1].Key != rightQuery || string(results[1].Value) != "right" {
		t.Fatalf("batch results = %#v", results)
	}
	mustCommit(t, session)

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(leftInput, "left-2", "changed"))
	called := []QueryKey{}
	results, err = session.EvaluateAllBatch(context.Background(),
		func(ctx context.Context, queries []BatchQuery) ([]BatchValue, error) {
			for _, query := range queries {
				called = append(called, query.Key)
			}
			return batch(ctx, queries)
		},
		leftQuery, rightQuery,
	)
	if err != nil {
		t.Fatalf("second EvaluateAllBatch() error = %v", err)
	}
	if len(called) != 1 || called[0] != leftQuery {
		t.Fatalf("executed queries = %v, want only %v", called, leftQuery)
	}
	if string(results[0].Value) != "changed" || string(results[1].Value) != "right" {
		t.Fatalf("second batch results = %#v", results)
	}
	mustCommit(t, session)
	if got := graph.Counters(leftQuery); got.Executions != 2 || got.Invalidations != 1 {
		t.Fatalf("left counters = %+v", got)
	}
	if got := graph.Counters(rightQuery); got.Executions != 1 || got.CacheHits != 1 {
		t.Fatalf("right counters = %+v", got)
	}
}

func TestEvaluateAllBatchFailurePublishesNothing(t *testing.T) {
	leftInput := NewInputKey("left-input")
	rightInput := NewInputKey("right-input")
	leftQuery := NewQueryKey("left-query")
	rightQuery := NewQueryKey("right-query")
	graph := mustGraph(t,
		Definition{Key: leftQuery, Run: readInputQuery(leftInput)},
		Definition{Key: rightQuery, Run: readInputQuery(rightInput)},
	)

	session := mustBegin(t, graph)
	mustApply(t, session,
		exactInput(leftInput, "left-1", "old-left"),
		exactInput(rightInput, "right-1", "old-right"),
	)
	mustEvaluate(t, session, leftQuery)
	mustEvaluate(t, session, rightQuery)
	mustCommit(t, session)
	wantGeneration := graph.Generation()
	wantLeftCounters := graph.Counters(leftQuery)
	wantRightCounters := graph.Counters(rightQuery)

	session = mustBegin(t, graph)
	mustApply(t, session,
		exactInput(leftInput, "left-2", "new-left"),
		exactInput(rightInput, "right-2", "new-right"),
	)
	runErr := errors.New("middle item failed")
	_, err := session.EvaluateAllBatch(context.Background(),
		func(_ context.Context, queries []BatchQuery) ([]BatchValue, error) {
			if len(queries) != 2 {
				t.Fatalf("batch size = %d, want 2", len(queries))
			}
			return []BatchValue{
				{Value: []byte("new-left")},
				{Err: runErr},
			}, nil
		},
		leftQuery, rightQuery,
	)
	if !errors.Is(err, runErr) {
		t.Fatalf("EvaluateAllBatch() error = %v, want %v", err, runErr)
	}
	if err := session.Commit(context.Background(), acceptRevisions); !errors.Is(err, runErr) {
		t.Fatalf("Commit() error = %v, want %v", err, runErr)
	}
	if graph.Generation() != wantGeneration || graph.Counters(leftQuery) != wantLeftCounters ||
		graph.Counters(rightQuery) != wantRightCounters {
		t.Fatal("failed batch changed committed graph metadata")
	}
	if got := stringValue(t, graph, leftQuery); got != "old-left" {
		t.Fatalf("left value after failure = %q", got)
	}
	if got := stringValue(t, graph, rightQuery); got != "old-right" {
		t.Fatalf("right value after failure = %q", got)
	}
}

func TestEvaluateAllBatchRejectsMemberDependencies(t *testing.T) {
	leftQuery := NewQueryKey("left-query")
	rightQuery := NewQueryKey("right-query")
	graph := mustGraph(t,
		Definition{Key: leftQuery, Run: func(context.Context, Reader) ([]byte, error) { return nil, nil }},
		Definition{Key: rightQuery, Run: func(context.Context, Reader) ([]byte, error) { return nil, nil }},
	)
	session := mustBegin(t, graph)
	_, err := session.EvaluateAllBatch(context.Background(),
		func(ctx context.Context, queries []BatchQuery) ([]BatchValue, error) {
			_, err := queries[0].Reader.Query(ctx, queries[1].Key)
			return nil, err
		},
		leftQuery, rightQuery,
	)
	if err == nil || !strings.Contains(err.Error(), "cannot depend on another batch member") {
		t.Fatalf("EvaluateAllBatch() error = %v", err)
	}
	if graph.Generation() != 0 {
		t.Fatal("rejected member dependency changed the graph")
	}
}

func TestEvaluateAllBatchCancellationPublishesNothing(t *testing.T) {
	query := NewQueryKey("query")
	graph := mustGraph(t, Definition{
		Key: query,
		Run: func(context.Context, Reader) ([]byte, error) { return []byte("ordinary"), nil },
	})
	session := mustBegin(t, graph)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := session.EvaluateAllBatch(ctx,
		func(context.Context, []BatchQuery) ([]BatchValue, error) {
			cancel()
			return []BatchValue{{Value: []byte("batch")}}, nil
		},
		query,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("EvaluateAllBatch() error = %v, want context cancellation", err)
	}
	if graph.Generation() != 0 {
		t.Fatal("canceled batch changed the graph")
	}
	if _, exists := graph.Value(query); exists {
		t.Fatal("canceled batch published a value")
	}
}

func TestEvaluateAllBatchReadersSupportConcurrentExecution(t *testing.T) {
	sharedInput := NewInputKey("shared-input")
	sharedQuery := NewQueryKey("shared-query")
	queries := make([]QueryKey, 128)
	definitions := make([]Definition, 1, 1+len(queries))
	definitions[0] = Definition{Key: sharedQuery, Run: readInputQuery(sharedInput)}
	inputs := make([]Input, 1, 1+len(queries))
	inputs[0] = exactInput(sharedInput, "shared-1", "shared")
	queryInputs := make(map[QueryKey]InputKey, len(queries))
	for index := range queries {
		query := NewQueryKey(fmt.Sprintf("query-%03d", index))
		input := NewInputKey(fmt.Sprintf("input-%03d", index))
		queries[index] = query
		queryInputs[query] = input
		definitions = append(definitions, Definition{
			Key: query,
			Run: func(context.Context, Reader) ([]byte, error) { return nil, nil },
		})
		inputs = append(inputs, exactInput(input, NewRevision(fmt.Sprintf("revision-%03d", index)).Opaque(), "value"))
	}
	graph := mustGraph(t, definitions...)
	session := mustBegin(t, graph)
	mustApply(t, session, inputs...)
	mustEvaluate(t, session, sharedQuery)

	results, err := session.EvaluateAllBatch(t.Context(),
		func(ctx context.Context, batch []BatchQuery) ([]BatchValue, error) {
			values := make([]BatchValue, len(batch))
			var workers sync.WaitGroup
			workers.Add(len(batch))
			for index := range batch {
				go func() {
					defer workers.Done()
					input, err := batch[index].Reader.ExactInput(queryInputs[batch[index].Key])
					if err != nil {
						values[index].Err = err
						return
					}
					shared, err := batch[index].Reader.Query(ctx, sharedQuery)
					if err != nil {
						values[index].Err = err
						return
					}
					value := input.Value
					value = append(value, shared...)
					values[index].Value = value
				}()
			}
			workers.Wait()
			return values, nil
		},
		queries...,
	)
	if err != nil {
		t.Fatalf("EvaluateAllBatch() error = %v", err)
	}
	if len(results) != len(queries) {
		t.Fatalf("EvaluateAllBatch() returned %d results, want %d", len(results), len(queries))
	}
	for index := range results {
		if got := string(results[index].Value); got != "valueshared" {
			t.Fatalf("result %d = %q, want %q", index, got, "valueshared")
		}
	}
	mustCommit(t, session)
}

func TestEvaluateAllBatchOwnedInputsStayDetachedUnderConcurrency(t *testing.T) {
	const queryCount = 20
	inputKey := NewInputKey("shared-input")
	definitions := make([]Definition, queryCount)
	queries := make([]QueryKey, queryCount)
	for index := range queryCount {
		query := NewQueryKey(fmt.Sprintf("query-%02d", index))
		queries[index] = query
		definitions[index] = Definition{
			Key: query,
			Run: func(context.Context, Reader) ([]byte, error) { return nil, nil },
		}
	}
	graph := mustGraph(t, definitions...)
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "revision", "immutable"))
	snapshots := make([][]byte, queryCount)
	initial := make([]string, queryCount)

	_, err := session.EvaluateAllBatch(t.Context(), func(_ context.Context, batch []BatchQuery) ([]BatchValue, error) {
		return runOwnedInputBatch(batch, inputKey, snapshots, initial)
	}, queries...)
	if err != nil {
		t.Fatalf("EvaluateAllBatch() error = %v", err)
	}
	for index := range queryCount {
		if initial[index] != "immutable" {
			t.Fatalf("initial snapshot %d = %q", index, initial[index])
		}
		if snapshots[index][0] != byte('A'+index) {
			t.Fatalf("snapshot %d shares mutable storage: %q", index, snapshots[index])
		}
	}
	mustCommit(t, session)
	readback := mustBegin(t, graph)
	stored, exists, err := readback.ExactInput(inputKey)
	if err != nil || !exists || string(stored.Value) != "immutable" {
		t.Fatalf("ExactInput() = %#v, %t, %v", stored, exists, err)
	}
	readback.Abort()
}

func runOwnedInputBatch(
	batch []BatchQuery,
	inputKey InputKey,
	snapshots [][]byte,
	initial []string,
) ([]BatchValue, error) {
	values := make([]BatchValue, len(batch))
	errs := make([]error, len(batch))
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(len(batch))
	for index := range batch {
		go func() {
			defer workers.Done()
			<-start
			input, readErr := batch[index].Reader.(OwnedInputReader).ExactInputOwned(inputKey)
			if readErr != nil {
				errs[index] = readErr
				return
			}
			initial[index] = string(input.Value)
			snapshots[index] = input.Value
			input.Value[0] = byte('A' + index)
			values[index].Value = []byte("done")
		}()
	}
	close(start)
	workers.Wait()
	for _, readErr := range errs {
		if readErr != nil {
			return nil, readErr
		}
	}
	return values, nil
}

func TestEvaluateAllBatchSameReaderRecordsConcurrentDependencies(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	expected := InputRevision{Key: inputKey, Revision: NewRevision("revision"), Found: true}
	graph := mustGraph(t, Definition{
		Key: queryKey,
		Run: func(context.Context, Reader) ([]byte, error) { return nil, nil },
	})
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "revision", "immutable"))

	_, err := session.EvaluateAllBatch(t.Context(), func(_ context.Context, batch []BatchQuery) ([]BatchValue, error) {
		return runConcurrentDependencyBatch(batch[0], inputKey, expected)
	}, queryKey)
	if err != nil {
		t.Fatalf("EvaluateAllBatch() error = %v", err)
	}
	var verified []InputRevision
	err = session.Commit(t.Context(), func(_ context.Context, inputs []InputRevision) (bool, error) {
		verified = append([]InputRevision(nil), inputs...)
		return true, nil
	})
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if len(verified) != 1 || verified[0] != expected {
		t.Fatalf("verified inputs = %#v, want %#v", verified, expected)
	}
}

func runConcurrentDependencyBatch(
	query BatchQuery,
	inputKey InputKey,
	expected InputRevision,
) ([]BatchValue, error) {
	const workersCount = 64
	errs := make([]error, workersCount)
	var workers sync.WaitGroup
	workers.Add(workersCount)
	for index := range workersCount {
		go func() {
			defer workers.Done()
			if index%2 == 0 {
				errs[index] = query.Reader.(ExactInputObserver).ObserveExactInput(expected)
				return
			}
			input, readErr := query.Reader.(OwnedInputReader).ExactInputOwned(inputKey)
			if readErr == nil {
				input.Value[0] = 'X'
			}
			errs[index] = readErr
		}()
	}
	workers.Wait()
	for _, readErr := range errs {
		if readErr != nil {
			return nil, readErr
		}
	}
	return []BatchValue{{Value: []byte("done")}}, nil
}

func TestEvaluateAllBatchResolverRunsOnceForConcurrentSharedInput(t *testing.T) {
	const queryCount = 64
	inputKey := NewInputKey("lazy-input")
	definitions := make([]Definition, queryCount)
	queries := make([]QueryKey, queryCount)
	for index := range queryCount {
		query := NewQueryKey(fmt.Sprintf("query-%02d", index))
		queries[index] = query
		definitions[index] = Definition{
			Key: query,
			Run: func(context.Context, Reader) ([]byte, error) { return nil, nil },
		}
	}
	graph := mustGraph(t, definitions...)
	resolverValue := []byte("resolver-value")
	var resolverCalls atomic.Int64
	session := mustBeginWithResolver(t, graph, func(_ context.Context, key InputKey) (Input, error) {
		resolverCalls.Add(1)
		return Input{Key: key, Revision: NewRevision("revision"), Found: true, Value: resolverValue}, nil
	})

	_, err := session.EvaluateAllBatch(t.Context(), func(_ context.Context, batch []BatchQuery) ([]BatchValue, error) {
		return runResolvedInputBatch(batch, inputKey)
	}, queries...)
	if err != nil {
		t.Fatalf("EvaluateAllBatch() error = %v", err)
	}
	if resolverCalls.Load() != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolverCalls.Load())
	}
	resolverValue[0] = 'Y'
	mustCommit(t, session)
	readback := mustBegin(t, graph)
	stored, exists, err := readback.ExactInput(inputKey)
	if err != nil || !exists || string(stored.Value) != "resolver-value" {
		t.Fatalf("ExactInput() = %#v, %t, %v", stored, exists, err)
	}
	readback.Abort()
}

func runResolvedInputBatch(batch []BatchQuery, inputKey InputKey) ([]BatchValue, error) {
	values := make([]BatchValue, len(batch))
	errs := make([]error, len(batch))
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(len(batch))
	for index := range batch {
		go func() {
			defer workers.Done()
			<-start
			input, readErr := batch[index].Reader.(OwnedInputReader).ExactInputOwned(inputKey)
			if readErr != nil {
				errs[index] = readErr
				return
			}
			if string(input.Value) != "resolver-value" {
				errs[index] = fmt.Errorf("resolved snapshot = %q", input.Value)
				return
			}
			input.Value[0] = 'X'
			values[index].Value = []byte("done")
		}()
	}
	close(start)
	workers.Wait()
	for _, readErr := range errs {
		if readErr != nil {
			return nil, readErr
		}
	}
	return values, nil
}

func TestEvaluateAllBatchObservationConflictPublishesNothing(t *testing.T) {
	inputKey := NewInputKey("input")
	leftQuery := NewQueryKey("left")
	rightQuery := NewQueryKey("right")
	graph := mustGraph(t,
		Definition{Key: leftQuery, Run: func(context.Context, Reader) ([]byte, error) { return nil, nil }},
		Definition{Key: rightQuery, Run: func(context.Context, Reader) ([]byte, error) { return nil, nil }},
	)
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "current", "value"))
	_, err := session.EvaluateAllBatch(t.Context(), func(_ context.Context, batch []BatchQuery) ([]BatchValue, error) {
		if _, readErr := batch[0].Reader.ExactInput(inputKey); readErr != nil {
			return nil, readErr
		}
		observeErr := batch[1].Reader.(ExactInputObserver).ObserveExactInput(InputRevision{
			Key: inputKey, Revision: NewRevision("stale"), Found: true,
		})
		return []BatchValue{{Value: []byte("left")}, {Err: observeErr}}, nil
	}, leftQuery, rightQuery)
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("EvaluateAllBatch() error = %v, want %v", err, ErrRevisionConflict)
	}
	if err := session.Commit(t.Context(), acceptRevisions); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("Commit() error = %v, want %v", err, ErrRevisionConflict)
	}
	if graph.Generation() != 0 {
		t.Fatalf("failed batch changed generation to %d", graph.Generation())
	}
	if _, exists := graph.Value(leftQuery); exists {
		t.Fatal("failed batch published the valid earlier result")
	}
}

func TestEvaluateAllBatchDrainsAndRevokesReaders(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{
		Key: queryKey,
		Run: func(context.Context, Reader) ([]byte, error) { return nil, nil },
	})
	resolverStarted := make(chan struct{})
	releaseResolver := make(chan struct{})
	session := mustBeginWithResolver(t, graph, func(_ context.Context, key InputKey) (Input, error) {
		close(resolverStarted)
		<-releaseResolver
		return exactInput(key, "revision", "value"), nil
	})
	type evaluation struct {
		results []Result
		err     error
	}
	evaluated := make(chan evaluation, 1)
	batchReturned := make(chan struct{})
	readFinished := make(chan error, 1)
	var retained Reader
	go func() {
		results, err := session.EvaluateAllBatch(t.Context(), func(_ context.Context, batch []BatchQuery) ([]BatchValue, error) {
			retained = batch[0].Reader
			go func() {
				_, readErr := batch[0].Reader.(OwnedInputReader).ExactInputOwned(inputKey)
				readFinished <- readErr
			}()
			<-resolverStarted
			close(batchReturned)
			return []BatchValue{{Value: []byte("done")}}, nil
		}, queryKey)
		evaluated <- evaluation{results: results, err: err}
	}()
	<-batchReturned
	select {
	case result := <-evaluated:
		t.Fatalf("EvaluateAllBatch() returned before an accepted read drained: %#v", result)
	default:
	}
	close(releaseResolver)
	if err := <-readFinished; err != nil {
		t.Fatalf("accepted read error = %v", err)
	}
	result := <-evaluated
	if result.err != nil || len(result.results) != 1 || string(result.results[0].Value) != "done" {
		t.Fatalf("EvaluateAllBatch() = %#v, %v", result.results, result.err)
	}
	if _, _, err := retained.Input(inputKey); err == nil {
		t.Fatal("retained reader remained active after batch return")
	}
	mustCommit(t, session)
}
