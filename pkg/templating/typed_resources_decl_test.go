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
	"reflect"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/typegen"
)

// Test helpers that build the typed `resources` Scriggo declaration and the
// matching runtime value the engine binds at template-run time.
//
// Production wires this same shape through
// `pkg/controller/typebootstrap.BuildEngineDeclarations` /
// `pkg/controller/rendercontext.BuildResourcesValue`. The templating package
// cannot import those (architectural layering — see pkg/CLAUDE.md), so we
// inline the equivalent reflect.StructOf logic here. The shape matches
// byte-for-byte so a chart-style template (`resources.<name>.List()`) that
// compiles against the production engine also compiles against an engine
// constructed with this helper.
//
// All fields collapse to the untyped (`[]any` / `any`) shape because the
// templating-level tests don't carry generated element types — they only
// need the OUTER struct (`*resources`) to exist with the right per-resource
// fields so dot-access through Scriggo's json-tag fallback resolves.

// typedResourceFieldType is the inner per-resource store struct shape:
//
//	struct{
//	    T         any
//	    List      func() []any
//	    Fetch     func(keys ...any) []any
//	    GetSingle func(keys ...any) any
//	}
//
// Mirrors `pkg/controller/typebootstrap.buildPerResourceStoreType(nil)`.
// Built once and reused across calls so reflect.StructOf compares equal.
var typedResourceFieldType = reflect.StructOf([]reflect.StructField{
	{Name: "T", Type: reflect.TypeOf((*any)(nil)).Elem()},
	{Name: "List", Type: reflect.FuncOf(
		nil,
		[]reflect.Type{reflect.SliceOf(reflect.TypeOf((*any)(nil)).Elem())},
		false,
	)},
	{Name: "Fetch", Type: reflect.FuncOf(
		[]reflect.Type{reflect.SliceOf(reflect.TypeOf((*any)(nil)).Elem())},
		[]reflect.Type{reflect.SliceOf(reflect.TypeOf((*any)(nil)).Elem())},
		true,
	)},
	{Name: "GetSingle", Type: reflect.FuncOf(
		[]reflect.Type{reflect.SliceOf(reflect.TypeOf((*any)(nil)).Elem())},
		[]reflect.Type{reflect.TypeOf((*any)(nil)).Elem()},
		true,
	)},
})

// typedResourcesStructType returns the outer `*struct{...}` type holding
// one field per supplied resource name. Fields are added in input order;
// callers that care about deterministic struct layout should pre-sort.
func typedResourcesStructType(names []string) reflect.Type {
	fields := make([]reflect.StructField, 0, len(names))
	for _, name := range names {
		fields = append(fields, reflect.StructField{
			Name: typegen.GoFieldName(name),
			Type: reflect.PointerTo(typedResourceFieldType),
			Tag:  reflect.StructTag(`json:"` + name + `"`),
		})
	}
	return reflect.StructOf(fields)
}

// typedResourcesDecl returns the `resources` declaration map suitable for
// `New` with Options.Declarations. The declaration is a typed-nil pointer to the
// dynamically-built struct so Scriggo sees the shape at compile time but the
// VALUE is provided at runtime via `Render`'s context map.
func typedResourcesDecl(names ...string) map[string]any {
	if len(names) == 0 {
		return map[string]any{}
	}
	resourcesType := typedResourcesStructType(names)
	return map[string]any{
		"resources": reflect.Zero(reflect.PointerTo(resourcesType)).Interface(),
	}
}

// buildTypedResourcesValue produces the runtime `*resources` struct value
// that pairs with the declaration from typedResourcesDecl. Each inner field's
// List / Fetch / GetSingle closures forward to the supplied ResourceStore;
// resources without a store get empty/nil returns.
//
// Names not present in `stores` still get a struct field (with nil closures
// returning empty results) — the runtime value's field list must match the
// declared struct exactly or Scriggo's variable bind panics at template run.
func buildTypedResourcesValue(stores map[string]ResourceStore, names []string) any {
	resourcesType := typedResourcesStructType(names)
	resources := reflect.New(resourcesType)
	for i, name := range names {
		var store ResourceStore
		if stores != nil {
			store = stores[name]
		}
		inner := reflect.New(typedResourceFieldType)
		elem := inner.Elem()
		listField := elem.FieldByName("List")
		fetchField := elem.FieldByName("Fetch")
		getSingleField := elem.FieldByName("GetSingle")

		listField.Set(reflect.MakeFunc(listField.Type(), func(_ []reflect.Value) []reflect.Value {
			if store == nil {
				return []reflect.Value{reflect.ValueOf([]any(nil))}
			}
			return []reflect.Value{reflect.ValueOf(store.List())}
		}))
		fetchField.Set(reflect.MakeFunc(fetchField.Type(), func(args []reflect.Value) []reflect.Value {
			if store == nil {
				return []reflect.Value{reflect.ValueOf([]any(nil))}
			}
			keys := args[0].Interface().([]any)
			return []reflect.Value{reflect.ValueOf(store.Fetch(keys...))}
		}))
		getSingleField.Set(reflect.MakeFunc(getSingleField.Type(), func(args []reflect.Value) []reflect.Value {
			zero := reflect.New(getSingleField.Type().Out(0)).Elem()
			if store == nil {
				return []reflect.Value{zero}
			}
			keys := args[0].Interface().([]any)
			item := store.GetSingle(keys...)
			if item == nil {
				return []reflect.Value{zero}
			}
			out := reflect.New(getSingleField.Type().Out(0)).Elem()
			out.Set(reflect.ValueOf(item))
			return []reflect.Value{out}
		}))

		resources.Elem().Field(i).Set(inner)
	}
	return resources.Interface()
}

// resourceNames extracts and returns a (deterministic-order) slice of resource
// names from a typed-resources map. Returning the SAME slice for both the
// declaration and the runtime-value construction is load-bearing — the field
// order in `typedResourcesStructType` must match between the two calls.
func resourceNames(stores map[string]ResourceStore) []string {
	out := make([]string, 0, len(stores))
	for name := range stores {
		out = append(out, name)
	}
	// reflect.StructOf is sensitive to field ORDER; sort to make construction
	// deterministic across test runs.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
