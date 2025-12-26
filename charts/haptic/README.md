# HAPTIC Helm Chart

This Helm chart deploys HAPTIC (HAProxy Template Ingress Controller), which manages HAProxy configurations dynamically based on Kubernetes resources.

## Overview

HAPTIC:

- Watches Kubernetes Ingress and/or Gateway API resources
- Renders Scriggo templates to generate [HAProxy](https://www.haproxy.org/) configurations
- Deploys configurations to HAProxy pods via [Dataplane API](https://github.com/haproxytech/dataplaneapi)
- Supports cross-namespace HAProxy pod management
- Template library system for modular feature support
- Conditional resource watching based on enabled features

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- **HAProxy 3.0 or newer** (the chart deploys HAProxy by default; template libraries require 3.0+ for SSL/TLS features)

## Installation

### Basic Installation

```bash
helm install my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic --version 0.1.0-alpha.1
```

### With Custom Values

```bash
helm install my-controller haptic/haptic \
  --set image.tag=v0.1.0 \
  --set replicaCount=2
```

### With Custom Values File

```bash
helm install my-controller haptic/haptic \
  -f my-values.yaml
```

## Configuration

### Key Configuration Options

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of controller replicas (2+ recommended for HA) | `2` |
| `image.repository` | Controller image repository | `registry.gitlab.com/haproxy-haptic/haptic` |
| `image.tag` | Controller image tag | Chart appVersion |
| `controller.templateLibraries.ingress.enabled` | Enable Ingress resource support | `true` |
| `controller.templateLibraries.gateway.enabled` | Enable Gateway API support (HTTPRoute, GRPCRoute) | `true` |
| `ingressClass.enabled` | Create IngressClass resource | `true` |
| `ingressClass.name` | IngressClass name | `haproxy` |
| `gatewayClass.enabled` | Create GatewayClass resource | `true` |
| `gatewayClass.name` | GatewayClass name | `haproxy` |
| `controller.debugPort` | Introspection HTTP server port (provides /healthz and /debug/*) | `8080` |
| `controller.config.podSelector` | Labels to match HAProxy pods | `{app.kubernetes.io/component: loadbalancer}` |
| `controller.logLevel` | Initial log level (LOG_LEVEL env var) | `INFO` |
| `controller.config.logging.level` | Log level from ConfigMap (overrides env var if set) | `""` |
| `credentials.dataplane.username` | Dataplane API username | `admin` |
| `credentials.dataplane.password` | Dataplane API password | `adminpass` |
| `networkPolicy.enabled` | Enable NetworkPolicy | `true` |

### Controller Configuration

The controller configuration is defined in `controller.config` and includes:

- **podSelector**: Labels to identify HAProxy pods to manage
- **watchedResources**: Kubernetes resources to watch (Ingress, Service, EndpointSlice, Secret)
- **templateSnippets**: Reusable template fragments
- **maps**: HAProxy map file templates
- **files**: Auxiliary files (error pages, etc.)
- **haproxyConfig**: Main HAProxy configuration template

Example custom configuration:

```yaml
controller:
  config:
    podSelector:
      matchLabels:
        app: my-haproxy
        environment: production

    watchedResources:
      ingresses:
        apiVersion: networking.k8s.io/v1
        resources: ingresses
        indexBy: ["metadata.namespace", "metadata.name"]
```

### Ingress Class Filtering

By default, the controller only watches Ingress resources with `spec.ingressClassName: haproxy`. This ensures the controller only processes ingresses intended for it.

**Default behavior:**

```yaml
controller:
  config:
    watchedResources:
      ingresses:
        fieldSelector: "spec.ingressClassName=haproxy"
```

**To change the ingress class name:**

```yaml
controller:
  config:
    watchedResources:
      ingresses:
        fieldSelector: "spec.ingressClassName=my-custom-class"
```

**To watch all ingresses regardless of class:**

```yaml
controller:
  config:
    watchedResources:
      ingresses:
        fieldSelector: ""
```

The field selector uses Kubernetes server-side filtering for efficient resource watching. Only ingresses matching the specified `spec.ingressClassName` will be processed by the controller.

## Template Libraries

The controller uses a modular template library system where configuration files are merged at Helm render time. Each library provides specific HAProxy functionality and can be enabled or disabled independently.

| Library | Default | Description |
|---------|---------|-------------|
| Base | Always enabled | Core HAProxy configuration, extension points |
| SSL | Enabled | TLS certificates, HTTPS frontend |
| Ingress | Enabled | Kubernetes Ingress support |
| Gateway | Enabled | Gateway API (HTTPRoute, GRPCRoute) |
| HAProxy Annotations | Enabled | `haproxy.org/*` annotation support |
| HAProxy Ingress | Enabled | HAProxy Ingress Controller compatibility |
| Path Regex Last | Disabled | Performance-first path matching |

### Enabling/Disabling Libraries

```yaml
controller:
  templateLibraries:
    gateway:
      enabled: true   # Enable Gateway API support
    ingress:
      enabled: false  # Disable Ingress support
```

For comprehensive documentation including extension points and custom configuration injection, see [Template Libraries](./docs/template-libraries.md).

For Gateway API features, see [Gateway API Library](./docs/libraries/gateway.md).

## IngressClass

The chart automatically creates an IngressClass resource when the ingress library is enabled and Kubernetes 1.18+ is detected.

### Configuration

```yaml
ingressClass:
  enabled: true       # Create IngressClass (default: true)
  name: haproxy       # IngressClass name
  default: false      # Mark as cluster default
  controllerName: haproxy-haptic.org/controller
```

### Capability Detection

The chart uses `Capabilities.APIVersions.Has` to check for `networking.k8s.io/v1/IngressClass`. If the API is not available (Kubernetes < 1.18), the resource is silently skipped without error.

### Creation Conditions

IngressClass is created only when ALL of the following are true:

1. `ingressClass.enabled: true` (default)
2. `controller.templateLibraries.ingress.enabled: true` (default)
3. `networking.k8s.io/v1/IngressClass` API exists in cluster

### Multi-Controller Environments

When running multiple ingress controllers:

**Ensure unique identification:**

```yaml
# Controller 1 (haptic)
ingressClass:
  name: haproxy
  controllerName: haproxy-haptic.org/controller

# Controller 2 (nginx)
ingressClass:
  name: nginx
  controllerName: k8s.io/ingress-nginx
```

**Only one should be default:**

```yaml
# Set default on one controller only
ingressClass:
  default: true  # Only on ONE controller
```

### Disabling IngressClass Creation

If you manage IngressClass resources separately or use an external tool:

```yaml
ingressClass:
  enabled: false
```

### Using IngressClass

Ingress resources reference the IngressClass via `spec.ingressClassName`:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: example
spec:
  ingressClassName: haproxy  # References IngressClass.metadata.name
  rules:
    - host: example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: example-service
                port:
                  number: 80
```

## GatewayClass

The chart automatically creates a GatewayClass resource when the gateway library is enabled and Gateway API CRDs are installed.

### Prerequisites

Install Gateway API CRDs (standard channel) before enabling the gateway library:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/standard-install.yaml
```

Check [Gateway API releases](https://github.com/kubernetes-sigs/gateway-api/releases) for newer versions.

### Configuration

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

### Capability Detection

The chart checks for `gateway.networking.k8s.io/v1/GatewayClass` before creating the resource. If Gateway API CRDs are not installed, the resource is silently skipped without error.

### Creation Conditions

GatewayClass is created only when ALL of the following are true:

1. `gatewayClass.enabled: true` (default)
2. `controller.templateLibraries.gateway.enabled: true` (must be explicitly enabled)
3. `gateway.networking.k8s.io/v1/GatewayClass` API exists in cluster

### parametersRef - Controller Configuration Link

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

### Multi-Controller Environments

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

### Advanced: Multiple GatewayClasses

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

### Using GatewayClass

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

### Disabling GatewayClass Creation

If you manage GatewayClass resources separately:

```yaml
gatewayClass:
  enabled: false
```

## Resource Limits and Container Awareness

The controller automatically detects and respects container resource limits:

### CPU Limits (GOMAXPROCS)

**Go 1.25+ Native Support**: The controller uses Go 1.25, which includes built-in container-aware GOMAXPROCS. The Go runtime automatically:

- Detects cgroup CPU limits (v1 and v2)
- Sets GOMAXPROCS to match the container's CPU limit (not the host's core count)
- Dynamically adjusts if CPU limits change at runtime

No configuration needed - this works automatically when you set CPU limits in the deployment.

### Memory Limits (GOMEMLIMIT)

**automemlimit Library**: The controller uses the `automemlimit` library to automatically set GOMEMLIMIT based on cgroup memory limits. By default:

- Sets GOMEMLIMIT to 90% of the container memory limit
- Leaves 10% headroom for non-heap memory sources
- Works with both cgroups v1 and v2

### Configuration

Set resource limits in your values file:

```yaml
resources:
  limits:
    cpu: 500m
    memory: 512Mi
  requests:
    cpu: 100m
    memory: 128Mi
```

The controller will automatically log the detected limits at startup:

```
INFO HAPTIC starting ... gomaxprocs=1 gomemlimit="461373644 bytes (440.00 MiB)"
```

### Fine-Tuning Memory Limits

The `AUTOMEMLIMIT` environment variable can adjust the memory limit ratio (default: 0.9):

```yaml
# In deployment.yaml or via Helm values
env:
  - name: AUTOMEMLIMIT
    value: "0.8"  # Set GOMEMLIMIT to 80% of container limit
```

Valid range: 0.0 < AUTOMEMLIMIT ≤ 1.0

### Why This Matters

- **Prevents OOM kills**: GOMEMLIMIT helps the Go GC keep heap memory under control
- **Reduces CPU throttling**: Proper GOMAXPROCS prevents over-scheduling goroutines
- **Improves performance**: Better GC tuning and reduced context switching

## NetworkPolicy Configuration

The controller requires network access to:

1. Kubernetes API Server (watch resources)
2. HAProxy Dataplane API pods in ANY namespace
3. DNS (CoreDNS/kube-dns)

### Default Configuration

By default, the NetworkPolicy allows:

- DNS: kube-system namespace
- Kubernetes API: 0.0.0.0/0 (adjust for production)
- HAProxy pods: All namespaces with matching labels

### Production Hardening

For production, restrict Kubernetes API access:

```yaml
networkPolicy:
  egress:
    kubernetesApi:
      - cidr: 10.96.0.0/12  # Your cluster's service CIDR
        ports:
          - port: 443
            protocol: TCP
```

### kind Cluster Specifics

For kind clusters with network policy enforcement:

```yaml
networkPolicy:
  enabled: true
  egress:
    allowDNS: true
    kubernetesApi:
      - cidr: 0.0.0.0/0  # kind requires broader access
```

## Service Architecture

The chart deploys two separate Kubernetes Services:

### Controller Service

Exposes the controller's operational endpoints:

- **healthz** (8080): Liveness and readiness probes
- **metrics** (9090): Prometheus metrics endpoint

This service is for cluster-internal monitoring only. Default configuration:

```yaml
service:
  type: ClusterIP
  healthzPort: 8080
  metricsPort: 9090
```

### HAProxy Service

Exposes the HAProxy load balancer for ingress traffic:

- **http** (80): HTTP traffic routing
- **https** (443): HTTPS/TLS traffic routing
- **stats** (8404): Health and statistics page

This service routes external traffic to HAProxy pods. You can configure it based on your deployment environment:

**Development (kind cluster)**:

```yaml
haproxy:
  enabled: true
  service:
    type: LoadBalancer  # kind maps to localhost
```

**Production (cloud provider)**:

```yaml
haproxy:
  enabled: true
  service:
    type: LoadBalancer
    annotations:
      service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
```

**Production (NodePort for external LB)**:

```yaml
haproxy:
  enabled: true
  service:
    type: NodePort
    http:
      nodePort: 30080
    https:
      nodePort: 30443
```

**Production (managed externally)**:

```yaml
haproxy:
  enabled: false  # Manage HAProxy deployment separately
```

### Configuration Options

The HAProxy Service configuration:

```yaml
haproxy:
  enabled: true
  service:
    type: NodePort  # ClusterIP, NodePort, or LoadBalancer
    annotations: {}  # Cloud provider annotations
    http:
      port: 80
      nodePort: 30080  # Only for NodePort/LoadBalancer
    https:
      port: 443
      nodePort: 30443  # Only for NodePort/LoadBalancer
    stats:
      port: 8404
      nodePort: 30404  # Only for NodePort/LoadBalancer
```

### Why Separate Services?

Separating the controller and HAProxy services provides:

- **Clear separation of concerns**: Operational metrics vs data plane traffic
- **Independent scaling**: Controller runs as single replica, HAProxy scales independently
- **Security**: Controller endpoints remain internal, only HAProxy exposed externally
- **Flexibility**: Different service types for different purposes (ClusterIP for controller, LoadBalancer for HAProxy)

## HAProxy Pod Requirements

The controller manages HAProxy pods deployed separately. Each HAProxy pod must:

1. **Have matching labels** as defined in `podSelector`
2. **Run HAProxy with Dataplane API sidecar**
3. **Share config volume** between HAProxy and Dataplane containers
4. **Expose Dataplane API** on port 5555

### Example HAProxy Pod Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: haproxy
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
      - name: haproxy
        image: haproxytech/haproxy-debian:3.2
        command: ["/bin/sh", "-c"]
        args:
          - |
            mkdir -p /etc/haproxy/maps /etc/haproxy/certs
            cat > /etc/haproxy/haproxy.cfg <<EOF
            global
                log stdout len 4096 local0 info
            defaults
                timeout connect 5s
            frontend status
                bind *:8404
                http-request return status 200 if { path /healthz }
                # Note: /ready endpoint intentionally omitted - added by controller
            EOF
            exec haproxy -W -db -S "/etc/haproxy/haproxy-master.sock,level,admin" -- /etc/haproxy/haproxy.cfg
        volumeMounts:
        - name: haproxy-config
          mountPath: /etc/haproxy
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8404
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /ready
            port: 8404
          initialDelaySeconds: 5
          periodSeconds: 5

      - name: dataplane
        image: haproxytech/haproxy-debian:3.2
        command: ["/bin/sh", "-c"]
        args:
          - |
            # Wait for HAProxy to create the socket
            while [ ! -S /etc/haproxy/haproxy-master.sock ]; do
              echo "Waiting for HAProxy master socket..."
              sleep 1
            done

            # Create Dataplane API config
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
                ssl_certs_dir: /etc/haproxy/certs
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

      volumes:
      - name: haproxy-config
        emptyDir: {}
```

## SSL Certificate Configuration

The controller requires a default SSL certificate for HTTPS traffic.

### Default Behavior (Development/Testing)

The chart works out of the box with cert-manager installed. By default, it creates:

- A self-signed `Issuer` named `<release>-ssl-selfsigned`
- A `Certificate` for `localdev.me` and `*.localdev.me`

The `localdev.me` domain resolves to `127.0.0.1`, making it useful for local development. No additional configuration is required beyond having cert-manager installed:

```bash
# Install cert-manager (if not already installed)
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.16.0/cert-manager.yaml

# Install the chart - SSL works out of the box
helm install my-release haptic/haptic
```

**Note:** The default self-signed certificate is intended for development and testing only. For production, override with your own domain and issuer.

### Production Deployment

For production, override the default certificate configuration with your actual domain and a trusted issuer:

```yaml
controller:
  defaultSSLCertificate:
    certManager:
      createIssuer: false  # Use your own issuer
      dnsNames:
        - "*.example.com"
        - "example.com"
      issuerRef:
        name: letsencrypt-prod
        kind: ClusterIssuer
```

This requires an existing ClusterIssuer or Issuer. Create one if you haven't already:

```bash
# Create a ClusterIssuer (example with Let's Encrypt)
kubectl apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: your-email@example.com
    privateKeySecretRef:
      name: letsencrypt-prod
    solvers:
    - http01:
        ingress:
          class: haproxy
EOF
```

The Helm chart creates a Certificate resource that cert-manager uses to automatically provision and renew the TLS Secret.

### Alternative: Manual Certificate

To manage certificates without cert-manager, disable cert-manager integration and create a TLS Secret manually:

```yaml
controller:
  defaultSSLCertificate:
    certManager:
      enabled: false
```

```bash
kubectl create secret tls default-ssl-cert \
  --cert=path/to/tls.crt \
  --key=path/to/tls.key \
  --namespace=haptic
```

### Custom Certificate Names

To use a different Secret name or namespace:

```yaml
controller:
  defaultSSLCertificate:
    secretName: "my-wildcard-cert"
    namespace: "certificates"
```

The controller will reference the Secret at `certificates/my-wildcard-cert`.

### TLS Secret Format

The Secret must be of type `kubernetes.io/tls` and contain two keys:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: default-ssl-cert
  namespace: haptic
type: kubernetes.io/tls
data:
  tls.crt: LS0tLS1CRUdJTi... # Base64-encoded certificate
  tls.key: LS0tLS1CRUdJTi... # Base64-encoded private key
```

### Disabling HTTPS

To run in HTTP-only mode (not recommended):

```yaml
controller:
  defaultSSLCertificate:
    enabled: false
```

Note: This disables HTTPS support entirely. HAProxy will only serve HTTP traffic.

### Certificate Rotation

**With cert-manager**: Certificates are automatically renewed before expiration.

**Manual certificates**: You must update the Secret with a new certificate before the old one expires:

```bash
# Update Secret with new certificate
kubectl create secret tls default-ssl-cert \
  --cert=new-tls.crt \
  --key=new-tls.key \
  --namespace=haptic \
  --dry-run=client -o yaml | kubectl apply -f -
```

The controller watches the Secret and will automatically deploy the updated certificate to HAProxy.

### Troubleshooting

**"Secret not found" errors:**

Check that the Secret exists in the correct namespace:

```bash
kubectl get secret default-ssl-cert -n haptic
```

**HAProxy fails to start with SSL errors:**

Verify the certificate and key are valid:

```bash
# Extract and verify certificate
kubectl get secret default-ssl-cert -n haptic -o jsonpath='{.data.tls\.crt}' | base64 -d | openssl x509 -text -noout

# Verify key
kubectl get secret default-ssl-cert -n haptic -o jsonpath='{.data.tls\.key}' | base64 -d | openssl rsa -check -noout
```

**Certificate not being updated:**

The controller watches Secrets with `store: on-demand`. Changes are detected automatically, but HAProxy deployment follows the configured drift prevention interval (default: 60s).

## Webhook Certificate Configuration

The admission webhook requires TLS certificates. The simplest setup uses cert-manager with a self-signed issuer:

```yaml
webhook:
  enabled: true
  certManager:
    enabled: true
    createIssuer: true  # Creates a self-signed Issuer automatically
```

This is the recommended approach when cert-manager is installed. The chart creates:

- A self-signed `Issuer` resource
- A `Certificate` resource that references the Issuer
- The webhook is automatically configured with CA bundle injection

To use an existing Issuer or ClusterIssuer instead:

```yaml
webhook:
  certManager:
    enabled: true
    createIssuer: false
    issuerRef:
      name: my-existing-issuer
      kind: ClusterIssuer
```

For manual certificate management without cert-manager, provide the CA bundle:

```yaml
webhook:
  certManager:
    enabled: false
  caBundle: "LS0tLS1CRUdJTi..."  # Base64-encoded CA certificate
```

## Ingress Annotations

The controller supports annotations on Ingress resources for configuring HAProxy features.

### Basic Authentication

Enable HTTP basic authentication on Ingress resources using these annotations:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: protected-app
  annotations:
    haproxy.org/auth-type: "basic-auth"
    haproxy.org/auth-secret: "my-auth-secret"
    haproxy.org/auth-realm: "Protected Application"
spec:
  ingressClassName: haptic
  rules:
    - host: app.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-service
                port:
                  number: 80
```

**Annotations:**

| Annotation | Description | Required | Default |
|------------|-------------|----------|---------|
| `haproxy.org/auth-type` | Authentication type | Yes | - |
| `haproxy.org/auth-secret` | Secret name containing credentials | Yes | - |
| `haproxy.org/auth-realm` | HTTP auth realm shown to users | No | `"Restricted Area"` |

**Supported authentication types:**

- `basic-auth`: HTTP basic authentication with username/password

**Secret reference formats:**

- `"secret-name"`: Secret in same namespace as Ingress
- `"namespace/secret-name"`: Secret in specific namespace

### Creating Authentication Secrets

Secrets must contain username-password pairs where values are **base64-encoded crypt(3) SHA-512 password hashes**:

```bash
# Generate SHA-512 hash and encode for Kubernetes
HASH=$(openssl passwd -6 mypassword)

# Create secret with encoded hash
kubectl create secret generic my-auth-secret \
  --from-literal=admin=$(echo -n "$HASH" | base64 -w0) \
  --from-literal=user=$(echo -n "$HASH" | base64 -w0)
```

**Secret structure:**

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-auth-secret
type: Opaque
data:
  # Keys are usernames, values are base64-encoded password hashes
  admin: JDYkMVd3c2YxNmprcDBkMVBpTyRkS3FHUTF0SW0uOGF1VlJIcVA3dVcuMVV5dVNtZ3YveEc3dEFiOXdZNzc1REw3ZGE0N0hIeVB4ZllDS1BMTktZclJvMHRNQWQyQk1YUHBDd2Z5ZW03MA==
  user: JDYkbkdxOHJ1T2kyd3l4MUtyZyQ1a2d1azEzb2tKWmpzZ2Z2c3JqdmkvOVoxQjZIbDRUcGVvdkpzb2lQeHA2eGRKWUpha21wUmIwSUVHb1ZUSC8zRzZrLmRMRzBuVUNMWEZnMEhTRTJ5MA==
```

**Important:**

- Multiple Ingress resources can reference the same secret
- Secrets are fetched on-demand (requires `store: on-demand` in secrets configuration)
- Password hashes must use crypt(3) SHA-512 format for HAProxy compatibility

## Validation Sidecar

Enable the validation sidecar to test configurations before deployment:

```yaml
validation:
  enabled: true
```

This adds HAProxy + Dataplane sidecars to the controller pod for config validation.

## Debugging

### Introspection HTTP Server

The controller provides an introspection HTTP server that exposes:

- `/healthz` - Health check endpoint used by Kubernetes liveness and readiness probes
- `/debug/vars` - Internal state and runtime variables
- `/debug/pprof` - Go profiling endpoints

This server is always enabled (defaults to port 8080) to support health checks. You can customize the port:

```yaml
controller:
  debugPort: 8080  # Default port
```

Access introspection endpoints via port-forward:

```bash
# Forward introspection port from controller pod
kubectl port-forward deployment/my-controller 8080:8080

# Check health status
curl http://localhost:8080/healthz

# List all available debug variables
curl http://localhost:8080/debug/vars

# Get current controller configuration
curl http://localhost:8080/debug/vars/config

# Get rendered HAProxy configuration
curl http://localhost:8080/debug/vars/rendered

# Get recent events (last 100)
curl http://localhost:8080/debug/vars/events

# Get resource counts
curl http://localhost:8080/debug/vars/resources

# Go profiling (CPU, heap, goroutines)
curl http://localhost:8080/debug/pprof/
go tool pprof http://localhost:8080/debug/pprof/heap
```

### Debug Variables

Available debug variables:

| Endpoint | Description |
|----------|-------------|
| `/debug/vars` | List all available variables |
| `/debug/vars/config` | Current controller configuration |
| `/debug/vars/credentials` | Credentials metadata (not actual values) |
| `/debug/vars/rendered` | Last rendered HAProxy config |
| `/debug/vars/auxfiles` | Auxiliary files (SSL certs, maps) |
| `/debug/vars/resources` | Resource counts by type |
| `/debug/vars/events` | Recent events (default: last 100) |
| `/debug/vars/state` | Full state dump (use carefully) |
| `/debug/vars/uptime` | Controller uptime |
| `/debug/pprof/` | Go profiling endpoints |

### JSONPath Field Selection

Extract specific fields using JSONPath:

```bash
# Get only the config version
curl 'http://localhost:8080/debug/vars/config?field={.version}'

# Get only template names
curl 'http://localhost:8080/debug/vars/config?field={.config.templates}'

# Get rendered config size
curl 'http://localhost:8080/debug/vars/rendered?field={.size}'
```

## Monitoring

The controller exposes 11 Prometheus metrics on port 9090 at `/metrics` endpoint covering:

- **Reconciliation**: Cycles, errors, and duration
- **Deployment**: Operations, errors, and duration
- **Validation**: Total validations and errors
- **Resources**: Tracked resource counts by type
- **Events**: Event bus activity and subscribers

### Quick Access

Access metrics directly via port-forward:

```bash
# Port-forward to controller pod
kubectl port-forward -n <namespace> pod/<controller-pod> 9090:9090

# Fetch metrics
curl http://localhost:9090/metrics
```

### Prometheus ServiceMonitor

Enable Prometheus Operator integration:

```yaml
monitoring:
  serviceMonitor:
    enabled: true
    interval: 30s
    scrapeTimeout: 10s
    labels:
      prometheus: kube-prometheus  # Match your Prometheus selector
```

### With NetworkPolicy

If using NetworkPolicy, allow Prometheus to scrape metrics:

```yaml
networkPolicy:
  enabled: true
  ingress:
    monitoring:
      enabled: true  # Enable metrics ingress
      podSelector:
        matchLabels:
          app: prometheus
      namespaceSelector:
        matchLabels:
          name: monitoring
```

### Advanced ServiceMonitor Configuration

Add custom labels and relabeling:

```yaml
monitoring:
  serviceMonitor:
    enabled: true
    interval: 15s
    labels:
      prometheus: kube-prometheus
      team: platform
    # Add cluster label to all metrics
    relabelings:
      - sourceLabels: [__address__]
        targetLabel: cluster
        replacement: production
    # Drop specific metrics
    metricRelabelings:
      - sourceLabels: [__name__]
        regex: 'haptic_event_subscribers'
        action: drop
```

### Example Prometheus Queries

```promql
# Reconciliation rate (per second)
rate(haptic_reconciliation_total[5m])

# Error rate
rate(haptic_reconciliation_errors_total[5m])

# 95th percentile reconciliation duration
histogram_quantile(0.95, rate(haptic_reconciliation_duration_seconds_bucket[5m]))

# Current HAProxy pod count
haptic_resource_count{type="haproxy-pods"}
```

### Grafana Dashboard

Create dashboards using these key metrics:

1. **Operations Overview**: reconciliation_total, deployment_total, validation_total
2. **Error Tracking**: *_errors_total counters
3. **Performance**: *_duration_seconds histograms
4. **Resource Utilization**: resource_count gauge

For complete metric definitions and more queries, see `pkg/controller/metrics/README.md` in the repository.

## High Availability

The controller supports running multiple replicas with **leader election** to ensure only one replica deploys configurations to HAProxy while all replicas remain ready for immediate failover.

### Leader Election (Default)

**Default configuration (2 replicas with leader election):**

```yaml
replicaCount: 2  # Runs 2 replicas by default

controller:
  config:
    controller:
      leaderElection:
        enabled: true  # Enabled by default
        leaseName: ""  # Defaults to Helm release fullname
        leaseDuration: 15s
        renewDeadline: 10s
        retryPeriod: 2s
```

**How it works:**

- All replicas watch resources, render templates, and validate configs
- Only the elected leader deploys configurations to HAProxy instances
- Automatic failover if leader fails (within leaseDuration, default 15s)
- Leadership transitions are logged and tracked via Prometheus metrics

**Check current leader:**

```bash
# View Lease resource
kubectl get lease haptic-leader -o yaml

# Check metrics
kubectl port-forward deployment/haptic-controller 9090:9090
curl http://localhost:9090/metrics | grep leader_election_is_leader
```

See [High Availability Operations Guide](../../docs/operations/high-availability.md) for detailed documentation.

### Multiple Replicas

Run 3+ replicas for enhanced availability:

```yaml
replicaCount: 3

podDisruptionBudget:
  enabled: true
  minAvailable: 2

# Distribute across availability zones
affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          labelSelector:
            matchLabels:
              app.kubernetes.io/name: haptic
          topologyKey: topology.kubernetes.io/zone
```

### Single Replica (Development)

Disable leader election for development/testing:

```yaml
replicaCount: 1

controller:
  config:
    controller:
      leader_election:
        enabled: false
```

### Autoscaling

```yaml
autoscaling:
  enabled: true
  minReplicas: 2  # Keep at least 2 for HA
  maxReplicas: 10
  targetCPUUtilizationPercentage: 80
```

## Configuration Reference

Complete reference of all Helm values with types, defaults, and descriptions.

### Deployment & Image

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `replicaCount` | int | `2` | Number of controller replicas (2+ recommended for HA with leader election) |
| `image.repository` | string | `registry.gitlab.com/haproxy-haptic/haptic` | Controller image repository |
| `image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `image.tag` | string | Chart appVersion | Controller image tag |
| `imagePullSecrets` | list | `[]` | Image pull secrets for private registries |
| `nameOverride` | string | `""` | Override chart name |
| `fullnameOverride` | string | `""` | Override full release name |

### Controller Core

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.crdName` | string | `haptic-config` | Name of HAProxyTemplateConfig CRD resource |
| `controller.debugPort` | int | `8080` | Introspection HTTP server port (/healthz, /debug/*) |
| `controller.ports.healthz` | int | `8080` | Health check endpoint port |
| `controller.ports.metrics` | int | `9090` | Prometheus metrics endpoint port |
| `controller.ports.webhook` | int | `9443` | Admission webhook HTTPS port |

### Template Libraries

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.templateLibraries.base.enabled` | bool | `true` | Core HAProxy configuration (always enabled) |
| `controller.templateLibraries.ssl.enabled` | bool | `true` | SSL/TLS and HTTPS frontend support |
| `controller.templateLibraries.ingress.enabled` | bool | `true` | Kubernetes Ingress resource support |
| `controller.templateLibraries.gateway.enabled` | bool | `true` | Gateway API support (HTTPRoute, GRPCRoute) |
| `controller.templateLibraries.haproxytech.enabled` | bool | `true` | haproxy.org/* annotation support |
| `controller.templateLibraries.haproxyIngress.enabled` | bool | `true` | HAProxy Ingress Controller compatibility |
| `controller.templateLibraries.pathRegexLast.enabled` | bool | `false` | Performance-first path matching (regex last) |

### Default SSL Certificate

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.defaultSSLCertificate.enabled` | bool | `true` | Enable default SSL certificate requirement |
| `controller.defaultSSLCertificate.secretName` | string | `default-ssl-cert` | TLS Secret name containing certificate |
| `controller.defaultSSLCertificate.namespace` | string | `""` | Secret namespace (defaults to Release.Namespace) |
| `controller.defaultSSLCertificate.certManager.enabled` | bool | `true` | Use cert-manager for certificate provisioning |
| `controller.defaultSSLCertificate.certManager.createIssuer` | bool | `true` | Create self-signed Issuer (dev/test only) |
| `controller.defaultSSLCertificate.certManager.dnsNames` | list | `["localdev.me", "*.localdev.me"]` | DNS names for the certificate |
| `controller.defaultSSLCertificate.certManager.issuerRef.name` | string | `""` | Issuer name (auto-set when createIssuer=true) |
| `controller.defaultSSLCertificate.certManager.issuerRef.kind` | string | `Issuer` | Issuer kind |
| `controller.defaultSSLCertificate.certManager.duration` | duration | `8760h` | Certificate validity (1 year) |
| `controller.defaultSSLCertificate.certManager.renewBefore` | duration | `720h` | Renew before expiry (30 days) |
| `controller.defaultSSLCertificate.create` | bool | `false` | Create Secret from inline cert/key (testing only) |
| `controller.defaultSSLCertificate.cert` | string | `""` | PEM certificate (when create=true) |
| `controller.defaultSSLCertificate.key` | string | `""` | PEM private key (when create=true) |

### Controller Config

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.credentialsSecretRef.name` | string | Auto-generated | Secret containing Dataplane API credentials |
| `controller.config.credentialsSecretRef.namespace` | string | `""` | Credentials secret namespace |
| `controller.config.podSelector.matchLabels` | map | `{app.kubernetes.io/component: loadbalancer}` | Labels to match HAProxy pods |
| `controller.config.controller.healthzPort` | int | `8080` | Health check port |
| `controller.config.controller.metricsPort` | int | `9090` | Metrics port |

### Leader Election

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.controller.leaderElection.enabled` | bool | `true` | Enable leader election (recommended for HA) |
| `controller.config.controller.leaderElection.leaseName` | string | `""` | Lease resource name (defaults to release fullname) |
| `controller.config.controller.leaderElection.leaseDuration` | duration | `15s` | Failover timeout duration |
| `controller.config.controller.leaderElection.renewDeadline` | duration | `10s` | Leader renewal timeout |
| `controller.config.controller.leaderElection.retryPeriod` | duration | `2s` | Retry interval between attempts |

### Dataplane Configuration

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.dataplane.port` | int | `5555` | Dataplane API port |
| `controller.config.dataplane.minDeploymentInterval` | duration | `2s` | Minimum time between deployments |
| `controller.config.dataplane.driftPreventionInterval` | duration | `60s` | Periodic drift prevention interval |
| `controller.config.dataplane.mapsDir` | string | `/etc/haproxy/maps` | HAProxy maps directory |
| `controller.config.dataplane.sslCertsDir` | string | `/etc/haproxy/ssl` | SSL certificates directory |
| `controller.config.dataplane.generalStorageDir` | string | `/etc/haproxy/general` | General storage directory |
| `controller.config.dataplane.configFile` | string | `/etc/haproxy/haproxy.cfg` | HAProxy config file path |

### Logging & Templating

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.logLevel` | string | `INFO` | Initial log level: TRACE, DEBUG, INFO, WARN, ERROR (case-insensitive) |
| `controller.config.logging.level` | string | `""` | Log level from ConfigMap. If set, overrides controller.logLevel at runtime |
| `controller.config.templatingSettings.extraContext.debug` | bool | `true` | Enable debug headers in HAProxy responses |
| `controller.config.watchedResourcesIgnoreFields` | list | `[metadata.managedFields]` | Fields to ignore in watched resources |

### Webhook Configuration

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `webhook.enabled` | bool | `true` | Enable admission webhook validation |
| `webhook.secretName` | string | Auto-generated | Webhook TLS certificate secret name |
| `webhook.service.port` | int | `443` | Webhook service port |
| `webhook.certManager.enabled` | bool | `false` | Use cert-manager for certificates |
| `webhook.certManager.createIssuer` | bool | `true` | Create a self-signed Issuer for webhook certs |
| `webhook.certManager.issuerRef.name` | string | `""` | Issuer name (auto-set when createIssuer=true) |
| `webhook.certManager.issuerRef.kind` | string | `Issuer` | Issuer kind |
| `webhook.certManager.duration` | duration | `8760h` | Certificate validity (1 year) |
| `webhook.certManager.renewBefore` | duration | `720h` | Renew before expiry (30 days) |
| `webhook.caBundle` | string | `""` | Base64-encoded CA bundle (manual certs) |

### IngressClass

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `ingressClass.enabled` | bool | `true` | Create IngressClass resource |
| `ingressClass.name` | string | `haproxy` | IngressClass name |
| `ingressClass.default` | bool | `false` | Mark as default IngressClass |
| `ingressClass.controllerName` | string | `haproxy-haptic.org/controller` | Controller identifier |

### GatewayClass

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `gatewayClass.enabled` | bool | `true` | Create GatewayClass resource |
| `gatewayClass.name` | string | `haproxy` | GatewayClass name |
| `gatewayClass.default` | bool | `false` | Mark as default GatewayClass |
| `gatewayClass.controllerName` | string | `haproxy-haptic.org/controller` | Controller identifier |
| `gatewayClass.parametersRef.group` | string | `haproxy-haptic.org` | HAProxyTemplateConfig API group |
| `gatewayClass.parametersRef.kind` | string | `HAProxyTemplateConfig` | HAProxyTemplateConfig kind |
| `gatewayClass.parametersRef.name` | string | `""` | Config name (defaults to controller.crdName) |
| `gatewayClass.parametersRef.namespace` | string | `""` | Config namespace (defaults to Release.Namespace) |

### Credentials

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `credentials.dataplane.username` | string | `admin` | Dataplane API username |
| `credentials.dataplane.password` | string | `adminpass` | Dataplane API password |

### ServiceAccount & RBAC

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `serviceAccount.create` | bool | `true` | Create ServiceAccount |
| `serviceAccount.automount` | bool | `true` | Automount API credentials |
| `serviceAccount.annotations` | map | `{}` | ServiceAccount annotations |
| `serviceAccount.name` | string | `""` | ServiceAccount name (auto-generated if empty) |
| `rbac.create` | bool | `true` | Create RBAC resources |

### Pod Configuration

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `podAnnotations` | map | `{}` | Pod annotations |
| `podLabels` | map | `{}` | Additional pod labels |
| `priorityClassName` | string | `""` | Pod priority class name |
| `topologySpreadConstraints` | list | `[]` | Pod topology spread constraints |
| `podSecurityContext.runAsNonRoot` | bool | `true` | Run as non-root user |
| `podSecurityContext.runAsUser` | int | `65532` | User ID |
| `podSecurityContext.runAsGroup` | int | `65532` | Group ID |
| `podSecurityContext.fsGroup` | int | `65532` | Filesystem group ID |
| `podSecurityContext.seccompProfile.type` | string | `RuntimeDefault` | Seccomp profile type |

### Container Security Context

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `securityContext.allowPrivilegeEscalation` | bool | `false` | Allow privilege escalation |
| `securityContext.capabilities.drop` | list | `[ALL]` | Dropped capabilities |
| `securityContext.readOnlyRootFilesystem` | bool | `true` | Read-only root filesystem |
| `securityContext.runAsNonRoot` | bool | `true` | Run as non-root |
| `securityContext.runAsUser` | int | `65532` | Container user ID |

### Service & Health Probes

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `service.type` | string | `ClusterIP` | Controller service type |
| `livenessProbe.httpGet.path` | string | `/healthz` | Liveness probe path |
| `livenessProbe.initialDelaySeconds` | int | `10` | Initial delay |
| `livenessProbe.periodSeconds` | int | `10` | Probe period |
| `livenessProbe.failureThreshold` | int | `3` | Failure threshold |
| `readinessProbe.httpGet.path` | string | `/healthz` | Readiness probe path |
| `readinessProbe.initialDelaySeconds` | int | `5` | Initial delay |
| `readinessProbe.periodSeconds` | int | `5` | Probe period |
| `readinessProbe.failureThreshold` | int | `3` | Failure threshold |

### Resources & Scheduling

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `resources.requests.cpu` | string | `100m` | CPU request |
| `resources.requests.memory` | string | `128Mi` | Memory request |
| `resources.limits.cpu` | string | `""` | CPU limit (optional) |
| `resources.limits.memory` | string | `""` | Memory limit (optional) |
| `nodeSelector` | map | `{}` | Node selector |
| `tolerations` | list | `[]` | Pod tolerations |
| `affinity` | map | `{}` | Pod affinity rules |

### Autoscaling & PDB

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `autoscaling.enabled` | bool | `false` | Enable HorizontalPodAutoscaler |
| `autoscaling.minReplicas` | int | `1` | Minimum replicas |
| `autoscaling.maxReplicas` | int | `10` | Maximum replicas |
| `autoscaling.targetCPUUtilizationPercentage` | int | `80` | Target CPU utilization |
| `podDisruptionBudget.enabled` | bool | `true` | Enable PodDisruptionBudget |
| `podDisruptionBudget.minAvailable` | int | `1` | Minimum available pods |

### Monitoring

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `monitoring.serviceMonitor.enabled` | bool | `false` | Create ServiceMonitor for Prometheus |
| `monitoring.serviceMonitor.interval` | duration | `30s` | Scrape interval |
| `monitoring.serviceMonitor.scrapeTimeout` | duration | `10s` | Scrape timeout |
| `monitoring.serviceMonitor.labels` | map | `{}` | ServiceMonitor labels |
| `monitoring.serviceMonitor.relabelings` | list | `[]` | Prometheus relabelings |
| `monitoring.serviceMonitor.metricRelabelings` | list | `[]` | Metric relabelings |

### HAProxy Deployment

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.enabled` | bool | `true` | Deploy HAProxy pods with this chart |
| `haproxy.replicaCount` | int | `2` | Number of HAProxy replicas |
| `haproxy.image.repository` | string | `haproxytech/haproxy-debian` | HAProxy image repository |
| `haproxy.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `haproxy.image.tag` | string | `3.2` | HAProxy image tag |
| `haproxy.enterprise.enabled` | bool | `false` | Use HAProxy Enterprise |
| `haproxy.enterprise.version` | string | `3.2` | Enterprise version |
| `haproxy.haproxyBin` | string | Auto-detected | HAProxy binary path |
| `haproxy.dataplaneBin` | string | Auto-detected | Dataplane API binary path |
| `haproxy.user` | string | Auto-detected | HAProxy user |

### HAProxy Ports

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.ports.http` | int | `8080` | HTTP frontend container port |
| `haproxy.ports.https` | int | `8443` | HTTPS frontend container port |
| `haproxy.ports.stats` | int | `8404` | Stats/health page port |
| `haproxy.ports.dataplane` | int | `5555` | Dataplane API port |

### HAProxy Service

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.service.type` | string | `NodePort` | HAProxy service type |
| `haproxy.service.annotations` | map | `{}` | Service annotations |
| `haproxy.service.http.port` | int | `80` | HTTP service port |
| `haproxy.service.http.nodePort` | int | `30080` | HTTP NodePort |
| `haproxy.service.https.port` | int | `443` | HTTPS service port |
| `haproxy.service.https.nodePort` | int | `30443` | HTTPS NodePort |
| `haproxy.service.stats.port` | int | `8404` | Stats service port |
| `haproxy.service.stats.nodePort` | int | `30404` | Stats NodePort |

### HAProxy Dataplane Sidecar

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.dataplane.service.type` | string | `ClusterIP` | Dataplane service type |
| `haproxy.dataplane.credentials.username` | string | `admin` | Dataplane API username |
| `haproxy.dataplane.credentials.password` | string | `adminpass` | Dataplane API password |

### HAProxy Resources & Scheduling

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.resources.requests.cpu` | string | `100m` | CPU request |
| `haproxy.resources.requests.memory` | string | `128Mi` | Memory request |
| `haproxy.resources.limits.cpu` | string | `500m` | CPU limit |
| `haproxy.resources.limits.memory` | string | `512Mi` | Memory limit |
| `haproxy.priorityClassName` | string | `""` | Pod priority class |
| `haproxy.topologySpreadConstraints` | list | `[]` | Topology spread constraints |

### HAProxy NetworkPolicy

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.networkPolicy.enabled` | bool | `false` | Enable HAProxy NetworkPolicy |
| `haproxy.networkPolicy.allowExternal` | bool | `true` | Allow external traffic |
| `haproxy.networkPolicy.allowedSources` | list | `[]` | Allowed traffic sources (when allowExternal=false) |
| `haproxy.networkPolicy.extraIngress` | list | `[]` | Additional ingress rules |
| `haproxy.networkPolicy.extraEgress` | list | `[]` | Additional egress rules |

### Controller NetworkPolicy

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `networkPolicy.enabled` | bool | `true` | Enable controller NetworkPolicy |
| `networkPolicy.egress.allowDNS` | bool | `true` | Allow DNS resolution |
| `networkPolicy.egress.kubernetesApi` | list | See values.yaml | Kubernetes API access rules |
| `networkPolicy.egress.haproxyPods.enabled` | bool | `true` | Allow access to HAProxy pods |
| `networkPolicy.egress.haproxyPods.podSelector` | map | See values.yaml | HAProxy pod selector |
| `networkPolicy.egress.haproxyPods.namespaceSelector` | map | `{}` | Namespace selector |
| `networkPolicy.egress.additionalRules` | list | See values.yaml | Additional egress rules |
| `networkPolicy.ingress.monitoring.enabled` | bool | `false` | Allow Prometheus scraping |
| `networkPolicy.ingress.monitoring.podSelector` | map | `{}` | Prometheus pod selector |
| `networkPolicy.ingress.monitoring.namespaceSelector` | map | `{}` | Prometheus namespace selector |
| `networkPolicy.ingress.healthChecks.enabled` | bool | `true` | Allow health check access |
| `networkPolicy.ingress.dataplaneApi.enabled` | bool | `true` | Allow Dataplane API access |
| `networkPolicy.ingress.webhook.enabled` | bool | `true` | Allow webhook access |
| `networkPolicy.ingress.additionalRules` | list | `[]` | Additional ingress rules |

## Upgrading

### Upgrade the Chart

```bash
helm upgrade my-controller haptic/haptic
```

### Upgrade with New Values

```bash
helm upgrade my-controller haptic/haptic \
  -f my-values.yaml
```

## Uninstalling

```bash
helm uninstall my-controller
```

This removes all resources created by the chart.

## Troubleshooting

### Controller Not Starting

Check logs:

```bash
kubectl logs -f -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller
```

Common issues:

- HAProxyTemplateConfig CRD or Secret missing
- RBAC permissions incorrect
- NetworkPolicy blocking access

### Cannot Connect to HAProxy Pods

1. **Check HAProxy pod labels** match `pod_selector`

   ```bash
   kubectl get pods --show-labels
   ```

2. **Verify Dataplane API is accessible**

   ```bash
   kubectl port-forward <haproxy-pod> 5555:5555
   curl http://localhost:5555/v3/info
   ```

3. **Check NetworkPolicy**

   ```bash
   kubectl describe networkpolicy
   ```

### NetworkPolicy Issues in kind

For kind clusters, ensure:

- Calico or Cilium CNI is installed
- DNS access is allowed
- Kubernetes API CIDR is correct

Debug NetworkPolicy:

```bash
# Check controller can resolve DNS
kubectl exec <controller-pod> -- nslookup kubernetes.default

# Check controller can reach HAProxy pod
kubectl exec <controller-pod> -- curl http://<haproxy-pod-ip>:5555/v3/info
```

## Examples

See the `examples/` directory for:

- Basic Ingress setup
- Multi-namespace configuration
- Production-ready values
- NetworkPolicy configurations

## Contributing

Contributions are welcome! Please see the main repository for guidelines.

## License

See the main repository for license information.
