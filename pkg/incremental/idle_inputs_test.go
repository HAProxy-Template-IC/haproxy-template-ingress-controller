package incremental

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

func TestApplyInputsWhileIdleInvalidatesOnlyUnevaluatedDependents(t *testing.T) {
	selectorKey := NewInputKey("selector")
	producerKey := NewQueryKey("producer")
	consumerKey := NewQueryKey("consumer")
	unrelatedKey := NewQueryKey("unrelated")
	graph := mustGraph(t,
		Definition{Key: producerKey, Run: constantQuery("producer")},
		Definition{Key: consumerKey, Run: readInputQuery(selectorKey)},
		Definition{Key: unrelatedKey, Run: constantQuery("unrelated")},
	)

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(selectorKey, "selector-1", "old"))
	mustEvaluate(t, session, producerKey)
	mustEvaluate(t, session, consumerKey)
	mustEvaluate(t, session, unrelatedKey)
	mustCommit(t, session)

	session = mustBegin(t, graph)
	mustEvaluate(t, session, producerKey)
	dirty, err := session.ApplyInputsWhileIdle(exactInput(selectorKey, "selector-2", "new"))
	if err != nil {
		t.Fatalf("ApplyInputsWhileIdle() error = %v", err)
	}
	if len(dirty) != 1 || dirty[0] != consumerKey {
		t.Fatalf("newly dirty queries = %#v, want %#v", dirty, []QueryKey{consumerKey})
	}
	if got := string(mustEvaluate(t, session, consumerKey)); got != "new" {
		t.Fatalf("consumer value = %q", got)
	}
	mustCommit(t, session)

	if counters := graph.Counters(producerKey); counters.Executions != 1 {
		t.Fatalf("producer counters = %+v", counters)
	}
	if counters := graph.Counters(unrelatedKey); counters.Invalidations != 0 {
		t.Fatalf("unrelated counters = %+v", counters)
	}
}

func TestApplyInputsWhileIdleRejectsAlreadyEvaluatedConsumerAtomically(t *testing.T) {
	selectorKey := NewInputKey("selector")
	consumerKey := NewQueryKey("consumer")
	graph := mustGraph(t, Definition{Key: consumerKey, Run: readInputQuery(selectorKey)})

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(selectorKey, "selector-1", "old"))
	mustEvaluate(t, session, consumerKey)
	mustCommit(t, session)

	session = mustBegin(t, graph)
	mustEvaluate(t, session, consumerKey)
	_, err := session.ApplyInputsWhileIdle(exactInput(selectorKey, "selector-2", "new"))
	if err == nil || !strings.Contains(err.Error(), "after it was evaluated") {
		t.Fatalf("ApplyInputsWhileIdle() error = %v", err)
	}
	if _, changed := session.inputChanges[selectorKey]; changed {
		t.Fatal("rejected selector update changed speculative input state")
	}
	if err := session.Commit(context.Background(), acceptRevisions); err == nil {
		t.Fatal("failed selector session committed")
	}
	if got := stringValue(t, graph, consumerKey); got != "old" {
		t.Fatalf("committed consumer value = %q", got)
	}
}

func TestApplyInputsWhileIdleRejectsDirectAndTransitiveCycles(t *testing.T) {
	for _, test := range []struct {
		name        string
		definitions func(InputKey, QueryKey) []Definition
	}{
		{
			name: "direct",
			definitions: func(selectorKey InputKey, producerKey QueryKey) []Definition {
				return []Definition{{Key: producerKey, Run: readInputQuery(selectorKey)}}
			},
		},
		{
			name: "transitive",
			definitions: func(selectorKey InputKey, producerKey QueryKey) []Definition {
				middleKey := NewQueryKey("middle")
				return []Definition{
					{Key: middleKey, Run: readInputQuery(selectorKey)},
					{Key: producerKey, Run: func(ctx context.Context, reader Reader) ([]byte, error) {
						return reader.Query(ctx, middleKey)
					}},
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			selectorKey := NewInputKey("selector")
			producerKey := NewQueryKey("producer")
			graph := mustGraph(t, test.definitions(selectorKey, producerKey)...)
			session := mustBegin(t, graph)
			mustApply(t, session, Input{
				Key:      selectorKey,
				Revision: NewRevision("missing"),
			})
			mustEvaluate(t, session, producerKey)

			_, err := session.ApplyInputsWhileIdle(exactInput(selectorKey, "winner", "value"))
			if err == nil || !strings.Contains(err.Error(), "after it was evaluated") {
				t.Fatalf("ApplyInputsWhileIdle() error = %v", err)
			}
			if got := session.inputChanges[selectorKey]; got.revision != NewRevision("missing") || got.found {
				t.Fatalf("rejected update changed selector = %#v", got)
			}
		})
	}
}

func TestApplyInputsWhileIdleTracksMissingAndWinnerTransitions(t *testing.T) {
	selectorKey := NewInputKey("selector")
	consumerKey := NewQueryKey("consumer")
	graph := mustGraph(t, Definition{Key: consumerKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		value, found, err := reader.Input(selectorKey)
		if err != nil {
			return nil, err
		}
		if !found {
			return []byte("missing"), nil
		}
		return value, nil
	}})

	session := mustBegin(t, graph)
	mustApply(t, session, Input{Key: selectorKey, Revision: NewRevision("missing-1")})
	if got := string(mustEvaluate(t, session, consumerKey)); got != "missing" {
		t.Fatalf("initial value = %q", got)
	}
	mustCommit(t, session)

	session = mustBegin(t, graph)
	dirty, err := session.ApplyInputsWhileIdle(exactInput(selectorKey, "winner-1", "winner"))
	if err != nil || len(dirty) != 1 || dirty[0] != consumerKey {
		t.Fatalf("missing to winner dirty = %#v, error = %v", dirty, err)
	}
	if got := string(mustEvaluate(t, session, consumerKey)); got != "winner" {
		t.Fatalf("winner value = %q", got)
	}
	mustCommit(t, session)

	session = mustBegin(t, graph)
	dirty, err = session.ApplyInputsWhileIdle(Input{
		Key:      selectorKey,
		Revision: NewRevision("missing-2"),
	})
	if err != nil || len(dirty) != 1 || dirty[0] != consumerKey {
		t.Fatalf("winner to missing dirty = %#v, error = %v", dirty, err)
	}
	if got := string(mustEvaluate(t, session, consumerKey)); got != "missing" {
		t.Fatalf("missing value = %q", got)
	}
}

func TestApplyInputsWhileIdleBackdatesAwayAndBackWithoutExecution(t *testing.T) {
	for _, test := range []struct {
		name    string
		initial Input
		away    Input
		back    Input
		want    string
	}{
		{
			name:    "present through missing",
			initial: exactInput(NewInputKey("selector"), "present-1", "winner"),
			away:    Input{Key: NewInputKey("selector"), Revision: NewRevision("missing")},
			back:    exactInput(NewInputKey("selector"), "present-2", "winner"),
			want:    "winner",
		},
		{
			name:    "missing through present",
			initial: Input{Key: NewInputKey("selector"), Revision: NewRevision("missing-1")},
			away:    exactInput(NewInputKey("selector"), "present", "winner"),
			back:    Input{Key: NewInputKey("selector"), Revision: NewRevision("missing-2")},
			want:    "missing",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runBackdatesAwayAndBackTest(t, test.initial, test.away, test.back, test.want)
		})
	}
}

func runBackdatesAwayAndBackTest(t *testing.T, initial, away, back Input, want string) {
	t.Helper()
	selectorKey := NewInputKey("selector")
	consumerKey := NewQueryKey("consumer")
	var executions atomic.Uint64
	graph := mustGraph(t, Definition{Key: consumerKey, Run: func(_ context.Context, reader Reader) ([]byte, error) {
		executions.Add(1)
		value, found, err := reader.Input(selectorKey)
		if err != nil {
			return nil, err
		}
		if !found {
			return []byte("missing"), nil
		}
		return value, nil
	}})

	session := mustBegin(t, graph)
	mustApply(t, session, initial)
	mustEvaluate(t, session, consumerKey)
	mustCommit(t, session)

	session = mustBegin(t, graph)
	dirty, err := session.ApplyInputsWhileIdle(away)
	if err != nil || len(dirty) != 1 || dirty[0] != consumerKey {
		t.Fatalf("away dirty = %#v, error = %v", dirty, err)
	}
	dirty, err = session.ApplyInputsWhileIdle(back)
	if err != nil || len(dirty) != 0 {
		t.Fatalf("back dirty = %#v, error = %v", dirty, err)
	}
	if got := string(mustEvaluate(t, session, consumerKey)); got != want {
		t.Fatalf("consumer value = %q, want %q", got, want)
	}
	var verified []InputRevision
	err = session.Commit(context.Background(), func(_ context.Context, inputs []InputRevision) (bool, error) {
		verified = append([]InputRevision(nil), inputs...)
		return true, nil
	})
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if executions.Load() != 1 {
		t.Fatalf("away-and-back input executed query %d times", executions.Load())
	}
	if len(verified) != 1 || verified[0].Revision != back.Revision || verified[0].Found != back.Found {
		t.Fatalf("verified revisions = %#v", verified)
	}
}

func TestApplyInputsWhileIdleRejectsActiveEvaluation(t *testing.T) {
	selectorKey := NewInputKey("selector")
	queryKey := NewQueryKey("query")
	var session *Session
	graph := mustGraph(t, Definition{Key: queryKey, Run: func(context.Context, Reader) ([]byte, error) {
		_, err := session.ApplyInputsWhileIdle(exactInput(selectorKey, "selector-2", "new"))
		return nil, err
	}})
	session = mustBegin(t, graph)
	mustApply(t, session, exactInput(selectorKey, "selector-1", "old"))

	_, err := session.Evaluate(context.Background(), queryKey)
	if err == nil || !strings.Contains(err.Error(), "during query evaluation") {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if got := session.inputChanges[selectorKey]; got.revision != NewRevision("selector-1") {
		t.Fatalf("active update changed selector = %#v", got)
	}
}

func TestApplyInputsWhileIdleRejectsPoisonedBatchAtomically(t *testing.T) {
	leftKey := NewInputKey("left")
	rightKey := NewInputKey("right")
	graph := mustGraph(t)
	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(rightKey, "right-1", "right"))

	_, err := session.ApplyInputsWhileIdle(
		exactInput(leftKey, "left-1", "left"),
		exactInput(rightKey, "right-1", "poison"),
	)
	if err == nil || !strings.Contains(err.Error(), "reused an exact revision") {
		t.Fatalf("ApplyInputsWhileIdle() error = %v", err)
	}
	if _, changed := session.inputChanges[leftKey]; changed {
		t.Fatal("rejected batch staged its valid prefix")
	}
	if got := session.inputChanges[rightKey]; string(got.value) != "right" {
		t.Fatalf("rejected batch changed existing input = %#v", got)
	}
}

func TestApplyInputsWhileIdleRejectsExactRevisionReuseAfterInterveningChanges(t *testing.T) {
	for _, test := range []struct {
		name        string
		changes     []Input
		reused      Input
		wantCurrent string
	}{
		{
			name: "committed revision",
			changes: []Input{
				exactInput(NewInputKey("selector"), "middle", "middle"),
			},
			reused:      exactInput(NewInputKey("selector"), "initial", "poison"),
			wantCurrent: "middle",
		},
		{
			name: "speculative revision",
			changes: []Input{
				exactInput(NewInputKey("selector"), "middle", "middle"),
				exactInput(NewInputKey("selector"), "later", "later"),
			},
			reused:      exactInput(NewInputKey("selector"), "middle", "poison"),
			wantCurrent: "later",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runExactRevisionReuseTest(t, test.changes, test.reused, test.wantCurrent)
		})
	}
}

func runExactRevisionReuseTest(t *testing.T, changes []Input, reused Input, wantCurrent string) {
	t.Helper()
	selectorKey := NewInputKey("selector")
	consumerKey := NewQueryKey("consumer")
	graph := mustGraph(t, Definition{Key: consumerKey, Run: readInputQuery(selectorKey)})

	session := mustBegin(t, graph)
	mustApply(t, session, exactInput(selectorKey, "initial", "initial"))
	mustEvaluate(t, session, consumerKey)
	mustCommit(t, session)

	session = mustBegin(t, graph)
	for _, change := range changes {
		_, err := session.ApplyInputsWhileIdle(change)
		if err != nil {
			t.Fatalf("ApplyInputsWhileIdle() error = %v", err)
		}
	}
	_, err := session.ApplyInputsWhileIdle(reused)
	if err == nil || !strings.Contains(err.Error(), "reused an exact revision") {
		t.Fatalf("ApplyInputsWhileIdle() error = %v", err)
	}
	if got := string(session.inputChanges[selectorKey].value); got != wantCurrent {
		t.Fatalf("rejected revision changed current input to %q", got)
	}
	if err := session.Commit(context.Background(), acceptRevisions); err == nil {
		t.Fatal("failed exact-revision session committed")
	}
	if got := stringValue(t, graph, consumerKey); got != "initial" {
		t.Fatalf("committed consumer value = %q", got)
	}
}
