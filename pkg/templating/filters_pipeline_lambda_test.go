package templating

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type lambdaPod struct {
	Name  string
	Ready bool
	IPs   []string
}

// TestPipelineLambdas covers `e => expr` against the real helpers: the
// parameter is typed from the stage's input, so field access inside the lambda
// is checked at compile time exactly as the written-out closure form is.
func TestPipelineLambdas(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     string
	}{
		{
			name:     "filter",
			template: `{{ len(pods | filter(p => p.Ready)) }}`,
			want:     "2",
		},
		{
			name:     "reject then map",
			template: `{{ join(pods | reject(p => p.Ready) | map(p => p.Name), ",") }}`,
			want:     "c",
		},
		{
			name:     "flat_map flattens one level",
			template: `{{ join(pods | flat_map(p => p.IPs), ",") }}`,
			want:     "10.0.0.1,10.0.0.2,10.0.0.3,10.0.0.4",
		},
		{
			name:     "unique_by with a key lambda",
			template: `{{ len(pods | unique_by(p => p.Ready)) }}`,
			want:     "2",
		},
		{
			name:     "group_by with a key lambda",
			template: `{{ len(keys(pods | group_by(p => p.Name))) }}`,
			want:     "3",
		},
		{
			// A lambda that reads an outer variable becomes a closure. The
			// checker types its body in a trial scope before the real pass, so
			// the capture has to survive being seen twice.
			name:     "captures an outer variable",
			template: `{% var min = 1 %}{{ len(pods | filter(p => len(p.IPs) > min)) }}`,
			want:     "1",
		},
		{
			name:     "captures an outer variable in a lowered chain",
			template: `{% var want = "a" %}{{ join(pods | filter(p => p.Name == want) | map(p => p.Name), ",") }}`,
			want:     "a",
		},
		{
			// The lambda's result type is whatever it returns, not the input's.
			name:     "result type follows the expression",
			template: `{{ join(pods | map(p => p.Name + "!"), ",") }}`,
			want:     "a!,b!,c!",
		},
	}

	pods := []lambdaPod{
		{Name: "a", Ready: true, IPs: []string{"10.0.0.1", "10.0.0.2"}},
		{Name: "b", Ready: true, IPs: []string{"10.0.0.3"}},
		{Name: "c", IPs: []string{"10.0.0.4"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := New(map[string]string{"t": tt.template}, &Options{
				Declarations: map[string]any{"pods": (*[]lambdaPod)(nil)},
			})
			require.NoError(t, err)

			got, err := engine.Render(context.Background(), "t", map[string]any{"pods": pods})
			require.NoError(t, err)
			require.Equal(t, tt.want, strings.TrimSpace(got))
		})
	}
}

// TestPipelineLambdaTypeErrorsAreCaught pins that inference does not cost the
// compile-time check that made closures worth preferring over string paths.
func TestPipelineLambdaTypeErrorsAreCaught(t *testing.T) {
	tests := map[string]string{
		"unknown field":       `{{ len(pods | filter(p => p.Redy)) }}`,
		"non-bool predicate":  `{{ len(pods | filter(p => p.Name)) }}`,
		"flat_map non-slice":  `{{ len(pods | flat_map(p => p.Name)) }}`,
		"lambda with no call": `{% var f = p => p.Ready %}{{ f }}`,
	}

	for name, tpl := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := New(map[string]string{"t": tpl}, &Options{
				Declarations: map[string]any{"pods": (*[]lambdaPod)(nil)},
			})
			require.Error(t, err)
		})
	}
}

// TestPipelineLambdaPreservesElementType pins that a chain written with arrows
// keeps its element type rather than degrading to []any: each stage reaches a
// field or operator only its own element type has.
func TestPipelineLambdaPreservesElementType(t *testing.T) {
	engine, err := New(map[string]string{
		"t": `{{ join(pods | filter(p => p.Ready) | flat_map(p => p.IPs) | map(ip => ip + "/32"), ",") }}`,
	}, &Options{Declarations: map[string]any{"pods": (*[]lambdaPod)(nil)}})
	require.NoError(t, err)

	got, err := engine.Render(context.Background(), "t", map[string]any{
		"pods": []lambdaPod{
			{Name: "a", Ready: true, IPs: []string{"10.0.0.1"}},
			{Name: "b", IPs: []string{"10.0.0.2"}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "10.0.0.1/32", strings.TrimSpace(got))
}

func TestElementLambdaParams(t *testing.T) {
	sliceOfPod := reflect.TypeOf([]lambdaPod{})

	tests := []struct {
		name     string
		argIndex int
		resolved []reflect.Type
		want     []reflect.Type
	}{
		{
			name:     "element type of the input slice",
			argIndex: 1,
			resolved: []reflect.Type{sliceOfPod, nil},
			want:     []reflect.Type{reflect.TypeOf(lambdaPod{})},
		},
		{
			name:     "not the closure argument",
			argIndex: 0,
			resolved: []reflect.Type{sliceOfPod, nil},
		},
		{
			name:     "input is not a slice",
			argIndex: 1,
			resolved: []reflect.Type{reflect.TypeOf(""), nil},
		},
		{
			name:     "input type unknown",
			argIndex: 1,
			resolved: []reflect.Type{nil, nil},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, elementLambdaParams(tt.argIndex, tt.resolved))
		})
	}
}
