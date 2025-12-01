# HAProxyTemplateConfig CRD Reference

## Overview

The `HAProxyTemplateConfig` custom resource is the **recommended way** to configure the HAProxy Template Ingress Controller. It provides a Kubernetes-native API with built-in validation, type safety, and embedded testing capabilities.

**Benefits over ConfigMap:**
- Schema validation catches errors before deployment
- Status conditions provide feedback on configuration health
- Native Kubernetes resource with proper RBAC
- Better GitOps integration with declarative configuration

**API Group**: `haproxy-template-ic.github.io`
**API Version**: `v1alpha1`
**Kind**: `HAProxyTemplateConfig`
**Short Names**: `htplcfg`, `haptpl`

## Basic Example

```yaml
apiVersion: haproxy-template-ic.github.io/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: haproxy-config
  namespace: default
spec:
  credentialsSecretRef:
    name: haproxy-credentials

  podSelector:
    matchLabels:
      app: haproxy
      component: loadbalancer

  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      indexBy:
        - metadata.namespace
        - metadata.name

  haproxyConfig:
    template: |
      global
          daemon
      defaults
          timeout connect 5s
      frontend http
          bind *:80
```

## Spec Fields

### credentialsSecretRef (required)

References a Secret containing Dataplane API credentials.

```yaml
credentialsSecretRef:
  name: haproxy-credentials
  namespace: default  # Optional, defaults to config namespace
```

**Required Secret keys:**
- `dataplane_username` - Production Dataplane API username
- `dataplane_password` - Production Dataplane API password
- `validation_username` - Validation HAProxy username (if validation enabled)
- `validation_password` - Validation HAProxy password (if validation enabled)

### podSelector (required)

Labels that identify which HAProxy pods the controller should manage.

```yaml
podSelector:
  matchLabels:
    app: haproxy
    component: loadbalancer
```

At least one label must be specified.

### controller

Controller-level settings for ports and leader election.

```yaml
controller:
  healthzPort: 8080  # Health check endpoints
  metricsPort: 9090  # Prometheus metrics

  leaderElection:
    enabled: true
    leaseName: haproxy-template-ic-leader
    leaseDuration: 60s
    renewDeadline: 15s
    retryPeriod: 5s
```

**Defaults:**
- `healthzPort`: 8080
- `metricsPort`: 9090
- `leaderElection.enabled`: true
- `leaderElection.leaseDuration`: 60s
- `leaderElection.renewDeadline`: 15s
- `leaderElection.retryPeriod`: 5s

See [High Availability](./operations/high-availability.md) for leader election details.

### logging

Log level configuration.

```yaml
logging:
  verbose: 1  # 0=WARNING, 1=INFO, 2=DEBUG
```

### dataplane

Dataplane API connection and deployment settings.

```yaml
dataplane:
  port: 5555  # Dataplane API port
  minDeploymentInterval: 2s  # Rate limiting
  driftPreventionInterval: 60s  # Periodic sync
  mapsDir: /etc/haproxy/maps
  sslCertsDir: /etc/haproxy/ssl
  generalStorageDir: /etc/haproxy/general
  configFile: /etc/haproxy/haproxy.cfg
```

**Paths must match Dataplane API resource configuration.**

### watchedResourcesIgnoreFields

JSONPath expressions for fields to remove from all watched resources.

```yaml
watchedResourcesIgnoreFields:
  - metadata.managedFields
  - metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]
```

Reduces memory usage by filtering unnecessary data.

### watchedResources (required)

Defines which Kubernetes resources to watch.

```yaml
watchedResources:
  ingresses:
    apiVersion: networking.k8s.io/v1
    resources: ingresses
    enableValidationWebhook: true  # Optional
    indexBy:
      - metadata.namespace
      - metadata.name
    labelSelector:  # Optional
      app: myapp
    namespace: production  # Optional, restricts to single namespace
    store: full  # or "on-demand" for cached store
```

See [Watching Resources](./watching-resources.md) for detailed configuration.

### templateSnippets

Reusable template fragments.

```yaml
templateSnippets:
  backend-name: |
    ing_{{ ingress.metadata.namespace }}_{{ ingress.metadata.name }}
```

Include in templates: `{% include "backend-name" %}`

### maps

HAProxy map file templates.

```yaml
maps:
  host.map:
    template: |
      {% for ingress in resources.ingresses.List() %}
      {{ rule.host }} {{ ingress.metadata.name }}_backend
      {% endfor %}
```

Reference in config: `{{ pathResolver.GetPath("host.map", "map") }}`

### files

General auxiliary files (error pages, etc.).

```yaml
files:
  error_503:
    path: /etc/haproxy/errors/503.http
    template: |
      HTTP/1.1 503 Service Unavailable
      <html><body><h1>503</h1></body></html>
```

Reference in config: `errorfile 503 {{ pathResolver.GetPath("error_503", "file") }}`

### sslCertificates

SSL certificate templates.

```yaml
sslCertificates:
  example-com:
    template: |
      {% set secret = resources.secrets.GetSingle("default", "tls-cert") %}
      {{ secret.data['tls.crt'] | b64decode }}
      {{ secret.data['tls.key'] | b64decode }}
```

Reference in config: `bind :443 ssl crt {{ pathResolver.GetPath("example-com", "cert") }}`

### haproxyConfig (required)

Main HAProxy configuration template.

```yaml
haproxyConfig:
  template: |
    global
        daemon
        maxconn 4096

    defaults
        mode http
        timeout connect 5s

    frontend http
        bind *:80
        use_backend %[req.hdr(host),map({{ pathResolver.GetPath("host.map", "map") }})]
```

See [Templating Guide](./templating.md) for syntax and filters.

### templatingSettings

Template rendering configuration and custom variables.

```yaml
templatingSettings:
  extraContext:
    debug:
      enabled: true
      verboseHeaders: false
    environment: production
    featureFlags:
      rateLimiting: true
      caching: false
    customTimeout: 30
```

**Fields:**

| Field          | Type                   | Required | Description                                                              |
|----------------|------------------------|----------|--------------------------------------------------------------------------|
| `extraContext` | map[string]interface{} | No       | Custom variables merged into template context (available in all templates) |

**Usage in templates:**

Custom variables are merged at the top level of the template context. Access them directly:

```jinja2
{% if debug.enabled %}
  # Debug-specific configuration
  http-response set-header X-HAProxy-Backend %[be_name]
{% endif %}

{% if environment == "production" %}
  timeout client {{ customTimeout }}s
{% else %}
  timeout client 300s
{% endif %}
```

The `extraContext` field accepts any valid JSON value (strings, numbers, booleans, objects, arrays). This allows you to configure template behavior for different environments, enable feature flags, or inject custom metadata without modifying controller code.

See [Templating Guide - Custom Template Variables](./templating.md#custom-template-variables) for detailed examples and use cases.

### validationTests

Embedded validation tests (optional, used by webhook and CLI).

```yaml
validationTests:
  - name: test_basic_ingress
    description: Validate basic ingress routing
    fixtures:
      ingresses:
        - apiVersion: networking.k8s.io/v1
          kind: Ingress
          metadata:
            name: test-ingress
            namespace: default
          spec:
            rules:
              - host: example.com
                http:
                  paths:
                    - path: /
                      pathType: Prefix
                      backend:
                        service:
                          name: test-service
                          port:
                            number: 80
    assertions:
      - type: haproxy_valid
        description: Generated config must be valid

      - type: contains
        target: haproxy_config
        pattern: "example.com"
        description: Config must include host
```

See [CRD Validation Design](./development/crd-validation-design.md) for test framework details.

## Status Subresource

The controller updates the status field with validation results:

```yaml
status:
  observedGeneration: 1
  lastValidated: "2025-01-27T10:00:00Z"
  validationStatus: Valid  # Valid, Invalid, or Unknown
  validationMessage: "All validation tests passed"
  conditions:
    - type: Ready
      status: "True"
      reason: ValidationSucceeded
      lastTransitionTime: "2025-01-27T10:00:00Z"
```

## Command-Line Management

### View Configurations

```bash
# List all configs
kubectl get haproxytemplateconfig
kubectl get htplcfg  # Short name

# View specific config
kubectl get htplcfg haproxy-config -o yaml

# Watch for changes
kubectl get htplcfg -w
```

### Validate Before Applying

```bash
# Validate local file
controller validate --config haproxy-config.yaml

# Validate deployed config
controller validate --name haproxy-config --namespace default
```

### Edit Configuration

```bash
# Interactive edit
kubectl edit htplcfg haproxy-config

# Apply from file
kubectl apply -f haproxy-config.yaml

# Patch specific fields
kubectl patch htplcfg haproxy-config --type=merge -p '
spec:
  logging:
    verbose: 2
'
```

## Migration from ConfigMap

If upgrading from ConfigMap-based configuration:

**Old (ConfigMap):**
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: haproxy-config
data:
  config: |
    pod_selector:
      match_labels:
        app: haproxy
    # ... rest of YAML config
```

**New (CRD):**
```yaml
apiVersion: haproxy-template-ic.github.io/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: haproxy-config
spec:
  credentialsSecretRef:
    name: haproxy-credentials
  podSelector:
    matchLabels:
      app: haproxy
  # ... rest of configuration as spec fields
```

**Key differences:**
- Configuration is now strongly typed with validation
- Credentials moved to separate Secret reference
- Field names use camelCase (e.g., `podSelector` vs `pod_selector`)
- Validation tests can be embedded inline

## Validation

The CRD includes OpenAPI schema validation that checks:
- Required fields are present
- Field types are correct
- String lengths meet minimum/maximum requirements
- Integer values are within valid ranges
- Enum values match allowed options

Additional validation occurs when:
1. **Admission webhook** - Runs embedded validation tests (if webhook enabled)
2. **Controller startup** - Validates configuration before starting
3. **CLI command** - `controller validate` runs tests locally

## Best Practices

**Security:**
- Never include credentials in the CRD - use credentialsSecretRef
- Restrict RBAC access to HAProxyTemplateConfig resources
- Use separate namespaces for controller and configs in multi-tenant scenarios

**Organization:**
- One HAProxyTemplateConfig per controller instance
- Use descriptive names that indicate purpose or environment
- Label configs for filtering: `environment: production`

**Testing:**
- Include validation tests for critical routing paths
- Test with realistic fixtures, not toy examples
- Run `controller validate` before applying changes
- Use CI/CD to validate configs in pull requests

**Templates:**
- Use templateSnippets for reusable logic
- Keep haproxyConfig template focused on structure
- Comment complex template logic
- Test templates with various resource combinations

## See Also

- [Configuration Reference](./configuration.md) - Detailed field descriptions
- [Templating Guide](./templating.md) - Template syntax and examples
- [Watching Resources](./watching-resources.md) - Resource watching configuration
- [CRD Validation Design](./development/crd-validation-design.md) - Validation framework
- [Getting Started](./getting-started.md) - Installation and basic usage
