package templating

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAppendIsTheGoBuiltin pins that templates get Go's own append, including
// the variadic spread. The engine used to shadow the name with a native, which
// silently cost `append(dst, src...)` — the spread is syntax the checker only
// applies to the builtin.
func TestAppendIsTheGoBuiltin(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     string
	}{
		{
			name:     "spread of the same slice type",
			template: `{% var a = []string{"x"} %}{% var b = []string{"y","z"} %}{{ join(append(a, b...), ",") }}`,
			want:     "x,y,z",
		},
		{
			name:     "spread of a pipeline result",
			template: `{% var a = []int{1} %}{% var b = []int{2,3,4} %}{{ len(append(a, b | filter(v => v > 2)...)) }}`,
			want:     "3",
		},
		{
			name:     "single-element append still works",
			template: `{% var a = []string{"x"} %}{{ join(append(a, "y"), ",") }}`,
			want:     "x,y",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := New(map[string]string{"t": tt.template}, nil)
			require.NoError(t, err)
			got, err := engine.Render(context.Background(), "t", nil)
			require.NoError(t, err)
			require.Equal(t, tt.want, strings.TrimSpace(got))
		})
	}
}

// TestAppendRejectsWideningSpread pins that a spread which would widen the
// element type is a compile error, not a reflect.AppendSlice panic mid-render.
// Boxing each element is what the explicit loop is for.
func TestAppendRejectsWideningSpread(t *testing.T) {
	_, err := New(map[string]string{
		"t": `{% var a = []any{} %}{% var b = []string{"x"} %}{{ len(append(a, b...)) }}`,
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "append")
}

// TestAppendAnyHandlesUntypedSlices pins the native that took over the cases
// Go's append cannot express: a nil slice, and a slice whose static type is
// `any` because it came out of a map[string]any.
func TestAppendAnyHandlesUntypedSlices(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     string
	}{
		{
			name:     "nil grows into a new slice",
			template: `{{ len(append_any(nil, "first")) }}`,
			want:     "1",
		},
		{
			name: "value read out of a map[string]any",
			template: `{% var m = map[string]any{"xs": []any{1}} %}` +
				`{% m["xs"] = append_any(m["xs"], 2) %}{{ len(m["xs"].([]any)) }}`,
			want: "2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := New(map[string]string{"t": tt.template}, nil)
			require.NoError(t, err)
			got, err := engine.Render(context.Background(), "t", nil)
			require.NoError(t, err)
			require.Equal(t, tt.want, strings.TrimSpace(got))
		})
	}
}
