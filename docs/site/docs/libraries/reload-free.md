# Reload-free route propagation

HAPTIC renders your watched resources into an HAProxy configuration and applies
it to the fleet. Some changes need an HAProxy reload; many don't. This page is
the author contract: write your templates so that the common changes — adding a
route, changing a header or a timeout, scaling a Service — become map or runtime
updates instead of a new config section, and HAProxy keeps serving without a
reload.

The contract is resource-agnostic. The same base macros work for a
custom CRD, an Ingress, or a Gateway API route, so the examples below build the
same backend three times from three different inputs.

## The macros

You declare a backend with `Backend()` and feed it servers with
`BackendServers()`; you move per-route logic into a map with `RegisterMap()` and
read it back with one static line via `HeaderModifierRules()`. All four live in
the base library and see only strings and lists — never a resource kind.

```
{{ Backend({
     "name":     beName,            # required; the backend section name
     "mode":     "http",            # http|tcp, default http
     "balance":  "roundrobin",      # roundrobin|leastconn|random|hash-consistent keep servers dynamic
     "body":     bodyLines,         # []string: directives that must stay in THIS section → structural
     "servers":  BackendServers(serviceName, 0, port, serverOpts, portName, beName, namespace),
   }) }}
{{ RegisterMap("my-route.map", entries, {"ordered": false}) }}
```

`Backend()` is strict: it accepts `name`, `mode`, `balance`, `hashType`, `body`,
`servers`, `defaultServer`, `guid`, `comments`, `shape` and `shapeReason`, and
fails the render on any other key. The `profile` and `serverLines` slots the
tables below describe arrive with the named-defaults profiles in a later change
(MR 2) and aren't accepted yet — until then, shared directives go in `body`
(which keeps the backend structural).

## Custom CRD first

Suppose you watch a `Route` CRD with `spec.backend`, `spec.port` and a list of
`spec.requestHeaders`. A custom kind carries no bundled schema, so you reach it
through the resource-agnostic `resource("routes")` accessor and read its fields
with `dig()` — the same way the governance library reads any kind. Generate one
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

## When a backend is static

A backend is *dynamic-eligible* — created, deleted, and repopulated at runtime —
unless one of these makes it *static*, in which case creating it, deleting it, or
changing its body reloads HAProxy. Servers stay runtime-updatable either way.

<!-- vale off -->
<!-- Reproduced verbatim from the design decision record; the directive names and
     wording are authoritative and must match it exactly. -->
| # | Condition | Effect | Where decided |
|---|---|---|---|
| 1 | The pod runs HAProxy < 3.4 (no `add backend`/`del backend`) | create/delete reload on that pod; server/map/cert changes still runtime; the config text is identical | `deployplan` per pod (`Caps.DynamicBackends`) |
| 2 | `body` is non-empty — a directive that cannot live in a named `defaults`: `stick-table`/`stick on` (local rate limiting, bandwidth limiting), `filter …` (bwlim, explicit compression ordering, SPOE consumer limiter), `use-server`, `server-template`, `capture`, `redirect`, `dispatch`, `id`/`description`, raw operator injections (`config-backend`, `configuration-snippet`, `backend-config-snippet`), or `serverLines` (unix-socket loopbacks, `ring` servers); base's own static backends (`default_backend`, loopbacks, `gw-invalid-backend`, rate-limit table backends) | structural shape: create/delete/body change reload; server changes still runtime | `Backend()` (`ShapeReason`) |
| 3 | The backend's profile (named `defaults`) is new or its body changed in this render | one reload for the profile section; the backend itself is dynamic afterwards, as is every later backend on that profile | `deployplan` rule 1 |
| 4 | Backend-level attributes changed on an existing backend (`mode`, profile, `guid`, `balance`/`hashType`) or its text changed in a way the record does not explain | modification reload (HAProxy cannot alter these at runtime) | `deployplan` rules 1 + 4 |
| 5 | LB algorithm not dynamic-capable: `static-rr`, an explicit `hashType: map-based`, or `first` if spike (d) shows it is not | `add server` is refused ⇒ the backend cannot be populated at runtime ⇒ create/delete and server adds reload (`set server` on existing servers still runtime) | `Backend()` + `deployplan` rule 4 |
| 6 | A server keyword outside the verified `add server` set (`ssl-min-ver`/`ssl-max-ver`, `no-check`, `resolvers`, `init-addr`, `sni-auto`, `no-ssl`, …) | server adds — and therefore backend creation — reload; the keyword is named in `Reason` | `deployplan` A2 allow-list |
| 7 | Spike (b) negative branch only: the profile carries `http-request`/`http-check` rules that `add backend … from` does not inherit | those backends are treated as structural until the rules are moved to the frontend | 0-pre exit criteria |
| 8 | Name collision on `add backend` (a leftover from a deferred delete with the same name) | that apply reloads (no shape read-back exists in HAProxy) | agent A5 |
| 9 | Deletion of a backend something references statically (`default_backend NAME`, literal `use_backend NAME`) — creation is dynamic, but HAProxy refuses `del backend` | delete reloads (fallback) | agent A2 fallback |
| 10 | Backend text emitted outside `Backend()` (a library writing the section by hand) | lands in a `core` blob ⇒ every change reloads | assembler |

Everything else is dynamic: per-server values (`ssl`/`ca-file`/`sni`/…, `maxconn`,
check params, agent-check, weight, proxy protocol), profiles carrying values
(timeouts, cookies, retries, health-check specs, auth configs) once the profile
exists, all map and cert content, and route add/delete on HAProxy 3.4.
<!-- vale on -->

## Where to put a directive

The reload behaviour of a directive is decided by which slot of `Backend()` you
put it in.

| Put it in | For | Change behaviour |
|---|---|---|
| `profile` *(MR 2, not yet accepted)* | Value-free or per-value directives shared by every backend of one shape: timeouts, cookies, retries, `http-request`/`http-check` rules, health-check specs | A new profile reloads once; from then on every backend on it becomes dynamic, and changing a profile value reloads that one profile |
| a map + one static line | Per-route/per-backend values read at request time: header modifiers, path rewrites, redirect targets, timeouts (via `map_str_int`) | Adding or editing an entry is a map-only change — no reload |
| server `Extra` (via `serverOpts`) | Per-server keywords in the verified `add server` set: `weight`, `maxconn`, `check`, `ssl`, `sni`, `send-proxy`, agent-check | Applied to the running server or added with the server — no reload |
| `body` | Directives that must stay in this section: `stick-table`, `filter`, `use-server`, raw operator injections | Makes the backend structural — create/delete/body change reload |
| `serverLines` *(MR 2, not yet accepted)* | Raw server lines (Unix sockets, loopbacks, `ring` servers) | Always structural |

Until the `profile` and `serverLines` slots land (MR 2), `Backend()` rejects both
keys and shared directives go in `body`, which keeps the backend structural.

HAProxy validates placement: `haproxy -c` (in the admission webhook, the
config-load gate, and the asynchronous render gate) rejects a directive that's
illegal where you put it, so there is no chart-side keyword grammar to satisfy —
put a directive in the wrong slot and the render fails loudly rather than
shipping a broken config.

## Which per-object changes reload

- **Reload-free now** (map or runtime updates): a header modifier value, a path
  rewrite, a redirect target, a server/tunnel timeout, a Host/Connection/
  X-Forwarded-Prefix override, a body-size limit, and any map the libraries
  already drive; endpoint churn (scaling a Service) as `set server`/`add server`;
  cert and CA content, and new SNI certs.
- **A new or deleted route** (its backend section): where the pod's agent can
  add and remove a backend at runtime — HAProxy 3.4, whose `add backend`/`del
  backend` the `deployplan` drives — a route with a dynamic-eligible shape avoids
  a reload; on 3.0–3.3, and wherever that runtime path isn't yet in effect, the
  backend section is created or removed by a paced reload.
- **Always a reload**: a change to a `body` directive, a backend-level attribute
  (`mode`, `balance`, profile), a new profile section, or anything a library
  emits outside the macros (a `core` blob).

The runtime apply that turns a dynamic-eligible route into a no-reload change is
the agent's job, decided per pod by `deployplan` from the pod's reported HAProxy
version; a pod that can't apply a change at runtime falls back to a paced reload,
and the old worker keeps serving until the new one is ready.
