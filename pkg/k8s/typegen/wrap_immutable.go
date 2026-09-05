// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package typegen

import (
	"math"
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"
)

var immutableProjectionTypes sync.Map

// WrapImmutableInto projects an immutable JSON object directly into a generated type.
func WrapImmutableInto(obj map[string]any, typ reflect.Type) (reflect.Value, error) {
	if _, generated := immutableProjectionTypes.Load(typ); generated {
		if value, ok := projectImmutableJSON(obj, typ); ok {
			return value, nil
		}
	}
	return WrapInto(obj, typ)
}

// WrapImmutableIntoPointer projects immutable JSON into one fresh generated value.
func WrapImmutableIntoPointer(obj map[string]any, typ reflect.Type) (reflect.Value, error) {
	if typ == nil {
		return wrapIntoPointer(obj, typ)
	}
	if _, generated := immutableProjectionTypes.Load(typ); generated {
		pointer := reflect.New(typ)
		if projectImmutableJSONInto(obj, pointer.Elem()) {
			return pointer, nil
		}
	}
	return wrapIntoPointer(obj, typ)
}

func registerImmutableProjectionType(typ reflect.Type) {
	if typ != nil {
		immutableProjectionTypes.Store(typ, struct{}{})
	}
}

func projectImmutableJSON(source any, target reflect.Type) (reflect.Value, bool) {
	if source == nil {
		return reflect.Zero(target), true
	}
	if value, projected, handled := projectImmutableJSONScalar(source, target); handled {
		return value, projected
	}
	result := reflect.New(target).Elem()
	if !projectImmutableJSONInto(source, result) {
		return reflect.Value{}, false
	}
	return result, true
}

func projectImmutableJSONScalar(source any, target reflect.Type) (reflect.Value, bool, bool) {
	var value reflect.Value
	switch target.Kind() {
	case reflect.String:
		typed, ok := source.(string)
		if !ok || !utf8.ValidString(typed) {
			return reflect.Value{}, false, true
		}
		value = reflect.ValueOf(typed)
	case reflect.Bool:
		typed, ok := source.(bool)
		if !ok {
			return reflect.Value{}, false, true
		}
		value = reflect.ValueOf(typed)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		typed, ok := immutableJSONInt(source)
		if !ok || target.OverflowInt(typed) {
			return reflect.Value{}, false, true
		}
		value = reflect.ValueOf(typed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		typed, ok := immutableJSONUint(source)
		if !ok || target.OverflowUint(typed) {
			return reflect.Value{}, false, true
		}
		value = reflect.ValueOf(typed)
	case reflect.Float32, reflect.Float64:
		typed, ok := immutableJSONFloat(source)
		if !ok || target.OverflowFloat(typed) {
			return reflect.Value{}, false, true
		}
		value = reflect.ValueOf(typed)
	default:
		return reflect.Value{}, false, false
	}
	if value.Type() != target {
		value = value.Convert(target)
	}
	return value, true, true
}

func projectImmutableJSONInto(source any, result reflect.Value) bool {
	if source == nil {
		result.SetZero()
		return true
	}
	target := result.Type()
	switch target.Kind() {
	case reflect.Pointer:
		result.Set(reflect.New(target.Elem()))
		return projectImmutableJSONInto(source, result.Elem())
	case reflect.Interface:
		return projectImmutableJSONInterfaceInto(source, target, result)
	case reflect.Struct:
		return projectImmutableJSONStructInto(source, target, result)
	case reflect.Map:
		return projectImmutableJSONMapInto(source, target, result)
	case reflect.Slice:
		return projectImmutableJSONSliceInto(source, target, result)
	case reflect.Array:
		return projectImmutableJSONArrayInto(source, target, result)
	default:
		return projectImmutableJSONScalarInto(source, target, result)
	}
}

func projectImmutableJSONInterfaceInto(source any, target reflect.Type, result reflect.Value) bool {
	if target.NumMethod() != 0 {
		return false
	}
	cloned, ok := cloneImmutableJSONInterface(source)
	if !ok {
		return false
	}
	if cloned != nil {
		result.Set(reflect.ValueOf(cloned))
	}
	return true
}

func projectImmutableJSONStructInto(source any, target reflect.Type, result reflect.Value) bool {
	object, ok := source.(map[string]any)
	if !ok {
		return false
	}
	for index := range target.NumField() {
		field := target.Field(index)
		name := fieldJSONName(&field)
		if name == "" {
			continue
		}
		sourceValue, exists := object[name]
		if !exists {
			continue
		}
		if !projectImmutableJSONInto(sourceValue, result.Field(index)) {
			return false
		}
	}
	return true
}

func projectImmutableJSONMapInto(source any, target reflect.Type, result reflect.Value) bool {
	if target.Key().Kind() != reflect.String {
		return false
	}
	object, ok := source.(map[string]any)
	if !ok {
		return false
	}
	if object == nil {
		return true
	}
	result.Set(reflect.MakeMapWithSize(target, len(object)))
	for key, sourceValue := range object {
		if !utf8.ValidString(key) {
			return false
		}
		value, projected := projectImmutableJSON(sourceValue, target.Elem())
		if !projected {
			return false
		}
		result.SetMapIndex(reflect.ValueOf(key).Convert(target.Key()), value)
	}
	return true
}

func projectImmutableJSONSliceInto(source any, target reflect.Type, result reflect.Value) bool {
	array, ok := source.([]any)
	if !ok {
		return false
	}
	if array == nil {
		return true
	}
	result.Set(reflect.MakeSlice(target, len(array), len(array)))
	for index, sourceValue := range array {
		if !projectImmutableJSONInto(sourceValue, result.Index(index)) {
			return false
		}
	}
	return true
}

func projectImmutableJSONArrayInto(source any, target reflect.Type, result reflect.Value) bool {
	array, ok := source.([]any)
	if !ok {
		return false
	}
	for index := range min(len(array), target.Len()) {
		if !projectImmutableJSONInto(array[index], result.Index(index)) {
			return false
		}
	}
	return true
}

func projectImmutableJSONScalarInto(source any, target reflect.Type, result reflect.Value) bool {
	switch target.Kind() {
	case reflect.String:
		value, ok := source.(string)
		if !ok || !utf8.ValidString(value) {
			return false
		}
		result.SetString(value)
		return true
	case reflect.Bool:
		value, ok := source.(bool)
		if !ok {
			return false
		}
		result.SetBool(value)
		return true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value, ok := immutableJSONInt(source)
		if !ok || target.OverflowInt(value) {
			return false
		}
		result.SetInt(value)
		return true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value, ok := immutableJSONUint(source)
		if !ok || target.OverflowUint(value) {
			return false
		}
		result.SetUint(value)
		return true
	case reflect.Float32, reflect.Float64:
		value, ok := immutableJSONFloat(source)
		if !ok || target.OverflowFloat(value) {
			return false
		}
		result.SetFloat(value)
		return true
	default:
		return false
	}
}

func fieldJSONName(field *reflect.StructField) string {
	name := field.Tag.Get("json")
	if comma := strings.IndexByte(name, ','); comma >= 0 {
		name = name[:comma]
	}
	if name == "-" {
		return ""
	}
	if name == "" {
		return field.Name
	}
	return name
}

func immutableJSONInt(source any) (int64, bool) {
	switch value := source.(type) {
	case int64:
		return value, true
	case uint64:
		if value <= math.MaxInt64 {
			return int64(value), true
		}
	}
	return 0, false
}

func immutableJSONUint(source any) (uint64, bool) {
	switch value := source.(type) {
	case int64:
		if value >= 0 {
			return uint64(value), true
		}
	case uint64:
		return value, true
	}
	return 0, false
}

func immutableJSONFloat(source any) (float64, bool) {
	switch value := source.(type) {
	case int64:
		return float64(value), true
	case uint64:
		return float64(value), true
	case float64:
		return value, !math.IsNaN(value) && !math.IsInf(value, 0)
	}
	return 0, false
}

func cloneImmutableJSONInterface(source any) (any, bool) {
	switch value := source.(type) {
	case nil, bool:
		return value, true
	case string:
		return value, utf8.ValidString(value)
	case int64:
		return float64(value), true
	case uint64:
		return float64(value), true
	case float64:
		return value, !math.IsNaN(value) && !math.IsInf(value, 0)
	case map[string]any:
		return cloneImmutableJSONMap(value)
	case []any:
		return cloneImmutableJSONSlice(value)
	default:
		return nil, false
	}
}

func cloneImmutableJSONMap(value map[string]any) (any, bool) {
	if value == nil {
		return nil, true
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		if !utf8.ValidString(key) {
			return nil, false
		}
		cloned, ok := cloneImmutableJSONInterface(item)
		if !ok {
			return nil, false
		}
		result[key] = cloned
	}
	return result, true
}

func cloneImmutableJSONSlice(value []any) (any, bool) {
	if value == nil {
		return nil, true
	}
	result := make([]any, len(value))
	for index, item := range value {
		cloned, ok := cloneImmutableJSONInterface(item)
		if !ok {
			return nil, false
		}
		result[index] = cloned
	}
	return result, true
}
