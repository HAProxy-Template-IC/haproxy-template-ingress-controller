package incremental_test

import (
	"context"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

func TestColdExactBatchPublicReaderIntegration(t *testing.T) {
	inputKey := incremental.NewInputKey("input")
	queryKey := incremental.NewQueryKey("query")
	graph, err := incremental.New(incremental.Definition{
		Key: queryKey,
		Run: func(context.Context, incremental.Reader) ([]byte, error) {
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	session, err := graph.BeginColdReset(incremental.Input{
		Key: inputKey, Revision: incremental.NewRevision("revision"), Found: true, Value: []byte("value"),
	})
	if err != nil {
		t.Fatalf("BeginColdReset() error = %v", err)
	}
	results, err := session.EvaluateAllColdExactBatch(t.Context(), func(
		_ context.Context,
		batch incremental.ColdExactBatch,
	) error {
		query := batch.Query(0)
		var reader incremental.Reader = query
		value, found, readErr := reader.Input(inputKey)
		if readErr != nil || !found {
			return readErr
		}
		_, completeErr := query.Complete(string(value))
		return completeErr
	}, queryKey)
	if err != nil {
		t.Fatalf("EvaluateAllColdExactBatch() error = %v", err)
	}
	if len(results) != 1 || results[0].Key != queryKey {
		t.Fatalf("results = %#v", results)
	}
	if err := graph.ValidateExactValue(queryKey, results[0].Value); err != nil {
		t.Fatalf("ValidateExactValue() error = %v", err)
	}
}

func TestColdExactBatchPublicCompleteWaveIntegration(t *testing.T) {
	left := incremental.NewQueryKey("left")
	right := incremental.NewQueryKey("right")
	graph, err := incremental.New(
		incremental.Definition{
			Key: left,
			Run: func(context.Context, incremental.Reader) ([]byte, error) {
				return nil, nil
			},
		},
		incremental.Definition{
			Key: right,
			Run: func(context.Context, incremental.Reader) ([]byte, error) {
				return nil, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	session, err := graph.BeginColdReset()
	if err != nil {
		t.Fatalf("BeginColdReset() error = %v", err)
	}
	results, err := session.EvaluateAllColdExactBatch(t.Context(), func(
		_ context.Context,
		batch incremental.ColdExactBatch,
	) error {
		_, completeErr := batch.CompleteWave(
			incremental.ColdExactBatchValue{Index: 0, Key: left, Value: "left"},
			incremental.ColdExactBatchValue{Index: 1, Key: right, Value: "right"},
		)
		return completeErr
	}, right, left)
	if err != nil {
		t.Fatalf("EvaluateAllColdExactBatch() error = %v", err)
	}
	if len(results) != 2 || results[0].Key != left || results[1].Key != right {
		t.Fatalf("results = %#v", results)
	}
	for _, result := range results {
		if err := graph.ValidateExactValue(result.Key, result.Value); err != nil {
			t.Fatalf("ValidateExactValue(%q) error = %v", result.Key.Opaque(), err)
		}
	}
}
