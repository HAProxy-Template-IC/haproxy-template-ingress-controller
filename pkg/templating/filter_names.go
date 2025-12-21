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

// Filter name constants for template engines.
const (
	// FilterSortBy sorts items by JSONPath criteria.
	FilterSortBy = "sort_by"

	// FilterGlobMatch filters items by glob pattern.
	FilterGlobMatch = "glob_match"

	// FilterStrip removes leading/trailing whitespace.
	FilterStrip = "strip"

	// FilterTrim removes leading/trailing whitespace (alias for strip).
	FilterTrim = "trim"

	// FilterB64Decode decodes base64-encoded strings.
	FilterB64Decode = "b64decode"

	// FilterDebug outputs debug information for items.
	FilterDebug = "debug"

	// FilterIndent indents each line of a string by a specified amount.
	FilterIndent = "indent"
)

// Function name constants for template engines.
const (
	// FuncFail stops template execution with an error message.
	FuncFail = "fail"

	// FuncMerge merges two maps, returning a new map.
	// Values from the second map override values from the first.
	FuncMerge = "merge"

	// FuncKeys returns sorted keys from a map.
	FuncKeys = "keys"

	// FuncSortStrings sorts a []any slice as strings.
	// Useful when append() returns []any but you need sorted strings.
	FuncSortStrings = "sort_strings"

	// String manipulation functions (Scriggo).

	// FuncStringsContains checks if a string contains a substring.
	FuncStringsContains = "strings_contains"

	// FuncStringsSplit splits a string by a separator.
	FuncStringsSplit = "strings_split"

	// FuncStringsTrim trims whitespace from a string.
	FuncStringsTrim = "strings_trim"

	// FuncStringsLower converts a string to lowercase.
	FuncStringsLower = "strings_lower"

	// FuncStringsReplace replaces all occurrences of old with new.
	FuncStringsReplace = "strings_replace"

	// FuncStringsSplitN splits a string by a separator with a maximum number of parts.
	FuncStringsSplitN = "strings_splitn"

	// Type conversion functions (Scriggo only).

	// FuncToString converts a value to string.
	FuncToString = "tostring"

	// FuncToInt converts a value to int.
	FuncToInt = "toint"

	// FuncToFloat converts a value to float64.
	FuncToFloat = "tofloat"

	// Utility functions (Scriggo only).

	// FuncCeil returns the ceiling of a float.
	FuncCeil = "ceil"

	// FuncSeq generates a sequence of integers from 0 to n-1.
	// Syntax: seq(n) returns []int{0, 1, 2, ..., n-1}.
	FuncSeq = "seq"

	// FuncRegexSearch checks if a string matches a regex pattern.
	FuncRegexSearch = "regex_search"

	// FuncIsDigit checks if a string contains only digits.
	FuncIsDigit = "isdigit"

	// FuncSanitizeRegex escapes regex special characters.
	FuncSanitizeRegex = "sanitize_regex"

	// FuncTitle converts a string to title case.
	FuncTitle = "title"

	// FuncDig navigates nested maps/structures using a sequence of keys.
	// Returns nil if any key along the path is missing.
	// Ruby-style dig: obj | dig("metadata", "namespace") | fallback("")
	// Available in: Scriggo only.
	FuncDig = "dig"

	// FuncToStringSlice converts []any to []string.
	// Available in: Scriggo only.
	FuncToStringSlice = "toStringSlice"

	// FuncJoin joins a string slice with a separator.
	// Available in: Scriggo only.
	FuncJoin = "join"

	// FuncReplace replaces strings (alias for strings_replace).
	FuncReplace = "replace"

	// FuncCoalesce returns the first non-nil value, or the default if nil.
	// Workaround for Scriggo's default operator limitation with field access.
	FuncCoalesce = "coalesce"

	// FuncFallback returns the first non-nil value, or the fallback if nil.
	// Jinja2-style alias for coalesce, works well with pipe syntax (e.g., {{ value | fallback("default") }}).
	FuncFallback = "fallback"

	// FuncNamespace creates a mutable map for storing state in loops.
	FuncNamespace = "namespace"

	// Slice manipulation functions.

	// FuncToSlice converts any value to []any for safe ranging.
	// Returns empty slice if input is nil, otherwise converts to []any.
	// Syntax: toSlice(value) - returns []any.
	FuncToSlice = "toSlice"

	// FuncAppendAny appends an item to a slice, handling interface{} types.
	// Syntax: append(slice, item) - returns []any
	// If slice is nil, creates a new slice with the item.
	FuncAppendAny = "append"

	// Deduplication and filtering functions.

	// FuncFirstSeen checks if a composite key is being seen for the first time.
	// Returns true on first occurrence, false on subsequent calls with same key.
	// Thread-safe for parallel template rendering.
	// Syntax: first_seen("prefix", key1, key2, ...) returns bool.
	FuncFirstSeen = "first_seen"

	// FuncSelectAttr filters items by attribute existence or value.
	// Jinja2-compatible filter for selecting items from a sequence.
	// Syntax:
	//   selectattr(items, "attr")           - items where attr is defined/truthy
	//   selectattr(items, "attr", "eq", v)  - items where attr equals v
	//   selectattr(items, "attr", "ne", v)  - items where attr not equals v
	//   selectattr(items, "attr", "in", list) - items where attr is in list
	FuncSelectAttr = "selectattr"

	// FuncJoinKey joins multiple values into a composite key string with a separator.
	// Automatically converts all values to strings.
	// Syntax: join_key("_", val1, val2, ...) returns string.
	FuncJoinKey = "join_key"

	// FuncShardSlice divides a slice into N shards and returns the portion for a given shard index.
	// Used for parallel template rendering where work is split across goroutines.
	// Syntax: shard_slice(items, shardIndex, totalShards) returns []any.
	FuncShardSlice = "shard_slice"
)
