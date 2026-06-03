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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplatePostProcessor_BasicPassthrough(t *testing.T) {
	globals := buildScriggoGlobals(nil, nil, nil)
	processor, err := NewTemplatePostProcessor("{{ input }}", globals)
	require.NoError(t, err)

	result, err := processor.Process("hello world")
	require.NoError(t, err)
	assert.Equal(t, "hello world", result)
}

func TestTemplatePostProcessor_SimpleReplace(t *testing.T) {
	globals := buildScriggoGlobals(nil, nil, nil)
	source := `{{ replace(input, "PLACEHOLDER", "resolved") }}`
	processor, err := NewTemplatePostProcessor(source, globals)
	require.NoError(t, err)

	result, err := processor.Process("value is PLACEHOLDER here")
	require.NoError(t, err)
	assert.Equal(t, "value is resolved here", result)
}

func TestTemplatePostProcessor_RegexCountAndReplace(t *testing.T) {
	globals := buildScriggoGlobals(nil, nil, nil)
	source := `{%%
  var serverCount = len(regexp("(?m)^\\s*server\\s").FindAll(input, -1))
  var proxyCount = len(regexp("(?m)^(?:frontend|backend|listen)\\s").FindAll(input, -1))
  var result = serverCount + proxyCount
  result = result + result / 5
  if result < 100 {
    result = 100
  }
%%}{{ replace(input, "__COUNT__", tostring(result)) }}`

	processor, err := NewTemplatePostProcessor(source, globals)
	require.NoError(t, err)

	input := `global
  daemon

frontend http
  guid fe:http
  bind :80

backend svc1
  guid be:svc1
  server s1 10.0.0.1:80
server s1b 10.0.0.3:80

backend svc2
  guid be:svc2
  server s2 10.0.0.2:80
server s2b 10.0.0.4:80
server s2c 10.0.0.5:80

max-objects __COUNT__
`

	result, err := processor.Process(input)
	require.NoError(t, err)

	// 1 frontend + 2 backends + 5 servers = 8 objects
	// 8 + 8/5 = 9, but min 100
	assert.Contains(t, result, "max-objects 100")
	assert.NotContains(t, result, "__COUNT__")
}

func TestTemplatePostProcessor_CompilationError(t *testing.T) {
	globals := buildScriggoGlobals(nil, nil, nil)
	_, err := NewTemplatePostProcessor("{% invalid syntax here %}", globals)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template post-processor compilation failed")
}

func TestTemplatePostProcessor_EmptySource(t *testing.T) {
	globals := buildScriggoGlobals(nil, nil, nil)
	_, err := NewTemplatePostProcessor("", globals)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-empty 'source' parameter")
}

func TestTemplatePostProcessor_LargeInput(t *testing.T) {
	globals := buildScriggoGlobals(nil, nil, nil)
	processor, err := NewTemplatePostProcessor("{{ input }}", globals)
	require.NoError(t, err)

	// Build a large input string (~100KB)
	var large strings.Builder
	for i := range 1000 {
		large.WriteString("  server SRV_" + string(rune('0'+i%10)) + " 10.0.0.1:8080\n")
	}

	result, err := processor.Process(large.String())
	require.NoError(t, err)
	assert.Equal(t, large.String(), result)
}

func TestTemplateEngine_WithTemplatePostProcessor(t *testing.T) {
	templates := map[string]string{
		"test": `global
    daemon
max-objects __PLACEHOLDER__
backend svc
    guid be:svc
    server s1 10.0.0.1:80`,
	}

	postProcessorConfigs := map[string][]PostProcessorConfig{
		"test": {
			{
				Type: PostProcessorTypeTemplate,
				Params: map[string]string{
					"source": `{{ replace(input, "__PLACEHOLDER__", "42") }}`,
				},
			},
		},
	}

	engine, err := New(templates, nil, nil, postProcessorConfigs)
	require.NoError(t, err)

	output, err := engine.Render(context.Background(), "test", nil)
	require.NoError(t, err)

	assert.Contains(t, output, "max-objects 42")
	assert.NotContains(t, output, "__PLACEHOLDER__")
}

func TestTemplateEngine_TemplateAndRegexPostProcessors(t *testing.T) {
	templates := map[string]string{
		"test": `global
    daemon
    max-objects __PLACEHOLDER__
        backend svc`,
	}

	postProcessorConfigs := map[string][]PostProcessorConfig{
		"test": {
			// First: template post-processor resolves placeholder
			{
				Type: PostProcessorTypeTemplate,
				Params: map[string]string{
					"source": `{{ replace(input, "__PLACEHOLDER__", "100") }}`,
				},
			},
			// Second: regex normalizes indentation
			{
				Type: PostProcessorTypeRegexReplace,
				Params: map[string]string{
					"pattern": "^[ ]+",
					"replace": "  ",
				},
			},
		},
	}

	engine, err := New(templates, nil, nil, postProcessorConfigs)
	require.NoError(t, err)

	output, err := engine.Render(context.Background(), "test", nil)
	require.NoError(t, err)

	expected := `global
  daemon
  max-objects 100
  backend svc
`
	assert.Equal(t, expected, output)
}

func TestTemplateEngine_TemplatePostProcessorCompilationError(t *testing.T) {
	templates := map[string]string{
		"test": "content",
	}

	postProcessorConfigs := map[string][]PostProcessorConfig{
		"test": {
			{
				Type: PostProcessorTypeTemplate,
				Params: map[string]string{
					"source": "{% bad syntax %}",
				},
			},
		},
	}

	_, err := New(templates, nil, nil, postProcessorConfigs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template post-processor compilation failed")
}

func TestTemplateEngine_TemplatePostProcessorMissingSource(t *testing.T) {
	templates := map[string]string{
		"test": "content",
	}

	postProcessorConfigs := map[string][]PostProcessorConfig{
		"test": {
			{
				Type:   PostProcessorTypeTemplate,
				Params: map[string]string{},
			},
		},
	}

	_, err := New(templates, nil, nil, postProcessorConfigs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-empty 'source' parameter")
}
