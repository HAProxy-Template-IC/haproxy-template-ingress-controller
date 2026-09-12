package incremental

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"testing"
)

func TestReplacementRetirementPreservesVerificationInputs(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			testReplacementRetirementVerification(t, prepared)
		})
	}
}

func testReplacementRetirementVerification(t *testing.T, prepared bool) {
	t.Helper()
	used := exactInput(NewInputKey("used"), "used-revision", "kept")
	unused := exactInput(NewInputKey("unused"), "unused-revision", "retired")
	query := NewQueryKey("query")
	graph := mustRetiringGraph(t, Definition{Key: query, Run: readInputQuery(used.Key)})
	session, err := graph.BeginColdResetWithResolver(failingResolver(t), used, unused)
	if err != nil {
		t.Fatalf("BeginColdResetWithResolver() error = %v", err)
	}
	mustEvaluate(t, session, query)
	calls := 0
	verify := func(_ context.Context, observations []InputRevision) (bool, error) {
		calls++
		return verifyRetirementTransactionInputs(session, observations, used, unused)
	}
	if prepared {
		draft, err := session.PrepareGraphCommit(t.Context())
		if err != nil {
			t.Fatalf("PrepareGraphCommit() error = %v", err)
		}
		if err := draft.Publish(t.Context(), verify); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
	} else if err := session.Commit(t.Context(), verify); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("verifier calls = %d, want 1", calls)
	}
	assertCommittedInputs(t, graph, used.Key)
	retired := session.RetiredInputs()
	if len(retired) != 1 || retired[0] != unused.Key {
		t.Fatalf("retired inputs = %v, want unused input", retired)
	}
}

func verifyRetirementTransactionInputs(session *Session, observations []InputRevision, inputs ...Input) (bool, error) {
	if len(observations) != len(inputs) {
		return false, fmt.Errorf("observations = %d, want %d", len(observations), len(inputs))
	}
	for _, expected := range inputs {
		if !slices.Contains(observations, revisionOf(expected)) {
			return false, fmt.Errorf("verification observation %q missing or changed", expected.Key.Opaque())
		}
		actual, exists, err := session.ExactInput(expected.Key)
		if err != nil || !exists || actual.Revision != expected.Revision ||
			actual.Found != expected.Found || !bytes.Equal(actual.Value, expected.Value) {
			return false, fmt.Errorf("verification input %q missing or changed: exists=%t, error=%v", expected.Key.Opaque(), exists, err)
		}
		matched, err := session.MatchesExactInput(expected)
		if err != nil || !matched {
			return false, fmt.Errorf("verification input %q does not match: %v", expected.Key.Opaque(), err)
		}
	}
	return true, nil
}
