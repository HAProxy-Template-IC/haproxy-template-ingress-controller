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
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriggoSortBy_EmptySlice(t *testing.T) {
	items := []interface{}{}
	criteria := []string{"$.name"}

	result, err := scriggoSortBy(items, criteria)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestScriggoSortBy_EmptyCriteria(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{"name": "b"},
		map[string]interface{}{"name": "a"},
	}

	result, err := scriggoSortBy(items, []string{})
	require.NoError(t, err)
	assert.Len(t, result, 2)
	// Should return original order when no criteria
	assert.Equal(t, "b", result[0].(map[string]interface{})["name"])
	assert.Equal(t, "a", result[1].(map[string]interface{})["name"])
}

func TestScriggoSortBy_SingleCriteria(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{"name": "charlie"},
		map[string]interface{}{"name": "alice"},
		map[string]interface{}{"name": "bob"},
	}
	criteria := []string{"$.name"}

	result, err := scriggoSortBy(items, criteria)
	require.NoError(t, err)
	require.Len(t, result, 3)

	assert.Equal(t, "alice", result[0].(map[string]interface{})["name"])
	assert.Equal(t, "bob", result[1].(map[string]interface{})["name"])
	assert.Equal(t, "charlie", result[2].(map[string]interface{})["name"])
}

func TestScriggoSortBy_Descending(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{"priority": 1},
		map[string]interface{}{"priority": 3},
		map[string]interface{}{"priority": 2},
	}
	criteria := []string{"$.priority:desc"}

	result, err := scriggoSortBy(items, criteria)
	require.NoError(t, err)
	require.Len(t, result, 3)

	assert.Equal(t, 3, result[0].(map[string]interface{})["priority"])
	assert.Equal(t, 2, result[1].(map[string]interface{})["priority"])
	assert.Equal(t, 1, result[2].(map[string]interface{})["priority"])
}

func TestScriggoSortBy_MultipleCriteria(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{"type": "b", "priority": 1},
		map[string]interface{}{"type": "a", "priority": 2},
		map[string]interface{}{"type": "a", "priority": 1},
	}
	criteria := []string{"$.type", "$.priority"}

	result, err := scriggoSortBy(items, criteria)
	require.NoError(t, err)
	require.Len(t, result, 3)

	// First sort by type (a, a, b), then by priority within same type
	assert.Equal(t, "a", result[0].(map[string]interface{})["type"])
	assert.Equal(t, 1, result[0].(map[string]interface{})["priority"])
	assert.Equal(t, "a", result[1].(map[string]interface{})["type"])
	assert.Equal(t, 2, result[1].(map[string]interface{})["priority"])
	assert.Equal(t, "b", result[2].(map[string]interface{})["type"])
}

func TestScriggoSortBy_ExistsModifier(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{"name": "without-method"},
		map[string]interface{}{"name": "with-method", "method": "GET"},
		map[string]interface{}{"name": "also-without"},
	}
	criteria := []string{"$.method:exists:desc"}

	result, err := scriggoSortBy(items, criteria)
	require.NoError(t, err)
	require.Len(t, result, 3)

	// Items with method field should come first (exists:desc)
	assert.Equal(t, "with-method", result[0].(map[string]interface{})["name"])
}

func TestScriggoSortBy_LengthModifier(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{"name": "a", "items": []interface{}{"x", "y"}},
		map[string]interface{}{"name": "b", "items": []interface{}{"x", "y", "z"}},
		map[string]interface{}{"name": "c", "items": []interface{}{"x"}},
	}
	criteria := []string{"$.items | length:desc"}

	result, err := scriggoSortBy(items, criteria)
	require.NoError(t, err)
	require.Len(t, result, 3)

	// Sort by items length descending
	assert.Equal(t, "b", result[0].(map[string]interface{})["name"]) // 3 items
	assert.Equal(t, "a", result[1].(map[string]interface{})["name"]) // 2 items
	assert.Equal(t, "c", result[2].(map[string]interface{})["name"]) // 1 item
}

func TestScriggoSortBy_DoesNotModifyOriginal(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{"name": "c"},
		map[string]interface{}{"name": "a"},
		map[string]interface{}{"name": "b"},
	}
	criteria := []string{"$.name"}

	// Keep original order reference
	originalFirst := items[0].(map[string]interface{})["name"]

	result, err := scriggoSortBy(items, criteria)
	require.NoError(t, err)

	// Original slice should not be modified
	assert.Equal(t, originalFirst, items[0].(map[string]interface{})["name"])
	// Result should be sorted
	assert.Equal(t, "a", result[0].(map[string]interface{})["name"])
}

func TestScriggoGlobMatch_EmptySlice(t *testing.T) {
	items := []interface{}{}
	pattern := "*"

	result := scriggoGlobMatch(items, pattern)
	assert.Empty(t, result)
}

func TestScriggoGlobMatch_EmptyPattern(t *testing.T) {
	items := []interface{}{"a", "b", "c"}

	// Empty pattern returns empty slice (early return optimization)
	result := scriggoGlobMatch(items, "")
	assert.Empty(t, result)
}

func TestScriggoGlobMatch_StringItems(t *testing.T) {
	items := []interface{}{"backend-api", "backend-web", "frontend-ui", "frontend-admin"}
	pattern := "backend-*"

	result := scriggoGlobMatch(items, pattern)
	require.Len(t, result, 2)

	assert.Equal(t, "backend-api", result[0])
	assert.Equal(t, "backend-web", result[1])
}

func TestScriggoGlobMatch_MapItems(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{"name": "backend-api"},
		map[string]interface{}{"name": "backend-web"},
		map[string]interface{}{"name": "frontend-ui"},
	}
	pattern := "backend-*"

	result := scriggoGlobMatch(items, pattern)
	require.Len(t, result, 2)

	// glob_match now returns []string with matched names
	assert.Equal(t, "backend-api", result[0])
	assert.Equal(t, "backend-web", result[1])
}

func TestScriggoGlobMatch_NoMatches(t *testing.T) {
	items := []interface{}{"abc", "def", "ghi"}
	pattern := "xyz*"

	result := scriggoGlobMatch(items, pattern)
	assert.Empty(t, result)
}

func TestScriggoGlobMatch_InvalidPattern(t *testing.T) {
	items := []interface{}{"test"}
	pattern := "[" // Invalid glob pattern

	// scriggoGlobMatch panics on invalid pattern
	assert.Panics(t, func() {
		scriggoGlobMatch(items, pattern)
	})
}

func TestScriggoGlobMatch_ItemsWithoutName(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{"other": "field"},
		map[string]interface{}{"name": "valid"},
		123, // Non-string, non-map item
	}
	pattern := "*"

	result := scriggoGlobMatch(items, pattern)
	// Only the item with a name field should match
	require.Len(t, result, 1)
	assert.Equal(t, "valid", result[0])
}

func TestScriggoStrip(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"  hello  ", "hello"},
		{"hello", "hello"},
		{"  ", ""},
		{"", ""},
		{"\t\nhello\t\n", "hello"},
		{"  hello world  ", "hello world"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := scriggoStrip(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestScriggoTrim(t *testing.T) {
	// scriggoTrim should behave identically to scriggoStrip
	tests := []struct {
		input    string
		expected string
	}{
		{"  hello  ", "hello"},
		{"hello", "hello"},
		{"  ", ""},
		{"", ""},
		{"\t\nhello\t\n", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := scriggoTrim(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestScriggoTrim_IsAliasForStrip(t *testing.T) {
	// Verify that trim and strip produce identical results
	inputs := []string{
		"  hello  ",
		"no whitespace",
		"\t\ttabs\t\t",
		"",
	}

	for _, input := range inputs {
		trimResult := scriggoTrim(input)
		stripResult := scriggoStrip(input)
		assert.Equal(t, stripResult, trimResult, "trim and strip should be identical for input: %q", input)
	}
}

func TestScriggoB64Decode_Success(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple", "SGVsbG8gV29ybGQ=", "Hello World"},
		{"empty", "", ""},
		{"short", "dGVzdA==", "test"},
		{"special chars", "SGVsbG8hIEBXb3JsZCQ=", "Hello! @World$"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := scriggoB64Decode(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestScriggoB64Decode_InvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"invalid chars", "not-valid-base64!@#"},
		{"wrong padding", "SGVsbG8====="},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := scriggoB64Decode(tt.input)
			require.Error(t, err)
		})
	}
}

// TestScriggoStrip_TypeConversion tests lenient type conversion for strip filter.
func TestScriggoStrip_TypeConversion(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{"string", "  hello  ", "hello"},
		{"int", 123, "123"},
		{"int64", int64(456), "456"},
		{"float64", 3.14, "3.14"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"nil", nil, ""},
		{"empty string", "", ""},
		{"interface with string", interface{}("  test  "), "test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scriggoStrip(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestScriggoStringsContains_TypeConversion tests both parameters accept interface{}.
func TestScriggoStringsContains_TypeConversion(t *testing.T) {
	tests := []struct {
		name     string
		s        interface{}
		substr   interface{}
		expected bool
	}{
		{"both strings", "hello world", "world", true},
		{"s is int", 12345, "234", true},
		{"substr is int", "port:8080", 8080, true},
		{"both ints", 12345, 234, true},
		{"s is float", 3.14159, ".14", true},
		{"substr is bool", "value:true", true, true},
		{"both nil", nil, nil, true}, // empty string contains empty string
		{"no match", "hello", "xyz", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scriggoStringsContains(tt.s, tt.substr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestScriggoStringsReplace_TypeConversion tests all three parameters.
func TestScriggoStringsReplace_TypeConversion(t *testing.T) {
	tests := []struct {
		name        string
		s           interface{}
		old         interface{}
		replacement interface{}
		expected    string
	}{
		{"all strings", "hello world", "world", "universe", "hello universe"},
		{"s is int", 12345, "234", "XXX", "1XXX5"},
		{"old is int", "port:8080", 8080, "9090", "port:9090"},
		{"replacement is int", "value:old", "old", 123, "value:123"},
		{"all ints", 12345, 234, 999, "19995"},
		{"mixed types", 3.14, ".", ",", "3,14"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scriggoStringsReplace(tt.s, tt.old, tt.replacement)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestScriggoB64Decode_TypeConversion tests type conversion for b64decode.
func TestScriggoB64Decode_TypeConversion(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
		wantErr  bool
	}{
		{"string", base64.StdEncoding.EncodeToString([]byte("Hello")), "Hello", false},
		{"nil", nil, "", false},                 // nil → "" → b64decode("") → ""
		{"int (invalid base64)", 123, "", true}, // 123 → "123" → error
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := scriggoB64Decode(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestScriggoDebug_Basic(t *testing.T) {
	value := map[string]interface{}{
		"name": "test",
		"port": 8080,
	}

	result := scriggoDebug(value)

	// Should contain DEBUG prefix
	assert.Contains(t, result, "# DEBUG:")
	// Should contain the JSON data
	assert.Contains(t, result, "name")
	assert.Contains(t, result, "test")
	assert.Contains(t, result, "port")
	assert.Contains(t, result, "8080")
}

func TestScriggoDebug_WithLabel(t *testing.T) {
	value := []string{"a", "b", "c"}

	result := scriggoDebug(value, "test-label")

	// Should contain DEBUG with label
	assert.Contains(t, result, "# DEBUG test-label:")
	assert.Contains(t, result, "a")
	assert.Contains(t, result, "b")
	assert.Contains(t, result, "c")
}

func TestScriggoDebug_EmptyLabel(t *testing.T) {
	value := "simple string"

	result := scriggoDebug(value, "")

	// Empty label should behave like no label
	assert.Contains(t, result, "# DEBUG:")
	assert.NotContains(t, result, "# DEBUG :")
}

func TestScriggoDebug_NilValue(t *testing.T) {
	result := scriggoDebug(nil)

	// Should handle nil values
	assert.Contains(t, result, "# DEBUG:")
	assert.Contains(t, result, "null")
}

func TestScriggoDebug_ComplexStructure(t *testing.T) {
	value := map[string]interface{}{
		"routes": []map[string]interface{}{
			{"name": "api", "priority": 10},
			{"name": "web", "priority": 5},
		},
	}

	result := scriggoDebug(value)

	// Should contain formatted output
	assert.Contains(t, result, "# DEBUG:")
	assert.Contains(t, result, "routes")
	assert.Contains(t, result, "api")
	assert.Contains(t, result, "web")
}

func TestWrapFilterForScriggo(t *testing.T) {
	// Create a simple filter
	filter := func(in interface{}, args ...interface{}) (interface{}, error) {
		return "filtered: " + in.(string), nil
	}

	wrapped := wrapFilterForScriggo(filter)
	result, err := wrapped("test", nil)

	require.NoError(t, err)
	assert.Equal(t, "filtered: test", result)
}

func TestWrapFunctionForScriggo(t *testing.T) {
	// Create a simple function
	fn := func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 {
			return "no args", nil
		}
		return "got: " + args[0].(string), nil
	}

	wrapped := wrapFunctionForScriggo(fn)

	// Test with no args
	result, err := wrapped()
	require.NoError(t, err)
	assert.Equal(t, "no args", result)

	// Test with args
	result, err = wrapped("hello")
	require.NoError(t, err)
	assert.Equal(t, "got: hello", result)
}

func TestBuildScriggoGlobals(t *testing.T) {
	customFilters := map[string]FilterFunc{
		"custom_filter": func(in interface{}, args ...interface{}) (interface{}, error) {
			return "custom: " + in.(string), nil
		},
	}

	customFunctions := map[string]GlobalFunc{
		"custom_func": func(args ...interface{}) (interface{}, error) {
			return "function result", nil
		},
	}

	globals := buildScriggoGlobals(customFilters, customFunctions)

	// Check that built-in filters are registered
	assert.Contains(t, globals, FilterSortBy)
	assert.Contains(t, globals, FilterGlobMatch)
	assert.Contains(t, globals, FilterStrip)
	assert.Contains(t, globals, FilterTrim)
	assert.Contains(t, globals, FilterB64Decode)
	assert.Contains(t, globals, FilterDebug)

	// Check that custom filter and function are registered
	assert.Contains(t, globals, "custom_filter")
	assert.Contains(t, globals, "custom_func")
}

func TestBuildScriggoGlobals_NilInputs(t *testing.T) {
	globals := buildScriggoGlobals(nil, nil)

	// Should still have built-in filters
	assert.Contains(t, globals, FilterSortBy)
	assert.Contains(t, globals, FilterGlobMatch)
	assert.Contains(t, globals, FilterStrip)
	assert.Contains(t, globals, FilterTrim)
	assert.Contains(t, globals, FilterB64Decode)
	assert.Contains(t, globals, FilterDebug)
}

// Note: Integration tests for filters in templates require context variables.
// The filter unit tests above verify filter functionality directly. The built-in
// filters (strip, trim, b64decode) are tested in engine_scriggo_test.go via
// template rendering.

// TestScriggo_BuiltinAppend verifies that Go's built-in append() works with
// []interface{} slices in Scriggo templates. This is critical for the mutable
// state pattern.
//
// Note: Scriggo requires variables to be declared at compile time. Runtime
// context variables aren't automatically in scope. Variables must either
// be declared in the template with `var` or pre-declared as globals.
func TestScriggo_BuiltinAppend(t *testing.T) {
	tests := []struct {
		name     string
		template string
		expected string
	}{
		{
			name: "append single item to empty slice",
			template: `{% var items = []interface{}{} %}
{% items = append(items, "first") %}
count: {{ len(items) }}`,
			expected: "count: 1",
		},
		{
			name: "append multiple items",
			template: `{% var items = []interface{}{} %}
{% items = append(items, "a") %}
{% items = append(items, "b") %}
{% items = append(items, "c") %}
count: {{ len(items) }}`,
			expected: "count: 3",
		},
		{
			name: "append in loop pattern",
			template: `{% var items = []interface{}{} %}
{% for i := 0; i < 3; i++ %}
{% items = append(items, i) %}
{% end %}
count: {{ len(items) }}`,
			expected: "count: 3",
		},
		{
			name: "append string items and iterate",
			template: `{% var items = []interface{}{} %}
{% items = append(items, "a") %}
{% items = append(items, "b") %}
{% for _, item := range items %}{{ item }}{% end %}`,
			expected: "ab",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := NewScriggo(
				map[string]string{"test": tt.template},
				[]string{"test"},
				nil, nil, nil,
			)
			require.NoError(t, err)

			output, err := engine.Render("test", nil)
			require.NoError(t, err)
			assert.Contains(t, output, tt.expected)
		})
	}
}

// TestScriggo_FailFunction verifies that the fail() function works correctly
// with Scriggo, propagating errors through template rendering.
//
// Note: Scriggo requires functions that return errors to have specific signatures.
// The fail function needs to be wrapped properly to propagate errors.
func TestScriggo_FailFunction(t *testing.T) {
	// First test: verify error-returning function with proper signature
	t.Run("error propagation from function", func(t *testing.T) {
		templates := map[string]string{
			"test": `{{ errorFunc("test error") }}`,
		}

		// Register a function with concrete signature that returns error
		customFunctions := map[string]GlobalFunc{
			"errorFunc": func(args ...interface{}) (interface{}, error) {
				msg := args[0].(string)
				return nil, fmt.Errorf("%s", msg)
			},
		}

		engine, err := NewScriggo(templates, []string{"test"}, nil, customFunctions, nil)
		require.NoError(t, err)

		_, err = engine.Render("test", nil)

		// Document current behavior - does error propagate?
		t.Logf("Error from function: %v", err)
		// Note: If this fails, Scriggo may not propagate errors from variadic functions
	})

	// Second test: the actual fail function with Scriggo-native implementation
	// Note: Scriggo's fail() uses env.Stop() to halt execution and returns a string
	// (which is never used) so it can be used in expression syntax {{ }}.
	t.Run("fail function propagation", func(t *testing.T) {
		templates := map[string]string{
			"test": `{{ fail("Configuration error") }}`,
		}

		customFunctions := map[string]GlobalFunc{
			"fail": FailFunction, // Passed but replaced by scriggoFail in buildScriggoGlobals
		}

		engine, err := NewScriggo(templates, []string{"test"}, nil, customFunctions, nil)
		require.NoError(t, err)

		output, err := engine.Render("test", nil)
		t.Logf("Output: %q, Error: %v", output, err)

		// The scriggoFail function uses env.Stop() to halt execution
		require.Error(t, err, "fail() should propagate error")
		assert.Contains(t, err.Error(), "Configuration error")
	})
}

// TestScriggo_FailFunction_DirectSignature tests fail with a direct Scriggo-compatible signature.
func TestScriggo_FailFunction_DirectSignature(t *testing.T) {
	templates := map[string]string{
		"test": `{{ fail("Direct error") }}`,
	}

	// Register fail as a native function with direct signature
	// Scriggo may need functions with specific signatures
	engine, err := NewScriggo(
		templates,
		[]string{"test"},
		nil,
		map[string]GlobalFunc{
			"fail": func(args ...interface{}) (interface{}, error) {
				if len(args) != 1 {
					return nil, fmt.Errorf("fail() requires one argument")
				}
				msg, ok := args[0].(string)
				if !ok {
					return nil, fmt.Errorf("fail() argument must be string")
				}
				return nil, fmt.Errorf("%s", msg)
			},
		},
		nil,
	)
	require.NoError(t, err)

	_, err = engine.Render("test", nil)
	t.Logf("Direct signature test - Error: %v", err)
}

// TestScriggoMerge tests the merge function for combining maps.
func TestScriggoMerge(t *testing.T) {
	t.Run("merge empty maps", func(t *testing.T) {
		result := scriggoMerge(
			map[string]interface{}{},
			map[string]interface{}{},
		)
		assert.Empty(t, result)
	})

	t.Run("merge into empty map", func(t *testing.T) {
		result := scriggoMerge(
			map[string]interface{}{},
			map[string]interface{}{"a": 1, "b": 2},
		)
		assert.Equal(t, map[string]interface{}{"a": 1, "b": 2}, result)
	})

	t.Run("merge empty into populated", func(t *testing.T) {
		result := scriggoMerge(
			map[string]interface{}{"a": 1, "b": 2},
			map[string]interface{}{},
		)
		assert.Equal(t, map[string]interface{}{"a": 1, "b": 2}, result)
	})

	t.Run("merge with override", func(t *testing.T) {
		result := scriggoMerge(
			map[string]interface{}{"a": 1, "b": 2},
			map[string]interface{}{"b": 3, "c": 4},
		)
		assert.Equal(t, map[string]interface{}{"a": 1, "b": 3, "c": 4}, result)
	})

	t.Run("does not modify original", func(t *testing.T) {
		original := map[string]interface{}{"a": 1}
		updates := map[string]interface{}{"b": 2}

		result := scriggoMerge(original, updates)

		// Result has both
		assert.Equal(t, map[string]interface{}{"a": 1, "b": 2}, result)
		// Original unchanged
		assert.Equal(t, map[string]interface{}{"a": 1}, original)
		// Updates unchanged
		assert.Equal(t, map[string]interface{}{"b": 2}, updates)
	})
}

// TestScriggoMerge_Integration tests merge function in actual templates.
func TestScriggoMerge_Integration(t *testing.T) {
	tests := []struct {
		name     string
		template string
		expected string
	}{
		{
			name: "basic merge",
			template: `{% var config = map[string]interface{}{"a": 1} %}
{% config = merge(config, map[string]interface{}{"b": 2}) %}
a={{ config["a"] }}, b={{ config["b"] }}`,
			expected: "a=1, b=2",
		},
		{
			name: "merge with override",
			template: `{% var config = map[string]interface{}{"a": 1, "b": 2} %}
{% config = merge(config, map[string]interface{}{"b": 99}) %}
b={{ config["b"] }}`,
			expected: "b=99",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			templates := map[string]string{"test": tt.template}
			engine, err := NewScriggo(templates, []string{"test"}, nil, nil, nil)
			require.NoError(t, err)

			output, err := engine.Render("test", nil)
			require.NoError(t, err)
			assert.Contains(t, strings.TrimSpace(output), tt.expected)
		})
	}
}

// TestScriggoKeys tests the keys function for sorted map key extraction.
func TestScriggoKeys(t *testing.T) {
	t.Run("empty map", func(t *testing.T) {
		result := scriggoKeys(map[string]interface{}{})
		assert.Empty(t, result)
	})

	t.Run("single key", func(t *testing.T) {
		result := scriggoKeys(map[string]interface{}{"a": 1})
		assert.Equal(t, []string{"a"}, result)
	})

	t.Run("keys are sorted", func(t *testing.T) {
		result := scriggoKeys(map[string]interface{}{
			"c": 3,
			"a": 1,
			"b": 2,
		})
		assert.Equal(t, []string{"a", "b", "c"}, result)
	})

	t.Run("many keys sorted", func(t *testing.T) {
		result := scriggoKeys(map[string]interface{}{
			"zebra":   1,
			"apple":   2,
			"banana":  3,
			"cherry":  4,
			"date":    5,
			"apricot": 6,
		})
		assert.Equal(t, []string{"apple", "apricot", "banana", "cherry", "date", "zebra"}, result)
	})
}

// TestScriggoKeys_Integration tests keys function in actual templates.
func TestScriggoKeys_Integration(t *testing.T) {
	tests := []struct {
		name     string
		template string
		expected string
	}{
		{
			name: "iterate keys in sorted order",
			template: `{% var config = map[string]interface{}{"c": 3, "a": 1, "b": 2} %}
{% for _, key := range keys(config) %}{{ key }}:{{ config[key] }} {% end %}`,
			expected: "a:1 b:2 c:3",
		},
		{
			name: "count keys",
			template: `{% var config = map[string]interface{}{"x": 1, "y": 2, "z": 3} %}
count={{ len(keys(config)) }}`,
			expected: "count=3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			templates := map[string]string{"test": tt.template}
			engine, err := NewScriggo(templates, []string{"test"}, nil, nil, nil)
			require.NoError(t, err)

			output, err := engine.Render("test", nil)
			require.NoError(t, err)
			assert.Contains(t, strings.TrimSpace(output), tt.expected)
		})
	}
}

// TestScriggoCacheFunctions tests the cache functions for compute_once pattern.
func TestScriggoCacheFunctions(t *testing.T) {
	t.Run("basic cache set and get", func(t *testing.T) {
		templates := map[string]string{
			"test": `{% set_cached("key1", "value1") %}
result={{ get_cached("key1") }}`,
		}

		entryPoints := []string{"test"}
		engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
		require.NoError(t, err)

		output, err := engine.Render("test", nil)
		require.NoError(t, err)
		assert.Contains(t, strings.TrimSpace(output), "result=value1")
	})

	t.Run("has_cached returns false for missing key", func(t *testing.T) {
		templates := map[string]string{
			"test": `exists={{ has_cached("nonexistent") }}`,
		}

		entryPoints := []string{"test"}
		engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
		require.NoError(t, err)

		output, err := engine.Render("test", nil)
		require.NoError(t, err)
		assert.Contains(t, output, "exists=false")
	})

	t.Run("has_cached returns true after set", func(t *testing.T) {
		templates := map[string]string{
			"test": `{% set_cached("key1", "value1") %}
exists={{ has_cached("key1") }}`,
		}

		entryPoints := []string{"test"}
		engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
		require.NoError(t, err)

		output, err := engine.Render("test", nil)
		require.NoError(t, err)
		assert.Contains(t, output, "exists=true")
	})

	t.Run("compute_once pattern", func(t *testing.T) {
		// This simulates the compute_once pattern:
		// Expensive computation only runs once, result is cached
		templates := map[string]string{
			"test": `{% var counter = 0 %}
{% if !has_cached("computed") %}
{% counter = counter + 1 %}
{% set_cached("computed", counter) %}
{% end %}
{% if !has_cached("computed") %}
{% counter = counter + 1 %}
{% set_cached("computed", counter) %}
{% end %}
{% if !has_cached("computed") %}
{% counter = counter + 1 %}
{% set_cached("computed", counter) %}
{% end %}
computed={{ get_cached("computed") }}`,
		}

		entryPoints := []string{"test"}
		engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
		require.NoError(t, err)

		output, err := engine.Render("test", nil)
		require.NoError(t, err)
		// Counter should only be 1 because the check happens each time
		// but only the first check sets the cache
		assert.Contains(t, strings.TrimSpace(output), "computed=1")
	})

	t.Run("cache is isolated per render", func(t *testing.T) {
		templates := map[string]string{
			"test": `{% var counter = 0 %}
{% if !has_cached("counter") %}
{% counter = 1 %}
{% set_cached("counter", counter) %}
{% end %}
value={{ get_cached("counter") }}`,
		}

		entryPoints := []string{"test"}
		engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
		require.NoError(t, err)

		// First render
		output1, err := engine.Render("test", nil)
		require.NoError(t, err)
		assert.Contains(t, output1, "value=1")

		// Second render - should start fresh, not reuse cache
		output2, err := engine.Render("test", nil)
		require.NoError(t, err)
		assert.Contains(t, output2, "value=1")
	})

	t.Run("get_cached returns nil for missing key", func(t *testing.T) {
		templates := map[string]string{
			"test": `{% var val = get_cached("missing") %}
is_nil={{ val == nil }}`,
		}

		entryPoints := []string{"test"}
		engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
		require.NoError(t, err)

		output, err := engine.Render("test", nil)
		require.NoError(t, err)
		assert.Contains(t, output, "is_nil=true")
	})

	t.Run("cache supports complex values", func(t *testing.T) {
		templates := map[string]string{
			"test": `{% var data = map[string]interface{}{"name": "test", "count": 42} %}
{% set_cached("data", data) %}
{% var retrieved = get_cached("data").(map[string]interface{}) %}
name={{ retrieved["name"] }}, count={{ retrieved["count"] }}`,
		}

		entryPoints := []string{"test"}
		engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
		require.NoError(t, err)

		output, err := engine.Render("test", nil)
		require.NoError(t, err)
		assert.Contains(t, output, "name=test")
		assert.Contains(t, output, "count=42")
	})
}

// =============================================================================
// first_seen tests
// =============================================================================

func TestScriggoFirstSeen_Basic(t *testing.T) {
	// Define data inside template since Scriggo requires compile-time type knowledge
	templates := map[string]string{
		"test": `{%- var items = []interface{}{"a", "b", "a", "c", "b", "a"} %}
{%- for _, item := range items %}
{%- if first_seen("item", item) %}FIRST:{{ item }} {% end %}
{%- end %}`,
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	// Only first occurrence of each item should be output
	assert.Equal(t, "FIRST:a FIRST:b FIRST:c", strings.TrimSpace(output))
}

func TestScriggoFirstSeen_CompositeKey(t *testing.T) {
	// Define data inside template since Scriggo requires compile-time type knowledge
	templates := map[string]string{
		"test": `{%- var namespaces = []interface{}{"default", "default", "kube-system"} %}
{%- var names = []interface{}{"svc1", "svc2"} %}
{%- for _, ns := range namespaces %}
{%- for _, name := range names %}
{%- if first_seen("resource", ns, name) %}{{ ns }}/{{ name }} {% end %}
{%- end %}
{%- end %}`,
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	// Each unique ns/name combination should appear once
	assert.Contains(t, output, "default/svc1")
	assert.Contains(t, output, "default/svc2")
	assert.Contains(t, output, "kube-system/svc1")
	assert.Contains(t, output, "kube-system/svc2")
	// Count occurrences - each should appear exactly once
	assert.Equal(t, 1, strings.Count(output, "default/svc1"))
}

func TestScriggoFirstSeen_EmptyKey(t *testing.T) {
	templates := map[string]string{
		"test": `{%- if first_seen() %}empty1 {% end %}
{%- if first_seen() %}empty2 {% end %}`,
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	// Empty key should always return true (no caching)
	assert.Contains(t, output, "empty1")
	assert.Contains(t, output, "empty2")
}

// =============================================================================
// selectattr tests
// =============================================================================

func TestScriggoSelectAttr_ExistenceCheck(t *testing.T) {
	templates := map[string]string{
		"test": `{%- var items = []interface{}{
			map[string]interface{}{"name": "a", "http": map[string]interface{}{"paths": []interface{}{}}},
			map[string]interface{}{"name": "b"},
			map[string]interface{}{"name": "c", "http": map[string]interface{}{}},
		} %}
{%- var filtered = selectattr(items, "http") %}
count={{ len(filtered) }}`,
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	// Items "a" and "c" have "http" attribute (even if empty map)
	assert.Contains(t, output, "count=2")
}

func TestScriggoSelectAttr_EqualTest(t *testing.T) {
	templates := map[string]string{
		"test": `{%- var items = []interface{}{
			map[string]interface{}{"name": "a", "pathType": "Exact"},
			map[string]interface{}{"name": "b", "pathType": "Prefix"},
			map[string]interface{}{"name": "c", "pathType": "Exact"},
		} %}
{%- var exact = selectattr(items, "pathType", "eq", "Exact") %}
{%- for _, item := range exact %}{{ item.(map[string]interface{})["name"] }} {% end %}`,
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Contains(t, output, "a")
	assert.Contains(t, output, "c")
	assert.NotContains(t, output, "b")
}

func TestScriggoSelectAttr_NotEqualTest(t *testing.T) {
	templates := map[string]string{
		"test": `{%- var items = []interface{}{
			map[string]interface{}{"name": "a", "status": "active"},
			map[string]interface{}{"name": "b", "status": "inactive"},
			map[string]interface{}{"name": "c", "status": "active"},
		} %}
{%- var notActive = selectattr(items, "status", "ne", "active") %}
{%- for _, item := range notActive %}{{ item.(map[string]interface{})["name"] }} {% end %}`,
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Contains(t, output, "b")
	assert.NotContains(t, output, "a")
	assert.NotContains(t, output, "c")
}

func TestScriggoSelectAttr_InTest(t *testing.T) {
	templates := map[string]string{
		"test": `{%- var items = []interface{}{
			map[string]interface{}{"name": "a", "pathType": "Exact"},
			map[string]interface{}{"name": "b", "pathType": "Prefix"},
			map[string]interface{}{"name": "c", "pathType": "ImplementationSpecific"},
			map[string]interface{}{"name": "d", "pathType": "Regex"},
		} %}
{%- var allowed = []string{"Exact", "Prefix"} %}
{%- var filtered = selectattr(items, "pathType", "in", allowed) %}
{%- for _, item := range filtered %}{{ item.(map[string]interface{})["name"] }} {% end %}`,
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Contains(t, output, "a")
	assert.Contains(t, output, "b")
	assert.NotContains(t, output, "c")
	assert.NotContains(t, output, "d")
}

func TestScriggoSelectAttr_NilInput(t *testing.T) {
	// Test that nil input returns empty slice
	result := scriggoSelectAttr(nil, "attr")
	assert.Empty(t, result)
}

func TestScriggoSelectAttr_EmptySlice(t *testing.T) {
	result := scriggoSelectAttr([]interface{}{}, "attr")
	assert.Empty(t, result)
}

// =============================================================================
// join_key tests
// =============================================================================

func TestScriggoJoinKey_Basic(t *testing.T) {
	templates := map[string]string{
		"test": `{{ join_key("_", "default", "my-service", 80) }}`,
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Equal(t, "default_my-service_80", strings.TrimSpace(output))
}

func TestScriggoJoinKey_WithVariables(t *testing.T) {
	templates := map[string]string{
		"test": `{%- var ns = "kube-system" %}
{%- var name = "coredns" %}
{%- var port = 53 %}
{{ join_key("/", ns, name, port) }}`,
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Contains(t, output, "kube-system/coredns/53")
}

func TestScriggoJoinKey_Empty(t *testing.T) {
	result := scriggoJoinKey("_")
	assert.Equal(t, "", result)
}

func TestScriggoJoinKey_SinglePart(t *testing.T) {
	result := scriggoJoinKey("_", "only")
	assert.Equal(t, "only", result)
}

func TestScriggoJoinKey_NilValues(t *testing.T) {
	// nil values should convert to empty string via scriggoToString
	result := scriggoJoinKey("_", "a", nil, "b")
	assert.Equal(t, "a__b", result)
}

// =============================================================================
// isEmpty helper tests
// =============================================================================

func TestIsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		value    interface{}
		expected bool
	}{
		{"nil", nil, true},
		{"empty string", "", true},
		{"non-empty string", "hello", false},
		{"zero int", 0, false},
		{"non-zero int", 42, false},
		{"false bool", false, true},
		{"true bool", true, false},
		{"empty slice", []interface{}{}, true},
		{"non-empty slice", []interface{}{"a"}, false},
		{"empty map", map[string]interface{}{}, true},
		{"non-empty map", map[string]interface{}{"key": "val"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isEmpty(tt.value)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// =============================================================================
// isValueInList helper tests
// =============================================================================

func TestIsValueInList(t *testing.T) {
	t.Run("string slice", func(t *testing.T) {
		list := []string{"Exact", "Prefix"}
		assert.True(t, isValueInList("Exact", list))
		assert.True(t, isValueInList("Prefix", list))
		assert.False(t, isValueInList("Regex", list))
	})

	t.Run("interface slice", func(t *testing.T) {
		list := []interface{}{"a", "b", "c"}
		assert.True(t, isValueInList("a", list))
		assert.True(t, isValueInList("b", list))
		assert.False(t, isValueInList("d", list))
	})

	t.Run("nil list", func(t *testing.T) {
		assert.False(t, isValueInList("a", nil))
	})

	t.Run("empty list", func(t *testing.T) {
		assert.False(t, isValueInList("a", []string{}))
	})
}

// =============================================================================
// scriggoIndent tests
// =============================================================================

func TestScriggoIndent_DefaultBehavior(t *testing.T) {
	input := "first\nsecond\nthird"
	result, err := scriggoIndent(input)
	require.NoError(t, err)
	expected := "first\n    second\n    third"
	assert.Equal(t, expected, result)
}

func TestScriggoIndent_CustomWidth(t *testing.T) {
	input := "first\nsecond"
	result, err := scriggoIndent(input, 8)
	require.NoError(t, err)
	expected := "first\n        second"
	assert.Equal(t, expected, result)
}

func TestScriggoIndent_CustomString(t *testing.T) {
	input := "first\nsecond"
	result, err := scriggoIndent(input, ">>")
	require.NoError(t, err)
	expected := "first\n>>second"
	assert.Equal(t, expected, result)
}

func TestScriggoIndent_FirstLine(t *testing.T) {
	input := "first\nsecond"
	result, err := scriggoIndent(input, 4, true)
	require.NoError(t, err)
	expected := "    first\n    second"
	assert.Equal(t, expected, result)
}

func TestScriggoIndent_BlankLines(t *testing.T) {
	input := "first\n\nthird"
	result, err := scriggoIndent(input, 4, false, true)
	require.NoError(t, err)
	expected := "first\n    \n    third"
	assert.Equal(t, expected, result)
}

func TestScriggoIndent_BlankLinesSkipped(t *testing.T) {
	input := "first\n\nthird"
	result, err := scriggoIndent(input)
	require.NoError(t, err)
	expected := "first\n\n    third"
	assert.Equal(t, expected, result)
}

func TestScriggoIndent_FirstAndBlank(t *testing.T) {
	input := "first\n\nthird"
	result, err := scriggoIndent(input, 2, true, true)
	require.NoError(t, err)
	expected := "  first\n  \n  third"
	assert.Equal(t, expected, result)
}

func TestScriggoIndent_EmptyString(t *testing.T) {
	result, err := scriggoIndent("")
	require.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestScriggoIndent_SingleLine(t *testing.T) {
	result, err := scriggoIndent("single")
	require.NoError(t, err)
	assert.Equal(t, "single", result)
}

func TestScriggoIndent_ZeroWidth(t *testing.T) {
	input := "first\nsecond"
	result, err := scriggoIndent(input, 0)
	require.NoError(t, err)
	expected := "first\nsecond"
	assert.Equal(t, expected, result)
}

func TestScriggoIndent_MultipleBlankLines(t *testing.T) {
	input := "first\n\n\nfourth"
	result, err := scriggoIndent(input)
	require.NoError(t, err)
	expected := "first\n\n\n    fourth"
	assert.Equal(t, expected, result)
}

func TestScriggoIndent_InvalidWidth(t *testing.T) {
	_, err := scriggoIndent("test", 3.14)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "indent width must be int or string")
}

func TestScriggoIndent_NegativeWidth(t *testing.T) {
	_, err := scriggoIndent("test", -5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "indent width must be non-negative")
}

func TestScriggoIndent_InvalidFirstArg(t *testing.T) {
	_, err := scriggoIndent("test", 4, "not a bool")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "indent 'first' must be bool")
}

func TestScriggoIndent_InvalidBlankArg(t *testing.T) {
	_, err := scriggoIndent("test", 4, false, "not a bool")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "indent 'blank' must be bool")
}

func TestScriggoIndent_LinesWithWhitespace(t *testing.T) {
	input := "first\n  \nthird"
	result, err := scriggoIndent(input)
	require.NoError(t, err)
	// Line with only whitespace is treated as blank
	expected := "first\n  \n    third"
	assert.Equal(t, expected, result)
}

func TestScriggoIndent_CustomStringMultiChar(t *testing.T) {
	input := "first\nsecond\nthird"
	result, err := scriggoIndent(input, ">>> ")
	require.NoError(t, err)
	expected := "first\n>>> second\n>>> third"
	assert.Equal(t, expected, result)
}
