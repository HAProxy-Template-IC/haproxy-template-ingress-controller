# Ingress Library

The Ingress library provides support for Kubernetes `networking.k8s.io/v1` Ingress resources, enabling traditional Ingress-based traffic routing through HAProxy.

## Overview

The Ingress library enables HAProxy to route traffic based on Kubernetes Ingress resources:

- Path-based routing with Exact, Prefix, and ImplementationSpecific path types
- Host-based routing via Ingress rules
- TLS termination via Ingress `spec.tls` configuration
- Backend generation with automatic endpoint discovery
- IngressClass filtering (default: `haproxy`)

This library is enabled by default.

## Configuration

```yaml
controller:
  templateLibraries:
    ingress:
      enabled: true  # Enabled by default
```

### Ingress Class Filtering

By default, only Ingresses with `spec.ingressClassName: haproxy` are processed. This is configured via field selector in the library's watched resources.

## Extension Points

### Extension Points Used

The Ingress library implements these extension points from base.yaml:

| Extension Point | This Library's Snippet | What It Generates |
|-----------------|------------------------|-------------------|
| Features | `features-ingress-tls` | TLS certificate registration (priority 100) |
| Host Map | `map-host-ingress` | Host-to-group mapping entries |
| Path Exact Map | `map-path-exact-ingress` | Exact path match entries |
| Path Prefix Exact Map | `map-path-prefix-exact-ingress` | Prefix paths matching exactly |
| Path Prefix Map | `map-path-prefix-ingress` | Prefix path match entries |
| Backends | `backends-ingress` | Backend definitions for Ingress services |

### Injecting Custom Configuration

You can extend Ingress functionality by adding snippets that match extension point patterns:

```yaml
controller:
  config:
    templateSnippets:
      # Add custom path map entries
      map-path-exact-custom:
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
  ingressClassName: haproxy
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
  ingressClassName: haproxy
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
ing_<namespace>_<ingress-name>_<service-name>_<port>
```

**Example generated configuration:**

```haproxy
backend ing_default_my-app_api-service_80
    balance roundrobin
    option httpchk GET /api
    default-server check
    server SRV_1 10.0.0.1:8080 check  # Pod: api-pod-1
    server SRV_2 10.0.0.2:8080 check  # Pod: api-pod-2
```

### Backend Config Snippet

Inject custom HAProxy directives into backends using the `haproxy.org/backend-config-snippet` annotation:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: custom-backend
  annotations:
    haproxy.org/backend-config-snippet: |
      http-request set-header X-Custom-Header "value"
      timeout server 120s
spec:
  # ...
```

## Watched Resources

| Resource | API Version | Purpose |
|----------|-------------|---------|
| Ingresses | networking.k8s.io/v1 | Traffic routing rules |
| Services | v1 | Service discovery |
| EndpointSlices | discovery.k8s.io/v1 | Backend endpoint discovery |

### Field Selector

Ingresses are filtered by `spec.ingressClassName=haproxy`. Only matching Ingresses are watched and processed.

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
| `test-ingress-duplicate-backend-different-ports` | Multiple paths to same service with different ports |

Run tests with:

```bash
./scripts/test-templates.sh --test test-ingress-duplicate-backend-different-ports
```

## See Also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Extension points and routing infrastructure
- [SSL Library](ssl.md) - TLS certificate management
- [HAProxy Annotations Library](haproxytech.md) - Additional Ingress annotations
- [HAProxy Ingress Library](haproxy-ingress.md) - Regex path type support
