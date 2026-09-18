---
description: "Get started with HAPTIC, the template-driven HAProxy ingress controller for Kubernetes. Install with Helm, deploy HAProxy, and verify your setup."
hide:
  - navigation
---

# Getting started

## Overview

Install HAPTIC with Helm, then route traffic through an Ingress. The optional
sample app lets you inspect the generated HAProxy configuration and test a request
from your terminal.

Try the bundled Ingress configuration in your browser. Click **Run live**, then
edit the sample Ingress resources to see the generated backends and routing maps.

<div class="pg-embed" markdown data-scenario="ingress" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Ingress resources become an HAProxy config" data-height="480">

</div>

## Prerequisites

- A Kubernetes 1.33 or newer cluster
- `kubectl` configured to access the cluster
- Helm 3.8 or newer

!!! note "Webhook validation"
    The admission webhook is enabled by default. It rejects Ingress, HTTPRoute, and GRPCRoute changes that fail validation. The chart issues its certificate; cert-manager is optional. For rotation and certificate alternatives, see [Webhook certificates](./ssl-certificates.md#webhook-certificates).

## Install with Helm

Install the controller and HAProxy using Helm:

```bash
# Install from OCI registry (deploys both controller and HAProxy pods)
helm install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.3 \
  --namespace haptic --create-namespace
```

The Helm chart deploys:

- **Controller**: Watches Kubernetes resources and generates HAProxy configurations
- **HAProxy pods**: Load balancers, each with the HAPTIC agent alongside (2 replicas by default)
- **Resource permissions**: Access to the Kubernetes resources HAPTIC watches
- **Configuration and libraries**: An `HAProxyTemplateConfig` and its referenced [template libraries](template-libraries.md), with Ingress and Gateway API support enabled

The chart creates a default HTTPS certificate. It uses cert-manager for issuance
and renewal when available; otherwise, it creates a self-signed certificate.
For your own domains, configure [SSL certificates](./ssl-certificates.md).

Verify both components are running:

```bash
# Check controller
kubectl get pods -n haptic -l app.kubernetes.io/component=controller

# Check HAProxy pods
kubectl get pods -n haptic -l app.kubernetes.io/component=loadbalancer
```

You should see two controller pods (the chart defaults to two replicas with leader election) and two HAProxy pods, all in `Running` state with full readiness (`2/2` and `4/4`). The controller pod runs the controller plus its validator sidecar; each HAProxy pod runs `haproxy`, the HAPTIC agent, the SPOA hub, and the Vector log/metrics sidecar.

!!! note "HAProxy version"
    The chart defaults to HAProxy 3.4. To pin a different series, set `--set haproxyVersion=3.0`. See [HAProxy Versions](./operations/haproxy-versions.md) for the full list and support status.

## HAPTIC is running

The bundled libraries handle Ingress and Gateway API routing without custom templates:

- **Ingress** — set `ingressClassName: haptic`. Use [native annotations](./libraries/haptic-annotations.md) for authentication, rate limits, redirects, and other route policies. When [migrating](./migrating.md), enable the library for your existing annotation prefix.
- **Gateway API** — create a `Gateway` with `gatewayClassName: haptic` and attach `HTTPRoute` resources; see the [Gateway library](./libraries/gateway.md) and [GatewayClass setup](./gateway-class.md).

Select HAPTIC's class on your routing resources and ensure their backend Services have ready endpoints. Install the Gateway API CRDs before creating Gateways or routes; see [GatewayClass setup](./gateway-class.md).

## Optional walkthrough: route a sample app

The rest of this guide deploys a sample app and confirms routing end to end. Skip it if you'll use your own Ingress or Gateway resources.

### Deploy a sample app

Create a simple echo service:

```yaml
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
```

Save as `echo-app.yaml` and apply:

```bash
kubectl apply -f echo-app.yaml
```

### Create an Ingress

Create an Ingress that routes your test hostname to the echo service:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: echo-ingress
  namespace: default
spec:
  ingressClassName: haptic
  rules:
  - host: echo.example.local
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: echo
            port:
              number: 80
```

Save as `echo-ingress.yaml` and apply:

```bash
kubectl apply -f echo-ingress.yaml
```

The controller detects the Ingress, renders the HAProxy configuration, and deploys it to the HAProxy pods. See [What's Happening Behind the Scenes](#whats-happening-behind-the-scenes) for details.

!!! tip "TLS for a host"
    This Ingress serves HTTP and HTTPS. Without `spec.tls`, HTTPS uses the chart's [default certificate](./ssl-certificates.md). To use a certificate for your hostname, add a `spec.tls` entry referencing a `kubernetes.io/tls` Secret. See [TLS configuration](./libraries/ingress.md#tls-configuration) for certificate setup or HTTP-only routing.

### Verify the configuration

#### Check the controller logs

Watch the controller process the Ingress:

```bash
kubectl logs -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller --tail=50 -f
```

At the default `info` log level, each change produces a single consolidated `Reconciliation` summary line from the leader replica, for example:

```text
level=INFO msg=Reconciliation trigger=resource_change instances=2/2 reloads=2 ops=30 render_ms=1 validate_ms=1 deploy_ms=184 total_ms=289 backend_create=2 server_create=20 server_update=8 map_update=6
```

The summary reports the trigger, updated instances, reloads, runtime operations, and phase timings. For individual stages, [enable debug logging](./troubleshooting.md#enable-debug-logging).

#### Inspect the rendered HAProxy configuration

Inspect the rendered configuration in the controller-managed `HAProxyCfg` resource:

```bash
kubectl describe haproxycfg -n haptic
```

You should see:

- A frontend section with routing rules
- A backend section referencing the echo service
- Server entries pointing to the echo pod endpoints

!!! note "Output vs input"
    `HAProxyCfg` is controller output. To change the configuration durably, update `controller.config` in your Helm values and upgrade the release. Editing the output doesn't change the templates. The rendered config alone doesn't confirm that every pod has applied it; check deployment status and test the route.

### Test the routing

#### Port-forward to HAProxy

Use a port-forward to reach HAProxy locally:

```bash
kubectl port-forward -n haptic svc/haptic-haproxy 8080:80
```

#### Test the endpoint

In another terminal:

```bash
curl -H "Host: echo.example.local" http://localhost:8080/
```

The response includes the request headers and the serving pod's `HOSTNAME`.
Repeat the request to check that HAProxy distributes traffic across the echo pods.

## What's happening behind the scenes

The admission webhook validates the proposed Ingress before Kubernetes stores it. The controller then renders the templates and sends each HAProxy pod the changes it needs. The agent applies supported changes at runtime and reloads for structural changes. See the [Architecture Overview](./development/design/architecture-overview.md).

## Next steps

### Route with Ingress or Gateway API

Use the [Ingress reference](./libraries/ingress.md) for path matching, TLS, and
annotations. For Gateway API, follow [GatewayClass setup](./gateway-class.md),
then consult the [Gateway reference](./libraries/gateway.md) for route types and
listener options.

### Replacing another Ingress controller?

See [Migrating to HAPTIC](./migrating.md)
for the cutover procedure and compatibility checks.

### Customize the configuration

Put configuration changes under `controller.config` in your Helm values and [upgrade the release](./deploying-with-helm.md#upgrading). The [CRD Reference](./crd-reference.md) documents the fields.

### Watch additional resources

Add watches for ConfigMaps or your own CRDs — see [Watching Resources](./watching-resources.md).

### Extend with templates (advanced)

Use the [templating guide](./templating.md) to add a custom annotation, read your
own resource types, or emit an HAProxy directive the bundled libraries don't cover.

### Run in production

For 3+ replicas, PodDisruptionBudgets, and leader election, see [High Availability](./operations/high-availability.md). For Prometheus metrics and dashboards, see [Monitoring](./operations/monitoring.md).

## Troubleshooting

Check the symptom that matches your setup:

- **Controller not starting** -- check logs for missing HAProxyTemplateConfig, RBAC errors, or API connectivity issues
- **HAProxy pods not updating** -- verify the agent container is running and credentials match
- **Ingress not routing** -- ensure `ingressClassName: haptic` is set (or whatever you configured `ingressClass.name` to) and the backend Service has endpoints

For detailed diagnosis steps, see the [Troubleshooting Guide](./troubleshooting.md).

## Clean up

Remove the sample app if you deployed it:

```bash
kubectl delete ingress echo-ingress -n default
kubectl delete deployment echo -n default
kubectl delete service echo -n default
```

To remove the HAPTIC installation used in this guide:

```bash
helm uninstall haptic -n haptic
```

Delete the namespace only if it contains nothing else you need:

```bash
kubectl delete namespace haptic
```

Helm retains the CRDs. If no other HAPTIC installation uses them, you can remove
them and all remaining instances of those resources:

```bash
kubectl delete crd \
  haproxytemplateconfigs.haproxy-haptic.org \
  haproxytemplatelibraries.haproxy-haptic.org \
  haproxycfgs.haproxy-haptic.org \
  haproxygeneralfiles.haproxy-haptic.org \
  haproxycrtlistfiles.haproxy-haptic.org \
  haproxymapfiles.haproxy-haptic.org
```
