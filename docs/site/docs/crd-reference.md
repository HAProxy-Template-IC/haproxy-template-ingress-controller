# HAProxyTemplateConfig CRD reference

## Overview

One `HAProxyTemplateConfig` resource defines everything HAPTIC does: what it watches, what it renders, and the tests that gate deployment. It provides schema validation, status conditions, and embedded testing capabilities.

**API Group**: `haproxy-haptic.org`
**API Version**: `v1alpha1`
**Kind**: `HAProxyTemplateConfig`
**Short Names**: `htplcfg`, `haptpl`

The schema is deliberately resource-agnostic — you template whatever you watch, so it works on a bespoke CRD exactly as it does on Ingress.

▶ [Open the custom-CRD example in the playground](/playground/?preset=crd){target=_blank} — HAPTIC templating any resource, not just Ingress.

## Basic example

Run the whole custom resource in your browser to watch it render to a minimal haproxy.cfg.

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-controls="tabs" data-title="A minimal HAProxyTemplateConfig" data-height="440">

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

</div>

## Spec fields

The four required fields come first (`credentialsSecretRef`, `podSelector`, `watchedResources`, `haproxyConfig`), followed by the template entries and the operational tuning fields.

### `credentialsSecretRef`

References a Secret containing Dataplane API credentials. **Required.**

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `name` | string | Yes | — |
| `namespace` | string | No | The config's namespace |

```yaml
credentialsSecretRef:
  name: haproxy-credentials
```

The Secret must contain the keys `dataplane_username` and `dataplane_password`. Credentials are used only for the production Dataplane API; config validation runs locally against the `haproxy` binary and needs no credentials. See [Security — Credentials](./operations/security.md#credentials) for rotation and GitOps caveats.

### `podSelector`

Labels that identify which HAProxy pods the controller manages. **Required.**

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `matchLabels` | `map[string]string` | Yes (at least one label) | — |

```yaml
podSelector:
  matchLabels:
    app.kubernetes.io/component: loadbalancer
```

The Helm chart ships `app.kubernetes.io/component: loadbalancer` (plus dynamically set `app.kubernetes.io/name` / `app.kubernetes.io/instance`); use any labels your HAProxy pods actually carry. See [HAProxy Deployment — Pod Requirements](./haproxy-deployment.md#haproxy-pod-requirements) for what discovered pods must provide.

### `watchedResources`

Defines which Kubernetes resources to watch. Each map key is an arbitrary name that appears in templates as `resources.<key>`. **Required** (at least one entry).

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `apiVersion` | string | Exactly one of `apiVersion` / `apiVersions` | — |
| `apiVersions` | `[]string` | Exactly one of `apiVersion` / `apiVersions` | — |
| `optional` | bool | No | `false` |
| `resources` | string | Yes | — |
| `indexBy` | `[]string` | No | — |
| `labelSelector` | string | No | `""` (equality-only, `"k=v[,k=v]"`; set-based syntax not supported) |
| `fieldSelector` | string | No | `""` (client-side JSONPath equality, `"field.path=value"`; matches any field) |
| `store` | string (`full` / `on-demand`) | No | `full` |
| `enableValidationWebhook` | bool | No | `false` |
| `debounceInterval` | string | No | `""` — empty / invalid uses the `2s` default; an explicit `"0"` disables debouncing |

```yaml
watchedResources:
  ingresses:
    apiVersion: networking.k8s.io/v1
    resources: ingresses
    indexBy:
      - metadata.namespace
      - metadata.name
```

Instead of a single `apiVersion`, an entry can declare an ordered
`apiVersions` candidate list together with `optional: true`. The controller
resolves the entry to the first candidate the cluster serves — at startup
and again whenever a matching CRD is installed, upgraded, or removed — so
your configuration works across CRD releases without redeployment:

```yaml
watchedResources:
  tcproutes:
    apiVersions:
      - gateway.networking.k8s.io/v1
      - gateway.networking.k8s.io/v1alpha2
    optional: true   # no served candidate → drop the watch, strip dependent features
    resources: tcproutes
    indexBy:
      - metadata.namespace
      - metadata.name
```

Rules:

- `apiVersion` and `apiVersions` are mutually exclusive; exactly one must be set.
- A **required** entry (no `optional`) whose candidates are all unserved fails
  startup with an error naming the resource — the controller retries and
  converges when the CRD appears.
- An **optional** entry whose candidates are all unserved is dropped, and every
  `templateSnippets` / `validationTests` entry whose `requires` names it gets
  stripped from the effective configuration.
- Templates read the resolved version via `resources.<name>.APIVersion()`.
- The current resolution is visible at `/debug/vars/effectiveConfigResolution`.

See [Watching Resources](./watching-resources.md) for the store types, indexing semantics, and selector behaviour.

### `watchedResourcesIgnoreFields`

JSONPath expressions for fields to remove from all watched resources before they're indexed, reducing memory usage.

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `watchedResourcesIgnoreFields` | `[]string` | No | — |

```yaml
watchedResourcesIgnoreFields:
  - metadata.managedFields
  - metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']
```

Applies uniformly to every watched resource; fields referenced by `indexBy` must not be trimmed. See [Watching Resources — Trimming Fields](./watching-resources.md#trimming-fields).

### `haproxyConfig`

The main HAProxy configuration template. **Required.**

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `template` | string | Yes | — |
| `postProcessing` | `[]PostProcessor` | No | — (see [`postProcessing`](#postprocessing-all-template-entries)) |

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

See the [Templating Guide](./templating.md) for syntax, loops, and helper functions.

### `templateSnippets`

Reusable template fragments, included in other templates via `{{ render "snippet-name" }}`.

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `template` | string | Yes | — |
| `requires` | `[]string` | No | — (names of `watchedResources` keys) |

```yaml
templateSnippets:
  backend-name:
    requires: [ingresses]
    template: |
      ing_{{ ingress.metadata.namespace }}_{{ ingress.metadata.name }}
```

`requires` entries must name `watchedResources` keys: when an optional watched
resource named there is unavailable, the snippet is stripped from the effective
configuration. A snippet that must survive stripping may reach a stripped
resource only through compile-safe seams — `render "..." default ""`,
`render_glob` extension points, or shared state — never a direct typed
`resources.<name>` reference. See [Templating — Template Snippets](./templating.md#template-snippets).

### `maps`

HAProxy map file templates. Each key is a map filename, referenced in config via `{{ pathResolver.GetPath("host.map", "map") }}`.

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `template` | string | Yes | — |
| `postProcessing` | `[]PostProcessor` | No | — (see [`postProcessing`](#postprocessing-all-template-entries)) |

```yaml
maps:
  host.map:
    template: |
      {% for _, ingress := range resources.ingresses.List() %}
      {% for _, rule := range ingress.spec.rules %}
      {{ rule.host }} {{ ingress.metadata.name }}_backend
      {% end %}
      {% end %}
```

See [Templating — Map Files](./templating.md#map-files).

### `files`

General auxiliary file templates (error pages, etc.). Each key is a filename, referenced in config via `{{ pathResolver.GetPath("503.http", "file") }}`.

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `template` | string | Yes | — |
| `postProcessing` | `[]PostProcessor` | No | — (see [`postProcessing`](#postprocessing-all-template-entries)) |

```yaml
files:
  503.http:
    template: |
      HTTP/1.1 503 Service Unavailable
      <html><body><h1>503</h1></body></html>
```

See [Templating — General Files](./templating.md#general-files).

### `sslCertificates`

SSL certificate templates, typically assembled from watched Secrets. Each key is a certificate name, referenced in config via `{{ pathResolver.GetPath("example-com", "cert") }}`.

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `template` | string | Yes | — |
| `postProcessing` | `[]PostProcessor` | No | — (see [`postProcessing`](#postprocessing-all-template-entries)) |

```yaml
sslCertificates:
  example-com:
    template: |
      {% var secret = resources.secrets.GetSingle("default", "tls-cert") %}
      {{ b64decode(secret.data["tls.crt"]) }}
      {{ b64decode(secret.data["tls.key"]) }}
```

See [Templating — SSL Certificates](./templating.md#ssl-certificates).

### `k8sResources`

Templates that emit Kubernetes resources for the controller to apply via Server-Side Apply. Each entry's rendered output is parsed as one or more YAML documents (multi-doc supported via `---` separators); each document must declare `apiVersion`, `kind`, and `metadata.name` (plus `metadata.namespace` for namespaced kinds).

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `template` | string | Yes | — |
| `postProcessing` | `[]PostProcessor` | No | — (see [`postProcessing`](#postprocessing-all-template-entries)) |

The controller injects an `OwnerReference` to the `HAProxyTemplateConfig` CR (`controller=true`, `blockOwnerDeletion=true`) on every full-ownership applied resource, so cascade-delete (for example `helm uninstall`) GCs the rendered objects. Resources that disappear from the rendered set across reconciliations are pruned. The applier respects the `haproxy-haptic.org/ownership: partial` annotation: when present on a rendered resource the Server-Side Apply (SSA) payload omits the `managed-by` label **and** the `OwnerReference`, the resource is excluded from the orphan-cleanup set, and the annotation itself is stripped before apply — useful for jointly owned objects on which HAPTIC only contributes a subset of fields (Server-Side Apply's per-list-map-entry merge keeps each owner's contribution intact).

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

### `postProcessing` (all template entries)

Every template-bearing entry — `haproxyConfig` and each entry under `maps`, `files`, `sslCertificates`, and `k8sResources` — accepts an optional `postProcessing` list that transforms the rendered output before it's used. Processors run sequentially.

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `type` | string (`regex_replace` / `template`) | Yes | — |
| `params` | `map[string]string` | Yes | — |

Params per type:

| Type | Params |
|------|--------|
| `regex_replace` | `pattern` (regular expression), `replace` (replacement string) — applied line by line |
| `template` | `source` (a Scriggo template; the rendered output is available as the `input` variable) |

```yaml
haproxyConfig:
  template: |
    ...
  postProcessing:
    - type: regex_replace
      params:
        pattern: '\n{3,}'
        replace: "\n\n"
    - type: template
      params:
        source: "{{ replace(input, \"__REGION__\", \"eu-west-1\") }}"
```

See [Templating — Post-Processing](./templating.md#post-processing) for a runnable example.

### `templatingSettings`

Template rendering configuration and custom variables.

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `extraContext` | object (any JSON value) | No | — |
| `engine` | string (`scriggo`) | No | `scriggo` (the only valid value) |

```yaml
templatingSettings:
  extraContext:
    environment: production
    featureFlags:
      rateLimiting: true
```

Custom variables are exposed to templates as the `extraContext` map. Read a key with `extraContext["key"]`, or `extraContext | dig("key") | fallback(default)` when it may be unset:

```go
{% if extraContext["environment"] == "production" %}
  timeout client {{ extraContext | dig("customTimeout") | fallback("300") }}s
{% end %}
```

See [Templating — Custom Template Variables](./templating.md#custom-template-variables) for detailed examples.

### `validationTests`

Embedded validation tests (optional; run by the admission webhook, the `validate` CLI, and the controller itself on config load).

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `description` | string | No | — |
| `fixtures` | `map[string][]object` | Yes | — (keys must name `watchedResources` entries) |
| `assertions` | `[]Assertion` | Yes | — |
| `httpResources` | `[]object` | No | — (mocked responses for `http.Fetch()` calls) |
| `currentConfig` | string | No | — (simulated live HAProxy config for runtime-context assertions) |
| `extraContext` | object | No | — (per-test overrides of `templatingSettings.extraContext`) |
| `minHAProxyVersion` | string | No | — (skip the test on older HAProxy) |
| `requires` | `[]string` | No | — (strip the test when a named optional watched resource is unavailable) |
| `requiresFields` | `[]string` | No | — (strip the test when a schema field path is absent) |

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

See [Validation Tests](./validation-tests.md) for the full test-framework reference — fixtures, assertion types, CLI usage, and the [`requires` / `requiresFields` stripping semantics](./validation-tests.md#conditional-tests-requires-and-requiresfields) — and [CRD & Validation Design](./development/crd-validation-design.md) for the design rationale.

### `validators`

Pluggable validator sidecars consulted by the admission webhook (optional).

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `name` | string | Yes | — (RFC 1123 label, unique across the array) |
| `socketPath` | string | Yes | — (absolute path to a Unix domain socket inside the controller pod) |
| `files` | `[]string` | Yes (at least one) | — (glob patterns matched against rendered file paths) |
| `timeoutMs` | integer | No | `5000` (range 1–60000) |
| `maxConnections` | integer | No | `4` (range 1–32) |

```yaml
validators:
  - name: spoa-hub
    socketPath: /var/run/haptic-validators/spoa-hub.sock
    files:
      - "/etc/haproxy-spoa-hub/*.toml"
```

See [Pluggable Validators](./operations/pluggable-validators.md) for the wire protocol, sidecar wiring, and routing examples.

### `migrationCoverage`

Per-migration-source annotation coverage declarations (optional).

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `source` | string | Yes | — (source controller name, unique) |
| `detect` | object | No | — (`ingressClasses`, `annotationPrefixes`) |
| `annotations` | `map[string]object` | No | — (source annotation keys → migration classification) |

The controller treats this as opaque data — it's contributed by the template libraries and merged by the Helm chart; no entry influences rendering or reconciliation. It powers the migration report in the [playground](/playground/), which reads it from a build-time chart render rather than from a cluster. Because nothing in a cluster reads it, the chart doesn't emit it by default; set `controller.config.includeMigrationCoverage=true` if you want it stored. See [Migrating](./migrating.md).

### `controller`

Controller-level settings for leader election and config publishing.

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `leaderElection.enabled` | bool | No | `true` |
| `leaderElection.leaseName` | string | No | `""` → `haptic-leader` (the Helm chart sets the release `fullname`) |
| `leaderElection.leaseDuration` | string | No | `30s` |
| `leaderElection.renewDeadline` | string | No | `20s` |
| `leaderElection.retryPeriod` | string | No | `5s` |

```yaml
controller:
  leaderElection:
    enabled: true
    leaseDuration: 30s
    renewDeadline: 20s
    retryPeriod: 5s
```

!!! note
    There is no reconciler-level debounce knob. The Reconciler fires immediately on every resource/HTTP event; batching is per-watcher (`spec.watchedResources.<name>.debounceInterval`, default `2s`) and reload throttling is the deployer's `spec.dataplane.minDeploymentInterval`.

!!! note
    These are the controller's built-in defaults from `pkg/core/config/defaults.go` — deliberately 2x the values `kube-controller-manager` and `kube-scheduler` ship with (`15s`/`10s`/`2s`), so the leader rides out multi-second API-server or CPU starvation stalls without losing the lease. The Helm chart sets the same values; setting any of these fields on the CRD only matters if you need different values (for example faster crash-failover, or clusters with significant clock skew).

See [High Availability](./operations/high-availability.md) for leader election details.

#### `configPublishing`

Controls how rendered configurations are stored in `HAProxyCfg` CRD resources.

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `compressionThreshold` | int64 | No | `1048576` (1 MiB). A value of `0` is treated as unset — the 1 MiB default applies (compression can't currently be disabled) |

```yaml
controller:
  configPublishing:
    compressionThreshold: 1048576
```

When the rendered configuration exceeds the threshold, it's compressed with zstd and base64-encoded; the `HAProxyCfg` resource stores it with `spec.compressed: true`, reducing etcd storage and speeding up watch events for large configurations. To read a published config back in plaintext, use `haptic-controller config view` — see [Debugging](./operations/debugging.md#common-recipes).

### `logging`

Log level configuration.

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `level` | string (`TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR`; case-insensitive) | No | `""` → the `LOG_LEVEL` environment variable → `INFO` |

```yaml
logging:
  level: DEBUG
```

### `dataplane`

Dataplane API connection, deployment, and validation settings.

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `port` | integer (1–65535) | No | `5555` |
| `minDeploymentInterval` | string | No | `2s` (the Helm chart ships `5s`) |
| `driftPreventionInterval` | string | No | `60s` |
| `deploymentTimeout` | string | No | `30s` |
| `configPublishInterval` | string | No | `10s` |
| `reloadVerificationTimeout` | string | No | `10s` |
| `syncTimeout` | string | No | `2m` |
| `mapsDir` | string | No | `/etc/haproxy/maps` |
| `sslCertsDir` | string | No | `/etc/haproxy/certs` (the Helm chart sets `/etc/haproxy/ssl`) |
| `generalStorageDir` | string | No | `/etc/haproxy/general` |
| `configFile` | string | No | `/etc/haproxy/haproxy.cfg` |

```yaml
dataplane:
  port: 5555
  minDeploymentInterval: 2s
  driftPreventionInterval: 60s
```

The three `*Dir` paths are used by the controller's local `haproxy -c` validation step as well as for deployment — they must match the paths the Dataplane API server is configured to manage (`configFile` is used only by local validation; the Dataplane API manages its own config-file path). The Helm chart keeps them in sync by deriving both sides from a single set of chart values. For tuning guidance on the interval fields, see [Performance — Deployment Pacing](./operations/performance.md#deployment-pacing).

## Status Subresource

The controller updates the status field with validation results:

| Field | Type | Description |
|-------|------|-------------|
| `observedGeneration` | int64 | The `.metadata.generation` the status reflects |
| `lastValidated` | timestamp | Last successful validation |
| `validationStatus` | string | `Valid`, `Invalid`, or `Unknown` — the printer column shown by `kubectl get htplcfg` |
| `validationMessage` | string | Human-readable summary |
| `validationErrors` | `[]string` | Populated when `Invalid`; each entry names the template and error context |
| `conditions` | `[]Condition` | Standard `metav1.Condition` list (for example `Ready`) |

```yaml
status:
  observedGeneration: 1
  lastValidated: "2025-01-27T10:00:00Z"
  validationStatus: Valid
  validationMessage: "All validation tests passed"
  validationErrors:
    - "haproxy.cfg: parse error at line 12: …"   # only when Invalid
  conditions:
    - type: Ready
      status: "True"
      reason: ValidationSucceeded
      lastTransitionTime: "2025-01-27T10:00:00Z"
```

## Command-line management

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

A Helm install creates several of these: one per enabled template library, named
`<configName>-<library>`, plus `<configName>` for your own `controller.config`.
The controller merges them in the order listed in the `CRD_NAME` environment
variable on the controller Deployment, and later entries win — your own config is
last, so it overrides every library.

Only `<configName>` is yours to edit. The library objects are chart output and
`helm upgrade` overwrites them; to change what a library emits, override the
snippet by name under `controller.config.templateSnippets` instead.

To see what the controller actually assembles from the whole set:

```bash
haptic-controller config view --input -n haptic
```

Validation status is reported on `<configName>` only — it represents the merged
set. Offline, `haptic-controller validate -f <file>` accepts the flag repeatedly
and accepts multi-document files, so you can validate a whole rendered set:

```bash
helm template charts/haptic | yq 'select(.kind == "HAProxyTemplateConfig")' > all.yaml
haptic-controller validate -f all.yaml
haptic-controller validate -f all.yaml --dump-merged   # print the merged spec
```

Applying a single hand-written `HAProxyTemplateConfig` — without Helm — still
works exactly as before: point `--crd-name` at it and it's the whole config.

### Validate before applying

```bash
# Validate local file
haptic-controller validate -f haproxy-config.yaml

# Validate deployed config
kubectl get htplcfg -n haptic haproxy-config -o yaml > /tmp/haproxy-config.yaml
haptic-controller validate -f /tmp/haproxy-config.yaml
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

## Best practices

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

- Use `templateSnippets` for reusable logic
- Keep `haproxyConfig` template focused on structure
- Comment complex template logic
- Test templates with various resource combinations

## See also

- [Templating Guide](./templating.md) — template syntax, loops, status patches
- [Template Reference](./template-reference.md) — context variables, functions, `pathResolver`
- [Watching Resources](./watching-resources.md) — store types, indexing, selectors
- [Validation Tests](./validation-tests.md) — writing and running embedded tests
- [CRD & Validation Design](./development/crd-validation-design.md) — rationale behind the CRD shape and validation layers
- [Getting Started](./getting-started.md) — installation walkthrough
