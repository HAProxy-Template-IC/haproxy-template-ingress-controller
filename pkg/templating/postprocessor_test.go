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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegexReplaceProcessor_IndentationNormalization(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		replace  string
		input    string
		expected string
	}{
		{
			name:    "normalize leading spaces to 2 spaces",
			pattern: "^[ ]+",
			replace: "  ",
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
			name:    "no change when no leading spaces",
			pattern: "^[ ]+",
			replace: "  ",
			input: `global
defaults`,
			expected: `global
defaults`,
		},
		{
			name:    "handle mixed indentation",
			pattern: "^[ ]+",
			replace: "  ",
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
			name:    "preserve empty lines",
			pattern: "^[ ]+",
			replace: "  ",
			input: `global
    daemon

defaults
    mode http`,
			expected: `global
  daemon

defaults
  mode http`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor, err := NewRegexReplaceProcessor(tt.pattern, tt.replace)
			require.NoError(t, err)

			result, err := processor.Process(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRegexReplaceProcessor_InvalidPattern(t *testing.T) {
	_, err := NewRegexReplaceProcessor("[invalid(", "replacement")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid regex pattern")
}

func TestRegexReplaceProcessor_EmptyInput(t *testing.T) {
	processor, err := NewRegexReplaceProcessor("^[ ]+", "  ")
	require.NoError(t, err)

	result, err := processor.Process("")
	require.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestNewPostProcessor_RegexReplace(t *testing.T) {
	config := PostProcessorConfig{
		Type: PostProcessorTypeRegexReplace,
		Params: map[string]string{
			"pattern": "^[ ]+",
			"replace": "  ",
		},
	}

	processor, err := NewPostProcessor(config)
	require.NoError(t, err)
	assert.NotNil(t, processor)

	// Test it works
	result, err := processor.Process("    indented")
	require.NoError(t, err)
	assert.Equal(t, "  indented", result)
}

func TestNewPostProcessor_MissingPattern(t *testing.T) {
	config := PostProcessorConfig{
		Type: PostProcessorTypeRegexReplace,
		Params: map[string]string{
			"replace": "  ",
		},
	}

	_, err := NewPostProcessor(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires 'pattern' parameter")
}

func TestNewPostProcessor_MissingReplace(t *testing.T) {
	config := PostProcessorConfig{
		Type: PostProcessorTypeRegexReplace,
		Params: map[string]string{
			"pattern": "^[ ]+",
		},
	}

	_, err := NewPostProcessor(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires 'replace' parameter")
}

func TestNewPostProcessor_UnknownType(t *testing.T) {
	config := PostProcessorConfig{
		Type: "unknown_type",
		Params: map[string]string{
			"pattern": "test",
			"replace": "replacement",
		},
	}

	_, err := NewPostProcessor(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown post-processor type")
}

func TestTemplateEngine_WithPostProcessors(t *testing.T) {
	templates := map[string]string{
		"haproxy.cfg": `global
    daemon
    maxconn 2000

defaults
    mode http
        timeout connect 5s
    option httplog`,
	}

	postProcessorConfigs := map[string][]PostProcessorConfig{
		"haproxy.cfg": {
			{
				Type: PostProcessorTypeRegexReplace,
				Params: map[string]string{
					"pattern": "^[ ]+",
					"replace": "  ",
				},
			},
		},
	}

	engine, err := New(EngineTypeScriggo, templates, nil, nil, postProcessorConfigs)
	require.NoError(t, err)

	output, err := engine.Render(context.Background(), "haproxy.cfg", nil)
	require.NoError(t, err)

	// Scriggo adds trailing newline, so include it in expected output
	expected := `global
  daemon
  maxconn 2000

defaults
  mode http
  timeout connect 5s
  option httplog
`

	assert.Equal(t, expected, output)
}

func TestTemplateEngine_MultiplePostProcessors(t *testing.T) {
	templates := map[string]string{
		"test": "  line1\n    line2\n      line3",
	}

	postProcessorConfigs := map[string][]PostProcessorConfig{
		"test": {
			// First normalize all indentation to 2 spaces
			{
				Type: PostProcessorTypeRegexReplace,
				Params: map[string]string{
					"pattern": "^[ ]+",
					"replace": "  ",
				},
			},
			// Then replace "line" with "row"
			{
				Type: PostProcessorTypeRegexReplace,
				Params: map[string]string{
					"pattern": "line",
					"replace": "row",
				},
			},
		},
	}

	engine, err := New(EngineTypeScriggo, templates, nil, nil, postProcessorConfigs)
	require.NoError(t, err)

	output, err := engine.Render(context.Background(), "test", nil)
	require.NoError(t, err)

	// Scriggo adds trailing newline
	expected := "  row1\n  row2\n  row3\n"
	assert.Equal(t, expected, output)
}

func TestTemplateEngine_PostProcessorError(t *testing.T) {
	templates := map[string]string{
		"test": "content",
	}

	// Create processor config with invalid regex that will fail at creation time
	postProcessorConfigs := map[string][]PostProcessorConfig{
		"test": {
			{
				Type: PostProcessorTypeRegexReplace,
				Params: map[string]string{
					"pattern": "[invalid(",
					"replace": "replacement",
				},
			},
		},
	}

	// Engine creation should fail due to invalid regex
	_, err := New(EngineTypeScriggo, templates, nil, nil, postProcessorConfigs)
	require.Error(t, err)
	// Error message from regex package
	assert.Contains(t, err.Error(), "invalid regex pattern")
}

func TestTemplateEngine_NoPostProcessors(t *testing.T) {
	templates := map[string]string{
		"test": "  content with spaces",
	}

	engine, err := New(EngineTypeScriggo, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render(context.Background(), "test", nil)
	require.NoError(t, err)

	// Without post-processors, spaces should be preserved (Scriggo adds trailing newline)
	assert.Equal(t, "  content with spaces\n", output)
}
