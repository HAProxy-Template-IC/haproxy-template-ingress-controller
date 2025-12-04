# Template Engine Library

## Overview

This package provides a Go template engine with Jinja2-like syntax through Gonja v2. Templates are pre-compiled at initialization for fast rendering and early error detection.

**Key features:**
- Pre-compilation at startup (fail-fast, microsecond rendering)
- Jinja2/Django template syntax
- Thread-safe concurrent rendering
- Custom filters for HAProxy use cases

## Quick Start

```go
package main

import (
    "log"
    "haproxy-template-ic/pkg/templating"
)

func main() {
    templates := map[string]string{
        "greeting": "Hello {{ name }}!",
        "config":   "server {{ host }}:{{ port }}",
    }

    engine, err := templating.New(templating.EngineTypeGonja, templates)
    if err != nil {
        log.Fatalf("failed to create engine: %v", err)
    }

    output, err := engine.Render("greeting", map[string]interface{}{
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
func New(engineType EngineType, templates map[string]string) (*TemplateEngine, error)
func NewWithFilters(engineType EngineType, templates map[string]string, filters map[string]FilterFunc) (*TemplateEngine, error)
```

Creates a new engine and compiles all templates. Returns `CompilationError` if any template has syntax errors.

### Rendering

```go
func (e *TemplateEngine) Render(templateName string, context map[string]interface{}) (string, error)
```

Executes a template with the provided context.

### Helper Methods

```go
func (e *TemplateEngine) HasTemplate(name string) bool      // Check existence
func (e *TemplateEngine) TemplateNames() []string           // List templates
func (e *TemplateEngine) TemplateCount() int                // Count templates
func (e *TemplateEngine) GetRawTemplate(name string) (string, error)  // Get source
```

### Error Types

| Type | When Returned | Key Fields |
|------|---------------|------------|
| `CompilationError` | Template syntax error | `TemplateName`, `TemplateSnippet`, `Cause` |
| `RenderError` | Runtime rendering failure | `TemplateName`, `Cause` |
| `TemplateNotFoundError` | Template doesn't exist | `TemplateName`, `AvailableTemplates` |
| `UnsupportedEngineError` | Invalid engine type | `EngineType` |

```go
output, err := engine.Render("mytemplate", context)
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

engine, err := templating.NewWithFilters(templating.EngineTypeGonja, templates, filters)
```

### Built-in Custom Filters

| Filter | Description | Example |
|--------|-------------|---------|
| `b64decode` | Decode base64 | `{{ secret.data.password \| b64decode }}` |
| `glob_match` | Filter by glob pattern | `{{ snippets \| glob_match("backend-*") }}` |
| `sort_by` | Sort by JSONPath | `{{ routes \| sort_by(["$.priority:desc"]) }}` |
| `extract` | Extract via JSONPath | `{{ routes \| extract("$.rules[*].host") }}` |
| `group_by` | Group by JSONPath | `{{ items \| group_by("$.namespace") }}` |
| `transform` | Regex substitution | `{{ paths \| transform("^/api", "") }}` |
| `debug` | Dump as JSON comment | `{{ routes \| debug("routes") }}` |
| `eval` | Show JSONPath evaluation | `{{ route \| eval("$.priority") }}` |

**sort_by modifiers:**
- `:desc` - Descending order
- `:exists` - Sort by field presence
- `| length` - Sort by collection/string length

**pathResolver.GetPath()** - Context method for file path resolution:
```jinja2
{{ pathResolver.GetPath("host.map", "map") }}     {# /etc/haproxy/maps/host.map #}
{{ pathResolver.GetPath("cert.pem", "cert") }}    {# /etc/haproxy/ssl/cert.pem #}
{{ pathResolver.GetPath("error.http", "file") }}  {# /etc/haproxy/general/error.http #}
```

## Template Syntax

Templates use Gonja v2 with Jinja2-like syntax. See [Gonja Documentation](https://github.com/nikolalohinski/gonja) for complete reference.

**Variables:**
```jinja2
{{ variable_name }}
{{ user.name }}
{{ items[0] }}
```

**Filters:**
```jinja2
{{ text | upper }}
{{ value | default("N/A") }}
{{ items | join(", ") }}
```

**Control Structures:**
```jinja2
{% if condition %}...{% endif %}
{% for item in items %}...{% endfor %}
```

**Loop Variables:** `loop.index`, `loop.index0`, `loop.first`, `loop.last`, `loop.length`

**Whitespace Control:** Use `{%-` and `-%}` to strip whitespace.

## Custom Tags

### compute_once

Executes expensive computations only once per render, even across multiple includes:

```jinja2
{%- set analysis = namespace(data=[]) %}
{%- compute_once analysis %}
  {# Expensive computation - only runs once #}
  {%- for item in large_dataset %}
    {%- set analysis.data = analysis.data.append(process(item)) %}
  {%- endfor %}
{%- endcompute_once %}
```

**Requirements:**
- Variable must be created before the `compute_once` block
- Use `namespace()` for mutable state

**Performance:** Reduces redundant computations by 75%+ in multi-include scenarios.

## Template Tracing

```go
engine.EnableTracing()
output, _ := engine.Render("template", context)
trace := engine.GetTraceOutput()
// Rendering: haproxy.cfg
// Completed: haproxy.cfg (0.007ms)
engine.DisableTracing()
```

## Filter Debug Logging

```go
engine.EnableFilterDebug()
// Logs sort_by comparisons with values and types
output, _ := engine.Render("template", context)
engine.DisableFilterDebug()
```

## Best Practices

**1. Pre-compile at startup:**
```go
// Good - compile once, reuse
engine, err := templating.New(templating.EngineTypeGonja, templates)
for _, ctx := range contexts {
    output, _ := engine.Render("template", ctx)
}

// Bad - recompiles every time
for _, ctx := range contexts {
    engine, _ := templating.New(templating.EngineTypeGonja, templates)
    output, _ := engine.Render("template", ctx)
}
```

**2. Check compilation errors early:**
```go
engine, err := templating.New(templating.EngineTypeGonja, templates)
if err != nil {
    var compErr *templating.CompilationError
    if errors.As(err, &compErr) {
        log.Fatal("compilation failed", "template", compErr.TemplateName)
    }
}
```

**3. Use default filter for optional values:**
```jinja2
timeout connect {{ timeout_connect | default("5s") }}
```

**4. Break large templates into pieces:**
```go
templates := map[string]string{
    "haproxy.cfg": `{% include "global" %}{% include "backends" %}`,
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

- [Gonja Documentation](https://github.com/nikolalohinski/gonja)
- [Templating Guide](../../docs/controller/docs/templating.md) - User documentation
