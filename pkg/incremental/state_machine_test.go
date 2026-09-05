package incremental

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

type randomizedInputState struct {
	revision Revision
	found    bool
	value    string
}

type randomizedTransactionFixture struct {
	t                    *testing.T
	graph                *Graph
	inputKeys            []InputKey
	queryKeys            []QueryKey
	model                []randomizedInputState
	random               *rand.Rand
	adds                 int
	updates              int
	deletes              int
	aborts               int
	collisions           int
	awayAndBackMutations int
}

func TestGraphRandomizedTransactionsMatchColdOracle(t *testing.T) {
	fixture := newRandomizedTransactionFixture(t, 6)
	fixture.run(120)
	fixture.assertCoverage()
}

func newRandomizedTransactionFixture(t *testing.T, inputCount int) *randomizedTransactionFixture {
	t.Helper()
	fixture := &randomizedTransactionFixture{
		t:         t,
		inputKeys: make([]InputKey, inputCount),
		queryKeys: make([]QueryKey, inputCount),
		model:     make([]randomizedInputState, inputCount),
		random:    rand.New(rand.NewPCG(187, 2026)),
	}
	for index := range inputCount {
		fixture.inputKeys[index] = NewInputKey(fmt.Sprintf("input-%d", index))
		fixture.queryKeys[index] = NewQueryKey(fmt.Sprintf("query-%d", index))
		fixture.model[index] = randomizedInputState{
			revision: NewRevision(fmt.Sprintf("initial-%d", index)),
			found:    index%2 == 0,
			value:    fmt.Sprintf("initial-value-%d", index),
		}
	}
	fixture.graph = mustGraph(t, randomizedDefinitions(fixture.inputKeys, fixture.queryKeys)...)
	initial := mustBegin(t, fixture.graph)
	mustApply(t, initial, randomizedInputs(fixture.inputKeys, fixture.model)...)
	assertRandomizedRenderMatchesCold(t, initial, fixture.inputKeys, fixture.queryKeys, fixture.model)
	mustCommit(t, initial)
	return fixture
}

func (f *randomizedTransactionFixture) run(steps int) {
	for step := range steps {
		index := f.random.IntN(len(f.inputKeys))
		switch {
		case step%19 == 0:
			f.runCollision(step, index)
		case step%13 == 0:
			f.runAwayAndBack(step, index)
		default:
			f.runMutation(step, index)
		}
	}
}

func (f *randomizedTransactionFixture) runCollision(step, index int) {
	f.collisions++
	beforeGeneration := f.graph.Generation()
	beforeExecutions := randomizedExecutions(f.graph, f.queryKeys)
	first := Input{
		Key: f.inputKeys[index], Revision: NewRevision(fmt.Sprintf("collision-%d", step)), Found: true,
		Value: []byte(fmt.Sprintf("collision-first-%d", step)),
	}
	middle := Input{
		Key: f.inputKeys[index], Revision: NewRevision(fmt.Sprintf("collision-middle-%d", step)), Found: true,
		Value: []byte(fmt.Sprintf("collision-middle-%d", step)),
	}
	poison := first
	poison.Value = []byte(fmt.Sprintf("collision-poison-%d", step))

	session := mustBegin(f.t, f.graph)
	if _, err := session.ApplyInputsWhileIdle(first); err != nil {
		f.t.Fatalf("step %d first collision input: %v", step, err)
	}
	if _, err := session.ApplyInputsWhileIdle(middle); err != nil {
		f.t.Fatalf("step %d middle collision input: %v", step, err)
	}
	if _, err := session.ApplyInputsWhileIdle(poison); err == nil ||
		!strings.Contains(err.Error(), "reused an exact revision") {
		f.t.Fatalf("step %d collision error = %v", step, err)
	}
	if err := session.Commit(context.Background(), acceptRevisions); err == nil {
		f.t.Fatalf("step %d poisoned transaction committed", step)
	}
	session.Abort()
	assertRandomizedGraphUnchanged(
		f.t, f.graph, f.queryKeys, beforeGeneration, beforeExecutions, step, "collision",
	)

	next := cloneRandomizedState(f.model)
	next[index] = randomizedInputState{revision: first.Revision, found: true, value: string(first.Value)}
	retry := mustBegin(f.t, f.graph)
	mustApply(f.t, retry, first)
	assertRandomizedRenderMatchesCold(f.t, retry, f.inputKeys, f.queryKeys, next)
	mustCommit(f.t, retry)
	assertOnlyRandomizedQueryExecuted(f.t, f.graph, f.queryKeys, beforeExecutions, index, step)
	f.model = next
}

func (f *randomizedTransactionFixture) runAwayAndBack(step, index int) {
	f.awayAndBackMutations++
	beforeGeneration := f.graph.Generation()
	beforeExecutions := randomizedExecutions(f.graph, f.queryKeys)
	away := randomizedAwayInput(f.inputKeys[index], f.model[index], step)
	back := randomizedStateInput(f.inputKeys[index], randomizedInputState{
		revision: NewRevision(fmt.Sprintf("back-%d", step)),
		found:    f.model[index].found,
		value:    f.model[index].value,
	})
	next := cloneRandomizedState(f.model)
	next[index].revision = back.Revision
	session := f.beginAwayAndBack(step, index, away, back, next)
	if f.random.IntN(4) == 0 {
		f.aborts++
		session.Abort()
		assertRandomizedGraphUnchanged(
			f.t, f.graph, f.queryKeys, beforeGeneration, beforeExecutions, step, "ABA abort",
		)
		session = f.beginAwayAndBack(step, index, away, back, next)
	}
	var verified []InputRevision
	err := session.Commit(context.Background(), func(_ context.Context, inputs []InputRevision) (bool, error) {
		verified = append([]InputRevision(nil), inputs...)
		return true, nil
	})
	if err != nil {
		f.t.Fatalf("step %d ABA commit: %v", step, err)
	}
	assertRandomizedRevisionVerified(f.t, verified, f.inputKeys[index], back, step)
	if executions := randomizedExecutions(f.graph, f.queryKeys); !equalUint64s(executions, beforeExecutions) {
		f.t.Fatalf("step %d ABA executions = %#v, want %#v", step, executions, beforeExecutions)
	}
	f.model = next
}

func (f *randomizedTransactionFixture) beginAwayAndBack(
	step, index int,
	away, back Input,
	next []randomizedInputState,
) *Session {
	session := mustBegin(f.t, f.graph)
	dirty, err := session.ApplyInputsWhileIdle(away)
	if err != nil || len(dirty) != 1 || dirty[0] != f.queryKeys[index] {
		f.t.Fatalf("step %d away dirty = %#v, error = %v", step, dirty, err)
	}
	dirty, err = session.ApplyInputsWhileIdle(back)
	if err != nil || len(dirty) != 0 {
		f.t.Fatalf("step %d back dirty = %#v, error = %v", step, dirty, err)
	}
	assertRandomizedRenderMatchesCold(f.t, session, f.inputKeys, f.queryKeys, next)
	return session
}

func (f *randomizedTransactionFixture) runMutation(step, index int) {
	operation := f.random.IntN(3)
	if step%6 >= 1 && step%6 <= 3 {
		operation = step%6 - 1
	}
	index, operation = randomizedMutationTarget(f.model, index, operation)
	next := cloneRandomizedState(f.model)
	f.applyMutation(next, step, index, operation)
	change := randomizedStateInput(f.inputKeys[index], next[index])
	beforeGeneration := f.graph.Generation()
	beforeExecutions := randomizedExecutions(f.graph, f.queryKeys)
	session := f.beginMutation(step, index, change, next)
	if f.random.IntN(4) == 0 {
		f.aborts++
		session.Abort()
		assertRandomizedGraphUnchanged(
			f.t, f.graph, f.queryKeys, beforeGeneration, beforeExecutions, step, "mutation abort",
		)
		session = f.beginMutation(step, index, change, next)
	}
	mustCommit(f.t, session)
	assertOnlyRandomizedQueryExecuted(f.t, f.graph, f.queryKeys, beforeExecutions, index, step)
	f.model = next
}

func (f *randomizedTransactionFixture) applyMutation(
	next []randomizedInputState,
	step, index, operation int,
) {
	switch operation {
	case 0:
		f.adds++
		next[index] = randomizedInputState{
			revision: NewRevision(fmt.Sprintf("add-%d", step)), found: true,
			value: fmt.Sprintf("added-%d", step),
		}
	case 1:
		f.updates++
		next[index] = randomizedInputState{
			revision: NewRevision(fmt.Sprintf("update-%d", step)), found: true,
			value: fmt.Sprintf("updated-%d", step),
		}
	case 2:
		f.deletes++
		next[index] = randomizedInputState{revision: NewRevision(fmt.Sprintf("delete-%d", step))}
	}
}

func (f *randomizedTransactionFixture) beginMutation(
	step, index int,
	change Input,
	next []randomizedInputState,
) *Session {
	session := mustBegin(f.t, f.graph)
	dirty, err := session.ApplyInputsWhileIdle(change)
	if err != nil || len(dirty) != 1 || dirty[0] != f.queryKeys[index] {
		f.t.Fatalf("step %d mutation dirty = %#v, error = %v", step, dirty, err)
	}
	assertRandomizedRenderMatchesCold(f.t, session, f.inputKeys, f.queryKeys, next)
	return session
}

func (f *randomizedTransactionFixture) assertCoverage() {
	if f.adds == 0 || f.updates == 0 || f.deletes == 0 || f.aborts == 0 ||
		f.collisions == 0 || f.awayAndBackMutations == 0 {
		f.t.Fatalf("incomplete state-machine coverage: add=%d update=%d delete=%d abort=%d collision=%d ABA=%d",
			f.adds, f.updates, f.deletes, f.aborts, f.collisions, f.awayAndBackMutations)
	}
}

func randomizedDefinitions(inputs []InputKey, queries []QueryKey) []Definition {
	definitions := make([]Definition, len(queries))
	for index := range queries {
		input := inputs[index]
		definitions[index] = Definition{Key: queries[index], Run: func(_ context.Context, reader Reader) ([]byte, error) {
			value, found, err := reader.Input(input)
			if err != nil {
				return nil, err
			}
			if !found {
				return []byte("missing"), nil
			}
			return value, nil
		}}
	}
	return definitions
}

func randomizedInputs(keys []InputKey, state []randomizedInputState) []Input {
	inputs := make([]Input, len(keys))
	for index := range keys {
		inputs[index] = randomizedStateInput(keys[index], state[index])
	}
	return inputs
}

func randomizedStateInput(key InputKey, state randomizedInputState) Input {
	input := Input{Key: key, Revision: state.revision, Found: state.found}
	if state.found {
		input.Value = []byte(state.value)
	}
	return input
}

func randomizedAwayInput(key InputKey, state randomizedInputState, step int) Input {
	if state.found {
		return Input{Key: key, Revision: NewRevision(fmt.Sprintf("away-missing-%d", step))}
	}
	return Input{
		Key: key, Revision: NewRevision(fmt.Sprintf("away-present-%d", step)), Found: true,
		Value: []byte(fmt.Sprintf("away-%d", step)),
	}
}

func cloneRandomizedState(state []randomizedInputState) []randomizedInputState {
	return append([]randomizedInputState(nil), state...)
}

func randomizedMutationTarget(
	state []randomizedInputState,
	start, operation int,
) (index, targetOperation int) {
	wantFound := operation != 0
	for offset := range len(state) {
		index := (start + offset) % len(state)
		if state[index].found == wantFound {
			return index, operation
		}
	}
	if wantFound {
		return start, 0
	}
	return start, 1
}

func assertRandomizedRenderMatchesCold(
	t *testing.T,
	session *Session,
	inputKeys []InputKey,
	queryKeys []QueryKey,
	model []randomizedInputState,
) {
	t.Helper()
	warm, err := session.EvaluateAll(context.Background(), queryKeys...)
	if err != nil {
		t.Fatalf("warm EvaluateAll() error = %v", err)
	}
	oracle := mustGraph(t, randomizedDefinitions(inputKeys, queryKeys)...)
	cold, err := oracle.BeginColdReset(randomizedInputs(inputKeys, model)...)
	if err != nil {
		t.Fatalf("BeginColdReset() error = %v", err)
	}
	coldValues, err := cold.EvaluateAll(context.Background(), queryKeys...)
	if err != nil {
		t.Fatalf("cold EvaluateAll() error = %v", err)
	}
	mustCommit(t, cold)
	if len(warm) != len(coldValues) {
		t.Fatalf("warm results = %d, cold results = %d", len(warm), len(coldValues))
	}
	for index := range warm {
		if warm[index].Key != coldValues[index].Key || !bytes.Equal(warm[index].Value, coldValues[index].Value) {
			t.Fatalf("result %d warm = %#v, cold = %#v", index, warm[index], coldValues[index])
		}
	}
}

func randomizedExecutions(graph *Graph, queries []QueryKey) []uint64 {
	executions := make([]uint64, len(queries))
	for index, query := range queries {
		executions[index] = graph.Counters(query).Executions
	}
	return executions
}

func assertRandomizedGraphUnchanged(
	t *testing.T,
	graph *Graph,
	queries []QueryKey,
	generation uint64,
	executions []uint64,
	step int,
	operation string,
) {
	t.Helper()
	if graph.Generation() != generation {
		t.Fatalf("step %d %s generation = %d, want %d", step, operation, graph.Generation(), generation)
	}
	if current := randomizedExecutions(graph, queries); !equalUint64s(current, executions) {
		t.Fatalf("step %d %s executions = %#v, want %#v", step, operation, current, executions)
	}
}

func assertOnlyRandomizedQueryExecuted(
	t *testing.T,
	graph *Graph,
	queries []QueryKey,
	before []uint64,
	changed int,
	step int,
) {
	t.Helper()
	after := randomizedExecutions(graph, queries)
	for index := range queries {
		want := before[index]
		if index == changed {
			want++
		}
		if after[index] != want {
			t.Fatalf("step %d query %d executions = %d, want %d", step, index, after[index], want)
		}
	}
}

func assertRandomizedRevisionVerified(
	t *testing.T,
	verified []InputRevision,
	key InputKey,
	want Input,
	step int,
) {
	t.Helper()
	for _, input := range verified {
		if input.Key == key {
			if input.Revision != want.Revision || input.Found != want.Found {
				t.Fatalf("step %d verified revision = %#v, want revision %q found=%t",
					step, input, want.Revision.Opaque(), want.Found)
			}
			return
		}
	}
	t.Fatalf("step %d verifier did not receive %q", step, key.Opaque())
}

func equalUint64s(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
