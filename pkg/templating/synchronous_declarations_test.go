// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package templating

import (
	"context"
	"fmt"
	"testing"

	"github.com/haproxy-haptic/scriggo"
	"github.com/haproxy-haptic/scriggo/native"
	"github.com/stretchr/testify/require"
)

func TestBuiltInDeclarationsPermitExecutionReuse(t *testing.T) {
	for _, source := range []string{
		`{{ dig(item, "value") }}`,
		`{{ pathResolver.GetPath("hosts.map", "map") }}`,
		`{% var sorted, _ = sort_by([]map[string]any{{"name": "b"}, {"name": "a"}}, []string{"$.name"}) %}{{ len(sorted) }}`,
		`{{ incremental_render("group") }}`,
		`{{ incremental_ranked_text_fragment("group", "cell") }}`,
		`{{ incremental_ranked_text_fragment_join("group", "cell", ",") }}`,
		`{{ incremental_ranked_fragments("group", "cell") }}`,
		`{{ incremental_ranked_fragments_join("group", "cell", ",") }}`,
		`{{ incremental_ranked_fragment_bytes("group", "cell") }}`,
		`{{ incremental_value_count("group", "cell") }}`,
		`{{ len(incremental_values("group", "cell")) }}`,
	} {
		t.Run(source, func(t *testing.T) {
			engine, err := New(map[string]string{"main": source}, nil)
			require.NoError(t, err)
			require.True(t, engine.compiledTemplates["main"].BatchSafe())
		})
	}
}

type executionContextKey struct{}

type retainingIncrementalRenderer struct {
	contexts []context.Context
}

func (r *retainingIncrementalRenderer) RenderIncremental(ctx context.Context, _ string) (string, error) {
	r.contexts = append(r.contexts, ctx)
	return ctx.Value(executionContextKey{}).(string), nil
}

func TestIncrementalRootReuseIsolatesRetainedContexts(t *testing.T) {
	engine, err := New(map[string]string{"main": `{{ incremental_render("group") }}`}, nil)
	require.NoError(t, err)
	require.True(t, engine.compiledTemplates["main"].BatchSafe())
	for worker := range 16 {
		t.Run(fmt.Sprint(worker), func(t *testing.T) {
			t.Parallel()
			renderer := &retainingIncrementalRenderer{}
			for iteration := range 20 {
				want := fmt.Sprintf("%d/%d", worker, iteration)
				ctx := WithIncrementalRenderer(context.WithValue(t.Context(), executionContextKey{}, want), renderer)
				output, err := engine.Render(ctx, "main", map[string]any{})
				require.NoError(t, err)
				require.Equal(t, want+"\n", output)
			}
			for iteration, ctx := range renderer.contexts {
				require.Equal(t, fmt.Sprintf("%d/%d", worker, iteration), ctx.Value(executionContextKey{}))
			}
		})
	}
}

func TestIncrementalRootOverrideDoesNotInheritExecutionReuse(t *testing.T) {
	decl := buildScriggoGlobals(nil, nil, map[string]any{
		FuncIncrementalRender: func(string) TextFragment { return textFragmentString("custom") },
	})
	compiled, err := scriggo.BuildTemplate(scriggo.Files{"main": []byte(`{{ incremental_render("group") }}`)}, "main", &scriggo.BuildOptions{Globals: decl})
	require.NoError(t, err)
	require.False(t, compiled.BatchSafe())
}

func TestCustomDeclarationsDoNotInheritExecutionReuse(t *testing.T) {
	customFunction := func(...any) (any, error) { return "custom", nil }
	customFilter := func(any, ...any) (any, error) { return "custom", nil }
	for name, declarations := range map[string]native.Declarations{
		"function":    buildScriggoGlobals(nil, map[string]GlobalFunc{FuncDig: customFunction}, nil),
		"filter":      buildScriggoGlobals(map[string]FilterFunc{FuncDig: customFilter}, nil, nil),
		"declaration": buildScriggoGlobals(nil, nil, map[string]any{FuncDig: customFunction}),
	} {
		t.Run(name, func(t *testing.T) {
			compiled, err := scriggo.BuildTemplate(scriggo.Files{"main": []byte(`{{ dig(item, "value") }}`)}, "main", &scriggo.BuildOptions{Globals: declarations})
			require.NoError(t, err)
			require.False(t, compiled.BatchSafe())
		})
	}
}
