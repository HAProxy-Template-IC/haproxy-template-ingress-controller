package incremental

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

func acceptRevisions(context.Context, []InputRevision) (bool, error) {
	return true, nil
}

func mustGraph(t *testing.T, definitions ...Definition) *Graph {
	t.Helper()
	graph, err := New(definitions...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return graph
}

func mustBegin(t *testing.T, graph *Graph) *Session {
	t.Helper()
	session, err := graph.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	return session
}

func mustApply(t *testing.T, session *Session, inputs ...Input) {
	t.Helper()
	if err := session.ApplyInputs(inputs...); err != nil {
		t.Fatalf("ApplyInputs() error = %v", err)
	}
}

func mustEvaluate(t *testing.T, session *Session, key QueryKey) []byte {
	t.Helper()
	value, err := session.Evaluate(context.Background(), key)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	return value
}

func mustCommit(t *testing.T, session *Session) {
	t.Helper()
	if err := session.Commit(context.Background(), acceptRevisions); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
}

func exactInput(key InputKey, revision, value string) Input {
	return Input{Key: key, Revision: NewRevision(revision), Found: true, Value: []byte(value)}
}

func TestGraphRedGreenBackdating(t *testing.T) {
	inputKey := NewInputKey("source/item")
	childKey := NewQueryKey("child")
	rootKey := NewQueryKey("root")
	graph := mustGraph(t,
		Definition{Key: childKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
			value, _, err := reader.Input(inputKey)
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(string(value), "same-") {
				return []byte("stable"), nil
			}
			return value, nil
		}},
		Definition{Key: rootKey, Run: func(ctx context.Context, reader Reader) ([]byte, error) {
			value, err := reader.Query(ctx, childKey)
			return append([]byte("root:"), value...), err
		}},
	)

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r1", "same-a"))
	if got := string(mustEvaluate(t, session, rootKey)); got != "root:stable" {
		t.Fatalf("initial value = %q", got)
	}
	mustCommit(t, session)

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r2", "same-b"))
	var verified []InputRevision
	if got := string(mustEvaluate(t, session, rootKey)); got != "root:stable" {
		t.Fatalf("backdated value = %q", got)
	}
	err := session.Commit(context.Background(), func(_ context.Context, inputs []InputRevision) (bool, error) {
		verified = append([]InputRevision(nil), inputs...)
		return true, nil
	})
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if len(verified) != 1 || verified[0].Revision != NewRevision("r2") {
		t.Fatalf("verified revisions = %#v", verified)
	}
	if counters := graph.Counters(childKey); counters.Executions != 2 || counters.Backdates != 1 {
		t.Fatalf("child counters = %+v", counters)
	}
	if counters := graph.Counters(rootKey); counters.Executions != 1 || counters.Backdates != 1 {
		t.Fatalf("root counters = %+v", counters)
	}

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r3", "changed"))
	if got := string(mustEvaluate(t, session, rootKey)); got != "root:changed" {
		t.Fatalf("changed value = %q", got)
	}
	mustCommit(t, session)
	if counters := graph.Counters(childKey); counters.Executions != 3 || counters.Changes != 2 {
		t.Fatalf("changed child counters = %+v", counters)
	}
	if counters := graph.Counters(rootKey); counters.Executions != 2 || counters.Changes != 2 {
		t.Fatalf("changed root counters = %+v", counters)
	}
}

func TestGraphBackdatesRevisionOnlyInputChangeWithoutExecution(t *testing.T) {
	inputKey := NewInputKey("source/item")
	queryKey := NewQueryKey("component")
	var executions atomic.Uint64
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		executions.Add(1)
		value, _, err := reader.Input(inputKey)
		return value, err
	}})

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r1", "stable"))
	mustEvaluate(t, session, queryKey)
	mustCommit(t, session)

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r2", "stable"))
	mustEvaluate(t, session, queryKey)
	var verified []InputRevision
	err := session.Commit(context.Background(), func(_ context.Context, inputs []InputRevision) (bool, error) {
		verified = append([]InputRevision(nil), inputs...)
		return true, nil
	})
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if executions.Load() != 1 {
		t.Fatalf("revision-only input change executed query %d times", executions.Load())
	}
	if len(verified) != 1 || verified[0].Revision != NewRevision("r2") {
		t.Fatalf("verified revisions = %#v", verified)
	}
}

func TestDynamicDependenciesReplaceReverseEdges(t *testing.T) {
	selectorKey := NewInputKey("selector")
	leftKey := NewInputKey("left")
	rightKey := NewInputKey("right")
	branchKey := NewQueryKey("branch")
	rootKey := NewQueryKey("root")
	graph := mustGraph(t,
		Definition{Key: branchKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
			selector, _, err := reader.Input(selectorKey)
			if err != nil {
				return nil, err
			}
			key := leftKey
			if string(selector) == "right" {
				key = rightKey
			}
			value, _, err := reader.Input(key)
			return value, err
		}},
		Definition{Key: rootKey, Run: func(ctx context.Context, reader Reader) ([]byte, error) {
			return reader.Query(ctx, branchKey)
		}},
	)

	session := mustBegin(t, graph)
	mustApply(t, session,
		exactInput(selectorKey, "s1", "left"),
		exactInput(leftKey, "l1", "same"),
		exactInput(rightKey, "r1", "same"),
	)
	mustEvaluate(t, session, rootKey)
	mustCommit(t, session)

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(selectorKey, "s2", "right"))
	mustEvaluate(t, session, rootKey)
	mustCommit(t, session)
	if counters := graph.Counters(rootKey); counters.Executions != 1 {
		t.Fatalf("root reran after unchanged branch output: %+v", counters)
	}
	branchBefore := graph.Counters(branchKey)
	rootBefore := graph.Counters(rootKey)

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(leftKey, "l2", "unused"))
	mustEvaluate(t, session, rootKey)
	mustCommit(t, session)
	branchAfter := graph.Counters(branchKey)
	rootAfter := graph.Counters(rootKey)
	if branchAfter.Executions != branchBefore.Executions || branchAfter.Invalidations != branchBefore.Invalidations {
		t.Fatalf("old branch still invalidated query: before=%+v after=%+v", branchBefore, branchAfter)
	}
	if rootAfter.Executions != rootBefore.Executions || rootAfter.Invalidations != rootBefore.Invalidations {
		t.Fatalf("old branch still invalidated consumer: before=%+v after=%+v", rootBefore, rootAfter)
	}

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(rightKey, "r2", "new"))
	if got := string(mustEvaluate(t, session, rootKey)); got != "new" {
		t.Fatalf("new branch value = %q", got)
	}
	mustCommit(t, session)
	if counters := graph.Counters(rootKey); counters.Executions != 2 {
		t.Fatalf("consumer missed changed branch: %+v", counters)
	}
}

func TestNegativeInputDeletionAndABA(t *testing.T) {
	inputKey := NewInputKey("object")
	queryKey := NewQueryKey("object-text")
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		value, found, err := reader.Input(inputKey)
		if err != nil {
			return nil, err
		}
		if !found {
			return []byte("missing"), nil
		}
		return value, nil
	}})

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "present", "A"))
	mustEvaluate(t, session, queryKey)
	mustCommit(t, session)

	session = mustBegin(t, graph)
	mustApply(t, session, Input{Key: inputKey, Revision: NewRevision("deleted")})
	if got := string(mustEvaluate(t, session, queryKey)); got != "missing" {
		t.Fatalf("deleted value = %q", got)
	}
	var negative InputRevision
	err := session.Commit(context.Background(), func(_ context.Context, inputs []InputRevision) (bool, error) {
		negative = inputs[0]
		return true, nil
	})
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if negative.Found || negative.Revision != NewRevision("deleted") {
		t.Fatalf("negative revision = %#v", negative)
	}

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "present", "A"))
	if got := string(mustEvaluate(t, session, queryKey)); got != "A" {
		t.Fatalf("ABA value = %q", got)
	}
	mustCommit(t, session)
	if counters := graph.Counters(queryKey); counters.Executions != 3 {
		t.Fatalf("ABA did not rerun query: %+v", counters)
	}

	session = mustBegin(t, graph)
	err = session.ApplyInputs(exactInput(inputKey, "present", "different"))
	if err == nil {
		t.Fatal("ApplyInputs() accepted different bytes for one exact revision")
	}
	if graph.Generation() != 3 {
		t.Fatalf("failed exact revision changed generation to %d", graph.Generation())
	}
}

func TestReaderExactInputPreservesIdentityAndReturnsClonedValue(t *testing.T) {
	inputKey := NewInputKey("exact")
	queryKey := NewQueryKey("reader")
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		input, err := reader.ExactInput(inputKey)
		if err != nil {
			return nil, err
		}
		if input.Key != inputKey || input.Revision != NewRevision("r1") || !input.Found {
			return nil, fmt.Errorf("unexpected exact input: %#v", input)
		}
		input.Value[0] = 'X'
		again, err := reader.ExactInput(inputKey)
		if err != nil {
			return nil, err
		}
		return again.Value, nil
	}})

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "r1", "value"))
	if got := string(mustEvaluate(t, session, queryKey)); got != "value" {
		t.Fatalf("value after caller mutation = %q", got)
	}
	mustCommit(t, session)
}

func TestMultiInputBatchInvalidatesOnce(t *testing.T) {
	leftKey := NewInputKey("left")
	rightKey := NewInputKey("right")
	queryKey := NewQueryKey("pair")
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		left, _, err := reader.Input(leftKey)
		if err != nil {
			return nil, err
		}
		right, _, err := reader.Input(rightKey)
		if err != nil {
			return nil, err
		}
		return []byte(string(left) + ":" + string(right)), nil
	}})

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(leftKey, "l1", "a"), exactInput(rightKey, "r1", "b"))
	mustEvaluate(t, session, queryKey)
	mustCommit(t, session)

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(rightKey, "r2", "d"), exactInput(leftKey, "l2", "c"))
	if got := string(mustEvaluate(t, session, queryKey)); got != "c:d" {
		t.Fatalf("batch value = %q", got)
	}
	mustCommit(t, session)
	counters := graph.Counters(queryKey)
	if counters.Executions != 2 || counters.Invalidations != 1 {
		t.Fatalf("batch counters = %+v", counters)
	}
}

func TestDirtyQueriesAreExactAndPersistent(t *testing.T) {
	leftInput := NewInputKey("left-input")
	rightInput := NewInputKey("right-input")
	leftQuery := NewQueryKey("component/left")
	rightQuery := NewQueryKey("component/right")
	rootQuery := NewQueryKey("root")
	graph := mustGraph(t,
		Definition{Key: leftQuery, Run: readInputQuery(leftInput)},
		Definition{Key: rightQuery, Run: readInputQuery(rightInput)},
		Definition{Key: rootQuery, Run: func(ctx context.Context, reader Reader) ([]byte, error) {
			return reader.Query(ctx, leftQuery)
		}},
	)
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(leftInput, "l1", "left"), exactInput(rightInput, "r1", "right"))
	if _, err := session.EvaluateAll(context.Background(), rootQuery, rightQuery); err != nil {
		t.Fatalf("EvaluateAll() error = %v", err)
	}
	mustCommit(t, session)

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(leftInput, "l2", "changed"))
	dirty, err := session.DirtyQueries()
	if err != nil {
		t.Fatalf("DirtyQueries() error = %v", err)
	}
	want := []QueryKey{leftQuery, rootQuery}
	if len(dirty) != len(want) || dirty[0] != want[0] || dirty[1] != want[1] {
		t.Fatalf("dirty queries = %#v, want %#v", dirty, want)
	}
	mustCommit(t, session)

	session = mustBegin(t, graph)
	dirty, err = session.DirtyQueries()
	if err != nil {
		t.Fatalf("persistent DirtyQueries() error = %v", err)
	}
	if len(dirty) != 2 || dirty[0] != leftQuery || dirty[1] != rootQuery {
		t.Fatalf("persistent dirty queries = %#v", dirty)
	}
	if err := session.RemoveQueries(leftQuery); err != nil {
		t.Fatalf("RemoveQueries() error = %v", err)
	}
	dirty, err = session.DirtyQueries()
	if err != nil {
		t.Fatalf("DirtyQueries() after removal error = %v", err)
	}
	if len(dirty) != 0 {
		t.Fatalf("removed dirty queries = %#v", dirty)
	}
	session.Abort()

	cold, err := graph.BeginColdReset(exactInput(leftInput, "cold", "cold"))
	if err != nil {
		t.Fatalf("BeginColdReset() error = %v", err)
	}
	dirty, err = cold.DirtyQueries()
	if err != nil {
		t.Fatalf("cold DirtyQueries() error = %v", err)
	}
	if len(dirty) != 0 {
		t.Fatalf("cold dirty queries = %#v", dirty)
	}
	cold.Abort()
}

func TestDeterministicEvaluationOrder(t *testing.T) {
	alphaKey := NewQueryKey("alpha")
	zuluKey := NewQueryKey("zulu")
	order := []string{}
	graph := mustGraph(t,
		Definition{Key: zuluKey, Run: func(context.Context, Reader) ([]byte, error) {
			order = append(order, "zulu")
			return []byte("z"), nil
		}},
		Definition{Key: alphaKey, Run: func(context.Context, Reader) ([]byte, error) {
			order = append(order, "alpha")
			return []byte("a"), nil
		}},
	)
	session := mustBegin(t, graph)
	results, err := session.EvaluateAll(context.Background(), zuluKey, alphaKey)
	if err != nil {
		t.Fatalf("EvaluateAll() error = %v", err)
	}
	if got := strings.Join(order, ","); got != "alpha,zulu" {
		t.Fatalf("execution order = %q", got)
	}
	if results[0].Key != alphaKey || results[1].Key != zuluKey {
		t.Fatalf("result order = %#v", results)
	}
	mustCommit(t, session)
}

func TestDynamicDefinitionInsertionAndRemoval(t *testing.T) {
	rootKey := NewQueryKey("root")
	componentOne := NewQueryKey("component/one")
	componentTwo := NewQueryKey("component/two")
	provider := func(key QueryKey) (QueryFunc, bool) {
		name, dynamic := strings.CutPrefix(key.Opaque(), "component/")
		if !dynamic {
			return nil, false
		}
		inputKey := NewInputKey("object/" + name)
		return func(_ context.Context, reader Reader) ([]byte, error) {
			value, _, err := reader.Input(inputKey)
			return value, err
		}, true
	}
	graph, err := NewWithProvider(provider, Definition{Key: rootKey, Run: func(ctx context.Context, reader Reader) ([]byte, error) {
		return reader.Query(ctx, componentOne)
	}})
	if err != nil {
		t.Fatalf("NewWithProvider() error = %v", err)
	}

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(NewInputKey("object/one"), "r1", "one"))
	if got := string(mustEvaluate(t, session, rootKey)); got != "one" {
		t.Fatalf("dynamic root = %q", got)
	}
	mustCommit(t, session)

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(NewInputKey("object/two"), "r1", "two"))
	if got := string(mustEvaluate(t, session, componentTwo)); got != "two" {
		t.Fatalf("inserted component = %q", got)
	}
	mustCommit(t, session)

	session = mustBegin(t, graph)
	if err := session.RemoveQueries(componentOne); err != nil {
		t.Fatalf("RemoveQueries() error = %v", err)
	}
	mustCommit(t, session)
	if _, exists := graph.Value(componentOne); exists {
		t.Fatal("removed dynamic component remains cached")
	}
	if _, exists := graph.Value(rootKey); exists {
		t.Fatal("dependent query remains cached")
	}
	graph.mu.RLock()
	_, inputEdgeExists := graph.current.reverse.Root().Get([]byte(dependencyTreeKey(inputDep(NewInputKey("object/one")))))
	_, queryEdgeExists := graph.current.reverse.Root().Get([]byte(dependencyTreeKey(queryDep(componentOne))))
	_, countersExist := graph.current.counters.Root().Get([]byte(componentOne.value))
	graph.mu.RUnlock()
	if inputEdgeExists || queryEdgeExists || countersExist {
		t.Fatal("removed query left committed state")
	}
	if got := stringValue(t, graph, componentTwo); got != "two" {
		t.Fatalf("unrelated component = %q", got)
	}

	unknownSession := mustBegin(t, graph)
	if _, err := unknownSession.Evaluate(context.Background(), NewQueryKey("unknown")); err == nil {
		t.Fatal("unknown dynamic query succeeded")
	}
}

func TestCommitRetainsOnlyInputsWithLiveDependencies(t *testing.T) {
	sharedInput := NewInputKey("shared")
	unusedInput := NewInputKey("unused")
	leftQuery := NewQueryKey("left")
	rightQuery := NewQueryKey("right")
	graph, err := NewWithProviderOptions(nil, Options{RetireUnreferencedInputs: true},
		Definition{Key: leftQuery, Run: func(_ context.Context, reader Reader) ([]byte, error) {
			value, _, err := reader.Input(sharedInput)
			return value, err
		}},
		Definition{Key: rightQuery, Run: func(_ context.Context, reader Reader) ([]byte, error) {
			value, _, err := reader.Input(sharedInput)
			return value, err
		}},
	)
	if err != nil {
		t.Fatalf("NewWithProviderOptions() error = %v", err)
	}

	resolver := func(_ context.Context, key InputKey) (Input, error) {
		return Input{}, fmt.Errorf("unexpected resolution of %q", key.Opaque())
	}
	session, err := graph.BeginWithResolver(resolver)
	if err != nil {
		t.Fatalf("BeginWithResolver() error = %v", err)
	}
	mustApply(t, session,
		exactInput(sharedInput, "shared-r1", "value"),
		exactInput(unusedInput, "unused-r1", "unused"),
	)
	mustEvaluate(t, session, leftQuery)
	mustEvaluate(t, session, rightQuery)
	mustCommit(t, session)
	assertCommittedInputs(t, graph, sharedInput)

	session, err = graph.BeginWithResolver(resolver)
	if err != nil {
		t.Fatalf("BeginWithResolver() error = %v", err)
	}
	if err := session.RemoveQueries(leftQuery); err != nil {
		t.Fatalf("RemoveQueries(left) error = %v", err)
	}
	mustCommit(t, session)
	assertCommittedInputs(t, graph, sharedInput)

	session, err = graph.BeginWithResolver(resolver)
	if err != nil {
		t.Fatalf("BeginWithResolver() error = %v", err)
	}
	if err := session.RemoveQueries(rightQuery); err != nil {
		t.Fatalf("RemoveQueries(right) error = %v", err)
	}
	mustCommit(t, session)
	assertCommittedInputs(t, graph)
}

func TestInputRetirementRequiresResolversForEverySession(t *testing.T) {
	graph, err := NewWithProviderOptions(nil, Options{RetireUnreferencedInputs: true})
	if err != nil {
		t.Fatalf("NewWithProviderOptions() error = %v", err)
	}
	if _, err := graph.Begin(); !errors.Is(err, ErrResolverRequired) {
		t.Fatalf("Begin() error = %v, want ErrResolverRequired", err)
	}
	if _, err := graph.BeginColdReset(); !errors.Is(err, ErrResolverRequired) {
		t.Fatalf("BeginColdReset() error = %v, want ErrResolverRequired", err)
	}
}

func assertCommittedInputs(t *testing.T, graph *Graph, want ...InputKey) {
	t.Helper()
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	if graph.current.inputs.Len() != len(want) {
		t.Fatalf("committed input count = %d, want %d", graph.current.inputs.Len(), len(want))
	}
	for _, key := range want {
		if _, exists := graph.current.inputs.Root().Get([]byte(key.value)); !exists {
			t.Fatalf("committed input %q is missing", key.Opaque())
		}
	}
}

func stringValue(t *testing.T, graph *Graph, key QueryKey) string {
	t.Helper()
	value, exists := graph.Value(key)
	if !exists {
		t.Fatalf("Value(%q) does not exist", key.Opaque())
	}
	return string(value)
}

func TestLazyInputResolverBranchAndConflict(t *testing.T) {
	selectorKey := NewInputKey("selector")
	leftKey := NewInputKey("item/left")
	rightKey := NewInputKey("item/right")
	branchKey := NewQueryKey("branch")
	graph := mustGraph(t, Definition{Key: branchKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		selector, _, err := reader.Input(selectorKey)
		if err != nil {
			return nil, err
		}
		value, found, err := reader.Input(NewInputKey("item/" + string(selector)))
		if err != nil {
			return nil, err
		}
		if !found {
			return []byte("missing"), nil
		}
		return value, nil
	}})

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(selectorKey, "s1", "left"), exactInput(leftKey, "l1", "left-value"))
	mustEvaluate(t, session, branchKey)
	mustCommit(t, session)

	var resolverCalls atomic.Int32
	resolver := func(_ context.Context, key InputKey) (Input, error) {
		resolverCalls.Add(1)
		if key != rightKey {
			return Input{}, fmt.Errorf("unexpected key %q", key.Opaque())
		}
		return Input{Key: key, Revision: NewRevision("right-negative")}, nil
	}
	session, err := graph.BeginWithResolver(resolver)
	if err != nil {
		t.Fatalf("BeginWithResolver() error = %v", err)
	}
	mustApply(t, session, exactInput(selectorKey, "s2", "right"))
	if got := string(mustEvaluate(t, session, branchKey)); got != "missing" {
		t.Fatalf("resolved negative branch = %q", got)
	}
	var verified []InputRevision
	err = session.Commit(context.Background(), func(_ context.Context, inputs []InputRevision) (bool, error) {
		verified = append([]InputRevision(nil), inputs...)
		return true, nil
	})
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if resolverCalls.Load() != 1 {
		t.Fatalf("resolver calls = %d", resolverCalls.Load())
	}
	if len(verified) != 2 || verified[0] != (InputRevision{Key: rightKey, Revision: NewRevision("right-negative")}) {
		t.Fatalf("verified lazy observations = %#v", verified)
	}
	before := graph.Counters(branchKey)

	session, err = graph.BeginWithResolver(func(context.Context, InputKey) (Input, error) {
		return Input{}, errors.New("resolver must not run")
	})
	if err != nil {
		t.Fatalf("BeginWithResolver() error = %v", err)
	}
	mustApply(t, session, exactInput(leftKey, "l2", "unused"))
	mustEvaluate(t, session, branchKey)
	mustCommit(t, session)
	after := graph.Counters(branchKey)
	if after.Executions != before.Executions || after.Invalidations != before.Invalidations {
		t.Fatalf("old lazy branch remained a dependency: before=%+v after=%+v", before, after)
	}
}

func TestLazyInputResolverRevisionConflict(t *testing.T) {
	queryKey := NewQueryKey("lazy-conflict")
	inputKey := NewInputKey("lazy-conflict-input")
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		value, _, err := reader.Input(inputKey)
		return value, err
	}})
	session, err := graph.BeginWithResolver(func(context.Context, InputKey) (Input, error) {
		return exactInput(inputKey, "stale", "value"), nil
	})
	if err != nil {
		t.Fatalf("BeginWithResolver() error = %v", err)
	}
	mustEvaluate(t, session, queryKey)
	err = session.Commit(context.Background(), func(context.Context, []InputRevision) (bool, error) {
		return false, nil
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("Commit() error = %v, want ErrRevisionConflict", err)
	}
	if graph.Generation() != 0 {
		t.Fatalf("resolver conflict committed generation %d", graph.Generation())
	}
}
