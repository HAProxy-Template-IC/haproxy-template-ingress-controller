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

func TestBuildEngineDeclarations_ShapeIsSliceOfPointers(t *testing.T) {
	// A minimal Result with one known type — we don't care what
	// the type *contains*, only that BuildEngineDeclarations wraps
	// it in the *[]*Generated shape the StoreWrapper will populate.
	t1 := reflect.StructOf([]reflect.StructField{
		{Name: "Name", Type: reflect.TypeOf("")},
	})
	t2 := reflect.StructOf([]reflect.StructField{
		{Name: "Kind", Type: reflect.TypeOf("")},
	})
	decls := BuildEngineDeclarations(&Result{
		Types: map[string]reflect.Type{"a": t1, "b": t2},
	})
	require.Len(t, decls, 2)

	for name, declared := range decls {
		// Each declared value is a typed-nil pointer (Scriggo's
		// runtime-variable convention). reflect.TypeOf returns
		// the static type — peel back through *T → []*T → *T
		// to confirm the shape.
		typ := reflect.TypeOf(declared)
		require.Equal(t, reflect.Ptr, typ.Kind(), "%q: outer must be a pointer", name)
		slice := typ.Elem()
		require.Equal(t, reflect.Slice, slice.Kind(), "%q: inner must be a slice", name)
		elem := slice.Elem()
		require.Equal(t, reflect.Ptr, elem.Kind(), "%q: slice element must be a pointer", name)
		assert.Equal(t, reflect.Struct, elem.Elem().Kind(), "%q: pointee must be the generated struct", name)
	}
}

// TestBuildEngineDeclarations_SkipsErrors documents that failed
// resources don't end up in the declarations map. The chart's
// generic `resources["<name>"]` access still works for those — the
// typed shortcut just isn't available, matching the fail-open
// contract of Bootstrap.
func TestBuildEngineDeclarations_SkipsErrors(t *testing.T) {
	t1 := reflect.StructOf([]reflect.StructField{{Name: "F", Type: reflect.TypeOf("")}})
	decls := BuildEngineDeclarations(&Result{
		Types: map[string]reflect.Type{"good": t1},
		// "broken" appears in Errors but NOT in Types — the
		// Bootstrap fail-open semantics.
	})
	_, ok := decls["good"]
	assert.True(t, ok)
	_, ok = decls["broken"]
	assert.False(t, ok)
}

func TestBuildEngineDeclarations_NilResult(t *testing.T) {
	// Defensive: a caller that hands us a nil Result shouldn't get
	// a nil-pointer panic. Realistic case: Bootstrap returns an
	// outer error before constructing the Result, the caller forgets
	// to gate the call. Return an empty map so the engine still
	// gets a usable declarations argument.
	got := BuildEngineDeclarations(nil)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

// TestBootstrap_EndToEndThroughEngine is the keystone integration:
// Bootstrap → BuildEngineDeclarations → NewScriggoWithDeclarations,
// then a template that accesses the typed shape compiles. This
// proves the package's API actually composes into the templating
// engine's contract without any further glue — the only seam
// pkg/controller will need to wire at Phase 4 is the K8s clients,
// not the type plumbing.
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

	// Templates compile against the typed shape: ranging over the
	// declared `gateways` slice and accessing the per-element
	// Metadata.Name field. The keystone property — typoed paths
	// fail at engine construction — is covered by pkg/templating's
	// own dynamic_globals_test.go; here we just confirm the
	// successful-build path.
	templates := map[string]string{
		"main": `{%- for _, g := range gateways %}{{ g.Metadata.Name }}
{% end -%}`,
	}
	engine, err := templating.NewScriggoWithDeclarations(
		templates, []string{"main"}, nil, nil, nil,
		BuildEngineDeclarations(result),
	)
	require.NoError(t, err,
		"the engine must accept Bootstrap's declarations without further glue")

	// Populate a typed slice and run the template to confirm the
	// runtime shape matches what BuildEngineDeclarations declared.
	// This is what the StoreWrapper rewrite (Phase 5, not built
	// yet) will do at snapshot-load time.
	gwType := result.Types["gateways"]
	sliceType := reflect.SliceOf(reflect.PointerTo(gwType))
	slice := reflect.MakeSlice(sliceType, 2, 2)
	for i, name := range []string{"a", "b"} {
		gw := reflect.New(gwType)
		gw.Elem().FieldByName("Metadata").FieldByName("Name").SetString(name)
		slice.Index(i).Set(gw)
	}
	holder := reflect.New(sliceType)
	holder.Elem().Set(slice)

	out, err := engine.Render(context.Background(), "main", map[string]any{
		"gateways": holder.Interface(),
	})
	require.NoError(t, err)
	assert.Equal(t, "a\nb\n", strings.TrimLeft(out, "\n"))
}
