package templating

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// TestMacroAndPipelineInterop pins what composes with a pipeline. A macro
// returns text, so it can consume a chain or serve as a stage closure but
// cannot pass a collection onward — a shared helper returning a collection is
// an exported func var instead, imported the same way.
func TestMacroAndPipelineInterop(t *testing.T) {
	tests := []struct {
		name      string
		templates map[string]string
		want      string
	}{
		{
			// A macro returns a string, so it can end a chain but not sit in
			// the middle of one.
			name: "macro terminates a pipe",
			templates: map[string]string{
				"lib": `{% macro Render(names []string) string %}{{ join(names, ",") }}{% end %}`,
				"t": `{% import "lib" for Render %}` +
					`{{ pods | filter(p => p.Ready) | map(p => p.Name) | Render() }}`,
			},
			want: "a,b",
		},
		{
			name: "macro as a stage closure",
			templates: map[string]string{
				"lib": `{% macro Label(p Pod) string %}{{ p.Name }}!{% end %}`,
				"t": `{% import "lib" for Label %}` +
					`{{ join(pods | map(Label), ",") }}`,
			},
			want: "a!,b!,c!",
		},
		{
			// The "all endpoints for a service" shape: a helper returning a
			// typed slice, not text.
			name: "imported func returning a typed slice feeds a chain",
			templates: map[string]string{
				"lib": `{% var ReadyIPs = func(ps []Pod) []string {` +
					` return ps | filter(p => p.Ready) | flat_map(p => p.IPs) } %}`,
				"t": `{% import "lib" for ReadyIPs %}` +
					`{{ join(ReadyIPs(pods) | unique(), ",") }}`,
			},
			want: "10.0.0.1,10.0.0.3",
		},
		{
			name: "helper returning a map of slices",
			templates: map[string]string{
				"lib": `{% var ByReady = func(ps []Pod) map[string][]Pod {` +
					` return ps | group_by(p => tostring(p.Ready)) } %}`,
				"t": `{% import "lib" for ByReady %}` +
					`{{ len(ByReady(pods)["true"]) }}`,
			},
			want: "2",
		},
		{
			name: "lambda passed to a user-defined higher-order func",
			templates: map[string]string{
				"lib": `{% var Where = func(ps []Pod, pred func(Pod) bool) []Pod {` +
					` return ps | filter(pred) } %}`,
				"t": `{% import "lib" for Where %}{{ len(Where(pods, p => p.Ready)) }}`,
			},
			want: "2",
		},
	}

	pods := []lambdaPod{
		{Name: "a", Ready: true, IPs: []string{"10.0.0.1"}},
		{Name: "b", Ready: true, IPs: []string{"10.0.0.3", "10.0.0.1"}},
		{Name: "c", IPs: []string{"10.0.0.9"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := New(tt.templates, &Options{
				Declarations: map[string]any{
					"pods": (*[]lambdaPod)(nil),
					"Pod":  reflect.TypeOf(lambdaPod{}),
				},
			})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got, err := engine.Render(context.Background(), "t", map[string]any{"pods": pods})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if strings.TrimSpace(got) != tt.want {
				t.Fatalf("got %q, want %q", strings.TrimSpace(got), tt.want)
			}
		})
	}
}
