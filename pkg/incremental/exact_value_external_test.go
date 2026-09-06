package incremental_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func TestExactValueHandleMutationCannotReachCache(t *testing.T) {
	query := incremental.NewQueryKey("query")
	graph, err := incremental.New(incremental.Definition{
		Key: query,
		Run: func(context.Context, incremental.Reader) ([]byte, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	session, err := graph.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	results, err := session.EvaluateAllExactBatch(t.Context(), func(
		_ context.Context,
		queries []incremental.BatchQuery,
	) ([]incremental.ExactBatchValue, error) {
		root, rootErr := queries[0].NewExactValue("immutable")
		return []incremental.ExactBatchValue{{Value: root, Err: rootErr}}, nil
	}, query)
	if err != nil {
		t.Fatalf("EvaluateAllExactBatch() error = %v", err)
	}
	original := results[0].Value
	copied := original
	copySame, err := copied.SameRoot(original)
	if err != nil || !copySame {
		t.Fatalf("copied SameRoot() = %v, %v; want true, nil", copySame, err)
	}
	results[0].Value = incremental.ExactValueRoot{}
	copied = incremental.ExactValueRoot{}
	if err := copied.ValidateAuthentication(); err == nil {
		t.Fatal("zero replacement authenticated")
	}
	if err := session.Commit(t.Context(), func(context.Context, []incremental.InputRevision) (bool, error) {
		return true, nil
	}); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	committed, found, err := graph.ExactValue(query)
	if err != nil || !found {
		t.Fatalf("ExactValue() found = %v, error = %v", found, err)
	}
	same, err := committed.SameRoot(original)
	if err != nil || !same {
		t.Fatalf("SameRoot() = %v, %v; want true, nil", same, err)
	}
	value, err := committed.String()
	if err != nil || value != "immutable" {
		t.Fatalf("String() = %q, %v; want immutable, nil", value, err)
	}
}

func TestGraphValidateExactValueRejectsForeignAndWrongQueryRoots(t *testing.T) {
	query := incremental.NewQueryKey("query")
	other := incremental.NewQueryKey("other")
	graph, root := externalExactValue(t, query)
	foreign, foreignRoot := externalExactValue(t, query)
	if err := graph.ValidateExactValue(query, root); err != nil {
		t.Fatalf("ValidateExactValue() error = %v", err)
	}
	handleCopy := root
	if err := graph.ValidateExactValue(query, handleCopy); err != nil {
		t.Fatalf("ValidateExactValue(copy) error = %v", err)
	}
	for name, test := range map[string]struct {
		key  incremental.QueryKey
		root incremental.ExactValueRoot
	}{
		"zero":      {key: query},
		"wrong-key": {key: other, root: root},
		"foreign":   {key: query, root: foreignRoot},
	} {
		t.Run(name, func(t *testing.T) {
			if err := graph.ValidateExactValue(test.key, test.root); err == nil {
				t.Fatal("ValidateExactValue() accepted an invalid root")
			}
		})
	}
	if err := foreign.ValidateExactValue(query, root); err == nil {
		t.Fatal("foreign graph accepted the original root")
	}
	if err := graph.ValidateCommittedExactValue(query, root); err == nil ||
		!strings.Contains(err.Error(), "no committed exact value") {
		t.Fatalf("ValidateCommittedExactValue() error = %v", err)
	}
}

func TestGraphValidateCommittedExactValueRequiresCurrentRootIdentity(t *testing.T) {
	query := incremental.NewQueryKey("query")
	inputKey := incremental.NewInputKey("input")
	graph := externalExactGraph(t, query)

	first := externalBegin(t, graph)
	externalApplyInput(t, first, inputKey, "r1", "first")
	firstResults, err := first.EvaluateAllExactBatch(
		t.Context(), externalInputExactBatch(inputKey, "first"), query,
	)
	if err != nil {
		t.Fatalf("first EvaluateAllExactBatch() error = %v", err)
	}
	externalCommit(t, first)
	historical := firstResults[0].Value
	if err := graph.ValidateCommittedExactValue(query, historical); err != nil {
		t.Fatalf("first ValidateCommittedExactValue() error = %v", err)
	}

	second := externalBegin(t, graph)
	externalApplyInput(t, second, inputKey, "r2", "second")
	secondResults, err := second.EvaluateAllExactBatch(
		t.Context(), externalInputExactBatch(inputKey, "second"), query,
	)
	if err != nil {
		t.Fatalf("second EvaluateAllExactBatch() error = %v", err)
	}
	externalCommit(t, second)
	current := secondResults[0].Value
	if err := graph.ValidateExactValue(query, historical); err != nil {
		t.Fatalf("historical ValidateExactValue() error = %v", err)
	}
	if err := graph.ValidateCommittedExactValue(query, historical); err == nil ||
		!strings.Contains(err.Error(), "not the committed query root") {
		t.Fatalf("historical ValidateCommittedExactValue() error = %v", err)
	}
	if err := graph.ValidateCommittedExactValue(query, current); err != nil {
		t.Fatalf("current ValidateCommittedExactValue() error = %v", err)
	}

	dirty := externalBegin(t, graph)
	externalApplyInput(t, dirty, inputKey, "r3", "dirty")
	externalCommit(t, dirty)
	if _, found, valueErr := graph.ExactValue(query); valueErr != nil || found {
		t.Fatalf("dirty ExactValue() found = %v, error = %v; want false, nil", found, valueErr)
	}
	if err := graph.ValidateCommittedExactValue(query, current); err != nil {
		t.Fatalf("dirty ValidateCommittedExactValue() error = %v", err)
	}
}

func TestSessionExactValueValidationDistinguishesBaseAndCurrentRoots(t *testing.T) {
	query := incremental.NewQueryKey("query")
	inputKey := incremental.NewInputKey("input")
	graph := externalExactGraph(t, query)

	first := externalBegin(t, graph)
	externalApplyInput(t, first, inputKey, "r1", "first")
	firstResults, err := first.EvaluateAllExactBatch(
		t.Context(), externalInputExactBatch(inputKey, "first"), query,
	)
	if err != nil {
		t.Fatalf("first EvaluateAllExactBatch() error = %v", err)
	}
	externalCommit(t, first)
	base := firstResults[0].Value

	second := externalBegin(t, graph)
	externalApplyInput(t, second, inputKey, "r2", "second")
	secondResults, err := second.EvaluateAllExactBatch(
		t.Context(), externalInputExactBatch(inputKey, "second"), query,
	)
	if err != nil {
		t.Fatalf("second EvaluateAllExactBatch() error = %v", err)
	}
	current := secondResults[0].Value
	if err := second.ValidateBaseExactValue(query, base); err != nil {
		t.Fatalf("base ValidateBaseExactValue() error = %v", err)
	}
	if err := second.ValidateCurrentExactValue(query, current); err != nil {
		t.Fatalf("current ValidateCurrentExactValue() error = %v", err)
	}
	if err := second.ValidateBaseExactValue(query, current); err == nil ||
		!strings.Contains(err.Error(), "not the transaction-base query root") {
		t.Fatalf("current ValidateBaseExactValue() error = %v", err)
	}
	if err := second.ValidateCurrentExactValue(query, base); err == nil ||
		!strings.Contains(err.Error(), "not the transaction-current query root") {
		t.Fatalf("base ValidateCurrentExactValue() error = %v", err)
	}
}

func TestSessionValidateCurrentExactValueAcceptsColdStagedRoot(t *testing.T) {
	query := incremental.NewQueryKey("query")
	graph := externalExactGraph(t, query)
	cold, err := graph.BeginColdReset()
	if err != nil {
		t.Fatalf("BeginColdReset() error = %v", err)
	}
	results, err := cold.EvaluateAllExactBatch(t.Context(), func(
		_ context.Context,
		queries []incremental.BatchQuery,
	) ([]incremental.ExactBatchValue, error) {
		root, rootErr := queries[0].NewExactValue("cold")
		return []incremental.ExactBatchValue{{Value: root, Err: rootErr}}, nil
	}, query)
	if err != nil {
		t.Fatalf("EvaluateAllExactBatch() error = %v", err)
	}
	if err := cold.ValidateCurrentExactValue(query, results[0].Value); err != nil {
		t.Fatalf("ValidateCurrentExactValue() error = %v", err)
	}
	if err := cold.ValidateBaseExactValue(query, results[0].Value); err == nil ||
		!strings.Contains(err.Error(), "no transaction-base exact value") {
		t.Fatalf("ValidateBaseExactValue() error = %v", err)
	}
}

func TestSessionCapturedExactValueSurvivesConcurrentWinningCommit(t *testing.T) {
	query := incremental.NewQueryKey("query")
	inputKey := incremental.NewInputKey("input")
	graph := externalExactGraph(t, query)

	initial := externalBegin(t, graph)
	externalApplyInput(t, initial, inputKey, "r1", "initial")
	initialResults, err := initial.EvaluateAllExactBatch(
		t.Context(), externalInputExactBatch(inputKey, "initial"), query,
	)
	if err != nil {
		t.Fatalf("initial EvaluateAllExactBatch() error = %v", err)
	}
	externalCommit(t, initial)
	initialRoot := initialResults[0].Value

	captured := externalBegin(t, graph)
	uncaptured := externalBegin(t, graph)
	if err := captured.ValidateBaseExactValue(query, initialRoot); err != nil {
		t.Fatalf("capturing ValidateBaseExactValue() error = %v", err)
	}

	winner := externalBegin(t, graph)
	externalApplyInput(t, winner, inputKey, "r2", "winner")
	if _, err := winner.EvaluateAllExactBatch(
		t.Context(), externalInputExactBatch(inputKey, "winner"), query,
	); err != nil {
		t.Fatalf("winner EvaluateAllExactBatch() error = %v", err)
	}
	externalCommit(t, winner)

	if err := captured.ValidateBaseExactValue(query, initialRoot); err != nil {
		t.Fatalf("captured ValidateBaseExactValue() after winner error = %v", err)
	}
	if err := captured.ValidateCurrentExactValue(query, initialRoot); err != nil {
		t.Fatalf("captured ValidateCurrentExactValue() after winner error = %v", err)
	}
	if err := captured.Commit(t.Context(), externalAcceptRevisions); !errors.Is(err, incremental.ErrCommitConflict) {
		t.Fatalf("captured Commit() error = %v", err)
	}
	// A session reads the generation it began on, captured or not; only
	// its commit races the winner.
	if err := uncaptured.ValidateBaseExactValue(query, initialRoot); err != nil {
		t.Fatalf("uncaptured ValidateBaseExactValue() after winner error = %v", err)
	}
	if err := uncaptured.Commit(t.Context(), externalAcceptRevisions); !errors.Is(err, incremental.ErrCommitConflict) {
		t.Fatalf("uncaptured Commit() error = %v", err)
	}
}

func TestExactBatchRejectsRootFromAbortedExecution(t *testing.T) {
	query := incremental.NewQueryKey("query")
	graph, aborted := externalExactValue(t, query)
	session, err := graph.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	_, err = session.EvaluateAllExactBatch(t.Context(), func(
		context.Context,
		[]incremental.BatchQuery,
	) ([]incremental.ExactBatchValue, error) {
		return []incremental.ExactBatchValue{{Value: aborted}}, nil
	}, query)
	if err == nil || !strings.Contains(err.Error(), "another query execution") {
		t.Fatalf("EvaluateAllExactBatch() error = %v", err)
	}
	if graph.Generation() != 0 {
		t.Fatalf("Generation() = %d, want 0", graph.Generation())
	}
}

func TestExactBatchRejectsHistoricalCommittedRoot(t *testing.T) {
	query := incremental.NewQueryKey("query")
	inputKey := incremental.NewInputKey("input")
	graph := externalExactGraph(t, query)
	first := externalBegin(t, graph)
	externalApplyInput(t, first, inputKey, "r1", "first")
	results, err := first.EvaluateAllExactBatch(
		t.Context(), externalInputExactBatch(inputKey, "first"), query,
	)
	if err != nil {
		t.Fatalf("first EvaluateAllExactBatch() error = %v", err)
	}
	externalCommit(t, first)
	historical := results[0].Value

	second := externalBegin(t, graph)
	externalApplyInput(t, second, inputKey, "r2", "second")
	_, err = second.EvaluateAllExactBatch(t.Context(), func(
		_ context.Context,
		queries []incremental.BatchQuery,
	) ([]incremental.ExactBatchValue, error) {
		if _, _, readErr := queries[0].Reader.Input(inputKey); readErr != nil {
			return nil, readErr
		}
		return []incremental.ExactBatchValue{{Value: historical}}, nil
	}, query)
	if err == nil || !strings.Contains(err.Error(), "another query execution") {
		t.Fatalf("second EvaluateAllExactBatch() error = %v", err)
	}
	committed, found, valueErr := graph.ExactValue(query)
	if valueErr != nil || !found {
		t.Fatalf("ExactValue() found = %v, error = %v", found, valueErr)
	}
	same, sameErr := committed.SameRoot(historical)
	if sameErr != nil || !same {
		t.Fatalf("SameRoot() = %v, %v; want true, nil", same, sameErr)
	}
}

func TestExactBatchRejectsRootFromCancelledExecution(t *testing.T) {
	query := incremental.NewQueryKey("query")
	graph := externalExactGraph(t, query)
	cancelledSession := externalBegin(t, graph)
	ctx, cancel := context.WithCancel(t.Context())
	var cancelled incremental.ExactValueRoot
	_, err := cancelledSession.EvaluateAllExactBatch(ctx, func(
		_ context.Context,
		queries []incremental.BatchQuery,
	) ([]incremental.ExactBatchValue, error) {
		var rootErr error
		cancelled, rootErr = queries[0].NewExactValue("cancelled")
		cancel()
		return []incremental.ExactBatchValue{{Value: cancelled, Err: rootErr}}, nil
	}, query)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled EvaluateAllExactBatch() error = %v", err)
	}
	cancelledSession.Abort()

	next := externalBegin(t, graph)
	_, err = next.EvaluateAllExactBatch(t.Context(), func(
		context.Context,
		[]incremental.BatchQuery,
	) ([]incremental.ExactBatchValue, error) {
		return []incremental.ExactBatchValue{{Value: cancelled}}, nil
	}, query)
	if err == nil || !strings.Contains(err.Error(), "another query execution") {
		t.Fatalf("next EvaluateAllExactBatch() error = %v", err)
	}
}

func TestExactBatchRejectsRootFromConflictedExecution(t *testing.T) {
	query := incremental.NewQueryKey("query")
	inputKey := incremental.NewInputKey("input")
	graph := externalExactGraph(t, query)
	winner := externalBegin(t, graph)
	conflicted := externalBegin(t, graph)
	externalApplyInput(t, winner, inputKey, "r1", "winner")
	externalApplyInput(t, conflicted, inputKey, "r2", "conflicted")
	if _, err := winner.EvaluateAllExactBatch(
		t.Context(), externalInputExactBatch(inputKey, "winner"), query,
	); err != nil {
		t.Fatalf("winner EvaluateAllExactBatch() error = %v", err)
	}
	conflictedResults, err := conflicted.EvaluateAllExactBatch(
		t.Context(), externalInputExactBatch(inputKey, "conflicted"), query,
	)
	if err != nil {
		t.Fatalf("conflicted EvaluateAllExactBatch() error = %v", err)
	}
	externalCommit(t, winner)
	if err := conflicted.Commit(t.Context(), externalAcceptRevisions); !errors.Is(err, incremental.ErrCommitConflict) {
		t.Fatalf("conflicted Commit() error = %v", err)
	}

	next := externalBegin(t, graph)
	externalApplyInput(t, next, inputKey, "r3", "next")
	_, err = next.EvaluateAllExactBatch(t.Context(), func(
		_ context.Context,
		queries []incremental.BatchQuery,
	) ([]incremental.ExactBatchValue, error) {
		if _, _, readErr := queries[0].Reader.Input(inputKey); readErr != nil {
			return nil, readErr
		}
		return []incremental.ExactBatchValue{{Value: conflictedResults[0].Value}}, nil
	}, query)
	if err == nil || !strings.Contains(err.Error(), "another query execution") {
		t.Fatalf("next EvaluateAllExactBatch() error = %v", err)
	}
}

func externalExactGraph(t *testing.T, query incremental.QueryKey) *incremental.Graph {
	t.Helper()
	graph, err := incremental.New(incremental.Definition{
		Key: query,
		Run: func(context.Context, incremental.Reader) ([]byte, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return graph
}

func externalBegin(t *testing.T, graph *incremental.Graph) *incremental.Session {
	t.Helper()
	session, err := graph.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	return session
}

func externalApplyInput(
	t *testing.T,
	session *incremental.Session,
	key incremental.InputKey,
	revision, value string,
) {
	t.Helper()
	if err := session.ApplyInputs(incremental.Input{
		Key: key, Revision: incremental.NewRevision(revision), Found: true, Value: []byte(value),
	}); err != nil {
		t.Fatalf("ApplyInputs() error = %v", err)
	}
}

func externalInputExactBatch(
	key incremental.InputKey,
	value string,
) incremental.ExactBatchQueryFunc {
	return func(_ context.Context, queries []incremental.BatchQuery) ([]incremental.ExactBatchValue, error) {
		results := make([]incremental.ExactBatchValue, len(queries))
		for index := range queries {
			if _, _, err := queries[index].Reader.Input(key); err != nil {
				results[index].Err = err
				continue
			}
			results[index].Value, results[index].Err = queries[index].NewExactValue(value)
		}
		return results, nil
	}
}

func externalCommit(t *testing.T, session *incremental.Session) {
	t.Helper()
	if err := session.Commit(t.Context(), externalAcceptRevisions); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
}

func externalAcceptRevisions(context.Context, []incremental.InputRevision) (bool, error) {
	return true, nil
}

func externalExactValue(
	t *testing.T,
	query incremental.QueryKey,
) (*incremental.Graph, incremental.ExactValueRoot) {
	t.Helper()
	graph, err := incremental.New(incremental.Definition{
		Key: query,
		Run: func(context.Context, incremental.Reader) ([]byte, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	session, err := graph.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	results, err := session.EvaluateAllExactBatch(t.Context(), func(
		_ context.Context,
		queries []incremental.BatchQuery,
	) ([]incremental.ExactBatchValue, error) {
		root, rootErr := queries[0].NewExactValue("value")
		return []incremental.ExactBatchValue{{Value: root, Err: rootErr}}, nil
	}, query)
	if err != nil {
		t.Fatalf("EvaluateAllExactBatch() error = %v", err)
	}
	session.Abort()
	return graph, results[0].Value
}
