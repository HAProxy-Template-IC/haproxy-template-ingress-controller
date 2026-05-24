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

package typebootstrap

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/validation/spec"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/schemafetcher"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// TestBuildEngineDeclarations_ResourcesStructShape pins the
// engine-declaration contract: a single top-level `resources` global
// whose type is a dynamically-built struct, one field per watched
// resource. Each field is a pointer to a per-resource store struct
// holding the chart-facing access surface (T, List, Fetch, GetSingle).
// The chart's macros and snippets reach typed access via
// `resources.<name>.List()` and `resources.<name>.T` — see
// `charts/CLAUDE.md` for the chart-author convention.
func TestBuildEngineDeclarations_ResourcesStructShape(t *testing.T) {
	t1 := reflect.StructOf([]reflect.StructField{
		{Name: "Name", Type: reflect.TypeOf("")},
	})
	t2 := reflect.StructOf([]reflect.StructField{
		{Name: "Kind", Type: reflect.TypeOf("")},
	})
	decls := BuildEngineDeclarations(&Result{
		Types: map[string]reflect.Type{"a": t1, "b": t2},
		Kinds: map[string]string{"a": "A", "b": "B"},
	})

	// Single top-level entry: "resources". Per-resource entries are
	// fields on the struct, not top-level globals.
	require.Len(t, decls, 1)
	resourcesDecl, ok := decls["resources"]
	require.True(t, ok, "single 'resources' declaration expected")

	// Declared shape: *Resources (typed-nil pointer to the dynamic
	// struct). Scriggo's package-scope mechanism derefs the outer
	// pointer so chart code sees the struct value directly.
	typ := reflect.TypeOf(resourcesDecl)
	require.Equal(t, reflect.Ptr, typ.Kind(), "outer must be a pointer")
	resourcesType := typ.Elem()
	require.Equal(t, reflect.Struct, resourcesType.Kind(), "pointer must point at a struct")
	require.Equal(t, 2, resourcesType.NumField(),
		"one field per watched resource")

	// Each field is *TypedStore_<resource> with the expected shape.
	for i := 0; i < resourcesType.NumField(); i++ {
		f := resourcesType.Field(i)
		require.Equal(t, reflect.Ptr, f.Type.Kind(), "%q: field must be a pointer", f.Name)
		store := f.Type.Elem()
		require.Equal(t, reflect.Struct, store.Kind(), "%q: pointee must be a struct", f.Name)

		// Per-store struct fields: T, List, Fetch, GetSingle. T
		// carries the generated value type (used in macro signatures
		// via the selector-chain-as-type Scriggo extension); the
		// others are func-typed for runtime invocation.
		require.NotEqual(t, -1, fieldIndexByName(store, "T"), "%q: missing T field", f.Name)
		require.NotEqual(t, -1, fieldIndexByName(store, "List"), "%q: missing List field", f.Name)
		require.NotEqual(t, -1, fieldIndexByName(store, "Fetch"), "%q: missing Fetch field", f.Name)
		require.NotEqual(t, -1, fieldIndexByName(store, "GetSingle"), "%q: missing GetSingle field", f.Name)
	}
}

// TestBuildEngineDeclarations_UntypedFallbackForFailedSchemas covers
// the resources whose schema bootstrap failed. They still get a
// struct field (so chart code that reaches `resources.<name>` doesn't
// fail to compile) but the per-method closures collapse to `any` /
// `[]any` return types.
func TestBuildEngineDeclarations_UntypedFallbackForFailedSchemas(t *testing.T) {
	t1 := reflect.StructOf([]reflect.StructField{{Name: "F", Type: reflect.TypeOf("")}})
	decls := BuildEngineDeclarations(&Result{
		Types:  map[string]reflect.Type{"good": t1},
		Kinds:  map[string]string{"good": "Good"},
		Errors: map[string]error{"broken": assert.AnError},
	})
	require.Len(t, decls, 1)
	resourcesDecl := decls["resources"]
	resourcesType := reflect.TypeOf(resourcesDecl).Elem()
	require.Equal(t, 2, resourcesType.NumField(),
		"both successful and failed-schema resources surface as fields")

	// The failed-schema field's store has []any / any return types
	// instead of typed slices / pointers. We don't pin the exact
	// types here — the contract is "doesn't compile to typed access"
	// rather than "this specific shape" — but verify the T field
	// exists with the any-fallback type.
	for i := 0; i < resourcesType.NumField(); i++ {
		f := resourcesType.Field(i)
		store := f.Type.Elem()
		tIdx := fieldIndexByName(store, "T")
		require.NotEqual(t, -1, tIdx, "%q: missing T field", f.Name)
	}
}

func TestBuildEngineDeclarations_NilResult(t *testing.T) {
	// Defensive: a nil Result returns an empty declarations map so
	// the engine still gets a usable argument.
	got := BuildEngineDeclarations(nil)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

// TestBootstrap_EndToEndThroughEngine is the keystone integration
// after the typebootstrap shape rewrite: Bootstrap →
// BuildEngineDeclarations → NewScriggoWithDeclarations, then a
// template that uses the chart-facing access pattern
// `resources.<name>.List()` compiles. This pins that the
// engine-declared shape and the runtime population stay in lockstep.
func TestBootstrap_EndToEndThroughEngine(t *testing.T) {
	fetcher := schemafetcher.NewMapFetcher(map[schema.GroupVersionKind]*spec.Schema{
		{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"}: {
			SchemaProps: spec.SchemaProps{
				Type: spec.StringOrArray{"object"},
				Properties: map[string]spec.Schema{
					"metadata": {SchemaProps: spec.SchemaProps{
						Type: spec.StringOrArray{"object"},
						Properties: map[string]spec.Schema{
							"name": {SchemaProps: spec.SchemaProps{Type: spec.StringOrArray{"string"}}},
						},
					}},
				},
			},
		},
	})

	result, err := Bootstrap(t.Context(), Config{
		Resources: []Resource{
			{
				Name: "gateways",
				GVK:  schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"},
			},
		},
		Fetcher: fetcher,
		Logger:  silentLogger(),
	})
	require.NoError(t, err)
	require.Empty(t, result.Errors)

	// Template compiles against `resources.gateways.List()` — the
	// chart-facing access shape. Field access on each loop variable
	// goes through the typed bytecode path (g.Metadata.Name), not
	// reflection.
	templates := map[string]string{
		"main": `{%- for _, g := range resources.gateways.List() %}{{ g.Metadata.Name }}
{% end -%}`,
	}
	engine, err := templating.NewScriggoWithDeclarations(
		templates, []string{"main"}, nil, nil, nil,
		BuildEngineDeclarations(result),
	)
	require.NoError(t, err,
		"the engine must accept Bootstrap's declarations without further glue")

	// Populate the resources struct at runtime: build a per-resource
	// store value with a List closure returning a single Gateway,
	// stash it as the `resources` global value, and render the
	// template. Mirrors what rendercontext.addTypedResources does in
	// production (just without the StoreWrapper layer).
	gwType := result.Types["gateways"]
	sliceType := reflect.SliceOf(reflect.PointerTo(gwType))

	gw := reflect.New(gwType)
	gw.Elem().FieldByName("Metadata").FieldByName("Name").SetString("a")
	listResult := reflect.MakeSlice(sliceType, 1, 1)
	listResult.Index(0).Set(gw)

	// Build the inner per-resource store struct's value. The shape
	// (List/Fetch/GetSingle closures + T field) matches what
	// rendercontext builds at render time.
	innerType := BuildPerResourceStoreType(gwType)
	innerVal := reflect.New(innerType).Elem()
	listFuncType := innerVal.FieldByName("List").Type()
	innerVal.FieldByName("List").Set(reflect.MakeFunc(listFuncType, func(_ []reflect.Value) []reflect.Value {
		return []reflect.Value{listResult}
	}))
	// Fetch and GetSingle aren't called by this template; leave their
	// zero values (nil closures) — Scriggo doesn't invoke them.

	// Wire the inner store into the outer Resources struct value.
	resourcesType := reflect.TypeOf(BuildEngineDeclarations(result)["resources"]).Elem()
	resourcesPtr := reflect.New(resourcesType)
	resourcesPtr.Elem().Field(0).Set(innerVal.Addr())

	out, err := engine.Render(context.Background(), "main", map[string]any{
		"resources": resourcesPtr.Interface(),
	})
	require.NoError(t, err)
	assert.Equal(t, "a\n", strings.TrimLeft(out, "\n"))
}

// fieldIndexByName returns the index of the named field on t, or -1
// if absent. Helper used by the declaration-shape assertions above.
func fieldIndexByName(t reflect.Type, name string) int {
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Name == name {
			return i
		}
	}
	return -1
}
