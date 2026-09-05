// Copyright 2026 Philipp Hossner
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
	"strings"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

func incrementalKeyExtractor(name string, key any) func(reflect.Value) (any, error) {
	if key == nil {
		return func(element reflect.Value) (any, error) { return element.Interface(), nil }
	}
	if path, ok := key.(string); ok {
		if strings.TrimSpace(path) == "" {
			panic(fmt.Sprintf("%s: attribute path must not be empty", name))
		}
		parts := strings.Split(path, ".")
		return func(element reflect.Value) (any, error) {
			value, found, err := incrementalDigValue(element.Interface(), parts)
			if !found {
				value = nil
			}
			return value, err
		}
	}
	caller := newElemCaller(funcArg(name, key))
	return func(element reflect.Value) (any, error) { return caller.call(element).Interface(), nil }
}

func incrementalDedupe(env native.Env, name string, slice, key any) any {
	rv, ok := sliceOf(slice)
	if !ok {
		return slice
	}
	extract := incrementalKeyExtractor(name, key)
	seen := make(map[deterministicScalarKey]struct{}, rv.Len())
	result := reflect.MakeSlice(rv.Type(), 0, rv.Len())
	for i := range rv.Len() {
		element := rv.Index(i)
		value, err := extract(element)
		if err != nil {
			incrementalStop(env, name, fmt.Errorf("key at index %d: %w", i, err))
			return nil
		}
		scalar, err := deterministicScalarOf(value)
		if err != nil {
			incrementalStop(env, name, fmt.Errorf("key at index %d: %w", i, err))
			return nil
		}
		key := scalar.key()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = reflect.Append(result, element)
	}
	return result.Interface()
}

var incrementalUniqueAdaptive = native.AdaptiveFunc{
	Impl: func(env native.Env, slice any) any {
		return incrementalDedupe(env, FuncUnique, slice, nil)
	},
	ReturnType: identityReturnType,
}

var incrementalUniqueByAdaptive = native.AdaptiveFunc{
	Impl: func(env native.Env, slice, key any) any {
		return incrementalDedupe(env, FuncUniqueBy, slice, key)
	},
	ReturnType:   identityReturnType,
	LambdaParams: elementLambdaParams,
}

var incrementalGroupByAdaptive = native.AdaptiveFunc{
	Impl: incrementalGroupBy,
	ReturnType: func(argumentTypes []reflect.Type) (reflect.Type, error) {
		elements := reflect.SliceOf(anyType)
		if len(argumentTypes) > 0 && argumentTypes[0] != nil && argumentTypes[0].Kind() == reflect.Slice {
			elements = argumentTypes[0]
		}
		return reflect.MapOf(reflect.TypeOf(""), elements), nil
	},
	LambdaParams: elementLambdaParams,
}

func incrementalGroupBy(env native.Env, slice, key any) any {
	extract := incrementalKeyExtractor(FuncGroupBy, key)
	rv, ok := sliceOf(slice)
	if !ok {
		rv = reflect.MakeSlice(reflect.SliceOf(anyType), 0, 0)
	}
	result := reflect.MakeMap(reflect.MapOf(reflect.TypeOf(""), rv.Type()))
	displays := make(map[string]deterministicScalarKey, rv.Len())
	for i := range rv.Len() {
		element := rv.Index(i)
		value, err := extract(element)
		if err != nil {
			incrementalStop(env, FuncGroupBy, fmt.Errorf("key at index %d: %w", i, err))
			return nil
		}
		scalar, err := deterministicScalarOf(value)
		if err != nil {
			incrementalStop(env, FuncGroupBy, fmt.Errorf("key at index %d: %w", i, err))
			return nil
		}
		if err := rememberDeterministicDisplay(displays, scalar); err != nil {
			incrementalStop(env, FuncGroupBy, err)
			return nil
		}
		mapKey := reflect.ValueOf(scalar.text)
		bucket := result.MapIndex(mapKey)
		if !bucket.IsValid() {
			bucket = reflect.MakeSlice(rv.Type(), 0, 1)
		}
		result.SetMapIndex(mapKey, reflect.Append(bucket, element))
	}
	return result.Interface()
}

func incrementalCountBy(env native.Env, items any, keyPath string) map[string]int {
	result := make(map[string]int)
	rv, ok := incrementalKeyedSlice(env, builtinCountBy, items)
	if !ok {
		return result
	}
	displays := make(map[string]deterministicScalarKey, rv.Len())
	for i := range rv.Len() {
		scalar, valid := incrementalPathScalar(env, builtinCountBy, rv.Index(i).Interface(), keyPath, i)
		if !valid {
			return nil
		}
		if err := rememberDeterministicDisplay(displays, scalar); err != nil {
			incrementalStop(env, builtinCountBy, err)
			return nil
		}
		result[scalar.text]++
	}
	return result
}

func incrementalIndexBy(env native.Env, items any, keyPath string) map[string]any {
	result := make(map[string]any)
	rv, ok := incrementalKeyedSlice(env, builtinIndexBy, items)
	if !ok {
		return result
	}
	displays := make(map[string]deterministicScalarKey, rv.Len())
	for i := range rv.Len() {
		item := rv.Index(i).Interface()
		scalar, valid := incrementalPathScalar(env, builtinIndexBy, item, keyPath, i)
		if !valid {
			return nil
		}
		if err := rememberDeterministicDisplay(displays, scalar); err != nil {
			incrementalStop(env, builtinIndexBy, err)
			return nil
		}
		result[scalar.text] = item
	}
	return result
}

func incrementalKeyedSlice(env native.Env, name string, items any) (reflect.Value, bool) {
	if items == nil {
		return reflect.Value{}, false
	}
	rv := reflect.ValueOf(items)
	if rv.Kind() != reflect.Slice {
		incrementalStop(env, name, fmt.Errorf("expected a slice, got %T", items))
		return reflect.Value{}, false
	}
	return rv, true
}

func incrementalPathScalar(
	env native.Env,
	name string,
	item any,
	keyPath string,
	index int,
) (deterministicScalar, bool) {
	value := item
	if keyPath != "" {
		var err error
		var found bool
		value, found, err = incrementalDigValue(item, strings.Split(keyPath, "."))
		if err != nil {
			incrementalStop(env, name, fmt.Errorf("key at index %d: %w", index, err))
			return deterministicScalar{}, false
		}
		if !found {
			value = nil
		}
	}
	scalar, err := deterministicScalarOf(value)
	if err != nil {
		incrementalStop(env, name, fmt.Errorf("key at index %d: %w", index, err))
		return deterministicScalar{}, false
	}
	return scalar, true
}

func incrementalSelectAttr(env native.Env, items any, attribute string, args ...any) []any {
	itemsSlice, ok := toSlice(items)
	if items == nil || !ok {
		return []any{}
	}
	test, testValue := incrementalSelectAttrTest(args)
	result := make([]any, 0, len(itemsSlice))
	for _, item := range itemsSlice {
		if item == nil {
			continue
		}
		attributeValue, found, err := incrementalDigValue(item, []string{attribute})
		if err != nil {
			incrementalStop(env, FuncSelectAttr, err)
			return nil
		}
		if !found {
			attributeValue = nil
		}
		matched, valid := incrementalSelectAttrMatch(env, test, attributeValue, testValue)
		if !valid {
			return nil
		}
		if matched {
			result = append(result, item)
		}
	}
	return result
}

func incrementalSelectAttrTest(args []any) (test string, value any) {
	if len(args) < 2 {
		return "", nil
	}
	test, _ = args[0].(string)
	return test, args[1]
}

func incrementalSelectAttrMatch(
	env native.Env,
	test string,
	attributeValue, testValue any,
) (matched, valid bool) {
	switch test {
	case "eq", "ne":
		left, err := deterministicScalarOf(attributeValue)
		if err != nil {
			incrementalStop(env, FuncSelectAttr, err)
			return false, false
		}
		right, err := deterministicScalarOf(testValue)
		if err != nil {
			incrementalStop(env, FuncSelectAttr, err)
			return false, false
		}
		equal := left.key() == right.key()
		return equal == (test == "eq"), true
	case "in":
		matched, err := deterministicScalarInSlice(attributeValue, testValue)
		if err != nil {
			incrementalStop(env, FuncSelectAttr, err)
			return false, false
		}
		return matched, true
	default:
		return attributeValue != nil, true
	}
}

func deterministicScalarInSlice(value, list any) (bool, error) {
	wanted, err := deterministicScalarOf(value)
	if err != nil {
		return false, err
	}
	if list == nil {
		return false, nil
	}
	rv := reflect.ValueOf(list)
	if rv.Kind() != reflect.Slice {
		return false, fmt.Errorf("membership operand must be a slice, got %T", list)
	}
	for i := range rv.Len() {
		candidate, err := deterministicScalarOf(rv.Index(i).Interface())
		if err != nil {
			return false, fmt.Errorf("membership key at index %d: %w", i, err)
		}
		if candidate.key() == wanted.key() {
			return true, nil
		}
	}
	return false, nil
}
