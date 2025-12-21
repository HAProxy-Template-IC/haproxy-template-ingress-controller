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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewScriggo_Success(t *testing.T) {
	templates := map[string]string{
		"greeting": "Hello World!",
		"farewell": "Goodbye World!",
	}
	entryPoints := []string{"greeting", "farewell"}

	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, engine)

	assert.Equal(t, EngineTypeScriggo, engine.EngineType())
	assert.Equal(t, 2, engine.TemplateCount())
	assert.True(t, engine.HasTemplate("greeting"))
	assert.True(t, engine.HasTemplate("farewell"))
	assert.False(t, engine.HasTemplate("nonexistent"))
}

func TestNewScriggo_EmptyTemplates(t *testing.T) {
	templates := map[string]string{}
	entryPoints := []string{}

	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, engine)

	assert.Equal(t, 0, engine.TemplateCount())
}

func TestNewScriggo_CompilationError(t *testing.T) {
	templates := map[string]string{
		"invalid": "Hello {{ name ", // Unclosed expression
	}
	entryPoints := []string{"invalid"}

	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)

	assert.Nil(t, engine)
	require.Error(t, err)

	var compilationErr *CompilationError
	assert.ErrorAs(t, err, &compilationErr)
	assert.Equal(t, "invalid", compilationErr.TemplateName)
}

func TestScriggoEngine_Render_StaticContent(t *testing.T) {
	templates := map[string]string{
		"static": "Hello, World!",
	}

	entryPoints := []string{"static"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("static", nil)
	require.NoError(t, err)
	assert.Equal(t, "Hello, World!\n", output)
}

func TestScriggoEngine_Render_TemplateNotFound(t *testing.T) {
	templates := map[string]string{
		"greeting": "Hello!",
	}

	entryPoints := []string{"greeting"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("nonexistent", nil)

	assert.Empty(t, output)
	require.Error(t, err)

	var notFoundErr *TemplateNotFoundError
	assert.ErrorAs(t, err, &notFoundErr)
	assert.Equal(t, "nonexistent", notFoundErr.TemplateName)
	assert.Contains(t, notFoundErr.AvailableTemplates, "greeting")
}

func TestScriggoEngine_TemplateNames(t *testing.T) {
	templates := map[string]string{
		"template1": "Content 1",
		"template2": "Content 2",
		"template3": "Content 3",
	}

	entryPoints := []string{"template1", "template2", "template3"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	names := engine.TemplateNames()
	assert.Len(t, names, 3)

	// Names should be sorted
	assert.Equal(t, []string{"template1", "template2", "template3"}, names)
}

func TestScriggoEngine_GetRawTemplate(t *testing.T) {
	templateContent := "Hello World!"
	templates := map[string]string{
		"greeting": templateContent,
	}

	entryPoints := []string{"greeting"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
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

func TestScriggoEngine_HasTemplate(t *testing.T) {
	templates := map[string]string{
		"existing": "Content",
	}

	entryPoints := []string{"existing"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	assert.True(t, engine.HasTemplate("existing"))
	assert.False(t, engine.HasTemplate("nonexistent"))
}

func TestScriggoEngine_RenderWithProfiling(t *testing.T) {
	templates := map[string]string{
		"greeting": "Hello World!",
	}

	entryPoints := []string{"greeting"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, stats, err := engine.RenderWithProfiling("greeting", nil)
	require.NoError(t, err)
	assert.Equal(t, "Hello World!\n", output)
	// Scriggo's RenderWithProfiling returns nil stats (documented limitation)
	assert.Nil(t, stats)
}

func TestScriggoEngine_Tracing(t *testing.T) {
	templates := map[string]string{
		"test": "Hello!",
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	// Initially disabled
	assert.False(t, engine.IsTracingEnabled())

	// Enable tracing
	engine.EnableTracing()
	assert.True(t, engine.IsTracingEnabled())

	// Render should generate trace
	_, err = engine.Render("test", nil)
	require.NoError(t, err)

	trace := engine.GetTraceOutput()
	assert.Contains(t, trace, "Rendering: test")
	assert.Contains(t, trace, "Completed: test")

	// Trace should be cleared after GetTraceOutput
	trace = engine.GetTraceOutput()
	assert.Empty(t, trace)

	// Disable tracing
	engine.DisableTracing()
	assert.False(t, engine.IsTracingEnabled())

	// Render without tracing should not generate trace
	_, err = engine.Render("test", nil)
	require.NoError(t, err)

	trace = engine.GetTraceOutput()
	assert.Empty(t, trace)
}

func TestScriggoEngine_Tracing_Concurrent(t *testing.T) {
	templates := map[string]string{
		"test1": "Hello!",
		"test2": "Goodbye!",
	}

	entryPoints := []string{"test1", "test2"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	engine.EnableTracing()

	const numGoroutines = 10
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				tmpl := "test1"
				if j%2 == 0 {
					tmpl = "test2"
				}
				_, err := engine.Render(tmpl, nil)
				assert.NoError(t, err)
			}
		}()
	}

	wg.Wait()

	trace := engine.GetTraceOutput()
	assert.NotEmpty(t, trace)
	assert.Contains(t, trace, "Rendering: test1")
	assert.Contains(t, trace, "Rendering: test2")
}

func TestScriggoEngine_AppendTraces(t *testing.T) {
	templates := map[string]string{
		"test": "Hello!",
	}

	entryPoints := []string{"test"}
	engine1, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)
	engine1.EnableTracing()

	engine2, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)
	engine2.EnableTracing()

	// Render in engine2
	_, err = engine2.Render("test", nil)
	require.NoError(t, err)

	// Append traces from engine2 to engine1
	engine1.AppendTraces(engine2)

	// engine1 should now have engine2's traces
	trace := engine1.GetTraceOutput()
	assert.Contains(t, trace, "Rendering: test")

	// engine2's traces should be cleared
	trace2 := engine2.GetTraceOutput()
	assert.Empty(t, trace2)
}

func TestScriggoEngine_AppendTraces_NilEngine(t *testing.T) {
	templates := map[string]string{
		"test": "Hello!",
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	// Should not panic
	engine.AppendTraces(nil)

	trace := engine.GetTraceOutput()
	assert.Empty(t, trace)
}

func TestScriggoEngine_FilterDebug(t *testing.T) {
	templates := map[string]string{
		"test": "Hello!",
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	// Initially disabled
	engine.tracing.mu.Lock()
	assert.False(t, engine.tracing.debugFilters)
	engine.tracing.mu.Unlock()

	// Enable
	engine.EnableFilterDebug()
	engine.tracing.mu.Lock()
	assert.True(t, engine.tracing.debugFilters)
	engine.tracing.mu.Unlock()

	// Disable
	engine.DisableFilterDebug()
	engine.tracing.mu.Lock()
	assert.False(t, engine.tracing.debugFilters)
	engine.tracing.mu.Unlock()
}

func TestScriggoEngine_PostProcessors(t *testing.T) {
	templates := map[string]string{
		"test": "hello foo world",
	}

	postProcessorConfigs := map[string][]PostProcessorConfig{
		"test": {
			{
				Type: PostProcessorTypeRegexReplace,
				Params: map[string]string{
					"pattern": `foo`,
					"replace": "bar",
				},
			},
		},
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, postProcessorConfigs)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)

	// Post-processor should replace "foo" with "bar"
	assert.Equal(t, "hello bar world\n", output)
}

func TestScriggoEngine_CustomFunctions(t *testing.T) {
	templates := map[string]string{
		"test": "Result: {{ greet() }}",
	}

	customFunctions := map[string]GlobalFunc{
		"greet": func(args ...interface{}) (interface{}, error) {
			return "Hello, World!", nil
		},
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, customFunctions, nil)
	require.NoError(t, err)

	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Equal(t, "Result: Hello, World!\n", output)
}

func TestNew_Scriggo(t *testing.T) {
	templates := map[string]string{
		"greeting": "Hello World!",
	}

	engine, err := New(EngineTypeScriggo, templates, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, engine)

	assert.Equal(t, EngineTypeScriggo, engine.EngineType())
}

func TestScriggoEngine_FormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"zero", 0, "0.000ms"},
		{"one_ms", time.Millisecond, "1.000ms"},
		{"fractional", 1234 * time.Microsecond, "1.234ms"},
		{"large", 123456 * time.Microsecond, "123.456ms"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.duration)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestScriggoTemplateFS(t *testing.T) {
	templates := map[string]string{
		"test.html": "content",
	}

	fs := &scriggoTemplateFS{templates: templates}

	// Test existing file
	file, err := fs.Open("test.html")
	require.NoError(t, err)
	require.NotNil(t, file)

	// Read content
	buf := make([]byte, 100)
	n, err := file.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "content", string(buf[:n]))

	// Stat
	stat, err := file.Stat()
	require.NoError(t, err)
	assert.Equal(t, "test.html", stat.Name())
	assert.Equal(t, int64(7), stat.Size())
	assert.False(t, stat.IsDir())

	// Close
	err = file.Close()
	require.NoError(t, err)

	// Test non-existent file
	_, err = fs.Open("nonexistent")
	require.Error(t, err)
}

func TestScriggoEngine_Render_EmptyContext(t *testing.T) {
	templates := map[string]string{
		"static": "Hello, World!",
	}

	entryPoints := []string{"static"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("static", nil)
	require.NoError(t, err)
	assert.Equal(t, "Hello, World!\n", output)
}

func TestScriggoEngine_BuiltinFilters(t *testing.T) {
	// Scriggo registers built-in filters as globals (functions)
	// Note: "trim" is the Scriggo builtin that requires (s, cutset) args.
	// We use "trimSpace" for whitespace trimming which requires only 1 arg.
	templates := map[string]string{
		"strip_test":     `{{ strip("  hello  ") }}`,
		"trimSpace_test": `{{ trimSpace("  world  ") }}`,
	}

	entryPoints := []string{"strip_test", "trimSpace_test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("strip_test", nil)
	require.NoError(t, err)
	assert.Equal(t, "hello\n", output)

	output, err = engine.Render("trimSpace_test", nil)
	require.NoError(t, err)
	assert.Equal(t, "world\n", output)
}

func TestScriggoEngine_B64DecodeFilter(t *testing.T) {
	templates := map[string]string{
		"decode": `{{ b64decode("SGVsbG8gV29ybGQ=") }}`,
	}

	entryPoints := []string{"decode"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render("decode", nil)
	require.NoError(t, err)
	assert.Equal(t, "Hello World\n", output)
}

func TestScriggoEngine_TemplateCount(t *testing.T) {
	templates := map[string]string{
		"a": "A",
		"b": "B",
		"c": "C",
	}

	entryPoints := []string{"a", "b", "c"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	assert.Equal(t, 3, engine.TemplateCount())
}

func TestScriggoEngine_EngineType(t *testing.T) {
	templates := map[string]string{
		"test": "content",
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	assert.Equal(t, EngineTypeScriggo, engine.EngineType())
}

func TestScriggoFileInfo(t *testing.T) {
	fi := &scriggoFileInfo{name: "test.txt", size: 100}

	assert.Equal(t, "test.txt", fi.Name())
	assert.Equal(t, int64(100), fi.Size())
	assert.Equal(t, false, fi.IsDir())
	assert.Nil(t, fi.Sys())
	assert.True(t, fi.ModTime().IsZero())
}

func TestScriggoEngine_SharedContextAccess(t *testing.T) {
	// Test that templates can use SharedContext.ComputeIfAbsent and Get methods
	templates := map[string]string{
		"test": `{%- var _, _ = shared.ComputeIfAbsent("key", func() interface{} { return "value" }) -%}Result: {{ shared.Get("key") }}`,
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	// SharedContext is auto-created by engine when not provided
	output, err := engine.Render("test", nil)
	require.NoError(t, err)
	assert.Contains(t, output, "Result: value")
}

// mockResourceStore implements ResourceStore for testing direct method calls.
type mockResourceStore struct {
	listResult      []interface{}
	fetchResult     []interface{}
	getSingleResult interface{}
}

func (m *mockResourceStore) List() []interface{} {
	return m.listResult
}

func (m *mockResourceStore) Fetch(keys ...interface{}) []interface{} {
	return m.fetchResult
}

func (m *mockResourceStore) GetSingle(keys ...interface{}) interface{} {
	return m.getSingleResult
}

func TestScriggoEngine_DirectMethodCallsOnResourceStore(t *testing.T) {
	// Test that templates can call methods directly on ResourceStore values
	// using typed maps: resources.ingresses.List()
	templates := map[string]string{
		"list_test": `{%- for _, item := range resources["ingresses"].List() -%}
{{ item.(map[string]any)["name"] }}
{%- end -%}`,
		"get_test": `{%- var secret = resources["secrets"].GetSingle("default", "my-secret") -%}
Secret: {{ secret.(map[string]any)["name"] }}`,
		"fetch_test": `{%- for _, ep := range resources["endpoints"].Fetch("my-service") -%}
{{ ep.(map[string]any)["name"] }}
{%- end -%}`,
	}

	entryPoints := []string{"list_test", "get_test", "fetch_test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	// Create mock stores with test data
	ingressStore := &mockResourceStore{
		listResult: []interface{}{
			map[string]interface{}{"name": "ingress-1"},
			map[string]interface{}{"name": "ingress-2"},
		},
	}
	secretStore := &mockResourceStore{
		getSingleResult: map[string]interface{}{"name": "my-secret"},
	}
	endpointStore := &mockResourceStore{
		fetchResult: []interface{}{
			map[string]interface{}{"name": "endpoint-1"},
			map[string]interface{}{"name": "endpoint-2"},
		},
	}

	// Create typed resources map
	resources := map[string]ResourceStore{
		"ingresses": ingressStore,
		"secrets":   secretStore,
		"endpoints": endpointStore,
	}

	context := map[string]interface{}{
		"resources":        resources,
		"templateSnippets": []string{},
	}

	// Test List()
	output, err := engine.Render("list_test", context)
	require.NoError(t, err)
	assert.Contains(t, output, "ingress-1")
	assert.Contains(t, output, "ingress-2")

	// Test GetSingle()
	output, err = engine.Render("get_test", context)
	require.NoError(t, err)
	assert.Contains(t, output, "Secret: my-secret")

	// Test Fetch()
	output, err = engine.Render("fetch_test", context)
	require.NoError(t, err)
	assert.Contains(t, output, "endpoint-1")
	assert.Contains(t, output, "endpoint-2")
}

func TestScriggoEngine_DirectMethodCallsOnControllerStore(t *testing.T) {
	// Test that templates can call methods directly on controller stores
	// using typed maps: controller.haproxy_pods.List()
	templates := map[string]string{
		"pods_test": `{%- var pods = controller["haproxy_pods"].List() -%}
Pod count: {{ len(pods) }}`,
	}

	entryPoints := []string{"pods_test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	// Create mock store with test data
	podStore := &mockResourceStore{
		listResult: []interface{}{
			map[string]interface{}{"name": "haproxy-pod-1"},
			map[string]interface{}{"name": "haproxy-pod-2"},
			map[string]interface{}{"name": "haproxy-pod-3"},
		},
	}

	// Create typed controller map
	controller := map[string]ResourceStore{
		"haproxy_pods": podStore,
	}

	context := map[string]interface{}{
		"controller":       controller,
		"templateSnippets": []string{},
	}

	output, err := engine.Render("pods_test", context)
	require.NoError(t, err)
	assert.Contains(t, output, "Pod count: 3")
}

func TestScriggoEngine_DotNotationMethodCallsOnResourceStore(t *testing.T) {
	// Test that templates can call methods using DOT notation (not bracket notation)
	// This is the pattern used in our actual templates: resources.ingresses.List()
	templates := map[string]string{
		"dot_notation_test": `{%- for _, item := range resources.ingresses.List() -%}
{{ item.(map[string]any)["name"] }}
{%- end -%}`,
	}

	entryPoints := []string{"dot_notation_test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err)

	// Create mock store with test data
	ingressStore := &mockResourceStore{
		listResult: []interface{}{
			map[string]interface{}{"name": "ingress-1"},
			map[string]interface{}{"name": "ingress-2"},
		},
	}

	// Create typed resources map
	resources := map[string]ResourceStore{
		"ingresses": ingressStore,
	}

	context := map[string]interface{}{
		"resources":        resources,
		"templateSnippets": []string{},
	}

	output, err := engine.Render("dot_notation_test", context)
	require.NoError(t, err)
	assert.Contains(t, output, "ingress-1")
	assert.Contains(t, output, "ingress-2")
}

func TestScriggoEngine_SharedContextComputeIfAbsentPattern(t *testing.T) {
	// Test the ComputeIfAbsent pattern for caching complex data structures.
	// This replaces the old has_cached/set_cached/shared["key"] patterns.
	templates := map[string]string{
		"test": `{#- Use ComputeIfAbsent to cache analysis once -#}
{%- var analysis, wasComputed = shared.ComputeIfAbsent("ssl_analysis", func() interface{} {
  var result = map[string]any{"backends": []any{}}
  for _, ingress := range resources.ingresses.List() {
    var ssl = ingress | dig("metadata", "annotations", "haproxy.org/ssl-passthrough") | fallback("")
    if ssl == "true" {
      var ns = ingress | dig("metadata", "namespace") | fallback("") | tostring()
      var name = ingress | dig("metadata", "name") | fallback("") | tostring()
      if first_seen("ssl_host", ns, name) {
        var backend = map[string]any{"name": "ssl-" + ns + "-" + name}
        result["backends"] = append(result["backends"].([]any), backend)
      }
    }
  }
  return result
}) %}
{%- var analysisMap = analysis.(map[string]any) %}
wasComputed={{ wasComputed }}
count={{ len(analysisMap["backends"].([]any)) }}`,
	}

	entryPoints := []string{"test"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err, "Failed to compile template with ComputeIfAbsent pattern")

	// Create mock store with test data
	ingressStore := &mockResourceStore{
		listResult: []interface{}{
			map[string]interface{}{
				"metadata": map[string]interface{}{
					"namespace": "default",
					"name":      "test-ingress",
					"annotations": map[string]interface{}{
						"haproxy.org/ssl-passthrough": "true",
					},
				},
			},
		},
	}

	resources := map[string]ResourceStore{
		"ingresses": ingressStore,
	}

	context := map[string]interface{}{
		"resources": resources,
	}

	output, err := engine.Render("test", context)
	require.NoError(t, err, "Failed to render template")
	assert.Contains(t, output, "wasComputed=true")
	assert.Contains(t, output, "count=1")
}

func TestScriggoEngine_ImportMacroWithResourceStoreAccess(t *testing.T) {
	// This test mimics the EXACT production pattern that fails:
	// - A template that imports another template
	// - The imported template defines a macro
	// - The macro uses resources.ingresses.List()
	//
	// Error in production: "cannot call non-function resources.ingresses.List (type interface {})"
	templates := map[string]string{
		// Template that imports another and defines a macro using resources.ingresses.List()
		"util-path-map-entry-ingress": `{#- Generate map entries #}
{%- import "util-backend-name-ingress" for BackendNameIngress -%}

{% macro PathMapEntryIngress(path_types []string, suffix string) string %}
{%- for _, ingress := range resources.ingresses.List() %}
Name: {{ ingress.(map[string]any)["name"] }}
Backend: {{ BackendNameIngress(ingress.(map[string]any)["name"].(string)) }}
{%- end %}
{% end macro %}
`,
		// The imported template (also defines a macro)
		"util-backend-name-ingress": `
{% macro BackendNameIngress(name string) string %}
backend-{{ name }}
{% end macro %}
`,
		// Template that uses the macro
		"map-path-exact-ingress": `{%- import "util-path-map-entry-ingress" for PathMapEntryIngress -%}
Output: {{ PathMapEntryIngress([]string{"Exact"}, "") }}
`,
	}

	entryPoints := []string{"util-path-map-entry-ingress", "util-backend-name-ingress", "map-path-exact-ingress"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	if err != nil {
		t.Logf("Compilation error: %v", err)
	}
	require.NoError(t, err, "Failed to compile templates with import/macro using resources")

	// Create mock store with test data
	ingressStore := &mockResourceStore{
		listResult: []interface{}{
			map[string]interface{}{"name": "test-ingress"},
		},
	}

	// Create typed resources map
	resources := map[string]ResourceStore{
		"ingresses": ingressStore,
	}

	context := map[string]interface{}{
		"resources":        resources,
		"templateSnippets": []string{},
	}

	output, err := engine.Render("map-path-exact-ingress", context)
	require.NoError(t, err)
	assert.Contains(t, output, "Name: test-ingress")
	assert.Contains(t, output, "Backend: backend-test-ingress")
}

// TestMacroWithRenderGlobInheritContext reproduces a bug where render_glob with
// inherit_context inside a user-defined macro causes a nil pointer dereference
// at the macro CALL site.
//
// The issue occurs when:
// 1. A template has imports at the top
// 2. A template has a macro that iterates over items
// 3. Inside the loop, local variables are declared
// 4. Inside the loop, render_glob with inherit_context is called
// 5. The macro is called
//
// Expected: The template should render successfully
// Actual: Runtime error: invalid memory address or nil pointer dereference.
func TestMacroWithRenderGlobInheritContext(t *testing.T) {
	templates := map[string]string{
		// Main entry point uses render_glob (no inherit_context)
		"main": `# Main Template
{{- render_glob "backends-*" }}
# End`,

		// Template with imports + macro + render_glob inherit_context
		// This matches the structure in ingress.yaml
		"backends-500-test": `{%- import "util-helper" for HelperMacro -%}
{#- Macro wrapping the backend generation -#}
{% macro BackendsTestMacro(items []any) string %}
  {%- for _, item := range items -%}
{{ HelperMacro(item) }}
  {%- var opts = map[string]any{"flags": []any{}} %}
  {{- render_glob "backend-directives-*" inherit_context }}
  {%- end -%}
{% end %}
{{ BackendsTestMacro(resources.ingresses.List()) }}
`,
		// Utility macro that gets imported
		"util-helper": `{% macro HelperMacro(item any) string %}helper:{{ item | dig("name") | fallback("") }}{% end %}`,

		// Extension snippets that access inherited variables
		// Using simple nil checks to avoid type issues
		"backend-directives-100": `{%- if item != nil %}  # item exists{% end %}`,
		"backend-directives-200": `{%- if opts != nil %}  # opts exists{% end %}`,
	}

	// The controller compiles only entry points (haproxy.cfg, maps), not snippets
	// Snippets like backends-* are discovered via render_glob
	entryPoints := []string{"main"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err, "Failed to compile templates with macro + render_glob inherit_context")

	// Create mock store with test data
	ingressStore := &mockResourceStore{
		listResult: []interface{}{
			map[string]interface{}{"name": "test-1"},
			map[string]interface{}{"name": "test-2"},
		},
	}

	// Create typed resources map
	resources := map[string]ResourceStore{
		"ingresses": ingressStore,
	}

	context := map[string]interface{}{
		"resources":        resources,
		"templateSnippets": []string{"backend-directives-100", "backend-directives-200"},
	}

	output, err := engine.Render("main", context)
	require.NoError(t, err, "render_glob with inherit_context inside macro should not cause nil pointer dereference")
	assert.Contains(t, output, "# item exists")
	assert.Contains(t, output, "# opts exists")
	assert.Contains(t, output, "helper:test-1")
}

// TestMacroWithRenderGlobInheritContext_ManyTemplates tests with 13 templates
// (matching the real backend-directives-* count) and exact variable names.
func TestMacroWithRenderGlobInheritContext_ManyTemplates(t *testing.T) {
	// Build 13 backend-directives templates (matching real count)
	templates := map[string]string{
		// Main entry point uses render_glob (no inherit_context)
		"haproxy.cfg": `# HAProxy Config
{{- render_glob "backends-*" }}
# End`,

		// Template with imports + macro + render_glob inherit_context
		// Using exact variable names from real templates: ingress, serverOpts
		"backends-500-ingress": `{%- import "util-backend-name-ingress" for BackendNameIngress -%}
{%- import "util-backend-servers" for BackendServers -%}

{#- Macro wrapping the backend generation -#}
{% macro BackendsIngressShard(ingresses []any, isFirstShard bool) string %}
  {%- var isFirst = isFirstShard %}
  {%- for _, ingress := range ingresses -%}
  {%- if isFirst %}
# backends-ingress

  {%- isFirst = false %}
  {%- end -%}
  {%- var rules = ingress | dig("spec", "rules") %}
  {%- if rules != nil %}
  {%- for _, rule := range rules -%}
  {%- var httpPaths = rule | dig("http", "paths") %}
  {%- if httpPaths != nil %}
  {%- for _, path := range httpPaths -%}
  {%- var service = path | dig("backend", "service") %}
  {%- if service != nil %}
  {%- var serviceName = service | dig("name") | fallback("") -%}
  {%- var port = service | dig("port", "number") | fallback(80) -%}
{%- if first_seen("ingress_backend", ingress | dig("metadata", "namespace"), ingress | dig("metadata", "name"), serviceName) -%}
# Backend for: Ingress {{ ingress | dig("metadata", "namespace") | fallback("") }}/{{ ingress | dig("metadata", "name") | fallback("") }}
backend {{ BackendNameIngress(ingress, path) }}
  balance roundrobin
  {%- var serverOpts = map[string]any{"flags": []any{}} %}
  {{- render_glob "backend-directives-*" inherit_context }}
{{ BackendServers(tostring(serviceName), port, serverOpts) }}
  {%- end -%}
  {%- end -%}
  {%- end -%}
  {%- end -%}
  {%- end -%}
  {%- end -%}
  {%- end -%}
{% end %}
{{ BackendsIngressShard(resources.ingresses.List(), true) }}
`,
		// Utility macros
		"util-backend-name-ingress": `{% macro BackendNameIngress(ingress any, path any) string %}ing_{{ ingress | dig("metadata", "namespace") }}_{{ ingress | dig("metadata", "name") }}{% end %}`,
		"util-backend-servers": `{% macro BackendServers(serviceName string, port any, serverOpts map[string]any) string %}  # svc={{ serviceName }} port={{ port }}
  server SRV {{ serviceName }}:{{ port }}{% end %}`,

		// 13 backend-directives templates (matching real count)
		// Some of these MODIFY serverOpts (like the real templates do)
		"backend-directives-100-a": `{%- if ingress != nil %}  # directive-100-a
{%- if serverOpts != nil %}{% serverOpts["modified100a"] = true -%}{%- end %}{%- end %}`,
		"backend-directives-100-b": `{%- if serverOpts != nil %}  # directive-100-b{% end %}`,
		"backend-directives-150":   `{%- if ingress != nil %}  # directive-150{% end %}`,
		"backend-directives-200":   `{%- if ingress != nil %}  # directive-200{% end %}`,
		"backend-directives-210":   `{%- if ingress != nil %}  # directive-210{% end %}`,
		"backend-directives-250-a": `{%- if ingress != nil %}  # directive-250-a{% end %}`,
		"backend-directives-250-b": `{%- if serverOpts != nil %}  # directive-250-b{% end %}`,
		"backend-directives-300":   `{%- if ingress != nil %}  # directive-300{% end %}`,
		"backend-directives-350":   `{%- if ingress != nil %}  # directive-350{% end %}`,
		"backend-directives-400":   `{%- if ingress != nil %}  # directive-400{% end %}`,
		"backend-directives-401":   `{%- if ingress != nil %}  # directive-401{% end %}`,
		"backend-directives-500":   `{%- if ingress != nil %}  # directive-500{% end %}`,
		"backend-directives-900":   `{%- if ingress != nil %}  # directive-900{% end %}`,
	}

	// Compile only haproxy.cfg as entry point (like the real controller)
	entryPoints := []string{"haproxy.cfg"}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err, "Failed to compile templates")

	// Create mock store with test data matching real structure
	ingressStore := &mockResourceStore{
		listResult: []interface{}{
			map[string]interface{}{
				"metadata": map[string]interface{}{"namespace": "default", "name": "ing1"},
				"spec": map[string]interface{}{
					"rules": []interface{}{
						map[string]interface{}{
							"http": map[string]interface{}{
								"paths": []interface{}{
									map[string]interface{}{
										"backend": map[string]interface{}{
											"service": map[string]interface{}{"name": "svc1", "port": map[string]interface{}{"number": 80}},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	resources := map[string]ResourceStore{
		"ingresses": ingressStore,
	}

	// Generate templateSnippets list for glob_match
	var snippetNames []string
	for name := range templates {
		if name != "haproxy.cfg" {
			snippetNames = append(snippetNames, name)
		}
	}

	context := map[string]interface{}{
		"resources":        resources,
		"templateSnippets": snippetNames,
	}

	output, err := engine.Render("haproxy.cfg", context)
	require.NoError(t, err, "render_glob with inherit_context inside macro should not cause nil pointer dereference")
	assert.Contains(t, output, "# backends-ingress")
	assert.Contains(t, output, "backend ing_default_ing1")
}

// TestMacroWithRenderGlobInheritContext_FullContext tests with the full rendering context
// that the controller testrunner uses, to see if missing context variables cause the bug.
func TestMacroWithRenderGlobInheritContext_FullContext(t *testing.T) {
	// Build templates with the minimal test case
	templates := map[string]string{
		"haproxy.cfg": `# HAProxy Config
{{- render_glob "backends-*" }}
# End`,
		"backends-500-ingress": `{#- MINIMAL TEST: Macro with render_glob inherit_context -#}
{% macro TestMacro(ingresses []any) string %}
  {%- for _, ingress := range ingresses -%}
  {%- var serverOpts = map[string]any{"flags": []any{}} %}
  {{- render_glob "backend-directives-*" inherit_context }}
  {%- end -%}
{% end %}
{{ TestMacro(resources.ingresses.List()) }}`,
		// 13 backend-directives templates (matching real count)
		"backend-directives-100-a": `{%- if ingress != nil %}  # directive-100-a{% end %}`,
		"backend-directives-100-b": `{%- if serverOpts != nil %}  # directive-100-b{% end %}`,
		"backend-directives-150":   `{%- if ingress != nil %}  # directive-150{% end %}`,
		"backend-directives-200":   `{%- if ingress != nil %}  # directive-200{% end %}`,
		"backend-directives-210":   `{%- if ingress != nil %}  # directive-210{% end %}`,
		"backend-directives-250-a": `{%- if ingress != nil %}  # directive-250-a{% end %}`,
		"backend-directives-250-b": `{%- if serverOpts != nil %}  # directive-250-b{% end %}`,
		"backend-directives-300":   `{%- if ingress != nil %}  # directive-300{% end %}`,
		"backend-directives-350":   `{%- if ingress != nil %}  # directive-350{% end %}`,
		"backend-directives-400":   `{%- if ingress != nil %}  # directive-400{% end %}`,
		"backend-directives-401":   `{%- if ingress != nil %}  # directive-401{% end %}`,
		"backend-directives-500":   `{%- if ingress != nil %}  # directive-500{% end %}`,
		"backend-directives-900":   `{%- if ingress != nil %}  # directive-900{% end %}`,
		// Add map files as entry points like the controller does
		"host.map": `# host map
{{- render_glob "map-host-*" }}`,
		"path-prefix.map":            `# path prefix map`,
		"path-exact.map":             `# path exact map`,
		"path-prefix-exact.map":      `# path prefix exact map`,
		"path-regex.map":             `# path regex map`,
		"weighted-multi-backend.map": `# weighted map`,
	}

	// Compile with multiple entry points (matching controller behavior)
	entryPoints := []string{
		"haproxy.cfg",
		"host.map",
		"path-prefix.map",
		"path-exact.map",
		"path-prefix-exact.map",
		"path-regex.map",
		"weighted-multi-backend.map",
	}
	engine, err := NewScriggo(templates, entryPoints, nil, nil, nil)
	require.NoError(t, err, "Failed to compile templates")

	// Create mock stores with test data
	ingressStore := &mockResourceStore{
		listResult: []interface{}{
			map[string]interface{}{
				"metadata": map[string]interface{}{"namespace": "default", "name": "ing1"},
				"spec": map[string]interface{}{
					"rules": []interface{}{
						map[string]interface{}{
							"http": map[string]interface{}{
								"paths": []interface{}{
									map[string]interface{}{
										"backend": map[string]interface{}{
											"service": map[string]interface{}{"name": "svc1", "port": map[string]interface{}{"number": 80}},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	resources := map[string]ResourceStore{
		"ingresses": ingressStore,
	}

	// Generate templateSnippets list
	var snippetNames []string
	for name := range templates {
		if name != "haproxy.cfg" {
			snippetNames = append(snippetNames, name)
		}
	}

	// Create a PathResolver (like testrunner does)
	pathResolver := &PathResolver{
		MapsDir:    "/tmp/maps",
		SSLDir:     "/tmp/ssl",
		CRTListDir: "/tmp/certs",
		GeneralDir: "/tmp/general",
	}

	// Full context matching testrunner's buildRenderingContext
	context := map[string]interface{}{
		"resources":        resources,
		"templateSnippets": snippetNames,
		"pathResolver":     pathResolver,
		"shared":           make(map[string]interface{}),
		"globalFeatures":   make(map[string]interface{}),
		"dataplane":        map[string]interface{}{},
		"controller":       map[string]ResourceStore{},
		// Note: fileRegistry and http are also used but require more complex setup
	}

	output, err := engine.Render("haproxy.cfg", context)
	require.NoError(t, err, "render_glob with inherit_context inside macro should not cause nil pointer dereference with full context")
	assert.Contains(t, output, "directive-100-a")
}
