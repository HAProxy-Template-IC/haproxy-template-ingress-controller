package incremental

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestFailedWorkRollsBack(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	runError := errors.New("query failed")
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		value, _, err := reader.Input(inputKey)
		if err != nil {
			return nil, err
		}
		if string(value) == "bad" {
			return nil, runError
		}
		return value, nil
	}})

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r1", "good"))
	mustEvaluate(t, session, queryKey)
	mustCommit(t, session)
	wantCounters := graph.Counters(queryKey)
	wantGeneration := graph.Generation()

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r2", "bad"))
	if _, err := session.Evaluate(context.Background(), queryKey); !errors.Is(err, runError) {
		t.Fatalf("Evaluate() error = %v, want %v", err, runError)
	}
	if err := session.Commit(context.Background(), acceptRevisions); !errors.Is(err, runError) {
		t.Fatalf("Commit() error = %v, want failed query", err)
	}
	if got := stringValue(t, graph, queryKey); got != "good" {
		t.Fatalf("value after rollback = %q", got)
	}
	if graph.Generation() != wantGeneration {
		t.Fatalf("generation after rollback = %d", graph.Generation())
	}
	if got := graph.Counters(queryKey); got != wantCounters {
		t.Fatalf("counters after rollback = %+v, want %+v", got, wantCounters)
	}

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r3", "recovered"))
	if got := string(mustEvaluate(t, session, queryKey)); got != "recovered" {
		t.Fatalf("retry value = %q", got)
	}
	mustCommit(t, session)
}

func TestCommitCASAndVerifierConflict(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		value, _, err := reader.Input(inputKey)
		return value, err
	}})

	first := mustBegin(t, graph)
	second := mustBegin(t, graph)
	mustApply(t, first, exactInput(inputKey, "r1", "first"))
	mustApply(t, second, exactInput(inputKey, "r2", "second"))
	mustEvaluate(t, first, queryKey)
	mustEvaluate(t, second, queryKey)
	mustCommit(t, first)
	var secondVerifierCalls atomic.Int32
	err := second.Commit(context.Background(), func(context.Context, []InputRevision) (bool, error) {
		secondVerifierCalls.Add(1)
		return true, nil
	})
	if !errors.Is(err, ErrCommitConflict) {
		t.Fatalf("second Commit() error = %v, want ErrCommitConflict", err)
	}
	if secondVerifierCalls.Load() != 0 {
		t.Fatalf("CAS loser called verifier %d times", secondVerifierCalls.Load())
	}
	if got := stringValue(t, graph, queryKey); got != "first" {
		t.Fatalf("CAS winner value = %q", got)
	}

	generation := graph.Generation()
	counters := graph.Counters(queryKey)
	conflicted := mustBegin(t, graph)
	mustApply(t, conflicted, exactInput(inputKey, "r3", "unverified"))
	mustEvaluate(t, conflicted, queryKey)
	err = conflicted.Commit(context.Background(), func(context.Context, []InputRevision) (bool, error) {
		return false, nil
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("Commit() error = %v, want ErrRevisionConflict", err)
	}
	if graph.Generation() != generation || graph.Counters(queryKey) != counters {
		t.Fatal("verifier conflict mutated committed state")
	}
	if got := stringValue(t, graph, queryKey); got != "first" {
		t.Fatalf("value after verifier conflict = %q", got)
	}
}

func TestCommitWithPublisherRunsOnlyAfterFallibleChecks(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r1", "rejected"))
	mustEvaluate(t, session, queryKey)
	published := false
	err := session.CommitWithPublisher(context.Background(), func(context.Context, []InputRevision) (bool, error) {
		return false, nil
	}, func() {
		published = true
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("CommitWithPublisher() error = %v, want ErrRevisionConflict", err)
	}
	if published {
		t.Fatal("publisher ran after failed verification")
	}
	if _, exists := graph.Value(queryKey); exists {
		t.Fatal("failed verification published query value")
	}

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r2", "accepted"))
	mustEvaluate(t, session, queryKey)
	ctx, cancel := context.WithCancel(context.Background())
	err = session.CommitWithPublisher(ctx, acceptRevisions, func() {
		published = true
		cancel()
	})
	if err != nil {
		t.Fatalf("CommitWithPublisher() error = %v", err)
	}
	if !published || graph.Generation() != 1 || stringValue(t, graph, queryKey) != "accepted" {
		t.Fatal("successful publisher did not publish graph state")
	}
}

func TestPreparedPublicationCancellationAbortsBeforePublisher(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r1", "candidate"))
	mustEvaluate(t, session, queryKey)
	ctx, cancel := context.WithCancel(context.Background())
	published := false
	completed := false
	aborted := false

	err := session.CommitWithPreparedPublisher(ctx, acceptRevisions, func([]InputKey) (CommitPublication, error) {
		cancel()
		return CommitPublication{
			Publish:  func() { published = true },
			Complete: func() { completed = true },
			Abort:    func() { aborted = true },
		}, nil
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CommitWithPreparedPublisher() error = %v, want context cancellation", err)
	}
	if published || completed || !aborted {
		t.Fatalf("prepared publication state = publish:%v complete:%v abort:%v", published, completed, aborted)
	}
	if graph.Generation() != 0 {
		t.Fatalf("canceled prepared publication changed generation to %d", graph.Generation())
	}
	if _, exists := graph.Value(queryKey); exists {
		t.Fatal("canceled prepared publication stored query value")
	}
}

func TestPreparedPublicationFailureRunsNoPublisher(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r1", "candidate"))
	mustEvaluate(t, session, queryKey)
	prepareErr := errors.New("state plan failed")
	published := false

	err := session.CommitWithPreparedPublisher(context.Background(), acceptRevisions,
		func([]InputKey) (CommitPublication, error) {
			return CommitPublication{Publish: func() { published = true }}, prepareErr
		})

	if !errors.Is(err, prepareErr) {
		t.Fatalf("CommitWithPreparedPublisher() error = %v, want preparation failure", err)
	}
	if published {
		t.Fatal("publisher ran after publication preparation failed")
	}
	if graph.Generation() != 0 {
		t.Fatalf("failed publication preparation changed generation to %d", graph.Generation())
	}
}

func TestCyclesErrorsPanicsAndCancellationAbort(t *testing.T) {
	leftKey := NewQueryKey("left")
	rightKey := NewQueryKey("right")
	cycleGraph := mustGraph(t,
		Definition{Key: leftKey, Run: func(ctx context.Context, reader Reader) ([]byte, error) {
			return reader.Query(ctx, rightKey)
		}},
		Definition{Key: rightKey, Run: func(ctx context.Context, reader Reader) ([]byte, error) {
			return reader.Query(ctx, leftKey)
		}},
	)
	session := mustBegin(t, cycleGraph)
	_, err := session.Evaluate(context.Background(), leftKey)
	var cycle *CycleError
	if !errors.As(err, &cycle) || len(cycle.Path) != 3 {
		t.Fatalf("cycle error = %#v", err)
	}
	if cycleGraph.Generation() != 0 {
		t.Fatal("cycle mutated graph")
	}

	panicKey := NewQueryKey("panic")
	panicGraph := mustGraph(t, Definition{Key: panicKey, Run: func(context.Context, Reader) ([]byte, error) {
		panic("boom")
	}})
	panicSession := mustBegin(t, panicGraph)
	if _, err := panicSession.Evaluate(context.Background(), panicKey); err == nil {
		t.Fatal("query panic succeeded")
	}
	if panicGraph.Generation() != 0 {
		t.Fatal("panic mutated graph")
	}

	cancelKey := NewQueryKey("cancel")
	cancelGraph := mustGraph(t, Definition{Key: cancelKey, Run: func(ctx context.Context, _ Reader) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	cancelSession := mustBegin(t, cancelGraph)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cancelSession.Evaluate(ctx, cancelKey); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Evaluate() error = %v", err)
	}
	if cancelGraph.Generation() != 0 {
		t.Fatal("cancellation mutated graph")
	}
}

func TestColdResetIsTransactional(t *testing.T) {
	leftInput := NewInputKey("left-input")
	rightInput := NewInputKey("right-input")
	leftQuery := NewQueryKey("left-query")
	rightQuery := NewQueryKey("right-query")
	read := func(key InputKey) QueryFunc {
		return func(_ context.Context, reader Reader) ([]byte, error) {
			value, _, err := reader.Input(key)
			return value, err
		}
	}
	graph := mustGraph(t,
		Definition{Key: leftQuery, Run: read(leftInput)},
		Definition{Key: rightQuery, Run: read(rightInput)},
	)
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(leftInput, "l1", "left"), exactInput(rightInput, "r1", "right"))
	if _, err := session.EvaluateAll(context.Background(), leftQuery, rightQuery); err != nil {
		t.Fatalf("EvaluateAll() error = %v", err)
	}
	mustCommit(t, session)
	generation := graph.Generation()

	failed, err := graph.BeginColdReset(exactInput(rightInput, "r2", "new-right"))
	if err != nil {
		t.Fatalf("BeginColdReset() error = %v", err)
	}
	if _, err := failed.Evaluate(context.Background(), leftQuery); err == nil {
		t.Fatal("cold query without an exact input succeeded")
	}
	failed.Abort()
	if graph.Generation() != generation || stringValue(t, graph, leftQuery) != "left" {
		t.Fatal("failed cold reset mutated graph")
	}

	cold, err := graph.BeginColdReset(exactInput(leftInput, "l2", "new-left"))
	if err != nil {
		t.Fatalf("BeginColdReset() error = %v", err)
	}
	if got := string(mustEvaluate(t, cold, leftQuery)); got != "new-left" {
		t.Fatalf("cold value = %q", got)
	}
	mustCommit(t, cold)
	if _, exists := graph.Value(rightQuery); exists {
		t.Fatal("cold reset retained an unevaluated node")
	}

	missing := mustBegin(t, graph)
	if _, err := missing.Evaluate(context.Background(), rightQuery); err == nil {
		t.Fatal("cold reset retained an omitted input")
	}
}

func TestValuesAreClonedAtEveryBoundary(t *testing.T) {
	inputKey := NewInputKey("input")
	mutatorKey := NewQueryKey("a-mutator")
	readerKey := NewQueryKey("b-reader")
	graph := mustGraph(t,
		Definition{Key: mutatorKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
			value, _, err := reader.Input(inputKey)
			if err != nil {
				return nil, err
			}
			value[0] = 'm'
			return value, nil
		}},
		Definition{Key: readerKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
			value, _, err := reader.Input(inputKey)
			return value, err
		}},
	)
	original := []byte("input")
	session := mustBegin(t, graph)
	mustApply(t, session, Input{Key: inputKey, Revision: NewRevision("r1"), Found: true, Value: original})
	original[0] = 'x'
	results, err := session.EvaluateAll(context.Background(), readerKey, mutatorKey)
	if err != nil {
		t.Fatalf("EvaluateAll() error = %v", err)
	}
	if string(results[0].Value) != "mnput" || string(results[1].Value) != "input" {
		t.Fatalf("results = %q, %q", results[0].Value, results[1].Value)
	}
	results[0].Value[0] = 'z'
	mustCommit(t, session)
	if got := stringValue(t, graph, mutatorKey); got != "mnput" {
		t.Fatalf("committed mutator = %q", got)
	}
	first, _ := graph.Value(readerKey)
	first[0] = 'z'
	second, _ := graph.Value(readerKey)
	if got := string(second); got != "input" {
		t.Fatalf("Value() alias changed committed bytes to %q", got)
	}
}

func TestCommittedInvalidationHidesStaleValue(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		value, _, err := reader.Input(inputKey)
		return value, err
	}})
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r1", "old"))
	mustEvaluate(t, session, queryKey)
	mustCommit(t, session)

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r2", "new"))
	mustCommit(t, session)
	if _, exists := graph.Value(queryKey); exists {
		t.Fatal("Value() returned a dirty committed entry")
	}

	session = mustBegin(t, graph)
	if got := string(mustEvaluate(t, session, queryKey)); got != "new" {
		t.Fatalf("value after deferred rerun = %q", got)
	}
	mustCommit(t, session)
}

func TestVerifierCoversSpeculativeNodeUpdates(t *testing.T) {
	childInput := NewInputKey("child-input")
	selectorInput := NewInputKey("selector-input")
	childKey := NewQueryKey("a-child")
	selectorKey := NewQueryKey("z-selector")
	rootKey := NewQueryKey("root")
	graph := mustGraph(t,
		Definition{Key: childKey, Run: readInputQuery(childInput)},
		Definition{Key: selectorKey, Run: readInputQuery(selectorInput)},
		Definition{Key: rootKey, Run: func(ctx context.Context, reader Reader) ([]byte, error) {
			child, err := reader.Query(ctx, childKey)
			if err != nil {
				return nil, err
			}
			selector, err := reader.Query(ctx, selectorKey)
			if err != nil {
				return nil, err
			}
			if string(selector) == "drop" {
				return []byte("dropped"), nil
			}
			return child, nil
		}},
	)
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(childInput, "c1", "old"), exactInput(selectorInput, "s1", "keep"))
	mustEvaluate(t, session, rootKey)
	mustCommit(t, session)

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(childInput, "c2", "new"), exactInput(selectorInput, "s2", "drop"))
	if got := string(mustEvaluate(t, session, rootKey)); got != "dropped" {
		t.Fatalf("root value = %q", got)
	}
	var observations []InputRevision
	err := session.Commit(context.Background(), func(_ context.Context, inputs []InputRevision) (bool, error) {
		observations = append([]InputRevision(nil), inputs...)
		return true, nil
	})
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if !hasRevision(observations, childInput, NewRevision("c2")) {
		t.Fatalf("speculative child revision missing from verifier: %#v", observations)
	}
}

func readInputQuery(key InputKey) QueryFunc {
	return func(_ context.Context, reader Reader) ([]byte, error) {
		value, _, err := reader.Input(key)
		return value, err
	}
}

func hasRevision(inputs []InputRevision, key InputKey, revision Revision) bool {
	for _, input := range inputs {
		if input.Key == key && input.Revision == revision {
			return true
		}
	}
	return false
}

// An admission render reads a session it never commits while the reconcile
// path commits generations underneath it. The session keeps reading the
// generation it began on; only its commit conflicts.
func TestReadOnlySessionSurvivesConcurrentCommit(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		value, _, err := reader.Input(inputKey)
		return value, err
	}})
	seed := mustBegin(t, graph)
	mustApply(t, seed, exactInput(inputKey, "r1", "base"))
	mustEvaluate(t, seed, queryKey)
	mustCommit(t, seed)

	reader := mustBegin(t, graph)
	writer := mustBegin(t, graph)
	mustApply(t, writer, exactInput(inputKey, "r2", "newer"))
	mustEvaluate(t, writer, queryKey)
	mustCommit(t, writer)

	if _, err := reader.DirtyQueries(); err != nil {
		t.Fatalf("DirtyQueries() after a concurrent commit = %v", err)
	}
	if got := string(mustEvaluate(t, reader, queryKey)); got != "base" {
		t.Fatalf("reader value = %q, want the generation it began on", got)
	}
	if err := reader.Commit(context.Background(), acceptRevisions); !errors.Is(err, ErrCommitConflict) {
		t.Fatalf("stale reader Commit() error = %v, want ErrCommitConflict", err)
	}
	if got := stringValue(t, graph, queryKey); got != "newer" {
		t.Fatalf("committed value = %q", got)
	}
}

// A session's cached reads answer from the generation it began on even after
// another session committed: the graph's current value moved, the base did not.
func TestReadOnlySessionBaseValueIgnoresConcurrentCommit(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		value, _, err := reader.Input(inputKey)
		return value, err
	}})
	seed := mustBegin(t, graph)
	mustApply(t, seed, exactInput(inputKey, "r1", "base"))
	mustEvaluate(t, seed, queryKey)
	mustCommit(t, seed)

	reader := mustBegin(t, graph)
	writer := mustBegin(t, graph)
	mustApply(t, writer, exactInput(inputKey, "r2", "newer"))
	mustEvaluate(t, writer, queryKey)
	mustCommit(t, writer)

	value, found := reader.BaseValue(queryKey)
	if !found || string(value) != "base" {
		t.Fatalf("BaseValue() = %q, %v; want the generation the session began on", value, found)
	}
	if current, _ := graph.Value(queryKey); string(current) != "newer" {
		t.Fatalf("graph.Value() = %q, want the committed generation", current)
	}
	if !reader.BaseHasInputDependents(inputKey) {
		t.Fatal("BaseHasInputDependents() = false, want the base's dependency on the input")
	}
	if reader.BaseHasDependents(queryKey) {
		t.Fatal("BaseHasDependents() = true for a query nothing depends on")
	}
}
