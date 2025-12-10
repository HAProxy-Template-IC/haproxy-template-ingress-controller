# Getting Started

## Overview

This guide walks you through deploying the HAProxy Template Ingress Controller and creating your first template-driven configuration. You'll learn how to:

- Deploy HAProxy pods with Dataplane API sidecars
- Install the controller using Helm
- Create a basic Ingress configuration
- Verify the deployment and test routing

The entire process takes approximately 30-45 minutes on a local Kubernetes cluster.

## Prerequisites

- Kubernetes cluster (1.19+) - kind, minikube, or cloud provider
- kubectl configured to access your cluster
- Helm 3.0+

## Step 1: Deploy HAProxy with Dataplane API

The controller requires HAProxy pods running with Dataplane API sidecars. These pods serve as the load balancers that the controller will configure.

### Understanding the Architecture

```
┌─────────────────────────────────┐
│      HAProxy Pod                │
├─────────────────────────────────┤
│  ┌──────────┐  ┌─────────────┐  │
│  │ HAProxy  │  │  Dataplane  │  │
│  │  Process │◄─┤  API Sidecar│  │
│  └──────────┘  └─────────────┘  │
│       ▲              ▲           │
│       │              │           │
│    haproxy.cfg    :5555 API     │
└───────┼──────────────┼───────────┘
        │              │
        └──────┬───────┘
               │
    ┌──────────▼──────────┐
    │  Template Controller│
    └─────────────────────┘
```

### Create HAProxy Deployment

Apply this manifest to create a basic HAProxy deployment:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: haproxy
  namespace: default
spec:
  replicas: 2
  selector:
    matchLabels:
      app: haproxy
      component: loadbalancer
  template:
    metadata:
      labels:
        app: haproxy
        component: loadbalancer
    spec:
      containers:
      # Main HAProxy container
      - name: haproxy
        image: haproxytech/haproxy-debian:3.2
        command: ["/bin/sh", "-c"]
        args:
          - |
            # Create required directories
            mkdir -p /etc/haproxy/maps /etc/haproxy/ssl /etc/haproxy/general

            # Create minimal bootstrap configuration
            cat > /etc/haproxy/haproxy.cfg <<EOF
            global
                log stdout format raw local0 info
                daemon

            defaults
                timeout connect 5s
                timeout client 30s
                timeout server 30s

            frontend bootstrap
                bind *:8404
                http-request return status 200 if { path /healthz }
            EOF

            # Start HAProxy in master-worker mode
            exec haproxy -W -db -S "/etc/haproxy/haproxy-master.sock,level,admin" -f /etc/haproxy/haproxy.cfg
        volumeMounts:
        - name: haproxy-config
          mountPath: /etc/haproxy
        ports:
        - containerPort: 80
          name: http
        - containerPort: 443
          name: https
        - containerPort: 8404
          name: stats
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8404
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /healthz
            port: 8404
          initialDelaySeconds: 5
          periodSeconds: 5

      # Dataplane API sidecar
      - name: dataplane
        image: haproxytech/haproxy-debian:3.2
        command: ["/bin/sh", "-c"]
        args:
          - |
            # Wait for HAProxy master socket
            while [ ! -S /etc/haproxy/haproxy-master.sock ]; do
              echo "Waiting for HAProxy master socket..."
              sleep 1
            done

            # Create Dataplane API configuration
            cat > /etc/haproxy/dataplaneapi.yaml <<'EOF'
            config_version: 2
            name: haproxy-dataplaneapi
            dataplaneapi:
              host: 0.0.0.0
              port: 5555
              user:
                - name: admin
                  password: adminpass
                  insecure: true
              transaction:
                transaction_dir: /var/lib/dataplaneapi/transactions
                backups_number: 10
                backups_dir: /var/lib/dataplaneapi/backups
              resources:
                maps_dir: /etc/haproxy/maps
                ssl_certs_dir: /etc/haproxy/ssl
                general_storage_dir: /etc/haproxy/general
            haproxy:
              config_file: /etc/haproxy/haproxy.cfg
              haproxy_bin: /usr/local/sbin/haproxy
              master_worker_mode: true
              master_runtime: /etc/haproxy/haproxy-master.sock
              reload:
                reload_delay: 1
                reload_cmd: /bin/sh -c "echo 'reload' | socat stdio unix-connect:/etc/haproxy/haproxy-master.sock"
                restart_cmd: /bin/sh -c "echo 'reload' | socat stdio unix-connect:/etc/haproxy/haproxy-master.sock"
                reload_strategy: custom
            log_targets:
              - log_to: stdout
                log_level: info
            EOF

            # Start Dataplane API
            exec dataplaneapi -f /etc/haproxy/dataplaneapi.yaml
        volumeMounts:
        - name: haproxy-config
          mountPath: /etc/haproxy
        ports:
        - containerPort: 5555
          name: dataplane-api

      volumes:
      - name: haproxy-config
        emptyDir: {}
```

Save this as `haproxy-deployment.yaml` and apply:

```bash
kubectl apply -f haproxy-deployment.yaml
```

Verify the pods are running:

```bash
kubectl get pods -l app.kubernetes.io/component=loadbalancer
```

You should see two pods with `2/2 RUNNING`.

## Step 2: Install the Controller

Install the controller using Helm with default configuration:

```bash
# Add the Helm repository (if published)
# helm repo add haproxy-template-ic https://haproxy-template-ic.gitlab.io/haproxy-template-ingress-controller
# helm repo update

# Install from local chart
helm install haproxy-ic ./charts/haproxy-template-ic \
  --set credentials.dataplane.username=admin \
  --set credentials.dataplane.password=adminpass
```

The default Helm installation:

- Runs 2 replicas with leader election enabled for high availability
- Creates a HAProxyTemplateConfig CRD resource with basic Ingress watching
- Sets up RBAC permissions for watching Ingress, Service, and EndpointSlice resources
- Configures the controller to find HAProxy pods using labels `app=haproxy, component=loadbalancer`

Verify the controller is running:

```bash
kubectl get pods -l app.kubernetes.io/name=haproxy-template-ic,app.kubernetes.io/component=controller
kubectl logs -l app.kubernetes.io/name=haproxy-template-ic,app.kubernetes.io/component=controller --tail=20
```

You should see logs indicating the controller has started and is watching resources.

## Step 3: Deploy a Sample Application

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

## Step 4: Create an Ingress Resource

Create an Ingress resource that the controller will process:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: echo-ingress
  namespace: default
spec:
  ingressClassName: haproxy-template-ic
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

## Step 5: Verify the Configuration

### Check Controller Logs

Watch the controller process the Ingress:

```bash
kubectl logs -l app.kubernetes.io/name=haproxy-template-ic,app.kubernetes.io/component=controller --tail=50 -f
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
HAPROXY_POD=$(kubectl get pods -l app.kubernetes.io/component=loadbalancer -o jsonpath='{.items[0].metadata.name}')

# View the generated configuration
kubectl exec $HAPROXY_POD -c haproxy -- cat /etc/haproxy/haproxy.cfg
```

You should see:

- A frontend section with routing rules
- A backend section referencing the echo service
- Server entries pointing to the echo pod endpoints

## Step 6: Test the Routing

### Port-Forward to HAProxy

```bash
kubectl port-forward -n default deployment/haproxy 8080:80
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
kubectl get haproxytemplateconfig haproxy-template-ic-config -o yaml

# Edit the configuration
kubectl edit haproxytemplateconfig haproxy-template-ic-config
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
helm upgrade haproxy-ic ./charts/haproxy-template-ic \
  --reuse-values \
  --set monitoring.serviceMonitor.enabled=true \
  --set monitoring.serviceMonitor.interval=30s
```

See [Monitoring Guide](./operations/monitoring.md) for metrics and dashboards.

## Troubleshooting

### Controller Not Starting

Check the controller logs for errors:

```bash
kubectl logs -l app.kubernetes.io/name=haproxy-template-ic,app.kubernetes.io/component=controller
```

Common issues:

- Missing HAProxyTemplateConfig or Secret
- Insufficient RBAC permissions
- Cannot connect to Kubernetes API

### HAProxy Pods Not Updating

Verify the controller can connect to HAProxy Dataplane API:

```bash
# Port-forward to Dataplane API
kubectl port-forward $HAPROXY_POD 5555:5555

# Test the API
curl -u admin:adminpass http://localhost:5555/v2/info
```

If this fails, check:

- Dataplane API sidecar is running
- Credentials match between controller and HAProxy
- Master socket exists at `/etc/haproxy/haproxy-master.sock`

### Ingress Not Routing

Check that:

1. The Ingress has `ingressClassName: haproxy-template-ic`
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
# Remove Ingress
kubectl delete -f echo-ingress.yaml

# Remove echo application
kubectl delete -f echo-app.yaml

# Uninstall controller
helm uninstall haproxy-ic

# Remove HAProxy deployment
kubectl delete -f haproxy-deployment.yaml

# Remove CRD (optional, removes all configs)
kubectl delete crd haproxytemplateconfigs.haproxy-template-ic.gitlab.io
```

## See Also

- [CRD Reference](./crd-reference.md) - Complete configuration options
- [Templating Guide](./templating.md) - Template syntax and filters
- [HAProxy Configuration](./supported-configuration.md) - Supported HAProxy features
- [Watching Resources](./watching-resources.md) - Resource watching configuration
- [Helm Chart Documentation](/helm-chart/latest/) - Chart values and options
