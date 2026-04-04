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
	"math"
	"reflect"
	"strconv"
)

// scriggoToString converts any value to its string representation.
//
// Usage in Scriggo templates:
//
//	{% var s = tostring(port) %}
func scriggoToString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// scriggoToInt converts a value to int.
//
// Usage in Scriggo templates:
//
//	{% var n = toint(port_string) %}
func scriggoToInt(v any) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	case string:
		i, err := strconv.Atoi(val)
		if err != nil {
			return 0
		}
		return i
	default:
		return 0
	}
}

// scriggoToFloat converts a value to float64.
//
// Usage in Scriggo templates:
//
//	{% var f = tofloat(value) %}
func scriggoToFloat(v any) (float64, error) {
	if v == nil {
		return 0, nil
	}
	switch val := v.(type) {
	case float64:
		return val, nil
	case int:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case string:
		return strconv.ParseFloat(val, 64)
	default:
		return 0, fmt.Errorf("converting %T to float", v)
	}
}

// scriggoToStringSlice converts []any to []string.
// Each element is converted to string via fmt.Sprintf.
//
// Usage in Scriggo templates:
//
//	{%- var hosts = toStringSlice(hostnames) -%}
func scriggoToStringSlice(items any) []string {
	if items == nil {
		return []string{}
	}
	switch v := items.(type) {
	case []string:
		return v
	case []any:
		result := make([]string, len(v))
		for i, item := range v {
			result[i] = fmt.Sprintf("%v", item)
		}
		return result
	default:
		return []string{}
	}
}

// scriggoToSlice converts any value to []any for safe ranging.
// Returns an empty slice if input is nil, otherwise converts to []any.
// This is necessary in Scriggo because Kubernetes resource fields may be nil
// and we need to safely iterate over them.
//
// Usage in Scriggo templates:
//
//	{# Safe iteration over potentially nil value #}
//	{%- for _, item := range toSlice(spec_rules) %}
//	  ... process item ...
//	{%- end %}
func scriggoToSlice(items any) []any {
	if items == nil {
		return []any{}
	}
	result, _ := toSlice(items)
	return result
}

// toSlice converts an any to []any.
func toSlice(items any) ([]any, bool) {
	if items == nil {
		return nil, false
	}

	// Already a slice of interfaces
	if slice, ok := items.([]any); ok {
		return slice, true
	}

	// Use reflection for other slice types
	rv := reflect.ValueOf(items)
	if rv.Kind() != reflect.Slice {
		return nil, false
	}

	result := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		result[i] = rv.Index(i).Interface()
	}
	return result, true
}

// scriggoCeil returns the ceiling of a float.
//
// Usage in Scriggo templates:
//
//	{% var n = ceil(ratio) %}
func scriggoCeil(v float64) float64 {
	return math.Ceil(v)
}

// scriggoSeq generates a sequence of integers from 0 to n-1.
// This is the Scriggo equivalent of Python's range() for iteration.
//
// Usage in Scriggo templates:
//
//	{%- for _, i := range seq(weight) -%}
//	  {{ i }}
//	{%- end -%}
func scriggoSeq(n int) []int {
	if n <= 0 {
		return []int{}
	}
	result := make([]int, n)
	for i := range n {
		result[i] = i
	}
	return result
}

// isNilValue checks if a value is nil, including typed nil values like (*T)(nil).
// In Go, a typed nil pointer stored in an any is not equal to nil.
// This function uses reflection to check for nil pointers, maps, slices, etc.
func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
		return rv.IsNil()
	}
	return false
}

// scriggoIsNil is the template-exposed version of isNilValue.
// It checks if a value is nil, including typed nil pointers like (*T)(nil).
//
// In Scriggo templates, comparing a typed nil pointer to nil doesn't work:
//
//	{%- if currentConfig != nil %}  // This fails with typed nil pointers!
//
// Instead, use isNil:
//
//	{%- if !isNil(currentConfig) %}  // This works correctly
//
// Returns true if the value is nil (including nil pointers, maps, slices, channels, funcs).
func scriggoIsNil(value any) bool {
	return isNilValue(value)
}
