package incremental

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
)

func TestColdGenerationPublishesOneAuthenticatedRoot(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})

	initial := mustBegin(t, graph)
	mustApply(t, initial, exactInput(inputKey, "r1", "initial"))
	mustEvaluate(t, initial, queryKey)
	mustCommit(t, initial)
	previousGeneration := graph.current
	previousRoot := mustCommittedExactValue(t, graph, queryKey)
	replacement, replacementRoot := evaluatedColdReplacement(t, graph, inputKey, queryKey)
	prepare, publicationErr := generationPublicationProbe(graph, previousGeneration, queryKey, replacementRoot)
	err := replacement.CommitWithPreparedPublisher(t.Context(), acceptRevisions,
		prepare)
	if err != nil {
		t.Fatalf("CommitWithPreparedPublisher() error = %v", err)
	}
	if err := publicationErr(); err != nil {
		t.Fatal(err)
	}
	if err := graph.ValidateCommittedExactValue(queryKey, replacementRoot); err != nil {
		t.Fatalf("replacement root validation error = %v", err)
	}
	if err := graph.ValidateExactValue(queryKey, previousRoot); err != nil {
		t.Fatalf("historical root ownership error = %v", err)
	}
	if err := graph.ValidateCommittedExactValue(queryKey, previousRoot); err == nil {
		t.Fatal("historical root remained the committed root")
	}
}

func mustCommittedExactValue(t *testing.T, graph *Graph, key QueryKey) ExactValueRoot {
	t.Helper()
	value, exists, err := graph.ExactValue(key)
	if err != nil || !exists {
		t.Fatalf("ExactValue() = %#v, %v, %v", value, exists, err)
	}
	return value
}

func evaluatedColdReplacement(
	t *testing.T,
	graph *Graph,
	inputKey InputKey,
	queryKey QueryKey,
) (*Session, ExactValueRoot) {
	t.Helper()
	replacement := mustColdSession(t, graph, exactInput(inputKey, "r2", "replacement"))
	results, err := replacement.EvaluateAllColdExactBatch(t.Context(), func(
		_ context.Context,
		batch ColdExactBatch,
	) error {
		value, found, readErr := batch.Query(0).Input(inputKey)
		if readErr != nil || !found {
			return fmt.Errorf("reading replacement input: found=%v: %w", found, readErr)
		}
		_, completeErr := batch.Query(0).Complete(string(value))
		return completeErr
	}, queryKey)
	if err != nil {
		t.Fatalf("EvaluateAllColdExactBatch() error = %v", err)
	}
	return replacement, results[0].Value
}

func generationPublicationProbe(
	graph *Graph,
	previous *graphGeneration,
	queryKey QueryKey,
	replacementRoot ExactValueRoot,
) (prepare CommitPublicationPreparer, publicationError func() error) {
	var publicationErr error
	prepare = func([]InputKey) (CommitPublication, error) {
		return CommitPublication{
			Publish: func() {
				if graph.current != previous {
					publicationErr = errors.New("graph generation changed before graph publication")
				}
			},
			Complete: func() {
				if graph.current == previous || !graph.current.valid(graph) {
					publicationErr = errors.New("graph generation was not atomically replaced")
					return
				}
				entry, found := graph.current.nodes.Root().Get([]byte(queryKey.value))
				if !found || entry.value.value != replacementRoot.value {
					publicationErr = errors.New("published generation contains a substituted query root")
				}
			},
		}, nil
	}
	publicationError = func() error { return publicationErr }
	return prepare, publicationError
}

func TestWarmCommitPublishesGenerationAfterPlanPreparation(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	initial := mustBegin(t, graph)
	mustApply(t, initial, exactInput(inputKey, "r1", "initial"))
	mustEvaluate(t, initial, queryKey)
	mustCommit(t, initial)
	previous := graph.current

	warm := mustBegin(t, graph)
	mustApply(t, warm, exactInput(inputKey, "r2", "warm"))
	mustEvaluate(t, warm, queryKey)
	mustCommit(t, warm)
	if graph.current == previous || graph.current.number != previous.number+1 {
		t.Fatalf("warm generation = %#v after %#v", graph.current, previous)
	}
	if got := stringValue(t, graph, queryKey); got != "warm" {
		t.Fatalf("warm value = %q", got)
	}
}

func TestGraphGenerationCorruptionFailsClosed(t *testing.T) {
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: staticQuery("value")})
	session := mustBegin(t, graph)
	mustEvaluate(t, session, queryKey)
	mustCommit(t, session)

	current := graph.current
	graph.current = nil
	t.Cleanup(func() { graph.current = current })
	if _, err := graph.Begin(); err == nil {
		t.Fatal("Begin() accepted a missing generation")
	}
	if _, exists := graph.Value(queryKey); exists {
		t.Fatal("Value() returned data from a missing generation")
	}
	if _, exists, err := graph.ExactValue(queryKey); err == nil || exists {
		t.Fatalf("ExactValue() = exists %v, error %v", exists, err)
	}
	if graph.Generation() != 0 || graph.Counters(queryKey) != (NodeCounters{}) || graph.HasDependents(queryKey) {
		t.Fatal("corrupt generation exposed committed metadata")
	}
}

func TestConcurrentColdGenerationReadersNeverObservePartialPublication(t *testing.T) {
	inputKey := NewInputKey("input")
	queryKey := NewQueryKey("query")
	graph := mustGraph(t, Definition{Key: queryKey, Run: readInputQuery(inputKey)})
	initial := mustBegin(t, graph)
	mustApply(t, initial, exactInput(inputKey, "r0", "value-0"))
	mustEvaluate(t, initial, queryKey)
	mustCommit(t, initial)

	var stop atomic.Bool
	var readerFailure atomic.Pointer[string]
	var readers sync.WaitGroup
	for range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !stop.Load() {
				graph.mu.RLock()
				generation := graph.current
				if !generation.valid(graph) {
					failure := "reader observed an unauthenticated generation"
					readerFailure.CompareAndSwap(nil, &failure)
					graph.mu.RUnlock()
					return
				}
				entry, exists := generation.nodes.Root().Get([]byte(queryKey.value))
				if !exists || entry.dirty || entry.changedAt > generation.number ||
					entry.value.validateOwned(graph.valueAuthority, queryKey) != nil {
					failure := "reader observed a partial generation"
					readerFailure.CompareAndSwap(nil, &failure)
					graph.mu.RUnlock()
					return
				}
				graph.mu.RUnlock()
			}
		}()
	}
	for index := 1; index <= 40; index++ {
		replacement := mustColdSession(
			t,
			graph,
			exactInput(inputKey, fmt.Sprintf("r%d", index), fmt.Sprintf("value-%d", index)),
		)
		mustEvaluate(t, replacement, queryKey)
		mustCommit(t, replacement)
	}
	stop.Store(true)
	readers.Wait()
	if failure := readerFailure.Load(); failure != nil {
		t.Fatal(*failure)
	}
}

func TestRandomizedColdGenerationMatchesFullOracle(t *testing.T) {
	const queryCount = 24
	oracle := newRandomizedColdOracle(t, queryCount)
	random := rand.New(rand.NewPCG(187, 2026))
	for generation := 1; generation <= 60; generation++ {
		inputs := oracle.nextInputs(random, generation)
		session := mustColdSession(t, oracle.graph, inputs...)
		oracle.evaluate(t, session)
		poisonInputBytes(inputs)
		previous := oracle.graph.current
		accept := generation%11 != 0
		err := oracle.commit(t, session, accept)
		if !accept {
			requireRejectedGeneration(t, generation, err, oracle.graph.current, previous)
			continue
		}
		if err != nil {
			t.Fatalf("generation %d commit error = %v", generation, err)
		}
		oracle.requireValues(t, generation)
	}
}

type randomizedColdOracle struct {
	graph     *Graph
	inputKeys []InputKey
	queryKeys []QueryKey
	want      []string
}

func newRandomizedColdOracle(t *testing.T, queryCount int) *randomizedColdOracle {
	t.Helper()
	inputKeys := make([]InputKey, queryCount)
	queryKeys := make([]QueryKey, queryCount)
	definitions := make([]Definition, queryCount)
	for index := range queryCount {
		inputKeys[index] = NewInputKey(fmt.Sprintf("input-%02d", index))
		queryKeys[index] = NewQueryKey(fmt.Sprintf("query-%02d", index))
		definitions[index] = Definition{Key: queryKeys[index], Run: staticQuery("")}
	}
	return &randomizedColdOracle{
		graph:     mustGraph(t, definitions...),
		inputKeys: inputKeys,
		queryKeys: queryKeys,
		want:      make([]string, queryCount),
	}
}

func (o *randomizedColdOracle) nextInputs(random *rand.Rand, generation int) []Input {
	inputs := make([]Input, len(o.inputKeys))
	for index := range o.inputKeys {
		found := random.IntN(5) != 0
		value := ""
		if found {
			value = fmt.Sprintf("generation-%02d-value-%08x", generation, random.Uint32())
		}
		inputs[index] = Input{
			Key:      o.inputKeys[index],
			Revision: NewRevision(fmt.Sprintf("generation-%02d-revision-%02d", generation, index)),
			Found:    found,
			Value:    []byte(value),
		}
		o.want[index] = fmt.Sprintf("%t:%s", found, value)
	}
	random.Shuffle(len(inputs), func(left, right int) {
		inputs[left], inputs[right] = inputs[right], inputs[left]
	})
	return inputs
}

func (o *randomizedColdOracle) evaluate(t *testing.T, session *Session) {
	t.Helper()
	_, err := session.EvaluateAllColdExactBatch(t.Context(), func(
		_ context.Context,
		batch ColdExactBatch,
	) error {
		for index := range batch.Len() {
			input, readErr := batch.Query(index).ExactInput(o.inputKeys[index])
			if readErr != nil {
				return readErr
			}
			if _, completeErr := batch.Query(index).Complete(
				fmt.Sprintf("%t:%s", input.Found, input.Value),
			); completeErr != nil {
				return completeErr
			}
		}
		return nil
	}, o.queryKeys...)
	if err != nil {
		t.Fatalf("EvaluateAllColdExactBatch() error = %v", err)
	}
}

func poisonInputBytes(inputs []Input) {
	for index := range inputs {
		if len(inputs[index].Value) != 0 {
			inputs[index].Value[0] ^= 0xff
		}
	}
}

func (o *randomizedColdOracle) commit(t *testing.T, session *Session, accept bool) error {
	t.Helper()
	return session.Commit(t.Context(), func(_ context.Context, observations []InputRevision) (bool, error) {
		if len(observations) != len(o.queryKeys) {
			return false, fmt.Errorf("observations = %d, want %d", len(observations), len(o.queryKeys))
		}
		return accept, nil
	})
}

func requireRejectedGeneration(
	t *testing.T,
	generation int,
	err error,
	current *graphGeneration,
	previous *graphGeneration,
) {
	t.Helper()
	if !errors.Is(err, ErrRevisionConflict) || current != previous {
		t.Fatalf("generation %d rejected publication = %v, current changed %v", generation, err, current != previous)
	}
}

func (o *randomizedColdOracle) requireValues(t *testing.T, generation int) {
	t.Helper()
	for index, key := range o.queryKeys {
		if got := stringValue(t, o.graph, key); got != o.want[index] {
			t.Fatalf("generation %d query %d = %q, want %q", generation, index, got, o.want[index])
		}
	}
}

func staticQuery(value string) QueryFunc {
	return func(context.Context, Reader) ([]byte, error) {
		return []byte(value), nil
	}
}
