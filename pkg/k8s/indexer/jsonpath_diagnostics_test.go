package indexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONPathExecutionErrorKeepsResourceValuesPrivate(t *testing.T) {
	expression := "spec.credential[?(@.active==true)]"
	evaluator, err := NewJSONPathEvaluator(expression)
	require.NoError(t, err)
	_, err = evaluator.Evaluate(map[string]any{
		"spec": map[string]any{"credential": "private-resource-value"},
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "private-resource-value")
	assert.Contains(t, err.Error(), expression)
	var evaluationError *JSONPathError
	require.ErrorAs(t, err, &evaluationError)
	require.NotNil(t, evaluationError.Unwrap())
	assert.ErrorIs(t, err, evaluationError.Cause)
}
