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
	"log/slog"
	"math"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/open2b/scriggo/builtin"
	"github.com/open2b/scriggo/native"
)

// buildScriggoGlobals creates the global declarations for Scriggo templates.
// In Scriggo, filters become regular functions that templates can call.
//
// Example usage in Scriggo template:
//
//	{% for item := range sort_by(items, []string{"$.name"}) %}
//	{{ strip(item.value) }}
//	{% end %}
func buildScriggoGlobals(customFilters map[string]FilterFunc, customFunctions map[string]GlobalFunc) native.Declarations {
	decl := native.Declarations{}

	// Register runtime context variables, custom functions, and builtins
	registerScriggoRuntimeVars(decl)
	registerScriggoCustomFunctions(decl)
	registerScriggoBuiltins(decl)

	// Register any custom filters passed in (wrapped for Scriggo)
	for name, filter := range customFilters {
		decl[name] = wrapFilterForScriggo(filter)
	}

	// Register any custom functions (except fail, which we handle specially)
	for name, fn := range customFunctions {
		if name == FuncFail {
			// Skip - we use scriggoFail instead of wrapped FailFunction
			continue
		}
		decl[name] = wrapFunctionForScriggo(fn)
	}

	return decl
}

// registerScriggoRuntimeVars registers runtime context variables.
// These are declared with nil pointers so Scriggo knows the TYPE at compile time,
// but the VALUE is provided at runtime via template.Run(vars).
func registerScriggoRuntimeVars(decl native.Declarations) {
	decl["pathResolver"] = (*PathResolver)(nil)
	decl["resources"] = (*map[string]ResourceStore)(nil)
	decl["controller"] = (*map[string]ResourceStore)(nil)
	decl["templateSnippets"] = (*[]string)(nil)
	decl["fileRegistry"] = (*FileRegistrar)(nil)
	decl["dataplane"] = (*map[string]interface{})(nil)
	decl["capabilities"] = (*map[string]interface{})(nil)
	decl["shared"] = (*map[string]interface{})(nil)
	decl["extraContext"] = (*map[string]interface{})(nil)
	decl["http"] = (*HTTPFetcher)(nil) // HTTP store for fetching remote content
}

// registerScriggoCustomFunctions registers all custom functions for Scriggo templates.
func registerScriggoCustomFunctions(decl native.Declarations) {
	// Custom filters as functions
	decl[FilterSortBy] = scriggoSortBy
	decl[FilterGlobMatch] = scriggoGlobMatch
	decl[FilterStrip] = scriggoStrip
	decl[FilterTrim] = scriggoTrim
	decl[FilterB64Decode] = scriggoB64Decode
	decl[FilterDebug] = scriggoDebug

	// Scriggo-specific fail function (uses native.Env)
	decl[FuncFail] = scriggoFail

	// Dict utility functions
	decl[FuncMerge] = scriggoMerge
	decl[FuncKeys] = scriggoKeys

	// Sorting functions
	decl[FuncSortStrings] = scriggoSortStrings

	// Cache functions for compute_once pattern
	decl[FuncHasCached] = scriggoHasCached
	decl[FuncGetCached] = scriggoGetCached
	decl[FuncSetCached] = scriggoSetCached

	// Deduplication and filtering functions
	decl[FuncFirstSeen] = scriggoFirstSeen
	decl[FuncSelectAttr] = scriggoSelectAttr
	decl[FuncJoinKey] = scriggoJoinKey

	// String manipulation functions (custom implementations)
	decl[FuncStringsContains] = scriggoStringsContains
	decl[FuncStringsSplit] = scriggoStringsSplit
	decl[FuncStringsTrim] = scriggoStringsTrim
	decl[FuncStringsLower] = scriggoStringsLower
	decl[FuncStringsReplace] = scriggoStringsReplace
	decl[FuncStringsSplitN] = scriggoStringsSplitN
	decl[FilterIndent] = scriggoIndent

	// Type conversion functions
	decl[FuncToString] = scriggoToString
	decl[FuncToInt] = scriggoToInt
	decl[FuncToFloat] = scriggoToFloat

	// Utility functions
	decl[FuncCeil] = scriggoCeil
	decl[FuncSeq] = scriggoSeq
	decl[FuncRegexSearch] = scriggoRegexSearch
	decl[FuncIsDigit] = scriggoIsDigit
	decl[FuncSanitizeRegex] = scriggoSanitizeRegex
	decl[FuncTitle] = scriggoTitle
	decl[FuncDig] = scriggoDig
	decl[FuncToStringSlice] = scriggoToStringSlice
	decl[FuncJoin] = scriggoJoin
	decl[FuncReplace] = scriggoStringsReplace

	// Namespace function for mutable state patterns
	decl[FuncNamespace] = scriggoNamespace
	decl[FuncCoalesce] = scriggoCoalesce
	decl[FuncFallback] = scriggoCoalesce // Jinja2-style alias

	// Slice manipulation functions
	decl[FuncToSlice] = scriggoToSlice
	decl[FuncAppendAny] = scriggoAppendAny
}

// registerScriggoBuiltins registers all Scriggo builtin functions.
func registerScriggoBuiltins(decl native.Declarations) {
	registerScriggoBuiltinCore(decl)
	registerScriggoBuiltinStrings(decl)
}

// registerScriggoBuiltinCore registers core Scriggo builtins (crypto, encoding, math, etc.).
func registerScriggoBuiltinCore(decl native.Declarations) {
	// crypto
	decl["hmacSHA1"] = builtin.HmacSHA1
	decl["hmacSHA256"] = builtin.HmacSHA256
	decl["sha1"] = builtin.Sha1
	decl["sha256"] = builtin.Sha256

	// encoding
	decl["base64"] = builtin.Base64
	decl["hex"] = builtin.Hex
	decl["marshalJSON"] = builtin.MarshalJSON
	decl["marshalJSONIndent"] = builtin.MarshalJSONIndent
	decl["marshalYAML"] = builtin.MarshalYAML
	decl["md5"] = builtin.Md5
	decl["unmarshalJSON"] = builtin.UnmarshalJSON
	decl["unmarshalYAML"] = builtin.UnmarshalYAML

	// html
	decl["htmlEscape"] = builtin.HtmlEscape

	// math
	decl["abs"] = builtin.Abs
	decl["max"] = builtin.Max
	decl["min"] = builtin.Min
	decl["pow"] = builtin.Pow

	// net
	decl["queryEscape"] = builtin.QueryEscape

	// regexp
	decl["regexp"] = builtin.RegExp

	// sort
	decl["reverse"] = builtin.Reverse

	// slice operations (batch processing for performance)
	decl["group_by"] = builtin.GroupBy
	decl["count_by"] = builtin.CountBy
	decl["index_by"] = builtin.IndexBy
	decl["unique_by"] = builtin.UniqueBy
	decl["map_extract"] = builtin.MapExtract

	// strconv
	decl["formatFloat"] = builtin.FormatFloat
	decl["formatInt"] = builtin.FormatInt
	decl["parseFloat"] = builtin.ParseFloat
	decl["parseInt"] = builtin.ParseInt

	// time
	decl["date"] = builtin.Date
	decl["now"] = builtin.Now
	decl["parseDuration"] = builtin.ParseDuration
	decl["parseTime"] = builtin.ParseTime
	decl["unixTime"] = builtin.UnixTime
}

// registerScriggoBuiltinStrings registers string-related Scriggo builtins.
func registerScriggoBuiltinStrings(decl native.Declarations) {
	decl["trimSpace"] = scriggoTrimSpace
	decl["strings_contains"] = scriggoStringsContains
	decl["abbreviate"] = builtin.Abbreviate
	decl["capitalize"] = builtin.Capitalize
	decl["capitalizeAll"] = builtin.CapitalizeAll
	decl["hasPrefix"] = builtin.HasPrefix
	decl["hasSuffix"] = builtin.HasSuffix
	decl["index"] = builtin.Index
	decl["indexAny"] = builtin.IndexAny
	decl["join"] = scriggoJoin // Override builtin to support []interface{} from append()
	decl["lastIndex"] = builtin.LastIndex
	decl["replace"] = scriggoStringsReplace // Override builtin to support 3-arg syntax (replaces all)
	decl["replaceAll"] = builtin.ReplaceAll
	decl["runeCount"] = builtin.RuneCount
	decl["split"] = builtin.Split
	decl["splitAfter"] = builtin.SplitAfter
	decl["splitAfterN"] = builtin.SplitAfterN
	decl["splitN"] = builtin.SplitN
	decl["sprint"] = builtin.Sprint
	decl["sprintf"] = builtin.Sprintf
	decl["toKebab"] = builtin.ToKebab
	decl["toLower"] = builtin.ToLower
	decl["toUpper"] = builtin.ToUpper
	decl["trim"] = builtin.Trim
	decl["trimLeft"] = builtin.TrimLeft
	decl["trimPrefix"] = builtin.TrimPrefix
	decl["trimRight"] = builtin.TrimRight
	decl["trimSuffix"] = builtin.TrimSuffix
}

// scriggoSortBy sorts a slice of items by multiple criteria.
// Criteria are JSONPath expressions with optional modifiers:
//   - ":desc" for descending order
//   - ":exists" to sort by existence of field
//   - "| length" to sort by length
//
// Example:
//
//	sort_by(items, []string{"$.priority:desc", "$.name"})
func scriggoSortBy(items []interface{}, criteria []string) ([]interface{}, error) {
	if len(items) == 0 || len(criteria) == 0 {
		// Return a copy for consistency with normal path (avoids modifying original slice)
		result := make([]interface{}, len(items))
		copy(result, items)
		return result, nil
	}

	// Make a copy to avoid modifying the original
	result := make([]interface{}, len(items))
	copy(result, items)

	// Create sortable wrapper
	sortable := &sortableItems{
		items:    result,
		criteria: criteria,
		debugger: nil, // Filter debug not yet supported in Scriggo
	}

	// Precompute sort keys and sort
	sortable.precomputeKeys()
	sort.Stable(sortable)

	return sortable.items, nil
}

// scriggoGlobMatch filters items that match a glob pattern.
// Used for matching template snippet names.
// Accepts both []string and []interface{} for flexibility.
// Returns []string for direct use in Scriggo range expressions without type casting.
// Panics on invalid input to provide clear error messages.
//
// Example:
//
//	glob_match(snippetNames, "backend-*")
func scriggoGlobMatch(items interface{}, pattern string) []string {
	if items == nil || pattern == "" {
		return []string{}
	}

	switch v := items.(type) {
	case []string:
		return globMatchStrings(v, pattern)
	case []interface{}:
		return globMatchInterfaces(v, pattern)
	default:
		panic(fmt.Sprintf("glob_match: expected []string or []interface{}, got %T", items))
	}
}

// globMatchStrings matches a pattern against a slice of strings.
func globMatchStrings(items []string, pattern string) []string {
	result := make([]string, 0, len(items))
	for _, name := range items {
		matched, err := filepath.Match(pattern, name)
		if err != nil {
			panic(fmt.Sprintf("glob_match: invalid pattern %q: %v", pattern, err))
		}
		if matched {
			result = append(result, name)
		}
	}
	return result
}

// globMatchInterfaces matches a pattern against a slice of interface{} values.
// Each item can be a string or a map with a "name" field.
func globMatchInterfaces(items []interface{}, pattern string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		name := extractStringName(item)
		if name == "" {
			continue
		}
		matched, err := filepath.Match(pattern, name)
		if err != nil {
			panic(fmt.Sprintf("glob_match: invalid pattern %q: %v", pattern, err))
		}
		if matched {
			result = append(result, name)
		}
	}
	return result
}

// extractStringName extracts a string name from an interface{} value.
// Supports string values directly or maps with a "name" field.
func extractStringName(item interface{}) string {
	switch s := item.(type) {
	case string:
		return s
	case map[string]interface{}:
		if n, ok := s["name"].(string); ok {
			return n
		}
	}
	return ""
}

// scriggoStrip removes leading and trailing whitespace from a value.
// The input is converted to string using lenient type conversion.
// Uses shared Strip implementation from filters.go.
//
// Example:
//
//	strip("  hello world  ") => "hello world"
//	strip(123) => "123"
func scriggoStrip(s interface{}) string {
	str := scriggoToString(s)
	return Strip(str)
}

// scriggoTrim is an alias for scriggoStrip for compatibility.
// Both "strip" and "trim" are common filter names for whitespace removal.
var scriggoTrim = scriggoStrip

// scriggoB64Decode decodes a base64-encoded value.
// The input is converted to string using lenient type conversion.
// Useful for decoding Kubernetes secret values.
//
// Example:
//
//	b64decode("SGVsbG8gV29ybGQ=") => "Hello World"
func scriggoB64Decode(s interface{}) (string, error) {
	str := scriggoToString(s)
	decoded, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// scriggoDebug outputs the structure of a variable as formatted JSON comments.
// Uses shared Debug implementation from filters.go.
//
// Example:
//
//	debug(routes)           => "# DEBUG:\n# [...]"
//	debug(routes, "label")  => "# DEBUG label:\n# [...]"
func scriggoDebug(value interface{}, label ...string) string {
	// Extract label if provided
	labelStr := ""
	if len(label) > 0 {
		labelStr = label[0]
	}
	return Debug(value, labelStr)
}

// wrapFilterForScriggo wraps a FilterFunc to be callable from Scriggo templates.
// FilterFunc signature: func(in interface{}, args ...interface{}) (interface{}, error)
// Scriggo needs a concrete function signature, so we wrap it.
func wrapFilterForScriggo(filter FilterFunc) func(in interface{}, args ...interface{}) (interface{}, error) {
	return func(in interface{}, args ...interface{}) (interface{}, error) {
		return filter(in, args...)
	}
}

// wrapFunctionForScriggo wraps a GlobalFunc to be callable from Scriggo templates.
func wrapFunctionForScriggo(fn GlobalFunc) func(args ...interface{}) (interface{}, error) {
	return func(args ...interface{}) (interface{}, error) {
		return fn(args...)
	}
}

// scriggoFail stops template execution with an error message using
// Scriggo's native.Env.Stop() mechanism to properly halt template rendering.
//
// The function returns a string (which is never used because env.Stop() halts
// execution) so it can be used in expression context {{ }}.
//
// Usage in Scriggo templates:
//
//	{{ fail("Service not found") }}
//	{% if !service %}{{ fail("Service is required") }}{% end %}
func scriggoFail(env native.Env, msg string) string {
	env.Stop(fmt.Errorf("%s", msg))
	return "" // Never reached - env.Stop() halts execution
}

// scriggoMerge returns a new map with all key-value pairs from both maps.
// Values from the updates map override values from the original map.
//
// Usage in Scriggo templates:
//
//	{% var config = map[string]interface{}{"a": 1, "b": 2} %}
//	{% config = merge(config, map[string]interface{}{"b": 3, "c": 4}) %}
//	{# Result: {"a": 1, "b": 3, "c": 4} #}
func scriggoMerge(dict, updates map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(dict)+len(updates))
	for k, v := range dict {
		result[k] = v
	}
	for k, v := range updates {
		result[k] = v
	}
	return result
}

// scriggoKeys returns a sorted slice of keys from a map.
// Keys are sorted alphabetically for deterministic iteration order.
// Works with any map type that has string keys (map[string]any, map[string][]any, etc.).
//
// Usage in Scriggo templates:
//
//	{% var config = map[string]interface{}{"b": 2, "a": 1, "c": 3} %}
//	{% for _, key := range keys(config) %}
//	{{ key }}: {{ config[key] }}
//	{% end %}
//	{# Output: a: 1, b: 2, c: 3 (sorted) #}
func scriggoKeys(dict any) []string {
	if dict == nil {
		return []string{}
	}

	rv := reflect.ValueOf(dict)
	if rv.Kind() != reflect.Map {
		return []string{}
	}

	keys := make([]string, 0, rv.Len())
	for _, k := range rv.MapKeys() {
		keys = append(keys, k.String())
	}
	sort.Strings(keys)
	return keys
}

// scriggoSortStrings sorts a slice of interface{} values as strings.
// This is useful when working with []any slices that contain string values,
// since Scriggo's built-in sort() requires typed slices.
//
// Usage in Scriggo templates:
//
//	{% var items = []any{"c", "a", "b"} %}
//	{% var sorted = sort_strings(items) %}
//	{# sorted is []string{"a", "b", "c"} #}
func scriggoSortStrings(items []interface{}) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case string:
			result = append(result, v)
		default:
			// Convert non-string to string
			result = append(result, fmt.Sprintf("%v", v))
		}
	}
	sort.Strings(result)
	return result
}

// RenderCacheKey is the context key for per-render cache storage.
// This type prevents collisions with other context values.
type renderCacheKey struct{}

// RenderCacheContextKey is exported for use in engine_scriggo.go.
var RenderCacheContextKey = renderCacheKey{}

// scriggoHasCached checks if a key exists in the per-render cache.
// This enables the compute_once pattern for expensive operations.
//
// Usage in Scriggo templates:
//
//	{% if !has_cached("gateway_analysis") %}
//	    {# expensive computation #}
//	    {% set_cached("gateway_analysis", result) %}
//	{% end %}
func scriggoHasCached(env native.Env, key string) bool {
	cache := getRenderCache(env)
	if cache == nil {
		return false
	}
	_, exists := cache[key]
	return exists
}

// scriggoGetCached retrieves a value from the per-render cache.
// Returns nil if the key doesn't exist.
//
// Usage in Scriggo templates:
//
//	{% var analysis = get_cached("gateway_analysis") %}
func scriggoGetCached(env native.Env, key string) interface{} {
	cache := getRenderCache(env)
	if cache == nil {
		return nil
	}
	return cache[key]
}

// scriggoSetCached stores a value in the per-render cache.
// The value persists for the duration of the current render operation.
//
// Usage in Scriggo templates:
//
//	{% set_cached("gateway_analysis", result) %}
func scriggoSetCached(env native.Env, key string, value interface{}) {
	cache := getRenderCache(env)
	if cache == nil {
		// Cache not initialized - should not happen if engine is configured correctly.
		// Log warning to aid debugging when caching unexpectedly fails.
		slog.Warn("set_cached called but render cache not initialized",
			"key", key)
		return
	}
	cache[key] = value
}

// getRenderCache retrieves the per-render cache from the context.
func getRenderCache(env native.Env) map[string]interface{} {
	ctx := env.Context()
	if ctx == nil {
		return nil
	}
	cache, ok := ctx.Value(RenderCacheContextKey).(map[string]interface{})
	if !ok {
		return nil
	}
	return cache
}

// renderContextKey is the context key for storing the render context (globals).
type renderContextKey struct{}

// RenderContextContextKey is exported for use in engine_scriggo.go.
var RenderContextContextKey = renderContextKey{}

// =============================================================================
// Deduplication and filtering functions
// =============================================================================

// scriggoFirstSeen checks if a composite key is being seen for the first time.
// Returns true on first occurrence, false on subsequent calls with the same key.
// This atomically combines has_cached + set_cached into a single operation.
//
// Usage in Scriggo templates:
//
//	{%- if first_seen("ingress_tls", namespace, secretName) %}
//	    {# process this TLS secret for the first time #}
//	{%- end %}
//
// The key is built by joining all parts with "_", e.g.: "ingress_tls_default_my-secret".
func scriggoFirstSeen(env native.Env, parts ...interface{}) bool {
	if len(parts) == 0 {
		return true // No key parts = treat as always first
	}

	// Build composite key from all parts
	keyParts := make([]string, len(parts))
	for i, part := range parts {
		keyParts[i] = scriggoToString(part)
	}
	key := strings.Join(keyParts, "_")

	cache := getRenderCache(env)
	if cache == nil {
		return true // No cache = treat as first time
	}

	if _, exists := cache[key]; exists {
		return false // Already seen
	}

	cache[key] = true
	return true
}

// scriggoSelectAttr filters items by attribute existence or value.
// This is a Jinja2-compatible filter for selecting items from a sequence.
//
// Usage in Scriggo templates:
//
//	{%- var rules_with_http = selectattr(rules, "http") %}
//	{%- var exact_paths = selectattr(paths, "pathType", "eq", "Exact") %}
//	{%- var matching = selectattr(paths, "pathType", "in", []string{"Exact", "Prefix"}) %}
//
// Supported tests:
//   - selectattr(items, "attr") - items where attr is defined and truthy
//   - selectattr(items, "attr", "eq", value) - items where attr equals value
//   - selectattr(items, "attr", "ne", value) - items where attr does not equal value
//   - selectattr(items, "attr", "in", list) - items where attr value is in list
func scriggoSelectAttr(items interface{}, attr string, args ...interface{}) []interface{} {
	result := []interface{}{}

	// Handle nil input
	if items == nil {
		return result
	}

	// Convert items to slice
	itemsSlice, ok := toSlice(items)
	if !ok {
		return result
	}

	// Parse optional test and value arguments
	var test string
	var testValue interface{}
	if len(args) >= 2 {
		test, _ = args[0].(string)
		testValue = args[1]
	}

	for _, item := range itemsSlice {
		if item == nil {
			continue
		}

		// Get attribute value using dig
		attrValue := scriggoDig(item, attr)

		// Apply test based on arguments
		switch test {
		case "eq":
			// Equal test
			if scriggoToString(attrValue) == scriggoToString(testValue) {
				result = append(result, item)
			}
		case "ne":
			// Not equal test
			if scriggoToString(attrValue) != scriggoToString(testValue) {
				result = append(result, item)
			}
		case "in":
			// Membership test
			if isValueInList(attrValue, testValue) {
				result = append(result, item)
			}
		default:
			// Default: check if attribute is defined (not nil).
			// An empty map or slice still counts as "defined" - only nil means absent.
			if attrValue != nil {
				result = append(result, item)
			}
		}
	}

	return result
}

// isValueInList checks if a value is in a list (for selectattr "in" test).
func isValueInList(value, list interface{}) bool {
	if list == nil {
		return false
	}

	valueStr := scriggoToString(value)

	// Handle []string
	if strList, ok := list.([]string); ok {
		for _, item := range strList {
			if item == valueStr {
				return true
			}
		}
		return false
	}

	// Handle []interface{}
	if anyList, ok := list.([]interface{}); ok {
		for _, item := range anyList {
			if scriggoToString(item) == valueStr {
				return true
			}
		}
		return false
	}

	// Handle []any (same as []interface{})
	listSlice, ok := toSlice(list)
	if !ok {
		return false
	}

	for _, item := range listSlice {
		if scriggoToString(item) == valueStr {
			return true
		}
	}
	return false
}

// isEmpty checks if a value is "empty" (for selectattr truthiness test).
func isEmpty(value interface{}) bool {
	if value == nil {
		return true
	}

	switch v := value.(type) {
	case string:
		return v == ""
	case int, int64, float64:
		return false // Numbers are not empty
	case bool:
		return !v
	case []interface{}:
		return len(v) == 0
	case map[string]interface{}:
		return len(v) == 0
	default:
		// Try reflection for other slice/map types
		rv := reflect.ValueOf(value)
		switch rv.Kind() {
		case reflect.Slice, reflect.Map, reflect.Array:
			return rv.Len() == 0
		case reflect.Ptr:
			return rv.IsNil()
		}
		return false
	}
}

// toSlice converts an interface{} to []interface{}.
func toSlice(items interface{}) ([]interface{}, bool) {
	if items == nil {
		return nil, false
	}

	// Already a slice of interfaces
	if slice, ok := items.([]interface{}); ok {
		return slice, true
	}

	// Use reflection for other slice types
	rv := reflect.ValueOf(items)
	if rv.Kind() != reflect.Slice {
		return nil, false
	}

	result := make([]interface{}, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		result[i] = rv.Index(i).Interface()
	}
	return result, true
}

// scriggoJoinKey joins multiple values into a composite key string.
// Automatically converts all values to strings using scriggoToString.
//
// Usage in Scriggo templates:
//
//	{%- var key = join_key("_", namespace, name, serviceName, port) %}
//
// This is cleaner than:
//
//	{%- var key = tostring(ns) + "_" + tostring(name) + "_" + tostring(svc) + "_" + tostring(port) %}
func scriggoJoinKey(sep string, parts ...interface{}) string {
	if len(parts) == 0 {
		return ""
	}

	strs := make([]string, len(parts))
	for i, part := range parts {
		strs[i] = scriggoToString(part)
	}
	return strings.Join(strs, sep)
}

// =============================================================================
// String manipulation functions
// =============================================================================

// scriggoStringsContains checks if a value contains a substring.
// Both parameters are converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% if strings_contains(path, "/api/") %}
func scriggoStringsContains(s, substr interface{}) bool {
	str := scriggoToString(s)
	substrStr := scriggoToString(substr)
	return strings.Contains(str, substrStr)
}

// scriggoStringsSplit splits a value by a separator.
// Both parameters are converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% for _, part := range strings_split(path, "/") %}
func scriggoStringsSplit(s, sep interface{}) []string {
	str := scriggoToString(s)
	sepStr := scriggoToString(sep)
	return strings.Split(str, sepStr)
}

// scriggoStringsTrim trims whitespace from a value.
// The input is converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% var trimmed = strings_trim(value) %}
func scriggoStringsTrim(s interface{}) string {
	str := scriggoToString(s)
	return strings.TrimSpace(str)
}

// scriggoTrimSpace trims whitespace from a value.
// The input is converted to string using lenient type conversion.
// This is an alias for strings_trim with a shorter name.
//
// Usage in Scriggo templates:
//
//	{% var trimmed = trimSpace(value) %}
func scriggoTrimSpace(s interface{}) string {
	str := scriggoToString(s)
	return strings.TrimSpace(str)
}

// scriggoStringsLower converts a value to lowercase.
// The input is converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% var lower = strings_lower(method) %}
func scriggoStringsLower(s interface{}) string {
	str := scriggoToString(s)
	return strings.ToLower(str)
}

// scriggoStringsReplace replaces all occurrences of old with new in s.
// All three parameters are converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% var escaped = strings_replace(path, "/", "\\/") %}
func scriggoStringsReplace(s, old, replacement interface{}) string {
	str := scriggoToString(s)
	oldStr := scriggoToString(old)
	replacementStr := scriggoToString(replacement)
	return strings.ReplaceAll(str, oldStr, replacementStr)
}

// scriggoStringsSplitN splits a value by separator with a maximum number of parts.
// String parameters are converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% var parts = strings_splitn(line, " ", 2) %}
//	{% var first = parts[0] %}
//	{% var rest = parts[1] %}
func scriggoStringsSplitN(s, sep interface{}, n int) []string {
	str := scriggoToString(s)
	sepStr := scriggoToString(sep)
	return strings.SplitN(str, sepStr, n)
}

// scriggoIndent indents each line of a value by width spaces or a custom prefix.
// The input is converted to string using lenient type conversion.
// By default, the first line and blank lines are not indented.
//
// Args:
//
//	s: Input value to indent (converted to string)
//	args[0] (optional): width - int (spaces) or string (prefix), default=4
//	args[1] (optional): first - bool, indent first line, default=false
//	args[2] (optional): blank - bool, indent blank lines, default=false
//
// Usage in Scriggo templates:
//
//	{{ indent(text) }}                  {# 4 spaces, skip first/blank #}
//	{{ indent(text, 8) }}               {# 8 spaces #}
//	{{ indent(text, ">>") }}            {# custom prefix #}
//	{{ indent(text, 4, true) }}         {# include first line #}
//	{{ indent(text, 4, false, true) }}  {# include blank lines #}
//
// Returns indented string or error on invalid arguments.
func scriggoIndent(s interface{}, args ...interface{}) (string, error) {
	str := scriggoToString(s)

	// Parse arguments
	indentStr, err := parseIndentWidth(args)
	if err != nil {
		return "", err
	}

	indentFirst, err := parseIndentBool(args, 1, "first")
	if err != nil {
		return "", err
	}

	indentBlank, err := parseIndentBool(args, 2, "blank")
	if err != nil {
		return "", err
	}

	// Process lines
	return applyIndentation(str, indentStr, indentFirst, indentBlank), nil
}

// parseIndentWidth extracts and validates the width argument.
func parseIndentWidth(args []interface{}) (string, error) {
	if len(args) == 0 || args[0] == nil {
		return "    ", nil // 4 spaces default
	}

	switch v := args[0].(type) {
	case int:
		if v < 0 {
			return "", fmt.Errorf("indent width must be non-negative, got %d", v)
		}
		return strings.Repeat(" ", v), nil
	case string:
		return v, nil
	default:
		return "", fmt.Errorf("indent width must be int or string, got %T", v)
	}
}

// parseIndentBool extracts and validates a boolean argument at the given index.
func parseIndentBool(args []interface{}, index int, name string) (bool, error) {
	if len(args) <= index || args[index] == nil {
		return false, nil
	}

	val, ok := args[index].(bool)
	if !ok {
		return false, fmt.Errorf("indent '%s' must be bool, got %T", name, args[index])
	}
	return val, nil
}

// applyIndentation applies indentation to each line based on the flags.
func applyIndentation(s, indentStr string, indentFirst, indentBlank bool) string {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return s
	}

	var result strings.Builder
	for i, line := range lines {
		if shouldIndentLine(i, line, indentFirst, indentBlank) {
			result.WriteString(indentStr)
		}
		result.WriteString(line)

		if i < len(lines)-1 {
			result.WriteString("\n")
		}
	}

	return result.String()
}

// shouldIndentLine determines if a line should be indented based on flags.
func shouldIndentLine(lineIndex int, line string, indentFirst, indentBlank bool) bool {
	isFirst := lineIndex == 0
	isBlank := strings.TrimSpace(line) == ""

	if isFirst && !indentFirst {
		return false
	}
	if isBlank && !indentBlank {
		return false
	}
	return true
}

// =============================================================================
// Type conversion functions
// =============================================================================

// scriggoToString converts any value to its string representation.
//
// Usage in Scriggo templates:
//
//	{% var s = tostring(port) %}
func scriggoToString(v interface{}) string {
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
func scriggoToInt(v interface{}) int {
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
func scriggoToFloat(v interface{}) (float64, error) {
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
		return 0, fmt.Errorf("cannot convert %T to float", v)
	}
}

// =============================================================================
// Utility functions
// =============================================================================

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

// scriggoRegexSearch checks if a value matches a regex pattern.
// Both string parameters are converted to string using lenient type conversion.
// Returns true if pattern is found in string.
//
// Usage in Scriggo templates:
//
//	{% if regex_search(name, "ssl.*passthrough") %}
func scriggoRegexSearch(env native.Env, s, pattern interface{}) bool {
	str := scriggoToString(s)
	patternStr := scriggoToString(pattern)
	re, err := regexp.Compile(patternStr)
	if err != nil {
		env.Fatal(fmt.Errorf("regex_search: invalid pattern %q: %w", patternStr, err))
		return false
	}
	return re.MatchString(str)
}

// scriggoIsDigit checks if a value contains only digits.
// The input is converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% if isdigit(value) %}
func scriggoIsDigit(s interface{}) bool {
	str := scriggoToString(s)
	if str == "" {
		return false
	}
	for _, r := range str {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// scriggoSanitizeRegex escapes special regex characters in a value.
// The input is converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% var escaped = sanitize_regex(path) %}
func scriggoSanitizeRegex(s interface{}) string {
	str := scriggoToString(s)
	return regexp.QuoteMeta(str)
}

// scriggoTitle converts a value to title case.
// The input is converted to string using lenient type conversion.
//
// Usage in Scriggo templates:
//
//	{% var title = title(resource_type) %}
func scriggoTitle(s interface{}) string {
	str := scriggoToString(s)
	return strings.Title(str) //nolint:staticcheck // strings.Title is deprecated but sufficient for our use
}

// scriggoDig navigates nested maps using a sequence of keys.
// Returns nil if any key along the path is missing or the value is nil.
// This is a Ruby-style dig function for cleaner nested access.
//
// Usage in Scriggo templates:
//
//	{{ ingress | dig("metadata", "namespace") | fallback("") }}
//	{{ path | dig("backend", "service", "name") | fallback("unknown") }}
//	{%- var port = path | dig("backend", "service", "port", "number") | fallback(80) %}
func scriggoDig(obj interface{}, keys ...string) interface{} {
	if obj == nil || len(keys) == 0 {
		return obj
	}

	current := obj
	for _, key := range keys {
		if current == nil {
			return nil
		}

		switch v := current.(type) {
		case map[string]interface{}:
			val, ok := v[key]
			if !ok {
				return nil
			}
			current = val
		case map[string]string:
			val, ok := v[key]
			if !ok {
				return nil
			}
			current = val
		default:
			// Can't navigate further - not a map type
			return nil
		}
	}

	return current
}

// scriggoToStringSlice converts []any to []string.
// Each element is converted to string via fmt.Sprintf.
//
// Usage in Scriggo templates:
//
//	{%- var hosts = toStringSlice(hostnames) -%}
func scriggoToStringSlice(items interface{}) []string {
	if items == nil {
		return []string{}
	}
	switch v := items.(type) {
	case []string:
		return v
	case []interface{}:
		result := make([]string, len(v))
		for i, item := range v {
			result[i] = fmt.Sprintf("%v", item)
		}
		return result
	default:
		return []string{}
	}
}

// scriggoJoin joins a string slice with a separator.
// Accepts interface{} to handle both []string and []interface{} from templates.
//
// Usage in Scriggo templates:
//
//	{%- var list = join(hosts, ", ") -%}
//	{{ items | join(" ") }}
func scriggoJoin(items interface{}, sep string) string {
	switch v := items.(type) {
	case []string:
		return strings.Join(v, sep)
	case []interface{}:
		strs := make([]string, len(v))
		for i, item := range v {
			strs[i] = fmt.Sprintf("%v", item)
		}
		return strings.Join(strs, sep)
	default:
		return ""
	}
}

// scriggoCoalesce returns the first non-nil value, or the default if all are nil.
// This is needed because Scriggo's `default` operator only works when the left
// expression is a predeclared identifier (variable, constant, function), a render
// expression, or a macro call. It does NOT work with field access expressions
// like `obj.field` or map index expressions like `map["key"]`.
//
// See: https://scriggo.com/templates/specification#default-expression
//
// Usage in Scriggo templates:
//
//	{%- var items = coalesce(obj.field, []any{}) -%}
//	{%- var name = coalesce(user.name, "anonymous") -%}
func scriggoCoalesce(value, defaultVal interface{}) interface{} {
	if value == nil {
		return defaultVal
	}
	return value
}

// scriggoNamespace creates a mutable map for storing state across loop iterations,
// enabling mutable state patterns in templates.
//
// Maps in Go are reference types and mutable by default, so this function
// simply returns the provided map (or creates an empty one if nil).
//
// Usage in Scriggo templates:
//
//	{# Create namespace with initial values #}
//	{%- var ns = namespace(map[string]any{"seen": []any{}, "count": 0}) -%}
//
//	{# Modify values (maps are mutable) #}
//	{%- for _, item := range items -%}
//	  {%- ns["count"] = ns["count"].(int) + 1 -%}
//	  {%- ns["seen"] = append(ns["seen"].([]any), item) -%}
//	{%- end -%}
//
//	{# Empty namespace #}
//	{%- var ns = namespace(nil) -%}
func scriggoNamespace(init map[string]interface{}) map[string]interface{} {
	if init == nil {
		return make(map[string]interface{})
	}
	return init
}

// =============================================================================
// Slice manipulation functions
// =============================================================================

// scriggoToSlice converts any value to []interface{} for safe ranging.
// Returns an empty slice if input is nil, otherwise converts to []interface{}.
// This is necessary in Scriggo because Kubernetes resource fields may be nil
// and we need to safely iterate over them.
//
// Usage in Scriggo templates:
//
//	{# Safe iteration over potentially nil value #}
//	{%- for _, item := range toSlice(spec_rules) %}
//	  ... process item ...
//	{%- end %}
func scriggoToSlice(items interface{}) []interface{} {
	if items == nil {
		return []interface{}{}
	}
	result, _ := toSlice(items)
	return result
}

// scriggoAppendAny appends an item to a slice, handling interface{} types.
// This is necessary in Scriggo because map field access returns interface{},
// and Go's append() requires a concrete slice type.
//
// Usage in Scriggo templates:
//
//	{# Works with nil (creates new slice) #}
//	{%- var list = append(nil, "first") -%}
//
//	{# Works with interface{} from maps #}
//	{%- ns.flags = append(ns.flags, "flag") -%}
//
//	{# Works with []any directly #}
//	{%- items = append(items, item) -%}
func scriggoAppendAny(slice, item interface{}) []interface{} {
	if slice == nil {
		return []interface{}{item}
	}

	// Try to convert to []interface{} ([]any is an alias for []interface{})
	if s, ok := slice.([]interface{}); ok {
		return append(s, item)
	}

	// Can't append - return new slice with just the item
	// This handles edge cases like receiving a non-slice type
	return []interface{}{item}
}
