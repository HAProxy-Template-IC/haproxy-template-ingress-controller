# pkg/templating - Template Engine

Development context for the template engine library.

**API Documentation**: See `pkg/templating/README.md`
**Architecture**: See `/docs/controller/docs/development/design.md` (Template Engine section)

## Engine

The project uses Scriggo as its template engine. Scriggo provides Go template syntax with high performance.

## When to Work Here

Modify this package when:

- Adding custom template filters
- Improving template compilation performance
- Fixing template rendering bugs
- Enhancing error reporting

**DO NOT** modify this package for:

- Rendering coordination → Use `pkg/controller`
- Event handling → Use `pkg/events`
- HAProxy configuration → Use `pkg/dataplane`
- Kubernetes resources → Use `pkg/k8s`

## Key Design Principles

### Pure Library

This is a **pure library** with zero dependencies on other pkg/ packages. It could be extracted and used in any Go project needing templating.

Dependencies: Scriggo fork (from `gitlab.com/haproxy-haptic/scriggo`) and standard library.

### Resource-Agnostic Functions

**CRITICAL**: Template functions MUST be resource-agnostic. This is a generic templatable ingress controller where **no Kubernetes resource gets preferential treatment**.

**DO NOT add functions that:**

- Parse or navigate specific K8s resource structures (Service, Ingress, HTTPRoute, etc.)
- Provide shortcuts for specific resource fields (e.g., `lookup_port_name(service, port)`)
- Assume knowledge of any particular resource schema

**Instead:**

- Provide generic utilities: `dig()`, `fallback()`, `toSlice()`, `toint()`, etc.
- Let users write resource-specific logic as **template macros** in their libraries
- Keep the template engine completely ignorant of Kubernetes semantics

**Example of WRONG approach:**

```go
// DON'T: Resource-specific function in templating package
func scriggoLookupPortName(svc interface{}, portNumber int) string {
    // Navigates Service.spec.ports structure - WRONG!
    ports := scriggoDig(svc, "spec", "ports")
    // ...
}
```

**Example of CORRECT approach:**

```scriggo
{#- Users write resource-specific logic as macros in their library files -#}
{% macro LookupPortName(svc any, port int) string %}
  {%- var svcPorts = svc | dig("spec", "ports") | toSlice() -%}
  {%- for _, sp := range svcPorts -%}
    {%- if toint(sp | dig("port") | fallback(0)) == port -%}
      {{- sp | dig("name") | fallback("") -}}
    {%- end -%}
  {%- end -%}
{% end %}
```

This separation ensures the template engine remains generic while users can build any resource-specific functionality they need in their template libraries.

## Package Structure

```
pkg/templating/
├── engine_scriggo.go         # Scriggo engine core (constructors, Render, profiling)
├── engine_scriggo_tracing.go # Tracing and filter debug configuration
├── engine_scriggo_fs.go      # Virtual filesystem for template compilation
├── engine_interface.go    # Engine interface definition
├── filter_names.go        # Filter name constants
├── filters_scriggo.go     # Scriggo-specific filter implementations
├── types.go               # Type definitions (EngineType)
├── errors.go              # Custom error types
├── engine_scriggo_test.go # Scriggo engine unit tests
├── filters_scriggo_test.go # Scriggo filter tests
└── README.md              # User documentation
```

## Core Design

### Pre-compilation Strategy

Templates are compiled once at initialization for optimal runtime performance:

```go
// Compilation happens once (Scriggo is default)
engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
if err != nil {
    // Compilation errors caught early
    log.Fatal(err)
}

// Rendering is fast (microseconds)
for i := 0; i < 1000; i++ {
    output, err := engine.Render(ctx, "template", context)
}
```

**Benefits:**

- Syntax errors detected at startup (fail-fast)
- No runtime compilation overhead
- Thread-safe concurrent rendering
- Predictable performance

### Error Types

Four distinct error types for clear error handling:

```go
// CompilationError - template has syntax errors
type CompilationError struct {
    TemplateName    string
    TemplateSnippet string  // First 200 chars for context
    Cause           error
}

// RenderError - runtime rendering failed
type RenderError struct {
    TemplateName string
    Cause        error
}

// TemplateNotFoundError - template doesn't exist
type TemplateNotFoundError struct {
    TemplateName       string
    AvailableTemplates []string
}

// UnsupportedEngineError - invalid engine type
type UnsupportedEngineError struct {
    EngineType EngineType
}
```

## Scriggo Runtime Variables

Scriggo requires special handling for runtime variables (passed via `template.Run(vars)`).

### The Nil Pointer Pattern

**Problem:** If a variable is declared in globals with an initial value AND passed at runtime, Scriggo panics with "variable already initialized".

**Solution:** Declare runtime variables with **nil pointers** so Scriggo knows the TYPE at compile time, but the VALUE is provided at runtime:

```go
// In filters_scriggo.go buildScriggoGlobals()
decl["pathResolver"] = (*PathResolver)(nil)           // Not &PathResolver{}
decl["resources"] = (*map[string]ResourceStore)(nil)  // Not &map[string]{}
decl["templateSnippets"] = (*[]string)(nil)           // Not &[]string{}
```

**Why this works:**

- Compile time: Scriggo sees the TYPE from the nil pointer declaration
- Runtime: `template.Run(vars)` provides the actual VALUE
- No conflict because the compile-time declaration has no value

### Common Runtime Variables

| Variable | Type | Purpose |
|----------|------|---------|
| `pathResolver` | `*PathResolver` | File path resolution |
| `resources` | `*map[string]ResourceStore` | Kubernetes resources |
| `controller` | `*map[string]ResourceStore` | Controller state |
| `templateSnippets` | `*[]string` | Available snippet names |
| `fileRegistry` | `*FileRegistrar` | Dynamic file registration |
| `shared` | `*SharedContext` | Cross-template shared state |
| `dataplane` | `*map[string]interface{}` | Dataplane state |
| `capabilities` | `*map[string]interface{}` | Capability flags |
| `extraContext` | `*map[string]interface{}` | Custom template variables |
| `http` | `*HTTPFetcher` | HTTP store for fetching remote content |
| `runtimeEnvironment` | `*RuntimeEnvironment` | Runtime environment info |

### PathResolver and HAProxy Paths

PathResolver generates file paths for HAProxy auxiliary files (maps, SSL certs, error pages). The paths can be relative or absolute depending on configuration.

**CRITICAL**: For relative paths to work, the HAProxy config **MUST** include `default-path config` in the global section:

```haproxy
global
    default-path config
```

This directive tells HAProxy to resolve relative paths from the **config file's directory**, not from HAProxy's working directory. Without it, HAProxy cannot find files specified with relative paths.

**Path resolution modes:**

| Mode | PathResolver Config | Template Output | When Used |
|------|---------------------|-----------------|-----------|
| Relative | `MapsDir: "maps"` | `maps/host.map` | Local validation, production with `default-path config` |
| Absolute | `MapsDir: "/etc/haproxy/maps"` | `/etc/haproxy/maps/host.map` | Legacy or special cases |

**Template usage:**

```scriggo
{#- Using PathResolver.GetPath() #}
errorfile 400 {{ pathResolver.GetPath("400.http", "file") }}
{#- Output: files/400.http (relative) #}

use_backend %[path,map({{ pathResolver.GetPath("path.map", "map") }})]
{#- Output: maps/path.map (relative) #}
```

See `charts/CLAUDE.md` (HAProxy File Path Requirements) for full documentation.

### Adding New Runtime Variables

1. Declare with nil pointer in `buildScriggoGlobals()`:

   ```go
   decl["myVar"] = (*MyType)(nil)
   ```

2. Pass value at render time via context:

   ```go
   context["myVar"] = actualValue
   ```

### Calling Methods on Runtime Variables

**Methods work on runtime variables.** When a type is declared with a nil pointer, Scriggo knows the type's methods at compile time. At runtime, when the actual value is provided, those methods can be called:

```go
// In filters_scriggo.go - declare type (nil pointer)
decl["pathResolver"] = (*PathResolver)(nil)

// In templates - call methods directly
{{ pathResolver.GetPath("host.map", "map") }}
```

This works because:

1. Scriggo sees `*PathResolver` type at compile time
2. Scriggo validates that `GetPath` method exists on `*PathResolver`
3. At runtime, the actual `*PathResolver` value is provided via context
4. Method call executes on the runtime value

**No wrapper functions needed.** You can call methods directly on runtime variables. Wrapper functions are only needed when:

- You need access to `native.Env` for Scriggo-specific context
- The method signature doesn't fit Scriggo's calling conventions
- You want to provide a simpler API

**Example - direct method call vs wrapper:**

```go
// Direct method call - preferred, simpler
{{ pathResolver.GetPath("host.map", "map") }}

// Both produce identical results - use the direct method call
```

## Testing Approach

### Test Template Logic, Not Engine Syntax

Focus on testing the library API and error handling:

```go
func TestEngine_Render(t *testing.T) {
    tests := []struct {
        name     string
        template string
        context  map[string]interface{}
        want     string
        wantErr  bool
    }{
        {
            name:     "simple variable substitution",
            template: "Hello {{ name }}",
            context:  map[string]interface{}{"name": "World"},
            want:     "Hello World",
        },
        {
            name:     "missing variable with default",
            template: "Hello {{ name default \"Guest\" }}",
            context:  map[string]interface{}{},
            want:     "Hello Guest",
        },
        {
            name:     "complex context",
            template: "{% for _, item := range items %}{{ item }}{% end %}",
            context:  map[string]interface{}{"items": []string{"a", "b"}},
            want:     "ab",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            engine, err := templating.New(templating.EngineTypeScriggo,
                map[string]string{"test": tt.template}, nil, nil, nil)

            if tt.wantErr {
                require.Error(t, err)
                return
            }

            require.NoError(t, err)

            got, err := engine.Render(context.Background(), "test", tt.context)
            require.NoError(t, err)
            assert.Equal(t, tt.want, got)
        })
    }
}
```

### Test Error Handling

```go
func TestEngine_CompilationError(t *testing.T) {
    // Invalid template syntax
    templates := map[string]string{
        "invalid": "{% if true %}\n{% end extra text %}",
    }

    _, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)

    require.Error(t, err)

    var compErr *templating.CompilationError
    require.True(t, errors.As(err, &compErr))
    assert.Equal(t, "invalid", compErr.TemplateName)
    assert.NotEmpty(t, compErr.TemplateSnippet)
}

func TestEngine_TemplateNotFound(t *testing.T) {
    engine, _ := templating.New(templating.EngineTypeScriggo, map[string]string{
        "exists": "content",
    }, nil, nil, nil)

    _, err := engine.Render(context.Background(), "missing", nil)

    require.Error(t, err)

    var notFoundErr *templating.TemplateNotFoundError
    require.True(t, errors.As(err, &notFoundErr))
    assert.Equal(t, "missing", notFoundErr.TemplateName)
    assert.Contains(t, notFoundErr.AvailableTemplates, "exists")
}
```

## Common Pitfalls

### Recreating Engine on Every Render

**Problem**: Recompiling templates on every render.

```go
// Bad - compiles templates every time (milliseconds)
for _, context := range contexts {
    engine, _ := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
    output, _ := engine.Render(ctx, "template", context)
}
```

**Solution**: Create once, reuse for all renders.

```go
// Good - compile once, render many times (microseconds)
engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
if err != nil {
    log.Fatal(err)
}

for _, context := range contexts {
    output, _ := engine.Render(ctx, "template", context)
}
```

### Not Checking Compilation Errors

**Problem**: Syntax errors discovered at runtime in production.

```go
// Bad - ignoring compilation errors
engine, _ := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)

// Later in production...
output, err := engine.Render(ctx, "template", context)
// err: template doesn't exist (because compilation failed)
```

**Solution**: Always check compilation errors at startup.

```go
// Good - fail fast on invalid templates
engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
if err != nil {
    var compErr *templating.CompilationError
    if errors.As(err, &compErr) {
        log.Fatal("template compilation failed",
            "template", compErr.TemplateName,
            "error", compErr.Cause)
    }
    log.Fatal(err)
}
```

### Large Template Snippets in Errors

**Problem**: Logging 10KB template in error message.

```go
// Bad - logs entire template
log.Error("compilation failed", "template", templateContent)
```

**Solution**: CompilationError already includes snippet (first 50 chars).

```go
// Good - error contains context snippet
if err != nil {
    var compErr *templating.CompilationError
    if errors.As(err, &compErr) {
        log.Error("compilation failed",
            "template", compErr.TemplateName,
            "snippet", compErr.TemplateSnippet,  // First 200 chars
            "error", compErr.Cause)
    }
}
```

### Ignoring Thread Safety

**Problem**: Assuming Engine is not thread-safe.

```go
// Unnecessary - Engine is already thread-safe
var mu sync.Mutex

func render(engine templating.Engine, ctx map[string]interface{}) string {
    mu.Lock()
    defer mu.Unlock()
    output, _ := engine.Render(ctx, "template", ctx)
    return output
}
```

**Solution**: Use Engine concurrently without locking.

```go
// Good - no lock needed
func render(engine templating.Engine, ctx map[string]interface{}) string {
    output, _ := engine.Render(ctx, "template", ctx)
    return output
}

// Safe to call from multiple goroutines
var wg sync.WaitGroup
for i := 0; i < 100; i++ {
    wg.Add(1)
    go func() {
        defer wg.Done()
        render(engine, context)
    }()
}
wg.Wait()
```

## Template Tracing

The template engine provides execution tracing for observability and performance debugging.

### Enabling Tracing

```go
engine, _ := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)

// Enable tracing
engine.EnableTracing()

// Render templates (tracing happens automatically)
output, _ := engine.Render(ctx, "haproxy.cfg", context)

// Get trace output
trace := engine.GetTraceOutput()
fmt.Println(trace)

// Disable tracing
engine.DisableTracing()
```

### Trace Output

Tracing logs each template render with name and duration:

```
Rendering: haproxy.cfg
Completed: haproxy.cfg (0.007ms)
Rendering: backends.cfg
  Rendering: backend-snippet
  Completed: backend-snippet (0.003ms)
Completed: backends.cfg (0.012ms)
```

**Output format:**

- Indentation shows nesting depth (includes)
- Duration in milliseconds with 3 decimal places
- Start/complete pairs for each template

### Use Cases

**1. Performance debugging:**

```go
engine.EnableTracing()

// Render all templates
for name := range templates {
    engine.Render(ctx, name, context)
}

// Parse trace to find slow templates
trace := engine.GetTraceOutput()
for _, line := range strings.Split(trace, "\n") {
    if strings.Contains(line, "Completed") {
        // Extract duration and identify >10ms templates
    }
}
```

**2. Template execution verification:**

```go
// Ensure expected templates are executed
engine.EnableTracing()
engine.Render(ctx, "haproxy.cfg", context)
trace := engine.GetTraceOutput()

if !strings.Contains(trace, "Rendering: backends.cfg") {
    log.Warn("backends.cfg was not included")
}
```

**3. Integration with validation tests:**

```go
// In controller validate command
engine.EnableTracing()

runner := testrunner.New(config, engine, paths, options)
results, _ := runner.RunTests(ctx, "")

// Show trace after results
trace := engine.GetTraceOutput()
fmt.Println("\n=== Template Execution Trace ===")
fmt.Println(trace)
```

### Implementation Details

**Tracing configuration:**

```go
type scriggoTracingConfig struct {
    enabled      bool           // Tracing on/off (protected by mu)
    debugFilters bool           // Reserved for future filter debug logging
    mu           sync.RWMutex   // Protects enabled flag and traces slice
    traces       []string       // Accumulated trace outputs from all renders
}
```

**Thread-safety approach:**

- Per-render state (depth, builder) stored in execution context (isolated per `Render()` call)
- Shared state (enabled flag, traces slice) protected by mutex
- Tracing enabled/disabled flag snapshot taken at render start to avoid repeated locking
- Completed traces appended to shared slice under mutex lock

**Render method integration:**

```go
func (e *ScriggoEngine) Render(templateName string, context map[string]interface{}) (string, error) {
    // Take thread-safe snapshot of enabled flag
    e.tracing.mu.Lock()
    tracingEnabled := e.tracing.enabled
    e.tracing.mu.Unlock()

    // If tracing is enabled, attach per-render trace state to context
    var traceBuilder *strings.Builder
    if tracingEnabled {
        traceBuilder = &strings.Builder{}
        ctx.Set("_trace_depth", 0)
        ctx.Set("_trace_builder", traceBuilder)
        ctx.Set("_trace_enabled", true)

        e.tracef(ctx, "Rendering: %s", templateName)
        // Increment depth in context
        ctx.Set("_trace_depth", 1)

        startTime := time.Now()
        defer func() {
            duration := time.Since(startTime)
            // Decrement depth in context
            ctx.Set("_trace_depth", 0)
            e.tracef(ctx, "Completed: %s (%.3fms)", templateName,
                float64(duration.Microseconds())/1000.0)

            // Store completed trace thread-safely
            if traceBuilder != nil && traceBuilder.Len() > 0 {
                e.tracing.mu.Lock()
                e.tracing.traces = append(e.tracing.traces, traceBuilder.String())
                e.tracing.mu.Unlock()
            }
        }()
    }

    // Normal rendering logic
    return e.render(templateName, context)
}
```

**Key design decisions:**

- Per-render isolation: Each `Render()` call gets its own trace builder and depth counter in execution context
- No shared mutable state during render: Depth and builder are context-local, preventing race conditions
- Mutex-protected aggregation: Completed traces collected into shared slice under lock
- Single snapshot: Enabled flag read once per render, stored in context to avoid re-checking shared state
- Uses `defer` to ensure completion logging even on errors
- Thread-safe for concurrent renders with race detector validation

### Performance Overhead

Tracing overhead is minimal:

- Simple render: ~0.001-0.002ms overhead per template
- Complex render: <1% overhead
- String builder prevents repeated allocations
- Two mutex operations per render when enabled (snapshot flag, append trace)
- No contention during rendering (trace building happens in context-local storage)

**Recommendation**: Safe to leave enabled for debugging, but disable in performance-critical production paths.

### Filter Operations in Traces

Template tracing captures filter operations when enabled, showing the data flow through custom filters:

```go
engine.EnableTracing()
output, _ := engine.Render(ctx, "test", context)
trace := engine.GetTraceOutput()

// Example trace output:
// Rendering: test
//   Filter: sort_by([]interface {}, 3 items) [priority:desc]
//   Filter: glob_match([]interface {}, 5 items) [backend-*]
// Completed: test (0.012ms)
```

**Captured information:**

- Filter name (sort_by, glob_match, debug)
- Input type ([]interface {}, map[string]interface {})
- Item count for slice operations
- Filter parameters in brackets

**Use cases:**

- Understanding which filters are called and in what order
- Verifying filter parameters are evaluated correctly
- Debugging unexpected sorting results
- Performance analysis of filter operations

### Limitations

**Current limitations:**

1. No filtering by template name
2. Fixed output format (not configurable)
3. No automatic slow template warnings
4. Trace cleared on each `GetTraceOutput()` call

**Workarounds:**

- Filter trace output in calling code
- Parse trace to generate custom reports
- Set thresholds in analysis code
- Copy trace before clearing if needed

### Testing Tracing

**Basic tracing test:**

```go
func TestEngine_Tracing(t *testing.T) {
    templates := map[string]string{
        "main": "{{ render \"sub\" }}",
        "sub":  "content",
    }

    engine, _ := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
    engine.EnableTracing()

    _, err := engine.Render(context.Background(), "main", nil)
    require.NoError(t, err)

    trace := engine.GetTraceOutput()

    // Verify trace contains expected entries
    assert.Contains(t, trace, "Rendering: main")
    assert.Contains(t, trace, "Rendering: sub")
    assert.Contains(t, trace, "Completed: main")
    assert.Contains(t, trace, "Completed: sub")

    // Verify nesting (sub should be indented)
    assert.Contains(t, trace, "  Rendering: sub")
}
```

**Concurrent tracing test (race detector validation):**

```go
func TestTracing_ConcurrentRenders(t *testing.T) {
    templates := map[string]string{
        "template1": `Result: {{ value }}`,
        "template2": `Output: {{ toUpper(value) }}`,
    }

    engine, _ := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
    engine.EnableTracing()

    // Run concurrent renders
    const numGoroutines = 10
    done := make(chan bool, numGoroutines)

    for i := 0; i < numGoroutines; i++ {
        go func(id int) {
            defer func() { done <- true }()

            for j := 0; j < 5; j++ {
                tmpl := fmt.Sprintf("template%d", (j%2)+1)
                output, err := engine.Render(context.Background(), tmpl, map[string]interface{}{
                    "value": fmt.Sprintf("goroutine-%d", id),
                })
                assert.NoError(t, err)
                assert.NotEmpty(t, output)
            }
        }(i)
    }

    // Wait for all goroutines to complete
    for i := 0; i < numGoroutines; i++ {
        <-done
    }

    // Verify trace contains entries from all renders
    trace := engine.GetTraceOutput()
    assert.NotEmpty(t, trace)
    assert.Contains(t, trace, "Rendering: template1")
    assert.Contains(t, trace, "Rendering: template2")

    // Run with race detector: go test -race
}
```

## Filter Debug Logging

The template engine provides detailed debug logging for filter operations, particularly useful for debugging sort_by comparisons.

### Enabling Filter Debug

```go
engine, _ := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)

// Enable filter debug logging
engine.EnableFilterDebug()

// Render templates (debug logs appear in slog output)
output, _ := engine.Render(ctx, "test", context)

// Disable filter debug logging
engine.DisableFilterDebug()
```

### Debug Output

When enabled, sort_by filter logs each comparison with structured logging:

```
INFO SORT comparison criterion=$.priority:desc valA=10 valA_type=int valB=5 valB_type=int result=1
INFO SORT comparison criterion=$.priority:desc valA=5 valA_type=int valB=1 valB_type=int result=1
```

**Logged fields:**

- `criterion`: The sort expression being evaluated
- `valA`: Value extracted from first item
- `valA_type`: Go type of first value
- `valB`: Value extracted from second item
- `valB_type`: Go type of second value
- `result`: Comparison result (-1, 0, 1)

### CLI Integration

The `controller validate` command supports `--debug-filters` flag:

```bash
# Enable filter debug logging during validation tests
./bin/haptic-controller validate --test test-gateway-route-precedence --debug-filters

# Combine with other debugging flags
./bin/haptic-controller validate --debug-filters --trace-templates --dump-rendered
```

**How it works:**

- Flag passed from CLI → testrunner.Options → testrunner.Runner
- Runner creates worker engines with `EnableFilterDebug()` called
- Each worker engine independently logs filter operations
- Useful for understanding why routes are sorted in a particular order

### Use Cases

**1. Debugging sort_by precedence:**

```go
// Template
routes | sort_by(["$.match.method:exists:desc", "$.match.headers | length:desc"])

// Enable debug to see:
// - Which routes have method field (exists check)
// - Header count for each route
// - Comparison order and results
```

**2. Understanding mixed-type sorting:**

```go
// When sorting items with different value types
items | sort_by(["$.value"])

// Debug shows actual types being compared:
// valA=string valA_type=string
// valB=123 valB_type=int
```

**3. Verifying expression evaluation:**

```go
// Complex expressions with multiple criteria
items | sort_by(["$.type", "$.priority:desc", "$.name"])

// Debug shows evaluation of each criterion
```

### Implementation Notes

- Debug logging uses Go's `log/slog` package
- Logging only happens when `EnableFilterDebug()` is active
- Thread-safe: Multiple concurrent renders each log independently
- Zero overhead when disabled (simple boolean check)
- Only sort_by filter currently logs (most common debugging need)

### Performance Impact

Filter debug logging is lightweight:

- Single boolean check per comparison
- Structured logging (slog) is efficient
- No string formatting when disabled
- Typical overhead: <5% for sort-heavy templates

**Recommendation**: Safe to enable during development and validation testing. Disable in production unless investigating specific issues.

## Debug Filters

The template engine provides the `debug` filter for debugging templates by inspecting variable contents.

### debug Filter

Dumps variable structure as JSON-formatted HAProxy comments:

```jinja2
{%- set routes = [
    {"name": "api", "priority": 10},
    {"name": "web", "priority": 5}
] %}

{{- routes | debug }}

{# Output:
# DEBUG:
# [
#   {
#     "name": "api",
#     "priority": 10
#   },
#   {
#     "name": "web",
#     "priority": 5
#   }
# ]
#}
```

**With label:**

```jinja2
{{ routes | debug("sorted-routes") }}

{# Output:
# DEBUG sorted-routes:
# [...]
#}
```

**Use cases:**

- Inspecting variable structure during template development
- Verifying filter transformations (before/after)
- Understanding data passed to templates
- Safe output (formatted as comments, won't break HAProxy config)

**Features:**

- JSON formatted with indentation
- Prefixed with `#` (HAProxy comment)
- Optional label for identifying debug output
- Works with any data type (objects, arrays, strings, numbers)

### Example Usage

```jinja2
{%- set items = [...] %}

{# Before sorting #}
{{ items | debug("unsorted") }}

{# After sorting #}
{%- set sorted = items | sort_by(["$.priority:desc"]) %}
{{ sorted | debug("sorted") }}
```

### Implementation Notes

- Uses `json.MarshalIndent()` for formatting
- Adds `#` prefix to every line
- Falls back to `fmt.Sprintf("%v")` if JSON marshaling fails
- Returns string (can be used in {{ }} expressions)

### Performance Impact

The debug filter is intended for debugging only:

- JSON marshaling overhead (skip in production templates)
- No impact on other filters or rendering when not used
- Safe to leave in templates during development (just comment out)

**Recommendation**: Use during template development and testing. Remove or comment out before production deployment.

### Testing Debug Filter

```go
func TestDebugFilter(t *testing.T) {
    templates := map[string]string{
        "test": `{{ debug(item, "test-item") }}`,
    }

    engine, _ := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
    output, _ := engine.Render(context.Background(), "test", map[string]interface{}{
        "item": map[string]interface{}{"key": "value"},
    })

    assert.Contains(t, output, "# DEBUG test-item:")
    assert.Contains(t, output, `"key": "value"`)
}
```

## Caching Expensive Computations

Use `shared.ComputeIfAbsent()` for compute-once patterns:

```go
{%- var analysis, _ = shared.ComputeIfAbsent("analysis", func() interface{} {
    return analyzeRoutes(resources)
}) -%}
```

`ComputeIfAbsent` is thread-safe and guarantees exactly-once computation. Use `shared.Get(key)` for read-only access to previously computed values.

**Impact:** Reduces expensive computation calls from N to 1 per render.

## Performance Optimization

### Benchmarking

```go
func BenchmarkEngine_Render(b *testing.B) {
    templates := map[string]string{
        "simple": "Hello {{ name }}!",
        "loop": `{% for _, i := range items %}{{ i }}{% end %}`,
        "complex": `
            {% for _, user := range users %}
                Name: {{ user.name }}
                Email: {{ toLower(user.email) }}
                {% if user.admin %}Admin{% end %}
            {% end %}
        `,
    }

    engine, _ := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)

    contexts := map[string]map[string]interface{}{
        "simple": {"name": "World"},
        "loop": {"items": []int{1, 2, 3, 4, 5}},
        "complex": {
            "users": []map[string]interface{}{
                {"name": "Alice", "email": "ALICE@EXAMPLE.COM", "admin": true},
                {"name": "Bob", "email": "BOB@EXAMPLE.COM", "admin": false},
            },
        },
    }

    for name, ctx := range contexts {
        b.Run(name, func(b *testing.B) {
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                engine.Render(ctx, name, ctx)
            }
        })
    }
}
```

**Expected results:**

- Simple: ~20-50µs per render
- Loop: ~50-100µs per render
- Complex: ~100-200µs per render

### Memory Optimization

```go
// Bad - allocates new map for each render
for i := 0; i < 1000; i++ {
    ctx := map[string]interface{}{
        "name": users[i].Name,
    }
    engine.Render(ctx, "template", ctx)
}

// Good - reuse context map
ctx := make(map[string]interface{})
for i := 0; i < 1000; i++ {
    ctx["name"] = users[i].Name
    engine.Render(ctx, "template", ctx)
}
```

### Template Size

```go
// Bad - one huge template (slow compilation, poor error messages)
templates := map[string]string{
    "haproxy.cfg": `
        global
            daemon
        defaults
            mode http
        {{ 5000 lines of template }}
    `,
}

// Good - break into logical pieces
templates := map[string]string{
    "haproxy.cfg": `
        {{ render "global" }}
        {{ render "defaults" }}
        {{ render "frontends" }}
        {{ render "backends" }}
    `,
    "global": "global\n    daemon",
    "defaults": "defaults\n    mode http",
    "frontends": "...",
    "backends": "...",
}
```

## Troubleshooting

### Template Not Found

**Diagnosis:**

1. Check template name exactly matches
2. List available templates
3. Verify templates were provided at initialization

```go
if !engine.HasTemplate(templateName) {
    available := engine.TemplateNames()
    log.Error("template not found",
        "requested", templateName,
        "available", available)
}
```

### Rendering Produces Empty Output

**Diagnosis:**

1. Check for silent errors in template (use `{% if var != nil %}`)
2. Verify context contains expected data
3. Check for whitespace stripping (`{%-` and `-%}`)

```go
// Debug context
log.Info("rendering template",
    "template", templateName,
    "context_keys", getKeys(context))

output, err := engine.Render(ctx, templateName, context)

log.Info("render result",
    "output_length", len(output),
    "output_preview", output[:min(100, len(output))])
```

### High Memory Usage

**Diagnosis:**

1. Check template count and size
2. Review context object sizes
3. Look for memory leaks in custom filters

```go
// Monitor engine memory
var m runtime.MemStats
runtime.ReadMemStats(&m)

log.Info("template engine memory",
    "template_count", engine.TemplateCount(),
    "alloc_mb", m.Alloc/1024/1024,
    "total_alloc_mb", m.TotalAlloc/1024/1024)
```

### Slow Rendering

**Diagnosis:**

1. Benchmark specific template
2. Check for expensive operations in context preparation
3. Review loop complexity in templates

```go
// Profile template rendering
start := time.Now()
output, err := engine.Render(ctx, templateName, context)
duration := time.Since(start)

if duration > 100*time.Millisecond {
    log.Warn("slow template render",
        "template", templateName,
        "duration_ms", duration.Milliseconds(),
        "output_size", len(output))
}
```

## Extension Considerations

### Scriggo Fork

This project uses a forked version of Scriggo. The fork adds:

1. **Native `{% include %}` statement**: Syntax for static includes:

   ```go
   {% include "partial.txt" %}  {# Compile-time include #}
   ```

2. **Bug fixes and compatibility improvements** for use with this template engine.

The fork is available on GitLab and configured via a replace directive in go.mod:

```go
replace gitlab.com/haproxy-haptic/scriggo => gitlab.com/haproxy-haptic/scriggo v0.0.0-20251212162249-9274cec0fd7b
```

### Include: Static vs Dynamic

The Scriggo engine provides two ways to include templates:

| Pattern | Syntax | Evaluation | Use Case |
|---------|--------|------------|----------|
| Static render | `{{ render "literal/path" }}` | Runtime | Fixed template paths |
| Dynamic render | `{{ render(variable) }}` | Runtime | Computed template names |
| Glob render | `{{ render_glob "pattern-*" }}` | Runtime | Pattern-matched templates |

**Static renders** (`render` function with string literal):

- Path is a string literal
- Template is resolved at runtime

```go
{{ render "header" }}      {# Valid - literal path #}
{{ render "partials/footer" }} {# Valid - literal path #}
```

**Dynamic renders** (`render` function with variable):

- Path can be a variable or expression
- Template is resolved at runtime
- Required for computed template names

```go
{% for _, name := range glob_match(templateSnippets, "backend-*") %}
  {{ render(name) }}  {# Dynamic - name is a variable #}
{% end %}
```

**Glob renders** (`render_glob` function):

- Pattern-based template inclusion
- Matches and renders all templates matching the glob pattern

```go
{{ render_glob "backend-*" }}  {# Renders all backend-* templates #}
```

### Scriggo Template Syntax

Scriggo uses Go template syntax:

- Loops: `{% for x := range items %}...{% end %}`
- Conditionals: `{% if cond %}...{% end %}`
- Variables: `{{ .name }}` with leading dot
- **Imports**: `{% import "template" for FuncName %}`

**Pipe operator**: Our Scriggo fork supports pipe syntax for filters. The pipe operator passes the left-hand expression as the first argument to the function on the right:

   ```go
   {{ user.name | uppercase() }}        {# Same as: uppercase(user.name) #}
   {{ items | join(", ") }}             {# Same as: join(items, ", ") #}
   {{ value | fallback("N/A") }}        {# Same as: fallback(value, "N/A") #}
   {{ price | format("%.2f") | prefix("$") }}  {# Chaining supported #}
   ```

   Note: The fallback function is named `fallback` (not `default`) because `default` is a reserved Go keyword.

Both function-call and pipe syntax work:

- Pipe syntax: `{{ value | strip() }}`
- Function syntax: `{{ strip(value) }}`

### Filter and Function Name Constants

Filter and function names are defined as constants in `filter_names.go`:

```go
// Filters
const (
    FilterSortBy    = "sort_by"
    FilterGlobMatch = "glob_match"
    FilterStrip     = "strip"
    FilterTrim      = "trim"
    FilterB64Decode = "b64decode"
    FilterDebug     = "debug"
    FilterIndent    = "indent"
)

// Functions (subset - see filter_names.go for full list)
const (
    FuncFail          = "fail"
    FuncMerge         = "merge"
    FuncKeys          = "keys"
    FuncCoalesce      = "coalesce"
    FuncFallback      = "fallback"
    FuncDig           = "dig"
    FuncToSlice       = "toSlice"
    FuncSanitizeRegex = "sanitize_regex"
    // ... 30+ more functions
)
```

### Scriggo Functions

Scriggo provides functions designed to support idiomatic Go patterns:

#### Dict Operations

**`merge(dict, updates)`** - Merges two maps, returning a new map:

```go
{% var config = map[string]interface{}{"a": 1, "b": 2} %}
{% config = merge(config, map[string]interface{}{"b": 3, "c": 4}) %}
{# Result: {"a": 1, "b": 3, "c": 4} #}
```

**`keys(dict)`** - Returns sorted keys from a map:

```go
{% var config = map[string]interface{}{"c": 3, "a": 1, "b": 2} %}
{% for _, key := range keys(config) %}
{{ key }}: {{ config[key] }}
{% end %}
{# Output: a: 1, b: 2, c: 3 (sorted) #}
```

#### Caching with SharedContext

Use `shared.ComputeIfAbsent()` for compute-once patterns in templates:

```go
{%- var analysis, _ = shared.ComputeIfAbsent("analysis", func() interface{} {
    return analyzeRoutes(resources)
}) -%}
```

`ComputeIfAbsent` is thread-safe and guarantees exactly-once computation. Use `shared.Get(key)` for read-only access to previously computed values.

**Cache Characteristics:**

- Cache is per-render (isolated between `Render()` calls)
- Supports any value type (maps, slices, structs)
- Thread-safe for concurrent template sections

### Type Assertions

In Scriggo templates, type assertions are needed when working with `interface{}` (any) values in contexts that require concrete types.

**When type assertions ARE required:**

1. **Ranging over `coalesce()` results** - `coalesce()` returns `interface{}`:

   ```go
   {%- for _, item := range coalesce(map["key"], []any{}).([]any) -%}
   ```

2. **Accessing fields on map values** - map indexing returns `interface{}`:

   ```go
   {%- var data = secret.(map[string]any)["data"].(map[string]any) -%}
   ```

3. **String operations on interface values**:

   ```go
   {%- var name = item["name"].(string) + "_suffix" -%}
   ```

**When type assertions are NOT required:**

1. **Using typed ResourceStore methods** - methods like `Fetch()` return concrete types:

   ```go
   {%- for _, ep := range resources.endpoints.Fetch(svcName) -%}
   ```

   `Fetch()` returns `[]interface{}`, a concrete slice type.

2. **Simple variable access** - variables already have their type:

   ```go
   {{ httpsPort default 8443 }}
   ```

3. **Template output** - `{{ }}` handles any type:

   ```go
   {{ item["name"] }}  {# No assertion needed for output #}
   ```

**Rule of thumb:**

- Check function return types in `filters_scriggo.go`
- If a function returns `interface{}`, assertion is needed for operations
- If a function returns a concrete type like `[]interface{}`, no assertion needed

### Engine Interface Requirements

Engines must:

- Implement the `Engine` interface in `engine_interface.go`
- Support pre-compilation (compile at init, not at render time)
- Be thread-safe for concurrent rendering
- Use filter name constants from `filter_names.go`
- Error types must use existing error types (`CompilationError`, `RenderError`, etc.)

## Resources

- API documentation: `pkg/templating/README.md`
- Scriggo documentation: <https://scriggo.com/templates>
- Scriggo fork: `gitlab.com/haproxy-haptic/scriggo` (fork with `{% include %}` statement)
- Scriggo upstream documentation: <https://scriggo.com/templates>
- Jinja2 template documentation: <https://jinja.palletsprojects.com/>
- HAProxy template examples: `/examples/templates/`
