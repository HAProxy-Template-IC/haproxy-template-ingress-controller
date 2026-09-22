# IngressClass

Use an IngressClass to select which Ingresses HAPTIC manages. The [Helm chart](deploying-with-helm.md)
creates the `haptic` class by default and configures the controller to watch
Ingresses that name it. You only need the settings below to change that default
or run more than one controller.

## Configuration

Add these settings to your [Helm values file](deploying-with-helm.md#change-settings)
and apply the complete file when you upgrade. The example shows the defaults:

```yaml
ingressClass:
  enabled: true       # Create IngressClass (default: true)
  name: haptic        # IngressClass name (default: haptic, avoids conflict with other HAProxy controllers)
  default: false      # Mark as cluster default
  controllerName: haproxy-haptic.org/controller
```

For an existing controller's class name, follow the [migration procedure](migrating.md)
to transfer traffic and class ownership.

## Ingress class filtering

### Changing the class name

Add the class name to your existing Helm values:

```yaml
ingressClass:
  name: haproxy
```

[Apply the complete values file](deploying-with-helm.md#change-settings) to preserve
your other settings. This changes both the class name and HAPTIC's watch filter.
Existing Ingresses keep their previous `spec.ingressClassName`; update them when
you're ready to move their routes to the new class.

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

For field and label filtering, see [watch selectors](watching-resources.md#narrowing-the-watch).

### Ingresses without a class

An Ingress that omits `spec.ingressClassName` doesn't match the default `spec.ingressClassName=haptic` filter, so the controller doesn't watch it — its rules never reach HAProxy.

To select HAPTIC automatically for new Ingresses that omit a class, mark its
IngressClass as the cluster default:

```yaml
ingressClass:
  default: true
```

Kubernetes assigns this class to new Ingresses that omit `spec.ingressClassName`.
It doesn't rewrite existing Ingresses. Mark only one IngressClass as the cluster default.

To adopt an existing class-less Ingress named `my-app` in namespace `default`:

```bash
kubectl patch ingress my-app -n default --type=merge \
  -p '{"spec":{"ingressClassName":"haptic"}}'
```

## Creation conditions

IngressClass is created only when both of the following are true:

1. `ingressClass.enabled: true` (default)
2. `controller.templateLibraries.ingress.enabled: true` (default)

## Multi-controller environments

Give each controller its own IngressClass. An Ingress selects one class with
`spec.ingressClassName`; mark at most one class as the cluster default.

For two HAPTIC installations, configure distinct class names and controller
identifiers using [Running multiple HAPTIC instances](deploying-with-helm.md#running-multiple-haptic-instances-in-one-cluster).
For another controller, use that controller's chart settings to create its class.

<a id="using-ingressclass"></a>

## Disabling IngressClass creation

If you manage IngressClass resources separately or use an external tool:

```yaml
ingressClass:
  enabled: false
```

## See also

- [Annotations](./annotations.md) — native annotations and migration compatibility libraries
- [Migrating to HAPTIC](./migrating.md) — matching the incumbent controller's class during cutover
