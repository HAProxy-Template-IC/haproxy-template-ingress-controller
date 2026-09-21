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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScriggoParseYAML(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        any
		invalid     bool
	}{
		{"map", "enabled: true\nvalues: [a, b]", map[string]any{"enabled": true, "values": []any{"a", "b"}}, false},
		{"sequence", "[1, 2]", []any{1, 2}, false},
		{"scalar", "hello", "hello", false},
		{"null", "null", nil, false},
		{"empty", "", nil, true},
		{"invalid", "a: [", nil, true},
		{"duplicate", "a: 1\na: 2", nil, true},
		{"extra document", "a: 1\n---\na: 2", nil, true},
		{"extra empty document", "a: 1\n---", nil, true},
		{"invalid suffix", "a: 1\n---\n[", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scriggoParseYAML(tc.input)
			if tc.invalid {
				require.Error(t, err)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestParseYAMLIncrementalEntryPoint(t *testing.T) {
	source := `{% var parsed, err = parse_yaml(item["yaml"].(string)) %}{% if err != nil %}{{ err.Error() }}{% else %}{% var value = parsed.(map[string]any) %}{% value["owned"] = "yes" %}{{ value["name"] }}:{{ value["owned"] }}{% end %}`
	engine, err := New(map[string]string{"test": source}, &Options{
		EntryPoints: []string{"test"}, IncrementalEntryPoints: []string{"test"},
	})
	require.NoError(t, err)
	require.True(t, engine.compiledTemplates["test"].BatchSafe())
	require.NoError(t, engine.compiledTemplates["test"].DeterministicSafe())
	for _, tc := range []struct{ input, want string }{
		{"name: first", "first:yes"}, {"invalid: [", "parse_yaml: yaml: line 1: did not find expected node content"}, {"name: second", "second:yes"},
	} {
		output, renderErr := engine.RenderIncrementalComponent(t.Context(), "test", incrementalComponentContext(map[string]any{"item": map[string]any{"yaml": tc.input}}))
		require.NoError(t, renderErr)
		require.Equal(t, tc.want, output)
	}
	replayEngine, err := New(map[string]string{"test": `{% var parsed, err = parse_yaml("invalid: [") %}{% if err != nil %}{{ err.Error() }}{% else %}{{ parsed.(map[string]any)["a"] }}{% end %}`}, &Options{EntryPoints: []string{"test"}})
	require.NoError(t, err)
	_, err = replayEngine.PrepareExactCycleReplay([]string{"test"})
	require.NoError(t, err)
}
