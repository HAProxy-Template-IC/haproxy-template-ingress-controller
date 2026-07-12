# IngressClass

The [HAPTIC Helm chart](deploying-with-helm.md) automatically creates an IngressClass resource when the ingress library is enabled and the cluster exposes `networking.k8s.io/v1/IngressClass` (available since Kubernetes 1.19, below the chart's 1.21 minimum).

## Configuration

```yaml
ingressClass:
  enabled: true       # Create IngressClass (default: true)
  name: haptic        # IngressClass name (default: haptic, avoids conflict with other HAProxy controllers)
  default: false      # Mark as cluster default
  controllerName: haproxy-haptic.org/controller
```

The default name is `haptic` (not `haproxy`) so the chart can be installed alongside other HAProxy-based ingress controllers without colliding on IngressClass. When replacing an existing controller, override `ingressClass.name` to match your incumbent's class (often `haproxy`) and your existing Ingress manifests keep working.

## Ingress class filtering

By default, the controller only watches Ingress resources with `spec.ingressClassName: haptic`.

**Default behavior:**

```yaml
controller:
  config:
    watchedResources:
      ingresses:
        fieldSelector: "spec.ingressClassName=haptic"
```

**To change the ingress class name:**

```yaml
controller:
  config:
    watchedResources:
      ingresses:
        fieldSelector: "spec.ingressClassName=my-custom-class"
```

**To watch all ingresses regardless of class:**

```yaml
controller:
  config:
    watchedResources:
      ingresses:
        fieldSelector: ""
```

`fieldSelector` here is client-side JSONPath filtering, not the Kubernetes
server-side `fieldSelector` (which only supports a handful of fields like
`metadata.name`). The controller fetches all Ingresses the API server returns
and drops the ones whose `spec.ingressClassName` doesn't match before adding
them to the store. To narrow the watch *server-side* — the cheaper option
when you can use it — set `labelSelector` (server-side label match) on the
same entry. See [Watching Resources →
Narrowing the Watch](watching-resources.md#narrowing-the-watch).

## Creation conditions

IngressClass is created only when both of the following are true:

1. `ingressClass.enabled: true` (default)
2. `controller.templateLibraries.ingress.enabled: true` (default)

A third, internal condition — the chart checks that the `networking.k8s.io/v1/IngressClass` API exists — always holds on a supported (1.21+) cluster, since IngressClass reached v1 in Kubernetes 1.19.

## Multi-controller environments

When running multiple ingress controllers:

**Ensure unique identification:**

```yaml
# Controller 1 (haptic)
ingressClass:
  name: haptic
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

## Using IngressClass

Ingress resources opt in to HAPTIC by referencing the class via `spec.ingressClassName`:

```yaml
spec:
  ingressClassName: haptic  # References IngressClass.metadata.name
```

For a complete Ingress walkthrough, see [Getting Started](./getting-started.md#step-3-create-an-ingress-resource).

## Disabling IngressClass creation

If you manage IngressClass resources separately or use an external tool:

```yaml
ingressClass:
  enabled: false
```

## See also

- [Annotations](./annotations.md) — per-Ingress behavior via vendor annotation libraries
- [Migrating to HAPTIC](./migrating.md) — matching the incumbent controller's class during cutover
