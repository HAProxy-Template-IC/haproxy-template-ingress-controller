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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

// stringBuilderPool pools strings.Builder instances for reuse.
// Used by scriggoFirstSeen to avoid allocating a new builder on every call.
var stringBuilderPool = sync.Pool{
	New: func() interface{} {
		return &strings.Builder{}
	},
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

// scriggoFirstSeen checks if a composite key is being seen for the first time.
// Returns true on first occurrence, false on subsequent calls with the same key.
// Uses SharedContext.ComputeIfAbsent with the wasComputed return value for thread-safe
// atomic check-and-set during parallel template rendering.
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

	shared := getSharedContext(env)
	if shared == nil {
		return true // No shared context = treat as first time
	}

	// Get pooled builder to avoid allocating a new one on every call.
	b := stringBuilderPool.Get().(*strings.Builder)
	b.Reset()
	b.Grow(len(parts) * 24) // Estimate: ~20 chars per part + separators

	for i, part := range parts {
		if i > 0 {
			b.WriteByte('_')
		}
		writeToBuilder(b, part)
	}

	// Extract key string before returning builder to pool.
	// b.String() allocates - this is unavoidable since ComputeIfAbsent needs a string key.
	key := b.String()
	stringBuilderPool.Put(b)

	// Use ComputeIfAbsent - wasComputed tells us if this is the first occurrence.
	// The compute function just stores a marker value; what matters is wasComputed.
	_, wasFirst := shared.ComputeIfAbsent(key, func() interface{} {
		return true
	})
	return wasFirst
}

// writeToBuilder writes a value to a strings.Builder, inlining common type conversions.
// This avoids allocations from fmt.Sprintf for common types.
func writeToBuilder(b *strings.Builder, v interface{}) {
	switch val := v.(type) {
	case string:
		b.WriteString(val)
	case int:
		b.WriteString(strconv.Itoa(val))
	case int64:
		b.WriteString(strconv.FormatInt(val, 10))
	case float64:
		b.WriteString(strconv.FormatFloat(val, 'f', -1, 64))
	case bool:
		if val {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	default:
		if val == nil {
			return
		}
		fmt.Fprintf(b, "%v", v)
	}
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

// scriggoShardSlice divides a slice into N shards and returns the portion for a given shard index.
// This is used for parallel template rendering where work is split across goroutines.
//
// The function divides items evenly across shards, distributing remainder items
// to the first shards (shards 0..remainder-1 get one extra item).
//
// Example: 10 items with 3 shards:
//   - Shard 0: items[0:4]  (4 items - gets extra because remainder=1)
//   - Shard 1: items[4:7]  (3 items)
//   - Shard 2: items[7:10] (3 items)
//
// Usage in Scriggo templates:
//
//	{%- var shard = shard_slice(allItems, shardIdx, totalShards) %}
//	{%- for _, item := range shard %}
//	  ... process item in parallel shard ...
//	{%- end %}
func scriggoShardSlice(items interface{}, shardIndex, totalShards int) []interface{} {
	// Convert items to slice
	itemsSlice, ok := toSlice(items)
	if !ok || len(itemsSlice) == 0 {
		return []interface{}{}
	}

	// If sharding is disabled or invalid, return full slice
	if totalShards <= 1 || shardIndex >= totalShards || shardIndex < 0 {
		return itemsSlice
	}

	totalItems := len(itemsSlice)
	baseSize := totalItems / totalShards
	remainder := totalItems % totalShards

	// Calculate start index: sum of previous shard sizes
	// Shards 0..remainder-1 get baseSize+1 items each
	// Shards remainder..totalShards-1 get baseSize items each
	start := shardIndex*baseSize + min(shardIndex, remainder)
	end := start + baseSize
	if shardIndex < remainder {
		end++
	}

	// Ensure bounds are valid
	if start >= totalItems {
		return []interface{}{}
	}
	if end > totalItems {
		end = totalItems
	}

	return itemsSlice[start:end]
}
