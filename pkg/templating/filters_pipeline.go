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
	"fmt"
	"reflect"
	"sort"
	"strings"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

// Collection pipeline helpers. Each is a native.AdaptiveFunc so the static
// return type at every call site is computed from the argument types, which is
// what keeps a typed `[]T` typed across a chain instead of degrading to
// []any-with-dig(). See ADR-0018.
//
// Predicates and key functions are closures, not JSONPath strings, so field
// access inside them is checked at engine compile time. The string-keyed
// alternative fails silently — `selectattr(eps, "targetRef.name", "ne", "")`
// matches nothing because the dotted path reaches dig() as a single key.
//
// RULE #1: every helper here operates on slices and closures. None knows a
// Kubernetes kind, and none is more ergonomic for a well-known resource than
// for an arbitrary CRD.

// anyType is the reflect.Type of interface{}, the fallback static type when an
// argument's type is unknown at check time.
var anyType = reflect.TypeOf((*any)(nil)).Elem()

// elemCaller invokes a one-argument closure once per slice element.
//
// Every call re-enters the Scriggo VM, which costs ~5.6 µs and ~32 KB — three
// orders of magnitude more than the reflect bookkeeping around it, and the
// reason a pipeline over thousands of elements loses badly to a `{%% %%}` loop
// (see the benchmarks in benchmark_pipeline_test.go and ADR-0018's performance
// section). The argument slice is therefore hoisted out of the loop: it does
// not move the needle against the VM cost, but it is free.
type elemCaller struct {
	fn    reflect.Value
	args  [1]reflect.Value
	boxed bool
}

func newElemCaller(fn reflect.Value) *elemCaller {
	return &elemCaller{
		fn: fn,
		// A closure written `func(e EP) bool` receives the element directly;
		// one written `func(e any) bool` needs the value re-boxed, because
		// reflect.Call matches the declared parameter type exactly.
		boxed: fn.Type().NumIn() > 0 && fn.Type().In(0).Kind() == reflect.Interface,
	}
}

func (c *elemCaller) call(elem reflect.Value) reflect.Value {
	if c.boxed {
		c.args[0] = reflect.ValueOf(elem.Interface())
	} else {
		c.args[0] = elem
	}
	return c.fn.Call(c.args[:])[0]
}

// sliceOf returns v as a slice reflect.Value, reporting whether it is one.
// A nil or non-slice input is not an error: chart code reaches optional typed
// fields that are nil, and a nil slice must behave as an empty one rather than
// aborting the render.
func sliceOf(v any) (reflect.Value, bool) {
	if v == nil {
		return reflect.Value{}, false
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice {
		return reflect.Value{}, false
	}
	return rv, true
}

// funcArg validates that v is a one-argument, one-result function, failing with
// the helper's name so a chart author sees which stage is wrong. Every pipeline
// helper takes an element and returns one value.
func funcArg(name string, v any) reflect.Value {
	if v == nil {
		panic(fmt.Sprintf("%s: missing function argument", name))
	}
	fv := reflect.ValueOf(v)
	if fv.Kind() != reflect.Func {
		panic(fmt.Sprintf("%s: second argument must be a function, got %T", name, v))
	}
	if fv.Type().NumIn() != 1 || fv.Type().NumOut() != 1 {
		panic(fmt.Sprintf("%s: function must take 1 argument and return 1 value, got %s",
			name, fv.Type()))
	}
	return fv
}

// identityReturnType is the ReturnType hook for helpers that return a subset of
// their input: the call's static type is the input slice's static type.
func identityReturnType(argTypes []reflect.Type) (reflect.Type, error) {
	if len(argTypes) == 0 || argTypes[0] == nil {
		return anyType, nil
	}
	if argTypes[0].Kind() != reflect.Slice {
		return anyType, nil
	}
	return argTypes[0], nil
}

// selectMatching returns the elements for which pred equals keep.
func selectMatching(name string, slice, pred any, keep bool) any {
	rv, ok := sliceOf(slice)
	if !ok {
		return slice
	}
	pv := funcArg(name, pred)
	if pv.Type().Out(0).Kind() != reflect.Bool {
		panic(fmt.Sprintf("%s: predicate must return bool, got %s", name, pv.Type().Out(0)))
	}
	caller := newElemCaller(pv)
	out := reflect.MakeSlice(rv.Type(), 0, rv.Len())
	for i := range rv.Len() {
		if caller.call(rv.Index(i)).Bool() == keep {
			out = reflect.Append(out, rv.Index(i))
		}
	}
	return out.Interface()
}

// scriggoFilterAdaptive keeps the elements a predicate accepts.
//
//	items | filter(func(e EP) bool { return e.Ready })
var scriggoFilterAdaptive = native.AdaptiveFunc{
	Impl:       func(slice, pred any) any { return selectMatching(FuncFilter, slice, pred, true) },
	ReturnType: identityReturnType,
}

// scriggoRejectAdaptive drops the elements a predicate accepts. It exists so
// call sites read as a positive statement rather than a negated one.
//
//	items | reject(func(e EP) bool { return e.TargetRef.Name == "" })
var scriggoRejectAdaptive = native.AdaptiveFunc{
	Impl:       func(slice, pred any) any { return selectMatching(FuncReject, slice, pred, false) },
	ReturnType: identityReturnType,
}

// scriggoFlatMapAdaptive maps each element to a slice and concatenates the
// results, flattening exactly one level.
//
//	resources.endpoints.List() | flat_map(func(s resources.endpoints.T) []EP { return s.Endpoints })
var scriggoFlatMapAdaptive = native.AdaptiveFunc{
	Impl: func(slice, fn any) any {
		fv := funcArg(FuncFlatMap, fn)
		if fv.Type().Out(0).Kind() != reflect.Slice {
			panic(fmt.Sprintf("%s: function must return a slice, got %s — use map for element-wise results",
				FuncFlatMap, fv.Type().Out(0)))
		}
		rv, ok := sliceOf(slice)
		if !ok {
			return reflect.MakeSlice(fv.Type().Out(0), 0, 0).Interface()
		}
		// Each element contributes at least zero and typically one or more,
		// so the input length is a better starting capacity than zero.
		out := reflect.MakeSlice(fv.Type().Out(0), 0, rv.Len())
		caller := newElemCaller(fv)
		for i := range rv.Len() {
			part := caller.call(rv.Index(i))
			for j := range part.Len() {
				out = reflect.Append(out, part.Index(j))
			}
		}
		return out.Interface()
	},
	// The element type comes from the closure, not the input, so the return
	// type is the closure's result type verbatim (already a slice).
	ReturnType: func(argTypes []reflect.Type) (reflect.Type, error) {
		if len(argTypes) < 2 || argTypes[1] == nil || argTypes[1].Kind() != reflect.Func {
			return nil, fmt.Errorf("%s: second argument must be a function returning a slice", FuncFlatMap)
		}
		if argTypes[1].NumOut() != 1 || argTypes[1].Out(0).Kind() != reflect.Slice {
			return nil, fmt.Errorf("%s: function must return a slice, got %s", FuncFlatMap, argTypes[1])
		}
		return argTypes[1].Out(0), nil
	},
}

// sortByAdaptive builds the sort_by declaration. Two call shapes share the
// name, dispatched on the runtime type of the second argument:
//
//	items | sort_by([]string{"$.priority:desc", "$.name"})   JSONPath criteria
//	items | sort_by(func(a, b T) int { … })                  comparator
//
// The criteria form is kept, not deprecated: it expresses multi-key ordering
// with :desc / :exists / | length modifiers that a comparator states far more
// awkwardly, and every existing call site uses it.
//
// As an AdaptiveFunc this also widens the first argument from []any to any
// slice, so sort_by accepts typed slices — which the plain []any signature
// could not — and returns them with their element type intact.
//
// debug is read through a callback rather than captured, so the testrunner's
// post-construction EnableFilterDebug() toggle is honoured.
func sortByAdaptive(debugEnabled func() bool) native.AdaptiveFunc {
	return native.AdaptiveFunc{
		Impl: func(slice, by any) (any, error) {
			rv, ok := sliceOf(slice)
			if !ok {
				return slice, nil
			}
			if criteria, isCriteria := asCriteria(by); isCriteria {
				return sortByCriteria(rv, criteria, debugEnabled())
			}
			return sortByComparator(rv, by)
		},
		ReturnType: identityReturnType,
	}
}

// asCriteria recognises the JSONPath-criteria call shape. []any is accepted
// alongside []string because append() yields []any, and chart code builds
// criteria lists that way.
func asCriteria(by any) ([]string, bool) {
	switch v := by.(type) {
	case []string:
		return v, true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

// sortByCriteria sorts through the JSONPath machinery, which operates on []any,
// then rebuilds a slice of the input's element type so the static return type
// the AdaptiveFunc promised matches the runtime value.
func sortByCriteria(rv reflect.Value, criteria []string, debug bool) (any, error) {
	items := make([]any, rv.Len())
	for i := range rv.Len() {
		items[i] = rv.Index(i).Interface()
	}
	sorted, err := sortByItems(items, criteria, debug)
	if err != nil {
		return nil, err
	}
	out := reflect.MakeSlice(rv.Type(), 0, len(sorted))
	for _, item := range sorted {
		out = reflect.Append(out, reflect.ValueOf(item))
	}
	return out.Interface(), nil
}

// sortByComparator sorts with a user comparator, Go's cmp convention: negative
// when a sorts before b. The sort is stable so equal elements keep input order,
// which is what keeps a rendered map file byte-identical between renders.
func sortByComparator(rv reflect.Value, by any) (any, error) {
	if by == nil {
		return nil, fmt.Errorf("%s: missing second argument — pass []string criteria or a comparator function", FilterSortBy)
	}
	fv := reflect.ValueOf(by)
	if fv.Kind() != reflect.Func || fv.Type().NumIn() != 2 || fv.Type().NumOut() != 1 ||
		fv.Type().Out(0).Kind() != reflect.Int {
		return nil, fmt.Errorf("%s: second argument must be []string criteria or func(a, b T) int, got %T", FilterSortBy, by)
	}
	out := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
	reflect.Copy(out, rv)
	boxed := fv.Type().In(0).Kind() == reflect.Interface
	sort.SliceStable(out.Interface(), func(i, j int) bool {
		a, b := out.Index(i), out.Index(j)
		if boxed {
			a, b = reflect.ValueOf(a.Interface()), reflect.ValueOf(b.Interface())
		}
		return fv.Call([]reflect.Value{a, b})[0].Int() < 0
	})
	return out.Interface(), nil
}

// scriggoMapAdaptive applies a function to every element, preserving length.
// Use flat_map when the function returns a slice that should be concatenated.
//
//	pairs | map(func(p Pair) string { return p.Addr + " " + p.Pod })
var scriggoMapAdaptive = native.AdaptiveFunc{
	Impl: func(slice, fn any) any {
		fv := funcArg(FuncMap, fn)
		rv, ok := sliceOf(slice)
		if !ok {
			return reflect.MakeSlice(reflect.SliceOf(fv.Type().Out(0)), 0, 0).Interface()
		}
		// Length is preserved, so the output size is known exactly.
		out := reflect.MakeSlice(reflect.SliceOf(fv.Type().Out(0)), rv.Len(), rv.Len())
		caller := newElemCaller(fv)
		for i := range rv.Len() {
			out.Index(i).Set(caller.call(rv.Index(i)))
		}
		return out.Interface()
	},
	// The element type comes from the closure's result, so an input whose
	// static type is unknown still yields a precisely-typed output.
	ReturnType: func(argTypes []reflect.Type) (reflect.Type, error) {
		if len(argTypes) < 2 || argTypes[1] == nil || argTypes[1].Kind() != reflect.Func {
			return nil, fmt.Errorf("%s: second argument must be a function", FuncMap)
		}
		if argTypes[1].NumOut() != 1 {
			return nil, fmt.Errorf("%s: function must return exactly 1 value, got %s", FuncMap, argTypes[1])
		}
		return reflect.SliceOf(argTypes[1].Out(0)), nil
	},
}

// keyFunc turns unique_by / group_by's second argument into an element→key
// function. Two call shapes share one name, dispatched on the argument's
// runtime type:
//
//	unique_by(items, func(e T) K { … })   closure — checked at compile time
//	unique_by(items, "spec.hostname")     attribute path — for `any`-shaped data
//
// The string form supersedes scriggo/builtin's UniqueBy/GroupBy, which the
// chart already uses (`group_by(allRoutes, "pathKey")`). Unlike those, it
// splits a dotted path into separate dig keys, so `"spec.hostname"` navigates
// two levels instead of looking up one key literally named "spec.hostname" and
// silently finding nothing — the failure mode selectattr still has.
//
// key may be nil, meaning whole-element identity.
func keyFunc(name string, key any) func(reflect.Value) any {
	if key == nil {
		return func(e reflect.Value) any { return e.Interface() }
	}
	if path, ok := key.(string); ok {
		if strings.TrimSpace(path) == "" {
			panic(fmt.Sprintf("%s: attribute path must not be empty", name))
		}
		keys := strings.Split(path, ".")
		return func(e reflect.Value) any { return scriggoToString(scriggoDig(e.Interface(), keys...)) }
	}
	caller := newElemCaller(funcArg(name, key))
	return func(e reflect.Value) any { return caller.call(e).Interface() }
}

// dedupe keeps the first element per key, preserving input order.
func dedupe(name string, slice, key any) any {
	rv, ok := sliceOf(slice)
	if !ok {
		return slice
	}
	extract := keyFunc(name, key)
	seen := make(map[any]bool, rv.Len())
	out := reflect.MakeSlice(rv.Type(), 0, rv.Len())
	for i := range rv.Len() {
		elem := rv.Index(i)
		k := extract(elem)
		if k != nil && !reflect.TypeOf(k).Comparable() {
			panic(fmt.Sprintf("%s: key type %T is not comparable — return a string or another comparable value", name, k))
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = reflect.Append(out, elem)
	}
	return out.Interface()
}

// scriggoUniqueAdaptive keeps the first occurrence of each distinct element,
// preserving input order. Order matters: a reordered map file looks like a
// change to the controller and costs a reload.
var scriggoUniqueAdaptive = native.AdaptiveFunc{
	Impl:       func(slice any) any { return dedupe(FuncUnique, slice, nil) },
	ReturnType: identityReturnType,
}

// scriggoUniqueByAdaptive keeps the first element per key.
//
//	pairs | unique_by(func(p Pair) string { return p.Addr })
var scriggoUniqueByAdaptive = native.AdaptiveFunc{
	Impl:       func(slice, key any) any { return dedupe(FuncUniqueBy, slice, key) },
	ReturnType: identityReturnType,
}

// scriggoGroupByAdaptive buckets elements by a string key, preserving input
// order within each bucket. Iterate the result through keys() so the rendered
// output is deterministic — Go map order is not.
var scriggoGroupByAdaptive = native.AdaptiveFunc{
	Impl: func(slice, key any) any {
		extract := keyFunc(FuncGroupBy, key)
		rv, ok := sliceOf(slice)
		if !ok {
			rv = reflect.MakeSlice(reflect.SliceOf(anyType), 0, 0)
		}
		out := reflect.MakeMap(reflect.MapOf(reflect.TypeOf(""), rv.Type()))
		for i := range rv.Len() {
			elem := rv.Index(i)
			k := reflect.ValueOf(scriggoToString(extract(elem)))
			bucket := out.MapIndex(k)
			if !bucket.IsValid() {
				bucket = reflect.MakeSlice(rv.Type(), 0, 1)
			}
			out.SetMapIndex(k, reflect.Append(bucket, elem))
		}
		return out.Interface()
	},
	ReturnType: func(argTypes []reflect.Type) (reflect.Type, error) {
		elem := reflect.SliceOf(anyType)
		if len(argTypes) > 0 && argTypes[0] != nil && argTypes[0].Kind() == reflect.Slice {
			elem = argTypes[0]
		}
		return reflect.MapOf(reflect.TypeOf(""), elem), nil
	},
}
