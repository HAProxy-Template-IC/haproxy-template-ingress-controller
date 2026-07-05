# Ingress Library

The Ingress library provides support for Kubernetes `networking.k8s.io/v1` Ingress resources, enabling traditional Ingress-based traffic routing through HAProxy.

## Overview

The Ingress library enables HAProxy to route traffic based on Kubernetes Ingress resources:

- Path-based routing with Exact, Prefix, and ImplementationSpecific path types
- Host-based routing via Ingress rules
- TLS termination via Ingress `spec.tls` configuration
- Backend generation with automatic endpoint discovery
- IngressClass filtering (default: `haptic`)

This library is enabled by default.

## Configuration

```yaml
controller:
  templateLibraries:
    ingress:
      enabled: true  # Enabled by default
```

### Ingress Class Filtering

By default, only Ingresses with `spec.ingressClassName: haptic` are processed. This is configured via field selector in the library's watched resources. Override `ingressClass.name` to match an incumbent controller's class (often `haproxy`) when replacing one in-place.

## Extension Points

The Ingress library hooks into these extension points from base.yaml. Snippet names match what's emitted in `libraries/ingress.yaml`.

| Extension Point | Snippet | What It Generates |
|-----------------|---------|-------------------|
| `features-*` | `features-100-ingress-bind` | Sets `gf["bindHTTPDefault"]` / `gf["needHTTPFrontend"]`; for Ingresses with `spec.tls` also sets `gf["bindHTTPSDefault"]`, `gf["needHTTPSFrontend"]`, `gf["needHTTPSTermination"]` |
| `features-*` | `features-100-ingress-tls` | Registers TLS Secrets from `ingress.spec.tls[]` into `gf["tlsCertificates"]` for the SSL library's CRT-list |
| `backends-*` | `backends-500-ingress` | Backend blocks per unique `(namespace, service, port)` referenced by an Ingress |
| `map-host-*` | `map-host-500-ingress` | Host → group entries derived from `ingress.spec.rules[].host` |
| `map-path-exact-*` | `map-path-exact-500-ingress` | Entries for `pathType: Exact` paths |
| `map-pfxexact-*` | `map-pfxexact-500-ingress` | Prefix-exact entries emitted when `pathType: Prefix` paths need to match their exact boundary |
| `map-path-prefix-*` | `map-path-prefix-500-ingress` | Prefix entries for `pathType: Prefix` paths |
| `status-patches-*` | `status-patches-200-ingress` | Patches the LoadBalancer status on each matched Ingress |

Regex-path matching is not emitted by this library directly — it comes from the `haproxy-ingress` library's `map-path-regex-600-haproxy-ingress` snippet (enabled by default) which handles the `haproxy-ingress.github.io/path-type: regex` annotation.

### Injecting Custom Configuration

You can extend Ingress functionality by adding snippets with the right extension-point prefix and a priority that places them correctly alongside the built-in 500-range entries:

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

### Path Types

The Ingress library supports all standard Kubernetes Ingress path types:

| Path Type | HAProxy Matcher | Description |
|-----------|-----------------|-------------|
| `Exact` | `map()` | Path must match exactly |
| `Prefix` | `map_beg()` | Path must start with value |
| `ImplementationSpecific` | `map_beg()` | Treated as Prefix by default |

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

### Backend Generation

Backends are generated with:

- Automatic endpoint discovery via EndpointSlices
- Health checks using the path from the Ingress rule
- Round-robin load balancing
- Backend deduplication (multiple paths to same service share one backend)

**Generated backend naming convention:**

```
<namespace>_<ingress-name>_svc_<service-name>_<port-name>
```

`<port-name>` is the port name from the Service spec (e.g. `http`, `https`), not the numeric port number.

**Example generated configuration:**

```haproxy
backend default_my-app_svc_api-service_http
    balance roundrobin
    option httpchk GET /api
    default-server check
    server SRV_1 10.0.0.1:8080 enabled    # Pod: api-pod-1
    server SRV_2 10.0.0.2:8080 enabled    # Pod: api-pod-2
    server SRV_3 192.0.2.1:1 disabled     # Reserved slot for future scale-up
```

`check` lives on `default-server` (not on individual server lines) so endpoint changes can be applied via the runtime API without a HAProxy reload. Reserved `disabled` slots get filled in at runtime when the backend scales up.

### Backend Config Snippet

Custom HAProxy backend directives can be injected per-Ingress via the
`haproxy.org/backend-config-snippet` annotation. Processing of `haproxy.org/*`
annotations lives in the [haproxytech library](haproxytech.md), so the
annotation is honoured whenever `haproxytech` is enabled (default). See that
library's docs for the complete annotation reference.

## Status Reporting

The Ingress library automatically propagates LoadBalancer addresses to Ingress `.status.loadBalancer` fields. This enables DNS controllers (like external-dns) and `kubectl get ingress` to display the correct external address.

Addresses are discovered from the controller's LoadBalancer Service. Once an address is available, each Ingress processed by the controller receives its `status.loadBalancer.ingress` entries. If deployment fails, the status is cleared to empty.

### Degraded backend Events

An Ingress backend that references its Service port **by name** renders in a degraded shape while that Service is absent from the controller's store: the backend gets placeholder-only server slots and serves 503 until the Service appears (see the [base library's service port resolution](base.md) for why the render doesn't fail). That's correct during a propagation race — but a permanent Service-name typo looks exactly the same.

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
| Ingresses | networking.k8s.io/v1 | Traffic routing rules |
| Services | v1 | Service discovery |
| EndpointSlices | discovery.k8s.io/v1 | Backend endpoint discovery |

### Field Selector

Ingresses are filtered by `spec.ingressClassName=haptic`. Only matching Ingresses are watched and processed.

## Generated Map Files

The Ingress library contributes to these map files:

| Map File | Content |
|----------|---------|
| host.map | `hostname hostname` entries for each Ingress host |
| path-exact.map | `hostpath BACKEND:backendname` for Exact paths |
| path-prefix-exact.map | `hostpath BACKEND:backendname` for Prefix paths (exact match) |
| path-prefix.map | `hostpath/ BACKEND:backendname` for Prefix paths (prefix match) |

## Validation Tests

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

## See Also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Extension points and routing infrastructure
- [SSL Library](ssl.md) - TLS certificate management
- [haproxytech library](haproxytech.md) - Additional Ingress annotations
- [haproxy-ingress library](haproxy-ingress.md) - Regex path type support
