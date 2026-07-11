---
description: "Get started with HAPTIC, the template-driven HAProxy ingress controller for Kubernetes. Install with Helm, deploy HAProxy, and verify your setup."
hide:
  - navigation
---

# Getting Started

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
blocks live inside the `HAProxyTemplateConfig` custom resource — Step 3 shows the
full wrapped shape.

## Prerequisites

- Kubernetes cluster (1.21+) - kind, minikube, or cloud provider
- kubectl configured to access your cluster
- Helm 3.0+

!!! note "Webhook validation"
    The validating admission webhook is **enabled by default and works out of the box** — the chart generates a self-signed TLS certificate for it, with no cert-manager required. It intercepts CREATE/UPDATE on Ingresses, HTTPRoutes, and GRPCRoutes (the kinds the chart libraries opt in via `enableValidationWebhook: true`) and rejects changes that would break template rendering. The self-signed cert is long-lived and **not auto-rotated**; for automatic rotation set `webhook.certManager.enabled=true` (requires [cert-manager](https://cert-manager.io/docs/installation/)) — see [Security](./operations/security.md) for details.

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

Verify both components are running:

```bash
# Check controller
kubectl get pods -n haptic -l app.kubernetes.io/component=controller

# Check HAProxy pods
kubectl get pods -n haptic -l app.kubernetes.io/component=loadbalancer
```

You should see the controller pod and two HAProxy pods in `Running` state.

!!! note "HAProxy version"
    The chart defaults to HAProxy 3.4. To select a different version (e.g. 3.0 LTS or 3.3), set `--set haproxyVersion=3.0`. See [HAProxy Versions](./operations/haproxy-versions.md) for details.

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

Create an Ingress resource that the controller processes:

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

## Step 4: Verify the configuration

### Check controller logs

Watch the controller process the Ingress:

```bash
kubectl logs -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller --tail=50 -f
```

You should see log entries showing:

- Ingress resource detected
- Template rendering completed
- Configuration validation passed
- Deployment to HAProxy instances succeeded

### Inspect HAProxy configuration

Verify the generated HAProxy configuration was deployed:

```bash
# Get one of the HAProxy pods
HAPROXY_POD=$(kubectl get pods -n haptic -l app.kubernetes.io/component=loadbalancer -o jsonpath='{.items[0].metadata.name}')

# View the generated configuration
kubectl exec -n haptic $HAPROXY_POD -c haproxy -- cat /etc/haproxy/haproxy.cfg
```

You should see:

- A frontend section with routing rules
- A backend section referencing the echo service
- Server entries pointing to the echo pod endpoints

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

When you created the Ingress resource, the controller:

1. **Detected the change** via the Kubernetes watch API and updated its in-memory store
2. **Triggered a reconciliation** through a leading-edge debouncer (so a single change fires immediately)
3. **Rendered templates** using the default HAProxyTemplateConfig with your Ingress data
4. **Validated the rendered config**: client-native syntax parse → OpenAPI schema check. (The heavier `haproxy -c` semantic check already ran in the admission webhook when the Ingress was accepted, and the Dataplane API re-validates on push.) Both must pass before the change reaches HAProxy.
5. **Compared the validated config** with each pod's live config to classify the change (runtime-eligible server-field updates vs structural changes)
6. **Deployed the change** to all HAProxy pods in parallel via the Dataplane API, pushing the full rendered config in a single request per pod
7. **Used the runtime API** where possible (server address/weight changes, map updates, etc.) to avoid HAProxy process reloads

The entire process typically completes in under 1 second.

## Next steps

Now that you have a working setup, explore these topics:

### Migrating from another ingress controller

Replacing ingress-nginx or haproxy-ingress? See [Migrating to HAPTIC](./migrating.md)
for the zero-downtime, one-Ingress-at-a-time cutover — and the three settings
that silently break a migration if you miss them.

### Customize the configuration

The default configuration is generated from the HAProxyTemplateConfig CRD created by Helm. To customize:

```bash
# View the current configuration
kubectl get haproxytemplateconfig -n haptic haptic-config -o yaml

# Edit the configuration
kubectl edit haproxytemplateconfig -n haptic haptic-config
```

See [CRD Reference](./crd-reference.md) for all available options.

### Template customization

The default [template libraries](template-libraries.md) already cover many common use cases: path-based routing, SSL termination, and annotation-driven configuration. You do not need to write or modify templates to use these features.

When you need to go beyond the default libraries — custom annotations, domain-specific logic, or HAProxy features not covered — see the [Templating Guide](./templating.md).

### Watched resources

Extend the controller to watch additional Kubernetes resources:

- **EndpointSlices**: Use actual pod IPs instead of service DNS
- **Secrets**: Load TLS certificates dynamically
- **ConfigMaps**: Inject custom HAProxy configuration snippets
- **Custom CRDs**: Define your own resource types

See [Watching Resources](./watching-resources.md) for configuration details.

### High availability

Configure the controller for production deployments:

- Scale to 3+ replicas across availability zones
- Configure PodDisruptionBudgets
- Set up monitoring and alerting
- Enable leader election (already enabled by default)

See [High Availability](./operations/high-availability.md) for HA configuration.

### Monitoring

Set up Prometheus monitoring for the controller:

```bash
# Enable ServiceMonitor if using Prometheus Operator
helm upgrade haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.1 --reuse-values -n haptic \
  --set monitoring.serviceMonitor.enabled=true \
  --set monitoring.serviceMonitor.interval=30s
```

See [Monitoring Guide](./operations/monitoring.md) for metrics and dashboards.

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
