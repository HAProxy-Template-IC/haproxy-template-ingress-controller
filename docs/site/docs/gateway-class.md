# GatewayClass

A GatewayClass selects the controller that manages a Gateway. The default
HAPTIC installation creates a class named `haptic`; set
`spec.gatewayClassName: haptic` on Gateways you want it to serve.

<a id="prerequisites"></a>
<a id="expose-a-service-through-a-gateway"></a>
<a id="step-1-deploy-a-sample-application"></a>
<a id="step-2-create-a-gateway"></a>
<a id="step-3-create-an-httproute"></a>
<a id="step-4-test-the-routing"></a>
<a id="remove-the-sample"></a>

To install the Gateway API CRDs and create your first route, follow the
[Gateway API walkthrough](gateway-api.md).

## Configuration

Add these settings to your [Helm values file](deploying-with-helm.md#change-settings)
and apply the complete file when you upgrade. The example shows the defaults:

```yaml
controller:
  templateLibraries:
    gateway:
      enabled: true

gatewayClass:
  enabled: true
  name: haptic
  default: false
  controllerName: haproxy-haptic.org/controller
  parametersRef:
    group: haproxy-haptic.org
    kind: HAProxyTemplateConfig
    name: ""        # Defaults to controller.configName
    namespace: ""   # Defaults to Release.Namespace
```

## Creation conditions

HAPTIC creates the GatewayClass when all three conditions are met:

1. `gatewayClass.enabled: true` (default)
2. `controller.templateLibraries.gateway.enabled: true` (default)
3. The cluster serves the `gateway.networking.k8s.io` `gatewayclasses` CRD

The controller manages the class at runtime. If the CRDs are absent, HAPTIC
still handles Ingresses; installing the CRDs later creates the class without a
Helm upgrade.

## `parametersRef` - controller configuration link

The generated GatewayClass uses `spec.parametersRef` to identify the release's
`HAProxyTemplateConfig`. This reference documents the association; changing it
doesn't make a running controller load a different configuration. Each controller
loads the configuration selected at startup.

Defaults:

- `parametersRef.name` defaults to `controller.configName` (typically `haptic-config`)
- `parametersRef.namespace` defaults to chart's release namespace

**Inspect the reference:**

```bash
kubectl get gatewayclass haptic -o yaml
```

## Multi-controller environments

Give each controller a distinct GatewayClass name and controller identifier.
For multiple HAPTIC installations, follow [Running multiple HAPTIC instances](deploying-with-helm.md#running-multiple-haptic-instances-in-one-cluster).
Gateways must set `spec.gatewayClassName`. The `gatewayClass.default` value only
emits the `gateway.networking.k8s.io/is-default-class` annotation; HAPTIC
doesn't use it to choose a class.

## Advanced: Multiple GatewayClasses

To separate internal and internet-facing traffic, install a HAPTIC release for
each fleet, with its own classes, configuration, and HAProxy Service. Use the
[separate-release example](deploying-with-helm.md#running-multiple-haptic-instances-in-one-cluster)
and configure each release's [HAProxy Service](haproxy-deployment.md#haproxy-service)
through its Helm values.

Creating another GatewayClass and setting `parametersRef` alone doesn't create
another controller or HAProxy fleet.

<a id="using-gatewayclass"></a>

## Disabling GatewayClass creation

If you manage GatewayClass resources separately:

```yaml
gatewayClass:
  enabled: false
```

## See also

- [Gateway API library](./libraries/gateway.md) — route types, listeners, and annotation support
- [Migrating to HAPTIC](./migrating.md) — running HAPTIC alongside another controller
