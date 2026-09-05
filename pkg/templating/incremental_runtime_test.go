// Copyright 2026 Philipp Hossner
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
	"io"
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type noOpSharedContributionRecorder struct{}

func (*noOpSharedContributionRecorder) Unique(_, _, _ string) {}

func incrementalComponentContext(values map[string]any) map[string]any {
	contextValues := maps.Clone(values)
	if contextValues == nil {
		contextValues = make(map[string]any)
	}
	if _, exists := contextValues["item"]; !exists {
		contextValues["item"] = map[string]any{}
	}
	if _, exists := contextValues["source"]; !exists {
		contextValues["source"] = "test"
	}
	if _, exists := contextValues["props"]; !exists {
		contextValues["props"] = map[string]any{}
	}
	if _, exists := contextValues["renderSubject"]; !exists {
		contextValues["renderSubject"] = map[string]any{"mode": "reconcile"}
	}
	contextValues["shared"] = NewSharedContributionContext(&noOpSharedContributionRecorder{})
	return contextValues
}

func TestIncrementalComponentRejectsImmutableInputMutation(t *testing.T) {
	engine, err := New(map[string]string{
		"component": `{% item["value"] = "changed" %}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)
	item := map[string]any{"value": "original"}
	ctx := WithIncrementalImmutableInputs(t.Context(), item)

	_, err = engine.RenderIncrementalComponent(ctx, "component", incrementalComponentContext(map[string]any{"item": item}))
	require.ErrorContains(t, err, "mutates an immutable input")
	assert.Equal(t, "original", item["value"])
}

func TestIncrementalComponentRejectsControllerMapMutation(t *testing.T) {
	engine, err := New(map[string]string{
		"component": `{% controller["copy"] = controller["pods"] %}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)
	controller := map[string]ResourceStore{"pods": &mockResourceStore{}}

	_, err = engine.RenderIncrementalComponent(t.Context(), "component", incrementalComponentContext(map[string]any{
		"controller": controller,
	}))
	require.ErrorContains(t, err, "mutates an immutable input")
	assert.NotContains(t, controller, "copy")
}

func TestRenderIncrementalComponentRejectsOrdinaryTemplate(t *testing.T) {
	engine, err := New(map[string]string{"ordinary": "ordinary"}, &Options{
		EntryPoints: []string{"ordinary"},
	})
	require.NoError(t, err)

	_, err = engine.RenderIncrementalComponent(t.Context(), "ordinary", nil)
	require.EqualError(t, err, `template "ordinary" is not an incremental component`)
}

func TestRenderIncrementalComponentRequiresSharedContributionContext(t *testing.T) {
	engine, err := New(map[string]string{"component": "component"}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)

	_, err = engine.RenderIncrementalComponent(t.Context(), "component", nil)
	require.EqualError(t, err, `incremental component "component" requires a shared contribution context`)

	_, err = engine.RenderIncrementalComponent(t.Context(), "component", map[string]any{"shared": NewSharedContext()})
	require.EqualError(t, err, `incremental component "component" requires a shared contribution context`)
}

func TestRenderIncrementalComponentDerivesRenderModeFromRenderSubject(t *testing.T) {
	engine, err := New(map[string]string{"component": `{{ renderMode }}={{ renderSubject["mode"] }}`}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)

	for _, mode := range []string{"reconcile", "admission"} {
		values := incrementalComponentContext(map[string]any{
			"renderSubject": map[string]any{"mode": mode},
		})
		output, renderErr := engine.RenderIncrementalComponent(t.Context(), "component", values)
		require.NoError(t, renderErr)
		assert.Equal(t, mode+"="+mode, output)
		assert.NotContains(t, values, "renderMode")
	}
}

func TestRenderIncrementalComponentRejectsSuppliedOrInvalidRenderMode(t *testing.T) {
	engine, err := New(map[string]string{"component": `{{ renderMode }}`}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)

	supplied := incrementalComponentContext(map[string]any{"renderMode": "admission"})
	_, err = engine.RenderIncrementalComponent(t.Context(), "component", supplied)
	require.ErrorContains(t, err, "cannot supply derived renderMode")

	invalid := incrementalComponentContext(map[string]any{
		"renderSubject": map[string]any{"mode": "invalid"},
	})
	_, err = engine.RenderIncrementalComponent(t.Context(), "component", invalid)
	require.ErrorContains(t, err, "renderSubject.mode to be reconcile or admission")
}

func TestRenderIncrementalComponentRequiresStrictInputs(t *testing.T) {
	engine, err := New(map[string]string{"component": "component"}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)

	tests := map[string]struct {
		mutate func(map[string]any)
		want   string
	}{
		"missing source": {
			mutate: func(values map[string]any) { delete(values, "source") },
			want:   "requires a non-empty source string",
		},
		"empty source": {
			mutate: func(values map[string]any) { values["source"] = "" },
			want:   "requires a non-empty source string",
		},
		"invalid item": {
			mutate: func(values map[string]any) { values["item"] = []any{} },
			want:   "requires item to be an object",
		},
		"nil props": {
			mutate: func(values map[string]any) { values["props"] = map[string]any(nil) },
			want:   "requires props to be an object",
		},
		"missing render subject": {
			mutate: func(values map[string]any) { delete(values, "renderSubject") },
			want:   "requires renderSubject to be an object",
		},
		"invalid controller": {
			mutate: func(values map[string]any) { values["controller"] = map[string]any{} },
			want:   "requires controller to be a resource-store map",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			values := incrementalComponentContext(nil)
			test.mutate(values)
			_, err := engine.RenderIncrementalComponent(t.Context(), "component", values)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestIncrementalComponentAllowsLocalMutationAndSortsMapRanges(t *testing.T) {
	engine, err := New(map[string]string{
		"component": `{% var entries = map[string]string{"b": "2", "a": "1"} %}{% entries["c"] = "3" %}{% for key, value := range entries %}{{ key }}={{ value }} {% end %}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)

	for range 20 {
		output, err := engine.RenderIncrementalComponent(t.Context(), "component", incrementalComponentContext(nil))
		require.NoError(t, err)
		assert.Equal(t, "a=1 b=2 c=3 ", strings.TrimSuffix(output, "\n"))
	}
}

type testIncrementalRenderer struct {
	executor      IncrementalComponentExecutor
	base          map[string]any
	values        []any
	fragments     string
	reads         int
	fragmentReads int
}

type testCertifiedIncrementalRenderer struct {
	*testIncrementalRenderer
	certified *IncrementalCertifiedValues
}

func (r *testCertifiedIncrementalRenderer) IncrementalValuesCertified(
	_ context.Context,
	_, _ string,
) (*IncrementalCertifiedValues, error) {
	r.reads++
	return r.certified, nil
}

func (r *testIncrementalRenderer) RenderIncremental(ctx context.Context, _ string) (string, error) {
	componentContext := make(map[string]any, len(r.base)+1)
	for key, value := range r.base {
		componentContext[key] = value
	}
	componentContext["item"] = map[string]any{"metadata": map[string]any{"name": "route-a"}}
	return r.executor.RenderIncrementalComponent(ctx, "component", componentContext)
}

func (r *testIncrementalRenderer) IncrementalValues(_ context.Context, _, _ string) ([]any, error) {
	r.reads++
	return r.values, nil
}

func (r *testIncrementalRenderer) IncrementalRankedFragments(
	_ context.Context,
	_, _ string,
) (string, error) {
	r.fragmentReads++
	return r.fragments, nil
}

func (r *testIncrementalRenderer) IncrementalRankedFragmentsJoin(
	_ context.Context,
	_, _, delimiter string,
) (string, error) {
	r.fragmentReads++
	if r.fragments == "" {
		return "", nil
	}
	return r.fragments + delimiter + r.fragments, nil
}

func TestIncrementalRenderExecutesComponentEntryPoint(t *testing.T) {
	engine, err := New(map[string]string{
		"main":      `{{ incremental_render("route-lines") }}`,
		"component": `{{ dig(item, "metadata", "name") }}`,
	}, &Options{
		EntryPoints:            []string{"main", "component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)

	renderer := &testIncrementalRenderer{executor: engine, base: incrementalComponentContext(nil)}
	ctx := WithIncrementalRenderer(t.Context(), renderer)
	output, err := engine.Render(ctx, "main", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "route-a\n", output)
}

func TestIncrementalRenderRetainsTextFragmentForCapableSink(t *testing.T) {
	engine, err := New(map[string]string{
		"main": `before{{ incremental_render("route-lines") }}after`,
	}, nil)
	require.NoError(t, err)
	fragment := &testRetainedTextFragment{text: "middle"}
	renderer := &testTextFragmentRenderer{fragment: fragment}
	ctx := WithIncrementalRenderer(t.Context(), renderer)

	sink := &testTextFragmentSink{}
	_, err = engine.RenderRawTo(ctx, "main", map[string]any{}, sink)
	require.NoError(t, err)
	assert.Equal(t, []string{"text:before", "fragment", "text:after"}, sink.events)
	assert.Same(t, fragment, sink.fragment)
	assert.Equal(t, 0, fragment.writes)
	assert.Equal(t, 0, renderer.stringCalls)
	assert.Equal(t, 1, renderer.fragmentCalls)

	output, err := engine.Render(ctx, "main", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "beforemiddleafter\n", output)
	assert.Equal(t, 1, fragment.writes)
}

type testTextFragmentRenderer struct {
	fragment      TextFragment
	stringCalls   int
	fragmentCalls int
}

func (r *testTextFragmentRenderer) RenderIncremental(context.Context, string) (string, error) {
	r.stringCalls++
	return "fallback", nil
}

func (r *testTextFragmentRenderer) RenderIncrementalTextFragment(
	context.Context,
	string,
) (TextFragment, error) {
	r.fragmentCalls++
	return r.fragment, nil
}

type testRankedTextFragmentRenderer struct {
	*testIncrementalRenderer
	fragment            TextFragment
	joinedFragment      TextFragment
	fragmentReads       int
	joinedFragmentReads int
}

func (r *testRankedTextFragmentRenderer) IncrementalRankedTextFragment(
	context.Context,
	string,
	string,
) (TextFragment, error) {
	r.fragmentReads++
	return r.fragment, nil
}

func (r *testRankedTextFragmentRenderer) IncrementalRankedTextFragmentJoin(
	context.Context,
	string,
	string,
	string,
) (TextFragment, error) {
	r.joinedFragmentReads++
	return r.joinedFragment, nil
}

type testRetainedTextFragment struct {
	text   string
	writes int
}

func (f *testRetainedTextFragment) WriteTo(writer io.Writer) (int64, error) {
	f.writes++
	written, err := io.WriteString(writer, f.text)
	return int64(written), err
}

type testTextFragmentSink struct {
	events    []string
	fragment  TextFragment
	fragments []TextFragment
}

func (w *testTextFragmentSink) Write(value []byte) (int, error) {
	if len(value) > 0 {
		w.events = append(w.events, "text:"+string(value))
	}
	return len(value), nil
}

func (w *testTextFragmentSink) WriteTextFragment(fragment TextFragment) error {
	w.events = append(w.events, "fragment")
	w.fragment = fragment
	w.fragments = append(w.fragments, fragment)
	return nil
}

func TestIncrementalRenderRequiresTransaction(t *testing.T) {
	engine, err := New(map[string]string{"main": `{{ incremental_render("route-lines") }}`}, nil)
	require.NoError(t, err)

	_, err = engine.Render(t.Context(), "main", map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `incremental component "route-lines" has no render transaction`)
}

func TestIncrementalValuesIsRootOnlyAndGuardsReturnedValues(t *testing.T) {
	engine, err := New(map[string]string{
		"main": `{% var values = incremental_values("group", "cell") %}{{ values | toJSON() }}`,
	}, nil)
	require.NoError(t, err)
	value := map[string]any{"nested": map[string]any{"value": "original"}}
	renderer := &testIncrementalRenderer{values: []any{value}}
	ctx := WithIncrementalRenderer(t.Context(), renderer)
	output, err := engine.Render(ctx, "main", map[string]any{})
	require.NoError(t, err)
	assert.JSONEq(t, `[{"nested":{"value":"original"}}]`, strings.TrimSpace(output))
	assert.Equal(t, 1, renderer.reads)

	mutating, err := New(map[string]string{
		"main": `{% var values = incremental_values("group", "cell") %}{% values[0].(map[string]any)["nested"].(map[string]any)["value"] = "poison" %}`,
	}, nil)
	require.NoError(t, err)
	_, err = mutating.Render(ctx, "main", map[string]any{})
	require.ErrorContains(t, err, "mutates an immutable input")
	assert.Equal(t, "original", value["nested"].(map[string]any)["value"])

	for name, options := range map[string]*Options{
		"component": {
			EntryPoints:            []string{"private"},
			IncrementalEntryPoints: []string{"private"},
		},
		"binding": {
			EntryPoints:                   []string{"private"},
			IncrementalBindingEntryPoints: []string{"private"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, compileErr := New(map[string]string{
				"private": `{{ incremental_values("group", "cell") }}`,
			}, options)
			require.ErrorContains(t, compileErr, "incremental_values")
		})
	}
}

func TestIncrementalCertifiedValuesContainTemplateMutation(t *testing.T) {
	value := map[string]any{"nested": map[string]any{"value": "original"}}
	values := []any{value}
	renderer := &testCertifiedIncrementalRenderer{
		testIncrementalRenderer: &testIncrementalRenderer{},
		certified:               NewIncrementalCertifiedValues(values, CertifyIncrementalImmutableInputs(values)),
	}
	ctx := WithIncrementalRenderer(t.Context(), renderer)
	mutating, err := New(map[string]string{
		"main": `{% var values = incremental_values("group", "cell") %}{% values[0].(map[string]any)["nested"].(map[string]any)["value"] = "poison" %}`,
	}, nil)
	require.NoError(t, err)
	_, err = mutating.Render(ctx, "main", map[string]any{})
	require.ErrorContains(t, err, "mutates an immutable input")
	assert.Equal(t, "original", value["nested"].(map[string]any)["value"])

	reading, err := New(map[string]string{
		"main": `{% var values = incremental_values("group", "cell") %}{{ values | toJSON() }}`,
	}, nil)
	require.NoError(t, err)
	output, err := reading.Render(ctx, "main", map[string]any{})
	require.NoError(t, err)
	assert.JSONEq(t, `[{"nested":{"value":"original"}}]`, strings.TrimSpace(output))
	assert.Equal(t, 2, renderer.reads)

	copied := *renderer.certified
	renderer.certified = &copied
	_, err = reading.Render(ctx, "main", map[string]any{})
	require.ErrorContains(t, err, "invalid immutable certificate")
}

func TestIncrementalValuesRequiresTransaction(t *testing.T) {
	engine, err := New(map[string]string{
		"main": `{{ incremental_values("group", "cell") | toJSON() }}`,
	}, nil)
	require.NoError(t, err)

	_, err = engine.Render(t.Context(), "main", map[string]any{})
	require.ErrorContains(t, err, `incremental values "group"/"cell" have no render transaction`)
}

func TestIncrementalRankedFragmentsIsRootOnly(t *testing.T) {
	engine, err := New(map[string]string{
		"main": `{{ incremental_ranked_fragments("group", "lines") }}`,
	}, nil)
	require.NoError(t, err)
	renderer := &testIncrementalRenderer{fragments: "first\nsecond\n"}
	ctx := WithIncrementalRenderer(t.Context(), renderer)
	output, err := engine.Render(ctx, "main", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "first\nsecond\n", output)
	assert.Equal(t, 1, renderer.fragmentReads)

	for name, options := range map[string]*Options{
		"component": {
			EntryPoints:            []string{"private"},
			IncrementalEntryPoints: []string{"private"},
		},
		"binding": {
			EntryPoints:                   []string{"private"},
			IncrementalBindingEntryPoints: []string{"private"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, compileErr := New(map[string]string{
				"private": `{{ incremental_ranked_fragments("group", "lines") }}`,
			}, options)
			require.ErrorContains(t, compileErr, "incremental_ranked_fragments")
		})
	}
}

func TestIncrementalRankedFragmentsPreserveStringOperations(t *testing.T) {
	engine, err := New(map[string]string{
		"main": `{% var ranked = incremental_ranked_fragments("group", "lines") %}{% var joined = incremental_ranked_fragments_join("group", "documents", "|") %}{% if ranked == "legacy" && joined == "legacy|legacy" %}{{ "prefix:" + ranked + ":" + joined }}{% end %}`,
	}, nil)
	require.NoError(t, err)
	legacy := &testIncrementalRenderer{fragments: "legacy"}
	renderer := &testRankedTextFragmentRenderer{
		testIncrementalRenderer: legacy,
		fragment:                &testRetainedTextFragment{text: "retained"},
		joinedFragment:          &testRetainedTextFragment{text: "retained-join"},
	}
	ctx := WithIncrementalRenderer(t.Context(), renderer)

	output, err := engine.Render(ctx, "main", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "prefix:legacy:legacy|legacy\n", output)
	assert.Equal(t, 2, legacy.fragmentReads)
	assert.Zero(t, renderer.fragmentReads)
	assert.Zero(t, renderer.joinedFragmentReads)
}

func TestIncrementalRankedTextFragmentsPreferRetainedReaders(t *testing.T) {
	engine, err := New(map[string]string{
		"main":   `before{{ render "nested" }}after`,
		"nested": `{{- incremental_ranked_text_fragment("group", "lines") -}}between{{- incremental_ranked_text_fragment_join("group", "documents", "---") -}}`,
	}, &Options{EntryPoints: []string{"main"}})
	require.NoError(t, err)
	fragment := &testRetainedTextFragment{text: "ranked"}
	joinedFragment := &testRetainedTextFragment{text: "joined"}
	legacy := &testIncrementalRenderer{fragments: "legacy"}
	renderer := &testRankedTextFragmentRenderer{
		testIncrementalRenderer: legacy,
		fragment:                fragment,
		joinedFragment:          joinedFragment,
	}
	ctx := WithIncrementalRenderer(t.Context(), renderer)

	sink := &testTextFragmentSink{}
	_, err = engine.RenderRawTo(ctx, "main", map[string]any{}, sink)
	require.NoError(t, err)
	assert.Equal(t, []string{"text:before", "fragment", "text:between", "fragment", "text:after"}, sink.events)
	require.Len(t, sink.fragments, 2)
	assert.Same(t, fragment, sink.fragments[0])
	assert.Same(t, joinedFragment, sink.fragments[1])
	assert.Zero(t, fragment.writes)
	assert.Zero(t, joinedFragment.writes)
	assert.Zero(t, legacy.fragmentReads)

	output, err := engine.Render(ctx, "main", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "beforerankedbetweenjoinedafter\n", output)
	assert.Equal(t, 2, renderer.fragmentReads)
	assert.Equal(t, 2, renderer.joinedFragmentReads)
	assert.Equal(t, 1, fragment.writes)
	assert.Equal(t, 1, joinedFragment.writes)
}

func TestIncrementalRankedTextFragmentsWrapLegacyReaders(t *testing.T) {
	engine, err := New(map[string]string{
		"main": `{{ incremental_ranked_text_fragment("group", "lines") }}{{ incremental_ranked_text_fragment_join("group", "documents", "|") }}`,
	}, nil)
	require.NoError(t, err)
	renderer := &testIncrementalRenderer{fragments: "legacy"}
	ctx := WithIncrementalRenderer(t.Context(), renderer)
	sink := &testTextFragmentSink{}

	_, err = engine.RenderRawTo(ctx, "main", map[string]any{}, sink)
	require.NoError(t, err)
	require.Len(t, sink.fragments, 2)
	assert.Equal(t, textFragmentString("legacy"), sink.fragments[0])
	assert.Equal(t, textFragmentString("legacy|legacy"), sink.fragments[1])
	assert.Equal(t, 2, renderer.fragmentReads)
}

func TestIncrementalRankedTextFragmentsRejectNilRetainedResults(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     string
	}{
		{
			name:     "ranked",
			template: `{{ incremental_ranked_text_fragment("group", "lines") }}`,
			want:     `incremental ranked fragments "group"/"lines" returned a nil text fragment`,
		},
		{
			name:     "joined",
			template: `{{ incremental_ranked_text_fragment_join("group", "documents", "|") }}`,
			want:     `incremental ranked fragment join "group"/"documents" returned a nil text fragment`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, err := New(map[string]string{"main": test.template}, nil)
			require.NoError(t, err)
			renderer := &testRankedTextFragmentRenderer{
				testIncrementalRenderer: &testIncrementalRenderer{fragments: "legacy"},
			}
			ctx := WithIncrementalRenderer(t.Context(), renderer)
			sink := &testTextFragmentSink{}

			_, err = engine.RenderRawTo(ctx, "main", map[string]any{}, sink)
			require.ErrorContains(t, err, test.want)
			assert.Empty(t, sink.fragments)
			assert.Zero(t, renderer.testIncrementalRenderer.fragmentReads)
		})
	}
}

func TestIncrementalRankedTextFragmentsAreRootOnly(t *testing.T) {
	templates := map[string]string{
		"ranked": `{{ incremental_ranked_text_fragment("group", "lines") }}`,
		"joined": `{{ incremental_ranked_text_fragment_join("group", "documents", "|") }}`,
	}
	for name, source := range templates {
		for mode, options := range map[string]*Options{
			"component": {
				EntryPoints:            []string{"private"},
				IncrementalEntryPoints: []string{"private"},
			},
			"binding": {
				EntryPoints:                   []string{"private"},
				IncrementalBindingEntryPoints: []string{"private"},
			},
		} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				_, err := New(map[string]string{"private": source}, options)
				require.ErrorContains(t, err, "incremental_ranked_text_fragment")
			})
		}
	}
}

func TestIncrementalRankedFragmentsRequiresTransaction(t *testing.T) {
	engine, err := New(map[string]string{
		"main": `{{ incremental_ranked_fragments("group", "lines") }}`,
	}, nil)
	require.NoError(t, err)

	_, err = engine.Render(t.Context(), "main", map[string]any{})
	require.ErrorContains(t, err, `incremental ranked fragments "group"/"lines" have no render transaction`)
}

func TestIncrementalRankedFragmentsJoinIsRootOnly(t *testing.T) {
	engine, err := New(map[string]string{
		"main": `{{ incremental_ranked_fragments_join("group", "documents", "\n---\x00\n") }}`,
	}, nil)
	require.NoError(t, err)
	renderer := &testIncrementalRenderer{fragments: "document\n"}
	ctx := WithIncrementalRenderer(t.Context(), renderer)
	output, err := engine.Render(ctx, "main", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "document\n\n---\x00\ndocument\n", output)
	assert.Equal(t, 1, renderer.fragmentReads)

	for name, options := range map[string]*Options{
		"component": {
			EntryPoints:            []string{"private"},
			IncrementalEntryPoints: []string{"private"},
		},
		"binding": {
			EntryPoints:                   []string{"private"},
			IncrementalBindingEntryPoints: []string{"private"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, compileErr := New(map[string]string{
				"private": `{{ incremental_ranked_fragments_join("group", "documents", "---") }}`,
			}, options)
			require.ErrorContains(t, compileErr, "incremental_ranked_fragments_join")
		})
	}
}

func TestIncrementalRankedFragmentsJoinRequiresTransaction(t *testing.T) {
	engine, err := New(map[string]string{
		"main": `{{ incremental_ranked_fragments_join("group", "documents", "---") }}`,
	}, nil)
	require.NoError(t, err)

	_, err = engine.Render(t.Context(), "main", map[string]any{})
	require.ErrorContains(t, err, `incremental ranked fragment join "group"/"documents" has no render transaction`)
}
