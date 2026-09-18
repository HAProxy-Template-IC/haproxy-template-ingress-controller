# Scriggo customization

## Language boundaries

Scriggo uses Go expressions inside `{% ... %}` statements and `{{ ... }}` output.
It isn't Go `text/template`, Helm's template language, or Jinja. Use `:=`, Go
comparisons, and `for _, item := range items`; don't invent Sprig functions or
Jinja filters. HAPTIC's helpers and `render_glob` extend Scriggo; check the
version's `template-reference/index.md` before choosing a helper.

```scriggo
{% for _, svc := range resources.services.List() %}
backend {{ svc.metadata.namespace }}_{{ svc.metadata.name }}
{% end %}
```

That fragment illustrates typed iteration; it isn't a complete routing setup.
Service backends also need port and EndpointSlice resolution. Reuse the bundled
backend library when extending its routing behavior.

`{% ... %}` runs statements, `{{ ... }}` emits text, and `{# ... #}` is a comment.
Whitespace trim markers (`{%-`, `-%}`) can join HAProxy directives accidentally;
inspect the rendered lines. Avoid Go keywords as identifiers.

`{{ render "name" }}` renders a snippet. `{{ render_glob "prefix-*" }}` renders
matching snippets in alphabetical order. Locals cross a render boundary only
when the caller uses `inherit_context`. Check the caller before using names such
as `ingress`, `backend`, or `route`; they aren't universal globals. Reusable
parameterized logic can use imported macros with explicit arguments.

## Extend the bundled chart

Put snippets under `controller.config.templateSnippets` in Helm values; each entry
has a `template` string. In a standalone `HAProxyTemplateConfig`, the same fields
live under `spec.templateSnippets`. Helm values are not input to `haptic validate`.

Read `libraries/base/index.md` for the current extension registry and
`template-libraries/index.md` for composition rules. Common hooks include:

| Prefix | Output location |
| --- | --- |
| `global-settings-*` | HAProxy `global` section |
| `defaults-settings-*` | HAProxy `defaults` section |
| `frontend-extra-*` | Early frontend directives |
| `frontend-filters-*` | Frontend request/response processing |
| `backend-directives-*` | Directives for a library-generated backend |
| `backends-*` | Complete additional backends |

Use a unique name such as `frontend-extra-400-platform-header`. Numeric segments
sort lexically, so use a consistent width. A same-name snippet overrides the
earlier definition, which can remove behavior or validation.

The [header values example](../assets/header-values.yaml) adds a response header
without replacing routing. Per-test `extraContext` pins the setting each assertion
depends on. Empty disables this example's header; malformed values fail rendering.
Frontend hooks run only when a routing library declares a listener. Include a
route fixture when testing a frontend snippet; an empty resource set won't
exercise it.

For a per-Ingress annotation, use a backend hook that receives that Ingress and
guard it for non-Ingress callers. A frontend directive generally affects every
route in that frontend unless it has an explicit matching condition. Confirm
scope with fixtures for both annotated and unannotated resources.

## Values and output

Read operator settings through `extraContext`, supplied by
`templatingSettings.extraContext`. `fallback(value, default)` replaces nil, not
an empty string, false, or zero. `dig()` normalizes optional typed zero values to
nil; direct typed access preserves those values. Choose defaults deliberately.

Validate externally supplied strings before inserting them into HAProxy syntax.
A newline, quote, or delimiter can create a different directive. Use a narrow
accepted format and `fail()` with a concrete fix. Keep the assertion on the
rendered behavior, including a negative test for the rejection.

Resolve auxiliary paths with `pathResolver.GetPath(filename, type)` or the path
returned by `fileRegistry.Register(...)`. Don't hardcode storage directories.
Use `toJSON()` to display composite values. Keep generated ordering deterministic.

Before promising runtime updates, consult `supported-configuration/index.md`
and inspect `haptic diff` against the relevant pod. Changes to backend profiles
or listeners can require a reload even when other changes use runtime commands.
