## Why

When an Ingress or Gateway backend references a Kubernetes Service port by **name** (`port.name: <foo>`) rather than by number (`port.number: 8443`), every backend-rendering library in `charts/haptic/libraries/` silently falls back to port `80` in the generated `server` line. The resulting HAProxy config points at a port the pod never listens on, so HAProxy returns 503 with `<NOSRV>/SC--` for every request and the operator gets no signal that the upstream is misconfigured.

A real-world example: an Ingress for the `unifi-controller` service referencing `port.name: https-gui` (Service port 8443, named targetPort on the pod) produced

```
# Backend for: Ingress unifi/unifi → Service unifi:80      ← wrong port number in the comment too
backend ing_unifi_unifi_unifi_https-gui
  default-server check ssl verify none
  server SRV_1 10.42.1.16:80 enabled                       ← :80, but pod listens on :8443
```

and stayed broken for ~73 days because the rendered config was syntactically valid and the controller logged no error.

The fallback-to-80 pattern lives independently in seven call sites across six libraries (`ingress`, `gateway` ×2, `nginx-ingress`, `haproxy-ingress`, `haproxytech`, and `base/util-default-backend`). The SSL-passthrough annotation scanner in `annotation-compat.yaml` has a related but worse variant (`port.number | fallback(port.name) | toint()` → silently converts a name string to 0, filtering the entry out entirely). The bug is structural, not local.

## What Changes

- New `ResolveServicePort(namespace, serviceName, portRef)` macro in `charts/haptic/libraries/base.yaml`. Returns a space-separated string `"<portNumber> <portName>"` (Scriggo macro return types are constrained to content-format types; callers unpack with `split(..., " ")`). Resolution rules:
  1. `portRef` is a `{number, name?}` dict with `number > 0` → use it; backfill `name` from the Service's `.spec.ports[]` if present (service is best-effort here: a numeric port stands on its own).
  2. `portRef` is a dict with `name` only → look up the numeric port in the Service's `.spec.ports[]` by matching `name`. Service must exist.
  3. `portRef` is a bare integer (Gateway API `BackendObjectReference.port`) → use it as the port number.
  4. Neither resolves → call `fail(...)` with a message naming the offending namespace/service/port and listing the Service's actual port names, halting render. The previously-good HAProxy config keeps serving.
- All seven existing call sites switch to the helper. Each call site passes the resolved `port` and `name` to `BackendServers(...)` instead of the current `(toint(port), nil, nil, ...)`, allowing `BackendServers` to resolve named targetPorts against EndpointSlices.
- The shared `BuildAnnotationSSLPassthrough` helper in `annotation-compat.yaml` is updated to use `ResolveServicePort` and to emit `svcPortName` in its backend entries. The three consumer libraries (`nginx-ingress.yaml`, `haproxy-ingress.yaml`, `haproxytech.yaml`) read it back and pass it to `BackendServers`.
- The `# Backend for:` comment in rendered configs now shows the resolved numeric port, not the misleading `:80`.
- `charts/haptic/CHANGELOG.md` gains a "Fixed" entry under `[Unreleased]`.

## Capabilities

### Modified Capabilities

- `template-libraries`: ingress, gateway, nginx-ingress, haproxy-ingress, and haproxytech libraries all resolve named Service ports correctly. Unresolvable port refs fail rendering loudly instead of silently producing broken `:80` configs.
- `haproxy-config-generation`: Generated `backend` blocks contain the actual pod port, not the fallback `80`.

### New Capabilities (internal)

- `service-port-resolution`: A single helper macro in `base.yaml` is the source of truth for converting an Ingress/Gateway `ServiceBackendPort`-shaped reference into a `(port, name)` pair, used by all consumers.

## Impact

- **charts/haptic/libraries/base.yaml**: new `ResolveServicePort` macro.
- **charts/haptic/libraries/ingress.yaml**: 1 call site updated (`util-generate-backends-ingress`).
- **charts/haptic/libraries/gateway.yaml**: 2 call sites updated (Gateway backend rendering, weighted-backend rendering).
- **charts/haptic/libraries/nginx-ingress.yaml**: 1 call site updated.
- **charts/haptic/libraries/haproxy-ingress.yaml**: 1 call site updated.
- **charts/haptic/libraries/haproxytech.yaml**: 1 call site updated.
- **charts/haptic/tests/**: new helm-unittest cases for `ResolveServicePort` (success modes, name-only, number-only, both, panic on unresolvable), and per-library tests that render an Ingress/Gateway/HTTPRoute with a named-port reference and assert the resulting backend `server` line carries the correct numeric port.
- **charts/haptic/CHANGELOG.md**: "Fixed" entry under `[Unreleased]`.

## Non-goals

- This change does not refactor `BackendServers` itself; its signature stays as-is.
- This change does not add new fields to the chart `values.yaml` or any CRD.
- Gateway API SSL-passthrough backends in `backends-501-gateway-ssl-passthrough` (gateway.yaml) call `ResolveServicePort` inline at render time rather than caching at scan time like the annotation-compat path does. Tracked as a follow-up to align the two patterns; not required for this fix.
