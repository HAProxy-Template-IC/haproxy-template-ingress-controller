# GatewayClass

The [HAPTIC Helm chart](deploying-with-helm.md) automatically creates a GatewayClass resource when the gateway library is enabled and Gateway API CRDs are installed.

## Prerequisites

For the chart to create the GatewayClass, install the Gateway API CRDs (standard channel) first:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.6.0/standard-install.yaml
```

Check [Gateway API releases](https://github.com/kubernetes-sigs/gateway-api/releases) for newer versions.

If the CRDs are absent, the chart skips the GatewayClass and installs everything else normally. Install the CRDs later and re-run `helm upgrade` to create it.

## Configuration

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
    name: ""        # Defaults to controller.crdName
    namespace: ""   # Defaults to Release.Namespace
```

## Creation conditions

The chart creates the GatewayClass only when **all** the following are true:

1. `gatewayClass.enabled: true` (default)
2. `controller.templateLibraries.gateway.enabled: true` (default)
3. The `gateway.networking.k8s.io/v1/GatewayClass` API exists in the cluster (Gateway API CRDs are installed)

If the API is absent, the chart skips the GatewayClass without error and installs the rest normally.

## `parametersRef` - controller configuration link

The GatewayClass automatically references the HAProxyTemplateConfig created by this chart via `parametersRef`. The reference records which HAProxyTemplateConfig drives Gateways of this class — useful when you run several classes with different configs.

**How it works:**

1. GatewayClass points to HAProxyTemplateConfig via `spec.parametersRef`
2. Controller reads HAProxyTemplateConfig for template snippets, maps, watched resources, and HAProxy configuration
3. Gateway API consumers get the same routing capabilities as Ingress consumers

**Default behavior:**

- `parametersRef.name` defaults to `controller.crdName` (typically `haptic-config`)
- `parametersRef.namespace` defaults to chart's release namespace

**Inspect the reference:**

```bash
kubectl get gatewayclass haptic -o yaml
```

## Multi-controller environments

When running multiple Gateway API controllers:

**Ensure unique identification:**

```yaml
# Controller 1 (haptic)
gatewayClass:
  name: haptic
  controllerName: haproxy-haptic.org/controller

# Controller 2 (nginx-gateway-fabric)
gatewayClass:
  name: nginx
  controllerName: gateway.nginx.org/nginx-gateway-controller
```

**Only one should be default:**

```yaml
# Set default on one controller only
gatewayClass:
  default: true  # Only on ONE controller
```

## Advanced: Multiple GatewayClasses

You can create multiple GatewayClasses pointing to different HAProxyTemplateConfig resources for different routing scenarios (for example internet-facing vs internal):

```bash
# Install chart with default config
helm install haproxy-internet oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic --version 0.2.0-alpha.1

# Create separate HAProxyTemplateConfig for internal traffic with different templates
kubectl apply -f - <<EOF
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: haproxy-internal-config
  namespace: default
spec:
  podSelector:
    matchLabels:
      app: haproxy-internal
  # ... different template configuration ...
EOF

# Create additional GatewayClass pointing to the internal config
kubectl apply -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: haproxy-internal
spec:
  controllerName: haproxy-haptic.org/controller
  parametersRef:
    group: haproxy-haptic.org
    kind: HAProxyTemplateConfig
    name: haproxy-internal-config
    namespace: default
EOF
```

## Using GatewayClass

Gateway resources opt in to HAPTIC by referencing the class via `spec.gatewayClassName`; routes then attach to the Gateway via `spec.parentRefs`:

```yaml
spec:
  gatewayClassName: haptic  # References GatewayClass.metadata.name
```

For the supported route types (HTTP, gRPC, TLS, TCP) and worked examples, see the [Gateway API library](./libraries/gateway.md).

## Disabling GatewayClass creation

If you manage GatewayClass resources separately:

```yaml
gatewayClass:
  enabled: false
```

## See also

- [Gateway API library](./libraries/gateway.md) — route types, listeners, and annotation support
- [Migrating to HAPTIC](./migrating.md) — running HAPTIC alongside another controller
