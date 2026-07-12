---
description: "Get started with HAPTIC, the template-driven HAProxy ingress controller for Kubernetes. Install with Helm, deploy HAProxy, and verify your setup."
hide:
  - navigation
---

# Getting started

## Overview

This guide walks you through deploying HAPTIC and creating your first template-driven configuration. You'll learn how to:

- Install the controller and HAProxy using Helm
- Create a basic Ingress configuration
- Verify the deployment and test routing

The entire process takes approximately 15-20 minutes on a local Kubernetes cluster.

Want a taste first? This is a complete, minimal HAPTIC config rendering an Ingress
into an HAProxy config — in your browser, no install. Click **Run live**, then edit
the template or the Ingress and watch the output change.

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-focus="14-15" data-controls="tabs,resources" data-title="An Ingress becomes an HAProxy config" data-height="480">

```yaml
# One HAProxy backend per Ingress, routed by host. Typed field access —
# the schema is bundled, so no dig() needed. Edit this, or the Ingress, and watch the output.
haproxyConfig:
  template: |
    global
      log stdout format raw local0

    defaults
      mode http
      timeout connect 5s
      timeout client 30s
      timeout server 30s

    frontend http
      bind :80
      use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]
      default_backend unmatched
    {%- for _, ing := range resources.ingresses.List() %}
    {%- for _, rule := range ing.spec.rules %}
    {%- for _, path := range rule.http.paths %}
    backend {{ ing.metadata.name }}
      server app {{ path.backend.service.name }}:{{ path.backend.service.port.number | fallback(80) }}
    {%- end %}
    {%- end %}
    {%- end %}

    backend unmatched
      http-request deny deny_status 404

watchedResources:
  ingresses:
    apiVersion: networking.k8s.io/v1
    resources: ingresses
    indexBy:
      - metadata.name

maps:
  host.map:
    template: |
      {%- for _, ing := range resources.ingresses.List() %}
      {%- for _, rule := range ing.spec.rules %}
      {{ rule.host }} {{ ing.metadata.name }}
      {%- end %}
      {%- end %}
```

```yaml
# The Ingress the config renders. Add another, or change the host or service.
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: shop
spec:
  rules:
    - host: shop.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: shop
                port:
                  number: 8080
```

</div>

The playground accepts this bare `spec` content directly; on a cluster the same
blocks nest under `spec` of the `HAProxyTemplateConfig` custom resource:

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: haptic-config
  namespace: haptic
spec:
  credentialsSecretRef:        # Dataplane API credentials Secret
    name: haptic-credentials
  podSelector:                 # which HAProxy pods receive the config
    matchLabels:
      app.kubernetes.io/component: loadbalancer
  haproxyConfig:
    template: |
      # ... as above ...
  watchedResources:
    # ... as above ...
  maps:
    # ... as above ...
```

The Helm chart installs a complete resource of this shape for you (Step 1); see
the [CRD Reference](./crd-reference.md) for every field.

## Prerequisites

- Kubernetes cluster (1.21+) - kind, minikube, or cloud provider
- kubectl configured to access your cluster
- Helm 3.0+

!!! note "Webhook validation"
    A validating admission webhook is **enabled by default and works out of the box** — it rejects Ingress, HTTPRoute, and GRPCRoute changes that would break template rendering, using a self-signed certificate the chart issues itself (no cert-manager required). For rotation and certificate alternatives, see [Webhook certificates](./ssl-certificates.md#webhook-certificates).

## Step 1: Install with Helm

Install the controller and HAProxy using Helm:

```bash
# Install from OCI registry (deploys both controller and HAProxy pods)
helm install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.1 \
  --namespace haptic --create-namespace
```

The Helm chart deploys:

- **Controller**: Watches Kubernetes resources and generates HAProxy configurations
- **HAProxy pods**: Load balancers with Dataplane API sidecars (2 replicas by default)
- **RBAC**: Permissions for watching Ingress, Service, and EndpointSlice resources
- **HAProxyTemplateConfig**: CRD resource with the default template configuration, including [template libraries](template-libraries.md) for Ingress and Gateway API out of the box

The chart provisions a default HTTPS certificate out of the box — a self-signed one, or a cert-manager-issued, auto-rotated one when cert-manager is present. For production domains, GitOps caveats, and alternatives, see [SSL Certificates](./ssl-certificates.md).

Verify both components are running:

```bash
# Check controller
kubectl get pods -n haptic -l app.kubernetes.io/component=controller

# Check HAProxy pods
kubectl get pods -n haptic -l app.kubernetes.io/component=loadbalancer
```

You should see two controller pods (the chart defaults to two replicas with leader election) and two HAProxy pods, all in `Running` state with full readiness (`2/2` and `3/3`).

!!! note "HAProxy version"
    The chart defaults to HAProxy 3.4, the latest Long-Term Support (LTS) release. To pin a different series, set `--set haproxyVersion=3.0`. See [HAProxy Versions](./operations/haproxy-versions.md) for the full list and support status.

## Step 2: Deploy a sample application

Create a simple echo service to test routing:

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

## Step 3: Create an Ingress resource

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

The controller automatically detects this new Ingress, renders the HAProxy configuration, validates it, and deploys it to the HAProxy pods. See [What's Happening Behind the Scenes](#whats-happening-behind-the-scenes) for details.

!!! tip "TLS for a host"
    This Ingress is served over HTTP only. The chart binds its HTTPS listener once at least one resource requests TLS — an Ingress with `spec.tls`, a Gateway HTTPS listener, or SSL passthrough. To terminate HTTPS for this host, add a `spec.tls` entry backed by a `kubernetes.io/tls` Secret — see [Ingress library — TLS configuration](./libraries/ingress.md#tls-configuration). Once the HTTPS listener is up, any host it serves that has no certificate of its own falls back to the chart's [default certificate](./ssl-certificates.md) (a self-signed cert out of the box).

## Step 4: Verify the configuration

### Check controller logs

Watch the controller process the Ingress:

```bash
kubectl logs -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller --tail=50 -f
```

At the default `info` log level, each change produces a single consolidated `Reconciliation` summary line from the leader replica, for example:

```text
level=INFO msg=Reconciliation trigger=resource_change instances=2/2 reloads=2 ops=30 render_ms=1 validate_ms=1 deploy_ms=184 total_ms=289 backend_create=2 server_create=20 server_update=8 map_update=6
```

It reports the trigger, how many HAProxy instances were updated (`instances`), the reloads and runtime operations applied (with a per-operation breakdown such as `backend_create` / `server_create`), and per-phase timings. For the individual stages — the resource change, template render, validation, and per-instance deploy — raise the controller to the `debug` level (see [Enable debug logging](./troubleshooting.md#enable-debug-logging)).

### Inspect the rendered HAProxy configuration

The controller writes the rendered HAProxy config to a read-only `HAProxyCfg` resource on every reconciliation, so you can inspect exactly what it deployed straight from the Kubernetes API — no pod access needed:

```bash
kubectl describe haproxycfg -n haptic
```

You should see:

- A frontend section with routing rules
- A backend section referencing the echo service
- Server entries pointing to the echo pod endpoints

!!! note "Output vs input"
    `HAProxyCfg` (singular `haproxycfg`, short name `hpcfg`) is the controller's *output* — it republishes it from the templates whenever the rendered configuration changes, so editing it directly has no lasting effect and isn't advised: the next config change overwrites your edit. To change the configuration, edit the *input* instead — the templates, watched resources, and dataplane settings in `HAProxyTemplateConfig` (short names `htplcfg`, `haptpl`). Use `kubectl describe` rather than `kubectl get -o yaml`, since the latter renders multiline configs as literal `\n`.

## Step 5: Test the routing

### Port-forward to HAProxy

HAProxy is running inside the cluster and isn't directly reachable from your machine. Port-forward creates a temporary tunnel from your local port to the HAProxy service:

```bash
kubectl port-forward -n haptic svc/haptic-haproxy 8080:80
```

### Test the endpoint

In another terminal:

```bash
# Test with Host header
curl -H "Host: echo.example.local" http://localhost:8080/

# You should receive a response from the echo server showing:
# - Request headers
# - Host information
# - Environment variables
```

### Test load balancing

Make multiple requests to see load balancing across echo pods:

```bash
for i in {1..10}; do
  curl -s -H "Host: echo.example.local" http://localhost:8080/ | grep -o '"HOSTNAME":"[^"]*"'
done
```

You should see responses from different echo pods.

## What's happening behind the scenes

When you created the Ingress, the controller detected the change via the Kubernetes watch API and rendered the templates from the default HAProxyTemplateConfig with your Ingress data. The rendered config then passed validation (syntax parse and schema check) before anything reached HAProxy. Finally, the controller deployed the change to all HAProxy pods in parallel via the Dataplane API — using HAProxy's runtime API where possible to avoid process reloads — typically completing the whole cycle in under 1 second. For the full pipeline, see the [Architecture Overview](./development/design/architecture-overview.md).

## Next steps

Now that you have a working setup, explore these topics:

### Learn templating

The [Templating Guide](./templating.md) is the natural next step: it covers the template language, the resource context your templates see, and how to add custom behavior. The default [template libraries](template-libraries.md) already handle path-based routing, SSL termination, and annotation-driven configuration — you only write templates to go beyond them ([custom annotations](./templating.md#reading-a-custom-annotation), domain-specific logic, HAProxy features they don't cover).

### Replacing another Ingress controller?

See [Migrating to HAPTIC](./migrating.md)
for the zero-downtime, one-Ingress-at-a-time cutover — and the three settings
that silently break a migration if you miss them.

### Customize the configuration

The running configuration is the HAProxyTemplateConfig resource Helm created — `kubectl edit haproxytemplateconfig -n haptic haptic-config` — and the [CRD Reference](./crd-reference.md) documents every field.

### Watch additional resources

Extend the controller to watch EndpointSlices, Secrets, ConfigMaps, or your own CRDs — see [Watching Resources](./watching-resources.md).

### Run in production

For 3+ replicas, PodDisruptionBudgets, and leader election, see [High Availability](./operations/high-availability.md). For Prometheus metrics and dashboards, see [Monitoring](./operations/monitoring.md).

## Troubleshooting

If you run into issues during setup, check these common areas:

- **Controller not starting** -- check logs for missing HAProxyTemplateConfig, RBAC errors, or API connectivity issues
- **HAProxy pods not updating** -- verify the Dataplane API sidecar is running and credentials match
- **Ingress not routing** -- ensure `ingressClassName: haptic` is set (or whatever you configured `ingressClass.name` to) and the backend Service has endpoints

For detailed diagnosis steps, see the [Troubleshooting Guide](./troubleshooting.md).

## Clean up

Remove all resources created in this guide:

```bash
# Remove Ingress and echo application
kubectl delete ingress echo-ingress -n default
kubectl delete deployment echo -n default
kubectl delete service echo -n default

# Uninstall HAPTIC (removes controller, HAProxy, and all related resources)
helm uninstall haptic -n haptic

# Remove namespace
kubectl delete namespace haptic

# Remove CRDs (optional). The chart installs five — keep them in place if you plan
# to reinstall, otherwise delete all five so the API group disappears cleanly.
kubectl delete crd \
  haproxytemplateconfigs.haproxy-haptic.org \
  haproxycfgs.haproxy-haptic.org \
  haproxygeneralfiles.haproxy-haptic.org \
  haproxycrtlistfiles.haproxy-haptic.org \
  haproxymapfiles.haproxy-haptic.org
```
