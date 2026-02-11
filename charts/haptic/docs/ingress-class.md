# IngressClass

## Overview

The chart automatically creates an IngressClass resource when the ingress library is enabled and Kubernetes 1.18+ is detected. This page covers IngressClass configuration, capability detection, and multi-controller environments.

For ingress class filtering (controlling which Ingress resources the controller watches), see [Configuration](./configuration.md#ingress-class-filtering).

## Configuration

```yaml
ingressClass:
  enabled: true       # Create IngressClass (default: true)
  name: haproxy       # IngressClass name
  default: false      # Mark as cluster default
  controllerName: haproxy-haptic.org/controller
```

## Capability Detection

The chart uses `Capabilities.APIVersions.Has` to check for `networking.k8s.io/v1/IngressClass`. If the API is not available (Kubernetes < 1.18), the resource is silently skipped without error.

## Creation Conditions

IngressClass is created only when ALL of the following are true:

1. `ingressClass.enabled: true` (default)
2. `controller.templateLibraries.ingress.enabled: true` (default)
3. `networking.k8s.io/v1/IngressClass` API exists in cluster

## Multi-Controller Environments

When running multiple ingress controllers:

**Ensure unique identification:**

```yaml
# Controller 1 (haptic)
ingressClass:
  name: haproxy
  controllerName: haproxy-haptic.org/controller

# Controller 2 (nginx)
ingressClass:
  name: nginx
  controllerName: k8s.io/ingress-nginx
```

**Only one should be default:**

```yaml
# Set default on one controller only
ingressClass:
  default: true  # Only on ONE controller
```

## Disabling IngressClass Creation

If you manage IngressClass resources separately or use an external tool:

```yaml
ingressClass:
  enabled: false
```

## Using IngressClass

Ingress resources reference the IngressClass via `spec.ingressClassName`:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: example
spec:
  ingressClassName: haproxy  # References IngressClass.metadata.name
  rules:
    - host: example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: example-service
                port:
                  number: 80
```
