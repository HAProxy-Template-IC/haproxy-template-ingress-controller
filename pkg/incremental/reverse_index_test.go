package incremental

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental/internal/orderedset"
)

func TestPersistentReverseIndexRandomizedDifferential(t *testing.T) {
	const width = 32
	sources := newReverseOracleSources(width)
	graph := newReverseOracleGraph(t, width)
	resolver := sources.resolver()

	session := mustBeginWithResolver(t, graph, resolver)
	mustApply(t, session, sources.all()...)
	for index := range width {
		mustEvaluate(t, session, NewQueryKey("consumer/"+strconv.Itoa(index)))
	}
	mustCommitWithVerifier(t, session, sources.verifier())
	assertReverseIndexMatchesOracle(t, graph, sources)

	random := rand.New(rand.NewPCG(0x187, 0xcafe))
	for step := range 750 {
		runReverseOracleStep(t, random, step, width, graph, sources)
		assertReverseIndexMatchesOracle(t, graph, sources)
	}
}

func runReverseOracleStep(
	t *testing.T,
	random *rand.Rand,
	step int,
	width int,
	graph *Graph,
	sources *reverseOracleSources,
) {
	t.Helper()
	switch random.IntN(7) {
	case 0:
		index := random.IntN(width)
		name := "selector/" + strconv.Itoa(index)
		sources.change(name, []byte([]string{"left", "right"}[random.IntN(2)]), true)
		commitReverseOracleChange(t, graph, sources, name)
	case 1:
		index := random.IntN(width)
		name := "link/" + strconv.Itoa(index)
		sources.change(name, []byte(strconv.Itoa(random.IntN(width))), true)
		commitReverseOracleChange(t, graph, sources, name)
	case 2:
		changeReverseOracleValue(t, random, step, graph, sources)
	case 3:
		removeReverseOracleQuery(t, random, width, graph, sources)
	case 4:
		recreateReverseOracleConsumer(t, random, width, graph, sources)
	case 5:
		abortReverseOracleChange(t, random, width, graph, sources)
	case 6:
		conflictReverseOracleChange(t, random, width, graph, sources)
	}
}

func changeReverseOracleValue(
	t *testing.T,
	random *rand.Rand,
	step int,
	graph *Graph,
	sources *reverseOracleSources,
) {
	t.Helper()
	name := []string{"value/left", "value/right"}[random.IntN(2)]
	found := random.IntN(3) != 0
	value := []byte(nil)
	if found {
		value = []byte(fmt.Sprintf("%s/%d", name, step))
	}
	sources.change(name, value, found)
	commitReverseOracleChange(t, graph, sources, name)
}

func removeReverseOracleQuery(
	t *testing.T,
	random *rand.Rand,
	width int,
	graph *Graph,
	sources *reverseOracleSources,
) {
	t.Helper()
	key := NewQueryKey([]string{"leaf/", "consumer/"}[random.IntN(2)] + strconv.Itoa(random.IntN(width)))
	session := mustBeginWithResolver(t, graph, sources.resolver())
	if err := session.RemoveQueries(key); err != nil {
		t.Fatalf("RemoveQueries() error = %v", err)
	}
	mustCommitWithVerifier(t, session, sources.verifier())
}

func recreateReverseOracleConsumer(
	t *testing.T,
	random *rand.Rand,
	width int,
	graph *Graph,
	sources *reverseOracleSources,
) {
	t.Helper()
	key := NewQueryKey("consumer/" + strconv.Itoa(random.IntN(width)))
	session := mustBeginWithResolver(t, graph, sources.resolver())
	mustEvaluate(t, session, key)
	mustCommitWithVerifier(t, session, sources.verifier())
}

func abortReverseOracleChange(
	t *testing.T,
	random *rand.Rand,
	width int,
	graph *Graph,
	sources *reverseOracleSources,
) {
	t.Helper()
	name := "selector/" + strconv.Itoa(random.IntN(width))
	baseline := sources.values[NewInputKey(name)]
	before := snapshotReverseRoots(t, graph)
	sources.change(name, []byte(oppositeBranch(string(baseline.Value))), true)
	session := mustBeginWithResolver(t, graph, sources.resolver())
	mustApply(t, session, sources.values[NewInputKey(name)])
	mustEvaluateDirty(t, session)
	session.Abort()
	assertReverseRootsUnchanged(t, graph, before)
	commitReverseOracleChange(t, graph, sources, name)
}

func conflictReverseOracleChange(
	t *testing.T,
	random *rand.Rand,
	width int,
	graph *Graph,
	sources *reverseOracleSources,
) {
	t.Helper()
	name := "selector/" + strconv.Itoa(random.IntN(width))
	baseline := sources.values[NewInputKey(name)]
	sources.change(name, []byte(oppositeBranch(string(baseline.Value))), true)
	candidate := mustBeginWithResolver(t, graph, sources.resolver())
	mustApply(t, candidate, sources.values[NewInputKey(name)])
	mustEvaluateDirty(t, candidate)
	concurrent := mustBeginWithResolver(t, graph, sources.resolver())
	mustCommitWithVerifier(t, concurrent, sources.verifier())
	if err := candidate.Commit(context.Background(), sources.verifier()); !errors.Is(err, ErrCommitConflict) {
		t.Fatalf("conflicted Commit() error = %v", err)
	}
	commitReverseOracleChange(t, graph, sources, name)
}

func TestReverseIndexRejectsForeignRootSubstitution(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "revision/1", "value/1"))
	mustEvaluate(t, session, queryKey)
	mustCommit(t, session)

	graph.mu.Lock()
	poisoned, _, _ := graph.current.reverse.Insert(
		[]byte(dependencyTreeKey(inputDep(inputKey))),
		orderedset.NewAuthority().Empty(),
	)
	graph.current.reverse = poisoned
	graph.mu.Unlock()
	_, err := graph.Begin()
	if err == nil || !strings.Contains(err.Error(), "invalid provenance") {
		t.Fatalf("Begin() error = %v", err)
	}
	if graph.Generation() != 0 {
		t.Fatalf("generation = %d after rejected reverse root", graph.Generation())
	}
}

func TestReverseIndexRejectsSameAuthorityScopeSubstitution(t *testing.T) {
	leftInput := NewInputKey("left")
	rightInput := NewInputKey("right")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(leftInput)})
	session := mustBegin(t, graph)
	mustApply(t, session,
		exactInput(leftInput, "left/1", "left"),
		exactInput(rightInput, "right/1", "right"),
	)
	mustEvaluate(t, session, queryKey)
	mustCommit(t, session)

	graph.mu.Lock()
	leftRoot, _ := graph.current.reverse.Root().Get([]byte(dependencyTreeKey(inputDep(leftInput))))
	poisoned, _, _ := graph.current.reverse.Insert(
		[]byte(dependencyTreeKey(inputDep(rightInput))),
		leftRoot,
	)
	graph.current.reverse = poisoned
	graph.mu.Unlock()
	_, err := graph.Begin()
	if err == nil || !strings.Contains(err.Error(), "invalid provenance") {
		t.Fatalf("Begin() error = %v", err)
	}
	if graph.Generation() != 0 {
		t.Fatalf("generation = %d after rejected reverse root", graph.Generation())
	}
}

func TestReverseRootsSurviveABABackdateAndCanceledCommit(t *testing.T) {
	selectorKey := NewInputKey("selector")
	leftKey := NewInputKey("left")
	rightKey := NewInputKey("right")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		selector, _, err := reader.Input(selectorKey)
		if err != nil {
			return nil, err
		}
		value, _, err := reader.Input(NewInputKey(string(selector)))
		return value, err
	}})
	session := mustBegin(t, graph)
	mustApply(t, session,
		exactInput(selectorKey, "selector/a1", "left"),
		exactInput(leftKey, "left/1", "left"),
		exactInput(rightKey, "right/1", "right"),
	)
	mustEvaluate(t, session, queryKey)
	mustCommit(t, session)
	before := snapshotReverseRoots(t, graph)
	beforeCounters := graph.Counters(queryKey)

	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(selectorKey, "selector/b", "right"))
	mustApply(t, session, exactInput(selectorKey, "selector/a2", "left"))
	mustEvaluate(t, session, queryKey)
	mustCommit(t, session)
	assertReverseRootsUnchanged(t, graph, before)
	afterCounters := graph.Counters(queryKey)
	if afterCounters.Executions != beforeCounters.Executions || afterCounters.Backdates != beforeCounters.Backdates+1 {
		t.Fatalf("A-B-A counters = %+v, want one backdate after %+v", afterCounters, beforeCounters)
	}

	before = snapshotReverseRoots(t, graph)
	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(selectorKey, "selector/b2", "right"))
	mustEvaluate(t, session, queryKey)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := session.Commit(canceled, acceptRevisions); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Commit() error = %v", err)
	}
	assertReverseRootsUnchanged(t, graph, before)
}

func TestReplacementSessionsCoverGenerationZeroAndExplicitColdReset(t *testing.T) {
	graph := mustGraph(t)
	initial := mustBegin(t, graph)
	if initial.cold || !initial.replacement {
		t.Fatalf("initial session cold = %t, replacement = %t", initial.cold, initial.replacement)
	}
	mustCommit(t, initial)

	warm := mustBegin(t, graph)
	if warm.cold || warm.replacement {
		t.Fatalf("warm session cold = %t, replacement = %t", warm.cold, warm.replacement)
	}
	warm.Abort()

	cold, err := graph.BeginColdReset()
	if err != nil {
		t.Fatalf("BeginColdReset() error = %v", err)
	}
	if !cold.cold || !cold.replacement {
		t.Fatalf("cold session cold = %t, replacement = %t", cold.cold, cold.replacement)
	}
	cold.Abort()
}

func TestReplacementReverseViewInvalidatesAfterStageAndRemoval(t *testing.T) {
	leftInput := NewInputKey("left")
	rightInput := NewInputKey("right")
	leftQuery := NewQueryKey("query/left")
	rightQuery := NewQueryKey("query/right")
	graph := mustGraph(t,
		Definition{Key: leftQuery, Run: readInputQuery(leftInput)},
		Definition{Key: rightQuery, Run: readInputQuery(rightInput)},
	)
	session := mustBegin(t, graph)
	mustApply(t, session,
		exactInput(leftInput, "left/1", "left"),
		exactInput(rightInput, "right/1", "right"),
	)
	mustEvaluate(t, session, leftQuery)
	if session.replacementReverseReady {
		t.Fatal("replacement reverse view built eagerly")
	}
	assertSessionInputDependents(t, session, leftInput, true)
	retained := session.replacementReverse[inputDep(leftInput)]
	if !session.replacementReverseReady {
		t.Fatal("replacement reverse view was not retained")
	}

	mustEvaluate(t, session, rightQuery)
	if session.replacementReverseReady {
		t.Fatal("staging a query did not invalidate the replacement reverse view")
	}
	assertSessionInputDependents(t, session, leftInput, true)
	assertSessionInputDependents(t, session, rightInput, true)
	if err := session.RemoveQueriesWhileIdle(leftQuery); err != nil {
		t.Fatalf("RemoveQueriesWhileIdle() error = %v", err)
	}
	if !session.replacementReverseReady {
		t.Fatal("query-removal cascade did not rebuild the invalidated replacement reverse view")
	}
	assertSessionInputDependents(t, session, leftInput, false)
	assertSessionInputDependents(t, session, rightInput, true)

	values, err := retained.Values(graph.reverseAuthority, reverseScope(inputDep(leftInput)))
	if err != nil || !slices.Equal(values, []string{leftQuery.value}) {
		t.Fatalf("retained replacement root values = %v, error %v", values, err)
	}
	mustCommit(t, session)
	if graph.HasInputDependents(leftInput) || !graph.HasInputDependents(rightInput) {
		t.Fatal("committed reverse dependencies do not match the final replacement view")
	}
}

func TestReplacementCommitPublishesBuiltRootsDirectly(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "revision/1", "value"))
	mustEvaluate(t, session, queryKey)
	built, err := session.replacementReverseRoot(inputDep(inputKey))
	if err != nil {
		t.Fatalf("replacementReverseRoot() error = %v", err)
	}
	mustCommit(t, session)

	graph.mu.RLock()
	committed, _ := graph.current.reverse.Root().Get([]byte(dependencyTreeKey(inputDep(inputKey))))
	graph.mu.RUnlock()
	same, err := built.SameRoot(graph.reverseAuthority, reverseScope(inputDep(inputKey)), committed)
	if err != nil || !same {
		t.Fatalf("committed replacement root identity = %t, error %v", same, err)
	}
}

func TestReplacementCommitRejectsDuplicateEdgeWithoutPublication(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "revision/1", "value"))
	mustEvaluate(t, session, queryKey)
	entry := session.nodeChanges[queryKey]
	entry.deps = append(entry.deps, entry.deps[0])
	session.nodeChanges[queryKey] = entry

	err := session.Commit(context.Background(), acceptRevisions)
	if err == nil || !strings.Contains(err.Error(), "duplicate dependency") {
		t.Fatalf("Commit() error = %v", err)
	}
	if graph.Generation() != 0 || graph.HasInputDependents(inputKey) {
		t.Fatal("rejected replacement commit changed the graph")
	}
}

func TestReplacementCommitRejectsForeignCachedRootWithoutPublication(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "revision/1", "value"))
	mustEvaluate(t, session, queryKey)
	if _, err := session.replacementReverseRoots(); err != nil {
		t.Fatalf("replacementReverseRoots() error = %v", err)
	}
	session.replacementReverse[inputDep(inputKey)] = orderedset.NewAuthority().Empty()

	err := session.Commit(context.Background(), acceptRevisions)
	if err == nil || !strings.Contains(err.Error(), "invalid provenance") {
		t.Fatalf("Commit() error = %v", err)
	}
	if graph.Generation() != 0 || graph.HasInputDependents(inputKey) {
		t.Fatal("poisoned replacement root changed the graph")
	}
}

func TestReplacementCommitRejectsRemovedQueryWithRetainedDependent(t *testing.T) {
	inputKey := NewInputKey("input")
	parentKey := NewQueryKey("parent")
	childKey := NewQueryKey("child")
	graph := mustGraph(t,
		Definition{Key: parentKey, Run: readInputQuery(inputKey)},
		Definition{Key: childKey, Run: func(ctx context.Context, reader Reader) ([]byte, error) {
			return reader.Query(ctx, parentKey)
		}},
	)
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(inputKey, "revision/1", "value"))
	mustEvaluate(t, session, childKey)
	session.removedQueries[parentKey] = struct{}{}

	err := session.Commit(context.Background(), acceptRevisions)
	if err == nil || !strings.Contains(err.Error(), "retains dependents") {
		t.Fatalf("Commit() error = %v", err)
	}
	if graph.Generation() != 0 || graph.HasDependents(parentKey) {
		t.Fatal("invalid replacement query removal changed the graph")
	}
}

func TestConcurrentGenerationZeroReplacementCommitsHaveOneWinner(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	sessions := make([]*Session, 2)
	for index := range sessions {
		sessions[index] = mustBegin(t, graph)
		mustApply(t, sessions[index], exactInput(inputKey, "revision/1", "value"))
		mustEvaluate(t, sessions[index], queryKey)
	}
	start := make(chan struct{})
	results := make(chan error, len(sessions))
	for _, session := range sessions {
		go func() {
			<-start
			results <- session.Commit(context.Background(), acceptRevisions)
		}()
	}
	close(start)
	errorsSeen := []error{<-results, <-results}
	successes := 0
	conflicts := 0
	for _, err := range errorsSeen {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrCommitConflict):
			conflicts++
		default:
			t.Fatalf("Commit() error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 || graph.Generation() != 1 {
		t.Fatalf("successes = %d, conflicts = %d, generation = %d", successes, conflicts, graph.Generation())
	}
}

func assertSessionInputDependents(t *testing.T, session *Session, key InputKey, want bool) {
	t.Helper()
	got, err := session.HasInputDependents(key)
	if err != nil || got != want {
		t.Fatalf("HasInputDependents(%q) = %t, error %v, want %t", key.value, got, err, want)
	}
}

type reverseOracleSources struct {
	values  map[InputKey]Input
	version uint64
}

func newReverseOracleSources(width int) *reverseOracleSources {
	sources := &reverseOracleSources{values: map[InputKey]Input{}}
	for index := range width {
		sources.change("selector/"+strconv.Itoa(index), []byte([]string{"left", "right"}[index%2]), true)
		sources.change("link/"+strconv.Itoa(index), []byte(strconv.Itoa((index+1)%width)), true)
	}
	sources.change("value/left", []byte("left/initial"), true)
	sources.change("value/right", nil, false)
	return sources
}

func (s *reverseOracleSources) change(name string, value []byte, found bool) {
	s.version++
	key := NewInputKey(name)
	s.values[key] = Input{
		Key:      key,
		Revision: NewRevision(fmt.Sprintf("revision/%d", s.version)),
		Found:    found,
		Value:    cloneBytes(value),
	}
}

func (s *reverseOracleSources) all() []Input {
	inputs := make([]Input, 0, len(s.values))
	for _, input := range s.values {
		input.Value = cloneBytes(input.Value)
		inputs = append(inputs, input)
	}
	slices.SortFunc(inputs, func(left, right Input) int {
		return strings.Compare(left.Key.value, right.Key.value)
	})
	return inputs
}

func (s *reverseOracleSources) resolver() InputResolver {
	return func(_ context.Context, key InputKey) (Input, error) {
		input, exists := s.values[key]
		if !exists {
			return Input{}, fmt.Errorf("unknown source %q", key.value)
		}
		input.Value = cloneBytes(input.Value)
		return input, nil
	}
}

func (s *reverseOracleSources) verifier() RevisionVerifier {
	return func(_ context.Context, observations []InputRevision) (bool, error) {
		for _, observation := range observations {
			input, exists := s.values[observation.Key]
			if !exists || input.Revision != observation.Revision || input.Found != observation.Found {
				return false, nil
			}
		}
		return true, nil
	}
}

func newReverseOracleGraph(t *testing.T, width int) *Graph {
	t.Helper()
	definitions := make([]Definition, 0, width*2)
	for index := range width {
		leaf := NewQueryKey("leaf/" + strconv.Itoa(index))
		definitions = append(definitions, Definition{Key: leaf, Run: func(_ context.Context, reader Reader) ([]byte, error) {
			selector, _, err := reader.Input(NewInputKey("selector/" + strconv.Itoa(index)))
			if err != nil {
				return nil, err
			}
			value, found, err := reader.Input(NewInputKey("value/" + string(selector)))
			if err != nil {
				return nil, err
			}
			if !found {
				return []byte("missing"), nil
			}
			return value, nil
		}})
		consumer := NewQueryKey("consumer/" + strconv.Itoa(index))
		definitions = append(definitions, Definition{Key: consumer, Run: func(ctx context.Context, reader Reader) ([]byte, error) {
			link, _, err := reader.Input(NewInputKey("link/" + strconv.Itoa(index)))
			if err != nil {
				return nil, err
			}
			return reader.Query(ctx, NewQueryKey("leaf/"+string(link)))
		}})
	}
	graph, err := NewWithProviderOptions(nil, Options{RetireUnreferencedInputs: true}, definitions...)
	if err != nil {
		t.Fatalf("NewWithProviderOptions() error = %v", err)
	}
	return graph
}

func commitReverseOracleChange(t *testing.T, graph *Graph, sources *reverseOracleSources, name string) {
	t.Helper()
	session := mustBeginWithResolver(t, graph, sources.resolver())
	mustApply(t, session, sources.values[NewInputKey(name)])
	mustEvaluateDirty(t, session)
	mustCommitWithVerifier(t, session, sources.verifier())
}

func mustEvaluateDirty(t *testing.T, session *Session) {
	t.Helper()
	dirty, err := session.DirtyQueries()
	if err != nil {
		t.Fatalf("DirtyQueries() error = %v", err)
	}
	if _, err := session.EvaluateAll(context.Background(), dirty...); err != nil {
		t.Fatalf("EvaluateAll() error = %v", err)
	}
}

func mustCommitWithVerifier(t *testing.T, session *Session, verifier RevisionVerifier) {
	t.Helper()
	if err := session.Commit(context.Background(), verifier); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
}

func assertReverseIndexMatchesOracle(t *testing.T, graph *Graph, sources *reverseOracleSources) {
	t.Helper()
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	expected := map[dependencyKey][]string{}
	graph.current.nodes.Root().Walk(func(rawKey string, _ committedNodeEntry) bool {
		key := NewQueryKey(rawKey)
		kind, raw, found := strings.Cut(key.value, "/")
		if !found {
			t.Fatalf("unexpected query key %q", key.value)
		}
		index, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("query key %q: %v", key.value, err)
		}
		switch kind {
		case "leaf":
			selector := NewInputKey("selector/" + strconv.Itoa(index))
			expected[inputDep(selector)] = append(expected[inputDep(selector)], key.value)
			value := NewInputKey("value/" + string(sources.values[selector].Value))
			expected[inputDep(value)] = append(expected[inputDep(value)], key.value)
		case "consumer":
			link := NewInputKey("link/" + strconv.Itoa(index))
			expected[inputDep(link)] = append(expected[inputDep(link)], key.value)
			leaf := NewQueryKey("leaf/" + string(sources.values[link].Value))
			expected[queryDep(leaf)] = append(expected[queryDep(leaf)], key.value)
		default:
			t.Fatalf("unexpected query kind %q", kind)
		}
		return false
	})
	if graph.current.reverse.Len() != len(expected) {
		t.Fatalf("reverse dependency keys = %d, want %d", graph.current.reverse.Len(), len(expected))
	}
	for dependency, want := range expected {
		slices.Sort(want)
		root, exists := graph.current.reverse.Root().Get([]byte(dependencyTreeKey(dependency)))
		if !exists {
			t.Fatalf("missing reverse dependency %#v", dependency)
		}
		got, err := root.Values(graph.reverseAuthority, reverseScope(dependency))
		if err != nil {
			t.Fatalf("reverse dependency %#v: %v", dependency, err)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("reverse dependency %#v = %#v, want %#v", dependency, got, want)
		}
	}
}

func snapshotReverseRoots(t *testing.T, graph *Graph) map[dependencyKey]orderedset.Root {
	t.Helper()
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	roots := make(map[dependencyKey]orderedset.Root, graph.current.reverse.Len())
	graph.current.reverse.Root().Walk(func(rawKey string, root orderedset.Root) bool {
		dependency, valid := parseDependencyTreeKey(rawKey)
		if !valid {
			t.Fatalf("reverse dependency key %q is invalid", rawKey)
		}
		if err := root.ValidateOwnership(graph.reverseAuthority, reverseScope(dependency)); err != nil {
			t.Fatalf("reverse dependency %#v: %v", dependency, err)
		}
		roots[dependency] = root
		return false
	})
	return roots
}

func assertReverseRootsUnchanged(t *testing.T, graph *Graph, want map[dependencyKey]orderedset.Root) {
	t.Helper()
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	if graph.current.reverse.Len() != len(want) {
		t.Fatalf("reverse dependency keys = %d, want %d", graph.current.reverse.Len(), len(want))
	}
	for dependency, expected := range want {
		actual, exists := graph.current.reverse.Root().Get([]byte(dependencyTreeKey(dependency)))
		if !exists {
			t.Fatalf("missing reverse dependency %#v", dependency)
		}
		same, err := actual.SameRoot(graph.reverseAuthority, reverseScope(dependency), expected)
		if err != nil || !same {
			t.Fatalf("reverse dependency %#v root changed, same=%t error=%v", dependency, same, err)
		}
	}
}

func oppositeBranch(value string) string {
	if value == "left" {
		return "right"
	}
	return "left"
}
