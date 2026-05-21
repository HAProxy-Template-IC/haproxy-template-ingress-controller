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
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

// renderContextKey is the context key for storing the render context (globals).
type renderContextKey struct{}

// RenderContextContextKey is exported for use in engine_scriggo.go.
var RenderContextContextKey = renderContextKey{}

// getSharedContext retrieves the SharedContext from the template context.
// Returns nil if not found or not properly configured.
func getSharedContext(env native.Env) *SharedContext {
	ctx := env.Context()
	if ctx == nil {
		return nil
	}
	renderCtx, ok := ctx.Value(RenderContextContextKey).(map[string]any)
	if !ok {
		return nil
	}
	shared, ok := renderCtx["shared"].(*SharedContext)
	if !ok {
		return nil
	}
	return shared
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

// scriggoDig navigates nested maps using a sequence of keys.
// Returns nil if any key along the path is missing or the value is nil.
// This is a Ruby-style dig function for cleaner nested access.
//
// Usage in Scriggo templates:
//
//	{{ ingress | dig("metadata", "namespace") | fallback("") }}
//	{{ path | dig("backend", "service", "name") | fallback("unknown") }}
//	{%- var port = path | dig("backend", "service", "port", "number") | fallback(80) %}
func scriggoDig(obj any, keys ...string) any {
	if obj == nil || len(keys) == 0 {
		return obj
	}

	// Fast path: direct map[string]any (99% of cases in K8s templates)
	// Avoids reflection overhead from isNilValue() on every iteration
	if m, ok := obj.(map[string]any); ok {
		return digMapFast(m, keys)
	}

	// Slow path with reflection for typed nil pointers and other types
	return digReflect(obj, keys)
}

// digMapFast is the optimized path for map[string]any traversal.
// Handles nested maps without reflection overhead.
func digMapFast(m map[string]any, keys []string) any {
	for i, key := range keys {
		val, ok := m[key]
		if !ok || val == nil {
			return nil
		}

		// If this is the last key, return the value directly
		if i == len(keys)-1 {
			return val
		}

		// Try to continue traversal with nested map
		next, ok := val.(map[string]any)
		if !ok {
			// Check for map[string]string on last key
			if strMap, ok := val.(map[string]string); ok {
				// Only one key left, look it up
				if i == len(keys)-2 {
					if strVal, found := strMap[keys[i+1]]; found {
						return strVal
					}
				}
			}
			return nil
		}
		m = next
	}
	return m
}

// digReflect handles typed nil pointers and other edge cases with reflection.
//
// It also navigates *typed structs* — the shape pkg/k8s/typegen produces
// and the chart's render-time wiring (RenderService.addTypedRenderContextEntries)
// hands to templates via the top-level resource globals. Struct field
// lookup is by JSON tag so chart code stays identical between the
// untyped map shape (`dig(gw, "metadata", "name")`) and the typed
// struct shape (same call, same key strings). This is the property
// that lets the chart adopt typed access incrementally without
// rewriting every dig() call site.
func digReflect(obj any, keys []string) any {
	// Handle typed nil values (e.g., *map[string]any with nil pointer)
	if isNilValue(obj) {
		return nil
	}

	current := obj
	for _, key := range keys {
		if current == nil {
			return nil
		}

		switch v := current.(type) {
		case map[string]any:
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
			next, ok := digStructField(current, key)
			if !ok {
				return nil
			}
			current = next
		}
	}

	return current
}

// digStructField navigates ONE level into a typed struct (or
// pointer/interface wrapping one). Returns ok=false when the value
// isn't a struct, the field doesn't exist, or any pointer along the
// way is nil. The found field's .Interface() is returned as `any` so
// subsequent dig iterations can treat it uniformly with the map path.
//
// JSON-tag matching is the primary lookup — chart authors write
// `dig(gw, "metadata", "name")` with JSON-style keys against both
// untyped maps and typed structs. We populate the json:"name" tags
// from typegen.goFieldName at type-generation time precisely so
// this lookup works without a separate path through the chart.
//
// Reflection cost: ~50ns/level under a sync.Map cache (see
// structFieldLookup). For a typical 3-level dig path that's ~150ns,
// well below the map-fast-path's ~25ns but irrelevant against the
// ~100µs render budget per template.
func digStructField(obj any, key string) (any, bool) {
	v := reflect.ValueOf(obj)
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil, false
	}
	idx, ok := structFieldIndex(v.Type(), key)
	if !ok {
		return nil, false
	}
	field := v.Field(idx)
	if !field.IsValid() {
		return nil, false
	}
	return field.Interface(), true
}

// jsonNameCache caches per-type JSON-name → field-index maps. Without
// this, every dig step on a typed struct re-scans every field via
// reflect — quadratic in field count for nested dig walks. The
// generated types come from typegen's Convert(), so they have stable
// identities across renders within one iteration; caching by
// reflect.Type is safe.
var jsonNameCache sync.Map // map[reflect.Type]map[string]int

// structFieldIndex returns the field index for the given JSON name
// on the supplied struct type, or -1 if no such field exists. Falls
// back to the Go field name when no field carries a matching json
// tag — covers types declared outside typegen that the chart might
// reach via dig (e.g. test fixtures), though typegen-produced types
// always have json tags.
func structFieldIndex(t reflect.Type, jsonName string) (int, bool) {
	if cached, ok := jsonNameCache.Load(t); ok {
		idx, ok := cached.(map[string]int)[jsonName]
		return idx, ok
	}
	m := buildJSONNameIndex(t)
	jsonNameCache.Store(t, m)
	idx, ok := m[jsonName]
	return idx, ok
}

// buildJSONNameIndex scans every field on t and builds the
// JSON-name → field-index map. Each field is indexed under TWO
// names — its JSON tag (the canonical chart-template key) and its
// Go field name (the fallback). Conflicts between the two are
// effectively impossible because typegen capitalises the JSON name
// into the Go name, so the JSON tag is always lower-case and the
// Go name is always upper-case-first.
func buildJSONNameIndex(t reflect.Type) map[string]int {
	out := make(map[string]int, t.NumField()*2)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		out[f.Name] = i
		tag := f.Tag.Get("json")
		if tag == "" {
			continue
		}
		// Strip ",omitempty" and the rest. encoding/json's own
		// convention treats everything after the first comma as
		// directives.
		name := tag
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			name = tag[:comma]
		}
		if name != "" && name != "-" {
			out[name] = i
		}
	}
	return out
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
func scriggoSelectAttr(items any, attr string, args ...any) []any {
	result := []any{}

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
	var testValue any
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
func isValueInList(value, list any) bool {
	if list == nil {
		return false
	}

	valueStr := scriggoToString(value)

	// Handle []string
	if strList, ok := list.([]string); ok {
		return slices.Contains(strList, valueStr)
	}

	// Handle []any
	if anyList, ok := list.([]any); ok {
		for _, item := range anyList {
			if scriggoToString(item) == valueStr {
				return true
			}
		}
		return false
	}

	// Handle []any (same as []any)
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
func isEmpty(value any) bool {
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
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	default:
		// Try reflection for other slice/map types
		rv := reflect.ValueOf(value)
		switch rv.Kind() {
		case reflect.Slice, reflect.Map, reflect.Array:
			return rv.Len() == 0
		case reflect.Pointer:
			return rv.IsNil()
		}
		return false
	}
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
func scriggoJoinKey(sep string, parts ...any) string {
	if len(parts) == 0 {
		return ""
	}

	strs := make([]string, len(parts))
	for i, part := range parts {
		strs[i] = scriggoToString(part)
	}
	return strings.Join(strs, sep)
}

// scriggoMerge returns a new map with all key-value pairs from both maps.
// Values from the updates map override values from the original map.
//
// Usage in Scriggo templates:
//
//	{% var config = map[string]any{"a": 1, "b": 2} %}
//	{% config = merge(config, map[string]any{"b": 3, "c": 4}) %}
//	{# Result: {"a": 1, "b": 3, "c": 4} #}
func scriggoMerge(dict, updates map[string]any) map[string]any {
	result := make(map[string]any, len(dict)+len(updates))
	maps.Copy(result, dict)
	maps.Copy(result, updates)
	return result
}

// scriggoKeys returns a sorted slice of keys from a map.
// Keys are sorted alphabetically for deterministic iteration order.
// Works with any map type that has string keys (map[string]any, map[string][]any, etc.).
//
// Usage in Scriggo templates:
//
//	{% var config = map[string]any{"b": 2, "a": 1, "c": 3} %}
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
	slices.Sort(keys)
	return keys
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
func scriggoNamespace(init map[string]any) map[string]any {
	if init == nil {
		return make(map[string]any)
	}
	return init
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
func scriggoCoalesce(value, defaultVal any) any {
	if value == nil {
		return defaultVal
	}
	return value
}

// scriggoJoin joins a string slice with a separator.
// Accepts any to handle both []string and []any from templates.
//
// Usage in Scriggo templates:
//
//	{%- var list = join(hosts, ", ") -%}
//	{{ items | join(" ") }}
func scriggoJoin(items any, sep string) string {
	switch v := items.(type) {
	case []string:
		return strings.Join(v, sep)
	case []any:
		strs := make([]string, len(v))
		for i, item := range v {
			strs[i] = scriggoToString(item)
		}
		return strings.Join(strs, sep)
	default:
		return ""
	}
}
