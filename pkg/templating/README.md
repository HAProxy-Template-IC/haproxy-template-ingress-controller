# pkg/templating

Pure template rendering library. Wraps a fork of [Scriggo](https://scriggo.com/) with a pre-compile-then-render lifecycle, HAProxy-specific filters and context helpers, and structured errors. Zero dependencies on other `pkg/` packages — this is a reusable library.

Module path: `gitlab.com/haproxy-haptic/haptic`. The source is authoritative (`go doc ./pkg/templating`); this README is a short orientation. `docs/controller/docs/templating.md` covers the template *author's* side (syntax, filters, custom variables) — this page is for Go callers.

## Minimal Usage

```go
import (
    "context"
    "log"

    "gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

templates := map[string]string{
    "greeting": "Hello {{ name }}!",
    "config":   "server {{ host }}:{{ port }}",
}

engine, err := templating.New(templates, nil, nil, nil)
if err != nil {
    log.Fatal(err)   // compilation errors surface here, fail fast
}

out, err := engine.Render(context.Background(), "greeting",
    map[string]any{"name": "World"})
```

The engine is safe for concurrent use — compile once at startup, render concurrently from many goroutines.

## `New` Signature

```go
func New(
    templates map[string]string,
    customFilters map[string]FilterFunc,
    customFunctions map[string]GlobalFunc,
    postProcessorConfigs map[string][]PostProcessorConfig,
) (Engine, error)
```

`New` compiles every template as an entry point; use `NewScriggo` for explicit entry points or `NewScriggoWithDeclarations` for domain-specific type declarations. `customFilters` plug into the pipe syntax (`{{ value | myFilter }}`), `customFunctions` into the call syntax (`{{ myFunc(value) }}`), and `postProcessorConfigs` chain per-template transformations (regex replace, or a Scriggo template whose `input` variable is the previously rendered output). Passing `nil` for any of the three is fine.

## Engine Interface (Highlights)

```go
type Engine interface {
    Render(ctx context.Context, templateName string, templateContext map[string]any) (string, error)

    HasTemplate(name string) bool
    TemplateNames() []string
    TemplateCount() int
    GetRawTemplate(name string) (string, error)

    EnableTracing()
    DisableTracing()
    GetTraceOutput() string

    EnableFilterDebug()
    DisableFilterDebug()
}
```

`ctx` controls rendering timeouts (`RenderTimeoutError` is returned on cancellation). Tracing produces a nested indented trace of every `render` / `render_glob` call; filter debug logs `sort_by` comparisons via `log/slog` at INFO level. Both are off by default and have negligible overhead when disabled — they're wired up to the `--trace-templates` and `--debug-filters` flags on `haptic-controller validate`.

## Error Types

```go
var compErr *templating.CompilationError     // syntax error; has TemplateName + first 200 chars via .TemplateSnippet
var renderErr *templating.RenderError        // runtime failure during Render
var timeoutErr *templating.RenderTimeoutError // ctx deadline exceeded
var notFoundErr *templating.TemplateNotFoundError // unknown name; has .AvailableTemplates
```

Always check with `errors.As`; the wrapped `.Cause` carries the underlying Scriggo diagnostic.

## What Ships Inside

### Filters (pipe syntax, `{{ v | filter(args) }}`)

`b64decode`, `glob_match`, `group_by`, `indent`, `sort_by` (supports `:desc`, `:exists`, `| length` modifiers), `debug`, `toJSON`, `strip`/`trim`, `to_str_map` (normalises any string-keyed map — typed `map[string]string`, untyped `map[string]any`, or generic `map[string]<T>` — into `map[string]string` for uniform iteration over labels / matchLabels / annotations).

### Functions (call syntax, `{{ fn(args) }}`)

Selection: `fallback`, `coalesce`, `fail`, `merge`, `keys`, `sort_strings`, `sanitize_regex`, `semver_gte`, `toLower`, `tostring`, `dig` (navigates nested maps **and** typed structs via JSON-tag → Go-field lookup), `shard_slice` (type-preserving slice shard via a `native.AdaptiveFunc` — return type at each call site matches the input element type), plus Scriggo's standard library.

Canonical reference: `pkg/templating/filter_names.go`.

### Runtime Context Variables

Scriggo needs to know the *type* of each runtime variable at compile time even though values arrive at `Render`. The library declares these with nil-pointer typedefs in `buildScriggoGlobals` — the `(*T)(nil)` pattern — so callers just pass values in the render context:

| Variable | Type | Purpose |
|----------|------|---------|
| `resources` | `*map[string]ResourceStore` | Watched Kubernetes resources (`.List`, `.Fetch`, `.GetSingle`); when a schema is loaded for an entry, the wrapper returns typed pointers (`[]*resources.<name>.T` / `*resources.<name>.T`) |
| `controller` | `*map[string]ResourceStore` | Controller-managed stores; currently `controller["haproxy_pods"]` only |
| `pathResolver` | `*PathResolver` | `pathResolver.GetPath(name, kind)` for map / SSL / file / crt-list paths |
| `fileRegistry` | `*FileRegistrar` | Templates can register dynamically generated auxiliary files via this |
| `templateSnippets` | `*[]string` | Names of available snippets; useful with `render_glob` |
| `shared` | `*SharedContext` | Per-render cache; `shared.ComputeIfAbsent(key, fn)` memoises expensive work |
| `dataplane` | `*map[string]any` | The CRD's `spec.dataplane` block — port, timeouts, paths |
| `capabilities` | `*map[string]any` | HAProxy feature flags derived from the local HAProxy version |
| `http` | `*HTTPFetcher` | `http.Fetch(url, opts)` for HTTP resources |
| `runtimeEnvironment` | `*RuntimeEnvironment` | Runtime info (`GOMAXPROCS`, etc.) |
| `extraContext` | `*map[string]any` | User-defined variables from `templatingSettings.extraContext` |
| typed-resource globals | `*[]*resources.<name>.T` | One per `watchedResources` entry when a schema is loaded — same name as the watched-resource key (e.g. `gateways`, `httproutes`). The `resources.<name>.T` selector chain is also a usable type expression in macro signatures, type assertions, and type-switch case clauses. |

Callers can inject additional per-render declarations through `templating.NewScriggoWithDeclarations` — for example, the renderer and template validator both add `currentConfig` (`*parserconfig.StructuredConfig`, nil on first deployment) so slot-preserving templates can guard with `{% if !isNil(currentConfig) %}`. The typegen-derived typed globals are injected via the same mechanism — `pkg/k8s/typegen` builds the `reflect.Type` declarations the engine merges in before compile.

To add a new runtime variable, declare it in `buildScriggoGlobals` with a nil pointer of the right type, then pass the value via the render context map — there's a walkthrough in `pkg/templating/CLAUDE.md`.

## Post-Processing

After rendering, a template can pass through a chain of post-processors — useful for fixing up indentation or running a second Scriggo pass with access to the first pass's output:

```yaml
postProcessing:
  - type: regex_replace
    params:
      pattern: "^[ ]+"
      replace: "  "
  - type: template
    params:
      source: |
        {%- if strings_contains(input, "__PLACEHOLDER__") -%}
        {{ replace(input, "__PLACEHOLDER__", "computed") }}
        {%- else -%}
        {{ input }}
        {%- end -%}
```

The `template` post-processor compiles at engine init (so syntax errors fail fast) and receives the rendered output as `input`.

## Design Rule: Resource-Agnostic

This package intentionally does **not** understand Kubernetes resources. There is no `lookup_service_port`, no `is_ingress`, no Gateway API helpers — those would turn the template engine into a policy layer for specific resource shapes. Users write resource-specific logic as Scriggo macros inside their own template libraries, and the engine stays generic enough to template anything. If you find yourself wanting to add a function that navigates a specific resource's fields, write a macro instead.

This is also why there is no `renderResource()` template function. Earlier versions of the codebase shipped one — an imperative collector populated as a side effect of rendering — but it has been removed. Resource emission is now a top-level CR concern: callers declare templates under `spec.k8sResources` (sibling of `templateSnippets`, `maps`, `files`, `sslCertificates`), the renderer renders each one, parses the rendered YAML (multi-doc supported via `---`), and registers the resulting `*RenderedResourceCollector` as a synchronous accumulator on the `RenderResult`. Downstream consumers (the controller's resourceapplier) read that slice off `RenderResult.RenderedResources`. The collector type still exists as the consumer-facing accumulator, but it is no longer fed by templates calling a side-effecting filter.

## Scriggo Fork

The engine depends on a forked Scriggo (`gitlab.com/haproxy-haptic/scriggo`) consumed as a normal `require` in `go.mod` — there is no `replace` directive. The pinned pseudo-version drifts as Renovate updates the dep; check `grep gitlab.com/haproxy-haptic/scriggo go.mod` for the live value rather than copying one out of this README. The fork adds:

- A native `{% include "..." %}` statement for compile-time includes.
- A `callNative` fast path that eliminates `reflect.Value.Call` for the haptic function signatures — the hot render loop is effectively zero-allocation after warm-up.
- Nil-safety fixes around `reflect.Value.Interface()` for dynamic includes.

Nothing in here expects vanilla Scriggo; don't swap the dep for upstream without running the template benchmarks.

## Testing

```bash
go test ./pkg/templating/...          # unit tests
go test ./pkg/templating/... -race    # race detector (engine is concurrent-safe)
go test ./pkg/templating/... -bench=. # benchmarks
```

The benchmarks in `benchmark_pool_test.go` and `benchmark_test.go` are the authoritative numbers; don't trust ad-hoc "~X µs per render" claims in prose documentation.

## See Also

- `pkg/templating/CLAUDE.md` — runtime-variable pattern, adding new filters, Scriggo fork notes
- `docs/controller/docs/templating.md` — template-author reference (syntax, filters, context variables)
- `pkg/controller/rendercontext` — builds the render context from watched stores and HTTP resources
- `pkg/controller/renderer` — event adapter that wires this engine into the reconciliation pipeline
- [Scriggo Templates](https://scriggo.com/templates) — base syntax reference

## License

Apache-2.0 — see root `LICENSE`.
