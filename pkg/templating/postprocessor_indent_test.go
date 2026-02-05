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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndentNormalizerProcessor_MatchesRegex(t *testing.T) {
	// Verify that IndentNormalizerProcessor produces identical output to RegexReplaceProcessor
	// for all test cases from the existing regex tests.
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "normalize leading spaces to 2 spaces",
			input: `global
    log stdout
        maxconn 2000
    daemon
defaults
    mode http
        timeout connect 5s`,
			expected: `global
  log stdout
  maxconn 2000
  daemon
defaults
  mode http
  timeout connect 5s`,
		},
		{
			name: "no change when no leading spaces",
			input: `global
defaults`,
			expected: `global
defaults`,
		},
		{
			name: "handle mixed indentation",
			input: `global
    option 1
        option 2
            option 3`,
			expected: `global
  option 1
  option 2
  option 3`,
		},
		{
			name: "preserve empty lines",
			input: `global
    daemon

defaults
    mode http`,
			expected: `global
  daemon

defaults
  mode http`,
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "trailing newline preserved",
			input:    "    indented\n",
			expected: "  indented\n",
		},
		{
			name:     "single line with spaces",
			input:    "    indented",
			expected: "  indented",
		},
		{
			name:     "single space normalized",
			input:    " single",
			expected: "  single",
		},
		{
			name:     "exactly two spaces unchanged content",
			input:    "  already",
			expected: "  already",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indent := &IndentNormalizerProcessor{indent: "  "}
			result, err := indent.Process(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)

			// Cross-check against regex processor for non-empty inputs
			if tt.input != "" {
				regex, err := NewRegexReplaceProcessor("^[ ]+", "  ")
				require.NoError(t, err)
				regexResult, err := regex.Process(tt.input)
				require.NoError(t, err)
				assert.Equal(t, regexResult, result, "IndentNormalizerProcessor output must match RegexReplaceProcessor output")
			}
		})
	}
}

func TestNewPostProcessor_UsesIndentFastPath(t *testing.T) {
	config := PostProcessorConfig{
		Type: PostProcessorTypeRegexReplace,
		Params: map[string]string{
			"pattern": "^[ ]+",
			"replace": "  ",
		},
	}

	processor, err := NewPostProcessor(config)
	require.NoError(t, err)

	// Verify the fast-path type is used
	_, ok := processor.(*IndentNormalizerProcessor)
	assert.True(t, ok, "expected IndentNormalizerProcessor for ^[ ]+ pattern")

	// Verify it works correctly
	result, err := processor.Process("    indented")
	require.NoError(t, err)
	assert.Equal(t, "  indented", result)
}

func TestNewPostProcessor_UsesRegexForOtherPatterns(t *testing.T) {
	config := PostProcessorConfig{
		Type: PostProcessorTypeRegexReplace,
		Params: map[string]string{
			"pattern": "^\\t+",
			"replace": "  ",
		},
	}

	processor, err := NewPostProcessor(config)
	require.NoError(t, err)

	// Non-indent patterns should still use regex
	_, ok := processor.(*RegexReplaceProcessor)
	assert.True(t, ok, "expected RegexReplaceProcessor for non-indent pattern")
}

func BenchmarkIndentNormalizerProcessor(b *testing.B) {
	// Build a realistic HAProxy config with mixed indentation
	input := buildBenchmarkInput()

	indent := &IndentNormalizerProcessor{indent: "  "}
	regex, _ := NewRegexReplaceProcessor("^[ ]+", "  ")

	b.Run("IndentNormalizer", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = indent.Process(input)
		}
	})

	b.Run("RegexReplace", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = regex.Process(input)
		}
	})
}

func buildBenchmarkInput() string {
	// Simulate a ~2000 line HAProxy config
	var lines []string
	for i := 0; i < 500; i++ {
		lines = append(lines,
			"frontend http_"+string(rune('a'+i%26)),
			"    bind *:80",
			"    mode http",
			"        option httplog",
			"        option forwardfor",
			"    default_backend servers",
		)
	}
	result := ""
	for _, l := range lines {
		result += l + "\n"
	}
	return result
}
