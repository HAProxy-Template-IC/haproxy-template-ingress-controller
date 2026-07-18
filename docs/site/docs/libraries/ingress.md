# Ingress library

The Ingress library turns `networking.k8s.io/v1` Ingress resources into HAProxy routing configuration.

## Overview

The Ingress library enables HAProxy to route traffic based on Kubernetes Ingress resources:

- Path-based routing with Exact, Prefix, and ImplementationSpecific path types
- Host-based routing via Ingress rules
- TLS termination — every Ingress is served over HTTPS with the default certificate by default, plus per-host certificates via `spec.tls`
- Backend generation with automatic endpoint discovery
- IngressClass filtering (default: `haptic`)

This library is enabled by default.

See the Ingress preset render a full HAProxy config live:

<div class="pg-embed" markdown data-scenario="ingress" data-facade="spec.templateSnippets.map-host-500-ingress" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Ingress → HAProxy config" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, add a second host to the `shop` Ingress — copy its existing rule and change the host to `www.shop.example.com`. Then open the **maps** tab and watch `www.shop.example.com` join `host.map` and `path-prefix.map`, both routing to the existing `storefront_shop_svc_shop_http` backend.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

`map-host-500-ingress` adds `www.shop.example.com www.shop.example.com` to `host.map`, and `map-path-prefix-500-ingress` adds `www.shop.example.com/ BACKEND:storefront_shop_svc_shop_http` to `path-prefix.map`. The **haproxy.cfg** tab still shows a single `backend storefront_shop_svc_shop_http`: both rules point at the same Service and port, so `backends-500-ingress` deduplicates them — its `first_seen("ingress_backend", ns, name, svcName, portId)` guard emits one backend per unique `(namespace, ingress, service, port)`, no matter how many hosts route to it.

</details>

</div>

## Configuration

```yaml
controller:
  templateLibraries:
    ingress:
      enabled: true  # Enabled by default
```

### Ingress class filtering

By default, only Ingresses with `spec.ingressClassName: haptic` are processed. This is configured via field selector in the library's watched resources. Override `ingressClass.name` to match an incumbent controller's class (often `haproxy`) when replacing one in-place.

## Extension points

The Ingress library hooks into these extension points from base.yaml. Snippet names match what's emitted in `libraries/ingress.yaml`.

| Extension Point | Snippet | What It Generates |
|-----------------|---------|-------------------|
| `features-*` | `features-100-ingress-bind` | Sets `gf["bindHTTPDefault"]` / `gf["needHTTPFrontend"]`; by default (or for Ingresses with `spec.tls`) also sets `gf["bindHTTPSDefault"]`, `gf["needHTTPSFrontend"]`, `gf["needHTTPSTermination"]` — see [HTTPS on by default](#https-on-by-default) |
| `features-*` | `features-100-ingress-tls` | Registers TLS Secrets from `ingress.spec.tls[]` into `gf["tlsCertificates"]` for the SSL library's CRT-list |
| `backends-*` | `backends-500-ingress` | Backend blocks per unique `(namespace, ingress, service, port)` referenced by an Ingress |
| `map-host-*` | `map-host-500-ingress` | Host → group entries derived from `ingress.spec.rules[].host` |
| `map-path-exact-*` | `map-path-exact-500-ingress` | Entries for `pathType: Exact` paths |
| `map-pfxexact-*` | `map-pfxexact-500-ingress` | Prefix-exact entries emitted when `pathType: Prefix` paths need to match their exact boundary |
| `map-path-prefix-*` | `map-path-prefix-500-ingress` | Prefix entries for `pathType: Prefix` paths |
| `status-patches-*` | `status-patches-200-ingress` | Patches the LoadBalancer status on each matched Ingress |

Regex-path matching isn't emitted by this library directly — it comes from the default-enabled [haptic-annotations](haptic-annotations.md) library, whose `map-path-regex-800-haptic-path-type` snippet handles `haproxy-haptic.org/path-type: regex`. The opt-in `haproxy-ingress` library provides the equivalent `haproxy-ingress.github.io/path-type: regex`.

### Injecting custom configuration

You can extend Ingress support by adding snippets with the right extension-point prefix and a priority that places them correctly alongside the built-in 500-range entries:

```yaml
controller:
  config:
    templateSnippets:
      # Runs alongside the built-in 500-range exact-path entries
      map-path-exact-700-custom:
        template: |
          # Custom exact path routing
          api.example.com/v1/health BACKEND:custom_health_backend
```

## Features

### Host-based routing

Each entry in `spec.rules` carries its own `host`, so a single Ingress can serve several hostnames — list one rule per host:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: multi-host
  namespace: default
spec:
  ingressClassName: haptic
  rules:
    - host: shop.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: storefront
                port:
                  number: 80
    - host: admin.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: admin-console
                port:
                  number: 80
```

A rule with no `host` matches every hostname (the catch-all listener).

#### Wildcard hosts

A single-label wildcard host — `*.example.com` — is supported. HAPTIC normalizes it to `.example.com` (dropping the `*`), and HAProxy strips the request's leading label before the map lookup. So `*.example.com` matches `shop.example.com` and `admin.example.com`, but not the apex `example.com` or a deeper `a.b.example.com` — a Kubernetes wildcard host matches exactly one label. To match more than one label, add the `haproxy-ingress.github.io/server-alias-regex` annotation, which routes matching hostnames through `host-regex.map`. See [Frontend routing logic](base.md#frontend-routing-logic) for the full host-match cascade.

```yaml
spec:
  ingressClassName: haptic
  rules:
    - host: "*.example.com"
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: wildcard-service
                port:
                  number: 80
```

### Path types

The Ingress library supports all standard Kubernetes Ingress path types:

| Path Type | HAProxy Matcher | Description |
|-----------|-----------------|-------------|
| `Exact` | `map()` | Path must match exactly |
| `Prefix` | `map_beg()` | Path must start with value |
| `ImplementationSpecific` | `map_beg()` | Treated as Prefix by default |

!!! note "Path match precedence"
    When more than one path could match a request, HAProxy evaluates the path maps in a fixed order: Exact, then Regex, then Prefix-exact, then Prefix. Host matching runs first (exact host, then single-label wildcard, then host regex). Set `controller.config.templatingSettings.extraContext.routing.regexMatchOrder=last` to move regex evaluation after the prefix matchers (Exact > Prefix-exact > Prefix > Regex). See [Frontend routing logic](base.md#frontend-routing-logic) for the complete cascade.

**Example Ingress:**

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-app
  namespace: default
spec:
  ingressClassName: haptic
  rules:
    - host: app.example.com
      http:
        paths:
          - path: /api
            pathType: Prefix
            backend:
              service:
                name: api-service
                port:
                  number: 80
          - path: /health
            pathType: Exact
            backend:
              service:
                name: health-service
                port:
                  number: 8080
```

Watch the three path-type maps populate as you add Exact and Prefix paths:

<div class="pg-embed" markdown data-scenario="ingress" data-facade="spec.templateSnippets.map-path-exact-500-ingress" data-tab="maps" data-controls="tabs,resources" data-title="Path types → map entries" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, add two paths to the `shop` Ingress rule (alongside the existing `/`): a `/api` path with `pathType: Prefix` and a `/health` path with `pathType: Exact`, both pointing at the `shop` service on port `80`. Then open the **maps** tab and watch each path land in a different map.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

- `map-path-exact-500-ingress` adds `shop.example.com/health BACKEND:storefront_shop_svc_shop_http` to `path-exact.map` — the `Exact` path lowers to a `map()` lookup.
- `map-path-prefix-500-ingress` adds `shop.example.com/api/ BACKEND:storefront_shop_svc_shop_http` to `path-prefix.map` — the `Prefix` path lowers to a `map_beg()` lookup.
- `map-pfxexact-500-ingress` also adds `shop.example.com/api BACKEND:storefront_shop_svc_shop_http` to `path-prefix-exact.map` — the exact-boundary entry so a request to exactly `/api` (no trailing slash) still matches the Prefix rule. A root `/` Prefix path emits no boundary entry, which is why the original `/` path isn't in this map.

All three route to the same `storefront_shop_svc_shop_http` backend: they share one Service and port, so `backends-500-ingress` emits a single backend.

</details>

</div>

### Conflicting routes: the oldest Ingress wins

A host and path can be routed to only one backend. When two Ingresses declare the same host, path, and path type, the controller resolves the collision deterministically: the **older** Ingress — by `creationTimestamp`, with the namespace and name as a tiebreaker — keeps the route, and the newer Ingress's conflicting route is dropped. This matches ingress-nginx's behavior, so an Ingress that was there first is never hijacked by a later conflicting one.

`Prefix` and `ImplementationSpecific` paths share the same routing slot, so a `Prefix` path on one Ingress and an `ImplementationSpecific` path with the same host and path on another still collide and are resolved together. `Exact` paths match separately and never collide with prefix paths.

Because the timestamp is the Ingress object's creation time, editing an Ingress doesn't change who wins — a route stays with the Ingress that first claimed it, regardless of later edits.

The controller records a `Warning` Event with reason `RouteConflict` on the Ingress that lost the route, naming the winner, so the dropped route is visible without reading the controller logs:

```console
$ kubectl describe ingress route-new -n team-b
...
Events:
  Type     Reason         Age   From              Message
  ----     ------         ----  ----              -------
  Warning  RouteConflict  10s   haptic-controller  host "shop.example.com" path "/checkout" (Prefix) is already served by Ingress team-a/route-old, which takes precedence; this Ingress's route is not applied
```

Different paths on the same host don't collide, so you can split a host across several Ingresses by giving each a distinct path. An [nginx canary](nginx-ingress.md) Ingress (`nginx.ingress.kubernetes.io/canary: "true"`) intentionally shares its main Ingress's host and path; it never competes for the base route — the main Ingress owns it, and the canary only overlays a traffic split on top.

!!! warning "No arbitration across resource types"
    Oldest-wins applies only between Ingresses. There's no conflict resolution between an Ingress and a Gateway API route (HTTPRoute/GRPCRoute) that claim the same host and path. The Ingress and Gateway libraries write into the same shared host and path map files independently, and each de-duplicates only within its own resource type — so an overlapping Ingress and HTTPRoute produce a duplicate map entry whose winner is order-dependent and not guaranteed. Give an Ingress and a Gateway route distinct host and path combinations rather than routing the same host and path through both.

### Default backend and custom error pages

Set `spec.defaultBackend` to route requests that match none of an Ingress's rule paths to a fallback Service:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: catch-all
  namespace: default
spec:
  ingressClassName: haptic
  defaultBackend:
    service:
      name: fallback-service
      port:
        number: 80
  rules:
    - host: app.example.com
      http:
        paths:
          - path: /api
            pathType: Prefix
            backend:
              service:
                name: api-service
                port:
                  number: 80
```

HAPTIC honours `spec.defaultBackend` in three shapes:

- **Rule-less Ingress** — with only `spec.defaultBackend` and no `rules`, every request that doesn't match a more specific route goes to the default backend.
- **Alongside rules** — a request that matches the Ingress's host but none of its paths falls through to the default backend; requests to other hosts aren't caught.
- **Newest wins per host** — when several Ingresses declare a default backend for the same host, the most recently created one wins, so a rollout switches the fallback deterministically.

To serve a custom page for unmatched requests — a branded 404 or a maintenance notice — point `spec.defaultBackend` at a small Service that returns it. For HAProxy's own error responses (for example the 503 shown when a backend has no ready endpoints), render the page as a file and wire it with an `errorfile` directive instead; see [Auxiliary files](../templating.md#general-files) for the `files` and `errorfile` pattern.

### TLS Configuration

TLS certificates are automatically loaded from Kubernetes Secrets and registered with the SSL library:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: secure-app
  namespace: default
spec:
  ingressClassName: haptic
  tls:
    - hosts:
        - secure.example.com
      secretName: tls-secret
  rules:
    - host: secure.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: secure-service
                port:
                  number: 443
```

The referenced Secret must be of type `kubernetes.io/tls`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: tls-secret
  namespace: default
type: kubernetes.io/tls
data:
  tls.crt: <base64-encoded-certificate>
  tls.key: <base64-encoded-key>
```

!!! warning "A missing TLS Secret is silent"
    If the Secret named in `spec.tls[].secretName` doesn't exist — or exists but lacks `tls.crt`/`tls.key` — HAPTIC skips registering that certificate. The host is still served over HTTPS, but with the [default certificate](../ssl-certificates.md), not your own. The render doesn't fail and, unlike a missing backend Service (which emits a `BackendUnresolved` Warning event), no event is emitted — so a mistyped or not-yet-created `secretName` shows up only as the wrong certificate on the wire. Verify the Secret exists with `kubectl get -n <namespace> secret <secretName>`, and check what's served with `openssl s_client -connect <host>:443 -servername <host>`.

#### HTTPS on by default

Every Ingress is served over **both HTTP and HTTPS** out of the box, even without a `spec.tls` entry. HAPTIC binds the chart's https port (`haproxy.ports.https`, default `443`) and terminates TLS with the [default certificate](../ssl-certificates.md) — a self-signed cert out of the box — routing HTTPS requests through the same host and path rules as HTTP. A `spec.tls` entry layers a host-specific certificate on top: that host is served with its own certificate instead of the default.

Two `extraContext` settings control the default HTTPS bind:

| Key | Default | Effect |
|-----|---------|--------|
| `ingressDefaultHTTPS` | `true` | Bind the https port for every Ingress using the default certificate. Set `false` to serve Ingress over plain HTTP only until a host opts in with `spec.tls`. |
| `default_ssl_cert_name` / `default_ssl_cert_namespace` | chart-set | The Secret backing the default certificate. When the default certificate is disabled (`defaultSSLCertificate.enabled=false`), there is no cert to bind, so the https port stays closed regardless of `ingressDefaultHTTPS`. |

To serve Ingress over plain HTTP only, disable the default bind through the chart:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        ingressDefaultHTTPS: false
```

#### Redirect HTTP to HTTPS

To send all Ingress traffic to HTTPS, turn on the global redirect. HAPTIC then emits an `http-request redirect scheme https` rule for every Ingress host that's served over HTTPS (the default-HTTPS bind is on, or the host has its own `spec.tls`), so it never redirects to a closed port:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        ingressDefaultSSLRedirect: true
```

| Key | Default | Effect |
|-----|---------|--------|
| `ingressDefaultSSLRedirect` | `false` | Redirect every HTTPS-served Ingress host from HTTP to HTTPS. Opt-in. |
| `ingressDefaultSSLRedirectCode` | `"308"` | HTTP status code for the redirect — one of `301`, `302`, `303`, `307`, `308`. |

The global toggle redirects all HTTPS-served Ingress hosts at once. For per-host control, leave it off and use the vendor `ssl-redirect` / `force-ssl-redirect` annotations, which register hosts individually and keep working alongside the global toggle.

### Backend generation

Backends are generated with:

- Automatic endpoint discovery via EndpointSlices
- TCP-connect health checks (`default-server check`) — the Ingress path isn't used as an HTTP health-check URI
- Round-robin load balancing
- Backend deduplication (multiple paths to same service share one backend)

**Generated backend naming convention:**

```
<namespace>_<ingress-name>_svc_<service-name>_<port-name>
```

`<port-name>` is the Service port's name when the port is named (for example `http`, `https`). When the Service port is unnamed — or the Service isn't yet in the controller's store — it falls back to the numeric port number (for example `..._svc_shop_80`).

**Example generated configuration:**

```haproxy
backend default_my-app_svc_api-service_http
    default-server check
    server SRV_1 10.0.0.1:8080 enabled    # Pod: api-pod-1
    server SRV_2 10.0.0.2:8080 enabled    # Pod: api-pod-2
    server SRV_3 192.0.2.1:1 disabled     # Reserved slot for future scale-up
```

`check` lives on `default-server` (not on individual server lines) so endpoint changes can be applied via the runtime API without a HAProxy reload. Reserved `disabled` slots get filled in at runtime when the backend scales up.

#### Backend namespace scope

An Ingress backend references a Service by name only — the Kubernetes API has no per-backend namespace field. HAPTIC therefore always resolves the Service, its EndpointSlices, and any `spec.tls` Secret in the Ingress's own namespace. A Service that doesn't exist in that namespace renders as a placeholder backend that serves 503 (and, for a port referenced by name, raises the `BackendUnresolved` Warning Event — see [Degraded backend events](#degraded-backend-events)). To route to a Service in a different namespace, use a Gateway API HTTPRoute with a `backendRef.namespace` and a matching ReferenceGrant — see [Cross-namespace routes](gateway.md#cross-namespace-routes-referencegrant); Ingress can't express a cross-namespace backend.

### WebSocket backends

WebSocket backends work without extra configuration. HAProxy tunnels the `Upgrade` handshake, so an Ingress routing to a WebSocket service needs no special annotation. Long-lived connections are bounded by HAProxy's `timeout tunnel`. Raise it per-backend with `haproxy.org/timeout-tunnel` (or `haproxy-ingress.github.io/timeout-tunnel`) when a connection must stay open longer:

```yaml
annotations:
  haproxy.org/timeout-tunnel: "1h"
```

### gRPC backends

gRPC runs over HTTP/2. Tell HAPTIC to speak HTTP/2 to a cleartext (h2c) backend with the native `haproxy-haptic.org/backend-protocol: h2` annotation:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: grpc-app
  namespace: default
  annotations:
    haproxy-haptic.org/backend-protocol: h2
spec:
  ingressClassName: haptic
  rules:
    - host: grpc.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: grpc-service
                port:
                  number: 50051
```

For a TLS backend, use `haproxy-haptic.org/backend-protocol: h2-ssl` (or `grpcs`) instead. The native [haptic-annotations](haptic-annotations.md) library is enabled by default; the opt-in vendor libraries name the same thing as `haproxy-ingress.github.io/backend-protocol: h2-ssl` or `nginx.ingress.kubernetes.io/backend-protocol: GRPC`/`GRPCS`. On the client side, HAProxy detects HTTP/2 cleartext prior-knowledge on the plaintext listener, so gRPC clients that dial insecurely still reach the backend.

### Backend config snippet

Custom HAProxy backend directives can be injected per-Ingress via the
`haproxy.org/backend-config-snippet` annotation. Processing of `haproxy.org/*`
annotations lives in the [haproxytech library](haproxytech.md), so the
annotation is honoured whenever `haproxytech` is enabled (default). See that
library's docs for the complete annotation reference.

## Status reporting

The Ingress library automatically propagates LoadBalancer addresses to Ingress `.status.loadBalancer` fields. This enables DNS controllers (like external-dns) and `kubectl get ingress` to display the correct external address.

Addresses are discovered from the controller's LoadBalancer Service. Once an address is available, each Ingress processed by the controller receives its `status.loadBalancer.ingress` entries. If deployment fails, the status is cleared to empty.

### Degraded backend events

An Ingress backend that references its Service port **by name** renders in a degraded shape while that Service is absent from the controller's store: the backend gets placeholder-only server slots and serves 503 until the Service appears. The render doesn't fail because an Ingress may legally be created before the Service it references — the base library resolves the missing reference to a port-less value and lets the backend converge on a later reconcile. That's correct during a propagation race — but a permanent Service-name typo looks exactly the same.

To make the difference visible, the controller emits a `Warning` Event (reason `BackendUnresolved`) on each affected Ingress, in the Ingress's namespace. The Event names every unresolvable Service and port name, so a typo shows up in:

```bash
kubectl describe ingress <name>
kubectl get events --field-selector reason=BackendUnresolved -A
```

The Event exists only while the backend stays placeholder-only:

- When the Service appears (or an EndpointSlice that carries the named port arrives), the Event is deleted on the next reconcile.
- Backends that already found real endpoints through an EndpointSlice never get an Event, even if the Service itself hasn't reached the store yet.
- By-number port references are trusted without Service validation and never produce this Event. Gateway API routes carry the equivalent signal in their own status instead (`ResolvedRefs: False`, reason `BackendNotFound`).

The Event's `metadata.creationTimestamp` tells you when the controller first observed the degradation. Kubernetes expires Events after the apiserver's `--event-ttl` (default 1 hour); the controller periodically refreshes the Event while the degradation persists, but the refresh rides on reconciliations — in a cluster with no resource changes at all for over an hour, the Event can lapse until the next reconcile re-creates it.

## Watched Resources

| Resource | API Version | Purpose |
|----------|-------------|---------|
| Ingresses | `networking.k8s.io/v1` | Traffic routing rules |
| Services | v1 | Service discovery |
| EndpointSlices | `discovery.k8s.io/v1` | Backend endpoint discovery |

### Field selector

The library watches and processes only Ingresses with `spec.ingressClassName: haptic`.

## Generated map files

The Ingress library contributes to these map files:

| Map File | Content |
|----------|---------|
| host.map | `hostname hostname` entries for each Ingress host |
| path-exact.map | `hostpath BACKEND:backendname` for Exact paths |
| path-prefix-exact.map | `hostpath BACKEND:backendname` for Prefix paths (exact match) |
| path-prefix.map | `hostpath/ BACKEND:backendname` for Prefix paths (prefix match) |

## Validation tests

The Ingress library includes these validation tests:

| Test | Description |
|------|-------------|
| `test-ingress-duplicate-backend-different-ports` | Multiple paths to same service with different ports (deduplication) |
| `test-ingress-tls-basic` | `spec.tls` registers TLS certificates into the SSL crt-list |
| `test-ingress-slot-preservation` | Existing pod slots survive a rolling deployment when `currentConfig` is provided |
| `test-ingress-slot-preservation-lower-ip` | Slot preservation is order-independent (new pod with a lower IP still gets the freed slot) |
| `test-ingress-status-patches` | LoadBalancer addresses from the controller Service propagate to `status.loadBalancer.ingress` |
| `test-ingress-endpoint-conditions-filter` | EndpointSlice endpoints with non-Ready conditions are excluded from the backend |
| `test-ingress-named-port` | Ingress referencing a service port by name resolves to the correct pod port |
| `test-ingress-named-port-typo-fails` | Ingress referencing a non-existent named port fails cleanly |
| `test-ingress-default-backend-rules-less` | Default backend on a rule-less Ingress generates a backend |
| `test-ingress-default-backend-with-rules-per-host` | Default backend coexists with per-host rules |
| `test-ingress-default-backend-newest-wins-per-host` | When multiple Ingresses supply a default backend for a host, the newest one wins |

Run a specific test with:

```bash
./scripts/test-templates.sh --test test-ingress-tls-basic
```

## See also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Extension points and routing infrastructure
- [SSL Library](ssl.md) - TLS certificate management
- [haproxytech library](haproxytech.md) - Additional Ingress annotations
- [haproxy-ingress library](haproxy-ingress.md) - Regex path type support
