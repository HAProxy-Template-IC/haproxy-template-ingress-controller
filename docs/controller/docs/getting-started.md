# Getting Started

## Overview

This guide walks you through deploying HAPTIC (HAProxy Template Ingress Controller) and creating your first template-driven configuration. You'll learn how to:

- Install the controller and HAProxy using Helm
- Create a basic Ingress configuration
- Verify the deployment and test routing

The entire process takes approximately 15-20 minutes on a local Kubernetes cluster.

## Prerequisites

- Kubernetes cluster (1.19+) - kind, minikube, or cloud provider
- kubectl configured to access your cluster
- Helm 3.0+

## Step 1: Install with Helm

Install the controller and HAProxy using Helm:

```bash
# Add the Helm repository
helm repo add haptic https://haproxy-haptic.org/haproxy-template-ingress-controller/charts
helm repo update

# Install (deploys both controller and HAProxy pods)
helm install haptic haptic/haptic --namespace haptic --create-namespace
```

The Helm chart deploys:

- **Controller**: Watches Kubernetes resources and generates HAProxy configurations
- **HAProxy pods**: Load balancers with Dataplane API sidecars (2 replicas by default)
- **RBAC**: Permissions for watching Ingress, Service, and EndpointSlice resources
- **HAProxyTemplateConfig**: CRD resource with the default template configuration

Verify both components are running:

```bash
# Check controller
kubectl get pods -n haptic -l app.kubernetes.io/component=controller

# Check HAProxy pods
kubectl get pods -n haptic -l app.kubernetes.io/component=loadbalancer
```

You should see the controller pod and two HAProxy pods in `Running` state.

## Step 2: Deploy a Sample Application

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

## Step 3: Create an Ingress Resource

Create an Ingress resource that the controller will process:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: echo-ingress
  namespace: default
spec:
  ingressClassName: haproxy
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

## Step 4: Verify the Configuration

### Check Controller Logs

Watch the controller process the Ingress:

```bash
kubectl logs -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller --tail=50 -f
```

You should see log entries showing:

- Ingress resource detected
- Template rendering completed
- Configuration validation passed
- Deployment to HAProxy instances succeeded

### Inspect HAProxy Configuration

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

## Step 5: Test the Routing

### Port-Forward to HAProxy

```bash
kubectl port-forward -n haptic svc/haptic-haproxy 8080:80
```

### Test the Endpoint

In another terminal:

```bash
# Test with Host header
curl -H "Host: echo.example.local" http://localhost:8080/

# You should receive a response from the echo server showing:
# - Request headers
# - Host information
# - Environment variables
```

### Test Load Balancing

Make multiple requests to see load balancing across echo pods:

```bash
for i in {1..10}; do
  curl -s -H "Host: echo.example.local" http://localhost:8080/ | grep -i hostname
done
```

You should see responses from different echo pods.

## What's Happening Behind the Scenes

When you created the Ingress resource, the controller:

1. **Detected the change** via Kubernetes watch API
2. **Rendered templates** using the default HAProxyTemplateConfig with your Ingress data
3. **Validated the configuration** using HAProxy's native parser
4. **Compared with current state** to determine what changed
5. **Deployed updates** to all HAProxy pods via Dataplane API
6. **Used runtime API** where possible (server addresses) to avoid reloads

The entire process typically completes in under 1 second.

## Next Steps

Now that you have a working setup, explore these topics:

### Customize the Configuration

The default configuration is generated from the HAProxyTemplateConfig CRD created by Helm. To customize:

```bash
# View the current configuration
kubectl get haproxytemplateconfig -n haptic haptic-config -o yaml

# Edit the configuration
kubectl edit haproxytemplateconfig -n haptic haptic-config
```

See [CRD Reference](./crd-reference.md) for all available options.

### Template Customization

Learn how to write custom templates for advanced HAProxy features:

- **Path-based routing**: Route requests based on URL paths
- **SSL termination**: Configure TLS certificates and HTTPS listeners
- **Rate limiting**: Add rate limits using stick tables
- **Authentication**: Enable HTTP basic auth on specific paths
- **Custom error pages**: Serve custom error responses

See [Templating Guide](./templating.md) for template syntax and examples.

### Watched Resources

Extend the controller to watch additional Kubernetes resources:

- **EndpointSlices**: Use actual pod IPs instead of service DNS
- **Secrets**: Load TLS certificates dynamically
- **ConfigMaps**: Inject custom HAProxy configuration snippets
- **Custom CRDs**: Define your own resource types

See [Watching Resources](./watching-resources.md) for configuration details.

### High Availability

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
helm upgrade haptic haptic/haptic -n haptic \
  --reuse-values \
  --set monitoring.serviceMonitor.enabled=true \
  --set monitoring.serviceMonitor.interval=30s
```

See [Monitoring Guide](./operations/monitoring.md) for metrics and dashboards.

## Troubleshooting

### Controller Not Starting

Check the controller logs for errors:

```bash
kubectl logs -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller
```

Common issues:

- Missing HAProxyTemplateConfig or Secret
- Insufficient RBAC permissions
- Cannot connect to Kubernetes API

### HAProxy Pods Not Updating

Verify the controller can connect to HAProxy Dataplane API:

```bash
# Port-forward to Dataplane API
kubectl port-forward -n haptic $HAPROXY_POD 5555:5555

# Test the API (default credentials)
curl -u admin:adminpwd http://localhost:5555/v2/info
```

If this fails, check:

- Dataplane API sidecar is running
- Credentials match between controller and HAProxy
- Master socket exists at `/etc/haproxy/haproxy-master.sock`

### Ingress Not Routing

Check that:

1. The Ingress has `ingressClassName: haproxy`
2. The Ingress is in the same namespace as watched resources
3. The backend Service exists and has endpoints

```bash
# Check Ingress
kubectl get ingress echo-ingress -o yaml

# Check Service
kubectl get service echo

# Check Endpoints
kubectl get endpointslices -l kubernetes.io/service-name=echo
```

For more troubleshooting guidance, see [Troubleshooting Guide](./troubleshooting.md).

## Clean Up

Remove all resources created in this guide:

```bash
# Remove Ingress and echo application
kubectl delete ingress echo-ingress
kubectl delete deployment echo
kubectl delete service echo

# Uninstall HAPTIC (removes controller, HAProxy, and all related resources)
helm uninstall haptic -n haptic

# Remove namespace
kubectl delete namespace haptic

# Remove CRD (optional, removes all HAProxyTemplateConfig resources)
kubectl delete crd haproxytemplateconfigs.haproxy-haptic.org
```

## See Also

- [CRD Reference](./crd-reference.md) - Complete configuration options
- [Templating Guide](./templating.md) - Template syntax and filters
- [HAProxy Configuration](./supported-configuration.md) - Supported HAProxy features
- [Watching Resources](./watching-resources.md) - Resource watching configuration
- [Helm Chart Documentation](/helm-chart/latest/) - Chart values and options
