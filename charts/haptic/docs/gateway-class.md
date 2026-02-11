# GatewayClass

## Overview

The chart automatically creates a GatewayClass resource when the gateway library is enabled and Gateway API CRDs are installed. This page covers GatewayClass configuration, the parametersRef mechanism, and advanced multi-GatewayClass scenarios.

## Prerequisites

Install Gateway API CRDs (standard channel) before enabling the gateway library:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/standard-install.yaml
```

Check [Gateway API releases](https://github.com/kubernetes-sigs/gateway-api/releases) for newer versions.

## Configuration

```yaml
controller:
  templateLibraries:
    gateway:
      enabled: true

gatewayClass:
  enabled: true
  name: haproxy
  default: false
  controllerName: haproxy-haptic.org/controller
  parametersRef:
    group: haproxy-haptic.org
    kind: HAProxyTemplateConfig
    name: ""        # Defaults to controller.crdName
    namespace: ""   # Defaults to Release.Namespace
```

## Capability Detection

The chart checks for `gateway.networking.k8s.io/v1/GatewayClass` before creating the resource. If Gateway API CRDs are not installed, the resource is silently skipped without error.

## Creation Conditions

GatewayClass is created only when ALL of the following are true:

1. `gatewayClass.enabled: true` (default)
2. `controller.templateLibraries.gateway.enabled: true` (must be explicitly enabled)
3. `gateway.networking.k8s.io/v1/GatewayClass` API exists in cluster

## parametersRef - Controller Configuration Link

The GatewayClass automatically references the HAProxyTemplateConfig created by this chart via `parametersRef`. This links Gateway API configuration to the controller's template-based configuration system.

**How it works:**

1. GatewayClass points to HAProxyTemplateConfig via `spec.parametersRef`
2. Controller reads HAProxyTemplateConfig for template snippets, maps, watched resources, and HAProxy configuration
3. Gateway API consumers get the same routing capabilities as Ingress consumers

**Default behavior:**

- `parametersRef.name` defaults to `controller.crdName` (typically `haptic-config`)
- `parametersRef.namespace` defaults to chart's release namespace

**Inspect the reference:**

```bash
kubectl get gatewayclass haproxy -o yaml
```

## Multi-Controller Environments

When running multiple Gateway API controllers:

**Ensure unique identification:**

```yaml
# Controller 1 (haptic)
gatewayClass:
  name: haproxy
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

You can create multiple GatewayClasses pointing to different HAProxyTemplateConfig resources for different routing scenarios (e.g., internet-facing vs internal):

```bash
# Install chart with default config
helm install haproxy-internet ./charts/haptic

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

Gateway resources reference the GatewayClass, and HTTPRoutes attach to Gateways:

**1. Create a Gateway:**

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: example-gateway
spec:
  gatewayClassName: haproxy  # References GatewayClass.metadata.name
  listeners:
    - name: http
      protocol: HTTP
      port: 80
```

**2. Create HTTPRoutes that attach to the Gateway:**

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: example-route
spec:
  parentRefs:
    - name: example-gateway  # References Gateway.metadata.name
  hostnames:
    - "example.com"
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: example-service
          port: 80
```

## Disabling GatewayClass Creation

If you manage GatewayClass resources separately:

```yaml
gatewayClass:
  enabled: false
```
