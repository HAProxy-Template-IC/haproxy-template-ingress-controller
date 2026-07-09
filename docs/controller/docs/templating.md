# Templating Guide

## Overview

The HAProxy Template Ingress Controller uses [Scriggo](https://scriggo.com/), a Go template engine, to generate HAProxy configurations from Kubernetes resources. The Helm chart ships with ready-to-use [template libraries](/helm-chart/latest/template-libraries/) that cover standard Ingress and Gateway API use cases — you only need to write templates when you want to extend or replace that default behavior. Templates access watched Kubernetes resources, and the controller renders them whenever resources change, validates the output, and deploys it to HAProxy instances.

Templates are rendered automatically when any watched resource changes, during initial synchronization, or periodically for drift detection.

<div class="pg-embed" markdown data-scenario="ingress" data-tab="haproxy.cfg" data-title="See a template render — live" data-controls="tabs,provenance" data-height="480">
</div>

Hit **Run live** above to render the bundled Ingress example entirely in your browser. Edit the template on the left and watch `haproxy.cfg` update on the right — then switch tabs to see the `maps`, `files`, and `status` it also produces. Click any output line to jump to the template line that produced it, or **Open in full playground** to bring your changes into the full editor.

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-focus="11" data-title="Your turn: add an HSTS header" data-difficulty="1">

Edit the `frontend web` section so every response carries a `Strict-Transport-Security` header, then hit **Run live** and watch line&nbsp;11 of the output. (Hint: HAProxy's `http-response set-header`.)

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: hsts-demo
spec:
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend web
        bind *:80
        # TODO(you): add a line so every response carries an HSTS header
        default_backend app
      backend app
        server s1 127.0.0.1:8080 check
```

<details class="pg-solution" markdown>
<summary>Peek at the solution</summary>

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: hsts-demo
spec:
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend web
        bind *:80
        http-response set-header Strict-Transport-Security "max-age=31536000; includeSubDomains"
        default_backend app
      backend app
        server s1 127.0.0.1:8080 check
```

</details>
</div>

## What You Can Template

| Template Type | Use When |
|---------------|----------|
| `haproxyConfig` | Main HAProxy configuration (frontends, backends, global settings) |
| `maps` | HAProxy lookup tables for host/path routing decisions |
| `files` | Auxiliary files like custom error pages |
| `sslCertificates` | TLS certificate files assembled from Kubernetes Secrets |

### HAProxy Configuration

The main `haproxyConfig` template generates the complete HAProxy configuration file:

```yaml
haproxyConfig:
  template: |
    global
        log stdout len 4096 local0 info
        daemon
        maxconn 4096

    defaults
        mode http
        timeout connect 5s
        timeout client 50s
        timeout server 50s

    frontend http
        bind *:80
        use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

    {% for _, ingress := range resources.ingresses.List() %}
    backend {{ ingress.metadata.name }}
        balance roundrobin
    {% end %}
```

!!! important
    Whenever your HAProxy config references a map file, error file, certificate, or crt-list, use `pathResolver.GetPath(filename, type)` instead of a hard-coded path. The controller deploys these files to a configurable directory (set in `spec.dataplane.mapsDir`, `sslCertsDir`, `generalStorageDir`) and `pathResolver` knows where they live, so the path stays correct even if you reconfigure those directories.

### Map Files

Map files generate HAProxy lookup tables. They are written to `spec.dataplane.mapsDir` (default `/etc/haproxy/maps/`) on the HAProxy pod:

```yaml
maps:
  host.map:
    template: |
      {%- for _, ingress := range resources.ingresses.List() %}
      {%- for _, rule := range fallback(ingress.spec.rules, []any{}) %}
      {%- if rule.http != nil %}
      {{ rule.host }} {{ rule.host }}
      {%- end %}
      {%- end %}
      {%- end %}
```

### General Files

Auxiliary files like custom error pages. Written to `spec.dataplane.generalStorageDir` (default `/etc/haproxy/general/`):

```yaml
files:
  503.http:
    template: |
      HTTP/1.0 503 Service Unavailable
      Cache-Control: no-cache
      Connection: close
      Content-Type: text/html

      <html><body><h1>503 Service Unavailable</h1></body></html>
```

### SSL Certificates

SSL/TLS certificate files assembled from Kubernetes Secrets. Written to `spec.dataplane.sslCertsDir` (default `/etc/haproxy/ssl/`):

```yaml
sslCertificates:
  example-com.pem:
    template: |
      {%- var secret = resources.secrets.GetSingle("default", "example-com-tls") %}
      {%- if secret != nil %}
      {{ b64decode(secret.data["tls.crt"]) }}
      {{ b64decode(secret.data["tls.key"]) }}
      {%- end %}
```

!!! note
    Certificate data in Secrets is base64-encoded. Use the `b64decode` filter to decode it.

### Template Snippets

Reusable template fragments included via `{{ render "snippet-name" }}`:

```yaml
templateSnippets:
  backend-name:
    template: >-
      ing_{{ ingress.metadata.namespace }}_{{ ingress.metadata.name }}

  backend-servers:
    template: |
      {%- for _, endpoint_slice := range resources.endpoints.Fetch(service_name) %}
      {%- for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
      {%- for _, address := range fallback(endpoint.addresses, []any{}) %}
      server {{ endpoint.targetRef.name }} {{ address }}:{{ port }} check
      {%- end %}
      {%- end %}
      {%- end %}
```

Include a snippet in a template:

```go
{{ render "backend-name" }}
```

Include all snippets matching a glob pattern (rendered in alphabetical order):

```go
{{ render_glob "backend-*" }}
```

Pass local variables to rendered snippets with `inherit_context`:

```go
{%- var service_name = "my-service" %}
{{ render "backend-servers" inherit_context }}
```

### Post-Processing

The `haproxyConfig` section supports a `postProcessing` list that transforms the rendered output before deployment. Post-processors run sequentially on the rendered configuration.

Available types:

| Type | Description |
|------|-------------|
| `regex_replace` | Line-by-line regex find/replace (`pattern` and `replace` params) |
| `template` | Scriggo template transformation with access to the rendered output via the `input` variable (`source` param) |

```yaml
haproxyConfig:
  template: |
    global
        daemon
    # ...
  postProcessing:
    - type: template
      params:
        source: |
          {%- if strings_contains(input, "__PLACEHOLDER__") -%}
          {{ replace(input, "__PLACEHOLDER__", "computed-value") }}
          {%- else -%}
          {{ input }}
          {%- end -%}
    - type: regex_replace
      params:
        pattern: "^[ ]+"
        replace: "  "
```

The `template` post-processor receives the fully rendered output as the `input` variable and has access to all standard Scriggo builtins (`regexp`, `replace`, `len`, `tostring`, etc.). Its output becomes the new rendered content.

## Template Syntax

Templates use Scriggo's template syntax. For complete syntax reference, see the [Scriggo documentation](https://scriggo.com/templates).

### Control Structures

```go
{# Loops #}
{% for _, ingress := range resources.ingresses.List() %}
  backend {{ ingress.metadata.name }}
{% end %}

{# Conditionals #}
{% if ingress.spec.tls != nil %}
  bind *:443 ssl crt {{ pathResolver.GetPath(ingress.metadata.name + ".pem", "cert") }}
{% end %}

{# Variables #}
{% var service_name = path.backend.service.name %}
{% var port = fallback(path.backend.service.port.number, 80) %}

{# Comments #}
{# This is a comment #}
```

### Helper Functions

The most commonly needed helpers when writing HAPTIC templates. All can be called as functions or via the pipe operator (`x | fn()` is equivalent to `fn(x)`).

| Helper | Purpose | Example |
|--------|---------|---------|
| `fallback(value, default)` | Return `default` if `value` is nil / empty / zero | `fallback(svc.port.number, 80)` |
| `dig(obj, "k1", "k2", ...)` | Walk a nested map / typed struct without nil-checking each level (navigates JSON tags on typed structs) | `dig(ing, "metadata", "annotations")` |
| `toSlice(v)` | Coerce `any` to `[]any` (safe to range over even if nil) | `for _, r := range toSlice(ing.spec.rules)` |
| `to_str_map(v)` | Normalise any string-keyed map (`map[string]string` from typegen, `map[string]any` from the untyped store path) to `map[string]string` — use on labels / matchLabels / annotations | `for k, v := range route.Metadata.Labels \| to_str_map()` |
| `shard_slice(items, idx, n)` | Type-preserving split of a slice into `n` shards, returning shard `idx` — input element type is kept | `shard_slice(gateways, i, totalShards)` |
| `tostring(v)`, `toint(v)`, `tofloat(v)` | Type conversions from `any` | `port = toint(annotation)` |
| `len(v)` | Length of slice / map / string | `len(ing.spec.rules)` |
| `keys(m)` | Sorted keys of a map | `for _, k := range keys(annotations)` |
| `merge(a, b)` | New map combining `a` and `b` (b wins on conflict) | `merge(defaults, overrides)` |
| `toLower(s)` / `toUpper(s)` | Case conversion | `host = toLower(rule.host)` |
| `replace(s, old, new)`, `split(s, sep)`, `join(slice, sep)`, `trim(s)`, `hasPrefix(s, p)`, `hasSuffix(s, p)` | String operations | `join(items, ", ")` |
| `first_seen(prefix, keys...)` | Returns `true` only the first time the key tuple is seen — for deduplicating | `if first_seen("backend", svc.namespace, svc.name)` |
| `sanitize_regex(s)` | Escape regex metacharacters in user input | `sanitize_regex(annotation)` |
| `semver_gte(version, "3.3")` | Compare HAProxy version (major.minor) | `if semver_gte(haproxyVersion, "3.3")` |
| `fail(msg)` | Abort rendering with an error message (surfaces in validation tests and webhooks) | `fail("missing required annotation")` |

For complete coverage including crypto, encoding, and Scriggo built-ins (`abs`, `min`, `max`, `sprintf`, `now()`, etc.), see the [Scriggo built-ins reference](https://scriggo.com/templates/builtins).

Try the helpers live in a pure Scriggo scratchpad — no config, no resources, just
the template language and every function above. Edit it and watch the output.

<div class="pg-embed" markdown data-scriggo data-title="Scriggo scratchpad — try the helpers" data-height="360">

```go
{# Every helper from the table above is available here. Edit freely. #}
{%- var envs = []any{"prod", "dev", "staging"} %}
{%- var sorted = envs | sort_by([]string{"$"}) %}
{%- for _, e := range sorted %}
backend {{ e }}
  server app {{ toLower(tostring(e)) }}.svc:80
{%- end %}
```

</div>

Ready for a challenge? Sort a list of backends heaviest-first, breaking ties by
name. Edit the template to fix the sort — or peek at the solution.

<div class="pg-embed" markdown data-scriggo data-title="Challenge: sort by two keys" data-difficulty="2" data-height="380">

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

### Path Resolution

`pathResolver` is a helper available in every template. Its `GetPath(filename, type)` method returns the path that HAProxy should use to reference an auxiliary file (map, error file, certificate, crt-list). Use it instead of writing paths by hand so the controller and HAProxy agree on where files live.

By default `GetPath` returns paths *relative* to HAProxy's `default-path` directive. The chart's `base` template library renders `default-path origin {{ pathResolver.GetBaseDir() }}` in the global section (e.g. `default-path origin /etc/haproxy` in production), which tells HAProxy to resolve relative paths against that explicit base directory. The controller writes maps, certs, and general files under the same base, so the relative paths line up at runtime; the validation pipeline rewrites just the `default-path origin` argument to a per-call temp directory so the same rendered config validates against a sandbox tree of identical shape. If you replace the base library, keep that directive (or render an absolute path yourself) — without it HAProxy resolves the relative paths from its own working directory and the file lookups fail.

```go
{# Map files — resolves to maps/host.map #}
use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

{# General files — resolves to general/504.http (chart default GeneralStorageDir basename) #}
errorfile 504 {{ pathResolver.GetPath("504.http", "file") }}

{# SSL certificates — resolves to ssl/example.com.pem #}
bind *:443 ssl crt {{ pathResolver.GetPath("example.com.pem", "cert") }}

{# crt-list files — resolves to general/cert-list.txt (CRTListDir defaults to GeneralStorageDir basename) #}
bind *:443 ssl crt-list {{ pathResolver.GetPath("cert-list.txt", "crt-list") }}
```

**Arguments**: `filename` (string), `type` (one of `"map"`, `"file"`, `"cert"`, `"crt-list"`)

### Custom Filters

| Filter | Description | Example |
|--------|-------------|---------|
| `b64decode` | Decode base64 strings | `{{ secret.data.password \| b64decode() }}` |
| `glob_match` | Filter strings by glob pattern | `{{ templateSnippets \| glob_match("backend-*") }}` |
| `group_by` | Group items by JSONPath | `{{ ingresses \| group_by("$.metadata.namespace") }}` |
| `map_extract` | Pluck one field (dotted key path) from each item into a flat slice | `{{ routes \| map_extract("routeId") }}` |
| `indent` | Indent each line by N spaces | `{{ render("snippet") \| indent(4) }}` |
| `sanitize_regex` | Escape regex special characters | `{{ path \| sanitize_regex() }}` |
| `sort_by` | Sort by JSONPath expressions | `{{ routes \| sort_by(["$.priority:desc"]) }}` |
| `debug` | Output as JSON comment | `{{ routes \| debug("routes") }}` |
| `toJSON` | Convert value to JSON string | `{{ myMap \| toJSON() }}` |
| `semver_gte` | Compare a semver string (major.minor) against a target | `{{ semver_gte(extraContext.haproxyVersion, "3.3") }}` (the chart auto-populates `extraContext.haproxyVersion`; outside the chart, set it yourself via `templatingSettings.extraContext.haproxyVersion` — see [Custom Template Variables](#custom-template-variables)) |

!!! note "Pipe operator requires parentheses"
    Scriggo's pipe operator requires a function call on the right side. `{{ value \| toLower }}` is a parse error; write `{{ value \| toLower() }}`. For filters that take additional arguments, the pipe passes the left-hand value as the first argument: `{{ items \| join(", ") }}` is equivalent to `{{ join(items, ", ") }}`.

**sort_by modifiers**: `:desc` (descending), `:exists` (by field presence), `| length` (by length)

**Example - Route precedence sorting**:

```go
{% var sorted = sort_by(routes, []string{
    "$.match.method:exists:desc",
    "$.match.headers | length:desc",
    "$.match.path.value | length:desc",
}) %}
```

## Available Template Data

### Context Variables

All templates have access to the following top-level variables:

| Variable | Type | Description |
|----------|------|-------------|
| `resources` | map of stores | Kubernetes resources indexed per `watchedResources` config — entries are wrappers exposing `.List()` / `.Fetch(keys...)` / `.GetSingle(keys...)` |
| `controller` | map of stores | Controller-managed stores; currently only `controller.haproxy_pods` for the discovered HAProxy pod set |
| `pathResolver` | object | Resolves filenames to HAProxy paths — use `GetPath(name, type)` |
| `capabilities` | map of bools | HAProxy feature flags derived from the local HAProxy version (e.g. `capabilities.SupportsCrtList`). Use for `{% if capabilities.SupportsCrtList %}…{% end %}` branches. |
| `currentConfig` | parsed config (or nil) | The previously-deployed HAProxy configuration as a `*parser.StructuredConfig`. **Nil on first deployment** — guard with `{% if !isNil(currentConfig) %}`. Used for slot-preserving updates. |
| `dataplane` | `config.Dataplane` block | The CRD's `spec.dataplane` block — port, timeouts, paths |
| `shared` | `*SharedContext` | Thread-safe compute-once cache for expensive computations (`shared.ComputeIfAbsent(key, factory)` + `shared.Get(key)`; no `Set` — prevents racy check-then-act patterns) |
| `templateSnippets` | list | Names of all available template snippets — useful for dynamic `render_glob` patterns |
| `runtimeEnvironment` | object | Runtime info exposed by the controller (e.g. `runtimeEnvironment.GOMAXPROCS`) |
| `fileRegistry` | object | Lets templates dynamically register auxiliary files at render time via `fileRegistry.Register("file"/"cert"/"map"/"crt-list", filename, content)`; returns the resolved path. Used by the SSL, haproxytech, and haproxy-ingress libraries to materialise CA bundles, client certs, and SSL crt-lists from Secrets. |
| `http` | object | HTTP fetcher for `http.Fetch("https://example.com/...")`. Always available — URLs are auto-registered the first time a template calls `http.Fetch()` and refreshed periodically per the call's `delay` option. The CRD has no top-level `spec.httpResources`; mocked responses live under `spec.validationTests[].httpResources` for tests only. |
| `extraContext` | map | The full `templatingSettings.extraContext` map. Top-level keys are also injected as bare variables — see below. |

Note: the controller doesn't inject a `haproxyVersion` variable on its own. The Helm chart populates `templatingSettings.extraContext.haproxyVersion` from its `haproxyVersion` value, and `MergeExtraContextInto` flattens every `extraContext` key into the top-level context — so chart-deployed templates can use either `{{ haproxyVersion }}` or `{{ extraContext.haproxyVersion }}`. If you bypass the chart, set the value yourself in `templatingSettings.extraContext.haproxyVersion`. For feature checks prefer `capabilities.*` flags, which the controller derives from the local HAProxy probe.

Custom variables defined in `templatingSettings.extraContext` are also available directly by name. See [Custom Template Variables](#custom-template-variables).

### The `resources` Variable

Templates access watched resources through the `resources` variable. Each store provides `List()`, `Fetch()`, and `GetSingle()` methods.

!!! note
    The keys available under `resources.*` are determined by the `watchedResources` configuration. See [Watching Resources](./watching-resources.md) to add resource types beyond the defaults.

```go
{# List all resources #}
{% for _, ingress := range resources.ingresses.List() %}

{# Fetch by index keys (parameters match indexBy configuration) #}
{% for _, ingress := range resources.ingresses.Fetch("default", "my-ingress") %}

{# Get single resource or nil #}
{% var secret = resources.secrets.GetSingle("default", "my-secret") %}
```

### Typed Resource Access

When a schema is loaded for a watched resource (live in production, or via `--schema-dir` offline), both the `resources.<name>` store wrapper **and** a top-level global named `<name>` return typed pointers instead of `map[string]any`. Field access goes through the strongly-typed struct, so a misspelled field is a compile-time error rather than a silently-`nil` `dig()`.

```go
{# Typed access — fields resolve at engine compile time #}
{%- for _, gw := range resources.gateways.List() %}
  # {{ gw.Metadata.Namespace }}/{{ gw.Metadata.Name }}: {{ len(gw.Spec.Listeners) }} listeners
{%- end %}

{# Identical behaviour via the typed top-level global #}
{%- for _, gw := range gateways %}
  # {{ gw.Metadata.Namespace }}/{{ gw.Metadata.Name }}
{%- end %}
```

**Typed return types.** With a schema loaded, every store method returns typed pointers:

| Call | Return type |
|------|-------------|
| `resources.<name>.List()` | `[]*resources.<name>.T` |
| `resources.<name>.Fetch(keys...)` | `[]*resources.<name>.T` |
| `resources.<name>.GetSingle(keys...)` | `*resources.<name>.T` (nil if not found) |

Without a schema (e.g. `controller validate` without `--schema-dir`), the same calls fall back to `[]any` / `map[string]any` exactly as before. The chart's `dig()`-based snippets work in either mode.

**`<name>.T` is a usable type expression.** Macros, var declarations, type assertions, slice types, and type-switch case clauses all accept it:

```go
{# Macro parameter typed against one kind #}
{% macro RenderGateway(gw *resources.gateways.T) %}
  # gw.Metadata.Name is statically typed here
  # {{ gw.Metadata.Name }}
{% end %}

{# Type-switch dispatch across multiple kinds (polymorphic `any` boundary) #}
{%- switch r := routeInfo["route"].(type) %}
{%- case *resources.httproutes.T %}
  # r is statically *resources.httproutes.T inside this branch
  # {{ r.Metadata.Name }}: {{ len(r.Spec.Rules) }} rules
{%- case *resources.grpcroutes.T %}
  # {{ r.Metadata.Name }} (gRPC)
{%- case *resources.tlsroutes.T %}
  # {{ r.Metadata.Name }} (TLS passthrough)
{%- end %}

{# Slice type for sharded parallel rendering #}
{% var shard []*resources.gateways.T = shard_slice(allGateways, i, n) %}
```

The type-switch case-clause form is the canonical pattern for chart code that crosses a polymorphic `any` boundary — the chart's `gateway` library uses it inside `60-frontend.yaml` to dispatch on HTTPRoute / GRPCRoute / TLSRoute. `shard_slice` is type-preserving: when its input is a typed slice, the result is the same typed slice (not `[]any`), so the downstream loop variable stays statically typed.

**Field name convention:** Go-PascalCase of the JSON tag, with NO acronym preservation. The rule lives in `pkg/k8s/typegen/converter.go::goFieldName` and matters because chart authors are used to upstream Go-style names (`APIVersion`, `IPBlock`) — those don't apply here. (Where the JSON tag already has an uppercase acronym, like `loadBalancerIP`, the typed field keeps it — `LoadBalancerIP` — which happens to match upstream; only rune 0 is ever changed.)

| JSON tag (source YAML)   | Typed field          |
|--------------------------|----------------------|
| `metadata`               | `Metadata`           |
| `spec`                   | `Spec`               |
| `apiVersion`             | `ApiVersion`         |
| `tls`                    | `Tls`                |
| `ingressClassName`       | `IngressClassName`   |
| `matchLabels`            | `MatchLabels`        |
| `clusterIP`              | `ClusterIP`          |
| `loadBalancerIP`         | `LoadBalancerIP`     |
| `kubernetes.io/foo`      | `Kubernetes_io_foo` (non-letter/digit → `_`) |

The no-acronym-dictionary choice is deliberate: there is no translation table to keep in sync. Templates write `gw.ApiVersion`, not `gw.APIVersion`.

**Inside a typed scope** (typed for-range, typed macro parameter, type-switch case branch) use direct field access — no `dig()`, no `tostring()`, no `fallback()` on already-typed primitives. Reach for `dig()` only at genuine polymorphic boundaries (a `routeInfo["route"]` switch entry, an `any` macro parameter, a `shared.Get(...)` return, a ConfigMap with no schema bundled, a `listenerOwner` that may be a Gateway or a ListenerSet, etc.). Mixed-shape chart code — some snippets typed, some not — is the expected adoption pattern, and `dig()` navigates typed structs by JSON tag, so a snippet ported one at a time keeps working without churning its callers.

**Optional fields normalise to nil through `dig()`.** A typegen-produced struct field whose schema entry is *not* in the OpenAPI `required` list carries a `json:"…,omitempty"` tag; `dig()` returns nil when such a field's value is the type's zero value (`""`, `0`, `false`, empty slice). The universal `dig(obj, "field") | fallback(default)` chart pattern therefore behaves identically across typed and untyped shapes — without the normalisation, an unpopulated optional string would return `""`, `fallback()` would skip, and downstream key composition would silently produce malformed strings. Required fields keep their zero values intact.

**Schema source.** Typed shapes are generated from each resource's OpenAPI v3 schema:

- **Production:** the controller fetches schemas live from the kube-apiserver — CRDs via their embedded `openAPIV3Schema`, K8s core resources via the apiserver's OpenAPI v3 endpoint.
- **Offline (`controller validate` / chart `validationTests` / `scripts/test-templates.sh`):** schemas come from a directory passed via `--schema-dir` (or `HAPTIC_SCHEMA_DIR` env var). The directory accepts full CRD YAMLs (`kubectl get crd X -o yaml` output) and bare OpenAPI v3 spec.Schema files with an `x-kubernetes-group-version-kind` extension. Without `--schema-dir`, no resources receive typed support; templates that reach for typed access in that case fail at engine compile time with a clear "no schema for X" pointer back to `--schema-dir`.

This repo's `tests/schemas/` bundles schemas for both the Gateway API CRDs / haptic CRDs *and* the K8s built-ins the chart watches (Namespace, Service, Secret, EndpointSlice, Ingress). All built-ins are CRD-wrapped so the offline GVK resolver picks up the (apiVersion, plural) mapping — `controller validate --schema-dir tests/schemas` therefore unlocks typed access for every chart-watched resource, not just the CRDs. The chart-test script auto-wires this directory; copy it into your own project's schema-dir if you reuse the bundled libraries. To refresh from a running cluster, run `scripts/fetch-k8s-openapi-schemas.sh` (queries `kubectl get --raw '/openapi/v3/...'`, inlines `$ref`s, emits CRD-wrapped YAML).

**Worked example.** `charts/haptic/libraries/gateway/05-typed-access-smoke.yaml` is the canonical single-snippet example — emits one HAProxy comment per Gateway using `gw.Metadata.Namespace` / `gw.Metadata.Name`. Its companion test `test-gateway-typed-access-smoke` pins the wiring end-to-end (engine declarations + runtime bindings + actual render output) and acts as a regression canary for typed access generally.

See also: [ADR-0010 — Typed Watched Resources](../../adr/0010-typed-watched-resources.md) for the design rationale and the alternatives considered.

### Index Configuration

The `indexBy` field determines what parameters `Fetch()` expects:

```yaml
watchedResources:
  ingresses:
    apiVersion: networking.k8s.io/v1
    resources: ingresses
    indexBy: ["metadata.namespace", "metadata.name"]
    # Fetch(namespace, name)

  endpoints:
    apiVersion: discovery.k8s.io/v1
    resources: endpointslices
    indexBy: ["metadata.labels.kubernetes\\.io/service-name"]
    # Fetch(service_name)
```

!!! tip
    Escape dots in JSONPath for labels: `kubernetes\\.io/service-name`

## Custom Template Variables

Add custom variables via `templatingSettings.extraContext`:

```yaml
spec:
  templatingSettings:
    extraContext:
      environment: production
      debug: true
      limits:
        maxConn: 10000
```

Access in templates:

```go
{% if debug %}
  http-response set-header X-Debug %[be_name]
{% end %}

global
  maxconn {{ limits.maxConn }}
```

## Common Patterns

### Reserved Server Slots (Avoid Reloads)

Pre-allocate server slots to enable runtime API updates without reloads:

```go
{%- var initial_slots = 10 %}
{%- var active_endpoints = []map[string]any{} %}

{# Collect endpoints #}
{%- for _, endpoint_slice := range resources.endpoints.Fetch(service_name) %}
  {%- for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
    {%- for _, address := range fallback(endpoint.addresses, []any{}) %}
      {%- active_endpoints = append(active_endpoints, map[string]any{"address": address, "port": port}) %}
    {%- end %}
  {%- end %}
{%- end %}

{# Per-server options like 'check' belong in the surrounding default-server,
   NOT on individual server lines — see the tip below for why. #}
default-server check

{# Fixed slots - active endpoints fill first, rest are disabled #}
{%- for i := 1; i <= initial_slots; i++ %}
  {%- if i-1 < len(active_endpoints) %}
    {%- var ep = active_endpoints[i-1] %}
server SRV_{{ i }} {{ ep["address"] }}:{{ ep["port"] }} enabled
  {%- else %}
server SRV_{{ i }} 192.0.2.1:1 disabled
  {%- end %}
{%- end %}
```

**Benefit**: Endpoint changes update server addresses via runtime API without dropping connections.

!!! tip "Maximize Runtime API Usage"
    Keep server lines minimal - only `address:port` plus `enabled` or `disabled`. Place all other options (`check`, `proto h2`, SSL settings) in the `default-server` directive:

    ```haproxy
    backend my-backend
        default-server check proto h2
        server SRV_1 10.0.0.1:8080 enabled
        server SRV_2 10.0.0.2:8080 enabled
        server SRV_3 192.0.2.1:1 disabled
    ```

    The Dataplane API can update Address, Port, and enabled/disabled state at runtime without reloading HAProxy. Both `enabled` and `disabled` are runtime-supported, enabling the reserved slots pattern. Options like `check` on individual server lines trigger reloads on any change.

### Cross-Resource Lookups

Use fields from one resource to query another:

```go
{% for _, ingress := range resources.ingresses.List() %}
{% for _, rule := range fallback(ingress.spec.rules, []any{}) %}
{% for _, path := range fallback(rule.http.paths, []any{}) %}
  {% var service_name = path.backend.service.name %}
  {% var port = fallback(path.backend.service.port.number, 80) %}

backend ing_{{ ingress.metadata.name }}_{{ service_name }}
    {%- for _, endpoint_slice := range resources.endpoints.Fetch(service_name) %}
    {%- for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
    {%- for _, address := range fallback(endpoint.addresses, []any{}) %}
    server {{ endpoint.targetRef.name }} {{ address }}:{{ port }} check
    {%- end %}
    {%- end %}
    {%- end %}
{% end %}
{% end %}
{% end %}
```

**Required indexing**:

```yaml
watchedResources:
  ingresses:
    indexBy: ["metadata.namespace", "metadata.name"]
  endpoints:
    indexBy: ["metadata.labels.kubernetes\\.io/service-name"]
```

### Safe Iteration

Use `fallback` function to handle missing fields:

```go
{% for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
  {% for _, address := range fallback(endpoint.addresses, []any{}) %}
    server srv {{ address }}:80
  {% end %}
{% end %}
```

### Filtering with Conditionals

Filter resources by attribute presence:

```go
{% for _, rule := range fallback(ingress.spec.rules, []any{}) %}
  {% if rule.http != nil %}
  {# rule.http is guaranteed to exist #}
  {% end %}
{% end %}
```

### Mutable Variables

Accumulate values across loop iterations using Go-style variable assignment:

```go
{% var active_endpoints = []map[string]any{} %}

{% for _, endpoint_slice := range resources.endpoints.Fetch(service_name) %}
  {% for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
    {% active_endpoints = append(active_endpoints, map[string]any{"address": endpoint.addresses[0]}) %}
  {% end %}
{% end %}

{% for i, ep := range active_endpoints %}
  server srv{{ i + 1 }} {{ ep["address"] }}:80
{% end %}
```

### Whitespace Control

```go
{%- for _, item := range items %}   {# Strip before #}
{% for _, item := range items -%}   {# Strip after #}
{%- for _, item := range items -%}  {# Strip both #}
```

## Status Patches

Templates can register status patches for Kubernetes resources using the `statusPatch()` function. The controller applies these patches to the `/status` subresource via Server-Side Apply (SSA) after each reconciliation phase.

This allows templates to report processing results back to resources (e.g., setting `Accepted` and `Programmed` conditions on Gateways, or propagating LoadBalancer addresses to Ingress status) without the controller needing to understand any specific resource's status schema.

### statusPatch()

Registers a status patch for a Kubernetes resource with outcome-keyed variants:

```go
{% statusPatch(namespace, name, apiVersion, kind, map[string]any{
    "deployed": map[string]any{
        "status": map[string]any{
            "conditions": []any{
                condition("Accepted", "True", "Accepted", "Resource accepted", generation, transitionTime(resource, "Accepted", "True")),
            },
        },
    },
    "deployFailed": map[string]any{
        "status": map[string]any{
            "conditions": []any{
                condition("Accepted", "True", "Accepted", "Resource accepted", generation, transitionTime(resource, "Accepted", "True")),
                condition("Programmed", "False", "AddressNotAssigned", "No address available", generation, transitionTime(resource, "Programmed", "False")),
            },
        },
    },
}) %}
```

**Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `namespace` | `string` | Resource namespace |
| `name` | `string` | Resource name |
| `apiVersion` | `string` | Resource API version (e.g., `networking.k8s.io/v1`) |
| `kind` | `string` | Resource kind (e.g., `Ingress`, `Gateway`) |
| `variants` | `map[string]any` | Status payloads keyed by pipeline phase |

**Variants:**

| Key | Applied When |
|-----|-------------|
| `rendered` | After successful template rendering (before deployment) |
| `deployed` | After successful HAProxy deployment |
| `renderFailed` | When a later rendering phase fails |
| `deployFailed` | When HAProxy deployment fails |

Templates render all variants upfront. The controller selects the appropriate variant based on the pipeline outcome.

### condition()

Creates a `metav1.Condition`-compatible map:

```go
{{ condition("Accepted", "True", "Accepted", "Resource is accepted", observedGeneration, lastTransitionTime) }}
```

**Parameters:** `type`, `status`, `reason`, `message`, `observedGeneration`, `lastTransitionTime`

### transitionTime()

Returns the correct `lastTransitionTime` for a condition: preserves the existing timestamp if the condition status hasn't changed, or returns the current time if it has changed or doesn't exist yet:

```go
{{ transitionTime(resource, "Accepted", "True") }}
```

For resources with nested condition arrays (e.g., Gateway API Route `parents[]`), pass the parent index:

```go
{{ transitionTime(resource, "Accepted", "True", parentIndex) }}
```

### Using Status Patches in Custom Templates

Status patch snippets should use the `status-patches-*` extension point (priority 200). This renders after feature analysis but before complex config generation, ensuring patches are captured even if later rendering fails.

```yaml
controller:
  config:
    templateSnippets:
      status-patches-200-custom:
        template: |
          {%- for _, resource := range resources.myresources.List() %}
            {%%
              var ns = resource | dig("metadata", "namespace") | fallback("") | tostring()
              var name = resource | dig("metadata", "name") | fallback("") | tostring()
              var gen = resource | dig("metadata", "generation") | fallback(0)
            %%}
            {%- statusPatch(ns, name, "example.com/v1", "MyResource", map[string]any{
                "deployed": map[string]any{
                    "status": map[string]any{
                        "conditions": []any{
                            condition("Ready", "True", "Deployed", "Successfully deployed", gen, transitionTime(resource, "Ready", "True")),
                        },
                    },
                },
            }) %}
          {%- end %}
```

The built-in Ingress and Gateway API libraries already include status patch snippets. You only need custom status patches for resources not covered by the default libraries.

## Complete Example

Full ingress → service → endpoints chain with reserved slots:

```yaml
watchedResources:
  ingresses:
    apiVersion: networking.k8s.io/v1
    resources: ingresses
    indexBy: ["metadata.namespace", "metadata.name"]
  endpoints:
    apiVersion: discovery.k8s.io/v1
    resources: endpointslices
    indexBy: ["metadata.labels.kubernetes\\.io/service-name"]

maps:
  host.map:
    template: |
      {%- for _, ingress := range resources.ingresses.List() %}
      {%- for _, rule := range fallback(ingress.spec.rules, []any{}) %}
      {{ rule.host }} ing_{{ ingress.metadata.name }}
      {%- end %}
      {%- end %}

templateSnippets:
  backend-servers:
    template: |
      {%- var initial_slots = 10 %}
      {%- var active_endpoints = []map[string]any{} %}
      {%- for _, es := range resources.endpoints.Fetch(service_name) %}
        {%- for _, ep := range fallback(es.endpoints, []any{}) %}
          {%- for _, addr := range fallback(ep.addresses, []any{}) %}
            {%- active_endpoints = append(active_endpoints, map[string]any{"addr": addr}) %}
          {%- end %}
        {%- end %}
      {%- end %}
      {%- for i := 1; i <= initial_slots; i++ %}
        {%- if i-1 < len(active_endpoints) %}
      server SRV_{{ i }} {{ active_endpoints[i-1]["addr"] }}:{{ port }} check
        {%- else %}
      server SRV_{{ i }} 192.0.2.1:1 disabled
        {%- end %}
      {%- end %}

haproxyConfig:
  template: |
    global
        daemon
        maxconn 4096

    defaults
        mode http
        timeout connect 5s
        timeout client 50s
        timeout server 50s

    frontend http
        bind *:80
        use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

    {% for _, ingress := range resources.ingresses.List() %}
    {% for _, rule := range fallback(ingress.spec.rules, []any{}) %}
    {% for _, path := range fallback(rule.http.paths, []any{}) %}
    {%- var service_name = path.backend.service.name %}
    {%- var port = fallback(path.backend.service.port.number, 80) %}

    backend ing_{{ ingress.metadata.name }}
        balance roundrobin
        {{ render "backend-servers" inherit_context }}
    {% end %}
    {% end %}
    {% end %}
```

## See Also

- [Template Engine Reference](https://gitlab.com/haproxy-haptic/haptic/blob/main/pkg/templating/README.md)
- [Scriggo Documentation](https://scriggo.com/templates)
- [HAProxy Configuration Manual](https://www.haproxy.com/documentation/haproxy-configuration-manual/latest/)
