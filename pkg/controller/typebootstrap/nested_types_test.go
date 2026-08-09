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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type nestedRef struct {
	Name string `json:"name"`
}

type nestedEndpoint struct {
	TargetRef nestedRef `json:"targetRef"`
	Addresses []string  `json:"addresses"`
}

type nestedEndpointSlice struct {
	Endpoints []nestedEndpoint `json:"endpoints"`
}

func TestNestedTypeFields(t *testing.T) {
	fields := nestedTypeFields(reflect.TypeOf(nestedEndpointSlice{}))

	byName := map[string]reflect.Type{}
	for _, f := range fields {
		byName[f.Name] = f.Type
	}

	// The nested element type is what a pipeline closure must name; without
	// this field it is unnameable, because typegen builds it with
	// reflect.StructOf.
	require.Contains(t, byName, "Endpoints")
	assert.Equal(t, reflect.TypeOf(nestedEndpoint{}), byName["Endpoints"],
		"a slice field must expose its ELEMENT type, not the slice")

	require.Contains(t, byName, "EndpointsTargetRef")
	assert.Equal(t, reflect.TypeOf(nestedRef{}), byName["EndpointsTargetRef"])

	// []string bottoms out in a scalar and contributes no type field.
	assert.NotContains(t, byName, "EndpointsAddresses")
}

func TestNestedTypeFieldsAreDeterministic(t *testing.T) {
	// The declared struct and the runtime value struct are compared by
	// reflect identity, so an unstable field order across boots would
	// surface as a template bind failure rather than as a naming quirk.
	first := nestedTypeFields(reflect.TypeOf(nestedEndpointSlice{}))
	for range 20 {
		assert.Equal(t, first, nestedTypeFields(reflect.TypeOf(nestedEndpointSlice{})))
	}
	require.True(t, sortedByName(first))
}

func sortedByName(fields []reflect.StructField) bool {
	for i := 1; i < len(fields); i++ {
		if fields[i-1].Name >= fields[i].Name {
			return false
		}
	}
	return true
}

func TestNestedTypeFieldsSharedTypeGetsShortestName(t *testing.T) {
	// A type reachable by several paths is declared once. The shortest path
	// wins so the name a chart author writes stays the obvious one.
	type leaf struct{ V string }
	type mid struct{ Leaf leaf }
	type root struct {
		Direct leaf
		Nested mid
	}

	byName := map[string]reflect.Type{}
	for _, f := range nestedTypeFields(reflect.TypeOf(root{})) {
		byName[f.Name] = f.Type
	}

	assert.Contains(t, byName, "Direct")
	assert.NotContains(t, byName, "NestedLeaf")
	assert.Equal(t, reflect.TypeOf(leaf{}), byName["Direct"])
}

func TestNestedTypeFieldsAvoidStoreFieldCollision(t *testing.T) {
	// A resource with a top-level object field named `List` would otherwise
	// collide with the store's List method field and panic reflect.StructOf.
	type inner struct{ V string }
	type collides struct {
		List  inner
		Fetch inner
	}

	nested := nestedTypeFields(reflect.TypeOf(collides{}))
	names := make([]string, 0, len(nested))
	for _, f := range nested {
		names = append(names, f.Name)
	}

	for _, reserved := range []string{"List", "Fetch", "GetSingle", "T", "APIVersion"} {
		assert.NotContains(t, names, reserved)
	}
	assert.NotEmpty(t, names, "a colliding field must be renamed, never dropped")
}

func TestNestedTypeFieldsHandleNilAndScalarInput(t *testing.T) {
	assert.Nil(t, nestedTypeFields(nil), "resources without a schema get no nested types")
	assert.Nil(t, nestedTypeFields(reflect.TypeOf("")))
}

func TestNestedTypeFieldsTerminateOnSelfReference(t *testing.T) {
	// typegen breaks reference cycles, but a declaration struct is built once
	// per boot: an unbounded walk would be a startup hang with no obvious
	// cause, so the depth bound is load-bearing rather than defensive.
	type node struct {
		Children []*node
		Name     string
	}
	fields := nestedTypeFields(reflect.TypeOf(node{}))
	assert.LessOrEqual(t, len(fields), maxNestedTypeDepth)
}

func TestBuildPerResourceStoreTypeExposesNestedTypes(t *testing.T) {
	storeType := BuildPerResourceStoreType(reflect.TypeOf(nestedEndpointSlice{}))

	// The store's own fields must survive alongside the nested ones —
	// rendercontext populates them by name.
	for _, required := range []string{"T", "List", "Fetch", "GetSingle", "APIVersion"} {
		_, ok := storeType.FieldByName(required)
		assert.True(t, ok, "store field %s must remain", required)
	}

	nested, ok := storeType.FieldByName("Endpoints")
	require.True(t, ok, "nested types must be reachable as resources.<name>.<Path>")
	assert.Equal(t, reflect.TypeOf(nestedEndpoint{}), nested.Type)
}

func TestBuildPerResourceStoreTypeWithoutSchemaHasNoNestedTypes(t *testing.T) {
	storeType := BuildPerResourceStoreType(nil)
	assert.Equal(t, 5, storeType.NumField(),
		"the untyped fallback keeps exactly the store's own fields")
}

// TestNestedTypeFieldsDisambiguateDerivedNameCollision covers the case a
// reviewer caught: concatenating a field path is not injective, so two
// genuinely different types can want the same name. reflect.StructOf panics on
// a duplicate field, which would take the controller down at boot for any CRD
// schema shaped this way.
func TestNestedTypeFieldsDisambiguateDerivedNameCollision(t *testing.T) {
	type leafA struct{ A string }
	type leafB struct{ B string }
	type mid struct{ Foo leafA }
	type root struct {
		Mid    mid
		MidFoo leafB
	}

	fields := nestedTypeFields(reflect.TypeOf(root{}))

	seen := map[string]bool{}
	for _, f := range fields {
		require.False(t, seen[f.Name], "duplicate field name %q would panic reflect.StructOf", f.Name)
		seen[f.Name] = true
	}

	// Both colliding types must survive — dropping one would make it
	// unnameable with no diagnostic saying why.
	types := map[reflect.Type]bool{}
	for _, f := range fields {
		types[f.Type] = true
	}
	assert.True(t, types[reflect.TypeOf(leafA{})], "leafA must keep a name")
	assert.True(t, types[reflect.TypeOf(leafB{})], "leafB must keep a name")

	// The struct must actually build.
	assert.NotPanics(t, func() { BuildPerResourceStoreType(reflect.TypeOf(root{})) })
}

// TestNestedTypeFieldsCollisionIsDeterministic pins that the disambiguated
// layout is stable: reflect.StructOf field order is part of the type's
// identity, and the declared struct must match the runtime value's exactly.
func TestNestedTypeFieldsCollisionIsDeterministic(t *testing.T) {
	type leafA struct{ A string }
	type leafB struct{ B string }
	type mid struct{ Foo leafA }
	type root struct {
		Mid    mid
		MidFoo leafB
	}

	first := nestedTypeFields(reflect.TypeOf(root{}))
	for range 20 {
		assert.Equal(t, first, nestedTypeFields(reflect.TypeOf(root{})))
	}
}

// TestNestedTypeFieldsSuffixDoesNotStealALiteralName covers the follow-up a
// reviewer caught: a suffix must not take a name some other field legitimately
// derived. Without the pre-scan, the second "Foo" claims "Foo2" and the field
// actually named Foo2 cascades to "Foo22" — no panic, but a rename chain that
// is much harder to reason about than the one collision that caused it.
func TestNestedTypeFieldsSuffixDoesNotStealALiteralName(t *testing.T) {
	type leafA struct{ A string }
	type leafB struct{ B string }
	type leafC struct{ C string }
	type mid struct{ Foo leafA }
	type root struct {
		Mid     mid   // derives MidFoo
		MidFoo  leafB // derives MidFoo — collides
		MidFoo2 leafC // derives MidFoo2 — must keep it
	}

	byType := map[reflect.Type]string{}
	names := map[string]bool{}
	for _, f := range nestedTypeFields(reflect.TypeOf(root{})) {
		require.False(t, names[f.Name], "duplicate field name %q", f.Name)
		names[f.Name] = true
		byType[f.Type] = f.Name
	}

	assert.Equal(t, "MidFoo2", byType[reflect.TypeOf(leafC{})],
		"a field that derives MidFoo2 keeps it; a suffix must not take it first")
	assert.NotPanics(t, func() { BuildPerResourceStoreType(reflect.TypeOf(root{})) })
}
