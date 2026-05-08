# HAProxyTemplateConfig CRD Reference

## Overview

The `HAProxyTemplateConfig` custom resource configures the HAProxy Template Ingress Controller. It provides schema validation, status conditions, and embedded testing capabilities.

**API Group**: `haproxy-haptic.org`
**API Version**: `v1alpha1`
**Kind**: `HAProxyTemplateConfig`
**Short Names**: `htplcfg`, `haptpl`

## Basic Example

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: haproxy-config
  namespace: default
spec:
  credentialsSecretRef:
    name: haproxy-credentials

  podSelector:
    matchLabels:
      app.kubernetes.io/component: loadbalancer

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

- `dataplane_username` - Dataplane API username
- `dataplane_password` - Dataplane API password

The same credentials are used for both production and validation Dataplane API instances; the controller does not need separate validation credentials.

### podSelector (required)

Labels that identify which HAProxy pods the controller should manage. The Helm chart ships `app.kubernetes.io/component: loadbalancer` (plus dynamically-set `app.kubernetes.io/name` / `app.kubernetes.io/instance`); use any labels your HAProxy pods actually carry.

```yaml
podSelector:
  matchLabels:
    app.kubernetes.io/component: loadbalancer
```

At least one label must be specified.

### controller

Controller-level settings for ports and leader election.

```yaml
controller:
  leaderElection:
    enabled: true
    leaseName: ""        # empty = defaults to the CRD name; Helm overrides with the release fullname
    leaseDuration: 15s   # default (DefaultLeaderElectionLeaseDuration)
    renewDeadline: 10s   # default (DefaultLeaderElectionRenewDeadline)
    retryPeriod: 2s      # default (DefaultLeaderElectionRetryPeriod)
  reconciliationDebounceInterval: 1s  # default; refractory window between resource changes and a render+deploy cycle
```

!!! note
    These are the controller's built-in defaults from `pkg/core/config/defaults.go` — the same values `kube-controller-manager` and `kube-scheduler` ship with. The Helm chart does not override them; setting any of these fields on the CRD only matters if you need different values (e.g. for clusters with significant clock skew or that need slower failover).

See [High Availability](./operations/high-availability.md) for leader election details.

#### configPublishing

Controls how rendered configurations are stored in `HAProxyCfg` CRD resources.

```yaml
controller:
  configPublishing:
    compressionThreshold: 1048576  # 1 MiB (default)
```

| Field                  | Type  | Default   | Description                                                                      |
|------------------------|-------|-----------|----------------------------------------------------------------------------------|
| `compressionThreshold` | int64 | 1048576   | Compress content when size exceeds this threshold (bytes). Set to 0 to disable |

**How compression works:**

- When HAProxy configuration exceeds the threshold, it's compressed using zstd and base64-encoded
- The `HAProxyCfg` resource stores compressed content with `spec.compressed: true`
- Reduces etcd storage and speeds up watch events for large configurations

**Fetching decompressed content:**

```bash
# View HAProxyCfg resources
kubectl get haproxycfg -n haptic

# Fetch and decompress content (requires zstd)
kubectl get haproxycfg <name> -n haptic -o jsonpath='{.spec.content}' | base64 -d | zstd -d

# If not compressed (spec.compressed is false), content is plain text
kubectl get haproxycfg <name> -n haptic -o jsonpath='{.spec.content}'
```

### logging

Log level configuration.

```yaml
logging:
  level: DEBUG  # TRACE, DEBUG, INFO, WARN, ERROR (case-insensitive)
```

If not set (empty string), the controller uses the `LOG_LEVEL` environment variable. If neither is set, defaults to INFO.

### dataplane

Dataplane API connection, deployment, and validation settings.

```yaml
dataplane:
  port: 5555                         # Dataplane API port (default 5555)
  minDeploymentInterval: 2s          # Minimum gap between deployments (default 2s)
  driftPreventionInterval: 60s       # Periodic redeploy to correct drift (default 60s)
  deploymentTimeout: 30s             # Safety net for lost deployments (default 30s)
  configPublishInterval: 30s         # Throttle for HAProxyCfg CRD republishes (default 30s)
  reloadVerificationTimeout: 10s     # Wait for HAProxy to confirm graceful reload (default 10s)
  syncTimeout: 2m                    # Per-endpoint sync timeout (default 2m)
  syncMaxRetries: 3                  # Retries on HTTP 409 transaction conflicts; 0 disables retries (default 3)
  maxParallel: 0                     # Concurrent Dataplane ops; 0 = unlimited (not recommended for large configs)
  rawPushThreshold: 100              # Switch to raw config push when change count exceeds this (default 100)
  mapsDir: /etc/haproxy/maps         # Used for both validation and deployment
  sslCertsDir: /etc/haproxy/ssl
  generalStorageDir: /etc/haproxy/general
  configFile: /etc/haproxy/haproxy.cfg
```

The four `*Dir` / `configFile` paths are used by the controller's local `haproxy -c` validation step as well as for deployment — they must match the paths the Dataplane API server is configured to manage. The Helm chart keeps them in sync by deriving both sides from a single set of chart values.

### watchedResourcesIgnoreFields

JSONPath expressions for fields to remove from all watched resources.

```yaml
watchedResourcesIgnoreFields:
  - metadata.managedFields
  - metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']
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
    labelSelector: "app=myapp"  # Optional, equality-only ("k=v[,k=v]"); set-based syntax not supported
    store: full  # or "on-demand" for cached store
    debounceInterval: ""  # Optional Go duration string; empty / invalid uses the 1s default
```

See [Watching Resources](./watching-resources.md) for detailed configuration.

### templateSnippets

Reusable template fragments.

```yaml
templateSnippets:
  backend-name:
    template: |
      ing_{{ ingress.metadata.namespace }}_{{ ingress.metadata.name }}
```

Include in templates: `{{ render "backend-name" }}`

### maps

HAProxy map file templates.

```yaml
maps:
  host.map:
    template: |
      {% for _, ingress := range resources.ingresses.List() %}
      {{ rule.host }} {{ ingress.metadata.name }}_backend
      {% end %}
```

Reference in config: `{{ pathResolver.GetPath("host.map", "map") }}`

### files

General auxiliary files (error pages, etc.).

```yaml
files:
  503.http:
    template: |
      HTTP/1.1 503 Service Unavailable
      <html><body><h1>503</h1></body></html>
```

Reference in config: `errorfile 503 {{ pathResolver.GetPath("503.http", "file") }}`

### sslCertificates

SSL certificate templates.

```yaml
sslCertificates:
  example-com:
    template: |
      {% var secret = resources.secrets.GetSingle("default", "tls-cert") %}
      {{ b64decode(secret.data["tls.crt"]) }}
      {{ b64decode(secret.data["tls.key"]) }}
```

Reference in config: `bind :443 ssl crt {{ pathResolver.GetPath("example-com", "cert") }}`

### k8sResources

Templates that emit Kubernetes resources for the controller to apply via Server-Side Apply. Each entry's rendered output is parsed as one or more YAML documents (multi-doc supported via `---` separators); each document must declare `apiVersion`, `kind`, and `metadata.name` (plus `metadata.namespace` for namespaced kinds).

The controller injects an `OwnerReference` to the `HAProxyTemplateConfig` CR (`controller=true`, `blockOwnerDeletion=true`) on every applied resource, so cascade-delete (e.g. `helm uninstall`) GCs the rendered objects. Resources that disappear from the rendered set across reconciliations are pruned. The applier respects the `haproxy-haptic.org/ownership: partial` annotation: when present on a rendered resource the SSA payload omits the `managed-by` label, the resource is excluded from the orphan-cleanup set, and the annotation itself is stripped before apply — useful for jointly-owned objects on which haptic only contributes a subset of fields (Server-Side Apply's per-list-map-entry merge keeps each owner's contribution intact).

Templates have full access to the same engine context as `haproxyConfig` — `resources`, filters, `templateSnippets`, `fileRegistry`, `extraContext`, and the per-render `shared` cache — so a `k8sResources` template can render extension points (`render_glob` patterns) and read shared state populated by the main config template.

```yaml
k8sResources:
  edge-service:
    template: |
      apiVersion: v1
      kind: Service
      metadata:
        name: edge
        namespace: {{ extraContext["controllerNamespace"] }}
      spec:
        type: LoadBalancer
        selector:
          app.kubernetes.io/component: loadbalancer
        ports:
          - name: http
            port: 80
            targetPort: http
            protocol: TCP
      ---
      apiVersion: discovery.k8s.io/v1
      kind: EndpointSlice
      metadata:
        name: edge-default
        namespace: {{ extraContext["controllerNamespace"] }}
        labels:
          kubernetes.io/service-name: edge
      addressType: IPv4
      endpoints:
        - addresses: ["10.0.0.1"]
      ports:
        - name: http
          port: 80
          protocol: TCP
```

Use this when the resource shape derives from observed cluster state (Ingresses, Gateways, Endpoints, …); use the chart's own static `templates/*.yaml` for fixed install-time wiring (RBAC, the dataplane Service, etc.). The chart's `libraries/base.yaml` ships a canonical example: the `haproxy-service` entry that renders the user-facing HAProxy LoadBalancer Service from listener state.

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
| `extraContext` | `map[string]any` | No       | Custom variables; the whole map is exposed as `extraContext` and each top-level key is also injected as a bare variable in templates |

**Usage in templates:**

Custom variables are merged at the top level of the template context. Access them directly:

```go
{% if debug.enabled %}
  # Debug-specific configuration
  http-response set-header X-HAProxy-Backend %[be_name]
{% end %}

{% if environment == "production" %}
  timeout client {{ customTimeout }}s
{% else %}
  timeout client 300s
{% end %}
```

The `extraContext` field accepts any valid JSON value (strings, numbers, booleans, objects, arrays). This allows you to configure template behavior for different environments, enable feature flags, or inject custom metadata without modifying controller code.

See [Templating Guide - Custom Template Variables](./templating.md#custom-template-variables) for detailed examples and use cases.

### validationTests

Embedded validation tests (optional, used by webhook and CLI).

```yaml
validationTests:
  test-basic-ingress:
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
        target: haproxy.cfg
        pattern: "example.com"
        description: Config must include host
```

See [Validation Tests](./validation-tests.md) for the full test-framework reference (fixtures, assertion types, CLI usage) and [CRD & Validation Design](./development/crd-validation-design.md) for the design rationale.

## Status Subresource

The controller updates the status field with validation results. Real fields are documented in `pkg/apis/haproxytemplate/v1alpha1/types_config.go`:

```yaml
status:
  observedGeneration: 1                              # tracks .metadata.generation
  lastValidated: "2025-01-27T10:00:00Z"              # last successful validation timestamp
  validationStatus: Valid                            # Valid, Invalid, or Unknown
  validationMessage: "All validation tests passed"   # human-readable summary
  validationErrors:                                  # populated when Invalid; each entry names template + error context
    - "haproxy.cfg: parse error at line 12: …"
  conditions:
    - type: Ready
      status: "True"
      reason: ValidationSucceeded
      lastTransitionTime: "2025-01-27T10:00:00Z"
```

`validationStatus` is the printer column shown by `kubectl get htplcfg`.

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
haptic-controller validate -f haproxy-config.yaml

# Validate deployed config
kubectl get htplcfg -n haptic haptic-config -o yaml > /tmp/haptic-config.yaml
haptic-controller validate -f /tmp/haptic-config.yaml
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
    level: DEBUG
'
```

## Migration from ConfigMap

Earlier pre-release builds accepted configuration as a `ConfigMap` with snake_case field names. That path was removed before the first tagged release. If you're still on an unreleased build that ships the old format, the mapping is:

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
apiVersion: haproxy-haptic.org/v1alpha1
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
3. **CLI command** - `haptic-controller validate` runs tests locally

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
- Run `haptic-controller validate` before applying changes
- Use CI/CD to validate configs in pull requests

**Templates:**

- Use templateSnippets for reusable logic
- Keep haproxyConfig template focused on structure
- Comment complex template logic
- Test templates with various resource combinations

## See Also

- [Templating Guide](./templating.md) — template syntax, filters, context variables
- [Watching Resources](./watching-resources.md) — store types, indexing, selectors
- [Validation Tests](./validation-tests.md) — writing and running embedded tests
- [CRD & Validation Design](./development/crd-validation-design.md) — rationale behind the CRD shape and validation layers
- [Getting Started](./getting-started.md) — installation walkthrough
