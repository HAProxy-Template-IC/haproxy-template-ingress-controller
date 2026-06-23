# Design

## Where the bug lives

In each backend-rendering library, the port number is grabbed from the ingress/route spec with a literal `fallback(80)`:

```jinja
{%- var port = svc | dig("port", "number") | fallback(80) -%}
...
{{ BackendServers(tostring(svcName), 0, toint(port), nil, nil, backendKey, tostring(ns)) }}
```

When `port.number` is missing (because the caller used `port.name` instead, which is fully valid Kubernetes-API), `port` becomes the literal `80`. `BackendServers` is then called with `port=80` and `portName=nil`. Its internal recovery path tries to look up the port name by matching the numeric port (80) against `service.spec.ports[].port` — that match never succeeds for services that don't expose 80, so the macro falls through and emits `:80` directly into the `server` line.

The same pattern repeats in `ingress.yaml`, `gateway.yaml` (twice — backend rendering and weighted-backend rendering), `nginx-ingress.yaml`, `haproxy-ingress.yaml`, and `haproxytech.yaml`. Six independent fallbacks to 80, all silently broken for the same reason.

## Why a helper macro, not in-place fixes

The minimum patch — passing `svc | dig("port", "name")` as the fifth arg to `BackendServers` in all six call sites — would work but leaves the `fallback(80)` pattern duplicated across libraries. The next contributor adding a sixth ingress-flavor library is one copy-paste away from re-introducing the bug.

Centralizing port resolution behind a named helper means: one place to test, one place to fix, one place to extend (e.g. when Gateway API adds new `BackendObjectReference` shapes), and a documented invariant — *if you have a service ref, call `ResolveServicePort` before anything else*.

## The helper

Location: `charts/haptic/libraries/base.yaml`, alongside the existing utility macros (`util-backend-servers`, `util-backend-servers-helpers`, etc).

Signature (Scriggo):

```jinja
{% macro ResolveServicePort(namespace string, serviceName string, portRef any) string %}
```

**Return type note:** the original design specified `map[string]any` for the return, but Scriggo macros are constrained at the parser level to return only content-format types (`string`, `html`, `css`, `js`, `json`, `markdown`) or void — see `gitlab.com/haproxy-haptic/scriggo` `internal/compiler/parser_func.go:116-122`. The macro therefore returns a space-separated string `"<portNumber> <portName>"`. Callers unpack with `split(ResolveServicePort(...), " ")` → `[portNumber, portName]`. The empty-portName case is `"<portNumber> "` (trailing space, second slice element is `""`).

`portRef` accepts two shapes:

1. **Dict shape** (Kubernetes Ingress / similar): `{number?: int, name?: string}` from `Ingress.spec.rules[].http.paths[].backend.service.port` or equivalent.
2. **Bare integer** (Gateway API): `BackendObjectReference.port` is a `*PortNumber` — a flat int, not a dict. Plain numbers like `8443` are accepted directly.

It can also be `nil` (which triggers the "neither number nor name" failure path).

### Resolution rules

1. **Number given.** If `portRef.number` is a positive integer, use it directly. Optionally look up the matching `name` from `service.spec.ports[]` (matched by `.port == portRef.number`) so the caller has it for the backend-name suffix and the rendered comment; empty string if no match.

2. **Name given, no number.** Look up `service.spec.ports[]` by `.name == portRef.name`. If found, return `{port: that-port, name: portRef.name}`. If the service has no such named port, panic (see below).

3. **Neither given, or service not found.** Panic with a message of the form:

   ```
   ResolveServicePort: cannot resolve port for <ns>/<service>:
     portRef=<repr>, service exists=<bool>, service ports=<list>
   ```

   The `panic(...)` aborts the current render, which is caught by the controller's renderer, surfaces as a `validation` / `render` error in the controller logs, marks the affected `HAProxyCfg` / Ingress / Gateway with a `deployFailed` status, and crucially does **not** publish the new config — so the previously-good HAProxy config keeps serving.

### Why panic and not skip

Two prior alternatives discussed and rejected:

- *Render-and-skip:* emit a `# SKIPPED` comment and no backend block. Quiet failure mode, easier to miss; requests to the skipped host fall through to the catch-all 404 and look like an ingress misconfiguration on the *requester* side, not on the ingress definition. This is what the original `:80` bug effectively was — a silent failure that hid for two months.
- *Render with port 0:* fail at HAProxy config validation. Loud, but blocks all unrelated ingresses behind the same controller because the whole config is rejected.

Panic on render is the only option where (a) the operator sees the error immediately, (b) the affected resource's status reflects the failure, and (c) unrelated workloads continue serving from the last-good config. Existing template panics in the codebase already work this way — see `charts/haptic/libraries/base.yaml` for the pattern.

## Caller changes

Each existing call site collapses from this:

```jinja
{%- var port = svc | dig("port", "number") | fallback(80) -%}
{%- var portId = (svc | dig("port", "name")) | fallback(svc | dig("port", "number") | fallback("")) -%}
{%- if first_seen("ingress_backend", ns, name, svcName, portId) -%}
# Backend for: Ingress {{ ns }}/{{ name }} → Service {{ svcName }}:{{ port }}
backend {{ backendKey }}
  ...
  {{ BackendServers(tostring(svcName), 0, toint(port), nil, nil, backendKey, tostring(ns)) }}
```

to this:

```jinja
{%- import "util-service-port-resolution" for ResolveServicePort -%}
{%- var resolvedParts = split(ResolveServicePort(tostring(ns), tostring(svcName), svc | dig("port")), " ") -%}
{%- var port = toint(resolvedParts[0]) -%}
{%- var portName = resolvedParts[1] -%}
{%- var portId = portName -%}
{%- if portId == "" -%}{%- portId = tostring(port) -%}{%- end -%}
{%- if first_seen("ingress_backend", ns, name, svcName, portId) -%}
# Backend for: Ingress {{ ns }}/{{ name }} → Service {{ svcName }}:{{ port }}
backend {{ backendKey }}
  ...
  {{ BackendServers(tostring(svcName), 0, port, nil, portName, backendKey, tostring(ns)) }}
```

Note the `portId` form uses an explicit `if portId == ""` guard rather than `fallback(tostring(port))`, because `fallback()` only substitutes on `nil`, not on empty strings.

Two key things change in the call: `port` is now the correct numeric port (so the comment matches reality), and `portName` is now passed as the fifth arg so `BackendServers`' EndpointSlice port lookup uses the name when picking the per-endpoint port (which matters when a Service exposes multiple ports).

Gateway library has two call sites with the same shape adapted for `BackendObjectReference`. NGINX/haproxy-ingress/haproxytech are structurally identical.

## Testing strategy

Two layers:

### Helper unit tests (helm-unittest)

In `charts/haptic/tests/library_loader_test.yaml` (or a new dedicated file `resolve_service_port_test.yaml`), add cases:

| Case | Input portRef | Service spec | Expected |
|---|---|---|---|
| Number only | `{number: 8443}` | port 8443 named `https-gui` | `{port: 8443, name: "https-gui"}` |
| Number only, unnamed | `{number: 8443}` | port 8443 with no name | `{port: 8443, name: ""}` |
| Name only | `{name: "https-gui"}` | port 8443 named `https-gui` | `{port: 8443, name: "https-gui"}` |
| Both | `{number: 8443, name: "https-gui"}` | port 8443 named `https-gui` | `{port: 8443, name: "https-gui"}` (number wins) |
| Name no match | `{name: "nope"}` | port 8443 named `https-gui` | panic, message references `nope` and the service's actual port names |
| Service missing | `{name: "x"}` | service does not exist | panic, message references the missing service |

The Scriggo template-test harness needs to assert that the panic fires; if the existing test framework doesn't support that directly, the test renders a fixture that calls the macro and asserts the controller-side error contains the expected substring (this pattern likely already exists for other panic-producing macros — find one and mirror it).

### Library integration tests (test-templates.sh)

For each of the five libraries:

1. Add a fixture Ingress (or HTTPRoute / Gateway, as appropriate) that references a Service port by **name**, where the Service exposes a numeric port other than 80 under that name.
2. Render via `./scripts/test-templates.sh`.
3. Assert the rendered backend's `server SRV_1` line contains the correct numeric port (not 80).
4. Assert the rendered `# Backend for: ...` comment also shows the correct port.

These integration tests are what would have caught the original bug — they're the regression guard.

Pre-existing tests using numeric ports must continue to pass unchanged; the helper is fully backwards compatible for the number-only path.

## Status patch behavior on render abort

When `fail()` aborts a render, the controller's `StatusApplier` falls back to applying the `renderFailed` status variant collected from the *last successful render's* status patches. Resources that were never successfully rendered (a brand-new misconfigured Ingress on first deploy) do NOT receive a `renderFailed` status update, because the macro aborts before the failing resource's `statusPatch()` call runs. In that case the only signal is the `ResolveServicePort: ...` error in the controller's log stream. Operators should configure log alerting on `ResolveServicePort:` substring matches, or look at the last-successful-render timestamp on existing resources.

## Out of scope

- `BackendServers` macro signature: untouched. Its existing contract (caller resolves port; macro handles slot assignment) is intentionally narrow and shouldn't grow.
- TCP routes / `TCPRoute` Gateway resources: not currently a call site for the `BackendServers` macro in this pattern; no change.
- Gateway API SSL-passthrough backend rendering currently re-reads the HTTPRoute resource at backend-render time to resolve ports (in `backends-501-gateway-ssl-passthrough`). This re-introduces a small race window vs. the annotation-compat path which caches at scan time. Fixing this requires extending the scan helper at `util-build-ssl-passthrough` to capture `svcName`/`svcPort`/`svcPortName` — out of scope for this branch but tracked as a follow-up.
- Controller Go code: no changes. The fix is entirely in templates.

## What turned out to NOT be out of scope

The earlier draft assumed annotation-driven SSL-passthrough backends (the consumers of `BuildAnnotationSSLPassthrough` in `annotation-compat.yaml`) had a separate, correct port-handling path. They didn't: the helper used `port.number | fallback(port.name) | toint()`, which silently converts a port-name string to 0 and filters the entry out — a different but equivalent silent failure. This branch fixes that helper to use `ResolveServicePort` and emit both `svcPort` and `svcPortName` to its three consumers (`nginx-ingress.yaml`, `haproxy-ingress.yaml`, `haproxytech.yaml`).

Also added to scope: the `util-default-backend` snippet in `base.yaml` (the chart's `defaultBackendService` Helm value). It had the same `fallback(80) + (nil, nil)` shape and is now using the helper.
