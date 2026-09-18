# Reload-free route propagation

HAProxy can change some running state through its Runtime API: map entries,
server addresses and weights, and certificates. Other changes, such as a new
listener or request-processing rule, need a reload. A reload starts a worker
with the new configuration while the old worker drains existing connections.

HAPTIC must also know which parts of your template output represent those live
settings. Use `Backend()` and `RegisterMap()` to provide that information. The
bundled routing libraries already use them; custom templates can use the same
helpers with Ingress, Gateway API, or any other resource.

## What changes in HAProxy

Editing `haproxy.cfg` or a map file on disk doesn't update the running worker.
HAPTIC writes the desired files and either sends Runtime API commands to the
worker or reloads HAProxy to read the new configuration.

| Change | How it reaches the running worker |
|--------|-----------------------------------|
| Entries in an already loaded routing map | Runtime map commands. |
| An existing server's address, port, weight, or maintenance state | Runtime server commands. |
| Certificate or CA contents | Runtime certificate commands, when the loaded configuration supports the update. |
| A new backend using an already loaded settings profile | Runtime backend commands on HAProxy 3.4; a reload on 3.0–3.3. |
| A listener, request-processing rule, or new settings profile | Reload. |

For example, changing `shop.example.com` to `store.example.com` in an existing
`host.map` changes map data. The frontend map lookup stays the same, so HAPTIC
can update the map at runtime. Adding a literal `acl` or `use_backend` rule to
`haproxy.cfg` changes the frontend rules and requires a reload.

The same distinction applies to headers: changing a value stored in a map can
happen live. Introducing a header name for which the configuration has no rule
adds a rule and requires a reload. Later routes can reuse that rule.

## How HAPTIC recognizes a live update

The template helpers produce configuration **and a structured description of
it**: backend settings, server records, map entries, and configuration sections.
HAPTIC compares that description with each pod's applied configuration. It checks
whether the pod's HAProxy version and current state support every required
runtime operation; otherwise, it schedules a reload.

HAPTIC doesn't reconstruct this information by parsing arbitrary configuration
text. A hand-written `server` line can describe a change that HAProxy supports
at runtime, but HAPTIC needs a server record from `Backend()` to identify and
apply that operation. Likewise, `RegisterMap()` declares the map's entries and
whether their order matters. HAPTIC updates individual entries where possible
and replaces a map atomically when needed to preserve order. Merely marking a
backend dynamic can't make an unsupported HAProxy operation work.

## The macros

Use the base library's `Backend()` to describe a backend and `RegisterMap()`
to register map entries. `HeaderModifierRules()` generates rules that read header
values from a map. For Service and EndpointSlice resolution, the separate
`kubernetes-backends` library provides `BackendServers()`.

The following call belongs in a backend-generation snippet. Supply your route's
backend name, settings, and Service reference:

```
{{ Backend(map[string]any{
     "name":    beName,
     "mode":    "http",
     "balance": "roundrobin",
     "profile": profileLines,
     "body":    bodyLines,
     "servers": BackendServers(serviceName, 0, port, serverOpts, portName, beName, namespace),
   }) }}
{%- var _ = RegisterMap("my-route.map", entries, map[string]any{"ordered": false}) -%}
```

Use `ordered: false` for exact, prefix, or IP map lookups. Keep the default,
`true`, when the first matching entry wins, as with regular-expression maps.

`Backend()` groups shared settings in a named `defaults haptic-be-<hash>`
section. The hash comes from the settings: backends with the same settings reuse
one profile. HAProxy 3.4 can add a backend using a profile it has already loaded
without reloading. Loading a new profile requires a reload first.

Put shared directives in `profile` and pass servers as records in `servers`.
Keep `body` empty for runtime backend creation; local filters, stick tables, and
raw directives in `body` make backend creation or deletion require a reload.

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

Adding a Route that reuses the loaded backend profile and header rules changes
map entries and backend records; HAProxy 3.4 can apply both at runtime.
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
inspect the changed output. The playground predicts reload behavior; it does
not deploy to HAProxy.

<div class="pg-embed" markdown data-scenario="ingress" data-facade="spec.templateSnippets.backends-500-ingress" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Backend() and RegisterMap() in one render" data-height="480">

<p class="pg-task" markdown>Press **Run live**. In the **haproxy.cfg** tab, find the `backend storefront_shop_svc_shop_http` section that `Backend()` assembled from the `shop` Ingress, with one pod-named `server` line per endpoint. Switch to the **maps** tab to see the `host.map` and `path-prefix.map` entries that `RegisterMap()` wrote to route to it. Then, in the **Resources** panel, change the `shop` Ingress's host to `store.example.com` and Run again — the map entry changes while the backend stays the same. HAPTIC can apply that map change without reloading a running HAProxy.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

`backends-500-ingress` calls `Backend()` once per unique `(namespace, ingress, service, port)`, so `haproxy.cfg` carries one `backend storefront_shop_svc_shop_http`. `map-host-500-ingress` and `map-path-prefix-500-ingress` call `RegisterMap()` to write `host.map` and `path-prefix.map`; changing the host rewrites one unordered map entry — a runtime map update, not a config-section change, so HAProxy keeps serving without a reload.

</details>

</div>

## When a backend is static

HAPTIC can create a backend at runtime when `Backend()` describes it as
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

Choose a slot that HAProxy permits for the directive. That placement determines
which changes HAPTIC can apply at runtime.

| Put it in | For | Change behaviour |
|---|---|---|
| `profile` | Value-free or per-value directives shared by every backend of one shape: timeouts, cookies, retries, `http-request`/`http-check` rules, health-check specs | Loading or changing a profile requires a reload of HAProxy. Later backends can reuse it at runtime if their remaining settings support runtime creation. |
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

| Route change | Reload behavior |
|--------------|-----------------|
| A value in an existing map: header value, rewrite, redirect, timeout, body-size limit, bandwidth limit, cache exclusion, canary weight, or mirror target | Runtime update while the rules reading that map remain unchanged. |
| A new literal used by a rule: API-key header name, JWT key file, HMAC algorithm, basic-auth realm, rate-limit window, bandwidth filter `min-size`, shared bandwidth rate, or canary header/cookie name | The first use adds a rule and requires a reload. Later routes can reuse it. |
| A canary header regex | Reload whenever the pattern changes. |
| A new or deleted backend | Runtime on HAProxy 3.4 when it reuses a loaded profile and supports runtime creation; otherwise reload. |
| Backend `body`, mode, balance settings, or profile; configuration outside the helpers | Reload when the configuration changes. |

HAPTIC decides separately for each pod from its applied state and reported
HAProxy version. If a runtime operation fails, the agent attempts a reload.
See [Supported configuration](../supported-configuration.md) for operation-level
limits and [HAProxy deployment](../haproxy-deployment.md) for reload pacing.
