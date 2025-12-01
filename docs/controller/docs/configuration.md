# Controller Configuration Reference

!!! warning "Deprecated: Use HAProxyTemplateConfig CRD Instead"
    This ConfigMap-based configuration approach is **deprecated**. For new deployments, use the [HAProxyTemplateConfig CRD](./crd-reference.md) which provides:

    - Kubernetes-native configuration with validation
    - Better integration with GitOps workflows
    - Schema validation and type safety
    - Status reporting and conditions

    This documentation is maintained for reference only. Migrate existing deployments to the CRD approach.

## Overview

The HAProxy Template Ingress Controller is configured using two Kubernetes resources:

1. **ConfigMap** (deprecated) - Contains the controller configuration, templates, and HAProxy settings
2. **Secret** - Contains credentials for the HAProxy Dataplane API

The controller automatically watches these resources for changes and reloads configuration dynamically without requiring
pod restarts.

## Environment Variables

The controller accepts configuration through environment variables and command-line flags.

| Variable         | Flag               | Default               | Description                                                     |
|------------------|--------------------|-----------------------|-----------------------------------------------------------------|
| `CONFIGMAP_NAME` | `--configmap-name` | `haproxy-config`      | Name of the ConfigMap containing controller configuration       |
| `SECRET_NAME`    | `--secret-name`    | `haproxy-credentials` | Name of the Secret containing HAProxy Dataplane API credentials |
| `VERBOSE`        | (none)             | `1`                   | Log level: `0`=WARNING, `1`=INFO, `2`=DEBUG                     |
| `DEBUG_PORT`     | `--debug-port`     | `0`                   | Port for debug HTTP server (0 to disable)                       |

**Priority Order**: CLI flags > Environment variables > Defaults

**Additional Flags**:

- `--kubeconfig` - Path to kubeconfig file (for out-of-cluster development)

**Example**:

```bash
# Using environment variables
export CONFIGMAP_NAME=my-haproxy-config
export SECRET_NAME=my-haproxy-credentials
export VERBOSE=2
./controller

# Using CLI flags
./controller --configmap-name=my-haproxy-config --secret-name=my-haproxy-credentials
```

## ConfigMap Configuration

The ConfigMap must contain a `config` key with YAML configuration.

### Complete Field Reference

#### `pod_selector`

Identifies which HAProxy pods to configure.

| Field          | Type              | Required | Description                  |
|----------------|-------------------|----------|------------------------------|
| `match_labels` | map[string]string | Yes      | Labels to match HAProxy pods |

**Example**:

```yaml
pod_selector:
  match_labels:
    app: haproxy
    component: loadbalancer
```

#### `controller`

Controller-level settings.

| Field          | Type | Default | Description                                             |
|----------------|------|---------|---------------------------------------------------------|
| `healthz_port` | int  | `8080`  | Port for health check endpoints (`/healthz`, `/readyz`) |
| `metrics_port` | int  | `9090`  | Port for Prometheus metrics endpoint (`/metrics`)       |

**Example**:

```yaml
controller:
  healthz_port: 8080
  metrics_port: 9090
```

#### `logging`

Logging configuration.

| Field     | Type | Default | Description                                 |
|-----------|------|---------|---------------------------------------------|
| `verbose` | int  | `1`     | Log level: `0`=WARNING, `1`=INFO, `2`=DEBUG |

**Example**:

```yaml
logging:
  verbose: 1  # INFO level
```

#### `dataplane`

HAProxy Dataplane API configuration and file paths.

| Field                       | Type   | Default                    | Description                                                      |
|-----------------------------|--------|----------------------------|------------------------------------------------------------------|
| `port`                      | int    | `5555`                     | Dataplane API port for production HAProxy pods                   |
| `min_deployment_interval`   | string | `2s`                       | Minimum time between consecutive deployments (Go duration)       |
| `drift_prevention_interval` | string | `60s`                      | Interval for periodic drift prevention deployments (Go duration) |
| `maps_dir`                  | string | `/etc/haproxy/maps`        | Directory for HAProxy map files                                  |
| `ssl_certs_dir`             | string | `/etc/haproxy/ssl`         | Directory for SSL certificates                                   |
| `general_storage_dir`       | string | `/etc/haproxy/general`     | Directory for general files (error pages, etc.)                  |
| `config_file`               | string | `/etc/haproxy/haproxy.cfg` | Path to main HAProxy configuration file                          |

**Example**:

```yaml
dataplane:
  port: 5555
  min_deployment_interval: 2s
  drift_prevention_interval: 60s
  maps_dir: /etc/haproxy/maps
  ssl_certs_dir: /etc/haproxy/ssl
  general_storage_dir: /etc/haproxy/general
  config_file: /etc/haproxy/haproxy.cfg
```

**Notes**:

- Paths are used for both validation and deployment
- Paths must match HAProxy Dataplane API resource configuration
- Duration strings use Go format: `500ms`, `2s`, `5m`, `1h`

#### `templating_settings`

Template rendering configuration and custom variables.

| Field           | Type                    | Default | Description                                                             |
|-----------------|-------------------------|---------|-------------------------------------------------------------------------|
| `extra_context` | map[string]interface{}  | `{}`    | Custom variables merged into template context (available in all templates) |

**Example**:

```yaml
templating_settings:
  extra_context:
    debug:
      enabled: true
      verbose_headers: false
    environment: production
    feature_flags:
      rate_limiting: true
      caching: false
    custom_timeout: 30
```

**Usage in templates**:

Custom variables are merged at the top level of the template context. Access them directly:

```jinja2
{% if debug.enabled %}
  # Debug-specific configuration
  http-response set-header X-HAProxy-Backend %[be_name]
{% endif %}

{% if environment == "production" %}
  timeout client {{ custom_timeout }}s
{% endif %}

{% if feature_flags.rate_limiting %}
  # Rate limiting configuration
  stick-table type ip size 100k expire 30s store http_req_rate(10s)
{% endif %}
```

See [Templating Guide](./templating.md#custom-template-variables) for detailed examples and use cases.

#### `watched_resources_ignore_fields`

JSONPath expressions for fields to remove from watched resources to reduce memory usage.

| Type     | Default | Description                  |
|----------|---------|------------------------------|
| []string | `[]`    | List of JSONPath expressions |

**Example**:

```yaml
watched_resources_ignore_fields:
  - metadata.managedFields
  - metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]
```

#### `watched_resources`

Defines which Kubernetes resources to watch and how to index them.

| Field                       | Type              | Required              | Description                                           |
|-----------------------------|-------------------|-----------------------|-------------------------------------------------------|
| `api_version`               | string            | Yes                   | Kubernetes API version (e.g., `networking.k8s.io/v1`) |
| `kind`                      | string            | Yes                   | Kubernetes resource kind (e.g., `Ingress`)            |
| `enable_validation_webhook` | bool              | No (default: `false`) | Enable admission webhook validation for this resource |
| `index_by`                  | []string          | Yes                   | JSONPath expressions for extracting index keys        |
| `label_selector`            | map[string]string | No                    | Filter resources by labels (server-side filtering)    |

**Example**:

```yaml
watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    index_by:
      - metadata.namespace
      - metadata.name
    label_selector:
      app: myapp

  services:
    api_version: v1
    kind: Service
    index_by:
      - metadata.namespace
      - metadata.name

  endpointslices:
    api_version: discovery.k8s.io/v1
    kind: EndpointSlice
    index_by:
      - metadata.namespace
      - metadata.labels['kubernetes.io/service-name']
```

#### `template_snippets`

Reusable template fragments that can be included in other templates.

| Field      | Type   | Required | Description                                   |
|------------|--------|----------|-----------------------------------------------|
| `name`     | string | Yes      | Snippet identifier for `{% include "name" %}` |
| `template` | string | Yes      | Template content                              |

**Example**:

```yaml
template_snippets:
  logging-config:
    name: logging-config
    template: |
      log stdout local0 info
      log stdout local1 notice

  ssl-options:
    name: ssl-options
    template: |
      ssl-default-bind-ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256
      ssl-default-bind-options ssl-min-ver TLSv1.2 no-tls-tickets
```

Use in templates:

```jinja2
global
    {% include "logging-config" %}
    {% include "ssl-options" %}
```

#### `maps`

HAProxy map file templates. Map files are used for dynamic routing, ACL matching, and other lookups.

| Field      | Type   | Required | Description                                  |
|------------|--------|----------|----------------------------------------------|
| `template` | string | Yes      | Template content that generates the map file |

**Example**:

```yaml
maps:
  host.map:
    template: |
      # Generated map file for host-based routing
      {% for ingress in ingresses %}
      {%- for rule in ingress.spec.rules %}
      {{ rule.host }} {{ ingress.metadata.name }}-backend
      {% endfor %}
      {%- endfor %}
```

Map file names are used as keys (e.g., `host.map`).

Use in HAProxy config with `pathResolver.GetPath()` method:

```jinja2
use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]
```

#### `files`

General-purpose auxiliary file templates (error pages, etc.).

| Field      | Type   | Required | Description                              |
|------------|--------|----------|------------------------------------------|
| `template` | string | Yes      | Template content that generates the file |

**Example**:

```yaml
files:
  404.http:
    template: |
      HTTP/1.1 404 Not Found
      Content-Type: text/html
      Cache-Control: no-cache
      Connection: close

      <!DOCTYPE html>
      <html>
      <head><title>404 Not Found</title></head>
      <body>
        <h1>404 Not Found</h1>
        <p>The requested resource was not found.</p>
      </body>
      </html>

  503.http:
    template: |
      HTTP/1.1 503 Service Unavailable
      Content-Type: text/html
      Cache-Control: no-cache
      Connection: close

      <!DOCTYPE html>
      <html>
      <head><title>503 Service Unavailable</title></head>
      <body>
        <h1>503 Service Unavailable</h1>
        <p>The service is temporarily unavailable.</p>
      </body>
      </html>
```

Use in HAProxy config with `pathResolver.GetPath()` method:

```jinja2
errorfile 404 {{ pathResolver.GetPath("404.http", "file") }}
errorfile 503 {{ pathResolver.GetPath("503.http", "file") }}
```

#### `ssl_certificates`

SSL certificate file templates. Certificates are typically sourced from Kubernetes Secrets.

| Field      | Type   | Required | Description                                          |
|------------|--------|----------|------------------------------------------------------|
| `template` | string | Yes      | Template content that generates the certificate file |

**Example**:

```yaml
ssl_certificates:
  example.com.pem:
    template: |
      {%- for secret in secrets %}
      {%- if secret.metadata.name == "example-com-tls" %}
      {{ secret.data['tls.crt'] | b64decode }}
      {{ secret.data['tls.key'] | b64decode }}
      {%- endif %}
      {%- endfor %}

  wildcard.example.com.pem:
    template: |
      {%- for secret in secrets %}
      {%- if secret.metadata.name == "wildcard-example-com-tls" %}
      {{ secret.data['tls.crt'] | b64decode }}
      {{ secret.data['tls.key'] | b64decode }}
      {%- endif %}
      {%- endfor %}
```

Use in HAProxy config with `pathResolver.GetPath()` method:

```jinja2
bind :443 ssl crt {{ pathResolver.GetPath("example.com.pem", "cert") }}
```

#### `haproxy_config`

Main HAProxy configuration template.

| Field      | Type   | Required | Description                         |
|------------|--------|----------|-------------------------------------|
| `template` | string | Yes      | Main HAProxy configuration template |

**Example**:

```yaml
haproxy_config:
  template: |
    global
        daemon
        maxconn 2000
        {% include "logging-config" %}
        {% include "ssl-options" %}

    defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
        errorfile 404 {{ pathResolver.GetPath("404.http", "file") }}
        errorfile 503 {{ pathResolver.GetPath("503.http", "file") }}

    frontend http
        bind :80
        use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

    frontend https
        bind :443 ssl crt {{ pathResolver.GetPath("example.com.pem", "cert") }}
        use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

    {% for ingress in ingresses %}
    backend {{ ingress.metadata.name }}-backend
        balance roundrobin
        {%- for rule in ingress.spec.rules %}
        {%- for path in rule.http.paths %}
        {%- set service_name = path.backend.service.name %}
        {%- set service_port = path.backend.service.port.number %}
        {%- for slice in endpointslices %}
        {%- if slice.metadata.labels['kubernetes.io/service-name'] == service_name %}
        {%- for endpoint in slice.endpoints %}
        {%- if endpoint.conditions.ready %}
        {%- for address in endpoint.addresses %}
        server {{ address }}_{{ service_port }} {{ address }}:{{ service_port }} check
        {%- endfor %}
        {%- endif %}
        {%- endfor %}
        {%- endif %}
        {%- endfor %}
        {%- endfor %}
        {%- endfor %}
    {% endfor %}
```

See [templating.md](./templating.md) for detailed template syntax and available filters.

### Complete ConfigMap Example

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: haproxy-config
  namespace: default
data:
  config: |
    # Pod selector
    pod_selector:
      match_labels:
        app: haproxy
        component: loadbalancer

    # Controller settings
    controller:
      healthz_port: 8080
      metrics_port: 9090

    # Logging configuration
    logging:
      verbose: 1

    # Dataplane API configuration
    dataplane:
      port: 5555
      min_deployment_interval: 2s
      drift_prevention_interval: 60s
      maps_dir: /etc/haproxy/maps
      ssl_certs_dir: /etc/haproxy/ssl
      general_storage_dir: /etc/haproxy/general
      config_file: /etc/haproxy/haproxy.cfg

    # Resource watching configuration
    watched_resources_ignore_fields:
      - metadata.managedFields

    watched_resources:
      ingresses:
        api_version: networking.k8s.io/v1
        kind: Ingress
        index_by:
          - metadata.namespace
          - metadata.name

      services:
        api_version: v1
        kind: Service
        index_by:
          - metadata.namespace
          - metadata.name

      endpointslices:
        api_version: discovery.k8s.io/v1
        kind: EndpointSlice
        index_by:
          - metadata.namespace
          - metadata.labels['kubernetes.io/service-name']

      secrets:
        api_version: v1
        kind: Secret
        index_by:
          - metadata.namespace
          - metadata.name
        label_selector:
          cert-type: tls

    # Template snippets
    template_snippets:
      logging-config:
        name: logging-config
        template: |
          log stdout local0 info
          log stdout local1 notice

      ssl-options:
        name: ssl-options
        template: |
          ssl-default-bind-ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256
          ssl-default-bind-options ssl-min-ver TLSv1.2 no-tls-tickets

    # Map files
    maps:
      host.map:
        template: |
          # Host-based routing map
          {% for ingress in ingresses %}
          {%- for rule in ingress.spec.rules %}
          {{ rule.host }} {{ ingress.metadata.name }}-backend
          {% endfor %}
          {%- endfor %}

    # Auxiliary files
    files:
      404.http:
        template: |
          HTTP/1.1 404 Not Found
          Content-Type: text/html

          <!DOCTYPE html>
          <html><body><h1>404 Not Found</h1></body></html>

    # SSL certificates
    ssl_certificates:
      example.com.pem:
        template: |
          {%- for secret in secrets %}
          {%- if secret.metadata.name == "example-com-tls" %}
          {{ secret.data['tls.crt'] | b64decode }}
          {{ secret.data['tls.key'] | b64decode }}
          {%- endif %}
          {%- endfor %}

    # Main HAProxy configuration
    haproxy_config:
      template: |
        global
            daemon
            maxconn 2000
            {% include "logging-config" %}
            {% include "ssl-options" %}

        defaults
            mode http
            timeout connect 5s
            timeout client 30s
            timeout server 30s
            errorfile 404 {{ pathResolver.GetPath("404.http", "file") }}

        frontend http
            bind :80
            use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

        frontend https
            bind :443 ssl crt {{ pathResolver.GetPath("example.com.pem", "cert") }}
            use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

        {% for ingress in ingresses %}
        backend {{ ingress.metadata.name }}-backend
            balance roundrobin
            {%- for rule in ingress.spec.rules %}
            {%- for path in rule.http.paths %}
            {%- set service_name = path.backend.service.name %}
            {%- set service_port = path.backend.service.port.number %}
            {%- for slice in endpointslices %}
            {%- if slice.metadata.labels['kubernetes.io/service-name'] == service_name %}
            {%- for endpoint in slice.endpoints %}
            {%- if endpoint.conditions.ready %}
            {%- for address in endpoint.addresses %}
            server {{ address }}_{{ service_port }} {{ address }}:{{ service_port }} check
            {%- endfor %}
            {%- endif %}
            {%- endfor %}
            {%- endif %}
            {%- endfor %}
            {%- endfor %}
            {%- endfor %}
        {% endfor %}
```

## Secret Configuration

The Secret contains credentials for authenticating with the HAProxy Dataplane API.

### Required Keys

| Key                  | Type   | Required | Description                                       |
|----------------------|--------|----------|---------------------------------------------------|
| `dataplane_username` | string | Yes      | Username for HAProxy Dataplane API authentication |
| `dataplane_password` | string | Yes      | Password for HAProxy Dataplane API authentication |

### Complete Secret Example

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: haproxy-credentials
  namespace: default
type: Opaque
data:
  # Base64 encoded credentials
  # echo -n "admin" | base64
  dataplane_username: YWRtaW4=
  # echo -n "your-secure-password" | base64
  dataplane_password: eW91ci1zZWN1cmUtcGFzc3dvcmQ=
```

### Security Notes

1. **Base64 Encoding**: Kubernetes Secrets automatically base64-encode values. When creating Secrets via kubectl or
   YAML, provide base64-encoded values in the `data` field, or use `stringData` for plain text (Kubernetes will encode
   it).

2. **Secret Best Practices**:
    - Use RBAC to restrict Secret access
    - Consider using encrypted Secrets (e.g., sealed-secrets, external-secrets)
    - Rotate credentials regularly
    - Never commit Secrets to version control

3. **Creating Secrets**:

Using kubectl:

```bash
kubectl create secret generic haproxy-credentials \
  --from-literal=dataplane_username=admin \
  --from-literal=dataplane_password=your-secure-password
```

Using YAML with stringData (unencoded):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: haproxy-credentials
  namespace: default
type: Opaque
stringData:
  dataplane_username: admin
  dataplane_password: your-secure-password
```

## RBAC Configuration

The controller requires specific Kubernetes permissions to function correctly.

### Required Permissions

The controller needs the following permissions:

| Resource         | API Group           | Verbs                  | Reason                                            |
|------------------|---------------------|------------------------|---------------------------------------------------|
| `configmaps`     | `` (core)           | `get`, `list`, `watch` | Read controller configuration                     |
| `secrets`        | `` (core)           | `get`, `list`, `watch` | Read API credentials and TLS certificates         |
| `services`       | `` (core)           | `get`, `list`, `watch` | Watch Service resources for backend configuration |
| `pods`           | `` (core)           | `get`, `list`, `watch` | Discover HAProxy pods and endpoints               |
| `namespaces`     | `` (core)           | `get`, `list`, `watch` | Support cross-namespace resource watching         |
| `ingresses`      | `networking.k8s.io` | `get`, `list`, `watch` | Watch Ingress resources for routing configuration |
| `endpointslices` | `discovery.k8s.io`  | `get`, `list`, `watch` | Watch EndpointSlice for backend server discovery  |

**Note**: Additional resource permissions may be required based on your `watched_resources` configuration.

### Complete RBAC Manifests

#### ServiceAccount

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: haproxy-template-ic
  namespace: default
```

#### ClusterRole

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: haproxy-template-ic
rules:
  # Core resources - required for controller operation
  - apiGroups: [ "" ]
    resources:
      - configmaps     # Controller configuration
      - secrets        # API credentials and TLS certificates
      - services       # Backend service discovery
      - pods           # HAProxy pod discovery
      - namespaces     # Cross-namespace resource watching
    verbs: [ "get", "list", "watch" ]

  # Ingress resources - routing configuration
  - apiGroups: [ "networking.k8s.io" ]
    resources:
      - ingresses
    verbs: [ "get", "list", "watch" ]

  # EndpointSlice - backend server discovery
  - apiGroups: [ "discovery.k8s.io" ]
    resources:
      - endpointslices
    verbs: [ "get", "list", "watch" ]
```

#### ClusterRoleBinding

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: haproxy-template-ic
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: haproxy-template-ic
subjects:
  - kind: ServiceAccount
    name: haproxy-template-ic
    namespace: default
```

### Namespace-Scoped Alternative

For single-namespace deployments, use Role instead of ClusterRole:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: haproxy-template-ic
  namespace: default
rules:
  - apiGroups: [ "" ]
    resources: [ "configmaps", "secrets", "services", "pods" ]
    verbs: [ "get", "list", "watch" ]

  - apiGroups: [ "networking.k8s.io" ]
    resources: [ "ingresses" ]
    verbs: [ "get", "list", "watch" ]

  - apiGroups: [ "discovery.k8s.io" ]
    resources: [ "endpointslices" ]
    verbs: [ "get", "list", "watch" ]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: haproxy-template-ic
  namespace: default
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: haproxy-template-ic
subjects:
  - kind: ServiceAccount
    name: haproxy-template-ic
    namespace: default
```

**Limitations**: Namespace-scoped RBAC prevents watching resources in other namespaces.

## Deployment Integration

### Controller Deployment

Example Deployment manifest showing environment variable configuration:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: haproxy-template-ic
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: haproxy-template-ic
  template:
    metadata:
      labels:
        app: haproxy-template-ic
      annotations:
        # Trigger pod restart on config/secret changes
        checksum/config: "<config-sha256>"
        checksum/secret: "<secret-sha256>"
    spec:
      serviceAccountName: haproxy-template-ic
      containers:
        - name: controller
          image: haproxy-template-ic:latest
          imagePullPolicy: IfNotPresent

          # Environment variables
          env:
            - name: CONFIGMAP_NAME
              value: "haproxy-config"
            - name: SECRET_NAME
              value: "haproxy-credentials"
            - name: VERBOSE
              value: "1"
            # Optionally enable debug server
            # - name: DEBUG_PORT
            #   value: "6060"

          # Container ports
          ports:
            - name: healthz
              containerPort: 8080
              protocol: TCP
            - name: metrics
              containerPort: 9090
              protocol: TCP

          # Health probes
          livenessProbe:
            httpGet:
              path: /healthz
              port: healthz
            initialDelaySeconds: 30
            periodSeconds: 10
            timeoutSeconds: 5
            failureThreshold: 3

          readinessProbe:
            httpGet:
              path: /readyz
              port: healthz
            initialDelaySeconds: 5
            periodSeconds: 5
            timeoutSeconds: 3
            failureThreshold: 3

          # Resource limits
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: 500m
              memory: 512Mi

          # Security context
          securityContext:
            runAsNonRoot: true
            runAsUser: 65534
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
            readOnlyRootFilesystem: true
```

### Service for Metrics

Expose metrics endpoint for Prometheus:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: haproxy-template-ic-metrics
  namespace: default
  labels:
    app: haproxy-template-ic
spec:
  type: ClusterIP
  ports:
    - name: metrics
      port: 9090
      targetPort: metrics
      protocol: TCP
  selector:
    app: haproxy-template-ic
```

### ServiceMonitor (Optional)

For Prometheus Operator:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: haproxy-template-ic
  namespace: default
spec:
  selector:
    matchLabels:
      app: haproxy-template-ic
  endpoints:
    - port: metrics
      interval: 30s
      path: /metrics
```

## Configuration Validation

The controller performs multi-stage validation:

1. **YAML Parsing**: Validates YAML syntax when loading ConfigMap
2. **Structural Validation**: Checks required fields, types, and value ranges
3. **Template Compilation**: Pre-compiles all templates to catch syntax errors
4. **JSONPath Validation**: Validates `index_by` expressions
5. **HAProxy Validation**: Validates rendered HAProxy configuration using Dataplane API

Configuration errors are logged and the controller will not apply invalid configurations. Check controller logs for
detailed error messages.

## Further Reading

- [Template Syntax and Filters](./templating.md) - Detailed template documentation
- [Supported HAProxy Configuration](./supported-configuration.md) - HAProxy sections and child components
- [Helm Chart Values](../charts/haproxy-template-ic/values.yaml) - Production deployment examples

## Troubleshooting

### Controller Not Starting

1. Check environment variables are set correctly:
   ```bash
   kubectl get deployment haproxy-template-ic -o jsonpath='{.spec.template.spec.containers[0].env}'
   ```

2. Verify ConfigMap exists and has correct key:
   ```bash
   kubectl get configmap haproxy-config -o yaml
   # Should have data.config key with YAML content
   ```

3. Verify Secret exists and has required keys:
   ```bash
   kubectl get secret haproxy-credentials -o jsonpath='{.data}' | jq 'keys'
   # Should include: dataplane_username, dataplane_password
   ```

4. Check RBAC permissions:
   ```bash
   kubectl auth can-i get configmaps --as=system:serviceaccount:default:haproxy-template-ic
   kubectl auth can-i get secrets --as=system:serviceaccount:default:haproxy-template-ic
   ```

### Configuration Not Loading

1. Check controller logs for parsing errors:
   ```bash
   kubectl logs -l app=haproxy-template-ic | grep -i "config"
   ```

2. Validate YAML syntax:
   ```bash
   kubectl get configmap haproxy-config -o jsonpath='{.data.config}' | yq eval -
   ```

3. Check for validation errors:
   ```bash
   kubectl logs -l app=haproxy-template-ic | grep -i "validation"
   ```

### Templates Not Rendering

1. Enable debug logging:
   ```bash
   kubectl set env deployment/haproxy-template-ic VERBOSE=2
   ```

2. Check template compilation errors:
   ```bash
   kubectl logs -l app=haproxy-template-ic | grep -i "template"
   ```

3. Verify watched resources are being indexed:
   ```bash
   kubectl logs -l app=haproxy-template-ic | grep -i "index"
   ```

### Credentials Not Working

1. Verify credentials are correctly base64 encoded:
   ```bash
   kubectl get secret haproxy-credentials -o jsonpath='{.data.dataplane_username}' | base64 -d
   ```

2. Test credentials against HAProxy Dataplane API:
   ```bash
   # Port-forward to HAProxy pod
   kubectl port-forward pod/<haproxy-pod> 5555:5555

   # Test authentication
   curl -u admin:password http://localhost:5555/v2/services/haproxy/configuration/version
   ```

3. Check for credential rotation events:
   ```bash
   kubectl logs -l app=haproxy-template-ic | grep -i "credentials"
   ```
