# GatewayClass

A GatewayClass selects the controller that manages a Gateway. HAPTIC creates the
`haptic` class when the Gateway library and class creation are enabled and the
Gateway API CRDs are installed. The controller manages this resource at runtime,
so installing the CRDs later doesn't require a Helm upgrade.

## Prerequisites

For the GatewayClass to appear, install the Gateway API CRDs (standard channel):

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.6.0/standard-install.yaml
```

Check [Gateway API releases](https://github.com/kubernetes-sigs/gateway-api/releases) for newer versions.

The v1.6.0 standard channel ships every route kind HAPTIC supports — HTTPRoute, GRPCRoute, TLSRoute, and TCPRoute. On older Gateway API releases some kinds live only in the experimental channel (`experimental-install.yaml`): TLSRoute before v1.5 and TCPRoute before v1.6. See [Supported Gateway API versions and channels](./libraries/gateway.md#supported-gateway-api-versions-and-channels) for the full split.

If the CRDs are absent, nothing is emitted and the rest of the install proceeds normally. Install the CRDs later and the controller creates the GatewayClass by itself — no `helm upgrade` required.

## Expose a Service through a Gateway

Route `echo.example.local` to a sample app with a Gateway and an HTTPRoute.
Before you start, [install HAPTIC](./getting-started.md#install-with-helm) and the
[Gateway API CRDs](#prerequisites). The example uses the default `haptic` class.

### Step 1: Deploy a sample application

Create an echo Deployment and Service in the `default` namespace:

```bash
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: echo
  namespace: default
spec:
  replicas: 2
  selector:
    matchLabels:
      app: echo
  template:
    metadata:
      labels:
        app: echo
    spec:
      containers:
        - name: echo
          image: ealen/echo-server:latest
          ports:
            - containerPort: 80
          env:
            - name: PORT
              value: "80"
---
apiVersion: v1
kind: Service
metadata:
  name: echo
  namespace: default
spec:
  selector:
    app: echo
  ports:
    - port: 80
      targetPort: 80
EOF
```

Wait for the sample application to become ready:

```bash
kubectl rollout status deployment/echo --namespace default --timeout=120s
```

### Step 2: Create a Gateway

Create a Gateway that references the `haptic` GatewayClass and opens an HTTP listener on port 80:

```bash
kubectl apply -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: edge
  namespace: default
spec:
  gatewayClassName: haptic
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: Same
EOF
```

The listener's `allowedRoutes.namespaces.from: Same` lets routes in the Gateway's own namespace (`default`) attach. HAPTIC serves Gateway listeners on the chart-static HTTP port (`haproxy.ports.http`, default 80) through the shared HAProxy pods, so no per-Gateway address is needed to test locally.

### Step 3: Create an HTTPRoute

Attach an HTTPRoute to the Gateway that forwards `echo.example.local` to the echo Service:

```bash
kubectl apply -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: echo
  namespace: default
spec:
  parentRefs:
    - name: edge
  hostnames:
    - echo.example.local
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: echo
          port: 80
EOF
```

The controller detects the Gateway and HTTPRoute, renders the HAProxy configuration, and deploys it to the HAProxy pods.

### Step 4: Test the routing

Port-forward to the shared HAProxy Service and send a request with the route's hostname:

```bash
kubectl port-forward -n haptic svc/haptic-haproxy 8080:80
```

In another terminal:

```bash
curl -H "Host: echo.example.local" http://localhost:8080/
```

You receive a response from the echo server. Confirm the controller wrote `Accepted` and `Programmed` conditions back to the Gateway:

```bash
kubectl get gateway edge -n default -o yaml
```

For every route type (HTTP, gRPC, TLS, TCP), listener option, and status condition, see the [Gateway API library](./libraries/gateway.md).

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
    name: ""        # Defaults to controller.configName
    namespace: ""   # Defaults to Release.Namespace
```

## Creation conditions

The GatewayClass exists only when **all** the following are true. Conditions 1 and 2 are chart-time; condition 3 is re-evaluated by the controller at runtime:

1. `gatewayClass.enabled: true` (default)
2. `controller.templateLibraries.gateway.enabled: true` (default)
3. The cluster serves the `gateway.networking.k8s.io` `gatewayclasses` CRD

If the API is absent, nothing is emitted and the rest of the install proceeds normally. Because condition 3 is a runtime check, installing the CRDs later is enough — the controller notices and creates the GatewayClass.

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
