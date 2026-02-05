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
	"strconv"
	"strings"
)

// evaluateExpression evaluates a JSONPath-like expression against an item.
// Simplified version for Scriggo template engine.
func evaluateExpression(item interface{}, expr string) interface{} {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}

	// Handle JSONPath-like navigation
	if strings.HasPrefix(expr, "$.") || expr == "$" {
		return navigateJSONPath(item, expr)
	}

	// Treat as field name without $. prefix
	return navigateJSONPath(item, "$."+expr)
}

// navigateJSONPath navigates through an object using JSONPath-like syntax.
func navigateJSONPath(item interface{}, path string) interface{} {
	// Remove leading $. if present
	if strings.HasPrefix(path, "$.") {
		path = strings.TrimPrefix(path, "$.")
	} else if path == "$" {
		return item
	}

	if path == "" {
		return item
	}

	// Split path into segments
	segments := strings.Split(path, ".")
	current := item

	for _, segment := range segments {
		if current == nil {
			return nil
		}

		// Handle array index access
		if strings.HasPrefix(segment, "[") && strings.HasSuffix(segment, "]") {
			indexStr := strings.TrimPrefix(strings.TrimSuffix(segment, "]"), "[")
			index, err := strconv.Atoi(indexStr)
			if err != nil {
				return nil
			}

			// Try to convert to slice
			if slice, ok := convertToSlice(current); ok {
				if index >= 0 && index < len(slice) {
					current = slice[index]
					continue
				}
			}
			return nil
		}

		// Regular field access via map or struct
		current = getField(current, segment)
	}

	return current
}

// getField retrieves a field from a map or struct.
func getField(item interface{}, fieldName string) interface{} {
	if item == nil {
		return nil
	}

	// Try map access first
	if m, ok := convertToMap(item); ok {
		if val, exists := m[fieldName]; exists {
			return val
		}
		return nil
	}

	// Try struct field access via reflection
	val := reflect.ValueOf(item)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}

	if val.Kind() == reflect.Struct {
		field := val.FieldByName(fieldName)
		if field.IsValid() {
			return field.Interface()
		}
	}

	return nil
}

// compareValues compares two values for sorting.
func compareValues(a, b interface{}) int {
	// Handle nil values
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return 1 // nil is considered greater (sorts to end)
	}
	if b == nil {
		return -1
	}

	// Try numeric comparison first
	if numA, okA := toFloat64(a); okA {
		if numB, okB := toFloat64(b); okB {
			if numA < numB {
				return -1
			} else if numA > numB {
				return 1
			}
			return 0
		}
	}

	// Fast path: direct string comparison (avoids fmt.Sprint allocation)
	if strA, okA := a.(string); okA {
		if strB, okB := b.(string); okB {
			return strings.Compare(strA, strB)
		}
	}

	// Fallback: convert to string via fmt.Sprint
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

// getLength returns the length of a value.
func getLength(v interface{}) int {
	if v == nil {
		return 0
	}

	if s, ok := v.(string); ok {
		return len(s)
	}

	if slice, ok := convertToSlice(v); ok {
		return len(slice)
	}

	if m, ok := convertToMap(v); ok {
		return len(m)
	}

	// Use reflection as fallback
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Slice || val.Kind() == reflect.Array || val.Kind() == reflect.Map {
		return val.Len()
	}

	return 0
}

// toFloat64 converts a value to float64 if possible.
func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int8:
		return float64(val), true
	case int16:
		return float64(val), true
	case int32:
		return float64(val), true
	case int64:
		return float64(val), true
	case uint:
		return float64(val), true
	case uint8:
		return float64(val), true
	case uint16:
		return float64(val), true
	case uint32:
		return float64(val), true
	case uint64:
		return float64(val), true
	default:
		return 0, false
	}
}

// convertToSlice tries to convert a value to []interface{}.
func convertToSlice(v interface{}) ([]interface{}, bool) {
	if v == nil {
		return nil, false
	}

	// Direct type assertion for []interface{}
	if slice, ok := v.([]interface{}); ok {
		return slice, true
	}

	// Use reflection for other slice types
	val := reflect.ValueOf(v)
	if val.Kind() != reflect.Slice && val.Kind() != reflect.Array {
		return nil, false
	}

	result := make([]interface{}, val.Len())
	for i := 0; i < val.Len(); i++ {
		result[i] = val.Index(i).Interface()
	}
	return result, true
}

// convertToMap tries to convert a value to map[string]interface{}.
func convertToMap(v interface{}) (map[string]interface{}, bool) {
	if v == nil {
		return nil, false
	}

	// Direct type assertion
	if m, ok := v.(map[string]interface{}); ok {
		return m, true
	}

	// Use reflection for struct to map conversion
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}

	if val.Kind() == reflect.Map {
		result := make(map[string]interface{})
		iter := val.MapRange()
		for iter.Next() {
			key := fmt.Sprint(iter.Key().Interface())
			result[key] = iter.Value().Interface()
		}
		return result, true
	}

	return nil, false
}
