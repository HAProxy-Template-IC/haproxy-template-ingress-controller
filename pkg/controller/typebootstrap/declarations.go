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
	"fmt"
	"maps"
	"reflect"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/typegen"
)

// anyType is the canonical reflect.Type for an empty interface. Used
// as the slice element / return type for watched resources whose
// schema bootstrap failed — chart code still gets the same
// `.List() / .GetSingle() / .Fetch()` shape, but the elements are
// untyped `any` values that need `dig()` for navigation.
var anyType = reflect.TypeOf((*any)(nil)).Elem()

// keysArgType is the variadic `...any` parameter type used by Fetch
// and GetSingle. Built once and reused by every per-resource func
// type below.
var keysArgType = reflect.SliceOf(anyType)

// BuildEngineDeclarations turns a Bootstrap [Result] into the
// `map[string]any` shape that templating.Options.Declarations
// expects.
//
// The contract is a single top-level global named "resources" whose
// type is a dynamically-built struct ([reflect.StructOf]). The outer
// struct has one field per watched resource; each inner field is
// itself a struct holding the chart-facing access surface:
//
//	resources struct {
//	    Widgets *widgetStore `json:"widgets"`
//	    FooBars *fooBarStore `json:"foobars"`
//	    ...
//	}
//
//	widgetStore struct {
//	    List      func() []*Widget
//	    Fetch     func(keys ...any) []*Widget
//	    GetSingle func(keys ...any) *Widget
//	}
//
// Chart templates reach the typed-iteration path via
// `resources.widgets.List()` and the indexed-lookup paths via
// `resources.widgets.GetSingle(ns, name)` /
// `resources.widgets.Fetch(...)`. The return values are typed
// `*Widget` / `[]*Widget`, so Scriggo's type-checker validates
// field access (`w.Metadata.Namespace`) at engine boot. (Names here are
// generic placeholders — the field set is whatever the operator watches,
// never a fixed list of well-known kinds.)
//
// Resources whose schema didn't resolve (entry in [Result.Errors])
// still get an inner store struct, with the same field names, but
// the closure return types collapse to `[]any` / `any`. Chart code
// reaches them with identical syntax — just no compile-time field
// validation on the element shape.
//
// The closure VALUES live in rendercontext: typebootstrap only
// declares the *types*. Scriggo's typed-nil-pointer declaration
// pattern (the outer `(*Resources)(nil)` here) carries the type
// through engine compilation; the runtime binding fills in the
// closures per render.
//
// Field naming: outer Go field name = PascalCase of the resource
// key (`gateways` → `Gateways`); json tag = the lower-case wire-form
// resource name, so the chart can write `resources.gateways` and
// have the Scriggo json-tag selector fallback route the lowercase
// access to the PascalCase field. Inner field names are
// `List` / `Fetch` / `GetSingle` (no json tag — chart writes
// PascalCase directly).
//
// `extraResourceNames` carries the watched-resource names that are
// NOT in result.Types or result.Errors — typically core Kubernetes
// types (Service, Secret, ConfigMap, Pod) for which the controller
// has no typegen-derived schema available, but the chart still
// watches them and templates still reach `resources.<name>`. These
// names get untyped struct fields so the engine-declared shape stays
// in lockstep with the runtime-populated value built by
// rendercontext.
//
// Resources are listed in sorted order so the struct layout is
// deterministic across boots.
//
// Returns a map with one entry keyed "resources". Empty input (no
// watched resources at all) yields an empty map; callers should
// merge without special-casing.
func BuildEngineDeclarations(result *Result, extraResourceNames ...string) map[string]any {
	seen := make(map[string]struct{})
	if result != nil {
		for name := range result.Types {
			seen[name] = struct{}{}
		}
		for name := range result.Errors {
			seen[name] = struct{}{}
		}
	}
	for _, name := range extraResourceNames {
		seen[name] = struct{}{}
	}
	if len(seen) == 0 {
		return map[string]any{}
	}
	names := slices.Sorted(maps.Keys(seen))

	fields := make([]reflect.StructField, 0, len(names))
	for _, name := range names {
		var elemType reflect.Type
		if result != nil {
			elemType = result.Types[name]
		}
		innerType := BuildPerResourceStoreType(elemType)
		fields = append(fields, reflect.StructField{
			Name: typegen.GoFieldName(name),
			Type: reflect.PointerTo(innerType),
			Tag:  reflect.StructTag(fmt.Sprintf(`json:%q`, name)),
		})
	}

	resourcesType := reflect.StructOf(fields)
	// Per-resource types are reachable from chart macros via the
	// selector-chain-as-type Scriggo extension:
	//
	//	{% macro Foo(g *resources.gateways.T) string %} ... {% end %}
	//
	// The `T` field on each store struct (see BuildPerResourceStoreType)
	// carries the resource's generated value type. Scriggo lifts the
	// field's static type when the selector appears in a type-expression
	// position. This keeps the type namespace localised under
	// `resources` — no top-level type-name pollution.
	return map[string]any{
		"resources": reflect.Zero(reflect.PointerTo(resourcesType)).Interface(),
	}
}

// BuildPerResourceStoreType is the exported entry point
// rendercontext uses to build the same per-resource store struct
// shape declared here. Keeping this in one place ensures the
// engine-declared type and the render-time value type match
// byte-for-byte (different types of the same shape compare unequal
// to reflect, so any drift would surface as "wrong type" errors at
// template bind time).
//
// When `elemType` is non-nil, the store struct's methods are typed
// for `*elemType`. When nil, they fall back to untyped `any` /
// `[]any` — used for watched resources whose schema bootstrap
// failed.
func BuildPerResourceStoreType(elemType reflect.Type) reflect.Type {
	var (
		listReturn      reflect.Type
		fetchReturn     reflect.Type
		getSingleReturn reflect.Type
		tFieldType      reflect.Type
	)
	if elemType != nil {
		elemPtr := reflect.PointerTo(elemType)
		listReturn = reflect.SliceOf(elemPtr)
		fetchReturn = reflect.SliceOf(elemPtr)
		getSingleReturn = elemPtr
		// `T` carries the resource's generated value type so chart
		// authors can reference it in macro signatures via the
		// selector-chain-as-type Scriggo extension:
		//
		//	{% macro Foo(g *resources.gateways.T) %}{{ g.Metadata.Name }}{% end %}
		//
		// The field's RUNTIME value is the zero value of the type —
		// never read at render time, only its static type matters.
		// Per-render memory cost: ~size-of-Resource per watched
		// resource (a few hundred bytes for typical K8s shapes;
		// ~3 KB total for the chart's 12-ish typed resources).
		tFieldType = elemType
	} else {
		listReturn = reflect.SliceOf(anyType)
		fetchReturn = reflect.SliceOf(anyType)
		getSingleReturn = anyType
		// Resources without a schema still get a `T` field for
		// uniformity, typed as `any`. Chart code that reaches for
		// `*resources.<noSchema>.T` gets `*any` (pointer to
		// interface), which Scriggo accepts but field access on it
		// requires dig() — same degraded experience as the rest of
		// the untyped fallback path.
		tFieldType = anyType
	}

	listFunc := reflect.FuncOf(nil, []reflect.Type{listReturn}, false)
	keysVariadic := reflect.FuncOf(
		[]reflect.Type{keysArgType},
		[]reflect.Type{fetchReturn},
		true,
	)
	getSingleVariadic := reflect.FuncOf(
		[]reflect.Type{keysArgType},
		[]reflect.Type{getSingleReturn},
		true,
	)

	// APIVersion returns the group/version this resource is actually
	// watched at — the version the effective-config resolution selected
	// from the entry's candidate list. Generic watch-set metadata: status
	// macros pass it as the statusPatch apiVersion argument instead of
	// hardcoding version literals that break when a cluster serves a
	// different candidate.
	apiVersionFunc := reflect.FuncOf(nil, []reflect.Type{reflect.TypeOf("")}, false)

	own := []reflect.StructField{
		{Name: storeFieldT, Type: tFieldType},
		{Name: storeFieldList, Type: listFunc},
		{Name: storeFieldFetch, Type: keysVariadic},
		{Name: storeFieldGetSingle, Type: getSingleVariadic},
		{Name: storeFieldAPIVersion, Type: apiVersionFunc},
	}
	nested := nestedTypeFields(elemType)
	fields := make([]reflect.StructField, 0, len(own)+len(nested))
	fields = append(fields, own...)
	// Nested shapes get their own type-carrying fields so a pipeline closure
	// can declare its parameter and result types (ADR-0018). Without them
	// the types typegen builds are unnameable and only `any` + dig() works
	// below the resource's top level.
	fields = append(fields, nested...)

	return reflect.StructOf(fields)
}
