package incremental

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestEvaluateAllExactBatchReusesAuthenticatedRoots(t *testing.T) {
	left := NewQueryKey("left")
	right := NewQueryKey("right")
	graph := exactValueTestGraph(t, left, right)
	session := mustBegin(t, graph)
	results, err := session.EvaluateAllExactBatch(t.Context(), exactStringBatch(map[QueryKey]string{
		left: "left-value", right: "right-value",
	}), right, left)
	if err != nil {
		t.Fatalf("EvaluateAllExactBatch() error = %v", err)
	}
	if len(results) != 2 || results[0].Key != left || results[1].Key != right {
		t.Fatalf("results = %#v", results)
	}
	assertExactValue(t, results[0].Value, "left-value")
	assertExactValue(t, results[1].Value, "right-value")
	leftRoot := results[0].Value
	rightRoot := results[1].Value
	mustCommit(t, session)

	committed, found, err := graph.ExactValue(left)
	if err != nil || !found || committed != leftRoot {
		t.Fatalf("ExactValue() = %v, %v, %v; want %v, true, nil", committed, found, err, leftRoot)
	}
	session = mustBegin(t, graph)
	results, err = session.EvaluateAllExactBatch(t.Context(), func(context.Context, []BatchQuery) ([]ExactBatchValue, error) {
		t.Fatal("warm exact-root hit executed the batch")
		return nil, nil
	}, right, left)
	if err != nil {
		t.Fatalf("warm EvaluateAllExactBatch() error = %v", err)
	}
	if results[0].Value != leftRoot || results[1].Value != rightRoot {
		t.Fatalf("warm roots = %v, %v; want %v, %v", results[0].Value, results[1].Value, leftRoot, rightRoot)
	}
	mustCommit(t, session)
}

func TestExactValueRootIdentityAndExactEquality(t *testing.T) {
	query := NewQueryKey("query")
	graph := exactValueTestGraph(t, query)
	session := mustBegin(t, graph)
	var peer ExactValueRoot
	results, err := session.EvaluateAllExactBatch(t.Context(), func(_ context.Context, queries []BatchQuery) ([]ExactBatchValue, error) {
		root, err := queries[0].NewExactValue("same")
		if err != nil {
			return nil, err
		}
		peer, err = queries[0].NewExactValue("same")
		return []ExactBatchValue{{Value: root, Err: err}}, nil
	}, query)
	if err != nil {
		t.Fatalf("EvaluateAllExactBatch() error = %v", err)
	}
	same, err := results[0].Value.SameRoot(peer)
	if err != nil || same {
		t.Fatalf("SameRoot() = %v, %v; want false, nil", same, err)
	}
	equal, err := results[0].Value.ExactEqual(peer)
	if err != nil || !equal {
		t.Fatalf("ExactEqual() = %v, %v; want true, nil", equal, err)
	}
}

func TestExecutedEqualExactValueRetainsCanonicalRoot(t *testing.T) {
	inputKey := NewInputKey("input")
	query := NewQueryKey("query")
	graph := exactValueTestGraph(t, query)

	first := mustBegin(t, graph)
	mustApply(t, first, exactInput(inputKey, "r1", "first"))
	firstResults, err := first.EvaluateAllExactBatch(t.Context(), exactInputConstantBatch(inputKey, "same"), query)
	if err != nil {
		t.Fatalf("first EvaluateAllExactBatch() error = %v", err)
	}
	mustCommit(t, first)

	second := mustBegin(t, graph)
	mustApply(t, second, exactInput(inputKey, "r2", "second"))
	secondResults, err := second.EvaluateAllExactBatch(t.Context(), exactInputConstantBatch(inputKey, "same"), query)
	if err != nil {
		t.Fatalf("second EvaluateAllExactBatch() error = %v", err)
	}
	if secondResults[0].Value != firstResults[0].Value {
		t.Fatalf("equal execution root = %v, want canonical %v", secondResults[0].Value, firstResults[0].Value)
	}
	mustCommit(t, second)
	if got := graph.Counters(query); got.Executions != 2 || got.Changes != 1 || got.Backdates != 1 {
		t.Fatalf("equal execution counters = %+v", got)
	}
}

func TestEvaluateAllExactBatchRejectsInvalidRoots(t *testing.T) {
	query := NewQueryKey("query")
	other := NewQueryKey("other")
	foreign := exactValueTestGraph(t, query)
	foreignSession := mustBegin(t, foreign)
	foreignResults, err := foreignSession.EvaluateAllExactBatch(
		t.Context(), exactStringBatch(map[QueryKey]string{query: "foreign"}), query,
	)
	if err != nil {
		t.Fatalf("foreign EvaluateAllExactBatch() error = %v", err)
	}
	foreignSession.Abort()

	tests := []struct {
		name  string
		keys  []QueryKey
		batch ExactBatchQueryFunc
	}{
		{
			name: "zero",
			keys: []QueryKey{query},
			batch: func(context.Context, []BatchQuery) ([]ExactBatchValue, error) {
				return []ExactBatchValue{{}}, nil
			},
		},
		{
			name: "malformed",
			keys: []QueryKey{query},
			batch: func(context.Context, []BatchQuery) ([]ExactBatchValue, error) {
				return []ExactBatchValue{{Value: ExactValueRoot{value: &exactValue{}}}}, nil
			},
		},
		{
			name: "copied wrapper",
			keys: []QueryKey{query},
			batch: func(_ context.Context, queries []BatchQuery) ([]ExactBatchValue, error) {
				root, err := queries[0].NewExactValue("value")
				if err != nil {
					return nil, err
				}
				copied := *root.value
				root.value = &copied
				return []ExactBatchValue{{Value: root}}, nil
			},
		},
		{
			name: "substituted storage",
			keys: []QueryKey{query},
			batch: func(_ context.Context, queries []BatchQuery) ([]ExactBatchValue, error) {
				root, err := queries[0].NewExactValue("value")
				if err != nil {
					return nil, err
				}
				otherRoot, err := queries[0].NewExactValue("other")
				if err != nil {
					return nil, err
				}
				root.value.storage = otherRoot.value.storage
				return []ExactBatchValue{{Value: root}}, nil
			},
		},
		{
			name: "copied execution",
			keys: []QueryKey{query},
			batch: func(_ context.Context, queries []BatchQuery) ([]ExactBatchValue, error) {
				root, err := queries[0].NewExactValue("value")
				if err != nil {
					return nil, err
				}
				copied := *root.value.execution
				copied.seal = &copied
				root.value.execution = &copied
				root.value.storage.execution = &copied
				return []ExactBatchValue{{Value: root}}, nil
			},
		},
		{
			name: "other query",
			keys: []QueryKey{query, other},
			batch: func(_ context.Context, queries []BatchQuery) ([]ExactBatchValue, error) {
				first, err := queries[0].NewExactValue(queries[0].Key.Opaque())
				if err != nil {
					return nil, err
				}
				second, err := queries[1].NewExactValue(queries[1].Key.Opaque())
				if err != nil {
					return nil, err
				}
				return []ExactBatchValue{{Value: second}, {Value: first}}, nil
			},
		},
		{
			name: "foreign graph",
			keys: []QueryKey{query},
			batch: func(context.Context, []BatchQuery) ([]ExactBatchValue, error) {
				return []ExactBatchValue{{Value: foreignResults[0].Value}}, nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertInvalidExactValueBatchRejected(t, test.keys, test.batch)
		})
	}
}

func assertInvalidExactValueBatchRejected(
	t *testing.T,
	keys []QueryKey,
	batch ExactBatchQueryFunc,
) {
	t.Helper()
	graph := exactValueTestGraph(t, keys...)
	session := mustBegin(t, graph)
	_, err := session.EvaluateAllExactBatch(t.Context(), batch, keys...)
	if err == nil || !strings.Contains(err.Error(), "invalid provenance") &&
		!strings.Contains(err.Error(), "belongs to another query") {
		t.Fatalf("EvaluateAllExactBatch() error = %v", err)
	}
	if graph.Generation() != 0 {
		t.Fatal("invalid exact root changed the graph")
	}
}

func TestExactValueRootABARetainsHistorySemantics(t *testing.T) {
	inputKey := NewInputKey("input")
	query := NewQueryKey("query")
	graph := exactValueTestGraph(t, query)

	render := func(session *Session, revision, value string) ExactValueRoot {
		t.Helper()
		mustApply(t, session, exactInput(inputKey, revision, value))
		results, err := session.EvaluateAllExactBatch(t.Context(), func(_ context.Context, queries []BatchQuery) ([]ExactBatchValue, error) {
			observed, found, err := queries[0].Reader.Input(inputKey)
			if err != nil || !found {
				return nil, fmt.Errorf("reading input: found=%v: %w", found, err)
			}
			root, err := queries[0].NewExactValue(string(observed))
			return []ExactBatchValue{{Value: root, Err: err}}, nil
		}, query)
		if err != nil {
			t.Fatalf("EvaluateAllExactBatch() error = %v", err)
		}
		mustCommit(t, session)
		return results[0].Value
	}

	firstA := render(mustBegin(t, graph), "r1", "A")
	rootB := render(mustBegin(t, graph), "r2", "B")
	secondA := render(mustBegin(t, graph), "r3", "A")
	if firstA == rootB || secondA == firstA || secondA == rootB {
		t.Fatalf("A-B-A roots = %v, %v, %v", firstA, rootB, secondA)
	}
	if got := graph.Counters(query); got.Executions != 3 || got.Changes != 3 || got.Backdates != 0 {
		t.Fatalf("A-B-A counters = %+v", got)
	}
	assertExactValue(t, secondA, "A")
}

func TestExactBatchCancellationFailureAndConflictPublishNothing(t *testing.T) {
	inputKey := NewInputKey("input")
	query := NewQueryKey("query")
	graph := exactValueTestGraph(t, query)
	seed := mustBegin(t, graph)
	mustApply(t, seed, exactInput(inputKey, "r1", "old"))
	seedResults, err := seed.EvaluateAllExactBatch(t.Context(), exactInputStringBatch(inputKey), query)
	if err != nil {
		t.Fatalf("seed EvaluateAllExactBatch() error = %v", err)
	}
	mustCommit(t, seed)
	oldRoot := seedResults[0].Value

	t.Run("cancellation", func(t *testing.T) {
		session := mustBegin(t, graph)
		mustApply(t, session, exactInput(inputKey, "r2", "cancelled"))
		ctx, cancel := context.WithCancel(t.Context())
		_, err := session.EvaluateAllExactBatch(ctx, func(_ context.Context, queries []BatchQuery) ([]ExactBatchValue, error) {
			root, rootErr := queries[0].NewExactValue("cancelled")
			cancel()
			return []ExactBatchValue{{Value: root, Err: rootErr}}, nil
		}, query)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("EvaluateAllExactBatch() error = %v, want context cancellation", err)
		}
		assertCommittedRoot(t, graph, query, oldRoot)
	})

	t.Run("result failure", func(t *testing.T) {
		session := mustBegin(t, graph)
		mustApply(t, session, exactInput(inputKey, "r2", "failed"))
		runErr := errors.New("failed")
		_, err := session.EvaluateAllExactBatch(t.Context(), func(context.Context, []BatchQuery) ([]ExactBatchValue, error) {
			return []ExactBatchValue{{Err: runErr}}, nil
		}, query)
		if !errors.Is(err, runErr) {
			t.Fatalf("EvaluateAllExactBatch() error = %v, want %v", err, runErr)
		}
		assertCommittedRoot(t, graph, query, oldRoot)
	})

	t.Run("commit conflict", func(t *testing.T) {
		first := mustBegin(t, graph)
		second := mustBegin(t, graph)
		mustApply(t, first, exactInput(inputKey, "r2", "first"))
		mustApply(t, second, exactInput(inputKey, "r3", "second"))
		firstResults, err := first.EvaluateAllExactBatch(t.Context(), exactInputStringBatch(inputKey), query)
		if err != nil {
			t.Fatalf("first EvaluateAllExactBatch() error = %v", err)
		}
		if _, err := second.EvaluateAllExactBatch(t.Context(), exactInputStringBatch(inputKey), query); err != nil {
			t.Fatalf("second EvaluateAllExactBatch() error = %v", err)
		}
		mustCommit(t, first)
		if err := second.Commit(t.Context(), acceptRevisions); !errors.Is(err, ErrCommitConflict) {
			t.Fatalf("second Commit() error = %v, want ErrCommitConflict", err)
		}
		assertCommittedRoot(t, graph, query, firstResults[0].Value)
	})
}

func TestExactBatchConcurrentRootCreation(t *testing.T) {
	const count = 128
	keys := make([]QueryKey, count)
	for index := range keys {
		keys[index] = NewQueryKey(fmt.Sprintf("query-%03d", index))
	}
	graph := exactValueTestGraph(t, keys...)
	session := mustBegin(t, graph)
	results, err := session.EvaluateAllExactBatch(t.Context(), func(_ context.Context, queries []BatchQuery) ([]ExactBatchValue, error) {
		values := make([]ExactBatchValue, len(queries))
		var group sync.WaitGroup
		for index := range queries {
			group.Add(1)
			go func(index int) {
				defer group.Done()
				root, err := queries[index].NewExactValue(queries[index].Key.Opaque())
				values[index] = ExactBatchValue{Value: root, Err: err}
			}(index)
		}
		group.Wait()
		return values, nil
	}, keys...)
	if err != nil {
		t.Fatalf("EvaluateAllExactBatch() error = %v", err)
	}
	for index := range results {
		assertExactValue(t, results[index].Value, results[index].Key.Opaque())
	}
	mustCommit(t, session)
}

func TestExactBatchFactoryIsRevoked(t *testing.T) {
	query := NewQueryKey("query")
	graph := exactValueTestGraph(t, query)
	session := mustBegin(t, graph)
	var saved BatchQuery
	if _, err := session.EvaluateAllExactBatch(t.Context(), func(_ context.Context, queries []BatchQuery) ([]ExactBatchValue, error) {
		saved = queries[0]
		root, err := saved.NewExactValue("value")
		return []ExactBatchValue{{Value: root, Err: err}}, nil
	}, query); err != nil {
		t.Fatalf("EvaluateAllExactBatch() error = %v", err)
	}
	if _, err := saved.NewExactValue("late"); err == nil || !strings.Contains(err.Error(), "no longer active") {
		t.Fatalf("late NewExactValue() error = %v", err)
	}
}

func TestLegacyValuesRemainMutationIsolated(t *testing.T) {
	query := NewQueryKey("query")
	produced := []byte("authoritative")
	graph := mustGraph(t, Definition{Key: query, Run: func(context.Context, Reader) ([]byte, error) {
		return produced, nil
	}})
	session := mustBegin(t, graph)
	result := mustEvaluate(t, session, query)
	produced[0] = 'P'
	result[1] = 'X'
	mustCommit(t, session)
	if got := stringValue(t, graph, query); got != "authoritative" {
		t.Fatalf("committed value = %q", got)
	}
	first, found := graph.Value(query)
	if !found {
		t.Fatal("Value() did not find committed query")
	}
	first[0] = 'X'
	second, found := graph.Value(query)
	if !found || string(second) != "authoritative" {
		t.Fatalf("Value() after caller mutation = %q, %v", second, found)
	}
}

func TestLegacyBatchValuesRemainMutationIsolated(t *testing.T) {
	query := NewQueryKey("query")
	graph := exactValueTestGraph(t, query)
	produced := []byte("authoritative")
	session := mustBegin(t, graph)
	results, err := session.EvaluateAllBatch(t.Context(), func(context.Context, []BatchQuery) ([]BatchValue, error) {
		return []BatchValue{{Value: produced}}, nil
	}, query)
	if err != nil {
		t.Fatalf("EvaluateAllBatch() error = %v", err)
	}
	produced[0] = 'P'
	results[0].Value[1] = 'X'
	mustCommit(t, session)
	if got := stringValue(t, graph, query); got != "authoritative" {
		t.Fatalf("committed batch value = %q", got)
	}

	session = mustBegin(t, graph)
	results, err = session.EvaluateAllBatch(t.Context(), func(context.Context, []BatchQuery) ([]BatchValue, error) {
		t.Fatal("warm legacy batch hit executed the batch")
		return nil, nil
	}, query)
	if err != nil {
		t.Fatalf("warm EvaluateAllBatch() error = %v", err)
	}
	results[0].Value[0] = 'X'
	session.Abort()
	if got := stringValue(t, graph, query); got != "authoritative" {
		t.Fatalf("committed batch value after warm mutation = %q", got)
	}
}

func TestLegacyEmptyValueRemainsNil(t *testing.T) {
	query := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: query, Run: func(context.Context, Reader) ([]byte, error) {
		return []byte{}, nil
	}})
	session := mustBegin(t, graph)
	if value := mustEvaluate(t, session, query); value != nil {
		t.Fatalf("Evaluate() = %#v, want nil", value)
	}
	mustCommit(t, session)
	value, found := graph.Value(query)
	if !found || value != nil {
		t.Fatalf("Value() = %#v, %v; want nil, true", value, found)
	}
}

func TestPoisonedExactRootsFailClosed(t *testing.T) {
	left := NewQueryKey("left")
	right := NewQueryKey("right")
	graph := exactValueTestGraph(t, left, right)
	session := mustBegin(t, graph)
	results, err := session.EvaluateAllExactBatch(t.Context(), exactStringBatch(map[QueryKey]string{
		left: "left", right: "right",
	}), left, right)
	if err != nil {
		t.Fatalf("EvaluateAllExactBatch() error = %v", err)
	}
	mustCommit(t, session)

	graph.mu.Lock()
	entry, exists := graph.current.nodes.Root().Get([]byte(left.value))
	if !exists {
		graph.mu.Unlock()
		t.Fatal("committed left query is missing")
	}
	entry.value = results[1].Value
	poisoned, _, _ := graph.current.nodes.Insert([]byte(left.value), entry)
	graph.current.nodes = poisoned
	graph.mu.Unlock()

	if _, found, err := graph.ExactValue(left); err == nil || found {
		t.Fatalf("ExactValue() = _, %v, %v; want provenance error", found, err)
	}
	if _, err := graph.Begin(); err == nil {
		t.Fatal("Begin() accepted a substituted committed root")
	}
}

func TestCommitRejectsSubstitutedSpeculativeRoot(t *testing.T) {
	left := NewQueryKey("left")
	right := NewQueryKey("right")
	graph := exactValueTestGraph(t, left, right)
	session := mustBegin(t, graph)
	results, err := session.EvaluateAllExactBatch(t.Context(), exactStringBatch(map[QueryKey]string{
		left: "left", right: "right",
	}), left, right)
	if err != nil {
		t.Fatalf("EvaluateAllExactBatch() error = %v", err)
	}
	entry := session.nodeChanges[left]
	entry.value = results[1].Value
	session.nodeChanges[left] = entry
	if err := session.Commit(t.Context(), acceptRevisions); err == nil || !strings.Contains(err.Error(), "belongs to another query") {
		t.Fatalf("Commit() error = %v", err)
	}
	if graph.Generation() != 0 {
		t.Fatal("poisoned speculative root changed the graph")
	}
}

func exactValueTestGraph(t *testing.T, keys ...QueryKey) *Graph {
	t.Helper()
	definitions := make([]Definition, len(keys))
	for index := range keys {
		definitions[index] = Definition{Key: keys[index], Run: func(context.Context, Reader) ([]byte, error) {
			return nil, nil
		}}
	}
	return mustGraph(t, definitions...)
}

func exactStringBatch(values map[QueryKey]string) ExactBatchQueryFunc {
	return func(_ context.Context, queries []BatchQuery) ([]ExactBatchValue, error) {
		results := make([]ExactBatchValue, len(queries))
		for index := range queries {
			root, err := queries[index].NewExactValue(values[queries[index].Key])
			results[index] = ExactBatchValue{Value: root, Err: err}
		}
		return results, nil
	}
}

func exactInputStringBatch(key InputKey) ExactBatchQueryFunc {
	return func(_ context.Context, queries []BatchQuery) ([]ExactBatchValue, error) {
		results := make([]ExactBatchValue, len(queries))
		for index := range queries {
			value, found, err := queries[index].Reader.Input(key)
			if err != nil || !found {
				results[index].Err = fmt.Errorf("reading input: found=%v: %w", found, err)
				continue
			}
			results[index].Value, results[index].Err = queries[index].NewExactValue(string(value))
		}
		return results, nil
	}
}

func exactInputConstantBatch(key InputKey, value string) ExactBatchQueryFunc {
	return func(_ context.Context, queries []BatchQuery) ([]ExactBatchValue, error) {
		results := make([]ExactBatchValue, len(queries))
		for index := range queries {
			if _, found, err := queries[index].Reader.Input(key); err != nil || !found {
				results[index].Err = fmt.Errorf("reading input: found=%v: %w", found, err)
				continue
			}
			results[index].Value, results[index].Err = queries[index].NewExactValue(value)
		}
		return results, nil
	}
}

func assertExactValue(t *testing.T, root ExactValueRoot, want string) {
	t.Helper()
	if err := root.ValidateAuthentication(); err != nil {
		t.Fatalf("ValidateAuthentication() error = %v", err)
	}
	got, err := root.String()
	if err != nil || got != want {
		t.Fatalf("String() = %q, %v; want %q, nil", got, err, want)
	}
	bytes, err := root.Bytes()
	if err != nil || string(bytes) != want {
		t.Fatalf("Bytes() = %q, %v; want %q, nil", bytes, err, want)
	}
	if len(bytes) > 0 {
		bytes[0] ^= 0xff
		again, readErr := root.String()
		if readErr != nil || again != want {
			t.Fatalf("String() after Bytes mutation = %q, %v; want %q, nil", again, readErr, want)
		}
	}
}

func assertCommittedRoot(t *testing.T, graph *Graph, key QueryKey, want ExactValueRoot) {
	t.Helper()
	got, found, err := graph.ExactValue(key)
	if err != nil || !found || got != want {
		t.Fatalf("ExactValue() = %v, %v, %v; want %v, true, nil", got, found, err, want)
	}
}
