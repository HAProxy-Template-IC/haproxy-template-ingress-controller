package typegen

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloneImmutableJSONPreservesValuesAndDetachesContainers(t *testing.T) {
	values := []any{nil, true, "value", int64(math.MaxInt64), uint64(math.MaxUint64), float64(1.25),
		map[string]any(nil), []any(nil), map[string]any{}, []any{}}
	source := map[string]any{"first": values, "second": values}
	cloned, err := CloneImmutableJSON(source)
	require.NoError(t, err)
	require.Equal(t, source, cloned)
	source["first"].([]any)[0] = "caller mutation"
	copyObject := cloned.(map[string]any)
	assert.Nil(t, copyObject["first"].([]any)[0])
	copyObject["first"].([]any)[1] = "read mutation"
	assert.Equal(t, true, copyObject["second"].([]any)[1])
	assert.Equal(t, true, source["first"].([]any)[1])
}

func TestCloneImmutableJSONRejectsUnsupportedValues(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	var deep any = "leaf"
	for range immutableJSONMaxDepth + 1 {
		deep = []any{deep}
	}
	for name, value := range map[string]any{
		"cycle":           cycle,
		"depth":           deep,
		"function":        func() {},
		"foreign map":     map[string]string{"key": "value"},
		"invalid text":    string([]byte{0xff}),
		"infinite number": math.Inf(1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := CloneImmutableJSON(value)
			require.Error(t, err)
		})
	}
}
