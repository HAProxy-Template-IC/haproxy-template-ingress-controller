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

// The tests in this file pin the engine's contract for *dynamically
// constructed* Go types declared via [New] with [Options.Declarations].
// Scriggo's globals accept any [reflect.Type] — including one built
// at runtime with [reflect.StructOf] — and the engine compiles
// templates against the supplied field shape. The typed-watched-
// resources work (pkg/k8s/typegen → pkg/controller/typebootstrap →
// here) leans hard on this behaviour, so a Scriggo upgrade or an
// engine refactor that broke it would silently degrade every typed-
// resource access in the chart back to render-time "field zero"
// surprises.
//
// We deliberately do NOT import pkg/k8s/typegen here: the assertions
// are about the engine's contract with `reflect.StructOf`-shaped
// types in general, and pulling typegen in would conflate "the
// converter built the type the operator wanted" with "the engine
// accepts arbitrary built-by-StructOf types". The two are separate
// failure modes that deserve separate tests.

// makeGatewayShape constructs a Gateway-flavoured struct via
// reflect.StructOf. The exact field set mirrors what pkg/k8s/typegen
// would produce for a real Gateway schema, but the construction is
// hand-rolled so this test never depends on the converter.
func makeGatewayShape() reflect.Type {
	metaType := reflect.StructOf([]reflect.StructField{
		{Name: "Name", Type: reflect.TypeOf("")},
		{Name: "Namespace", Type: reflect.TypeOf("")},
	})
	return reflect.StructOf([]reflect.StructField{
		{Name: "ApiVersion", Type: reflect.TypeOf("")},
		{Name: "Kind", Type: reflect.TypeOf("")},
		{Name: "Metadata", Type: metaType},
	})
}

// TestNew_Declarations_DynamicStructFieldAccess is the
// keystone proof that engine globals work with runtime-built types.
// Without this passing, the entire typed-watched-resources pipeline
// is dead on arrival.
func TestNew_Declarations_DynamicStructFieldAccess(t *testing.T) {
	gwType := makeGatewayShape()
	gwTypeKind := reflect.PointerTo(gwType)

	templates := map[string]string{
		"main": `{{ gateway.Kind }}/{{ gateway.Metadata.Namespace }}/{{ gateway.Metadata.Name }}`,
	}
	engine, err := New(templates, &Options{EntryPoints: []string{"main"}, Declarations: map[string]any{"gateway": reflect.Zero(gwTypeKind).Interface()}})
	require.NoError(t, err)

	gw := reflect.New(gwType).Elem()
	gw.FieldByName("ApiVersion").SetString("gateway.networking.k8s.io/v1")
	gw.FieldByName("Kind").SetString("Gateway")
	meta := gw.FieldByName("Metadata")
	meta.FieldByName("Name").SetString("public")
	meta.FieldByName("Namespace").SetString("ingress")

	out, err := engine.Render(context.Background(), "main", map[string]any{
		"gateway": gw.Addr().Interface(),
	})
	require.NoError(t, err)
	// Scriggo appends a trailing newline to .txt / unformatted
	// templates; trim it rather than encoding the format-specific
	// suffix in the assertion (the trailing-newline behaviour is
	// covered by other engine tests and is not what THIS test is
	// pinning).
	assert.Equal(t, "Gateway/ingress/public", strings.TrimRight(out, "\n"))
}

// TestNew_Declarations_TypoCatchAtBuildTime is the property
// that justifies the whole effort: a misspelled field path on a
// typed global fails at *template compile* time (when the engine
// is constructed), not at render time. This is what flips the
// failure mode from "every reconcile silently renders empty
// frontend blocks" to "the controller refuses to boot with a
// pointer at the broken template".
func TestNew_Declarations_TypoCatchAtBuildTime(t *testing.T) {
	gwType := makeGatewayShape()
	gwTypeKind := reflect.PointerTo(gwType)

	// Note "Naamespace" instead of "Namespace". A map-backed value
	// would just dig() up nil here; the typed view must reject.
	bad := map[string]string{
		"main": `{{ gateway.Metadata.Naamespace }}`,
	}
	_, err := New(bad, &Options{EntryPoints: []string{"main"}, Declarations: map[string]any{"gateway": reflect.Zero(gwTypeKind).Interface()}})
	require.Error(t, err,
		"a typoed field path on a typed global must fail at engine construction")

	// Sanity check the other direction: the same engine constructed
	// with the correct field name compiles fine. Guards against a
	// future regression where engine construction starts rejecting
	// every typed global rather than just typoed ones.
	good := map[string]string{
		"main": `{{ gateway.Metadata.Namespace }}`,
	}
	_, err = New(good, &Options{EntryPoints: []string{"main"}, Declarations: map[string]any{"gateway": reflect.Zero(gwTypeKind).Interface()}})
	require.NoError(t, err, "the correct field name must compile cleanly")
}

// TestNew_Declarations_RangeOverDynamicSlice mirrors the
// chart's everyday loop pattern:
//
//	{%- for _, gw := range resources.gateways.List() %}
//	  {{ gw.Metadata.Namespace }}
//	{%- end %}
//
// — but with the typed-slice variant that pkg/k8s/typegen.WrapSlice
// produces (a slice of pointers to the generated struct type). The
// store wrapper (Phase 5) will yield this shape; templates compile
// against it.
func TestNew_Declarations_RangeOverDynamicSlice(t *testing.T) {
	gwType := reflect.StructOf([]reflect.StructField{
		{Name: "Name", Type: reflect.TypeOf("")},
	})
	sliceType := reflect.SliceOf(gwType)
	sliceKind := reflect.PointerTo(sliceType)

	templates := map[string]string{
		"main": `{%- for _, g := range gateways %}{{ g.Name }}
{% end -%}`,
	}
	engine, err := New(templates, &Options{EntryPoints: []string{"main"}, Declarations: map[string]any{"gateways": reflect.Zero(sliceKind).Interface()}})
	require.NoError(t, err)

	slice := reflect.MakeSlice(sliceType, 2, 2)
	slice.Index(0).FieldByName("Name").SetString("a")
	slice.Index(1).FieldByName("Name").SetString("b")
	holder := reflect.New(sliceType)
	holder.Elem().Set(slice)

	out, err := engine.Render(context.Background(), "main", map[string]any{
		"gateways": holder.Interface(),
	})
	require.NoError(t, err)
	assert.Equal(t, "a\nb\n", out)
}

// TestNew_Declarations_MissingFieldRejected pins the side
// of the typegen/IgnoreFields contract that lives in the engine:
// once a global's type omits a field, template references to that
// field must fail at engine construction. typegen's tests cover
// the stripping side; this test covers the rejection side without
// needing the converter at all — the missing field could just as
// well be a typo, a schema gap, or anything else.
func TestNew_Declarations_MissingFieldRejected(t *testing.T) {
	// A type that DELIBERATELY omits ManagedFields, matching what
	// typegen would produce for an operator who stripped it via
	// HAProxyTemplateConfig.spec.watchedResourcesIgnoreFields.
	gwType := reflect.StructOf([]reflect.StructField{
		{Name: "Metadata", Type: reflect.StructOf([]reflect.StructField{
			{Name: "Name", Type: reflect.TypeOf("")},
			// no ManagedFields
		})},
	})
	gwTypeKind := reflect.PointerTo(gwType)

	templates := map[string]string{
		"main": `{{ gateway.Metadata.ManagedFields }}`,
	}
	_, err := New(templates, &Options{EntryPoints: []string{"main"}, Declarations: map[string]any{"gateway": reflect.Zero(gwTypeKind).Interface()}})
	require.Error(t, err,
		"reference to a field absent from the typed global must fail at "+
			"engine construction — the operator stripped it via "+
			"watchedResourcesIgnoreFields and the chart shouldn't reach it")
}
