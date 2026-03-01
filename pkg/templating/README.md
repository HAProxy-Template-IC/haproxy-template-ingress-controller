# Template Engine Library

## Overview

This package provides template rendering using Scriggo, a Go-native template engine.

**Key features:**

- Pre-compilation at startup (fail-fast, microsecond rendering)
- Go template syntax
- Thread-safe concurrent rendering
- Custom filters for HAProxy use cases
- Dynamic include support

## Quick Start

```go
package main

import (
    "context"
    "log"
    "haptic/pkg/templating"
)

func main() {
    templates := map[string]string{
        "greeting": "Hello {{ name }}!",
        "config":   "server {{ host }}:{{ port }}",
    }

    // EngineTypeScriggo is the default and recommended engine
    engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
    if err != nil {
        log.Fatalf("failed to create engine: %v", err)
    }

    output, err := engine.Render(context.Background(), "greeting", map[string]interface{}{
        "name": "World",
    })
    if err != nil {
        log.Fatal(err)
    }

    log.Println(output) // Output: Hello World!
}
```

## API Reference

### Constructor

```go
func New(engineType EngineType, templates map[string]string, customFilters map[string]FilterFunc, customFunctions map[string]GlobalFunc, postProcessorConfigs map[string][]PostProcessorConfig) (Engine, error)
```

Creates a new engine and compiles all templates. Returns `CompilationError` if any template has syntax errors.

### Rendering

```go
func (e Engine) Render(ctx context.Context, templateName string, templateContext map[string]interface{}) (string, error)
```

Executes a template with the provided context. The `ctx` parameter controls cancellation and timeouts.

### Helper Methods

```go
func (e Engine) HasTemplate(name string) bool      // Check existence
func (e Engine) TemplateNames() []string           // List templates
func (e Engine) TemplateCount() int                // Count templates
func (e Engine) GetRawTemplate(name string) (string, error)  // Get source
```

### Error Types

| Type | When Returned | Key Fields |
|------|---------------|------------|
| `CompilationError` | Template syntax error | `TemplateName`, `TemplateSnippet`, `Cause` |
| `RenderError` | Runtime rendering failure | `TemplateName`, `Cause` |
| `RenderTimeoutError` | Context deadline exceeded | `TemplateName`, `Cause` |
| `TemplateNotFoundError` | Template doesn't exist | `TemplateName`, `AvailableTemplates` |
| `UnsupportedEngineError` | Invalid engine type | `EngineType` |

```go
output, err := engine.Render(ctx, "mytemplate", context)
if err != nil {
    var renderErr *templating.RenderError
    if errors.As(err, &renderErr) {
        log.Printf("Render failed for '%s': %v", renderErr.TemplateName, renderErr.Cause)
    }
}
```

## Custom Filters

### Creating Custom Filters

```go
filters := map[string]templating.FilterFunc{
    "to_upper": func(in interface{}, args ...interface{}) (interface{}, error) {
        str, ok := in.(string)
        if !ok {
            return nil, fmt.Errorf("to_upper requires string input")
        }
        return strings.ToUpper(str), nil
    },
}

engine, err := templating.New(templating.EngineTypeScriggo, templates, filters, nil, nil)
```

### Built-in Custom Filters

| Filter | Description | Example |
|--------|-------------|---------|
| `b64decode` | Decode base64 | `{{ secret.data.password \| b64decode }}` |
| `glob_match` | Filter by glob pattern | `{{ templateSnippets \| glob_match("backend-*") }}` |
| `group_by` | Group by JSONPath | `{{ items \| group_by("$.namespace") }}` |
| `indent` | Indent each line by N spaces | `{{ render("snippet") \| indent(4) }}` |
| `sort_by` | Sort by JSONPath | `{{ routes \| sort_by(["$.priority:desc"]) }}` |
| `debug` | Dump as JSON comment | `{{ routes \| debug("routes") }}` |

**sort_by modifiers:**

- `:desc` - Descending order
- `:exists` - Sort by field presence
- `| length` - Sort by collection/string length

### Built-in Functions

| Function | Description | Example |
|----------|-------------|---------|
| `fail(msg)` | Stop rendering with error | `{% fail("Missing required field") %}` |
| `merge(dict, updates)` | Merge two maps | `{% config = merge(config, updates) %}` |
| `keys(dict)` | Get sorted map keys | `{% for _, k := range keys(config) %}` |

**pathResolver.GetPath()** - Context method for file path resolution:

```jinja2
{{ pathResolver.GetPath("host.map", "map") }}     {# maps/host.map #}
{{ pathResolver.GetPath("cert.pem", "cert") }}    {# ssl/cert.pem #}
{{ pathResolver.GetPath("error.http", "file") }}  {# files/error.http #}
```

## Template Syntax (Scriggo)

Scriggo uses Go template syntax. See [Scriggo Documentation](https://scriggo.com/templates) for complete reference.

**Variables:**

```go
{{ name }}
{{ user.name }}
{{ items[0] }}
```

**Functions (both function call and pipe syntax are supported):**

```go
{{ strip(value) }}
{{ coalesce(value, "default") }}
{{ value | fallback("default") }}
{{ items | join(", ") }}
```

**Control Structures:**

```go
{% if condition %}...{% end %}
{% for _, item := range items %}...{% end %}
```

**Variable Declaration:**

```go
{% var count = 0 %}
{% count = count + 1 %}
```

**Whitespace Control:** Use `{%-` and `-%}` to strip whitespace.

## Caching Expensive Computations

Use `shared.ComputeIfAbsent()` for compute-once patterns:

```go
{%- var analysis, _ = shared.ComputeIfAbsent("analysis", func() interface{} {
    return analyzeRoutes(resources)
}) -%}
```

`ComputeIfAbsent` is thread-safe and guarantees exactly-once computation. Use `shared.Get(key)` for read-only access to previously computed values.

## Template Tracing

```go
engine.EnableTracing()
output, _ := engine.Render(context.Background(), "template", ctx)
trace := engine.GetTraceOutput()
// Rendering: haproxy.cfg
// Completed: haproxy.cfg (0.007ms)
engine.DisableTracing()
```

## Filter Debug Logging

```go
engine.EnableFilterDebug()
// Logs sort_by comparisons with values and types
output, _ := engine.Render(context.Background(), "template", ctx)
engine.DisableFilterDebug()
```

## Post-Processing

Post-processors transform rendered output before it is returned. They run in sequence — each receives the previous processor's output.

**Available types:**

| Type | Description |
|------|-------------|
| `regex_replace` | Line-by-line regex find/replace (with fast-path for indentation normalization) |
| `template` | Scriggo template transformation with access to the rendered output via `input` variable |

**Configuration example:**

```yaml
postProcessing:
  - type: template
    params:
      source: |
        {%- if strings_contains(input, "__PLACEHOLDER__") -%}
        {{ replace(input, "__PLACEHOLDER__", "computed_value") }}
        {%- else -%}
        {{ input }}
        {%- end -%}
  - type: regex_replace
    params:
      pattern: "^[ ]+"
      replace: "  "
```

The `template` post-processor compiles its source at engine initialization (fail-fast on syntax errors) and has access to all standard Scriggo builtins (`regexp`, `replace`, `len`, `tostring`, `strings_contains`, etc.).

## Best Practices

**1. Pre-compile at startup:**

```go
// Good - compile once, reuse
engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
for _, ctx := range contexts {
    output, _ := engine.Render(context.Background(), "template", ctx)
}

// Bad - recompiles every time
for _, ctx := range contexts {
    engine, _ := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
    output, _ := engine.Render(context.Background(), "template", ctx)
}
```

**2. Check compilation errors early:**

```go
engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
if err != nil {
    var compErr *templating.CompilationError
    if errors.As(err, &compErr) {
        log.Fatal("compilation failed", "template", compErr.TemplateName)
    }
}
```

**3. Use coalesce for optional values (Scriggo):**

```go
timeout connect {{ coalesce(timeout_connect, "5s") }}
```

**4. Break large templates into pieces:**

```go
templates := map[string]string{
    "haproxy.cfg": `{{ render "global" }}{{ render "backends" }}`,
    "global":      "global\n    daemon",
    "backends":    "...",
}
```

## Troubleshooting

| Problem | Solution |
|---------|----------|
| Compilation error | Check template syntax, verify filter names |
| Template not found | Verify name spelling, use `HasTemplate()` |
| Empty output | Check context data, verify conditionals |
| Slow rendering | Reuse engine instance, simplify loops |

## Performance

- **Compilation:** 1-10ms per template
- **Rendering:** 10-100µs per typical template
- **Thread-safe:** Safe for concurrent use from multiple goroutines

## Related Documentation

- [Scriggo Documentation](https://scriggo.com/templates) - Template engine documentation
- [Templating Guide](../../docs/controller/docs/templating.md) - User documentation
