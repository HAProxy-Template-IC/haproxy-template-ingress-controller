# Base library

The base library renders the entire `haproxyConfig` and defines the extension points every other library — and your own snippets — plug into.

## Overview

The base library is **enabled by default** and provides the entire `haproxyConfig` template that the rest of the libraries plug into. It provides:

- Core HAProxy configuration structure (global, defaults, frontends, backends)
- The plugin pattern via extension points for other libraries to inject content
- Frontend routing logic with path matching and backend selection
- Utility macros for template development
- Error page templates
- Map file infrastructure for routing decisions

Every render flows through base — here it's underpinning the Ingress preset live:

<div class="pg-embed" markdown data-scenario="ingress" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Base library underpinning a render" data-height="440">

<p class="pg-task" markdown>In the **Templates** pane, add a `global-settings-500-tuning` snippet under `spec.templateSnippets` (the YAML is in the hint), then watch `tune.bufsize 262144` appear inside the `global` section of the `haproxy.cfg` tab.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

Base's `global-settings` snippet is just `render_glob "global-settings-*"` rendered inside the `global` section, so any snippet whose name starts with `global-settings-` is emitted there in alphabetical order. Add this under `spec.templateSnippets`:

```yaml
global-settings-500-tuning:
  template: |
    tune.bufsize 262144
```

Its `tune.bufsize 262144` line lands in `global` after the built-in path and
process settings. The bundled chart deliberately doesn't emit
`tune.ssl.default-dh-param`: the supported community images use AWS-LC, where
HAProxy doesn't support this setting and warns that the directive was ignored.
An OpenSSL-based deployment that needs finite-field Diffie-Hellman can add its
own `global-settings-*` snippet or, preferably, an explicit
`ssl-dh-param-file`.

</details>

</div>

## Try it: emit a header per map entry

Every base extension point is just a Scriggo snippet that emits HAProxy directives from data. Here is that idea in miniature: iterate an inline map and emit one directive per entry.

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-focus="11-12" data-title="Challenge: turn a map into response headers" data-difficulty="2">

<p class="pg-task" markdown>The `frontend http` section already holds an `extraHeaders` map of header names to values, but emits nothing. Range it with <code>{% for k, v := range extraHeaders %}</code> and emit one `http-response set-header` per entry.</p>

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: extra-headers-demo
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
      frontend http
        bind *:80
      {%- var extraHeaders = map[string]any{
        "X-Frame-Options": "DENY",
        "X-Content-Type-Options": "nosniff",
      } %}
        # TODO(you): emit one http-response set-header per entry in extraHeaders
        default_backend app
      backend app
        server s1 127.0.0.1:8080 check
```

<details class="pg-solution" markdown>
<summary>Peek at the solution</summary>

Loop the map with `{% for k, v := range extraHeaders %}` and show the key and value on an `http-response set-header` line. Go maps have no defined iteration order, so the two headers can render in either order — fine here, since they're independent.

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: extra-headers-demo
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
      frontend http
        bind *:80
      {%- var extraHeaders = map[string]any{
        "X-Frame-Options": "DENY",
        "X-Content-Type-Options": "nosniff",
      } %}
      {%- for k, v := range extraHeaders %}
        http-response set-header {{ k }} {{ v | tostring() }}
      {%- end %}
        default_backend app
      backend app
        server s1 127.0.0.1:8080 check
```

</details>

</div>

## Configuration

The base library has the standard enable/disable flag, but it's rarely useful to disable it: every other library plugs into the extension points base provides, so setting it to `false` produces a broken render with no `haproxyConfig` and no extension points.

```yaml
controller:
  templateLibraries:
    base:
      enabled: true  # Default; leave on unless you supply a complete replacement haproxyConfig
```

## Extension points

The base library defines extension points using the `render_glob "prefix-*"` operator. Any template snippet with a matching prefix is automatically rendered at the designated location in the HAProxy configuration.

### Available extension points

This table is the authoritative registry of every `render_glob` extension point `base.yaml` defines. The [Template Libraries overview](../template-libraries.md#available-extension-points) lists the commonly used subset.

| Extension Point | Prefix Pattern | Location in Config | Purpose |
|-----------------|----------------|-------------------|---------|
| Global Settings | `global-settings-*` | Inside `global` section | Global directives (logging, process, paths, SSL tuning) |
| Defaults Settings | `defaults-settings-*` | Inside `defaults` section | Defaults directives (options, balance, timeouts, errorfiles) |
| Features | `features-*` | Early in config generation | Feature initialization and registration |
| Global Top | `global-top-*` | After `defaults` section | Top-level HAProxy elements (userlists, peers, etc.) |
| HTTP Bind Extra | `http-bind-extra-*` | Inside the outer plaintext TCP frontend, after the chart-static bind | Additional plaintext-HTTP `bind` lines (for example, Gateway HTTP listeners on non-default ports); every added port goes through the same [h2c detection](#h2c-cleartext-detection) |
| Frontend Extra | `frontend-extra-*` | After frontend bind, before routing | Early frontend directives (options, captures, ACLs) |
| Listener Port Translation | `frontend-routing-listener-port-*` | Routing prologue, after `txn.listener_port` is seeded from `dst_port` | Remap `txn.listener_port` when a library binds a pod port that differs from the user-facing listener port (for example, Gateway per-Gateway HTTPS binds) |
| Frontend Matchers | `frontend-matchers-advanced-*` | Within frontend routing logic | Advanced request matching (method, headers, query params) |
| Frontend Filters | `frontend-filters-*` | HTTP frontend, after routing | Request/response filters (header modification, redirects) |
| Access Log Fields | `log-fields-*` | Inside the per-frontend `log-format` line | Named JSON fields contributed to the [structured access log](../haproxy-deployment.md#access-logging) |
| Custom Frontends | `frontends-*` | After HTTP frontend | Additional frontend definitions |
| Custom Backends | `backends-*` | Before `default_backend` | Backend definitions from resource libraries |
| Host Map | `map-host-*` | host.map file | Host-to-group mapping entries |
| Host Regex Map | `map-hostregex-*` | host-regex.map file | Regex hostname fallback entries, tried after the exact and wildcard host lookups miss |
| Path Exact Map | `map-path-exact-*` | path-exact.map file | Exact path match entries |
| Path Prefix Exact Map | `map-pfxexact-*` | path-prefix-exact.map file | Prefix-exact path match entries |
| Path Prefix Map | `map-path-prefix-*` | path-prefix.map file | Prefix path match entries |
| Path Regex Map | `map-path-regex-*` | path-regex.map file | Regex path match entries |
| Weighted Backend Map | `map-weighted-backend-*` | weighted-multi-backend.map file | Weighted routing entries |
| Body Size Map | `map-body-size-*` | body-size.map file | Per-backend request body-size limits (bytes), enforced by `frontend-filters-250-request-body-size` |
| Request Host Map | `map-reqhdr-host-*` | reqhdr-host.map file | Per-backend upstream `Host` header override, applied by `frontend-filters-260-request-set-host` |
| X-Forwarded-Prefix Map | `map-reqhdr-xfwd-prefix-*` | reqhdr-xfwd-prefix.map file | Per-backend `X-Forwarded-Prefix` header, applied by `frontend-filters-261-request-set-xfwd-prefix` |
| Connection Header Map | `map-reqhdr-connection-*` | reqhdr-connection.map file | Per-backend `Connection` header override, applied by `frontend-filters-262-request-set-connection` |
| Path Rewrite Map | `map-path-rewrite-*` | path-rewrite.map file | Per-backend literal full-path rewrite, applied by `frontend-filters-400-path-rewrite` (capture/regex rewrites stay in the backend) |
| Request Buffering Map | `map-request-buffering-*` | request-buffering.map file | Per-backend request-buffering override (`on`/`off`), applied by `frontend-filters-090-request-buffering` |
| Pod Names Map | `map-pod-names-*` | pod-names.map file | Backend pod IP → pod name, read at log time for the `server_pod` access-log field |
| Status Patches | `status-patches-*` | After features, before backends | Resource status patch registration (side effects only) |
| Status Extra | `status-extra-*` | Inside the status frontend | Extra status-frontend directives (Prometheus exporter, custom endpoints) |

The `map-body-size-*` through `map-path-rewrite-*` family shares one design: a resource library writes a per-backend value into a map keyed by backend name, and a static base-library filter looks it up at request time. A backend with no entry is unaffected, so adding or changing one of these values is a map-only (reload-free) change.

Further extension points are defined by other bundled libraries, not by the base library:

- `https-bind-extra-*` — invoked by the SSL library's HTTPS frontend for additional TLS `bind` lines; see [SSL Library](ssl.md).
- `ssl-tcp-bind-extra-*` — also SSL-defined, for the TCP-mode passthrough listener's `bind` lines.
- `backend-directives-*` — invoked by the Ingress library's `backends-500-ingress` snippet (with `inherit_context`) so per-backend annotation libraries can extend each Ingress backend block; see [haproxytech library](haproxytech.md) for the producer side. Templates outside the ingress backend loop won't see it.
- `spoe-agents-*`, `frontend-spoe-filters-*`, `frontend-spoe-set-pass-headers-*`, `frontend-spoe-set-fail-headers-*` — defined by the auto-loaded spoa-hub library; see [SPOA Hub](../operations/spoa-hub.md).

### How extension points work

1. Base library uses `render_glob "prefix-*"` to render all snippets matching the pattern
2. Other libraries (or user config) define snippets with matching prefixes
3. At render time, all matching snippets are rendered in **alphabetical order** — numeric prefixes in snippet names (for example `backends-500-ingress`) control execution order

### Injecting custom configuration

You can inject custom HAProxy configuration by adding template snippets with the appropriate prefix in your values.yaml:

```yaml
controller:
  config:
    templateSnippets:
      # Override default timeouts (replaces the base library snippet)
      defaults-settings-300-timeouts:
        template: |
          timeout connect 5000
          timeout client 30000
          timeout server 30000
          timeout tunnel 600000
          timeout http-request 10000

      # Add custom global tuning directives (extends the global section)
      global-settings-500-tuning:
        template: |
          tune.bufsize 262144
          no-memory-trimming

      # Add custom security rules to the HTTP frontend
      frontend-filters-custom-security:
        template: |
          http-request deny if { path_beg /admin } !{ src 10.0.0.0/8 }
          http-request deny if { path_beg /.env }

      # Add a custom userlist
      global-top-custom-userlist:
        template: |
          userlist api_users
            user apiuser password $2y$05$...

      # Add custom backend
      backends-custom-maintenance:
        template: |
          backend maintenance_backend
              http-request return status 503 content-type text/html string "<h1>Under Maintenance</h1>"
```

### Snippet priority

Snippets within a `render_glob` pattern execute in **alphabetical order**. Encode priority in the snippet name via a numeric prefix (lower numbers run first):

```yaml
templateSnippets:
  # Runs early — sorts before the 500-range
  features-050-ssl-initialization:
    template: |
      {# Initialize SSL infrastructure #}

  # Runs later — sorts after the 100-range certificate registration
  features-150-ssl-crtlist:
    template: |
      {# Generate certificate list #}
```

See [Template Libraries → Snippet Priority](../template-libraries.md#snippet-priority) for the reserved range conventions used by the built-in libraries.

## Features

### Frontend routing logic

The base library implements the routing system using HAProxy maps and transaction variables. The rendered config keeps the per-frontend directives terse; this section is the reference the generated comments link to.

**1. Host matching** — `txn.host_match` is resolved through a fall-through cascade, each step tried only if the previous left it empty (`-m len 0`):

| Order | Lookup | Purpose |
|-------|--------|---------|
| 1 | `host_full` (Host header verbatim, incl. `:port`) | Port-pinned routes — for example Gateway listeners on non-default ports — match before the port-stripped lookups. Only fires when the request actually carried a port, so the common case has zero overhead. |
| 2 | `host` (port stripped) | Normal hostname match. |
| 3 | `host` with leading label removed (`regsub(^[^.]*,,)`) | Wildcard hosts (`*.example.com` stored as `.example.com`). |
| 4 | `host-regex.map` | Regex hostnames. |
| 5 | `host:listener_port` / `:listener_port` | Per-listener-port fallback when no hostname matched (Gateway listeners on dedicated ports). |

`txn.host_match` is seeded to `''` first so every step can use `-m len 0` ("not matched yet") consistently — `-m found` would be true after a lookup that yielded an empty string, blocking the rest of the cascade.

**Listener-port translation.** `txn.listener_port` is the user-facing port the request arrived on. For chart-static binds it equals `dst_port`. Resource libraries that map a pod-port to a different listener port (for example Gateway API per-Gateway HTTPS binds listening on an allocated pod port like `18002` while the map keys use the original `8443`) plug a translation into the `frontend-routing-listener-port-*` extension point. With no such library, `dst_port` passes through unchanged.

**2. Path matching** — evaluated in order Exact > Regex > Prefix-exact > Prefix:

```haproxy
http-request set-var(txn.path_match) var(txn.host_match),concat(,txn.path,),map(maps/path-exact.map)
http-request set-var(txn.path_match) var(txn.host_match),concat(,txn.path,),map_reg(maps/path-regex.map) if !{ var(txn.path_match) -m found }
http-request set-var(txn.path_match) var(txn.host_match),concat(,txn.path,),map(maps/path-prefix-exact.map) if !{ var(txn.path_match) -m found }
http-request set-var(txn.path_match) var(txn.host_match),concat(,txn.path,),map_beg(maps/path-prefix.map) if !{ var(txn.path_match) -m found }
```

!!! note "Overriding Path Match Order"
    Setting `controller.config.templatingSettings.extraContext.routing.regexMatchOrder=last` swaps in the alternate `frontend-routing-logic-regex-last` variant of this snippet at Helm load time, producing performance-first ordering (Exact > Prefix-exact > Prefix > Regex). Faster matchers run first and regex matching is only evaluated as a fallback. The variant snippet is unset before the merged config is rendered; the effective setting remains visible in `extraContext`.

**3. Qualifier system** — the first `:`-separated field of `path_match` selects the routing mode: `BACKEND:<name>` routes directly, `MULTIBACKEND:<weight>:<key>` selects a weighted backend via random draw. Advanced matchers (method / header / query, contributed by the gateway library through `frontend-matchers-advanced-*`) may rewrite `path_match`, so the qualifier is re-parsed after they run.

**Owner-resource identity.** `txn.resource_id` (`<namespace>/<name>`) is derived from the qualifier value the routing chain already produced — no extra map lookup — and keys the per-resource feature maps (auth, the Coraza web application firewall, body-size, header rewrites). Backend names use `_` as the separator (`<ns>_<name>_svc_<svc>_<port>` for Ingress, `<ns>_<name>_<ruleIdx>` for Gateway weighted routing); Kubernetes names disallow `_`, so splitting on `_` is collision-free.

### Connection reliability and timeouts

The defaults section is tuned for a Kubernetes ingress workload, where backends are pod IPs from EndpointSlices reached directly over the cluster's Container Network Interface (CNI) fabric.

**`timeout connect` defaults to `100ms`** (most controllers ship HAProxy's `5s`). Same-node connects are sub-millisecond, cross-node overlay networking (flannel/calico/cilium) is typically under `30ms`, and even AWS cross-AZ stays under ~`10ms` p99 — so `100ms` is already several times the normal case. The case it deliberately fails fast on is a TCP `SYN` to a pod IP whose pod just terminated: during the brief window between an EndpointSlice update and HAPTIC's runtime update landing, a server slot can still point at a dying pod. A request already dispatched into HAProxy is committed to that slot and must wait out `timeout connect` before `option redispatch` retries it elsewhere. At `5s` that surfaces as a client-visible 504; at `100ms` the full failover (original attempt + retry) completes within ~`200ms`.

Override it for genuinely slow networks (multi-region, satellite, constrained CPU):

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        timeout_connect: "5000"   # ms; also timeout_client / timeout_server / timeout_http_request / timeout_http_keep_alive
```

**`option redispatch`** lets a failed TCP connect be retried against a *different* server in the backend rather than the same dead one. Combined with HAProxy's default `retries 3`, the retry lands on a healthy slot instead of hanging on the dead IP until the client times out. See HAProxy's [retries documentation](https://www.haproxy.com/documentation/hapee/latest/service-reliability/retries/retries/).

**`retry-on conn-failure empty-response response-timeout`** covers the rest of the pod-termination race. A connect failure is only half of it: a terminating pod usually still has its listening socket bound after the application has stopped, so the kernel completes the handshake and the application then resets the connection. HAProxy logs that as a 502 with termination state `SH--`, `t_connect 0` and `retries 0` — the default `retry-on conn-failure` doesn't match it, because the connection *succeeded*. `empty-response` is the condition that does, which is what makes `option redispatch` and `retries 3` engage. Override the condition list with `extraContext.retryOn`.

Retries that replay the request are limited to idempotent methods. An L7 retry re-sends a request the server has already received, so retrying a `POST` or `PATCH` can submit it twice; GET, HEAD, PUT, DELETE, OPTIONS and TRACE are idempotent per [RFC 9110 §9.2.2](https://www.rfc-editor.org/rfc/rfc9110#section-9.2.2) and are retried. This matches nginx-ingress, which excludes non-idempotent requests from `proxy_next_upstream` unless you add `non_idempotent`. `conn-failure` is an L4 retry and is unaffected, so a failed connect is still sent to another server for every method. Set `extraContext.retryNonIdempotent: true` to retry every method — only when every backend behind the controller is safe to replay.

### `h2c` cleartext detection

The plaintext HTTP entry point is an outer `mode tcp` frontend that inspects the first wire bytes and routes to one of two unix-socket-bound inner `mode http` frontends, preserving the original client IP via PROXY-protocol v2 across the hop. Both inner frontends share the same routing logic, so any HTTP-level snippet lands in both protocol paths.

HAProxy can't auto-detect HTTP/2 cleartext (h2c) on a plaintext bind, and it can't parse an `Upgrade: h2c` handshake in `mode tcp`. The only available signal is the HTTP/2 *prior-knowledge* connection preface — the 24 bytes `PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n` — which the outer frontend matches byte-exactly with an `acl ... req.payload(0,24) -m bin <hex>`. gRPC-Go's insecure dial uses prior-knowledge by default, and the Gateway API conformance suite dials every GRPCRoute test with `insecure.NewCredentials()`, so this path is exercised by all gRPC conformance tests. The connection is classified as soon as 24 bytes arrive (with a `WAIT_END` fallback for shorter HTTP/1.1 sends); the ~10µs unix-socket round-trip is invisible against backend latency.

### gRPC request handling

The `default_backend` returns a gRPC-aware fallback for unmatched requests. For `application/grpc` requests it returns a trailers-only response (`grpc-status: 12`, Unimplemented) instead of a plain 404 — without a valid `content-type`, grpc-go reports "malformed header: missing HTTP content-type" and tears down the whole HTTP/2 connection, cancelling every multiplexed stream on it. Because HAProxy's `http-request return` strips `content-type` from its header arguments, the base library re-adds it with `http-after-response set-header`, which runs on responses produced by the `return` action. Non-gRPC requests get a plain `404`.

### Request buffering

HAProxy waits for the request body before it takes a backend connection, so a client that trickles its upload holds an HAProxy buffer instead of a backend server slot. This is the standard defence against the slow POST attack, where an attacker declares a large body and sends it a byte at a time to exhaust the application's worker pool.

Buffering is on by default. Turn it off fleet-wide, or change how long HAProxy waits:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        requestBuffering:
          enabled: true
          waitTimeout: 10s
```

Override it for a single route with the [`haproxy-haptic.org/request-buffering`](haptic-annotations.md#request-buffering) annotation (`on` or `off`). The override lives in `request-buffering.map`, so changing one route's setting reloads nothing, and it's authoritative: a route set to `off` isn't buffered even when a sibling route on the same frontend enables [request mirroring](haptic-annotations.md#canary-and-traffic-mirroring), which buffers bodies of its own accord. The consequence for a mirrored route that opts out is that its mirror receives an empty body.

HAProxy releases the request as soon as *either* the body is complete or `tune.bufsize` is full, so this is slow-client protection rather than an upload buffer — a 1 GB upload proceeds once the first 16 KiB arrive. When the wait expires with neither condition met, the client gets a `408` and the backend is never contacted.

#### Streaming requests are never buffered

Only requests that declare a `Content-Length` are held. Nothing that streams can know its length in advance, so this single condition excludes every streaming protocol without naming any of them.

That exclusion is load-bearing rather than cosmetic. Buffering a bidirectional stream deadlocks it: the client sends its first message and waits for a response, but HAProxy hasn't forwarded the request headers yet, so the backend never sees the call and never answers. Neither side can make progress until the wait expires and the client receives a `408` that the backend never produced.

gRPC sends neither `Content-Length` nor `Transfer-Encoding` — for unary and streaming calls alike — so no gRPC request is ever buffered. Chunked HTTP/1.1 uploads are excluded on the same rule, which also covers long-poll and command-channel patterns where the server answers before the request body ends.

Setting the annotation to `on` therefore can't break a streaming route. Use `off` for a route whose clients *do* declare a `Content-Length` but still expect a response before the body ends, such as a resumable-upload endpoint.

### Built-in operators and functions

#### `render_glob` (operator)

Renders all snippets matching a glob pattern. This is a built-in Scriggo operator, not a macro — no import is needed:

```scriggo
{{ render_glob "backends-*" }}
{{ render_glob "map-host-*" inherit_context }}
```

See the [Scriggo template guide](../templating.md) for details.

#### `sanitize_regex` (function)

Built-in function that escapes regex metacharacters so a user-supplied literal (a host or path containing `.`, `+`, …) can be safely embedded in a `map_reg()` lookup. Edit the value and watch each metacharacter pick up a backslash:

<div class="pg-embed" markdown data-scriggo data-title="sanitize_regex escapes a literal for map_reg" data-height="320">

```go
{# sanitize_regex(s) escapes every regex metacharacter in s (regexp.QuoteMeta),
   so the string matches literally inside a map_reg() lookup. Edit it. #}
{%- var literal = "/api.v1/users" -%}
{{ sanitize_regex(literal) }} api_backend
```

</div>

!!! warning "Config output isn't auto-escaped"
    The rendered HAProxy config is plain text — template output is never escaped for it (Scriggo only context-escapes the `html`/`css`/`js` format types, and the `haproxyConfig` template uses none of them). A user-supplied value (annotation, header, host, cookie) that carries a newline can split a config line and smuggle a second directive that still passes `haproxy -c`. When you interpolate an unchecked value onto a config line in your own snippet, guard it: use `sanitize_regex` for a `map_reg()`/regex context (it escapes the value, neutralizing metacharacters), and the `ValidateConfigValue` / `ValidateCidrList` macros from the Ingress annotations-compat library for single-token fields (SNI, cipher, cookie domain/path, header values) and `src` CIDR lists (both `fail()` the whole render on a control-character or out-of-charset breakout). The bundled vendor libraries already route their annotation values through these guards.

### Utility macros

The base library provides reusable macros across several `util-*` snippets, imported by other libraries:

| Macro | Purpose |
|-------|---------|
| `CalculateShardCount(resourceCount, itemsPerShard)` | Computes `clamp(count / itemsPerShard, 1, 2*GOMAXPROCS)` |
| `HostMatchCondition(hosts)` | Builds a host-match ACL condition (in `util-ingress-helpers`) |
| `BuildServerOptions(serverOpts)` | Renders server-line option flags (in `util-backend-servers-helpers`) |
| `BackendServers(serviceName, maxSlots, port, opts, portName, backendName, namespace)` | Generates the full server pool with reserved slots (in `util-backend-servers`) |

Usage:

```scriggo
{%- import "util-backend-servers" for BackendServers %}
{{ BackendServers(serviceName, 10, port, serverOpts, nil, backendKey, namespace) }}
```

### Backend server pool

The `util-backend-servers` snippet generates server lines with:

- Pre-allocated server slots for dynamic scaling
- Health check configuration
- Support for per-server options (`maxconn`, SSL, etc.)

The snippet holds only the `BackendServers` macro, so import it and call the macro — rendering the snippet emits nothing:

```scriggo
{%- var service_name = "my-service" %}
{%- var port = 8080 %}
{%- import "util-backend-servers" for BackendServers %}
{{ BackendServers(service_name, 0, port, serverOpts, nil, backendName, namespace) }}
```

### Error pages

Pre-configured error response templates for common HTTP errors:

| File | HTTP Status |
|------|-------------|
| 400.http | Bad Request |
| 403.http | Forbidden |
| 408.http | Request Timeout |
| 500.http | Internal Server Error |
| 502.http | Bad Gateway |
| 503.http | Service Unavailable |
| 504.http | Gateway Timeout |

### Structured access log

Every frontend emits one JSON object per request, assembled from HAProxy's native
JSON log encoding. `base.yaml` owns the core field set — request identity,
timers, the owning Kubernetes resource, and `denied_by` — and every library adds
fields for the features it implements through the `log-fields-*` extension point,
each gated on that feature actually being configured.

Add your own fields without writing a snippet:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        accessLog:
          fields:
            tenant: req.hdr(X-Tenant)
```

Or contribute one from a library snippet:

```yaml
controller:
  config:
    templateSnippets:
      log-fields-900-my-feature:
        template: |
          %(my_field)[var(txn.my_var)]
```

<div class="pg-embed" markdown data-scenario="ingress" data-tab="haproxy.cfg" data-controls="tabs" data-title="Challenge: add a field to the access log" data-difficulty="2" data-height="440">

<p class="pg-task" markdown>In the **Templates** pane, add a `log-fields-900-scheme` snippet under `spec.templateSnippets` that contributes a `scheme` field, then find it inside the `log-format` line of each HTTP frontend in the `haproxy.cfg` tab.</p>

<details class="pg-solution" markdown>
<summary>Solution</summary>

A `log-fields-*` snippet emits items, not directives. `ssl_fc` is connection-scoped, so it's available at log time and needs no transaction variable:

```yaml
log-fields-900-scheme:
  template: |
    %(scheme:bool)[ssl_fc]
```

Band 900 sorts after every bundled contribution, so the field lands at the end of
the record. Typing it `:bool` is safe here because `ssl_fc` always resolves —
`false` on a plaintext connection. Try `%(scheme)[req.hdr(X-Forwarded-Proto)]`
instead and the render fails: HAProxy rejects request-header fetches inside a
`log-format`, which is why request-scoped values go through
`http-request set-var(txn.…)` first.

</details>

</div>

A `log-fields-*` snippet emits named log-format items and nothing else. Only
items available at log time are legal: HAProxy rejects `path`, `pathq`,
`req.hdr()`, `res.hdr()` and `req.ssl_sni` inside a `log-format`, so materialise
request- or response-scoped values into a transaction variable first. Because the
assembled format string is shared by every frontend, a snippet must not branch on
which frontend is rendering — that's what keeps one schema across the whole log
stream.

See [Access logging](../haproxy-deployment.md#access-logging) for the field
reference, the `denied_by` values, request-id and trace-context behaviour, and
how to replace the format wholesale.

### Debug headers

When debug mode is enabled, the frontend adds response headers for routing introspection:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        diagnostics:
          routingHeaders:
            enabled: true
```

Debug headers include:

- `X-HAProxy-Backend`: Selected backend name
- `X-HAProxy-Host-Match`: Matched host group
- `X-HAProxy-Path-Match`: Full path match result
- `X-HAProxy-Path-Match-Qualifier`: BACKEND or MULTIBACKEND

### Shared memory stats (HAProxy 3.3+)

When `haproxy.shmStats.enabled` is `true` and HAProxy version is 3.3 or later, the base library adds `shm-stats-file` and `shm-stats-file-max-objects` to the global section. This persists stats counters (frontend/backend/server metrics) across HAProxy reloads via shared memory, eliminating counter resets during configuration changes.

The `shm-stats-file-max-objects` value is a configurable fixed value (default 50000) set via `haproxy.shmStats.maxObjects`. Since the shm-stats file is fixed-size and can't be resized on reload, a large fixed value prevents reload failures when new ingresses are added. HAProxy allocates object slots lazily, so memory overhead is proportional to actual objects, not the configured maximum.

When `shmStats` is enabled, the chart automatically adds a `/dev/shm` emptyDir volume with `medium: Memory` to the HAProxy pod. The volume's `sizeLimit` is auto-calculated from `maxObjects` (~4KB per object with 10% margin), or can be overridden via `haproxy.shmStats.shmSizeLimit`. This volume counts against the pod's memory limit.

### Address discovery

The base library watches controller LoadBalancer Services and discovers external addresses for status reporting. Addresses are aggregated from **all** matching services and deduplicated, then stored in `gf["addresses"]`. This supports multi-service setups where HAProxy is exposed via both internal and public LoadBalancers.

Controller Services are discovered via label selector (`app.kubernetes.io/name=<name>,app.kubernetes.io/component=loadbalancer`). If no Service has LoadBalancer addresses assigned yet, the library falls back to the Services' own `spec.clusterIPs` (skipping the headless `None` sentinel), so status still carries an address on NodePort and ClusterIP installs. While status patches are enabled, `gf["addresses"]` is always set — to an empty list if nothing is discoverable — because every `status-patches-*` snippet gates on it being non-nil, and leaving it nil would suppress conditions the Gateway API requires regardless of addressing (such as `Accepted=True`).

Address discovery can be disabled via `controller.config.templatingSettings.extraContext.statusPatches.enabled: false`. When disabled, `gf["addresses"]` is never set, which prevents all `status-patches-*` snippets from writing to Ingress or Gateway status. This is useful during migration from another ingress controller to avoid premature DNS cutover when tools like external-dns watch status fields.

### Status-patch extension point

The `status-patches-*` extension point renders at priority 200 — after feature analysis (`features-*` at 050-150) but before backends and frontends (500+). This ensures status patches are captured even when later config generation fails, allowing the `renderFailed` variant to be applied.

Status patch snippets produce no HAProxy configuration output. They call `statusPatch()` as a side effect to register patches for later application by the controller.

### Declarative Kubernetes Resources (`k8sResources.haproxy-service`)

The base library declares the user-facing HAProxy LoadBalancer Service under the controller CRD's top-level `spec.k8sResources` map (sibling of `templateSnippets`, `maps`, `files`, `sslCertificates`). The controller's renderer parses the rendered YAML and applies the resulting Service via Server-Side Apply with field manager `haptic`; an `OwnerReference` to the `HAProxyTemplateConfig` CR is injected automatically (controller=true, blockOwnerDeletion=true) so cascade-delete (for example `helm uninstall`) garbage-collects the Service.

This replaces the chart-static `templates/haproxy-service.yaml` main-Service block, which was emitted by Helm at install time with a fixed port set. Two operational consequences operators should be aware of:

- **First-render delay**: when the chart is installed for the first time, the Service doesn't exist until the controller renders once (typically a few seconds). External tools (cert-manager, external-dns) that look up the Service immediately after `helm install` should expect a brief absence. The internal `<release>-haproxy-dataplane` Service remains chart-static and is created at install time as before.
- **`helm uninstall` cleans up via cascade-GC**: the `OwnerReference` ties the Service's lifecycle to the CR. When `helm uninstall` removes the CR, the Service goes with it; no extra cleanup hook is required.

The Service port set is a single list assembled by the chart at install / upgrade time. There is no static / dynamic split inside the Scriggo template — `templates/haproxytemplateconfig.yaml` builds the list and hands it to the renderer via `extraContext.haproxyService.ports`. Two stages contribute:

1. **Chart-time defaults + extras (Helm)**. The chart reads `haproxy.service.{http,https,stats}.{port,nodePort}` and assembles three default entries (`http`, `https`, `stats`), then appends every entry in `haproxy.service.extraPorts`. The result is a flat list of `corev1.ServicePort`-shaped objects.

    ```yaml
    haproxy:
      service:
        # Defaults — change the port number / nodePort here, or set
        # port: 0 to drop the entry from the rendered Service:
        http:
          port: 80
          nodePort: 30080
        https:
          port: 443
          nodePort: 30443
        stats:
          port: 8404
          nodePort: 30404

        # Add your own ports — same shape as corev1.ServicePort:
        extraPorts:
          - name: postgres
            port: 5432
            targetPort: 5432
            protocol: TCP
            nodePort: 30432   # only honored when service.type is NodePort/LoadBalancer
    ```

    `name` and `port` are required; `targetPort` defaults to `port`; `protocol` defaults to `TCP`; `appProtocol` is plumbed through verbatim when set. Names must be RFC 1123 labels and unique across all Service ports (including the Gateway-derived `gw-*` entries below). To drop one of the defaults entirely, set the matching `haproxy.service.<name>.port` to `0` — the chart filters zero-port entries out at render time.

2. **Render-time Gateway-derived ports (Scriggo)**. For each non-default Gateway / admitted ListenerSet listener port, the `k8sResources.haproxy-service` template appends a `gw-<port>-<proto-letter>` entry (for example `gw-9090-h` for an HTTPS listener on port 9090). Listener ports that already exist in the chart-time list (because their port number matches `http` / `https` / `stats` or one of the operator's `extraPorts` entries) are skipped — first writer wins. Skipped entirely for Gateways that set `spec.addresses` (those get a dedicated per-Gateway Service via `k8sResources.gateway-static-addresses`).

Chart values consumed by the merged spec: `haproxy.service.type`, `haproxy.service.annotations`, `haproxy.service.loadBalancerIP`, `haproxy.service.loadBalancerClass`, `haproxy.service.loadBalancerSourceRanges`, `haproxy.service.externalTrafficPolicy`, `haproxy.service.internalTrafficPolicy`, `haproxy.service.healthCheckNodePort`, `haproxy.service.publishNotReadyAddresses`. All are plumbed into `extraContext.haproxyService` by `templates/haproxytemplateconfig.yaml`.

## Map files

The base library generates these map files for routing:

| Map File | Purpose | Matcher |
|----------|---------|---------|
| host.map | Host header to group mapping | Exact match |
| host-regex.map | Regex hostname fallback (multi-label hosts under a wildcard listener) | `map_reg()` |
| path-exact.map | Exact path matching | `map()` |
| path-prefix-exact.map | Prefix paths that should match exactly | `map()` |
| path-prefix.map | Prefix path matching | `map_beg()` |
| path-regex.map | Regex path matching | `map_reg()` |
| weighted-multi-backend.map | Weighted backend selection | `map()` |
| pod-names.map | Backend pod IP to pod name, for the access log's `server_pod` field and the upstream span | `map()` |

And these per-backend feature maps, all keyed by backend name and looked up with `map()`:

| Map File | Purpose |
|----------|---------|
| body-size.map | Request body-size limit in bytes |
| request-buffering.map | Per-route request-buffering override (`on`/`off`) |
| reqhdr-host.map | Upstream `Host` header override |
| reqhdr-xfwd-prefix.map | `X-Forwarded-Prefix` header value |
| reqhdr-connection.map | `Connection` header override |
| path-rewrite.map | Literal full-path rewrite |

## HAProxy configuration structure

The base library generates this configuration structure. The `global` and `defaults` sections are composed from individually overridable snippets:

```haproxy
global
    # global-settings-100-logging — `format raw` so JSON records carry no
    # syslog prefix; len from extraContext.accessLog.maxLineBytes
    log stdout len 16384 format raw local0 info
    # global-settings-200-process
    daemon
    # nbthread is omitted by default (no CPU limit) so HAProxy auto-detects all
    # node cores; when haproxy.resources.limits.cpu is set it renders ceil(limit)
    # global-settings-250-shm-stats (when haproxy.shmStats.enabled=true and HAProxy >= 3.3)
    shm-stats-file /dev/shm/haproxy-stats
    shm-stats-file-max-objects 50000  # configurable via haproxy.shmStats.maxObjects
    # global-settings-300-paths — emits the BaseDir from pathResolver (chart default /etc/haproxy)
    default-path origin /etc/haproxy
    crt-base ssl/                              # relative to default-path origin
defaults
    # defaults-settings-100-options
    mode http
    log stdout len 16384 format raw local0 info   # one line per extraContext.accessLog.targets entry
    option httplog          # unreachable fallback: every frontend sets its own
                            # log-format, and this keeps HAProxy from warning if
                            # one ever doesn't
    option dontlognull
    option log-health-checks
    # defaults-settings-150-access-log-request-id
    unique-id-format %[uuid(7)]
    # defaults-settings-200-balance
    balance roundrobin
    # defaults-settings-300-timeouts
    timeout connect 100
    timeout client 50000
    timeout server 50000
    # defaults-settings-400-errorfiles — relative paths resolved via default-path origin
    errorfile 400 general/400.http
    # ... other error files

# global-top-* snippets here (userlists, etc.)

frontend status
    mode http
    bind *:8404
    option dontlog-normal
    log-format "%{+json}o ..."   # util-log-format-http (every HTTP frontend)
    # Health check endpoints

frontend http-tcp
    mode tcp
    option dontlog-normal        # the inner HTTP frontend logs the request
    log-format "%{+json}o ..."   # util-log-format-tcp (every TCP frontend)
    bind *:80                    # extraContext.httpPort — the port bind lives
                                 # on this outer h2c demultiplexer
    # http-bind-extra-* snippets (extra Gateway HTTP listener ports)

frontend http_frontend
    mode http
    option forwardfor
    bind unix@/etc/haproxy/http-h1-frontend.sock mode 660 accept-proxy
    # frontend-extra-* snippets (options, captures, ACLs)
    # Routing logic
    # frontend-matchers-advanced-* snippets
    # frontend-filters-* snippets
    use_backend %[var(txn.backend_name)] if { var(txn.backend_name) -m found }
    default_backend default_backend

# frontends-* snippets (HTTPS, TCP, etc.)

# backends-* snippets (resource-specific backends)

backend default_backend
    http-request return status 404
```

Each `global-settings-*` and `defaults-settings-*` snippet can be individually overridden or extended via `controller.config.templateSnippets` in your values.yaml. For example, to customize timeouts, override `defaults-settings-300-timeouts` with your own values. To add new global directives, create a `global-settings-500-tuning` snippet (or any name matching the pattern).

## See also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [SSL Library](ssl.md) - TLS certificate management and HTTPS frontend
- [Template Libraries → Path Matching Order](../template-libraries.md#path-matching-order) - Switching path-matching order
