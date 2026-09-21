# IngressClass

Use an IngressClass to select which Ingresses HAPTIC manages. The [Helm chart](deploying-with-helm.md)
creates the `haptic` class by default and configures the controller to watch
Ingresses that name it.

## Configuration

```yaml
ingressClass:
  enabled: true       # Create IngressClass (default: true)
  name: haptic        # IngressClass name (default: haptic, avoids conflict with other HAProxy controllers)
  default: false      # Mark as cluster default
  controllerName: haproxy-haptic.org/controller
```

The default class name is `haptic`. During a migration, you can retain existing
Ingress class references by setting `ingressClass.name` to match them. Follow the
[migration procedure](migrating.md) to transfer traffic and class ownership.

## Ingress class filtering

By default, the controller watches only Ingress resources with `spec.ingressClassName: haptic`.

### Changing the class name

Set `ingressClass.name` to change both the IngressClass name and the controller's
watch filter. Install or upgrade with the class you want:

```bash
helm upgrade --install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.3 \
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

To adopt an existing class-less Ingress named `my-app` in namespace `default`:

```bash
kubectl patch ingress my-app -n default --type=merge \
  -p '{"spec":{"ingressClassName":"haptic"}}'
```

## Creation conditions

IngressClass is created only when both of the following are true:

1. `ingressClass.enabled: true` (default)
2. `controller.templateLibraries.ingress.enabled: true` (default)

The chart also checks that the IngressClass API exists. Supported Kubernetes
versions provide it.

## Multi-controller environments

Give each controller its own IngressClass. An Ingress selects one class with
`spec.ingressClassName`; mark at most one class as the cluster default.

For two HAPTIC installations, configure distinct class names and controller
identifiers using [Running multiple HAPTIC instances](deploying-with-helm.md#running-multiple-haptic-instances-in-one-cluster).
For another controller, use that controller's chart settings to create its class.

## Using IngressClass

Ingress resources opt in to HAPTIC by referencing the class via `spec.ingressClassName`:

```yaml
spec:
  ingressClassName: haptic  # References IngressClass.metadata.name
```

For a complete Ingress walkthrough, see [Getting Started](./getting-started.md#create-an-ingress).

## Disabling IngressClass creation

If you manage IngressClass resources separately or use an external tool:

```yaml
ingressClass:
  enabled: false
```

## See also

- [Annotations](./annotations.md) — per-Ingress behavior via vendor annotation libraries
- [Migrating to HAPTIC](./migrating.md) — matching the incumbent controller's class during cutover
