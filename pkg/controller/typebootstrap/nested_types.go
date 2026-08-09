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
	"slices"
	"sort"
	"strconv"
	"strings"
)

// The per-resource store struct's own field names. BuildPerResourceStoreType
// declares them and nestedTypeName keeps derived names clear of them, so the
// two must read from one list — a nested field colliding with one of these
// panics reflect.StructOf at boot.
const (
	storeFieldT          = "T"
	storeFieldList       = "List"
	storeFieldFetch      = "Fetch"
	storeFieldGetSingle  = "GetSingle"
	storeFieldAPIVersion = "APIVersion"
)

// storeFieldNames is the collision guard. A nested type whose derived name
// lands here is suffixed rather than dropped — losing a type silently would
// make a chart pipeline unwritable with no diagnostic pointing at why.
var storeFieldNames = map[string]bool{
	storeFieldT:         true,
	storeFieldList:      true,
	storeFieldFetch:     true,
	storeFieldGetSingle: true,

	storeFieldAPIVersion: true,
}

// maxNestedTypeDepth bounds the walk. Kubernetes schemas are broad but shallow;
// the deepest path in the bundled set (Gateway listener TLS certificate refs)
// sits at 4. The bound exists so a self-referential CRD schema cannot produce an
// unbounded field set — typegen already breaks reference cycles, but a
// declaration struct is built once per boot and a runaway one would be a
// startup hang with no obvious cause.
const maxNestedTypeDepth = 6

// nestedTypeFields returns one struct field per distinct struct type reachable
// from elemType, so chart templates can name nested shapes as type expressions:
//
//	resources.endpoints.T          → EndpointSlice   (the resource itself)
//	resources.endpoints.Endpoints  → the nested Endpoint struct
//
// Nested types are otherwise unnameable: typegen builds them with
// reflect.StructOf, which produces unnamed types, so a closure over
// `slice.Endpoints` could not declare its parameter or result type at all. That
// is what makes collection pipelines writable with explicit types (ADR-0018).
//
// The name is the field path with separators removed (`Spec.Rules` →
// `SpecRules`), which is unique by construction. Types reached by more than one
// path are declared once, under the shortest path — then lexicographically, so
// the choice is stable across boots regardless of map iteration.
//
// Fields carry a zero value of the nested type; only the static type is ever
// read. The value cost is one zero struct per distinct nested type per watched
// resource, paid once per render context.
func nestedTypeFields(elemType reflect.Type) []reflect.StructField {
	if elemType == nil || elemType.Kind() != reflect.Struct {
		return nil
	}

	names := map[reflect.Type]string{elemType: ""}
	collectNestedTypes(elemType, nil, names, 0)

	fields := make([]reflect.StructField, 0, len(names))
	for typ, name := range names {
		if name == "" {
			continue // the resource type itself, already declared as T
		}
		fields = append(fields, reflect.StructField{Name: name, Type: typ})
	}
	// Sort before disambiguating, not after: reflect.StructOf field order is
	// part of the type's identity, and the declared struct must match the
	// runtime value's byte for byte or Scriggo's variable bind fails. Sorting
	// by name and then by type keeps that order stable across boots even when
	// two types compete for one name.
	sort.Slice(fields, func(i, j int) bool {
		if fields[i].Name != fields[j].Name {
			return fields[i].Name < fields[j].Name
		}
		return fields[i].Type.String() < fields[j].Type.String()
	})
	return disambiguate(fields)
}

// disambiguate makes every derived name unique.
//
// Concatenating a field path is not injective: `root{Mid mid; MidFoo leafB}`
// with `mid{Foo leafA}` derives "MidFoo" for two different types. Handing
// reflect.StructOf two fields of the same name panics, so a CRD schema shaped
// that way would take the controller down at boot rather than merely naming
// something awkwardly.
//
// The first field to claim a name keeps it — with input sorted, that is
// deterministic — and later claimants take a numeric suffix.
//
// Suffixes are only drawn from names nothing in the input already claims. A
// field genuinely derived as `Foo2` must keep that name; letting a second
// `Foo` take it first would bump the real `Foo2` to `Foo22` and turn one
// collision into a chain of renames.
func disambiguate(fields []reflect.StructField) []reflect.StructField {
	claimed := make(map[string]bool, len(fields))
	for _, f := range fields {
		claimed[f.Name] = true
	}
	assigned := make(map[string]bool, len(fields))
	for i, f := range fields {
		if !assigned[f.Name] {
			assigned[f.Name] = true
			continue
		}
		for n := 2; ; n++ {
			candidate := f.Name + strconv.Itoa(n)
			if !claimed[candidate] && !assigned[candidate] && !storeFieldNames[candidate] {
				fields[i].Name = candidate
				assigned[candidate] = true
				break
			}
		}
	}
	return fields
}

// collectNestedTypes walks struct fields depth-first, recording the best name
// for each distinct struct type. Slices, arrays, maps and pointers are
// unwrapped to their element type, because what a pipeline closure needs to
// name is the element, not the container.
func collectNestedTypes(typ reflect.Type, path []string, names map[reflect.Type]string, depth int) {
	if depth >= maxNestedTypeDepth {
		return
	}
	for i := range typ.NumField() {
		field := typ.Field(i)
		inner := unwrapToStruct(field.Type)
		if inner == nil {
			continue
		}
		fieldPath := append(slices.Clone(path), field.Name)
		candidate := nestedTypeName(fieldPath)

		existing, seen := names[inner]
		if seen && !preferName(candidate, existing) {
			continue
		}
		if seen && existing == "" {
			continue // never rename the resource type away from T
		}
		names[inner] = candidate
		collectNestedTypes(inner, fieldPath, names, depth+1)
	}
}

// unwrapToStruct peels pointers, slices, arrays and maps down to a struct type,
// or reports nil when the field bottoms out in a scalar.
func unwrapToStruct(typ reflect.Type) reflect.Type {
	for {
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			typ = typ.Elem()
		case reflect.Map:
			typ = typ.Elem()
		case reflect.Struct:
			return typ
		default:
			return nil
		}
	}
}

// nestedTypeName derives the declared field name from a field path, keeping it
// clear of the store struct's own field names.
func nestedTypeName(path []string) string {
	name := strings.Join(path, "")
	if storeFieldNames[name] {
		return name + "Type"
	}
	return name
}

// preferName reports whether candidate is a better name than current: shorter
// paths win, ties break lexicographically. Deterministic ordering matters
// because the declared struct and the runtime value struct are compared by
// reflect identity — any drift between boots would surface as a template bind
// failure, not as a naming inconsistency.
func preferName(candidate, current string) bool {
	if len(candidate) != len(current) {
		return len(candidate) < len(current)
	}
	return candidate < current
}
