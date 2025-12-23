# HAProxy Ingress Library

The HAProxy Ingress library provides compatibility with the [HAProxy Ingress Controller](https://haproxy-ingress.github.io/) annotation for regex path matching.

## Overview

This library enables the `haproxy-ingress.github.io/path-type: "regex"` annotation, allowing Ingress resources to use regular expression path matching via the `ImplementationSpecific` path type.

This library is enabled by default.

## Configuration

```yaml
controller:
  templateLibraries:
    haproxyIngress:
      enabled: true  # Enabled by default
```

## Extension Points

### Extension Points Used

The HAProxy Ingress library implements this extension point from base.yaml:

| Extension Point | This Library's Snippet | What It Generates |
|-----------------|------------------------|-------------------|
| Path Regex Map | `map-entry-path-regex-ingress-haproxy-ingress` | Regex path map entries for annotated Ingresses |

### Injecting Custom Configuration

You can extend regex path handling:

```yaml
controller:
  config:
    templateSnippets:
      # Add custom regex path entries
      map-path-regex-custom:
        template: |
          # Custom regex routes
          api.example.com ^/v[0-9]+/users/[0-9]+ BACKEND:users_backend
```

## Features

### Regex Path Type Annotation

When an Ingress has:

- `pathType: ImplementationSpecific` on a path
- `haproxy-ingress.github.io/path-type: "regex"` annotation

The path value is treated as a regular expression and matched using HAProxy's `map_reg()` function.

**Example:**

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: regex-app
  namespace: default
  annotations:
    haproxy-ingress.github.io/path-type: "regex"
spec:
  ingressClassName: haproxy
  rules:
    - host: api.example.com
      http:
        paths:
          - path: "^/api/v[0-9]+/users/[0-9]+$"
            pathType: ImplementationSpecific
            backend:
              service:
                name: users-service
                port:
                  number: 80
          - path: "^/api/v[0-9]+/orders/.*"
            pathType: ImplementationSpecific
            backend:
              service:
                name: orders-service
                port:
                  number: 80
```

### How It Works

1. Library scans all Ingresses for the `haproxy-ingress.github.io/path-type: "regex"` annotation
2. For matching Ingresses, paths with `pathType: ImplementationSpecific` are added to `path-regex.map`
3. HAProxy uses `map_reg()` to match incoming requests against these patterns

**Generated map entries:**

```
# haproxy-ingress/map-entry-path-regex-ingress-haproxy-ingress
# Ingress: default/regex-app (2 regex paths)
api.example.com^/api/v[0-9]+/users/[0-9]+$ BACKEND:ing_default_regex-app_users-service_80
api.example.com^/api/v[0-9]+/orders/.* BACKEND:ing_default_regex-app_orders-service_80
```

## Path Matching Priority

Regular expression paths are evaluated in the default order:

1. **Exact** paths (`pathType: Exact`)
2. **Regex** paths (`pathType: ImplementationSpecific` with annotation)
3. **Prefix** paths (`pathType: Prefix`)

!!! note "Performance Consideration"
    Regex matching is slower than exact or prefix matching. For performance-critical deployments, consider the [Path Regex Last library](path-regex-last.md) which evaluates regex paths last.

## Watched Resources

This library does not add additional watched resources. It uses the Ingress resources already watched by the [Ingress library](ingress.md).

## Compatibility Notes

| Feature | Support |
|---------|---------|
| `path-type: "regex"` | Full |
| `path-type: "exact"` | Not implemented (use `pathType: Exact`) |
| `path-type: "prefix"` | Not implemented (use `pathType: Prefix`) |
| `path-type: "begin"` | Not implemented |

Only the `regex` value is supported. Other path types should use the standard Kubernetes `pathType` field.

## See Also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Path matching infrastructure
- [Ingress Library](ingress.md) - Standard Ingress support
- [Path Regex Last Library](path-regex-last.md) - Alternative path matching order
- [HAProxy Ingress Documentation](https://haproxy-ingress.github.io/docs/configuration/keys/#path-type) - Original annotation reference
