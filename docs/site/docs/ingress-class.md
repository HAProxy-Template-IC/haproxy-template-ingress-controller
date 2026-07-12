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

By default, the controller watches only Ingress resources with `spec.ingressClassName: haptic`.

### Changing the class name

`ingressClass.name` is the single knob. The chart uses one value for two things at once:

- It names the created IngressClass resource (`metadata.name`).
- It derives the watch filter, injecting `spec.ingressClassName=<name>` as the `watchedResources.ingresses.fieldSelector` the controller applies.

So setting `ingressClass.name` keeps the IngressClass name and the watch filter in sync — you don't edit the field selector by hand. Install or upgrade with the class you want:

```bash
helm upgrade --install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.1 \
  --namespace haptic --create-namespace \
  --set ingressClass.name=haproxy
```

Your Ingresses then opt in with `spec.ingressClassName: haproxy`.

### Watching all Ingresses regardless of class

To watch every Ingress the API server returns, override the derived filter directly with an empty `fieldSelector`:

```yaml
controller:
  config:
    watchedResources:
      ingresses:
        fieldSelector: ""
```

A `controller.config.watchedResources.ingresses.fieldSelector` value takes precedence over the filter derived from `ingressClass.name`, but it changes only the watch filter — the created IngressClass keeps the name from `ingressClass.name` (default `haptic`). Prefer `ingressClass.name` unless you need a filter that isn't a plain class-name match.

The `fieldSelector` here is client-side JSONPath filtering (it can match any field), not Kubernetes' server-side field selector; for the cheaper server-side option, filter with `labelSelector` on the same entry. See [Watching Resources → Narrowing the Watch](watching-resources.md#narrowing-the-watch).

### Ingresses without a class

An Ingress that omits `spec.ingressClassName` doesn't match the default `spec.ingressClassName=haptic` filter, so the controller doesn't watch it — its rules never reach HAProxy.

To make HAPTIC adopt class-less Ingresses, mark its IngressClass as the cluster default:

```yaml
ingressClass:
  default: true
```

This adds the `ingressclass.kubernetes.io/is-default-class: "true"` annotation to the IngressClass. The Kubernetes API server then stamps the class name (`haptic` by default, or whatever you set as `ingressClass.name`) into `spec.ingressClassName` on any Ingress **created** without a class — at creation time only. Ingresses that already exist without a class aren't rewritten, so the controller keeps ignoring them; set their `spec.ingressClassName` explicitly to adopt them. Mark only one IngressClass as the cluster default.

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
