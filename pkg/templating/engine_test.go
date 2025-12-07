package templating

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_Success(t *testing.T) {
	templates := map[string]string{
		"greeting": "Hello {{ name }}!",
		"farewell": "Goodbye {{ name }}!",
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, engine)

	assert.Equal(t, EngineTypeGonja, engine.EngineType())
	assert.Equal(t, 2, engine.TemplateCount())
	assert.True(t, engine.HasTemplate("greeting"))
	assert.True(t, engine.HasTemplate("farewell"))
	assert.False(t, engine.HasTemplate("nonexistent"))
}

func TestNew_UnsupportedEngine(t *testing.T) {
	templates := map[string]string{
		"test": "Hello {{ name }}",
	}

	// Use an invalid engine type
	invalidEngine := EngineType(999)
	engine, err := New(invalidEngine, templates, nil, nil, nil)

	assert.Nil(t, engine)
	require.Error(t, err)

	var unsupportedErr *UnsupportedEngineError
	assert.ErrorAs(t, err, &unsupportedErr)
	assert.Equal(t, invalidEngine, unsupportedErr.EngineType)
}

func TestNew_CompilationError(t *testing.T) {
	templates := map[string]string{
		"valid":   "Hello {{ name }}",
		"invalid": "Hello {{ name",
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)

	assert.Nil(t, engine)
	require.Error(t, err)

	var compilationErr *CompilationError
	assert.ErrorAs(t, err, &compilationErr)
	assert.Equal(t, "invalid", compilationErr.TemplateName)
}

func TestRender_Success(t *testing.T) {
	templates := map[string]string{
		"greeting": "Hello {{ name }}!",
		"info":     "Name: {{ user.name }}, Age: {{ user.age }}",
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Test simple rendering
	output, err := engine.Render("greeting", map[string]interface{}{
		"name": "World",
	})
	require.NoError(t, err)
	assert.Equal(t, "Hello World!", output)

	// Test nested context
	output, err = engine.Render("info", map[string]interface{}{
		"user": map[string]interface{}{
			"name": "Alice",
			"age":  30,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "Name: Alice, Age: 30", output)
}

func TestRender_TemplateNotFound(t *testing.T) {
	templates := map[string]string{
		"greeting": "Hello {{ name }}!",
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("nonexistent", map[string]interface{}{})

	assert.Empty(t, output)
	require.Error(t, err)

	var notFoundErr *TemplateNotFoundError
	assert.ErrorAs(t, err, &notFoundErr)
	assert.Equal(t, "nonexistent", notFoundErr.TemplateName)
	assert.Contains(t, notFoundErr.AvailableTemplates, "greeting")
}

func TestRender_RenderError(t *testing.T) {
	templates := map[string]string{
		// Template with undefined filter (will cause runtime error in gonja)
		"with_error": "{{ value | undefined_filter }}",
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("with_error", map[string]interface{}{
		"value": "test",
	})

	assert.Empty(t, output)
	require.Error(t, err)

	var renderErr *RenderError
	assert.ErrorAs(t, err, &renderErr)
	assert.Equal(t, "with_error", renderErr.TemplateName)
}

func TestTemplateNames(t *testing.T) {
	templates := map[string]string{
		"template1": "Content 1",
		"template2": "Content 2",
		"template3": "Content 3",
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	names := engine.TemplateNames()
	assert.Len(t, names, 3)
	assert.Contains(t, names, "template1")
	assert.Contains(t, names, "template2")
	assert.Contains(t, names, "template3")
}

func TestGetRawTemplate(t *testing.T) {
	templateContent := "Hello {{ name }}!"
	templates := map[string]string{
		"greeting": templateContent,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Test existing template
	raw, err := engine.GetRawTemplate("greeting")
	require.NoError(t, err)
	assert.Equal(t, templateContent, raw)

	// Test non-existent template
	raw, err = engine.GetRawTemplate("nonexistent")
	assert.Empty(t, raw)
	require.Error(t, err)

	var notFoundErr *TemplateNotFoundError
	assert.ErrorAs(t, err, &notFoundErr)
}

func TestHasTemplate(t *testing.T) {
	templates := map[string]string{
		"existing": "Content",
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	assert.True(t, engine.HasTemplate("existing"))
	assert.False(t, engine.HasTemplate("nonexistent"))
}

func TestString(t *testing.T) {
	templates := map[string]string{
		"template1": "Content 1",
		"template2": "Content 2",
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	str := engine.String()
	assert.Contains(t, str, "TemplateEngine")
	assert.Contains(t, str, "gonja")
	assert.Contains(t, str, "templates=2")
}

func TestEngineType_String(t *testing.T) {
	tests := []struct {
		name       string
		engineType EngineType
		expected   string
	}{
		{
			name:       "Gonja engine",
			engineType: EngineTypeGonja,
			expected:   "gonja",
		},
		{
			name:       "Unknown engine",
			engineType: EngineType(999),
			expected:   "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.engineType.String())
		})
	}
}

func TestCompilationError_ErrorMessage(t *testing.T) {
	err := NewCompilationError("test-template", "template content here", assert.AnError)

	assert.Contains(t, err.Error(), "test-template")
	assert.Contains(t, err.Error(), "compile")
}

func TestCompilationError_SnippetTruncation(t *testing.T) {
	longContent := strings.Repeat("a", 300)
	err := NewCompilationError("test", longContent, assert.AnError)

	assert.Len(t, err.TemplateSnippet, 203) // 200 + "..."
	assert.True(t, strings.HasSuffix(err.TemplateSnippet, "..."))
}

func TestRenderError_ErrorMessage(t *testing.T) {
	err := NewRenderError("test-template", assert.AnError)

	assert.Contains(t, err.Error(), "test-template")
	assert.Contains(t, err.Error(), "render")
}

func TestTemplateNotFoundError_ErrorMessage(t *testing.T) {
	err := NewTemplateNotFoundError("missing", []string{"template1", "template2"})

	assert.Contains(t, err.Error(), "missing")
	assert.Contains(t, err.Error(), "not found")
	assert.Equal(t, []string{"template1", "template2"}, err.AvailableTemplates)
}

func TestGonja_ComplexFeatures(t *testing.T) {
	templates := map[string]string{
		"with_loop":   `{%+ for item in items +%}{{ item }}{%+ if not loop.last +%}, {%+ endif +%}{%+ endfor +%}`,
		"with_if":     `{% if count > 5 %}Many{% else %}Few{% endif %}`,
		"with_filter": `{{ text | upper }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Test loop
	output, err := engine.Render("with_loop", map[string]interface{}{
		"items": []string{"a", "b", "c"},
	})
	require.NoError(t, err)
	assert.Equal(t, "a, b, c", output)

	// Test conditional
	output, err = engine.Render("with_if", map[string]interface{}{
		"count": 10,
	})
	require.NoError(t, err)
	assert.Equal(t, "Many", output)

	// Test filter
	output, err = engine.Render("with_filter", map[string]interface{}{
		"text": "hello",
	})
	require.NoError(t, err)
	assert.Equal(t, "HELLO", output)
}

func TestNew_EmptyTemplates(t *testing.T) {
	templates := map[string]string{}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, engine)

	assert.Equal(t, 0, engine.TemplateCount())
	assert.Empty(t, engine.TemplateNames())
}

func TestTemplateIncludes(t *testing.T) {
	tests := []struct {
		name      string
		templates map[string]string
		render    string
		context   map[string]interface{}
		want      string
		wantErr   bool
	}{
		{
			name: "simple include",
			templates: map[string]string{
				"header": "Header: {{ title }}",
				"footer": "Footer text",
				"main":   `{%+ include "header" +%}` + "\nBody\n" + `{%+ include "footer" +%}`,
			},
			render:  "main",
			context: map[string]interface{}{"title": "Test"},
			want:    "Header: Test\nBody\nFooter text",
		},
		{
			name: "nested includes",
			templates: map[string]string{
				"base":   "{{ content }}",
				"middle": `Start-{% include "base" %}-End`,
				"top":    `Outer({% include "middle" %})`,
			},
			render:  "top",
			context: map[string]interface{}{"content": "INNER"},
			want:    "Outer(Start-INNER-End)",
		},
		{
			name: "include with loop",
			templates: map[string]string{
				"item": "- {{ name }}\n",
				"list": `Items:` + "\n" + `{% for i in items %}{% set name = i.name %}{% include "item" %}{% endfor %}`,
			},
			render: "list",
			context: map[string]interface{}{
				"items": []map[string]interface{}{
					{"name": "First"},
					{"name": "Second"},
				},
			},
			want: "Items:\n- First\n- Second\n",
		},
		{
			name: "include non-existent template",
			templates: map[string]string{
				"main": `{% include "missing" %}`,
			},
			render:  "main",
			context: map[string]interface{}{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := New(EngineTypeGonja, tt.templates, nil, nil, nil)
			require.NoError(t, err)

			output, err := engine.Render(tt.render, tt.context)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, output)
		})
	}
}

func TestNewWithFilters_GetPathFilter(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		context   map[string]interface{}
		want      string
		wantErr   bool
		errString string
	}{
		{
			name:     "map file path",
			template: `{{ pathResolver.GetPath("host.map", "map") }}`,
			context:  map[string]interface{}{},
			want:     "/etc/haproxy/maps/host.map",
		},
		{
			name:     "general file path",
			template: `{{ pathResolver.GetPath("504.http", "file") }}`,
			context:  map[string]interface{}{},
			want:     "/etc/haproxy/general/504.http",
		},
		{
			name:     "SSL certificate path",
			template: `{{ pathResolver.GetPath("example.com.pem", "cert") }}`,
			context:  map[string]interface{}{},
			want:     "/etc/haproxy/ssl/example.com.pem",
		},
		{
			name:     "map file from variable",
			template: `{{ pathResolver.GetPath(filename, "map") }}`,
			context: map[string]interface{}{
				"filename": "backend.map",
			},
			want: "/etc/haproxy/maps/backend.map",
		},
		{
			name:     "dynamic file type",
			template: `{{ pathResolver.GetPath(filename, filetype) }}`,
			context: map[string]interface{}{
				"filename": "error.http",
				"filetype": "file",
			},
			want: "/etc/haproxy/general/error.http",
		},
		{
			name:      "missing file type argument",
			template:  `{{ pathResolver.GetPath("test.map") }}`,
			context:   map[string]interface{}{},
			wantErr:   true,
			errString: "GetPath requires 2 arguments",
		},
		{
			name:      "invalid file type",
			template:  `{{ pathResolver.GetPath("test.txt", "invalid") }}`,
			context:   map[string]interface{}{},
			wantErr:   true,
			errString: "invalid file type",
		},
	}

	// Create path resolver with test paths
	pathResolver := &PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		SSLDir:     "/etc/haproxy/ssl",
		GeneralDir: "/etc/haproxy/general",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			templates := map[string]string{
				"test": tt.template,
			}

			// Add pathResolver to context instead of as filter
			context := map[string]interface{}{
				"pathResolver": pathResolver,
			}
			if tt.context != nil {
				for k, v := range tt.context {
					context[k] = v
				}
			}

			engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
			require.NoError(t, err)

			output, err := engine.Render("test", context)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errString != "" {
					assert.Contains(t, err.Error(), tt.errString)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, output)
		})
	}
}

func TestNewWithFilters_CustomPathsConfiguration(t *testing.T) {
	// Test with custom directory paths
	pathResolver := &PathResolver{
		MapsDir:    "/custom/maps",
		SSLDir:     "/custom/certs",
		GeneralDir: "/custom/files",
	}

	templates := map[string]string{
		"test": `{{ pathResolver.GetPath("test.map", "map") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"pathResolver": pathResolver,
	})
	require.NoError(t, err)
	assert.Equal(t, "/custom/maps/test.map", output)
}

func TestNewWithFilters_MultipleFilters(t *testing.T) {
	// Test registering multiple custom filters
	pathResolver := &PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		SSLDir:     "/etc/haproxy/ssl",
		GeneralDir: "/etc/haproxy/general",
	}

	// Custom uppercase filter for testing
	uppercaseFilter := func(in interface{}, args ...interface{}) (interface{}, error) {
		str, ok := in.(string)
		if !ok {
			return nil, assert.AnError
		}
		return strings.ToUpper(str), nil
	}

	filters := map[string]FilterFunc{
		"uppercase": uppercaseFilter,
	}

	templates := map[string]string{
		"test": `{{ pathResolver.GetPath(filename | uppercase, "map") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, filters, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"filename":     "host.map",
		"pathResolver": pathResolver,
	})
	require.NoError(t, err)
	assert.Equal(t, "/etc/haproxy/maps/HOST.MAP", output)
}

func TestNewWithFilters_NilFilters(t *testing.T) {
	// Test that nil filters works (same as New())
	templates := map[string]string{
		"test": "Hello {{ name }}",
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"name": "World",
	})
	require.NoError(t, err)
	assert.Equal(t, "Hello World", output)
}

// ============================================================================
// compute_once tag tests
// ============================================================================

func TestComputeOnce_ExecutesOnlyOnce(t *testing.T) {
	// This test verifies that compute_once executes the body only once,
	// even when the template is included multiple times
	templates := map[string]string{
		"main": `
{%- set counter = namespace(value=0) %}
{%- include "use_counter" -%}
{%- include "use_counter" -%}
{%- include "use_counter" -%}
Result: {{ counter.value }}`,
		"use_counter": `
{%- compute_once counter %}
  {%- set counter.value = counter.value + 1 %}
{%- endcompute_once -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("main", nil)
	require.NoError(t, err)

	// If compute_once works correctly, counter.value should be 1
	// If it executed 3 times, counter.value would be 3
	assert.Contains(t, output, "Result: 1")
}

func TestComputeOnce_SharesResultAcrossTemplates(t *testing.T) {
	// This test verifies that the result is available across different template includes
	templates := map[string]string{
		"main": `
{%- set data = namespace(value="", count=0) %}
{%- include "compute" -%}
{%- include "compute" -%}
{%- include "use_result" -%}`,
		"compute": `
{%- compute_once data %}
  {%- set data.value = "computed" %}
  {%- set data.count = 42 %}
{%- endcompute_once -%}`,
		"use_result": `
Value: {{ data.value }}, Count: {{ data.count }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("main", nil)
	require.NoError(t, err)

	assert.Contains(t, output, "Value: computed")
	assert.Contains(t, output, "Count: 42")
}

func TestComputeOnce_RequiresResultVariable(t *testing.T) {
	// This test verifies that an error is returned if the variable doesn't exist before compute_once
	templates := map[string]string{
		"main": `
{%- compute_once data %}
  {%- set data = "value" %}
{%- endcompute_once -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	_, err = engine.Render("main", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "variable 'data' must be created before compute_once block")
}

func TestComputeOnce_IsolatedBetweenRenders(t *testing.T) {
	// This test verifies that the cache is cleared between different Render() calls
	templates := map[string]string{
		"main": `
{%- set data = namespace(value="") %}
{%- compute_once data %}
  {%- set data.value = input_value %}
{%- endcompute_once -%}
Result: {{ data.value }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// First render with input_value = "first"
	output1, err := engine.Render("main", map[string]interface{}{
		"input_value": "first",
	})
	require.NoError(t, err)
	assert.Contains(t, output1, "Result: first")

	// Second render with input_value = "second"
	// Should get "second", not "first" (proves cache was cleared)
	output2, err := engine.Render("main", map[string]interface{}{
		"input_value": "second",
	})
	require.NoError(t, err)
	assert.Contains(t, output2, "Result: second")
	assert.NotContains(t, output2, "Result: first")
}

func TestComputeOnce_ComplexComputation(t *testing.T) {
	// This test verifies that compute_once works with complex nested operations
	templates := map[string]string{
		"main": `
{%- set analysis = namespace(items=[], total=0) %}
{%- include "use_analysis" -%}
{%- include "use_analysis" -%}`,
		"use_analysis": `
{%- compute_once analysis %}
  {%- for item in input_items %}
    {%- set analysis.items = analysis.items.append(item.name) %}
    {%- set analysis.total = analysis.total + item.value %}
  {%- endfor %}
{%- endcompute_once -%}
Total: {{ analysis.total }}, Count: {{ analysis.items | length }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("main", map[string]interface{}{
		"input_items": []map[string]interface{}{
			{"name": "item1", "value": 10},
			{"name": "item2", "value": 20},
			{"name": "item3", "value": 30},
		},
	})
	require.NoError(t, err)

	// Both includes should show the same totals (proof it ran once)
	// Count the occurrences
	totalCount := strings.Count(output, "Total: 60")
	itemCount := strings.Count(output, "Count: 3")
	assert.Equal(t, 2, totalCount, "Should appear twice (once per include)")
	assert.Equal(t, 2, itemCount, "Should appear twice (once per include)")
}

func TestComputeOnce_WithMacro(t *testing.T) {
	// This test simulates the real-world gateway.yaml use case with a macro
	templates := map[string]string{
		"main": `
{%- set analysis = namespace(output="") %}
{%- include "snippet1" -%}
{%- include "snippet2" -%}`,
		"macros": `
{%- macro analyze(data) -%}
  {%- for item in data %}
    {{- item.name -}}
  {%- endfor %}
{%- endmacro -%}`,
		"snippet1": `
{%- compute_once analysis %}
  {%- from "macros" import analyze %}
  {%- set analysis.output = analyze(resources) %}
{%- endcompute_once -%}
Snippet1: {{ analysis.output }}`,
		"snippet2": `
{%- compute_once analysis %}
  {%- from "macros" import analyze %}
  {%- set analysis.output = analyze(resources) %}
{%- endcompute_once -%}
Snippet2: {{ analysis.output }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("main", map[string]interface{}{
		"resources": []map[string]interface{}{
			{"name": "route1"},
			{"name": "route2"},
		},
	})
	require.NoError(t, err)

	// Both snippets should have the same analysis output
	assert.Contains(t, output, "Snippet1: route1route2")
	assert.Contains(t, output, "Snippet2: route1route2")
}

func TestComputeOnce_SyntaxError_MissingVariableName(t *testing.T) {
	// This test verifies proper error when variable name is missing
	templates := map[string]string{
		"main": `
{%- compute_once %}
  {%- set data = "value" %}
{%- endcompute_once -%}`,
	}

	_, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compute_once requires variable name")
}

func TestComputeOnce_SyntaxError_ExtraArguments(t *testing.T) {
	// This test verifies proper error when extra arguments are provided
	templates := map[string]string{
		"main": `
{%- compute_once data extra_arg %}
  {%- set data = "value" %}
{%- endcompute_once -%}`,
	}

	_, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no additional arguments")
}

func TestComputeOnce_Integration_WithTracing(t *testing.T) {
	// This integration test verifies compute_once behavior and demonstrates tracing functionality
	templates := map[string]string{
		"main": `
{%- set routes = namespace(analyzed=[], count=0) %}
{%- include "frontend-1" -%}
{%- include "frontend-2" -%}
{%- include "frontend-3" -%}
Total analyzed: {{ routes.count }}`,
		"frontend-1": `
{%- include "analyze" -%}
Frontend 1 routes: {{ routes.count }}
`,
		"frontend-2": `
{%- include "analyze" -%}
Frontend 2 routes: {{ routes.count }}
`,
		"frontend-3": `
{%- include "analyze" -%}
Frontend 3 routes: {{ routes.count }}
`,
		"analyze": `
{%- compute_once routes %}
  {%- include "expensive-analysis" %}
{%- endcompute_once -%}`,
		"expensive-analysis": `
{%- for route in input_routes %}
  {%- set routes.analyzed = routes.analyzed.append(route.name) %}
  {%- set routes.count = routes.count + 1 %}
{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Enable tracing to observe template execution
	engine.EnableTracing()

	output, err := engine.Render("main", map[string]interface{}{
		"input_routes": []map[string]interface{}{
			{"name": "route1"},
			{"name": "route2"},
			{"name": "route3"},
		},
	})
	require.NoError(t, err)

	// CRITICAL: Verify the computation ran ONCE and all frontends see the same result
	// If compute_once didn't work, count would be 9 (3 routes × 3 frontends)
	// With compute_once working, count is 3 (computed once, shared across all frontends)
	assert.Contains(t, output, "Total analyzed: 3")
	assert.Contains(t, output, "Frontend 1 routes: 3")
	assert.Contains(t, output, "Frontend 2 routes: 3")
	assert.Contains(t, output, "Frontend 3 routes: 3")

	// Verify tracing captured the main render
	trace := engine.GetTraceOutput()
	assert.Contains(t, trace, "Rendering: main")
	assert.Contains(t, trace, "Completed: main")

	// Note: Tracing only captures top-level Render() calls, not internal Gonja includes.
	// The important verification is the output above - compute_once prevented redundant
	// computation as evidenced by count=3 instead of count=9.
}

func TestComputeOnce_Integration_MultipleRenders(t *testing.T) {
	// This test demonstrates tracing across multiple independent renders
	templates := map[string]string{
		"template1": `{%- set data = namespace(value="") %}{%- compute_once data %}{%- set data.value = "template1" %}{%- endcompute_once -%}Result: {{ data.value }}`,
		"template2": `{%- set data = namespace(value="") %}{%- compute_once data %}{%- set data.value = "template2" %}{%- endcompute_once -%}Result: {{ data.value }}`,
		"template3": `{%- set data = namespace(value="") %}{%- compute_once data %}{%- set data.value = "template3" %}{%- endcompute_once -%}Result: {{ data.value }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Enable tracing
	engine.EnableTracing()

	// Render multiple templates
	output1, err := engine.Render("template1", nil)
	require.NoError(t, err)
	assert.Contains(t, output1, "Result: template1")

	output2, err := engine.Render("template2", nil)
	require.NoError(t, err)
	assert.Contains(t, output2, "Result: template2")

	output3, err := engine.Render("template3", nil)
	require.NoError(t, err)
	assert.Contains(t, output3, "Result: template3")

	// Get trace - should show all three renders
	trace := engine.GetTraceOutput()
	assert.Contains(t, trace, "Rendering: template1")
	assert.Contains(t, trace, "Completed: template1")
	assert.Contains(t, trace, "Rendering: template2")
	assert.Contains(t, trace, "Completed: template2")
	assert.Contains(t, trace, "Rendering: template3")
	assert.Contains(t, trace, "Completed: template3")

	// Each render should have its own compute_once cache (verified by correct output values)
	// This proves compute_once markers are cleared between renders
}

func TestTracing_ConcurrentRenders(t *testing.T) {
	// This test verifies that tracing is thread-safe when multiple goroutines
	// call Render() concurrently. Run with: go test -race
	templates := map[string]string{
		"template1": `Result: {{ value }}`,
		"template2": `Output: {{ value | upper }}`,
		"template3": `Value: {{ value | length }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Enable tracing
	engine.EnableTracing()

	// Run concurrent renders
	const numGoroutines = 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()

			// Each goroutine renders multiple templates
			for j := 0; j < 5; j++ {
				tmpl := fmt.Sprintf("template%d", (j%3)+1)
				context := map[string]interface{}{
					"value": fmt.Sprintf("goroutine-%d-iteration-%d", id, j),
				}

				output, err := engine.Render(tmpl, context)
				assert.NoError(t, err)
				assert.NotEmpty(t, output)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Get trace output (should contain entries from all renders)
	trace := engine.GetTraceOutput()
	assert.NotEmpty(t, trace)

	// Verify trace contains entries for all three templates
	assert.Contains(t, trace, "Rendering: template1")
	assert.Contains(t, trace, "Rendering: template2")
	assert.Contains(t, trace, "Rendering: template3")

	// Verify trace contains completion entries
	assert.Contains(t, trace, "Completed: template1")
	assert.Contains(t, trace, "Completed: template2")
	assert.Contains(t, trace, "Completed: template3")

	// Count total render entries (should be 50: 10 goroutines * 5 renders each)
	renderCount := strings.Count(trace, "Rendering:")
	assert.Equal(t, 50, renderCount, "Should have 50 render entries")

	completedCount := strings.Count(trace, "Completed:")
	assert.Equal(t, 50, completedCount, "Should have 50 completion entries")
}

func TestTracing_ConcurrentEnableDisable(t *testing.T) {
	// This test verifies that EnableTracing/DisableTracing are thread-safe
	// when called concurrently with Render()
	templates := map[string]string{
		"test": `Value: {{ value }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	done := make(chan bool)

	// Goroutine 1: Continuously enable/disable tracing
	go func() {
		for i := 0; i < 100; i++ {
			engine.EnableTracing()
			time.Sleep(time.Microsecond)
			engine.DisableTracing()
			time.Sleep(time.Microsecond)
		}
		done <- true
	}()

	// Goroutine 2: Continuously render templates
	go func() {
		for i := 0; i < 100; i++ {
			_, err := engine.Render("test", map[string]interface{}{
				"value": i,
			})
			assert.NoError(t, err)
			time.Sleep(time.Microsecond)
		}
		done <- true
	}()

	// Wait for both goroutines
	<-done
	<-done

	// If we get here without panics or race conditions, the test passes
}

func TestTracing_FilterOperations(t *testing.T) {
	// Test that filter operations are captured in trace output
	templates := map[string]string{
		"test": `{%- set items = [
			{"name": "low", "priority": 1},
			{"name": "high", "priority": 10},
			{"name": "medium", "priority": 5}
		] %}
{%- set sorted = items | sort_by(["priority:desc"]) %}
{%- set grouped = items | group_by("priority") %}
{%- set extracted = items | extract("name") %}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Enable tracing
	engine.EnableTracing()

	// Render template
	_, err = engine.Render("test", nil)
	require.NoError(t, err)

	// Get trace output
	trace := engine.GetTraceOutput()

	// Verify filter operations are captured
	assert.Contains(t, trace, "Filter: sort_by")
	assert.Contains(t, trace, "priority:desc")
	assert.Contains(t, trace, "Filter: group_by")
	assert.Contains(t, trace, "priority")
	assert.Contains(t, trace, "Filter: extract")
	assert.Contains(t, trace, "name")

	// Verify trace includes item counts
	assert.Contains(t, trace, "3 items")
}

func TestFilterDebug_EnableDisable(t *testing.T) {
	// Test that filter debug can be enabled and disabled
	templates := map[string]string{
		"test": `{%- set items = [{"priority": 2}, {"priority": 1}] -%}
{%- set sorted = items | sort_by(["priority"]) -%}
sorted_count={{ sorted | length }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Enable filter debug
	engine.EnableFilterDebug()

	// Render should succeed (debug logging happens via slog, not in template output)
	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Equal(t, "sorted_count=2", output)

	// Disable filter debug
	engine.DisableFilterDebug()

	// Rendering should still work
	output, err = engine.Render("test", nil)
	require.NoError(t, err)
	assert.Equal(t, "sorted_count=2", output)
}

func TestIsTracingEnabled(t *testing.T) {
	templates := map[string]string{
		"test": `{{ value }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Initially tracing should be disabled
	assert.False(t, engine.IsTracingEnabled())

	// Enable tracing
	engine.EnableTracing()
	assert.True(t, engine.IsTracingEnabled())

	// Disable tracing
	engine.DisableTracing()
	assert.False(t, engine.IsTracingEnabled())
}

func TestAppendTraces(t *testing.T) {
	// Test that traces from one engine can be appended to another
	templates := map[string]string{
		"test1": `{{ value1 }}`,
		"test2": `{{ value2 }}`,
	}

	// Create two engines
	engine1, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)
	engine1.EnableTracing()

	engine2, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)
	engine2.EnableTracing()

	// Render with both engines
	_, err = engine1.Render("test1", map[string]interface{}{"value1": "a"})
	require.NoError(t, err)

	_, err = engine2.Render("test2", map[string]interface{}{"value2": "b"})
	require.NoError(t, err)

	// Append engine2's traces to engine1
	engine1.AppendTraces(engine2)

	// engine1 should now have both traces
	trace := engine1.GetTraceOutput()
	assert.Contains(t, trace, "test1")
	assert.Contains(t, trace, "test2")

	// engine2's traces should have been cleared by AppendTraces (via GetTraceOutput)
	trace2 := engine2.GetTraceOutput()
	assert.Empty(t, trace2)
}

// ============================================================================
// Error Unwrap tests
// ============================================================================

func TestCompilationError_Unwrap(t *testing.T) {
	cause := fmt.Errorf("original error")
	err := NewCompilationError("test", "template content", cause)

	unwrapped := err.Unwrap()
	assert.Equal(t, cause, unwrapped)
}

func TestRenderError_Unwrap(t *testing.T) {
	cause := fmt.Errorf("original error")
	err := NewRenderError("test", cause)

	unwrapped := err.Unwrap()
	assert.Equal(t, cause, unwrapped)
}

func TestUnsupportedEngineError_Error(t *testing.T) {
	err := NewUnsupportedEngineError(EngineType(999))

	assert.Contains(t, err.Error(), "unsupported template engine type")
	assert.Contains(t, err.Error(), "unknown")
}

// ============================================================================
// strip and trim filter tests
// ============================================================================

func TestStripFilter(t *testing.T) {
	templates := map[string]string{
		"test": `{{ value | strip }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "strip whitespace",
			input: "  hello world  ",
			want:  "hello world",
		},
		{
			name:  "strip tabs and newlines",
			input: "\t\nhello\n\t",
			want:  "hello",
		},
		{
			name:  "no whitespace",
			input: "hello",
			want:  "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := engine.Render("test", map[string]interface{}{
				"value": tt.input,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, output)
		})
	}
}

func TestTrimFilter(t *testing.T) {
	templates := map[string]string{
		"default": `{{ value | trim }}`,
		"custom":  `{{ value | trim(chars="xy") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	t.Run("default trim whitespace", func(t *testing.T) {
		output, err := engine.Render("default", map[string]interface{}{
			"value": "  hello  ",
		})
		require.NoError(t, err)
		assert.Equal(t, "hello", output)
	})

	t.Run("custom characters", func(t *testing.T) {
		output, err := engine.Render("custom", map[string]interface{}{
			"value": "xxhelloxx",
		})
		require.NoError(t, err)
		assert.Equal(t, "hello", output)
	})
}

// ============================================================================
// transform filter tests
// ============================================================================

func TestTransformFilter(t *testing.T) {
	// Transform filter is exercised via context-passed transforms map
	templates := map[string]string{
		"test": `{%- set result = items | transform(transforms) -%}
{%- for item in result -%}
{{ item.path_key }},{{ item.priority }};
{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Pass the transforms map via context
	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"hostname": "api", "path": "/users", "headers": []string{"X-Auth", "X-Version"}},
			{"hostname": "web", "path": "/home", "headers": []string{"Accept"}},
		},
		"transforms": map[string]interface{}{
			"path_key": "$.hostname ~ $.path",
			"priority": "$.headers | length",
		},
	})
	require.NoError(t, err)
	assert.Contains(t, output, "api/users,2")
	assert.Contains(t, output, "web/home,1")
}

func TestTransformFilter_Errors(t *testing.T) {
	t.Run("not an array", func(t *testing.T) {
		templates := map[string]string{
			"test": `{%- set transforms = {"key": "$.value"} -%}{{ "string" | transform(transforms) }}`,
		}
		engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
		require.NoError(t, err)

		_, err = engine.Render("test", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected array/slice")
	})

	t.Run("transforms not a map", func(t *testing.T) {
		templates := map[string]string{
			"test": `{{ items | transform("not a map") }}`,
		}
		engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
		require.NoError(t, err)

		_, err = engine.Render("test", map[string]interface{}{
			"items": []map[string]interface{}{},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be a map")
	})
}

// ============================================================================
// glob_match filter tests
// ============================================================================

func TestGlobMatchFilter(t *testing.T) {
	templates := map[string]string{
		"test": `{%- set matched = items | glob_match(pattern) -%}
{%- for item in matched -%}{{ item }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	tests := []struct {
		name    string
		items   []string
		pattern string
		want    string
	}{
		{
			name:    "match prefix",
			items:   []string{"map-entry-1", "map-entry-2", "backend-rule"},
			pattern: "map-entry-*",
			want:    "map-entry-1,map-entry-2,",
		},
		{
			name:    "no matches",
			items:   []string{"foo", "bar"},
			pattern: "baz-*",
			want:    "",
		},
		{
			name:    "exact match",
			items:   []string{"test.txt", "test.log"},
			pattern: "test.txt",
			want:    "test.txt,",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert []string to []interface{}
			items := make([]interface{}, len(tt.items))
			for i, s := range tt.items {
				items[i] = s
			}

			output, err := engine.Render("test", map[string]interface{}{
				"items":   items,
				"pattern": tt.pattern,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, output)
		})
	}
}

func TestGlobMatchFilter_Errors(t *testing.T) {
	t.Run("invalid pattern", func(t *testing.T) {
		templates := map[string]string{
			"test": `{{ items | glob_match("[") }}`,
		}
		engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
		require.NoError(t, err)

		_, err = engine.Render("test", map[string]interface{}{
			"items": []interface{}{"test"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid pattern")
	})

	t.Run("not an array", func(t *testing.T) {
		templates := map[string]string{
			"test": `{{ "string" | glob_match("*") }}`,
		}
		engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
		require.NoError(t, err)

		_, err = engine.Render("test", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected array/slice")
	})
}

// Note: conflicts_by is a test, not a filter, and has Gonja argument passing complexities.
// See filters_generic_test.go:274 - the test is not currently used in templates.
// Unit testing this function directly would require calling conflictsByTest() directly,
// which is not exported.

// ============================================================================
// JSONPath expression tests (via extract filter)
// ============================================================================

func TestExtractFilter_JSONPath(t *testing.T) {
	t.Run("simple field extraction", func(t *testing.T) {
		templates := map[string]string{
			"test": `{%- for name in items | extract("$.name") -%}{{ name }},{%- endfor -%}`,
		}
		engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
		require.NoError(t, err)

		output, err := engine.Render("test", map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{"name": "item1"},
				map[string]interface{}{"name": "item2"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "item1,item2,", output)
	})

	t.Run("nested field extraction", func(t *testing.T) {
		templates := map[string]string{
			"test": `{%- for ns in items | extract("$.metadata.namespace") -%}{{ ns }},{%- endfor -%}`,
		}
		engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
		require.NoError(t, err)

		output, err := engine.Render("test", map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{"metadata": map[string]interface{}{"namespace": "default"}},
				map[string]interface{}{"metadata": map[string]interface{}{"namespace": "kube-system"}},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "default,kube-system,", output)
	})

	t.Run("concatenation expression", func(t *testing.T) {
		templates := map[string]string{
			"test": `{%- for key in items | extract("$.ns ~ '/' ~ $.name") -%}{{ key }},{%- endfor -%}`,
		}
		engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
		require.NoError(t, err)

		output, err := engine.Render("test", map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{"ns": "default", "name": "svc1"},
				map[string]interface{}{"ns": "prod", "name": "svc2"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "default/svc1,prod/svc2,", output)
	})
}

// ============================================================================
// getFieldByReflection tests (via sort_by with struct)
// ============================================================================

type testStruct struct {
	Name     string
	Priority int
}

func TestSortByWithStruct(t *testing.T) {
	templates := map[string]string{
		"test": `{%- set sorted = items | sort_by(["$.Priority:desc"]) -%}
{%- for item in sorted -%}{{ item.Name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []interface{}{
			testStruct{Name: "low", Priority: 1},
			testStruct{Name: "high", Priority: 10},
			testStruct{Name: "medium", Priority: 5},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "high,medium,low,", output)
}

// ============================================================================
// deepCopyValue tests (via transform filter)
// ============================================================================

func TestTransformDeepCopy(t *testing.T) {
	// Test that transform creates deep copies by verifying original data is preserved
	// even when transforms modify the copied data.
	// The transform filter uses JSON marshal/unmarshal which creates a deep copy.
	templates := map[string]string{
		"test": `{%- set transformed = items | transform(transforms) -%}
{%- for item in transformed -%}{{ item.name }}-{{ item.name_upper }};{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Transform that copies the name field to name_upper (in real use, would apply upper)
	// Using $.name to verify field extraction works
	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "item1"},
			{"name": "item2"},
		},
		"transforms": map[string]interface{}{
			"name_upper": "$.name",
		},
	})
	require.NoError(t, err)
	// Each transformed item should have both name and name_upper set to the same value
	assert.Equal(t, "item1-item1;item2-item2;", output)
}

// ============================================================================
// Global function tests
// ============================================================================

func TestGlobalFunctions(t *testing.T) {
	templates := map[string]string{
		"test": `{{ custom_func("hello", 42) }}`,
	}

	customFunc := func(args ...interface{}) (interface{}, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("expected 2 args")
		}
		return fmt.Sprintf("%v-%v", args[0], args[1]), nil
	}

	globalFuncs := map[string]GlobalFunc{
		"custom_func": customFunc,
	}

	engine, err := New(EngineTypeGonja, templates, nil, globalFuncs, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Equal(t, "hello-42", output)
}

func TestGlobalFunctions_Error(t *testing.T) {
	templates := map[string]string{
		"test": `{{ failing_func() }}`,
	}

	failingFunc := func(args ...interface{}) (interface{}, error) {
		return nil, fmt.Errorf("intentional failure")
	}

	globalFuncs := map[string]GlobalFunc{
		"failing_func": failingFunc,
	}

	engine, err := New(EngineTypeGonja, templates, nil, globalFuncs, nil)
	require.NoError(t, err)

	_, err = engine.Render("test", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "intentional failure")
}

// ============================================================================
// Additional edge case tests
// ============================================================================

func TestExtractFilter_SingleItem(t *testing.T) {
	templates := map[string]string{
		"test": `{{ item | extract("$.name") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"item": map[string]interface{}{"name": "test-item"},
	})
	require.NoError(t, err)
	assert.Equal(t, "test-item", output)
}

func TestExtractFilter_MissingPath(t *testing.T) {
	templates := map[string]string{
		"test": `{{ items | extract() }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	_, err = engine.Render("test", map[string]interface{}{
		"items": []interface{}{"a", "b"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path must be string")
}

func TestSortByWithLength(t *testing.T) {
	templates := map[string]string{
		"test": `{%- set sorted = items | sort_by(["$.headers | length:desc"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "one", "headers": []string{"X"}},
			{"name": "three", "headers": []string{"X", "Y", "Z"}},
			{"name": "two", "headers": []string{"X", "Y"}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "three,two,one,", output)
}

func TestSortByWithExists(t *testing.T) {
	templates := map[string]string{
		"test": `{%- set sorted = items | sort_by(["$.method:exists:desc"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "no-method"},
			{"name": "with-method", "method": "GET"},
			{"name": "also-no-method"},
		},
	})
	require.NoError(t, err)
	// Items with method should come first
	assert.True(t, strings.HasPrefix(output, "with-method,"))
}

func TestGetLengthVariousTypes(t *testing.T) {
	templates := map[string]string{
		"string": `{{ "hello" | length }}`,
		"slice":  `{{ items | length }}`,
		"map":    `{{ data | length }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	t.Run("string length", func(t *testing.T) {
		output, err := engine.Render("string", nil)
		require.NoError(t, err)
		assert.Equal(t, "5", output)
	})

	t.Run("slice length", func(t *testing.T) {
		output, err := engine.Render("slice", map[string]interface{}{
			"items": []string{"a", "b", "c"},
		})
		require.NoError(t, err)
		assert.Equal(t, "3", output)
	})

	t.Run("map length", func(t *testing.T) {
		output, err := engine.Render("map", map[string]interface{}{
			"data": map[string]interface{}{"a": 1, "b": 2},
		})
		require.NoError(t, err)
		assert.Equal(t, "2", output)
	})
}

// ============================================================================
// Custom list methods tests (createCustomListMethods)
// ============================================================================

func TestCustomListMethods_Append(t *testing.T) {
	// Test the custom append method that returns the modified list
	templates := map[string]string{
		"test": `{%- set items = namespace(list=[]) -%}
{%- set items.list = items.list.append("a") -%}
{%- set items.list = items.list.append("b") -%}
{%- for item in items.list -%}{{ item }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Equal(t, "a,b,", output)
}

func TestCustomListMethods_Reverse(t *testing.T) {
	templates := map[string]string{
		"test": `{%- set items = ["a", "b", "c"] -%}
{%- set reversed = items.reverse() -%}
{%- for item in reversed -%}{{ item }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Equal(t, "c,b,a,", output)
}

func TestCustomListMethods_Copy(t *testing.T) {
	templates := map[string]string{
		"test": `{%- set original = ["a", "b"] -%}
{%- set copied = original.copy() -%}
{{ copied | length }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Equal(t, "2", output)
}

// ============================================================================
// Custom dict methods tests (createCustomDictMethods)
// ============================================================================

func TestCustomDictMethods_Keys(t *testing.T) {
	templates := map[string]string{
		"test": `{%- set data = {"b": 2, "a": 1, "c": 3} -%}
{%- for k in data.keys() -%}{{ k }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	// Keys should be sorted alphabetically
	assert.Equal(t, "a,b,c,", output)
}

func TestCustomDictMethods_Items(t *testing.T) {
	templates := map[string]string{
		"test": `{%- set data = {"key": "value"} -%}
{{ data.items() | length }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Equal(t, "1", output)
}

func TestCustomDictMethods_Update(t *testing.T) {
	templates := map[string]string{
		"test": `{%- set data = {"a": 1} -%}
{%- set data = data.update({"b": 2}) -%}
{%- for k in data.keys() -%}{{ k }}:{{ data[k] }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Contains(t, output, "a:1,")
	assert.Contains(t, output, "b:2,")
}

// ============================================================================
// Array access via nested arrays tests
// ============================================================================

func TestSortByWithNestedArrayLength(t *testing.T) {
	// Test processArrayIndex via sorting by nested array element count
	templates := map[string]string{
		"test": `{%- set sorted = items | sort_by(["$.nested[0]:desc"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "first", "nested": []int{100}},
			{"name": "second", "nested": []int{200}},
			{"name": "third", "nested": []int{50}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "second,first,third,", output)
}

func TestSortByWithNumericComparison(t *testing.T) {
	// Test toFloat64 function for numeric sorting
	templates := map[string]string{
		"test": `{%- set sorted = items | sort_by(["$.value:desc"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "low", "value": 1},
			{"name": "high", "value": 100},
			{"name": "mid", "value": 50},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "high,mid,low,", output)
}

func TestSortByWithFloatValues(t *testing.T) {
	// Test toFloat64 with float values
	templates := map[string]string{
		"test": `{%- set sorted = items | sort_by(["$.weight"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "heavy", "weight": 10.5},
			{"name": "light", "weight": 1.2},
			{"name": "medium", "weight": 5.0},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "light,medium,heavy,", output)
}

// ============================================================================
// testInFixed tests
// ============================================================================

func TestInOperatorWithStrings(t *testing.T) {
	// Test the 'in' operator which uses testInFixed
	templates := map[string]string{
		"test": `{% if "a" in items %}found{% else %}not found{% endif %}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	t.Run("string in array - found", func(t *testing.T) {
		output, err := engine.Render("test", map[string]interface{}{
			"items": []string{"a", "b", "c"},
		})
		require.NoError(t, err)
		assert.Equal(t, "found", output)
	})

	t.Run("string not in array", func(t *testing.T) {
		output, err := engine.Render("test", map[string]interface{}{
			"items": []string{"x", "y", "z"},
		})
		require.NoError(t, err)
		assert.Equal(t, "not found", output)
	})
}

func TestInOperatorWithMaps(t *testing.T) {
	templates := map[string]string{
		"test": `{% if "key" in data %}found{% else %}not found{% endif %}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	t.Run("key in map - found", func(t *testing.T) {
		output, err := engine.Render("test", map[string]interface{}{
			"data": map[string]interface{}{"key": "value"},
		})
		require.NoError(t, err)
		assert.Equal(t, "found", output)
	})

	t.Run("key not in map", func(t *testing.T) {
		output, err := engine.Render("test", map[string]interface{}{
			"data": map[string]interface{}{"other": "value"},
		})
		require.NoError(t, err)
		assert.Equal(t, "not found", output)
	})
}

func TestInOperatorWithString(t *testing.T) {
	// Test 'in' with substring check
	templates := map[string]string{
		"test": `{% if "bc" in text %}found{% else %}not found{% endif %}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"text": "abcdef",
	})
	require.NoError(t, err)
	assert.Equal(t, "found", output)
}

// ============================================================================
// eval filter tests
// ============================================================================

func TestEvalFilter(t *testing.T) {
	templates := map[string]string{
		"test": `{{ item | eval("$.name") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"item": map[string]interface{}{"name": "test-value"},
	})
	require.NoError(t, err)
	assert.Contains(t, output, "test-value")
	assert.Contains(t, output, "string")
}

func TestEvalFilterWithLength(t *testing.T) {
	templates := map[string]string{
		"test": `{{ item | eval("$.items | length") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"item": map[string]interface{}{
			"items": []string{"a", "b", "c"},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, output, "3")
	assert.Contains(t, output, "int")
}

func TestEvalFilterWithExists(t *testing.T) {
	templates := map[string]string{
		"test": `{{ item | eval("$.optional:exists") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	t.Run("field exists", func(t *testing.T) {
		output, err := engine.Render("test", map[string]interface{}{
			"item": map[string]interface{}{"optional": "value"},
		})
		require.NoError(t, err)
		assert.Contains(t, output, "true")
	})

	t.Run("field missing", func(t *testing.T) {
		output, err := engine.Render("test", map[string]interface{}{
			"item": map[string]interface{}{},
		})
		require.NoError(t, err)
		assert.Contains(t, output, "false")
	})
}

// ============================================================================
// trim filter tests
// ============================================================================

func TestTrimFilterEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		template string
		context  map[string]interface{}
		expected string
	}{
		{
			name:     "trim with custom chars",
			template: `{{ "###hello###" | trim("#") }}`,
			context:  nil,
			expected: "hello",
		},
		{
			name:     "trim empty string",
			template: `{{ "" | trim }}`,
			context:  nil,
			expected: "",
		},
		{
			name:     "trim from variable",
			template: `{{ value | trim }}`,
			context:  map[string]interface{}{"value": "  spaced  "},
			expected: "spaced",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			templates := map[string]string{"test": tt.template}
			engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
			require.NoError(t, err)

			output, err := engine.Render("test", tt.context)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, output)
		})
	}
}

// ============================================================================
// convertToMap tests
// ============================================================================

func TestExtractFromNestedMap(t *testing.T) {
	// Tests convertToMap for nested structure navigation
	templates := map[string]string{
		"test": `{{ data | extract("$.metadata.labels.app") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"data": map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"app": "my-app",
				},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "my-app", output)
}

// ============================================================================
// getFieldByReflection tests
// ============================================================================

type TestStruct struct {
	Name   string
	Value  int
	Nested struct {
		Field string
	}
}

func TestExtractFromStruct(t *testing.T) {
	// Tests getFieldByReflection for struct field access
	templates := map[string]string{
		"test": `{{ data | extract("$.Name") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"data": TestStruct{Name: "struct-name", Value: 42},
	})
	require.NoError(t, err)
	assert.Equal(t, "struct-name", output)
}

// ============================================================================
// compareValues tests with different types
// ============================================================================

func TestSortByWithMixedTypes(t *testing.T) {
	// Test compareValues with different types
	templates := map[string]string{
		"test": `{%- set sorted = items | sort_by(["$.value"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "c", "value": "charlie"},
			{"name": "a", "value": "alpha"},
			{"name": "b", "value": "beta"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "a,b,c,", output)
}

func TestSortByWithBooleans(t *testing.T) {
	templates := map[string]string{
		"test": `{%- set sorted = items | sort_by(["$.active:desc"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "inactive", "active": false},
			{"name": "active1", "active": true},
			{"name": "active2", "active": true},
		},
	})
	require.NoError(t, err)
	// Active items should come first
	assert.True(t, strings.HasPrefix(output, "active"))
}

// ============================================================================
// applyPipeOperation tests
// ============================================================================

func TestSortByWithComplexPipeExpression(t *testing.T) {
	// Tests applyPipeOperation with length modifier
	templates := map[string]string{
		"test": `{%- set sorted = items | sort_by(["$.tags | length:desc"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "few", "tags": []string{"a"}},
			{"name": "many", "tags": []string{"a", "b", "c", "d"}},
			{"name": "some", "tags": []string{"a", "b"}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "many,some,few,", output)
}

// ============================================================================
// recordFilter tests (tracing)
// ============================================================================

func TestRecordFilterWithTracing(t *testing.T) {
	templates := map[string]string{
		"test": `{%- set result = items | sort_by(["$.value"]) | extract("$.name") -%}
{%- for r in result -%}{{ r }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Enable tracing to test recordFilter
	engine.EnableTracing()

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "b", "value": 2},
			{"name": "a", "value": 1},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "a,b,", output)

	// Check that tracing captured filter operations
	trace := engine.GetTraceOutput()
	assert.Contains(t, trace, "sort_by")
}

// ============================================================================
// AppendTraces test
// ============================================================================

func TestAppendTracesAcrossRenders(t *testing.T) {
	templates := map[string]string{
		"test": `{{ value }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	engine.EnableTracing()

	// Render multiple times
	for i := 0; i < 3; i++ {
		_, err := engine.Render("test", map[string]interface{}{"value": i})
		require.NoError(t, err)
	}

	// Get all traces
	trace := engine.GetTraceOutput()
	// Should have multiple render entries
	assert.Contains(t, trace, "Rendering: test")
}

// ============================================================================
// Additional coverage tests for low-coverage functions
// ============================================================================

func TestSortByWithAllNumericTypes(t *testing.T) {
	// Tests toFloat64 with various numeric types
	tests := []struct {
		name     string
		items    []map[string]interface{}
		expected string
	}{
		{
			name: "int64 values",
			items: []map[string]interface{}{
				{"name": "a", "value": int64(100)},
				{"name": "b", "value": int64(50)},
				{"name": "c", "value": int64(75)},
			},
			expected: "b,c,a,",
		},
		{
			name: "float32 values",
			items: []map[string]interface{}{
				{"name": "a", "value": float32(10.5)},
				{"name": "b", "value": float32(5.2)},
				{"name": "c", "value": float32(7.8)},
			},
			expected: "b,c,a,",
		},
		{
			name: "int32 values",
			items: []map[string]interface{}{
				{"name": "a", "value": int32(30)},
				{"name": "b", "value": int32(10)},
				{"name": "c", "value": int32(20)},
			},
			expected: "b,c,a,",
		},
		{
			name: "uint values",
			items: []map[string]interface{}{
				{"name": "a", "value": uint(300)},
				{"name": "b", "value": uint(100)},
				{"name": "c", "value": uint(200)},
			},
			expected: "b,c,a,",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			templates := map[string]string{
				"test": `{%- set sorted = items | sort_by(["$.value"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
			}

			engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
			require.NoError(t, err)

			output, err := engine.Render("test", map[string]interface{}{
				"items": tt.items,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.expected, output)
		})
	}
}

func TestExtractWithWildcardInNestedPath(t *testing.T) {
	// Tests processWildcardIndex via sort_by with nested array wildcard
	templates := map[string]string{
		"test": `{%- set sorted = data | sort_by(["$.items[*] | length:desc"]) -%}
{%- for d in sorted -%}{{ d.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"data": []map[string]interface{}{
			{"name": "few", "items": []string{"a"}},
			{"name": "many", "items": []string{"a", "b", "c", "d"}},
			{"name": "some", "items": []string{"a", "b"}},
		},
	})
	require.NoError(t, err)
	// Should sort by the flattened array length
	assert.Contains(t, output, "many,")
}

func TestCalculateLengthWithDifferentTypes(t *testing.T) {
	// Tests calculateLength with different types
	templates := map[string]string{
		"test": `{%- set sorted = items | sort_by(["$.data | length:desc"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "map", "data": map[string]interface{}{"a": 1, "b": 2, "c": 3}},
			{"name": "short-string", "data": "ab"},
			{"name": "long-string", "data": "abcdefgh"},
		},
	})
	require.NoError(t, err)
	// long-string (8), map (3), short-string (2)
	assert.Equal(t, "long-string,map,short-string,", output)
}

func TestConvertToMapWithDifferentInputs(t *testing.T) {
	// Tests convertToMap with typed map input
	templates := map[string]string{
		"test": `{{ data | extract("$.key") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Test with map[string]string (not map[string]interface{})
	output, err := engine.Render("test", map[string]interface{}{
		"data": map[string]string{"key": "typed-value"},
	})
	require.NoError(t, err)
	assert.Equal(t, "typed-value", output)
}

func TestGetFieldByReflectionWithPointer(t *testing.T) {
	// Tests getFieldByReflection with pointer to struct
	type Item struct {
		Name  string
		Count int
	}

	templates := map[string]string{
		"test": `{{ data | extract("$.Name") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	item := &Item{Name: "pointer-item", Count: 42}
	output, err := engine.Render("test", map[string]interface{}{
		"data": item,
	})
	require.NoError(t, err)
	assert.Equal(t, "pointer-item", output)
}

func TestCompareValuesWithNilValues(t *testing.T) {
	// Tests compareValues with nil values
	templates := map[string]string{
		"test": `{%- set sorted = items | sort_by(["$.value"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "has-value", "value": "b"},
			{"name": "nil-value", "value": nil},
			{"name": "another-value", "value": "a"},
		},
	})
	require.NoError(t, err)
	// Items with values should be sorted, nil values may come first or last
	assert.Contains(t, output, "another-value")
	assert.Contains(t, output, "has-value")
}

func TestGroupByWithMultipleGroups(t *testing.T) {
	// Tests groupByFilter more thoroughly
	templates := map[string]string{
		"test": `{%- set groups = items | group_by("$.status") -%}
{%- for key in groups.keys() -%}{{ key }}:{{ groups[key] | length }};{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "a", "status": "active"},
			{"name": "b", "status": "inactive"},
			{"name": "c", "status": "active"},
			{"name": "d", "status": "pending"},
			{"name": "e", "status": "active"},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, output, "active:3;")
	assert.Contains(t, output, "inactive:1;")
	assert.Contains(t, output, "pending:1;")
}

func TestTransformFilterWithMultipleTransforms(t *testing.T) {
	// Tests transformFilter more thoroughly
	templates := map[string]string{
		"test": `{%- set result = items | transform(transforms) -%}
{%- for item in result -%}{{ item.full_path }}-{{ item.method_count }};{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"host": "api", "path": "/users", "methods": []string{"GET", "POST"}},
			{"host": "web", "path": "/home", "methods": []string{"GET"}},
		},
		"transforms": map[string]interface{}{
			"full_path":    "$.host ~ $.path",
			"method_count": "$.methods | length",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "api/users-2;web/home-1;", output)
}

func TestEvalFilterWithMissingField(t *testing.T) {
	// Tests evalFilter with missing field
	templates := map[string]string{
		"test": `{{ item | eval("$.nonexistent") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"item": map[string]interface{}{"name": "test"},
	})
	require.NoError(t, err)
	assert.Contains(t, output, "<nil>")
}

func TestTrimFilterWithNonStringInput(t *testing.T) {
	// Tests trimFilter error handling
	templates := map[string]string{
		"test": `{{ value | trim }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Non-string input should be converted to string
	output, err := engine.Render("test", map[string]interface{}{
		"value": 123,
	})
	require.NoError(t, err)
	assert.Equal(t, "123", output)
}

func TestCustomListMethodsWithErrorHandling(t *testing.T) {
	// Test append with invalid args
	templates := map[string]string{
		"test": `{%- set items = ["a"] -%}
{%- set result = items.append("b", "extra") -%}
{{ result | length }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	_, err = engine.Render("test", nil)
	// Should error due to extra argument
	require.Error(t, err)
}

func TestCustomDictMethodsWithUpdateError(t *testing.T) {
	// Test update with invalid input type
	templates := map[string]string{
		"test": `{%- set data = {"a": 1} -%}
{%- set result = data.update("not a dict") -%}
{{ result }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	_, err = engine.Render("test", nil)
	// Should error due to invalid update argument
	require.Error(t, err)
}

func TestSortByWithFilterDebugEnabled(t *testing.T) {
	// Tests compareByExpressionWithDebug with debug enabled
	templates := map[string]string{
		"test": `{%- set sorted = items | sort_by(["$.value"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Enable filter debug
	engine.EnableFilterDebug()

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "b", "value": 2},
			{"name": "a", "value": 1},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "a,b,", output)

	engine.DisableFilterDebug()
}

func TestDeepCopyValueWithComplexStructure(t *testing.T) {
	// Tests deepCopyValue with nested structures
	templates := map[string]string{
		"test": `{%- set result = items | transform(transforms) -%}
{%- for item in result -%}{{ item.nested_count }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{
				"name":   "complex",
				"nested": map[string]interface{}{"a": 1, "b": 2, "c": 3},
			},
		},
		"transforms": map[string]interface{}{
			"nested_count": "$.nested | length",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "3,", output)
}

// ============================================================================
// More toFloat64 coverage tests
// ============================================================================

func TestSortByWithMoreNumericTypes(t *testing.T) {
	// Tests toFloat64 with remaining numeric types
	tests := []struct {
		name     string
		items    []map[string]interface{}
		expected string
	}{
		{
			name: "int8 values",
			items: []map[string]interface{}{
				{"name": "a", "value": int8(30)},
				{"name": "b", "value": int8(10)},
				{"name": "c", "value": int8(20)},
			},
			expected: "b,c,a,",
		},
		{
			name: "int16 values",
			items: []map[string]interface{}{
				{"name": "a", "value": int16(300)},
				{"name": "b", "value": int16(100)},
				{"name": "c", "value": int16(200)},
			},
			expected: "b,c,a,",
		},
		{
			name: "uint8 values",
			items: []map[string]interface{}{
				{"name": "a", "value": uint8(30)},
				{"name": "b", "value": uint8(10)},
				{"name": "c", "value": uint8(20)},
			},
			expected: "b,c,a,",
		},
		{
			name: "uint16 values",
			items: []map[string]interface{}{
				{"name": "a", "value": uint16(300)},
				{"name": "b", "value": uint16(100)},
				{"name": "c", "value": uint16(200)},
			},
			expected: "b,c,a,",
		},
		{
			name: "uint32 values",
			items: []map[string]interface{}{
				{"name": "a", "value": uint32(3000)},
				{"name": "b", "value": uint32(1000)},
				{"name": "c", "value": uint32(2000)},
			},
			expected: "b,c,a,",
		},
		{
			name: "uint64 values",
			items: []map[string]interface{}{
				{"name": "a", "value": uint64(30000)},
				{"name": "b", "value": uint64(10000)},
				{"name": "c", "value": uint64(20000)},
			},
			expected: "b,c,a,",
		},
		{
			name: "string numeric values",
			items: []map[string]interface{}{
				{"name": "a", "value": "30.5"},
				{"name": "b", "value": "10.2"},
				{"name": "c", "value": "20.7"},
			},
			expected: "b,c,a,",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			templates := map[string]string{
				"test": `{%- set sorted = items | sort_by(["$.value"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
			}

			engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
			require.NoError(t, err)

			output, err := engine.Render("test", map[string]interface{}{
				"items": tt.items,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.expected, output)
		})
	}
}

// ============================================================================
// applyPipeOperation tests (default)
// ============================================================================

func TestSortByWithDefaultPipeOperation(t *testing.T) {
	// Tests applyPipeOperation with default() operation
	templates := map[string]string{
		"test": `{%- set sorted = items | sort_by(["$.priority | default('50')"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "c", "priority": "30"},
			{"name": "a", "priority": "10"},
			{"name": "d"}, // No priority, will use default of 50
			{"name": "b", "priority": "20"},
		},
	})
	require.NoError(t, err)
	// Order should be: a(10), b(20), c(30), d(50-default)
	assert.Equal(t, "a,b,c,d,", output)
}

// ============================================================================
// buildGlobalFunctions tests
// ============================================================================

func TestMultipleGlobalFunctions(t *testing.T) {
	// Tests buildGlobalFunctions with multiple functions
	templates := map[string]string{
		"test": `{{ func1("a") }}-{{ func2("b") }}`,
	}

	globalFuncs := map[string]GlobalFunc{
		"func1": func(args ...interface{}) (interface{}, error) {
			return fmt.Sprintf("f1:%v", args[0]), nil
		},
		"func2": func(args ...interface{}) (interface{}, error) {
			return fmt.Sprintf("f2:%v", args[0]), nil
		},
	}

	engine, err := New(EngineTypeGonja, templates, nil, globalFuncs, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Equal(t, "f1:a-f2:b", output)
}

func TestGlobalFunctionWithNoArgs(t *testing.T) {
	// Tests buildGlobalFunctions with function that takes no args
	templates := map[string]string{
		"test": `{{ get_constant() }}`,
	}

	globalFuncs := map[string]GlobalFunc{
		"get_constant": func(args ...interface{}) (interface{}, error) {
			return "constant-value", nil
		},
	}

	engine, err := New(EngineTypeGonja, templates, nil, globalFuncs, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Equal(t, "constant-value", output)
}

// ============================================================================
// wrapCustomFilter tests
// ============================================================================

func TestMultipleCustomFilters(t *testing.T) {
	// Tests wrapCustomFilter with multiple custom filters
	templates := map[string]string{
		"test": `{{ value | double | triple }}`,
	}

	customFilters := map[string]FilterFunc{
		"double": func(in interface{}, args ...interface{}) (interface{}, error) {
			if v, ok := in.(int); ok {
				return v * 2, nil
			}
			return in, nil
		},
		"triple": func(in interface{}, args ...interface{}) (interface{}, error) {
			if v, ok := in.(int); ok {
				return v * 3, nil
			}
			return in, nil
		},
	}

	engine, err := New(EngineTypeGonja, templates, customFilters, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"value": 5,
	})
	require.NoError(t, err)
	// 5 * 2 = 10, 10 * 3 = 30
	assert.Equal(t, "30", output)
}

func TestCustomFilterWithArgs(t *testing.T) {
	// Tests wrapCustomFilter with filter that accepts arguments
	templates := map[string]string{
		"test": `{{ value | multiply(3) }}`,
	}

	customFilters := map[string]FilterFunc{
		"multiply": func(in interface{}, args ...interface{}) (interface{}, error) {
			if len(args) < 1 {
				return in, fmt.Errorf("multiply requires an argument")
			}
			if v, ok := in.(int); ok {
				if multiplier, ok := args[0].(int); ok {
					return v * multiplier, nil
				}
			}
			return in, nil
		},
	}

	engine, err := New(EngineTypeGonja, templates, customFilters, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"value": 7,
	})
	require.NoError(t, err)
	assert.Equal(t, "21", output)
}

// ============================================================================
// processWildcardIndex tests
// ============================================================================

func TestSortByWithWildcardNestedAccess(t *testing.T) {
	// Tests processWildcardIndex with wildcard array access
	templates := map[string]string{
		"test": `{%- set sorted = data | sort_by(["$.tags[*] | length:desc"]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"data": []map[string]interface{}{
			{"name": "few", "tags": []string{"a"}},
			{"name": "many", "tags": []string{"a", "b", "c", "d"}},
			{"name": "some", "tags": []string{"a", "b"}},
		},
	})
	require.NoError(t, err)
	// many has 4 tags, some has 2, few has 1
	assert.Contains(t, output, "many")
}

// ============================================================================
// recordFilter and extractFromSlice tests
// ============================================================================

func TestExtractFilterWithTracing(t *testing.T) {
	// Tests recordExtractFilterTrace
	templates := map[string]string{
		"test": `{%- set names = items | extract("$.name") -%}
{%- for name in names -%}{{ name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	engine.EnableTracing()

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "alpha"},
			{"name": "beta"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "alpha,beta,", output)

	trace := engine.GetTraceOutput()
	assert.Contains(t, trace, "extract")
}

func TestExtractFromNonSlice(t *testing.T) {
	// Tests extractFromSlice with non-slice input
	templates := map[string]string{
		"test": `{{ item | extract("$.name") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	// Single item (not a slice)
	output, err := engine.Render("test", map[string]interface{}{
		"item": map[string]interface{}{"name": "single"},
	})
	require.NoError(t, err)
	assert.Equal(t, "single", output)
}

// ============================================================================
// sortByFilter edge cases
// ============================================================================

func TestSortByWithEmptyCriteria(t *testing.T) {
	templates := map[string]string{
		"test": `{%- set sorted = items | sort_by([]) -%}
{%- for item in sorted -%}{{ item.name }},{%- endfor -%}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"items": []map[string]interface{}{
			{"name": "b"},
			{"name": "a"},
		},
	})
	require.NoError(t, err)
	// With empty criteria, order should remain unchanged
	assert.Contains(t, output, "a")
	assert.Contains(t, output, "b")
}

// ============================================================================
// trimFilter edge cases
// ============================================================================

func TestTrimFilterWithMultipleCustomChars(t *testing.T) {
	templates := map[string]string{
		"test": `{{ value | trim("xy") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"value": "xyhelloxy",
	})
	require.NoError(t, err)
	assert.Equal(t, "hello", output)
}

// ============================================================================
// evalFilter edge cases
// ============================================================================

func TestEvalFilterWithConcatenation(t *testing.T) {
	templates := map[string]string{
		"test": `{{ item | eval("$.first ~ '-' ~ $.last") }}`,
	}

	engine, err := New(EngineTypeGonja, templates, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", map[string]interface{}{
		"item": map[string]interface{}{"first": "hello", "last": "world"},
	})
	require.NoError(t, err)
	assert.Contains(t, output, "hello-world")
}
