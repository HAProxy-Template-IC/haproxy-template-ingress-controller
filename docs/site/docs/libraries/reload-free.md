# Reload-free route propagation

Use the base library's macros to describe backends and map entries that HAPTIC
can update through the HAProxy Runtime API. This guide shows how to keep changing
route values separate from the configuration rules that use them, so eligible
changes don't require a reload.

The same macros accept data from an Ingress, a Gateway route, or your own custom
resource. The examples below build the same backend from three input types.

## The macros

Use the base library's `Backend()` to describe a backend and `RegisterMap()`
to register map entries. `HeaderModifierRules()` generates rules that read header
values from a map. For Service and EndpointSlice resolution, the separate
`kubernetes-backends` library provides `BackendServers()`.

```
{{ Backend(map[string]any{
     "name":     beName,            # required; the backend section name
     "mode":     "http",            # http|tcp, default http; carried by the profile
     "balance":  "roundrobin",      # use consistent hashing with hash-based algorithms
     "profile":  profileLines,      # []string: directives shared by same-shape backends (timeouts, retries, cookie, http-request rules) → a named defaults
     "body":     bodyLines,         # []string: directives that must stay in THIS section (stick-table, filter, raw injections) → structural
     "servers":  BackendServers(serviceName, 0, port, serverOpts, portName, beName, namespace),
   }) }}
{{ RegisterMap("my-route.map", entries, map[string]any{"ordered": false}) }}
```

Every backend inherits a content-addressed named `defaults haptic-be-<hash> from
haptic-base` (`backend <name> from haptic-be-<hash>`); `mode`, `balance`,
`hash-type`, `default-server` and the `profile` lines live there, so two
backends of the same shape share one profile section and a route of an existing
shape is added at runtime without a reload. The backend section itself is only
`from`/`guid`/`body`/servers — keep `body` empty for a dynamic-eligible backend.

`Backend()` is strict: it accepts `name`, `mode`, `balance`, `hashType`,
`profile`, `body`, `servers`, `defaultServer`, `guid`, `comments`, `shape` and
`shapeReason`, and fails the render on any other key. Put raw server lines in `body`; that
makes the backend structural.

## Custom CRD first

Suppose you watch a `Route` CRD with `spec.backend.address`, `spec.backend.port`,
and `spec.requestHeaders`. This example uses `resource("routes")` and `dig()`
to work without a bundled schema. In a cluster, HAPTIC can also provide typed
access from the CRD's OpenAPI schema. Generate one
backend per Route with a literal server list, and move its headers into a map
keyed on the backend name:

```
{%- import "util-backend" for Backend -%}
{%- import "util-register-map" for RegisterMap -%}
{%- import "util-header-modifier-rules" for HeaderModifierRules -%}
{%- var setNames = []string{} -%}
{%- var entries = []string{} -%}
{%- for _, r := range resource("routes") -%}
  {%- var be = tostring(dig(r, "metadata", "namespace")) + "_" + tostring(dig(r, "metadata", "name")) -%}
  {{ Backend(map[string]any{
       "name":    be,
       "shape":   "dynamic",
       "servers": []any{map[string]any{"name": "primary", "address": tostring(dig(r, "spec", "backend", "address")), "port": toint(dig(r, "spec", "backend", "port"))}},
     }) }}
  {%- for _, h := range toSlice(dig(r, "spec", "requestHeaders")) -%}
    {%- var hn = tostring(dig(h, "name")) -%}
    {%- setNames = append(setNames, hn) -%}
    {%- entries = append(entries, be + "|set|" + toLower(hn) + " " + queryEscape(tostring(dig(h, "value")))) -%}
  {%- end -%}
{%- end -%}
{%- var mapPath = RegisterMap("route-reqhdr.map", entries, map[string]any{"ordered": false}) -%}
{{ HeaderModifierRules("request", "var(txn.backend_name)", mapPath, setNames, []string{}, []string{}) }}
```

Adding a Route that reuses a header name is now a map entry, not a config line.
The value is URL-encoded at the writer (`queryEscape`) and decoded at request
time (`HeaderModifierRules` appends `url_dec(1)`), so a space, a `;` or a `%` in
the value can neither split the map line nor read request state. A backend, its
map build, and the static line render in different passes — see the bundled
`custom-crd-example` library (enable `controller.templateLibraries.customCrdExample`)
for the working, tested split.

## The same for Ingress and Gateway

The bundled libraries build the identical shape from their own inputs:

- **Ingress** (`ingress.yaml`, and the annotation libraries) generates one
  backend per `spec.rules[].http.paths[].backend`, moves `request-set-header` /
  `response-set-header` annotation values into `ing-reqhdr.map` / `ing-reshdr.map`,
  and puts the settable server/tunnel timeouts in `backend-timeouts.map`.
- **Gateway API** (`gateway/`) generates one backend per HTTPRoute backendRef and
  moves RequestHeaderModifier, RequestRedirect, URLRewrite, RequestMirror and
  Gateway Enhancement Proposal 1742 timeouts into `gw-*.map`.

Because all three call the same macros, the reload behaviour below is the same
whichever resource you watch.

### See both macros live

The bundled Ingress library builds every render from the same two macros:
`Backend()` assembles each `backend` section, and `RegisterMap()` writes the
host and path maps that route to them. Run this render, then edit a resource and
watch both follow — reload-free.

<div class="pg-embed" markdown data-scenario="ingress" data-facade="spec.templateSnippets.backends-500-ingress" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Backend() and RegisterMap() in one render" data-height="480">

<p class="pg-task" markdown>Press **Run live**. In the **haproxy.cfg** tab, find the `backend storefront_shop_svc_shop_http` section that `Backend()` assembled from the `shop` Ingress, with one pod-named `server` line per endpoint. Switch to the **maps** tab to see the `host.map` and `path-prefix.map` entries that `RegisterMap()` wrote to route to it. Then, in the **Resources** panel, change the `shop` Ingress's host to `store.example.com` and Run again — the map entry follows, with no new `backend` and no reload.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

`backends-500-ingress` calls `Backend()` once per unique `(namespace, ingress, service, port)`, so `haproxy.cfg` carries one `backend storefront_shop_svc_shop_http`. `map-host-500-ingress` and `map-path-prefix-500-ingress` call `RegisterMap()` to write `host.map` and `path-prefix.map`; changing the host rewrites one unordered map entry — a runtime map update, not a config-section change, so HAProxy keeps serving without a reload.

</details>

</div>

## When a backend is static

A backend is eligible for runtime creation when `Backend()` describes it as
`dynamic`, its named defaults profile is already loaded, and the pod runs
HAProxy 3.4 or later. These changes require a reload:

| Condition | What requires a reload |
|-----------|------------------------|
| HAProxy 3.0–3.3 | Creating or deleting a backend. |
| Non-empty `body`, such as a local `stick-table`, `filter`, or raw configuration injection | Creating, deleting, or changing that structural backend. |
| New or changed named defaults profile | Loading the profile. Later backends can reuse it at runtime. |
| Changed backend mode, profile, `guid`, balance algorithm, `hashType`, or `defaultServer` | Updating that backend. |
| `static-rr` or map-based hashing | Adding a server. |
| Server keywords unsupported by `add server`, such as `no-check`, `resolvers`, or `init-addr` | Adding the server, including as part of a new backend. |
| Configuration emitted outside the structured helpers | Changing that configuration text. |

A runtime command can still fail, for example if a backend name is already in
use or a static reference prevents deletion. The agent then attempts a reload.

Server address, port, weight, and maintenance-state changes can run at runtime
in both structural and dynamic backends. Changing other keywords on an existing
server requires a reload, even if those keywords support runtime creation.
`ssl-min-ver`, `ssl-max-ver`, `ca-file`, and `crt` support runtime creation;
referenced files must already exist in the runtime store or be created in the
same apply. See [Supported configuration](../supported-configuration.md).

## Where to put a directive

The reload behaviour of a directive is decided by which slot of `Backend()` you
put it in.

| Put it in | For | Change behaviour |
|---|---|---|
| `profile` | Value-free or per-value directives shared by every backend of one shape: timeouts, cookies, retries, `http-request`/`http-check` rules, health-check specs | A new profile reloads once; from then on every backend on it becomes dynamic, and changing a profile value reloads that one profile |
| a map + one static line | Per-route/per-backend values read at request time: header modifiers, path rewrites, redirect targets, timeouts (via `map_str_int`) | Adding or editing an entry is a map-only change — no reload |
| the profile's `default-server` line | Shared server keywords, such as `check`, `maxconn`, `ssl`, and `send-proxy` | HAPTIC passes these keywords to runtime server creation. Changing the defaults profile requires a reload. |
| `body` | Directives that must stay in this section: `stick-table`, `filter`, `use-server`, raw operator injections | Makes the backend structural — create/delete/body change reload |

The bundled libraries put shared server settings in `default-server` and
backend settings in a named defaults profile. The controller copies those
server defaults into runtime `add server` commands, which don't inherit them
from the backend.

Keep per-route values in maps when HAProxy can read them at request time.
Reserve `body` for directives that must appear in the backend itself.

Session persistence (`cookie … dynamic`) needs a `dynamic-cookie-key`. It's
shared per installation so same-shape cookie backends share one profile, and
derived from the release identity so it's stable across upgrades. This derived
value isn't a secret. Override it with
`controller.config.templatingSettings.extraContext.dynamicCookieKey` (for a
hand-written CR without the chart, set it explicitly to a per-install secret).

HAProxy validates placement: `haproxy -c` (in the admission webhook, the
config-load gate, and the asynchronous render gate) rejects a directive that's
illegal where you put it, so there is no chart-side keyword grammar to satisfy —
an invalid placement is reported as a validation error.

## Which per-object changes reload

- **Reload-free now** (map or runtime updates): a header modifier value, a path
  rewrite, a redirect target, a server/tunnel timeout, a Host/Connection/
  X-Forwarded-Prefix override, a body-size limit, a per-stream bandwidth
  throttle, a cache path exclusion, a canary's weight or header value, a mirror
  target, and any map the libraries already drive; endpoint churn (scaling a
  Service) as `set server`/`add server`; cert and CA content, and new SNI certs.
- **Reload-free once one route has paid for it**: a value the frontend must spell
  out as a literal, because no converter takes it from a variable. The first route
  introducing one reloads. Every later route reusing that value needs only a map
  entry. These values are an API-key header name, a JWT key file, an HMAC
  algorithm, a basic-auth realm, a rate-limit window, a bandwidth filter's
  `min-size` (`limit-rate-after`), a shared-scope bandwidth rate, and a canary's
  header or cookie name. A canary header pattern is a regex no map can carry, so
  it reloads whenever it changes.
- **A new or deleted route** (its backend section): where the pod's agent can
  add and remove a backend at runtime — HAProxy 3.4, whose `add backend`/`del
  backend` the `deployplan` drives — a route with a dynamic-eligible shape avoids
  a reload; on 3.0–3.3, the
  backend section is created or removed by a paced reload.
- **Always a reload**: a change to a `body` directive, a backend-level attribute
  (`mode`, `balance`, profile), a new profile section, or anything a library
  emits outside the macros (a `core` blob).

The runtime apply that turns a dynamic-eligible route into a no-reload change is
the agent's job, decided per pod by `deployplan` from the pod's reported HAProxy
version; a pod that can't apply a change at runtime falls back to a paced reload,
and the old worker keeps serving until the new one is ready.
