package incremental

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func validateLiveExactQueryObservation(
	observation *ExactQueryObservation,
	sourceKey, ownerKey QueryKey,
) error {
	if err := observation.ValidateAuthentication(); err != nil {
		return fmt.Errorf("validating live observation: %w", err)
	}
	if err := observation.ValidateFor(sourceKey); err != nil {
		return fmt.Errorf("validating live observation key: %w", err)
	}
	if err := observation.ValidateFor(ownerKey); err == nil ||
		!strings.Contains(err.Error(), "belongs to another query") {
		return fmt.Errorf("wrong-key observation validation error = %v", err)
	}
	return nil
}

func TestColdExactBatchExactQueryObservationRecordsExactDependencies(t *testing.T) {
	inputKey := NewInputKey("source-input")
	sourceKey := NewQueryKey("source")
	ownerKey := NewQueryKey("transaction/owner")
	siblingKey := NewQueryKey("transaction/sibling")
	graph := exactQueryObservationGraph(t, inputKey, sourceKey, ownerKey, siblingKey)
	session := mustColdSession(t, graph, exactInput(inputKey, "revision-1", "source-value"))
	var retained ExactQueryObservation
	results, err := session.EvaluateAllColdExactBatch(t.Context(), func(
		ctx context.Context,
		batch ColdExactBatch,
	) error {
		owner := exactBatchQueryByKey(batch, ownerKey)
		value, observation, readErr := owner.QueryWithExactObservation(ctx, sourceKey)
		if readErr != nil {
			return readErr
		}
		if string(value) != "source-value" {
			return fmt.Errorf("source value = %q", value)
		}
		if err := validateLiveExactQueryObservation(&observation, sourceKey, ownerKey); err != nil {
			return err
		}
		retained = observation
		if err := exactBatchQueryByKey(batch, siblingKey).ObserveExactQuery(observation); err != nil {
			return err
		}
		for _, key := range []QueryKey{ownerKey, siblingKey} {
			if _, err := exactBatchQueryByKey(batch, key).Complete(key.Opaque()); err != nil {
				return err
			}
		}
		return nil
	}, siblingKey, ownerKey)
	if err != nil {
		t.Fatalf("EvaluateAllColdExactBatch() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v", results)
	}
	if err := retained.ValidateAuthentication(); err == nil || !strings.Contains(err.Error(), "no longer active") {
		t.Fatalf("revoked observation authentication error = %v", err)
	}
	wantInput := InputRevision{Key: inputKey, Revision: NewRevision("revision-1"), Found: true}
	assertExactQueryObservationDependency(t, session.nodeChanges[ownerKey], sourceKey, wantInput)
	assertExactQueryObservationDependency(t, session.nodeChanges[siblingKey], sourceKey, wantInput)
	mustCommit(t, session)
	if counters := graph.Counters(sourceKey); counters.Executions != 1 || counters.CacheHits != 0 {
		t.Fatalf("source counters = %#v, want one execution and no cache hit", counters)
	}
}

func TestColdExactBatchExactQueryObservationReusesSealedMember(t *testing.T) {
	inputKey := NewInputKey("source-input")
	sourceKey := NewQueryKey("00-source")
	ownerKey := NewQueryKey("10-owner")
	siblingKey := NewQueryKey("20-sibling")
	graph := exactValueTestGraph(t, sourceKey, ownerKey, siblingKey)
	session := mustColdSession(t, graph, exactInput(inputKey, "revision-1", "source-value"))
	_, err := session.EvaluateAllColdExactBatch(t.Context(), func(
		ctx context.Context,
		batch ColdExactBatch,
	) error {
		source := exactBatchQueryByKey(batch, sourceKey)
		input, err := source.ExactInputOwned(inputKey)
		if err != nil {
			return err
		}
		sourceRoot, err := source.Complete(string(input.Value))
		if err != nil {
			return err
		}
		if err := batch.SealWave(ExactResult{Key: sourceKey, Value: sourceRoot}); err != nil {
			return err
		}

		owner := exactBatchQueryByKey(batch, ownerKey)
		value, observation, err := owner.QueryWithExactObservation(ctx, sourceKey)
		if err != nil {
			return err
		}
		if err := exactBatchQueryByKey(batch, siblingKey).ObserveExactQuery(observation); err != nil {
			return err
		}
		ownerRoot, err := owner.Complete(string(value))
		if err != nil {
			return err
		}
		siblingRoot, err := exactBatchQueryByKey(batch, siblingKey).Complete(string(value))
		if err != nil {
			return err
		}
		return batch.SealWave(
			ExactResult{Key: ownerKey, Value: ownerRoot},
			ExactResult{Key: siblingKey, Value: siblingRoot},
		)
	}, sourceKey, ownerKey, siblingKey)
	if err != nil {
		t.Fatalf("EvaluateAllColdExactBatch() error = %v", err)
	}
	wantInput := InputRevision{Key: inputKey, Revision: NewRevision("revision-1"), Found: true}
	assertExactQueryObservationDependency(t, session.nodeChanges[ownerKey], sourceKey, wantInput)
	assertExactQueryObservationDependency(t, session.nodeChanges[siblingKey], sourceKey, wantInput)
	mustCommit(t, session)
}

func TestColdExactBatchExactQueryObservationRejectsPoison(t *testing.T) {
	tests := []struct {
		name   string
		poison func(ExactQueryObservation, ExactQueryObservation) ExactQueryObservation
		want   error
	}{
		{
			name: "zero handle",
			poison: func(ExactQueryObservation, ExactQueryObservation) ExactQueryObservation {
				return ExactQueryObservation{}
			},
		},
		{
			name: "substituted root",
			poison: func(observation, other ExactQueryObservation) ExactQueryObservation {
				observation.root = other.root
				return observation
			},
		},
		{
			name: "substituted change revision",
			poison: func(observation, _ ExactQueryObservation) ExactQueryObservation {
				observation.changedAt = 0
				return observation
			},
		},
		{
			name: "substituted dependencies",
			poison: func(observation, _ ExactQueryObservation) ExactQueryObservation {
				observation.deps = append([]dependency(nil), observation.deps...)
				observation.deps[0].changedAt = 0
				return observation
			},
		},
		{
			name: "substituted transitive input",
			poison: func(observation, _ ExactQueryObservation) ExactQueryObservation {
				observation.inputs = append([]InputRevision(nil), observation.inputs...)
				observation.inputs[0].Revision = NewRevision("stale-revision")
				return observation
			},
			want: ErrRevisionConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runColdExactBatchPoisonedObservation(t, test.poison, test.want)
		})
	}
}

func runColdExactBatchPoisonedObservation(
	t *testing.T,
	poison func(ExactQueryObservation, ExactQueryObservation) ExactQueryObservation,
	want error,
) {
	t.Helper()
	inputKey := NewInputKey("source-input")
	leftSource := NewQueryKey("source/left")
	rightSource := NewQueryKey("source/right")
	ownerKey := NewQueryKey("transaction/owner")
	siblingKey := NewQueryKey("transaction/sibling")
	graph := exactQueryObservationGraph(
		t,
		inputKey,
		leftSource,
		rightSource,
		ownerKey,
		siblingKey,
	)
	session := mustColdSession(t, graph, exactInput(inputKey, "revision-1", "source-value"))
	_, err := session.EvaluateAllColdExactBatch(t.Context(), func(
		ctx context.Context,
		batch ColdExactBatch,
	) error {
		return observePoisonedExactQuery(ctx, batch, leftSource, rightSource, ownerKey, siblingKey, poison)
	}, ownerKey, siblingKey)
	if err == nil {
		t.Fatal("poisoned observation was accepted")
	}
	if want != nil && !errors.Is(err, want) {
		t.Fatalf("EvaluateAllColdExactBatch() error = %v, want %v", err, want)
	}
	if graph.Generation() != 0 {
		t.Fatal("poisoned observation changed the graph")
	}
	if len(session.nodeChanges) != 2 {
		t.Fatalf("poisoned observation staged %d batch nodes", len(session.nodeChanges))
	}
}

func observePoisonedExactQuery(
	ctx context.Context,
	batch ColdExactBatch,
	leftSource, rightSource, ownerKey, siblingKey QueryKey,
	poison func(ExactQueryObservation, ExactQueryObservation) ExactQueryObservation,
) error {
	owner := exactBatchQueryByKey(batch, ownerKey)
	_, observation, err := owner.QueryWithExactObservation(ctx, leftSource)
	if err != nil {
		return err
	}
	_, other, err := owner.QueryWithExactObservation(ctx, rightSource)
	if err != nil {
		return err
	}
	poisonErr := exactBatchQueryByKey(batch, siblingKey).ObserveExactQuery(
		poison(observation, other),
	)
	if poisonErr == nil {
		return errors.New("poisoned observation was accepted")
	}
	if authenticationErr := observation.ValidateAuthentication(); authenticationErr == nil ||
		!strings.Contains(authenticationErr.Error(), "transaction has failed") {
		return fmt.Errorf("failed transaction observation authentication error = %v", authenticationErr)
	}
	return poisonErr
}

func TestColdExactBatchExactQueryObservationRejectsForeignTransaction(t *testing.T) {
	inputKey := NewInputKey("source-input")
	sourceKey := NewQueryKey("source")
	ownerKey := NewQueryKey("transaction/owner")
	siblingKey := NewQueryKey("transaction/sibling")
	outerGraph := exactQueryObservationGraph(t, inputKey, sourceKey, ownerKey)
	outer := mustColdSession(t, outerGraph, exactInput(inputKey, "outer-revision", "outer"))
	innerGraph := exactQueryObservationGraph(t, inputKey, sourceKey, siblingKey)
	inner := mustColdSession(t, innerGraph, exactInput(inputKey, "inner-revision", "inner"))

	_, err := outer.EvaluateAllColdExactBatch(t.Context(), func(
		ctx context.Context,
		batch ColdExactBatch,
	) error {
		owner := exactBatchQueryByKey(batch, ownerKey)
		_, observation, err := owner.QueryWithExactObservation(ctx, sourceKey)
		if err != nil {
			return err
		}
		_, foreignErr := inner.EvaluateAllColdExactBatch(ctx, func(
			_ context.Context,
			foreign ColdExactBatch,
		) error {
			return exactBatchQueryByKey(foreign, siblingKey).ObserveExactQuery(observation)
		}, siblingKey)
		if foreignErr == nil || !strings.Contains(foreignErr.Error(), "invalid provenance") {
			return fmt.Errorf("foreign observation error = %v", foreignErr)
		}
		_, err = owner.Complete("outer")
		return err
	}, ownerKey)
	if err != nil {
		t.Fatalf("outer EvaluateAllColdExactBatch() error = %v", err)
	}
	mustCommit(t, outer)
	if innerGraph.Generation() != 0 {
		t.Fatal("foreign observation changed the inner graph")
	}
}

func observeExactQueryWithForgedTargets(
	ctx context.Context,
	batch ColdExactBatch,
	sourceKey, ownerKey, siblingKey QueryKey,
) error {
	sourceRoot, err := exactBatchQueryByKey(batch, sourceKey).Complete("source")
	if err != nil {
		return err
	}
	if err := batch.SealWave(ExactResult{Key: sourceKey, Value: sourceRoot}); err != nil {
		return err
	}
	owner := exactBatchQueryByKey(batch, ownerKey)
	_, observation, err := owner.QueryWithExactObservation(ctx, sourceKey)
	if err != nil {
		return err
	}
	forged := exactBatchQueryByKey(batch, siblingKey)
	forged.index = owner.index
	if err := forged.ObserveExactQuery(observation); err == nil ||
		!strings.Contains(err.Error(), "invalid authority") {
		return fmt.Errorf("forged observer error = %v", err)
	}
	ownerRoot, err := owner.Complete("owner")
	if err != nil {
		return err
	}
	if err := batch.SealWave(ExactResult{Key: ownerKey, Value: ownerRoot}); err != nil {
		return err
	}
	if err := owner.ObserveExactQuery(observation); err == nil ||
		!strings.Contains(err.Error(), "sealed") {
		return fmt.Errorf("sealed observer error = %v", err)
	}
	sibling := exactBatchQueryByKey(batch, siblingKey)
	if err := sibling.ObserveExactQuery(observation); err != nil {
		return err
	}
	siblingRoot, err := sibling.Complete("sibling")
	if err != nil {
		return err
	}
	return batch.SealWave(ExactResult{Key: siblingKey, Value: siblingRoot})
}

func TestColdExactBatchExactQueryObservationRejectsUnsealedAndForgedTargets(t *testing.T) {
	sourceKey := NewQueryKey("00-source")
	ownerKey := NewQueryKey("10-owner")
	siblingKey := NewQueryKey("20-sibling")
	graph := exactValueTestGraph(t, sourceKey, ownerKey, siblingKey)
	session := mustColdSession(t, graph)
	_, err := session.EvaluateAllColdExactBatch(t.Context(), func(
		ctx context.Context,
		batch ColdExactBatch,
	) error {
		owner := exactBatchQueryByKey(batch, ownerKey)
		_, _, readErr := owner.QueryWithExactObservation(ctx, sourceKey)
		if readErr == nil || !strings.Contains(readErr.Error(), "before it is sealed") {
			return fmt.Errorf("unsealed source observation error = %v", readErr)
		}
		return readErr
	}, sourceKey, ownerKey, siblingKey)
	if err == nil || !strings.Contains(err.Error(), "before it is sealed") {
		t.Fatalf("EvaluateAllColdExactBatch() error = %v", err)
	}
	if graph.Generation() != 0 {
		t.Fatal("unsealed observation changed the graph")
	}

	graph = exactValueTestGraph(t, sourceKey, ownerKey, siblingKey)
	session = mustColdSession(t, graph)
	_, err = session.EvaluateAllColdExactBatch(t.Context(), func(
		ctx context.Context,
		batch ColdExactBatch,
	) error {
		return observeExactQueryWithForgedTargets(ctx, batch, sourceKey, ownerKey, siblingKey)
	}, sourceKey, ownerKey, siblingKey)
	if err != nil {
		t.Fatalf("valid batch after forged target error = %v", err)
	}
	mustCommit(t, session)
}

func TestColdExactBatchExactQueryObservationRejectsInputABA(t *testing.T) {
	inputKey := NewInputKey("source-input")
	sourceKey := NewQueryKey("source")
	ownerKey := NewQueryKey("transaction/owner")
	siblingKey := NewQueryKey("transaction/sibling")
	graph := exactQueryObservationGraph(t, inputKey, sourceKey, ownerKey, siblingKey)
	first := mustColdSession(t, graph, exactInput(inputKey, "revision-1", "same-value"))
	var oldInputs []InputRevision
	_, err := first.EvaluateAllColdExactBatch(t.Context(), func(
		ctx context.Context,
		batch ColdExactBatch,
	) error {
		owner := exactBatchQueryByKey(batch, ownerKey)
		value, observation, err := owner.QueryWithExactObservation(ctx, sourceKey)
		if err != nil {
			return err
		}
		oldInputs = append([]InputRevision(nil), observation.inputs...)
		if err := exactBatchQueryByKey(batch, siblingKey).ObserveExactQuery(observation); err != nil {
			return err
		}
		for _, key := range []QueryKey{ownerKey, siblingKey} {
			if _, err := exactBatchQueryByKey(batch, key).Complete(string(value)); err != nil {
				return err
			}
		}
		return nil
	}, ownerKey, siblingKey)
	if err != nil {
		t.Fatalf("first EvaluateAllColdExactBatch() error = %v", err)
	}
	mustCommit(t, first)

	second := mustColdSession(t, graph, exactInput(inputKey, "revision-2", "same-value"))
	_, err = second.EvaluateAllColdExactBatch(t.Context(), func(
		ctx context.Context,
		batch ColdExactBatch,
	) error {
		owner := exactBatchQueryByKey(batch, ownerKey)
		_, observation, err := owner.QueryWithExactObservation(ctx, sourceKey)
		if err != nil {
			return err
		}
		poison := observation
		poison.inputs = oldInputs
		return exactBatchQueryByKey(batch, siblingKey).ObserveExactQuery(poison)
	}, ownerKey, siblingKey)
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("ABA observation error = %v, want %v", err, ErrRevisionConflict)
	}
	if graph.Generation() != 1 {
		t.Fatalf("failed ABA observation changed generation to %d", graph.Generation())
	}
	if value := stringValue(t, graph, sourceKey); value != "same-value" {
		t.Fatalf("committed source value = %q", value)
	}
}

func exactQueryObservationGraph(
	t *testing.T,
	inputKey InputKey,
	sourceKeys ...QueryKey,
) *Graph {
	t.Helper()
	definitions := make([]Definition, len(sourceKeys))
	for index, key := range sourceKeys {
		run := func(context.Context, Reader) ([]byte, error) { return nil, nil }
		if strings.HasPrefix(key.Opaque(), "source") {
			run = func(_ context.Context, reader Reader) ([]byte, error) {
				input, err := reader.ExactInput(inputKey)
				return input.Value, err
			}
		}
		definitions[index] = Definition{Key: key, Run: run}
	}
	return mustGraph(t, definitions...)
}

func exactBatchQueryByKey(batch ColdExactBatch, key QueryKey) ColdExactBatchQuery {
	for index := range batch.Len() {
		query := batch.Query(index)
		if query.Key() == key {
			return query
		}
	}
	return ColdExactBatchQuery{}
}

func assertExactQueryObservationDependency(
	t *testing.T,
	entry nodeEntry,
	queryKey QueryKey,
	input InputRevision,
) {
	t.Helper()
	wantDependency := dependency{key: queryDep(queryKey), changedAt: entry.changedAt}
	if len(entry.deps) != 1 || entry.deps[0] != wantDependency {
		t.Fatalf("dependencies = %#v, want %#v", entry.deps, wantDependency)
	}
	if len(entry.inputs) != 1 || entry.inputs[0] != input {
		t.Fatalf("transitive inputs = %#v, want %#v", entry.inputs, input)
	}
}
