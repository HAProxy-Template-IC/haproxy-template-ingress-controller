# Template syntax

Learn the syntax with small examples you can edit in your browser. These
examples produce text and don't connect to a cluster. For applying a template to
your installation, follow [Write your first template](templating.md).

Start with a small edit. In this template, `{% var … %}` defines a value and
`{{ … }}` writes it into the output:

<div class="pg-embed" markdown data-scriggo data-title="Change a value in a template">

```scriggo
{% var maxConnections = 1000 %}
global
    maxconn {{ maxConnections }}
```

<p class="pg-task" markdown>Change `maxConnections` from `1000` to `2000`. The output updates to `maxconn 2000`. This is a configuration fragment; later examples generate a complete file.</p>

</div>

Templates combine literal text with expressions, loops, and conditions.
`{{ expression }}` writes a value; `{% statement %}` runs a statement.
The syntax follows the [Scriggo template language](https://scriggo.com/templates).

## Control structures

The loop emits a backend for each environment. The condition gives production
more connection slots:

<div class="pg-embed" markdown data-scriggo data-title="Use a loop and a condition">

```scriggo
{% var environments = []string{"production", "staging"} %}
{% for _, environment := range environments %}
backend {{ environment }}
  {% if environment == "production" %}
  fullconn 2000
  {% else %}
  fullconn 500
  {% end %}
{% end %}
```

<p class="pg-task" markdown>Add another environment to the list and find its backend in the output.</p>

</div>

`{# … #}` adds a template comment that doesn't appear in the output.
Avoid reserved words such as `type`, `range`, and `default` as variable names;
use names such as `resourceType`, `items`, and `defaultPort` instead.

## Helper functions

HAPTIC provides helpers for resource and configuration templates: nil-safe navigation (`dig`, `fallback`, `toSlice`), string and map utilities, deduplication (`first_seen`), sorting (`sort_by`), and version gates (`semver_gte`). The [Template Reference](./template-reference.md#functions-and-filters) lists every function with its calling styles and an example each.

Try the helpers in a template and inspect the text they produce:

<div class="pg-embed" markdown data-scriggo data-title="Try template functions" data-height="360">

```go
{# Every helper from the Template Reference is available here. Edit freely. #}
{%- var envs = []any{"prod", "dev", "staging"} %}
{%- var sorted = envs | sort_by([]string{"$"}) %}
{%- for _, e := range sorted %}
backend {{ e }}
  server app {{ toLower(tostring(e)) }}.svc:80
{%- end %}
```

</div>

`sort_by([]string{"$.weight:desc", "$.name"})` sorts by weight descending,
then by name when weights match. Try that two-key sort:

<div class="pg-embed" markdown data-scriggo data-title="Challenge: sort by two keys" data-difficulty="2" data-height="380">

<p class="pg-task" markdown>List the backends heaviest-first, breaking ties by name. Fix the `sorted` line with `sort_by` and check the output order.</p>

```go
{# Challenge: list the backends heaviest-first, ties broken by name.
   sort_by(items, criteria) sorts a []any by criteria like "$.field:desc". #}
{%- var backends = []any{
    map[string]any{"name": "web", "weight": 10},
    map[string]any{"name": "api", "weight": 30},
    map[string]any{"name": "cache", "weight": 30},
} %}
{#- TODO: sort by weight (desc), then name (asc). Fix the next line. -#}
{%- var sorted = backends %}
{%- for _, be := range sorted %}
server {{ be["name"] }} weight {{ be["weight"] }}
{%- end %}
```

<details class="pg-solution" markdown>
<summary>Solution</summary>

`$.weight:desc` sorts by weight descending; `$.name` breaks ties alphabetically.

```go
{%- var backends = []any{
    map[string]any{"name": "web", "weight": 10},
    map[string]any{"name": "api", "weight": 30},
    map[string]any{"name": "cache", "weight": 30},
} %}
{%- var sorted = backends | sort_by([]string{"$.weight:desc", "$.name"}) %}
{%- for _, be := range sorted %}
server {{ be["name"] }} weight {{ be["weight"] }}
{%- end %}
```

</details>

</div>

`first_seen("backend", serviceName)` returns true for the first occurrence
of a service name in that group and false for later occurrences. Use it to emit
one backend when several routes share a service:

<div class="pg-embed" markdown data-scriggo data-title="Challenge: emit each backend only once" data-difficulty="3" data-height="380">

<p class="pg-task" markdown>Several routes share a service; emit one `backend` line per unique service instead of one per route.</p>

```go
{%- var routes = []any{
    map[string]any{"host": "a.example.com", "service": "api"},
    map[string]any{"host": "b.example.com", "service": "api"},
    map[string]any{"host": "c.example.com", "service": "web"},
} -%}
{% for _, r := range routes -%}
{%- var svc = r | dig("service") | fallback("") -%}
{#- TODO: a service can back many hosts — emit each backend only once -#}
backend {{ svc }}
{% end -%}
```

<details class="pg-solution" markdown>
<summary>Peek at the solution</summary>

Gate the emit on `first_seen("backend", svc)` — it returns `true` only the first time it sees each service key, so the repeat is skipped.

```go
{%- var routes = []any{
    map[string]any{"host": "a.example.com", "service": "api"},
    map[string]any{"host": "b.example.com", "service": "api"},
    map[string]any{"host": "c.example.com", "service": "web"},
} -%}
{% for _, r := range routes -%}
{%- var svc = r | dig("service") | fallback("") -%}
{% if first_seen("backend", svc) -%}
backend {{ svc }}
{% end -%}
{% end -%}
```

</details>

</div>

## Mutable variables

Accumulate values across nested loops with `append`, then emit the collected result. This flattens every endpoint address into one numbered server list:

<div class="pg-embed" markdown data-scriggo data-title="Accumulate with append" data-height="360">

```go
{# Collect every address across nested loops, then emit them with a
   running index. #}
{%- var addresses = []any{} %}
{%- var slices = []any{
    map[string]any{"endpoints": []any{
        map[string]any{"addresses": []any{"10.0.0.1"}},
        map[string]any{"addresses": []any{"10.0.0.2"}},
    }},
    map[string]any{"endpoints": []any{
        map[string]any{"addresses": []any{"10.0.0.3"}},
    }},
} %}
{%- for _, es := range slices %}
{%- for _, ep := range es | dig("endpoints") | toSlice() %}
{%- for _, addr := range ep | dig("addresses") | toSlice() %}
{%- addresses = append(addresses, addr) %}
{%- end %}
{%- end %}
{%- end %}
{%- for i, addr := range addresses %}
server srv{{ i + 1 }} {{ addr }}:80
{%- end %}
```

</div>

## Whitespace control

Add `-` inside a tag to trim adjacent whitespace: `{%-` strips whitespace before the tag, `-%}` strips whitespace after it.

```go
{%- for _, item := range items %}   {# Strip before #}
{% for _, item := range items -%}   {# Strip after #}
{%- for _, item := range items -%}  {# Strip both #}
```

The stripped loop below renders one clean line per environment. Delete a dash and re-run to see the blank lines it was removing:

<div class="pg-embed" markdown data-scriggo data-title="Whitespace control" data-height="300">

```go
{# `{%-` strips the newline before the tag and `-%}` strips the one after,
   so this loop renders tight lines instead of a gap-filled block. #}
{%- var envs = []any{"prod", "staging", "dev"} %}
{%- for _, env := range envs %}
server {{ env }}.svc:80
{%- end %}
```

</div>

See the [function reference](template-reference.md#functions-and-filters) for all available helpers.
