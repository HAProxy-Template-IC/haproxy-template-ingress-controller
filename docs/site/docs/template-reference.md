# Template reference

Lookup tables for everything available inside a HAPTIC template: the context variables, every function and filter, `pathResolver`, and the status-patch functions. For a guided introduction with runnable examples, see the [Templating Guide](./templating.md).

## Context variables

All templates have access to the following top-level variables:

| Variable | Type | Description |
|----------|------|-------------|
| `resources` | map of stores | Kubernetes resources indexed per `watchedResources` config — entries are wrappers exposing `.List()` / `.Fetch(keys...)` / `.GetSingle(keys...)` / `.APIVersion()` (the group/version this resource is actually watched at — pass it to `statusPatch()` instead of hardcoding a literal), plus the type-carrying `.T` |
| `controller` | map of stores | Controller-managed stores; currently only `controller.haproxy_pods` for the discovered HAProxy pod set |
| `pathResolver` | object | Resolves filenames to HAProxy paths — see [`pathResolver`](#pathresolver) |
| `capabilities` | map (bool values) | HAProxy feature flags, `snake_case` keys (for example `capabilities.supports_crt_list`). Derived from the **lowest HAProxy version in the fleet**, as each pod's agent reports it, so a render is never ahead of the pod that would refuse it. Before the first pod is discovered they come from the controller image's own HAProxy binary, which the chart pins to the same `haproxyVersion`. Use for `{% if capabilities.supports_crt_list %}…{% end %}` branches — a mistyped key is silently falsy, not an error. |
| `currentConfig` | server index (or nil) | The servers the running configuration has: `currentConfig.ServerIndex["<backend>"]["<server>"].Address` and `.Port` (a pointer — check it for nil before you read it). **Nil on first deployment** — guard with `{% if !isNil(currentConfig) %}`. Used for slot-preserving updates. |
| `currentFiles` | `map[string]string` | The last successfully validated map, general-file, and crt-list output, keyed by base filename. Reconciliation advances it synchronously after validation; admission and other all-replica validation use the latest completely committed `HAProxyCfg` auxiliary reference set, including set metadata on referenced certificate Secrets. Secret data isn't exposed. If a legacy publication changes without a set ID, rendering fails until a complete set is committed, including on a leader with locally accepted output. After a set-ID publication is accepted, a missing set ID also fails instead of restoring legacy mode. Within a render the map is always non-nil (empty on first deployment), so index it without a guard and branch on `len(currentFiles) == 0`. Controller-set; `extraContext.currentFiles` can't override it. |
| `dataplane` | `config.Dataplane` block | The CRD's `spec.dataplane` block — the agent port, timeouts, and the auxiliary paths |
| `shared` | `*SharedContext` | Thread-safe compute-once cache for expensive computations (`shared.ComputeIfAbsent(key, factory)` + `shared.Get(key)`; no `Set` — prevents racy check-then-act patterns) |
| `templateSnippets` | list | Names of all available template snippets — useful for dynamic `render_glob` patterns |
| `runtimeEnvironment` | object | Runtime info exposed by the controller (for example `runtimeEnvironment.GOMAXPROCS`) |
| `fileRegistry` | object | Lets templates dynamically register auxiliary files at render time via `fileRegistry.Register("file"/"cert"/"map"/"crt-list"/"ca-file", filename, content)`; returns the resolved path. Used by the SSL, haproxytech, and haproxy-ingress libraries to materialise CA bundles, client certs, and SSL crt-lists from Secrets. A `"file"` registration takes an optional fourth argument `reloadOnPush` (default `true`); pass `false` for a file only a sidecar reads, so a content change deploys without reloading HAProxy. |
| `http` | object | HTTP fetcher for `http.Fetch("https://example.com/...")` — see [Watching Resources — HTTP Resources](./watching-resources.md#http-resources) for the auto-registration and refresh mechanism |
| `extraContext` | map | The full `templatingSettings.extraContext` map. Read a key with `extraContext.key` or `extraContext["key"]` — see [Custom Template Variables](./templating.md#custom-template-variables). |
| `renderMode` | string | Why this render is running: `"admission"` for a webhook dry-run of a *proposed* change, or `"reconcile"` for the live config of already-present state (also the value the daemon load gate and `controller validate` use). Branch on it so a validation check can `fail()` a proposed change under the webhook but only warn (via [`recordEvent()`](#recordevent)) during a live reconcile — a `fail()` on live state aborts the whole config render. The controller always sets it; a user's `extraContext.renderMode` can't override it. |
| `admissionSubject` | map | The watched object under admission review: `{"store": "<key>", "stores": {"<key>": true}, "namespace": ..., "name": ...}`, or an empty map on reconcile renders, config-proposal renders, and bulk overlays. `stores` contains every `watchedResources` alias whose contents the request changes; `store` is set only when that set has one entry. Match an alias with `admissionSubject \| dig("stores", alias) \| fallback(false)`. Combine with `renderMode` to `fail()` only for the admitted object, so one existing bad resource can't deny unrelated admissions. Controller-set; a user's `extraContext.admissionSubject` can't override it. |

Note: the controller doesn't inject a `haproxyVersion` variable on its own. The Helm chart populates `templatingSettings.extraContext.haproxyVersion` from its `haproxyVersion` value, so chart-deployed templates read it as `{{ extraContext.haproxyVersion }}`. If you bypass the chart, set the value yourself in `templatingSettings.extraContext.haproxyVersion`. For feature checks prefer `capabilities.*` flags, which follow the fleet's lowest HAProxy version rather than a value the chart happens to set.

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
| `regex_search(s, pattern)` | True when the RE2 pattern matches anywhere in the string. Both arguments are coerced with `tostring()` first, and a pattern that doesn't compile aborts the render | `{% if regex_search(name, "ssl.*passthrough") %}` |
| `semver_gte(version, "3.3")` | Compare a semver string (major.minor) against a target | `if semver_gte(extraContext.haproxyVersion, "3.3")` (the chart auto-populates `extraContext.haproxyVersion`; outside the chart, set it yourself via `templatingSettings.extraContext.haproxyVersion` — see [Custom Template Variables](./templating.md#custom-template-variables)) |
| `fail(msg)` | Abort rendering with an error message (surfaces in validation tests and webhooks) | `fail("missing required annotation")` |
| `b64decode(s)` | Decode base64 strings (Secret `.data` values) | `{{ secret.data.password \| b64decode() }}` |
| `b64encode(s)` | Encode a value as standard base64 | `{{ configmap.data.schema \| b64encode() }}` |
| `untar_gz(archive)` | Expand a gzip-compressed tar into a map of entry path to content. Returns `(map[string]string, error)` — a bad archive is reported through the error, never a panic, so the render survives it. All-or-nothing: on any error the map is empty. Entry paths are verbatim (a release tarball keeps its version directory); select with `keys()` + `glob_match()`. Only regular files; guarded against decompression bombs and path traversal | `{%- var files, err = untar_gz(archive) %}` |
| `glob_match(items, pattern)` | Filter strings by glob pattern | `{{ templateSnippets \| glob_match("backend-*") }}` |
| `map_extract(items, keyPath)` | Pluck one field (dotted key path) from each item into a flat slice | `{{ routes \| map_extract("routeId") }}` |
| `indent(s, n)` | Indent lines by N spaces (first and blank lines excluded) | `{{ render "snippet" \| indent(4) }}` |
| `debug(v, label)` | Output as JSON comment | `{{ routes \| debug("routes") }}` |
| `toJSON(v)` | Convert value to JSON string | `{{ myMap \| toJSON() }}` |
| `basename(path)` | Filename portion of a path, like Unix `basename` | `{%- var p, _ = fileRegistry.Register("map", n, c) %}{{ basename(p) }}` |
| `namespace(init)` | Mutable `map[string]any` for accumulating state across loop iterations | `{%- var acc = namespace(map[string]any{"n": 0}) %}` |
| `isNil(v)` | Nil check that also catches a typed nil pointer boxed in an `any` | `{% if !isNil(currentConfig) %}` |
| `coalesce(value, default)` | First non-nil of the two — the plain-call spelling of `fallback` | `coalesce(annotation, "default")` |

### Strings and types

| Function | Purpose | Example |
|----------|---------|---------|
| `strings_contains(s, sub)`, `strings_split(s, sep)`, `strings_splitn(s, sep, n)`, `strings_trim(s)`, `strings_lower(s)`, `strings_replace(s, old, new)` | `any`-tolerant string operations — they coerce their arguments, so they work on values read out of a `map[string]any` without a cast first. `strings_trim` trims whitespace; `strings_splitn` caps the result at `n` parts | `strings_splitn(annotation, ":", 2)` |
| `title(s)` | Title-case a string | `title(mode)` |
| `isdigit(s)` | True when the string is non-empty and all digits — check before `toint()` | `{% if isdigit(port) %}` |
| `toStringSlice(items)` | Convert a `[]any` to `[]string` | `toStringSlice(hosts)` |
| `sort_strings(items)` | Sort a `[]any` lexicographically, returning `[]string` | `sort_strings(keys(m))` |
| `sort_ints(items)` | Sort a `[]any` numerically, returning `[]int` — use for ports and IDs, where `sort_strings` would put `"10"` before `"2"`. Non-integer entries coerce through `toint()` and sort to the front | `sort_ints(ports)` |
| `ceil(f)` | Round a float up | `ceil(tofloat(total) / 4)` |
| `seq(n)` | `[]int{0, 1, …, n-1}`, for fixed-count loops such as reserved server slots | `{% for _, i := range seq(slots) %}` |
| `append(slice, item)` | Go's builtin, type-preserving; `append(dst, src...)` spreads a slice of the **same** type. Widening into `[]any` is a compile error — box per element in a loop. A slice reached through `any` is asserted at the boundary | `append(gf["hosts"].([]any), h)` |
| `dig_string(obj, default, keys...)` | `dig` + `fallback` + `tostring` in one call, for annotation and metadata lookups | `ing \| dig_string("", "metadata", "annotations", key)` |
| `join_key(sep, parts...)` | Join any values into one composite key string | `join_key("_", ns, name, port)` |
| `make_guid(parts...)` | Build a value for HAProxy's `guid` directive from parts joined by `:`, auto-truncating with a hash suffix past its 127-character limit | `guid {{ make_guid("be", beKey) }}` |
| `selectattr(items, attr[, op, value])` | Jinja2-style attribute filter. `op` is `eq`, `ne` or `in`; omitted, it keeps items whose `attr` is truthy. `attr` is one literal key — a dotted path matches nothing, so prefer `filter` with a closure | `selectattr(rules, "host", "ne", "")` |

### Collection pipelines

Type-preserving stages, chained with `|`. Each keeps its input's element type, so typed field access still resolves at the last stage and a misspelled field fails the config load instead of rendering an empty file. Predicates are closures — write them long-hand (`func(e T) bool { … }`) or as `x => expr` with both types inferred. See [Templating — Collection pipelines](./templating.md#collection-pipelines) for the guided version.

| Function | Purpose | Example |
|----------|---------|---------|
| `map(items, fn)` | One output per input | `pods \| map(p => p.Metadata.Name)` |
| `filter(items, pred)` | Keep the elements the predicate accepts | `routes \| filter(r => r.Spec.Tls)` |
| `reject(items, pred)` | Drop them instead, so the call site reads as a positive statement | `eps \| reject(e => e.TargetRef.Name == "")` |
| `flat_map(items, fn)` | Map to slices and concatenate, flattening exactly one level | `slices \| flat_map(s => s.Endpoints)` |
| `unique(items)` | First occurrence of each distinct element, input order preserved | `hosts \| unique()` |
| `unique_by(items, key)` | First element per key. `key` is a closure, or an attribute path for `any`-shaped data | `eps \| unique_by(e => e.Addr)` |
| `group_by(items, key)` | Bucket by string key, input order preserved within each bucket. Same two key forms. Iterate the result through `keys()` — Go map order isn't stable, and a reordered render reads as a change to the controller | `ingresses \| group_by("metadata.namespace")` |
| `sort_by(items, criteria)` | Sort by JSONPath expressions — see [`sort_by` modifiers](#sort_by-modifiers) | `routes \| sort_by([]string{"$.priority:desc"})` |
| `sort_by(items, cmp)` | Sort with a `func(a, b T) int` comparator (Go's `cmp` convention: negative when `a` sorts first), for orderings the criteria language can't state. Stable, like the criteria form | `routes \| sort_by(func(a, b Route) int { return a.Rank - b.Rank })` |

`sort_by` is the one stage that returns `(value, error)`. As a pipe stage that's invisible — the pipe keeps only the first result, so `x | sort_by(…)` assigns to one variable. A **direct** call returns both and needs two: `var rows, err = sort_by(items, criteria)`.

Unlike the Scriggo builtins they replace, the attribute-path form of `unique_by` and `group_by` splits a dotted path into separate `dig` keys, so `"spec.hostname"` navigates two levels instead of looking for one key literally named `spec.hostname`.

Each stage re-enters the template VM once per element. Chains of `map`/`filter`/`reject`/`flat_map` are lowered to loops at compile time and cost what the hand-written loop costs; the other stages don't lower, so over many thousands of elements a `{% for %}` loop still wins.

### Governance helpers

Resource-agnostic read/write access to any watched resource by JSONPath — the primitives the chart's [governance guardrails](./operations/governance.md) are built from.

| Function | Purpose | Example |
|----------|---------|---------|
| `resource(name)` | The per-render items of a watched resource named *dynamically*, sharing the same objects as `resources.<name>.List()` — so a `jsonpathSet` write is visible downstream | `resource(extraContext.targetStore)` |
| `jsonpathGet(item, path)` | Read a concrete JSONPath out of an item | `jsonpathGet(ing, "$.spec.ingressClassName")` |
| `jsonpathSet(item, path, value)` | Write a concrete JSONPath into an item, in place. Returns whether the write landed | `jsonpathSet(ing, "$.metadata.annotations.x", "1")` |

For complete coverage including crypto, encoding, and Scriggo built-ins (`abs`, `min`, `max`, `sprintf`, `now()`, etc.), see the [Scriggo built-ins reference](https://scriggo.com/templates/builtins).

### `sort_by` modifiers

`sort_by` criteria accept modifiers: `:desc` (descending), `:exists` (by field presence), `| length` (by length).

`sort_by` is a stable sort: items whose keys compare equal on every criterion keep their original input order. Add a final deterministic criterion (such as `$.name`) only when you want a specific order for otherwise-equal items rather than input order.

**Example — route precedence sorting:**

```go
{% var sorted, sortErr = sort_by(routes, []string{
    "$.match.method:exists:desc",
    "$.match.headers | length:desc",
    "$.match.path.value | length:desc",
}) %}
{% if sortErr != nil %}{{ fail("sorting routes: " + tostring(sortErr)) }}{% end %}
```

### Regex flavor

HAPTIC has two regex surfaces, and they use different engines:

- **Template-level regex** — `regex_search`, `sanitize_regex`, and the `regex_replace` post-processor — runs on Go's `regexp` package, which implements RE2 syntax. RE2 has no backreferences and no look-around assertions; a pattern that needs those won't compile. The `regex_replace` post-processor also runs line by line, so a pattern can't span a newline, and `^` / `$` anchor to each line rather than the whole document.
- **HAProxy-runtime regex** — patterns HAProxy evaluates itself, such as `map_reg` lookups, a Gateway API `RegularExpression` path match, or `server-alias-regex` — uses HAProxy's Perl Compatible Regular Expressions (PCRE) engine, which does support backreferences and look-around. A pattern that works in one surface may be rejected by the other.

!!! warning "Regex safety with user-supplied patterns"
    Template-level regex runs on RE2, which is linear-time and can't catastrophically backtrack, so a pattern in `regex_search`, `sanitize_regex`, or `regex_replace` is never a regular-expression denial-of-service (ReDoS) vector, whatever the input. PCRE does backtrack, and HAPTIC imposes no PCRE match limit — a pathological pattern-and-input pair reaching the HAProxy-runtime surface is a potential data-plane CPU-exhaustion risk. When patterns can come from tenants you don't control (for example an Ingress or HTTPRoute authored in a namespace you don't own), match on the RE2 side where you can, and review any tenant-supplied runtime regex.

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

## Event functions

### `recordEvent()`

Records a Kubernetes `Warning` Event against a resource. The controller emits it via an EventRecorder on the leader, so it shows up under `kubectl describe <kind> <name>` and `kubectl get events`. Like `statusPatch()`, it's resource-agnostic — you pass the resource object itself, and its `namespace`/`name`/`apiVersion`/`kind` are read off it, so it works for any watched resource or custom resource.

**Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `resource` | resource object | The watched resource to record the Event against — a typed resource, a `map`, or an unstructured object (for example, an item from `resources.ingresses.List()`) |
| `reason` | `string` | Short, machine-readable `PascalCase` reason (for example, `RouteConflict`) |
| `message` | `string` | Human-readable description |

```go
{% recordEvent(ingress,
    "RouteConflict", "host \"" + rule.Host + "\" path \"" + path.Path + "\" is already served by another Ingress") %}
```

The Event is a side-effect only — the call renders nothing. Identical `(resource, reason, message)` tuples emitted during one render collapse into a single Event. The controller re-emits on every reconcile while the condition holds, so the standard Kubernetes Event aggregation keeps it fresh and it ages out (default TTL ~1 hour) once the template stops recording it. The bundled Ingress library uses this to surface [route conflicts](./libraries/ingress.md#conflicting-routes-the-oldest-ingress-wins) on the losing Ingress.

## Typed access internals

!!! note "Background"
    This section documents how typed field names are generated — background for chart authors and contributors. Day-to-day usage, including the field-name table and the typed-vs-untyped decision rule, lives in [Templating — Typed Resource Access](./templating.md#typed-resource-access).

The field-name rule lives in `pkg/k8s/typegen/converter.go::GoFieldName`: Go-PascalCase of the JSON tag, with no acronym dictionary. The no-acronym-dictionary choice is deliberate — there is no translation table to keep in sync, so `apiVersion` becomes `ApiVersion` (not the upstream-Go-style `APIVersion`), and only rune 0 of a tag is ever changed.

**Worked example and regression canary.** `charts/haptic/charts/gateway/05-typed-access-smoke.yaml` is the canonical single-snippet example — it emits one HAProxy comment per Gateway using `gw.Metadata.Namespace` / `gw.Metadata.Name`. Its companion test `test-gateway-typed-access-smoke` pins the wiring end-to-end (engine declarations + runtime bindings + actual render output) and acts as a regression canary for typed access generally.

See the architecture decision record [`ADR-0010` — Typed Watched Resources](development/adr/0010-typed-watched-resources.md) for the design rationale and the alternatives considered.
