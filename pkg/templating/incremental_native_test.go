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
	"math"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type incrementalNativeCustomMarshaler struct {
	calls *int
}

func (m incrementalNativeCustomMarshaler) MarshalJSON() ([]byte, error) {
	(*m.calls)++
	return []byte(`"ambient"`), nil
}

type incrementalNativeSerializable struct {
	Value *int `json:"value" yaml:"value"`
}

func renderIncrementalNative(
	t *testing.T,
	source string,
	item map[string]any,
) (string, error) {
	t.Helper()
	if item == nil {
		item = map[string]any{}
	}
	engine, err := New(map[string]string{"component": source}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	if err != nil {
		return "", err
	}
	return engine.RenderIncrementalComponent(t.Context(), "component", map[string]any{
		"item":          item,
		"source":        "test",
		"props":         map[string]any{},
		"renderSubject": map[string]any{"mode": "reconcile"},
		"shared":        NewSharedContributionContext(&sharedRecorder{}),
	})
}

func TestIncrementalNativeOutputIgnoresScalarPointerAllocation(t *testing.T) {
	source := `{%- var rows = item["rows"].([]any) -%}` +
		`{{- tostring(item["scalar"]) -}}|` +
		`{%- for _, row := range sort_by(rows, []string{"$.rank"}) -%}` +
		`{{- dig_string(row, "", "name") -}}` +
		`{%- end -%}|` +
		`{%- for _, value := range unique(item["values"].([]any)) -%}` +
		`{{- tostring(value) -}}` +
		`{%- end -%}`
	makeItem := func() map[string]any {
		scalar := int64(7)
		firstRank, secondRank := int16(1), int16(2)
		one, duplicateOne, two := uint32(1), uint32(1), uint32(2)
		return map[string]any{
			"scalar": &scalar,
			"rows": []any{
				map[string]any{"name": "b", "rank": &secondRank},
				map[string]any{"name": "a", "rank": &firstRank},
			},
			"values": []any{&one, &duplicateOne, &two},
		}
	}

	first, err := renderIncrementalNative(t, source, makeItem())
	require.NoError(t, err)
	second, err := renderIncrementalNative(t, source, makeItem())
	require.NoError(t, err)

	assert.Equal(t, "7|ab|12", strings.TrimSuffix(first, "\n"))
	assert.Equal(t, first, second)
}

func TestIncrementalNativeKeyWrappersUseScalarValues(t *testing.T) {
	source := `{%- var rows = item["rows"].([]any) -%}` +
		`{%- var groups = group_by(rows, "key") -%}` +
		`{%- var counts = count_by(rows, "key") -%}` +
		`{%- var indexed = index_by(rows, "key") -%}` +
		`{%- for _, key := range keys(groups) -%}` +
		`{{- key }}={{ len(groups[key]) }}/{{ counts[key] }}/{{ dig_string(indexed[key], "", "name") }};` +
		`{%- end -%}`
	firstKey, duplicateFirstKey, secondKey := int64(1), int64(1), int64(2)
	output, err := renderIncrementalNative(t, source, map[string]any{
		"rows": []any{
			map[string]any{"name": "a", "key": &firstKey},
			map[string]any{"name": "c", "key": &duplicateFirstKey},
			map[string]any{"name": "b", "key": &secondKey},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "1=2/2/c;2=1/1/b;", strings.TrimSuffix(output, "\n"))
}

func TestIncrementalNativeRejectsNonScalarCoercions(t *testing.T) {
	var unsafePointer unsafe.Pointer
	tests := []struct {
		name   string
		source string
		item   map[string]any
		want   string
	}{
		{name: "tostring pointer", source: `{{ tostring(item["bad"]) }}`, item: map[string]any{"bad": &struct{ Value int }{Value: 1}}, want: FuncToString},
		{name: "tostring function", source: `{{ tostring(item["bad"]) }}`, item: map[string]any{"bad": func() {}}, want: FuncToString},
		{name: "tostring channel", source: `{{ tostring(item["bad"]) }}`, item: map[string]any{"bad": make(chan int)}, want: FuncToString},
		{name: "tostring unsafe pointer", source: `{{ tostring(item["bad"]) }}`, item: map[string]any{"bad": unsafePointer}, want: FuncToString},
		{name: "tostring NaN", source: `{{ tostring(item["bad"]) }}`, item: map[string]any{"bad": math.NaN()}, want: FuncToString},
		{name: "sort strings", source: `{{ join(sort_strings(item["bad"].([]any)), ",") }}`, item: map[string]any{"bad": []any{struct{}{}}}, want: FuncSortStrings},
		{name: "string slice", source: `{{ join(toStringSlice(item["bad"]), ",") }}`, item: map[string]any{"bad": []any{struct{}{}}}, want: FuncToStringSlice},
		{name: "string map", source: `{{ len(to_str_map(item["bad"])) }}`, item: map[string]any{"bad": map[string]any{"key": struct{}{}}}, want: FuncToStrMap},
		{name: "semver", source: `{{ semver_gte(item["bad"], "1.0") }}`, item: map[string]any{"bad": struct{}{}}, want: FuncSemverGte},
		{name: "dig string", source: `{{ dig_string(item, "", "bad") }}`, item: map[string]any{"bad": struct{}{}}, want: FuncDigString},
		{name: "join key", source: `{{ join_key(":", item["bad"]) }}`, item: map[string]any{"bad": struct{}{}}, want: FuncJoinKey},
		{name: "GUID", source: `{{ make_guid(item["bad"]) }}`, item: map[string]any{"bad": struct{}{}}, want: FuncMakeGUID},
		{name: "string helper", source: `{{ strings_contains(item["bad"], "x") }}`, item: map[string]any{"bad": struct{}{}}, want: FuncStringsContains},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := renderIncrementalNative(t, test.source, test.item)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.want)
		})
	}
}

func TestIncrementalNativeRejectsUnsafeKeys(t *testing.T) {
	tests := []struct {
		name   string
		source string
		rows   []any
		want   string
	}{
		{
			name:   "sort structured key",
			source: `{% var _ = sort_by(item["rows"].([]any), []string{"$.key"}) %}`,
			rows:   []any{map[string]any{"key": struct{}{}}},
			want:   FilterSortBy,
		},
		{
			name:   "unique NaN key",
			source: `{% var _ = unique(item["rows"].([]any)) %}`,
			rows:   []any{math.NaN()},
			want:   FuncUnique,
		},
		{
			name:   "unique structured key",
			source: `{% var _ = unique(item["rows"].([]any)) %}`,
			rows:   []any{struct{}{}},
			want:   FuncUnique,
		},
		{
			name:   "group pointer key",
			source: `{% var _ = group_by(item["rows"].([]any), "key") %}`,
			rows:   []any{map[string]any{"key": &struct{}{}}},
			want:   FuncGroupBy,
		},
		{
			name:   "count function key",
			source: `{% var _ = count_by(item["rows"].([]any), "key") %}`,
			rows:   []any{map[string]any{"key": func() {}}},
			want:   "count_by",
		},
		{
			name:   "index channel key",
			source: `{% var _ = index_by(item["rows"].([]any), "key") %}`,
			rows:   []any{map[string]any{"key": make(chan int)}},
			want:   "index_by",
		},
		{
			name:   "sort comparator",
			source: `{% var _ = sort_by(item["rows"].([]any), func(a any, b any) int { return 0 }) %}`,
			rows:   []any{"a", "b"},
			want:   "comparator functions are unavailable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := renderIncrementalNative(t, test.source, map[string]any{"rows": test.rows})
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.want)
		})
	}
}

func TestIncrementalNativeRejectsAmbiguousStringMapKeys(t *testing.T) {
	sources := map[string]string{
		FuncGroupBy: `{% var _ = group_by(item["rows"].([]any), "key") %}`,
		"count_by":  `{% var _ = count_by(item["rows"].([]any), "key") %}`,
		"index_by":  `{% var _ = index_by(item["rows"].([]any), "key") %}`,
	}
	rows := []any{
		map[string]any{"key": nil},
		map[string]any{"key": ""},
	}

	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			_, err := renderIncrementalNative(t, source, map[string]any{"rows": rows})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "distinct scalar keys")
		})
	}
}

func TestIncrementalNativeMarshalFailuresStopRendering(t *testing.T) {
	sources := map[string]string{
		FilterToJSON:        `{{ toJSON(item["bad"]) }}`,
		"marshalJSON":       `{{ marshalJSON(item["bad"]) }}`,
		"marshalJSONIndent": `{{ marshalJSONIndent(item["bad"], "", "  ") }}`,
		"marshalYAML":       `{{ marshalYAML(item["bad"]) }}`,
	}

	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			_, err := renderIncrementalNative(t, source, map[string]any{"bad": func() {}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), name)
		})
	}
}

func TestIncrementalNativeRejectsCustomMarshalersWithoutCallingThem(t *testing.T) {
	sources := map[string]string{
		FilterToJSON:        `{{ toJSON(item["bad"]) }}`,
		"marshalJSON":       `{{ marshalJSON(item["bad"]) }}`,
		"marshalJSONIndent": `{{ marshalJSONIndent(item["bad"], "", "  ") }}`,
		"marshalYAML":       `{{ marshalYAML(item["bad"]) }}`,
	}

	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			calls := 0
			_, err := renderIncrementalNative(t, source, map[string]any{
				"bad": incrementalNativeCustomMarshaler{calls: &calls},
			})
			require.ErrorContains(t, err, "uses a custom marshaler")
			assert.Zero(t, calls)
		})
	}
}

func TestIncrementalNativeSerializationIgnoresAllocationAndMapInsertionOrder(t *testing.T) {
	source := `{{ toJSON(item["value"]) }}|{{ marshalYAML(item["value"]) }}`
	makeItem := func(reverse bool) map[string]any {
		value := 7
		fields := make(map[string]any, 2)
		if reverse {
			fields["b"] = incrementalNativeSerializable{Value: &value}
			fields["a"] = 1
		} else {
			fields["a"] = 1
			fields["b"] = incrementalNativeSerializable{Value: &value}
		}
		return map[string]any{"value": fields}
	}

	first, err := renderIncrementalNative(t, source, makeItem(false))
	require.NoError(t, err)
	second, err := renderIncrementalNative(t, source, makeItem(true))
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Contains(t, first, `{"a":1,"b":{"value":7}}`)
}

func TestIncrementalNativeSerializationIgnoresPointerAliasing(t *testing.T) {
	source := `{{ toJSON(item["value"]) }}|{{ marshalYAML(item["value"]) }}`
	sharedValue := 7
	shared := &incrementalNativeSerializable{Value: &sharedValue}
	aliased := map[string]any{"a": shared, "b": shared}
	firstValue, secondValue := 7, 7
	disjoint := map[string]any{
		"a": &incrementalNativeSerializable{Value: &firstValue},
		"b": &incrementalNativeSerializable{Value: &secondValue},
	}

	first, err := renderIncrementalNative(t, source, map[string]any{"value": aliased})
	require.NoError(t, err)
	second, err := renderIncrementalNative(t, source, map[string]any{"value": disjoint})
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestIncrementalNativeSerializationRejectsCyclesAndNonStringMapKeys(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	tests := map[string]any{
		"cycle":               cycle,
		"non-string map keys": map[int]string{1: "value"},
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := renderIncrementalNative(t, `{{ toJSON(item["value"]) }}`, map[string]any{"value": value})
			require.Error(t, err)
		})
	}
}

func TestIncrementalTimeHelpersExcludeAmbientTimezoneState(t *testing.T) {
	tests := map[string]struct {
		source string
		want   string
	}{
		"date local": {
			source: `{{ date(2026, 8, 24, 12, 0, 0, 0, "Local") }}`,
			want:   "only support UTC",
		},
		"date named timezone": {
			source: `{{ date(2026, 8, 24, 12, 0, 0, 0, "Europe/Berlin") }}`,
			want:   "only support UTC",
		},
		"parse time implicit layout": {
			source: `{{ parseTime("", "2026-08-24T12:00:00Z") }}`,
			want:   "require an explicit layout",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := renderIncrementalNative(t, test.source, nil)
			require.ErrorContains(t, err, test.want)
		})
	}

	source := `{{ date(2026, 8, 24, 12, 0, 0, 0, "UTC") }}|` +
		`{{ parseTime("2006-01-02 15:04 MST", "2026-08-24 12:00 CEST") }}|` +
		`{{ unixTime(1787572800, 0) }}`
	first, err := renderIncrementalNative(t, source, nil)
	require.NoError(t, err)
	second, err := renderIncrementalNative(t, source, nil)
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestIncrementalHashAndEncodingBuiltinsAreContentOnly(t *testing.T) {
	source := `{{ hmacSHA1("message", "key") }}|{{ hmacSHA256("message", "key") }}|` +
		`{{ sha1("value") }}|{{ sha256("value") }}|{{ md5("value") }}|` +
		`{{ base64("value") }}|{{ hex("value") }}|{{ htmlEscape("<value>") }}`
	first, err := renderIncrementalNative(t, source, nil)
	require.NoError(t, err)
	second, err := renderIncrementalNative(t, source, nil)
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestIncrementalUntarGzUsesContentOnly(t *testing.T) {
	archive := makeTarGz(t,
		tarEntry{name: "rules/b.conf", content: "b"},
		tarEntry{name: "rules/a.conf", content: "a"},
	)
	source := `{% var files, err = untar_gz(item["archive"].(string)) %}` +
		`{{ tostring(err) }}|{% for name := range files %}{{ name }}={{ files[name] }};{% end %}`
	first, err := renderIncrementalNative(t, source, map[string]any{"archive": archive})
	require.NoError(t, err)
	second, err := renderIncrementalNative(t, source, map[string]any{"archive": archive})
	require.NoError(t, err)
	assert.Equal(t, "|rules/a.conf=a;rules/b.conf=b;", strings.TrimSuffix(first, "\n"))
	assert.Equal(t, first, second)
}

func TestIncrementalNativeRejectsNonFiniteMath(t *testing.T) {
	tests := map[string]struct {
		source string
		item   map[string]any
	}{
		"formatFloat": {`{{ formatFloat(item["value"].(float64), "g", -1) }}`, map[string]any{"value": math.Inf(1)}},
		"pow input":   {`{{ pow(item["value"].(float64), 2) }}`, map[string]any{"value": math.NaN()}},
		"pow result":  {`{{ pow(1e308, 2) }}`, nil},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := renderIncrementalNative(t, test.source, test.item)
			require.ErrorContains(t, err, "non-finite float")
		})
	}
}

func TestIncrementalNativeAliasesCannotMutateInputs(t *testing.T) {
	sources := map[string]string{
		"merge":       `{% var values = merge(item, map[string]any{}) %}{% values["nested"].(map[string]any)["value"] = "changed" %}`,
		"namespace":   `{% var values = namespace(item) %}{% values["nested"].(map[string]any)["value"] = "changed" %}`,
		"coalesce":    `{% var values = coalesce(item, map[string]any{}) %}{% values.(map[string]any)["nested"].(map[string]any)["value"] = "changed" %}`,
		"fallback":    `{% var values = fallback(item, map[string]any{}) %}{% values.(map[string]any)["nested"].(map[string]any)["value"] = "changed" %}`,
		"dig":         `{% var value = dig(item, "nested").(map[string]any) %}{% value["value"] = "changed" %}`,
		"jsonpath":    `{% var value = jsonpathGet(item, "nested").(map[string]any) %}{% value["value"] = "changed" %}`,
		"toSlice":     `{% var values = toSlice(item["values"]) %}{% values[0].(map[string]any)["value"] = "changed" %}`,
		"shard":       `{% var values = shard_slice(item["values"].([]any), 0, 2) %}{% values[0].(map[string]any)["value"] = "changed" %}`,
		"sort":        `{% var values = sort_by(item["values"].([]any), []string{"$.value"}) %}{% values[0].(map[string]any)["value"] = "changed" %}`,
		"map":         `{% var values = map(item["values"].([]any), func(value any) any { return value }) %}{% values[0].(map[string]any)["value"] = "changed" %}`,
		"filter":      `{% var values = filter(item["values"].([]any), func(value any) bool { return true }) %}{% values[0].(map[string]any)["value"] = "changed" %}`,
		"reject":      `{% var values = reject(item["values"].([]any), func(value any) bool { return false }) %}{% values[0].(map[string]any)["value"] = "changed" %}`,
		"flat map":    `{% var values = flat_map(item["buckets"].([]any), func(value any) []any { return value.([]any) }) %}{% values[0].(map[string]any)["value"] = "changed" %}`,
		"unique by":   `{% var values = unique_by(item["values"].([]any), "value") %}{% values[0].(map[string]any)["value"] = "changed" %}`,
		"group by":    `{% var groups = group_by(item["values"].([]any), "value") %}{% groups["original"][0].(map[string]any)["value"] = "changed" %}`,
		"selectattr":  `{% var values = selectattr(item["values"], "value") %}{% values[0].(map[string]any)["value"] = "changed" %}`,
		"index by":    `{% var values = index_by(item["values"], "value") %}{% values["original"].(map[string]any)["value"] = "changed" %}`,
		"map extract": `{% var values = map_extract(item["values"], "child") %}{% values[0].(map[string]any)["value"] = "changed" %}`,
	}

	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			child := map[string]any{"value": "original"}
			nested := map[string]any{"value": "original", "child": child}
			values := []any{nested}
			_, err := renderIncrementalNative(t, source, map[string]any{
				"nested":  nested,
				"values":  values,
				"buckets": []any{values},
			})
			require.ErrorContains(t, err, "mutates an immutable input")
			assert.Equal(t, "original", nested["value"])
			assert.Equal(t, "original", child["value"])
		})
	}
}

func TestIncrementalNativeLambdaCannotMutateInputs(t *testing.T) {
	nested := map[string]any{"value": "original"}
	_, err := renderIncrementalNative(t,
		`{% var values = map(item["values"].([]any), func(value any) any { value.(map[string]any)["value"] = "changed"; return value }) %}{{ len(values) }}`,
		map[string]any{"values": []any{nested}},
	)
	require.ErrorContains(t, err, "mutates an immutable input")
	assert.Equal(t, "original", nested["value"])
}

func TestOrdinaryTemplatesRejectNondeterministicNativeValues(t *testing.T) {
	engine, err := New(map[string]string{
		"ordinary": `{{ tostring(item["value"]) }}`,
	}, &Options{EntryPoints: []string{"ordinary"}})
	require.NoError(t, err)

	_, err = engine.Render(t.Context(), "ordinary", map[string]any{
		"item": map[string]any{"value": map[string]any{"key": "value"}},
	})
	require.ErrorContains(t, err, "has no deterministic scalar representation")
}
