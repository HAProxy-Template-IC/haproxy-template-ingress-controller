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

// Scriggo-template-level contract tests for the typed top-level-
// globals iteration the chart's typed-watched-resources migration
// depends on.
//
// Pins the four layers a chart conversion from
//
//	{%- for _, gw := range resources.gateways.List() %}    // untyped map
//
// to
//
//	{%- for _, gw := range gateways %}                     // typed *[]*Gateway
//
// has to traverse:
//
//   - top-level globals declared via the typed-nil pointer pattern
//     in [BuildEngineDeclarations] / [addTypedResources];
//   - `for _, gw := range gateways` iterating a *[]*Gateway from
//     [typegen.WrapSlice];
//   - helper macro signatures (`macro X(r any) string`) receiving the
//     typed pointer through an `any` parameter;
//   - dig / toSlice / range chains inside the macro body and the
//     calling snippet.
//
// Each test compiles the actual chart pattern as a small template
// and asserts the rendered output matches the equivalent untyped
// pattern. Any regression at any of the four layers lights up here
// — the chart migration will keep working as long as these tests
// stay green.

package templating

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renderWithTypedGlobal compiles a template that declares a typed
// `gateways` top-level global (the production wiring), then renders
// it with a runtime *[]*Gateway value. Mirrors what
// `pkg/controller/rendercontext/builder.go::addTypedResources` does
// in production.
func renderWithTypedGlobal(t *testing.T, template string, gatewaysValue any, gwType reflect.Type) string {
	t.Helper()

	// Declare the typed global the way BuildEngineDeclarations does:
	// a typed-nil pointer to the slice type, so Scriggo sees the
	// shape at compile time but the value is provided at runtime.
	ptrSliceType := reflect.PointerTo(reflect.SliceOf(reflect.PointerTo(gwType)))
	typedNil := reflect.New(ptrSliceType).Elem().Interface()

	engine, err := NewScriggoWithDeclarations(
		map[string]string{"test": template},
		[]string{"test"},
		nil, nil, nil,
		map[string]any{"gateways": typedNil},
	)
	require.NoError(t, err, "engine must compile the test template against the typed `gateways` global declaration")

	out, err := engine.Render(context.Background(), "test", map[string]any{
		"gateways": gatewaysValue,
	})
	require.NoError(t, err, "render must succeed for the typed-iteration pattern")
	return out
}

// renderWithUntypedResources compiles the equivalent template that
// iterates resources.gateways.List() (the pre-conversion shape), so
// the typed and untyped renders can be compared output-vs-output.
//
// "Untyped" here is relative to the element shape only — the OUTER
// `resources` global is still declared as the typed struct that the
// engine demands (the previous default `map[string]ResourceStore`
// declaration was removed). The per-resource store wrapper inside
// it returns `[]any` / `any`, which matches what the chart sees
// when typebootstrap couldn't generate a typed element.
func renderWithUntypedResources(t *testing.T, template string, gateways []any) string {
	t.Helper()

	stores := map[string]ResourceStore{
		"gateways": &staticResourceStore{items: gateways},
	}
	names := resourceNames(stores)

	engine, err := NewScriggoWithDeclarations(
		map[string]string{"test": template},
		[]string{"test"},
		nil, nil, nil,
		typedResourcesDecl(names...),
	)
	require.NoError(t, err, "engine must compile the test template against the typed `resources` global declaration")
	_ = engine.HasTemplate("test")

	out, err := engine.Render(context.Background(), "test", map[string]any{
		"resources": buildTypedResourcesValue(stores, names),
	})
	require.NoError(t, err, "render must succeed for the untyped-element iteration pattern")
	return out
}

// staticResourceStore is a minimal ResourceStore implementation for
// the tests. The real production stores live in pkg/k8s, but we only
// need List() here.
type staticResourceStore struct {
	items []any
}

func (s *staticResourceStore) List() []any            { return s.items }
func (s *staticResourceStore) Fetch(_ ...any) []any   { return nil }
func (s *staticResourceStore) GetSingle(_ ...any) any { return nil }

// TestScriggoTypedIteration_NestedDigSlice reproduces the chart
// snippet's pattern as a tiny template: range a typed *[]*Gateway,
// then for each gateway range `dig(gw, "spec", "listeners") |
// toSlice()`. The output is the listener names in order — the same
// signal the chart snippet emits when it registers crt-list files
// per (gateway, listener) tuple.
//
// If this test passes, the Scriggo wiring for typed iteration
// matches the documented contract and the chart conversion should
// have worked. If it fails, the failure mode here is the exact one
// to fix before any chart-side conversion is mechanical.
func TestScriggoTypedIteration_NestedDigSlice(t *testing.T) {
	gwType := buildGatewayShapeType(t)
	gateways := buildSampleGateways(t, gwType)

	// Hand a `*[]*Gateway` pointer to the runtime, matching
	// addTypedResources's pattern.
	holder := reflect.New(gateways.Type())
	holder.Elem().Set(gateways)
	typedGateways := holder.Interface()

	template := `{%- for _, gw := range gateways -%}` +
		`{%- for _, l := range dig(gw, "spec", "listeners") | toSlice() -%}` +
		`{{ dig(l, "name") }};` +
		`{%- end -%}` +
		`{%- end -%}`

	typedOut := renderWithTypedGlobal(t, template, typedGateways, gwType)

	// Equivalent untyped data for the cross-shape comparison.
	untypedGateways := []any{
		map[string]any{
			"metadata": map[string]any{"name": "edge", "namespace": "ns1"},
			"spec": map[string]any{
				"listeners": []any{
					map[string]any{"name": "https-default", "port": int64(443), "protocol": "HTTPS"},
					map[string]any{"name": "https-perport", "port": int64(8443), "protocol": "HTTPS"},
				},
			},
		},
		map[string]any{
			"metadata": map[string]any{"name": "internal", "namespace": "ns2"},
			"spec": map[string]any{
				"listeners": []any{
					map[string]any{"name": "http", "port": int64(80), "protocol": "HTTP"},
				},
			},
		},
	}

	untypedTemplate := `{%- for _, gw := range resources.gateways.List() -%}` +
		`{%- for _, l := range dig(gw, "spec", "listeners") | toSlice() -%}` +
		`{{ dig(l, "name") }};` +
		`{%- end -%}` +
		`{%- end -%}`
	untypedOut := renderWithUntypedResources(t, untypedTemplate, untypedGateways)

	// Trim trailing whitespace — Scriggo's `{%- -%}` whitespace control
	// strips around the iteration but the final newline at the end of
	// the template's source line survives. Both the typed and untyped
	// paths produce the same trailing-newline artifact, which is what
	// matters: the chart conversion preserves byte-identical output.
	expected := "https-default;https-perport;http;"
	assert.Equal(t, expected, strings.TrimSpace(typedOut),
		"typed iteration must produce the same listener-name sequence the chart's crt-list snippet relies on")
	assert.Equal(t, expected, strings.TrimSpace(untypedOut),
		"untyped iteration (sanity check) must produce the same sequence")
	assert.Equal(t, untypedOut, typedOut,
		"typed and untyped iteration must produce IDENTICAL output for the chart's dig-and-toSlice pattern; if this fails, the chart conversion is unsafe")
}

// TestScriggoTypedIteration_HelperMacroOverAny pins the macro-
// argument contract. The chart's helper macros (ResourceNamespace,
// ResourceName, ListenerName, ...) take `any` and use dig() inside
// the body. A loop variable from a typed `*[]*Gateway` should pass
// through the `any` parameter and the macro's internal dig should
// behave identically to the untyped case.
func TestScriggoTypedIteration_HelperMacroOverAny(t *testing.T) {
	gwType := buildGatewayShapeType(t)
	gateways := buildSampleGateways(t, gwType)
	holder := reflect.New(gateways.Type())
	holder.Elem().Set(gateways)
	typedGateways := holder.Interface()

	template := `{%- macro NS(r any) string -%}` +
		`{{- tostring(dig(r, "metadata", "namespace") | fallback("")) -}}` +
		`{%- end macro -%}` +
		`{%- for _, gw := range gateways -%}` +
		`{{ NS(gw) }};` +
		`{%- end -%}`

	typedOut := renderWithTypedGlobal(t, template, typedGateways, gwType)
	assert.Equal(t, "ns1;ns2;", strings.TrimSpace(typedOut),
		"helper macro taking `any` must receive the typed pointer and dig must return the namespace; this is the exact rung the chart's ResourceNamespace macro relies on")
}
