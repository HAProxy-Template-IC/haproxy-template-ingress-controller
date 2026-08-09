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
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pipelineRef / pipelineEP / pipelineSlice mirror the shape of a typed watched
// resource: an outer object whose nested types are reachable only as field
// types. They stand in for a typegen-produced EndpointSlice.
type pipelineRef struct {
	Name string `json:"name"`
}

type pipelineEP struct {
	TargetRef pipelineRef `json:"targetRef"`
	Addresses []string    `json:"addresses"`
	Ready     bool        `json:"ready"`
}

type pipelineSlice struct {
	Endpoints []pipelineEP `json:"endpoints"`
}

func pipelineFixture() []pipelineSlice {
	return []pipelineSlice{
		{Endpoints: []pipelineEP{
			{TargetRef: pipelineRef{Name: "pod-a"}, Addresses: []string{"10.0.0.1", "10.0.0.2"}, Ready: true},
			{TargetRef: pipelineRef{Name: ""}, Addresses: []string{"10.0.0.9"}, Ready: true},
		}},
		{Endpoints: []pipelineEP{
			{TargetRef: pipelineRef{Name: "pod-a"}, Addresses: []string{"10.0.0.1"}, Ready: true},
			{TargetRef: pipelineRef{Name: "pod-b"}, Addresses: []string{"10.0.0.3"}, Ready: false},
		}},
	}
}

// pipelineDeclarations binds the fixture and the names a template needs to
// write explicit closure types against it.
func pipelineDeclarations() map[string]any {
	return map[string]any{
		"eps":   (*[]pipelineSlice)(nil),
		"Slice": reflect.TypeOf(pipelineSlice{}),
		"EP":    reflect.TypeOf(pipelineEP{}),
	}
}

// renderPipeline compiles and renders tpl with the pipeline fixture bound to
// `eps`, plus the nested types registered under names a template can use.
func renderPipeline(t *testing.T, tpl string, data []pipelineSlice) (string, error) {
	t.Helper()
	engine, err := New(map[string]string{"t": tpl}, &Options{
		Declarations: pipelineDeclarations(),
	})
	if err != nil {
		return "", err
	}
	return engine.Render(context.Background(), "t", map[string]any{"eps": data})
}

func TestPipelineHelpers(t *testing.T) {
	tests := []struct {
		name     string
		template string
		data     []pipelineSlice
		want     string
	}{
		{
			name: "flat_map flattens one level and keeps the element type",
			template: `{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) %}` +
				`{% for _, e := range out %}{{ e.TargetRef.Name }};{% end %}`,
			data: pipelineFixture(),
			want: "pod-a;;pod-a;pod-b;",
		},
		{
			name: "reject drops matching elements",
			template: `{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`reject(func(e EP) bool { return e.TargetRef.Name == "" }) %}` +
				`{% for _, e := range out %}{{ e.TargetRef.Name }};{% end %}`,
			data: pipelineFixture(),
			want: "pod-a;pod-a;pod-b;",
		},
		{
			name: "filter keeps matching elements",
			template: `{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`filter(func(e EP) bool { return e.Ready }) %}{{ len(out) }}`,
			data: pipelineFixture(),
			want: "3",
		},
		{
			name: "unique_by keeps the first element per key",
			template: `{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`unique_by(func(e EP) string { return e.TargetRef.Name }) %}` +
				`{% for _, e := range out %}{{ e.TargetRef.Name }};{% end %}`,
			data: pipelineFixture(),
			want: "pod-a;;pod-b;",
		},
		{
			name: "unique deduplicates whole elements in input order",
			template: `{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`flat_map(func(e EP) []string { return e.Addresses }) | unique() %}{{ join(out, ",") }}`,
			data: pipelineFixture(),
			want: "10.0.0.1,10.0.0.2,10.0.0.9,10.0.0.3",
		},
		{
			name: "unique_by accepts an attribute path as well as a closure",
			template: `{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`unique_by("targetRef.name") %}{% for _, e := range out %}{{ e.TargetRef.Name }};{% end %}`,
			data: pipelineFixture(),
			want: "pod-a;;pod-b;",
		},
		{
			name: "group_by accepts an attribute path, preserving the element type",
			template: `{% var g = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`group_by("targetRef.name") %}` +
				`{% for _, k := range keys(g) %}{{ k }}={{ len(g[k]) }};{% end %}`,
			data: pipelineFixture(),
			want: "=1;pod-a=2;pod-b=1;",
		},
		{
			name: "a dotted attribute path navigates levels rather than matching one literal key",
			template: `{% var g = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`group_by("targetRef.name") %}{{ len(keys(g)) }}`,
			data: pipelineFixture(),
			want: "3",
		},
		{
			name: "group_by buckets by key with deterministic iteration via keys",
			template: `{% var g = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`group_by(func(e EP) string { return e.TargetRef.Name }) %}` +
				`{% for _, k := range keys(g) %}{{ k }}={{ len(g[k]) }};{% end %}`,
			data: pipelineFixture(),
			want: "=1;pod-a=2;pod-b=1;",
		},
		{
			name: "closures may take any and use dig",
			template: `{% var out = eps | flat_map(func(s any) []EP { return s.(Slice).Endpoints }) | ` +
				`reject(func(e any) bool { return tostring(e | dig("targetRef", "name")) == "" }) %}{{ len(out) }}`,
			data: pipelineFixture(),
			want: "3",
		},
		{
			name: "empty input yields empty output",
			template: `{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`reject(func(e EP) bool { return false }) %}{{ len(out) }}`,
			data: []pipelineSlice{},
			want: "0",
		},
		{
			name:     "nil inner slice contributes nothing",
			template: `{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) %}{{ len(out) }}`,
			data:     []pipelineSlice{{Endpoints: nil}},
			want:     "0",
		},
		{
			name: "map applies element-wise and takes its type from the closure",
			template: `{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`map(func(e EP) string { return e.TargetRef.Name }) %}{{ join(out, ",") }}`,
			data: pipelineFixture(),
			want: "pod-a,,pod-a,pod-b",
		},
		{
			name: "map preserves length where flat_map concatenates",
			template: `{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`map(func(e EP) int { return len(e.Addresses) }) %}{{ len(out) }}`,
			data: pipelineFixture(),
			want: "4",
		},
		{
			name:     "map over empty input yields empty output",
			template: `{% var out = eps | map(func(s Slice) string { return "x" }) %}{{ len(out) }}`,
			data:     []pipelineSlice{},
			want:     "0",
		},
		{
			name: "a map type still parses alongside a map call",
			template: `{% var seen = map[string]bool{"a": true} %}` +
				`{% var out = eps | map(func(s Slice) int { return len(s.Endpoints) }) %}` +
				`{{ len(out) }}{{ seen["a"] }}`,
			data: pipelineFixture(),
			want: "2true",
		},
		{
			name: "full pod-names pipeline",
			template: `{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`reject(func(e EP) bool { return e.TargetRef.Name == "" }) | ` +
				`flat_map(func(e EP) []string { return e.Addresses }) | unique() %}{{ join(out, ",") }}`,
			data: pipelineFixture(),
			want: "10.0.0.1,10.0.0.2,10.0.0.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := renderPipeline(t, tt.template, tt.data)
			require.NoError(t, err)
			assert.Equal(t, tt.want, strings.TrimRight(got, "\n"))
		})
	}
}

// The point of closures over attribute strings: a wrong field name is a
// compile error, where the string form of the same mistake renders empty.
func TestPipelineClosureCatchesFieldTypos(t *testing.T) {
	_, err := renderPipeline(t,
		`{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpointz }) %}{{ len(out) }}`,
		pipelineFixture())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Endpointz undefined")

	_, err = renderPipeline(t,
		`{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | `+
			`reject(func(e EP) bool { return e.TargetRef.NameZ == "" }) %}{{ len(out) }}`,
		pipelineFixture())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NameZ undefined")
}

func TestPipelineTypePreservation(t *testing.T) {
	// The static type of each stage is what makes typed field access work
	// downstream; assert it directly rather than inferring it from output.
	epSlice := reflect.TypeOf([]pipelineEP{})

	got, err := identityReturnType([]reflect.Type{epSlice})
	require.NoError(t, err)
	assert.Equal(t, epSlice, got, "filter/reject/unique preserve the input slice type")

	flatMapFn := reflect.TypeOf(func(pipelineSlice) []pipelineEP { return nil })
	got, err = scriggoFlatMapAdaptive.ReturnType([]reflect.Type{
		reflect.TypeOf([]pipelineSlice{}), flatMapFn,
	})
	require.NoError(t, err)
	assert.Equal(t, epSlice, got, "flat_map takes its element type from the closure")

	got, err = scriggoGroupByAdaptive.ReturnType([]reflect.Type{
		epSlice, reflect.TypeOf(func(pipelineEP) string { return "" }),
	})
	require.NoError(t, err)
	assert.Equal(t, reflect.MapOf(reflect.TypeOf(""), epSlice), got)
}

func TestPipelineReturnTypeFallbacks(t *testing.T) {
	// An untyped or non-slice first argument degrades to `any` rather than
	// failing the type check: chart code reaches values through shared.Get
	// and dig, both of which are statically `any`.
	got, err := identityReturnType([]reflect.Type{nil})
	require.NoError(t, err)
	assert.Equal(t, anyType, got)

	got, err = identityReturnType([]reflect.Type{reflect.TypeOf("")})
	require.NoError(t, err)
	assert.Equal(t, anyType, got)

	// flat_map cannot fall back — without a closure result type there is no
	// element type to promise, so it must reject the call site.
	_, err = scriggoFlatMapAdaptive.ReturnType([]reflect.Type{reflect.TypeOf([]any{}), reflect.TypeOf("")})
	require.Error(t, err)

	_, err = scriggoFlatMapAdaptive.ReturnType([]reflect.Type{
		reflect.TypeOf([]any{}), reflect.TypeOf(func(any) any { return nil }),
	})
	require.Error(t, err, "a non-slice closure result must be rejected")
}

func TestPipelineRuntimeGuards(t *testing.T) {
	tests := []struct {
		name    string
		call    func()
		wantMsg string
	}{
		{
			name:    "predicate must return bool",
			call:    func() { selectMatching(FuncFilter, []int{1}, func(int) int { return 0 }, true) },
			wantMsg: "predicate must return bool",
		},
		{
			name:    "predicate must be a function",
			call:    func() { selectMatching(FuncFilter, []int{1}, "nope", true) },
			wantMsg: "must be a function",
		},
		{
			name:    "wrong arity is named",
			call:    func() { funcArg(FuncUniqueBy, func(int, int) int { return 0 }) },
			wantMsg: "must take 1 argument",
		},
		{
			name:    "uncomparable key is reported, not panicked opaquely",
			call:    func() { dedupe(FuncUniqueBy, []int{1}, func(int) []string { return nil }) },
			wantMsg: "not comparable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				require.NotNil(t, r, "expected a panic naming the offending stage")
				assert.Contains(t, r.(string), tt.wantMsg)
			}()
			tt.call()
		})
	}
}

func TestSortByCallShapes(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     string
	}{
		{
			name: "criteria form still sorts by JSONPath",
			template: `{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`sort_by([]string{"$.targetRef.name"}) %}` +
				`{% for _, e := range out %}{{ e.TargetRef.Name }};{% end %}`,
			want: ";pod-a;pod-a;pod-b;",
		},
		{
			name: "criteria may arrive as []any, which is what append produces",
			template: `{% var c = append([]any{}, "$.targetRef.name") %}` +
				`{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | sort_by(c) %}` +
				`{% for _, e := range out %}{{ e.TargetRef.Name }};{% end %}`,
			want: ";pod-a;pod-a;pod-b;",
		},
		{
			name: "comparator form sorts and keeps the element type",
			template: `{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`sort_by(func(a EP, b EP) int { if a.TargetRef.Name > b.TargetRef.Name { return -1 }; return 1 }) %}` +
				`{% for _, e := range out %}{{ e.TargetRef.Name }};{% end %}`,
			want: "pod-b;pod-a;pod-a;;",
		},
		{
			name: "comparator is stable, so equal elements keep input order",
			template: `{% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
				`sort_by(func(a EP, b EP) int { return 0 }) %}` +
				`{% for _, e := range out %}{{ e.TargetRef.Name }};{% end %}`,
			want: "pod-a;;pod-a;pod-b;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := renderPipeline(t, tt.template, pipelineFixture())
			require.NoError(t, err)
			assert.Equal(t, tt.want, strings.TrimRight(got, "\n"))
		})
	}
}

func TestSortByRejectsUnusableSecondArgument(t *testing.T) {
	// Neither criteria nor comparator must fail loudly. Falling through to
	// one branch or the other would sort by something the author did not ask
	// for, and a wrongly ordered map file is invisible until it reloads.
	sorter := sortByAdaptive(func() bool { return false })
	impl := sorter.Impl.(func(any, any) (any, error))

	_, err := impl([]string{"b", "a"}, 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be []string criteria or func(a, b T) int")

	_, err = impl([]string{"b", "a"}, nil)
	require.Error(t, err)

	_, err = impl([]string{"b", "a"}, func(_, _ string) string { return "" })
	require.Error(t, err, "a comparator must return int")

	// A mixed []any is criteria only if every element is a string.
	_, err = impl([]string{"b", "a"}, []any{"$.x", 7})
	require.Error(t, err)
}

func TestSortByPassesThroughNonSlice(t *testing.T) {
	sorter := sortByAdaptive(func() bool { return false })
	impl := sorter.Impl.(func(any, any) (any, error))
	got, err := impl(nil, []string{"$.x"})
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestPipelineNonSliceInputPassesThrough(t *testing.T) {
	// A nil optional field must behave as an empty collection: aborting the
	// render for an absent field would make every pipeline stage a guard.
	assert.Nil(t, selectMatching(FuncFilter, nil, func(any) bool { return true }, true))
	assert.Equal(t, []pipelineEP{}, scriggoFlatMapAdaptive.Impl.(func(any, any) any)(
		nil, func(pipelineSlice) []pipelineEP { return nil }))
	assert.Nil(t, dedupe(FuncUnique, nil, nil))
}
