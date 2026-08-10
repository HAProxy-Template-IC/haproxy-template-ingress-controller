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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractStringName(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "string returns itself", in: "ingress", want: "ingress"},
		{name: "empty string", in: "", want: ""},
		{name: "map with name field", in: map[string]any{"name": "ingress"}, want: "ingress"},
		{name: "map without name field", in: map[string]any{"foo": "bar"}, want: ""},
		{name: "map with non-string name", in: map[string]any{"name": 42}, want: ""},
		{name: "nil returns empty", in: nil, want: ""},
		{name: "int returns empty", in: 42, want: ""},
		{name: "slice returns empty", in: []string{"a"}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractStringName(tt.in))
		})
	}
}

func TestGlobMatchStrings(t *testing.T) {
	tests := []struct {
		name    string
		items   []string
		pattern string
		want    []string
	}{
		{name: "exact match", items: []string{"foo", "bar"}, pattern: "foo", want: []string{"foo"}},
		{name: "wildcard", items: []string{"backend-api", "backend-web", "frontend-tls"}, pattern: "backend-*", want: []string{"backend-api", "backend-web"}},
		{name: "no match", items: []string{"foo", "bar"}, pattern: "baz", want: []string{}},
		{name: "empty items", items: nil, pattern: "*", want: []string{}},
		{name: "match all", items: []string{"a", "b"}, pattern: "*", want: []string{"a", "b"}},
		{name: "single char pattern", items: []string{"a", "ab", "abc"}, pattern: "?", want: []string{"a"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := globMatchStrings(tt.items, tt.pattern)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGlobMatchStrings_InvalidPattern(t *testing.T) {
	assert.Panics(t, func() {
		globMatchStrings([]string{"foo"}, "[invalid")
	})
}

func TestGlobMatchInterfaces(t *testing.T) {
	tests := []struct {
		name    string
		items   []any
		pattern string
		want    []string
	}{
		{name: "strings only", items: []any{"foo", "bar"}, pattern: "f*", want: []string{"foo"}},
		{name: "maps with name", items: []any{map[string]any{"name": "alpha"}, map[string]any{"name": "beta"}}, pattern: "a*", want: []string{"alpha"}},
		{name: "mixed strings and maps", items: []any{"foo", map[string]any{"name": "fizz"}}, pattern: "f*", want: []string{"foo", "fizz"}},
		{name: "empty name skipped", items: []any{"", "good"}, pattern: "*", want: []string{"good"}},
		{name: "non-string non-map skipped", items: []any{42, true, "good"}, pattern: "*", want: []string{"good"}},
		{name: "empty input", items: nil, pattern: "*", want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := globMatchInterfaces(tt.items, tt.pattern)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGlobMatchInterfaces_InvalidPattern(t *testing.T) {
	assert.Panics(t, func() {
		globMatchInterfaces([]any{"foo"}, "[invalid")
	})
}

func TestScriggoSortStrings(t *testing.T) {
	tests := []struct {
		name string
		in   []any
		want []string
	}{
		{name: "string slice", in: []any{"c", "a", "b"}, want: []string{"a", "b", "c"}},
		{name: "mixed types converted", in: []any{"b", 10, "a"}, want: []string{"10", "a", "b"}},
		{name: "empty slice", in: nil, want: []string{}},
		{name: "single element", in: []any{"only"}, want: []string{"only"}},
		{name: "duplicates preserved", in: []any{"a", "b", "a"}, want: []string{"a", "a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoSortStrings(tt.in))
		})
	}
}

func TestScriggoSortInts(t *testing.T) {
	tests := []struct {
		name string
		in   []any
		want []int
	}{
		{name: "int slice", in: []any{30, 10, 20}, want: []int{10, 20, 30}},
		{name: "numeric strings coerce", in: []any{"30", "10", "20"}, want: []int{10, 20, 30}},
		// Pre-filter motivation: lexicographic sort would produce {10, 2, 8080}; sort_ints gives the right order.
		{name: "lexicographic-vs-numeric", in: []any{10, 8080, 2}, want: []int{2, 10, 8080}},
		{name: "empty slice", in: nil, want: []int{}},
		{name: "non-numeric coerces to zero", in: []any{"abc", 5, "10"}, want: []int{0, 5, 10}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoSortInts(tt.in))
		})
	}
}

func TestWriteToBuilder(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "string", in: "hello", want: "hello"},
		{name: "int", in: 42, want: "42"},
		{name: "int64", in: int64(99), want: "99"},
		{name: "float64 integral", in: 3.0, want: "3"},
		{name: "float64 fractional", in: 3.14, want: "3.14"},
		{name: "bool true", in: true, want: "true"},
		{name: "bool false", in: false, want: "false"},
		{name: "nil writes nothing", in: nil, want: ""},
		{name: "fallback type via scriggoToString", in: []int{1, 2}, want: "[1 2]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			writeToBuilder(&b, tt.in)
			assert.Equal(t, tt.want, b.String())
		})
	}
}

func TestScriggoShardSlice(t *testing.T) {
	tenItems := []any{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}

	tests := []struct {
		name        string
		items       any
		shardIndex  int
		totalShards int
		want        any
	}{
		{name: "10 items, 3 shards, shard 0 (gets remainder)", items: tenItems, shardIndex: 0, totalShards: 3, want: []any{0, 1, 2, 3}},
		{name: "10 items, 3 shards, shard 1", items: tenItems, shardIndex: 1, totalShards: 3, want: []any{4, 5, 6}},
		{name: "10 items, 3 shards, shard 2", items: tenItems, shardIndex: 2, totalShards: 3, want: []any{7, 8, 9}},
		{name: "even distribution: 9 items, 3 shards, shard 0", items: []any{0, 1, 2, 3, 4, 5, 6, 7, 8}, shardIndex: 0, totalShards: 3, want: []any{0, 1, 2}},
		{name: "even distribution: 9 items, 3 shards, shard 1", items: []any{0, 1, 2, 3, 4, 5, 6, 7, 8}, shardIndex: 1, totalShards: 3, want: []any{3, 4, 5}},
		{name: "single shard returns all", items: tenItems, shardIndex: 0, totalShards: 1, want: tenItems},
		{name: "totalShards=0 returns all (sharding disabled)", items: tenItems, shardIndex: 0, totalShards: 0, want: tenItems},
		{name: "shardIndex >= totalShards returns all", items: tenItems, shardIndex: 5, totalShards: 3, want: tenItems},
		{name: "negative shardIndex returns all", items: tenItems, shardIndex: -1, totalShards: 3, want: tenItems},
		{name: "empty items returns empty", items: []any{}, shardIndex: 0, totalShards: 3, want: []any{}},
		{name: "non-slice items returns empty", items: 42, shardIndex: 0, totalShards: 3, want: []any{}},
		{name: "more shards than items", items: []any{1, 2}, shardIndex: 0, totalShards: 4, want: []any{1}},
		{name: "more shards than items, shard beyond", items: []any{1, 2}, shardIndex: 3, totalShards: 4, want: []any{}},
		// scriggoShardSlice preserves the input slice's element type
		// (the type-preserving behaviour AdaptiveFunc wraps). For
		// []string input the result is []string, not []any.
		{name: "[]string preserves slice element type", items: []string{"a", "b", "c", "d"}, shardIndex: 1, totalShards: 2, want: []string{"c", "d"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scriggoShardSlice(tt.items, tt.shardIndex, tt.totalShards)
			assert.Equal(t, tt.want, got)
		})
	}
}
