# Template reference

Lookup tables for everything available inside a HAPTIC template: the context variables, every function and filter, `pathResolver`, and the status-patch functions. For a guided introduction with runnable examples, see the [Templating Guide](./templating.md).

## Context variables

All templates have access to the following top-level variables:

| Variable | Type | Description |
|----------|------|-------------|
| `resources` | map of stores | Kubernetes resources indexed per `watchedResources` config — entries are wrappers exposing `.List()` / `.Fetch(keys...)` / `.GetSingle(keys...)` |
| `controller` | map of stores | Controller-managed stores; currently only `controller.haproxy_pods` for the discovered HAProxy pod set |
| `pathResolver` | object | Resolves filenames to HAProxy paths — see [`pathResolver`](#pathresolver) |
| `capabilities` | map (bool values) | HAProxy feature flags derived from the local HAProxy version, `snake_case` keys (for example `capabilities.supports_crt_list`). Use for `{% if capabilities.supports_crt_list %}…{% end %}` branches — a mistyped key is silently falsy, not an error. |
| `currentConfig` | parsed config (or nil) | The previously deployed HAProxy configuration as a `*parser.StructuredConfig`. **Nil on first deployment** — guard with `{% if !isNil(currentConfig) %}`. Used for slot-preserving updates. |
| `dataplane` | `config.Dataplane` block | The CRD's `spec.dataplane` block — port, timeouts, paths |
| `shared` | `*SharedContext` | Thread-safe compute-once cache for expensive computations (`shared.ComputeIfAbsent(key, factory)` + `shared.Get(key)`; no `Set` — prevents racy check-then-act patterns) |
| `templateSnippets` | list | Names of all available template snippets — useful for dynamic `render_glob` patterns |
| `runtimeEnvironment` | object | Runtime info exposed by the controller (for example `runtimeEnvironment.GOMAXPROCS`) |
| `fileRegistry` | object | Lets templates dynamically register auxiliary files at render time via `fileRegistry.Register("file"/"cert"/"map"/"crt-list", filename, content)`; returns the resolved path. Used by the SSL, haproxytech, and haproxy-ingress libraries to materialise CA bundles, client certs, and SSL crt-lists from Secrets. |
| `http` | object | HTTP fetcher for `http.Fetch("https://example.com/...")` — see [Watching Resources — HTTP Resources](./watching-resources.md#http-resources) for the auto-registration and refresh mechanism |
| `extraContext` | map | The full `templatingSettings.extraContext` map. Read a key with `extraContext.key` or `extraContext["key"]` — see [Custom Template Variables](./templating.md#custom-template-variables). |

Note: the controller doesn't inject a `haproxyVersion` variable on its own. The Helm chart populates `templatingSettings.extraContext.haproxyVersion` from its `haproxyVersion` value, so chart-deployed templates read it as `{{ extraContext.haproxyVersion }}`. If you bypass the chart, set the value yourself in `templatingSettings.extraContext.haproxyVersion`. For feature checks prefer `capabilities.*` flags, which the controller derives from the local HAProxy probe.

## Functions and filters

Every entry below is callable in two equivalent styles: as a plain function (`fn(x, args...)`) or via the pipe operator (`x | fn(args...)`), which passes the left-hand value as the first argument. The pipe requires parentheses on the right side — `{{ value | toLower() }}` works, `{{ value | toLower }}` is a parse error. The one exception is `len`, a language builtin: call `len(x)` directly.

| Function | Purpose | Example |
|----------|---------|---------|
| `fallback(value, default)` | Return `default` if `value` is nil. Empty strings and zeroes pass through — only `dig()` on optional typed fields normalises zero values to nil first | `fallback(svc.port.number, 80)` |
| `dig(obj, "k1", "k2", ...)` | Walk a nested map / typed struct without nil-checking each level (navigates JSON tags on typed structs) | `dig(ing, "metadata", "annotations")` |
| `toSlice(v)` | Coerce `any` to `[]any` (safe to range over even if nil) | `for _, r := range toSlice(ing.spec.rules)` |
| `to_str_map(v)` | Normalise any string-keyed map (`map[string]string` from typegen, `map[string]any` from the untyped store path) to `map[string]string` — use on labels / `matchLabels` / annotations | `for k, v := range route.Metadata.Labels \| to_str_map()` |
| `shard_slice(items, idx, n)` | Type-preserving split of a slice into `n` shards, returning shard `idx` — input element type is kept | `shard_slice(gateways, i, totalShards)` |
| `tostring(v)`, `toint(v)`, `tofloat(v)` | Type conversions from `any` | `port = toint(annotation)` |
| `len(v)` | Length of slice / map / string (language builtin — not pipeable) | `len(ing.spec.rules)` |
| `keys(m)` | Sorted keys of a map | `for _, k := range keys(annotations)` |
| `merge(a, b)` | New map combining `a` and `b` (b wins on conflict) | `merge(defaults, overrides)` |
| `toLower(s)` / `toUpper(s)` | Case conversion | `host = toLower(rule.host)` |
| `replace(s, old, new)`, `split(s, sep)`, `join(slice, sep)`, `strip(s)`, `trim(s, cutset)`, `hasPrefix(s, p)`, `hasSuffix(s, p)` | String operations (`strip` trims whitespace; `trim` takes an explicit cutset) | `join(items, ", ")` |
| `first_seen(prefix, keys...)` | Returns `true` only the first time the key tuple is seen — for deduplicating | `if first_seen("backend", svc.namespace, svc.name)` |
| `sanitize_regex(s)` | Escape regex metacharacters in user input | `sanitize_regex(annotation)` |
| `semver_gte(version, "3.3")` | Compare a semver string (major.minor) against a target | `if semver_gte(extraContext.haproxyVersion, "3.3")` (the chart auto-populates `extraContext.haproxyVersion`; outside the chart, set it yourself via `templatingSettings.extraContext.haproxyVersion` — see [Custom Template Variables](./templating.md#custom-template-variables)) |
| `fail(msg)` | Abort rendering with an error message (surfaces in validation tests and webhooks) | `fail("missing required annotation")` |
| `b64decode(s)` | Decode base64 strings (Secret `.data` values) | `{{ secret.data.password \| b64decode() }}` |
| `glob_match(items, pattern)` | Filter strings by glob pattern | `{{ templateSnippets \| glob_match("backend-*") }}` |
| `group_by(items, keyPath)` | Group items by dotted key path | `{{ ingresses \| group_by("metadata.namespace") }}` |
| `map_extract(items, keyPath)` | Pluck one field (dotted key path) from each item into a flat slice | `{{ routes \| map_extract("routeId") }}` |
| `indent(s, n)` | Indent lines by N spaces (first and blank lines excluded) | `{{ render "snippet" \| indent(4) }}` |
| `sort_by(items, criteria)` | Sort by JSONPath expressions — see [`sort_by` modifiers](#sort_by-modifiers) | `{{ routes \| sort_by(["$.priority:desc"]) }}` |
| `debug(v, label)` | Output as JSON comment | `{{ routes \| debug("routes") }}` |
| `toJSON(v)` | Convert value to JSON string | `{{ myMap \| toJSON() }}` |

For complete coverage including crypto, encoding, and Scriggo built-ins (`abs`, `min`, `max`, `sprintf`, `now()`, etc.), see the [Scriggo built-ins reference](https://scriggo.com/templates/builtins).

### `sort_by` modifiers

`sort_by` criteria accept modifiers: `:desc` (descending), `:exists` (by field presence), `| length` (by length).

**Example — route precedence sorting:**

```go
{% var sorted = sort_by(routes, []string{
    "$.match.method:exists:desc",
    "$.match.headers | length:desc",
    "$.match.path.value | length:desc",
}) %}
```

### Regex flavor

HAPTIC has two regex surfaces, and they use different engines:

- **Template-level regex** — `regex_search`, `sanitize_regex`, and the `regex_replace` post-processor — runs on Go's `regexp` package, which implements RE2 syntax. RE2 has no backreferences and no look-around assertions; a pattern that needs those won't compile. The `regex_replace` post-processor also runs line by line, so a pattern can't span a newline, and `^` / `$` anchor to each line rather than the whole document.
- **HAProxy-runtime regex** — patterns HAProxy evaluates itself, such as `map_reg` lookups or a Gateway API `RegularExpression` path match — uses HAProxy's Perl Compatible Regular Expressions (PCRE) engine, which does support backreferences and look-around. A pattern that works in one surface may be rejected by the other.

### Emitting warnings

There is no `warn()` function. `fail(msg)` is the only helper that interrupts a render, and it aborts outright — the message surfaces in validation tests and admission webhooks. To emit a non-fatal warning, write an HAProxy comment line yourself and let the render continue:

```go
{%- var region = ingress.metadata.annotations["haptic.example.com/region"] %}
{%- if region == "" %}
# WARNING: {{ ingress.metadata.name }} has no haptic.example.com/region annotation; using default
{%- end %}
```

The comment travels into the deployed config, where it stays visible in the rendered `HAProxyCfg` resource and HAProxy's own config dump. `debug(value, label)` is the other non-fatal option — it renders a value as a JSON comment for troubleshooting.

## `pathResolver`

`pathResolver` is available in every template. Its `GetPath(filename, type)` method returns the path that HAProxy should use to reference an auxiliary file (map, error file, certificate, crt-list). Use it instead of writing paths by hand so the controller and HAProxy agree on where files live.

**Arguments**: `filename` (string), `type` (one of `"map"`, `"file"`, `"cert"`, `"crt-list"`)

```go
{# Map files — resolves to maps/host.map #}
use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

{# General files — resolves to general/504.http (chart default GeneralStorageDir basename) #}
errorfile 504 {{ pathResolver.GetPath("504.http", "file") }}

{# SSL certificates — resolves to ssl/example_com.pem (dots in cert/crt-list names are sanitized to _) #}
bind *:443 ssl crt {{ pathResolver.GetPath("example.com.pem", "cert") }}

{# crt-list files — resolves to general/cert-list.txt (CRTListDir defaults to GeneralStorageDir basename) #}
bind *:443 ssl crt-list {{ pathResolver.GetPath("cert-list.txt", "crt-list") }}
```

By default `GetPath` returns paths *relative* to HAProxy's `default-path` directive. The chart's `base` template library renders `default-path origin {{ pathResolver.GetBaseDir() }}` in the global section (for example `default-path origin /etc/haproxy` in production), which tells HAProxy to resolve relative paths against that explicit base directory. The controller writes maps, certs, and general files under the same base, so the relative paths line up at runtime; the validation pipeline rewrites just the `default-path origin` argument to a per-call temp directory so the same rendered config validates against a sandbox tree of identical shape. If you replace the base library, keep that directive (or render an absolute path yourself) — without it HAProxy resolves the relative paths from its own working directory and the file lookups fail.

## Status-patch functions

Templates register status patches with `statusPatch()`; the controller applies them to the resource's `/status` subresource via Server-Side Apply (SSA) after each reconciliation phase. The [Templating Guide — Status Patches](./templating.md#status-patches) walks through the pattern with runnable examples.

### `statusPatch()`

Registers a status patch for a Kubernetes resource with outcome-keyed variants. Each variant's value is the resource's `.status` content directly (for example `conditions`, `loadBalancer`) — the controller writes it under `.status` via SSA, so don't wrap it in another `status` key.

**Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `namespace` | `string` | Resource namespace |
| `name` | `string` | Resource name |
| `apiVersion` | `string` | Resource API version (for example, `networking.k8s.io/v1`) |
| `kind` | `string` | Resource kind (for example, `Ingress`, `Gateway`) |
| `variants` | `map[string]any` | Status payloads keyed by pipeline phase |

**Variants:**

| Key | Applied When |
|-----|-------------|
| `rendered` | After successful template rendering (before deployment) |
| `deployed` | After successful HAProxy deployment |
| `renderFailed` | When a later rendering phase fails |
| `deployFailed` | When HAProxy deployment fails |

Templates render all variants upfront. The controller selects the appropriate variant based on the pipeline outcome.

### `condition()`

Creates a `metav1.Condition`-compatible map.

**Parameters:** `type`, `status`, `reason`, `message`, `observedGeneration`, `lastTransitionTime`

```go
{{ condition("Accepted", "True", "Accepted", "Resource is accepted", 1, "2024-01-01T00:00:00Z") | toJSON() }}
```

### `transitionTime()`

Returns the correct `lastTransitionTime` for a condition: preserves the existing timestamp if the condition status hasn't changed, or returns the current time if it has changed or doesn't exist yet.

**Parameters:** `existingConditions` (the resource's existing conditions list — navigate to it yourself with `dig(resource, "status", "conditions")`, so the helper stays agnostic to where a given resource keeps its conditions), `type`, `status`.

For resources with nested condition arrays (for example, Gateway API Route `parents[]`), navigate to the parent's conditions first:

```go
{%- var parents = dig(resource, "status", "parents") | toSlice() %}
{{ transitionTime(dig(parents[parentIndex], "conditions"), "Accepted", "True") }}
```

## Typed access internals

!!! note "Background"
    This section documents how typed field names are generated — background for chart authors and contributors. Day-to-day usage, including the field-name table and the typed-vs-untyped decision rule, lives in [Templating — Typed Resource Access](./templating.md#typed-resource-access).

The field-name rule lives in `pkg/k8s/typegen/converter.go::GoFieldName`: Go-PascalCase of the JSON tag, with no acronym dictionary. The no-acronym-dictionary choice is deliberate — there is no translation table to keep in sync, so `apiVersion` becomes `ApiVersion` (not the upstream-Go-style `APIVersion`), and only rune 0 of a tag is ever changed.

**Worked example and regression canary.** `charts/haptic/charts/gateway/05-typed-access-smoke.yaml` is the canonical single-snippet example — it emits one HAProxy comment per Gateway using `gw.Metadata.Namespace` / `gw.Metadata.Name`. Its companion test `test-gateway-typed-access-smoke` pins the wiring end-to-end (engine declarations + runtime bindings + actual render output) and acts as a regression canary for typed access generally.

See the architecture decision record [`ADR-0010` — Typed Watched Resources](development/adr/0010-typed-watched-resources.md) for the design rationale and the alternatives considered.
