# charts/ - Helm Chart Development

Development context for working with the HAProxy Template Ingress Controller Helm chart.

## Chart Architecture

### Library Merging System

The chart uses a library-based architecture where multiple YAML files are merged at Helm render time:

```
Merge Order (lowest to highest priority):
1. base.yaml          - Core HAProxy template and snippets
2. ingress.yaml       - Kubernetes Ingress support
3. gateway.yaml       - Gateway API support
4. haproxytech.yaml   - HAProxy annotation compatibility
5. values.yaml        - User configuration (highest priority)
```

**Merge Logic** (`templates/_helpers.tpl:69`):

```yaml
{{- define "haptic.mergeLibraries" -}}
{{- $merged := dict }}
# Load each library in order using mustMergeOverwrite
# Later libraries override earlier ones for the same keys
{{- $merged = mustMergeOverwrite $merged $baseLibrary }}
{{- $merged = mustMergeOverwrite $merged $ingressLibrary }}
# ... etc
{{- end }}
```

### Library Knowledge Hierarchy

Libraries form a dependency hierarchy - each library may only reference snippets and variables from libraries it "knows about":

```
Level 0: base.yaml
         │
         ├── Knows: nothing (completely resource-agnostic)
         │
Level 1: ssl.yaml, path-regex-last.yaml
         │
         ├── Know: base
         ├── Don't know: each other
         │
Level 2: ingress.yaml, gateway.yaml
         │
         ├── Know: base, ssl, path-regex-last
         ├── Don't know: each other
         │
Level 3: haproxy-ingress.yaml, haproxytech.yaml
         │
         ├── Know: all libraries above
         └── Don't know: each other
```

This hierarchy prevents circular dependencies and ensures predictable behavior during library merging. Violating the hierarchy (e.g., base.yaml referencing ingress-specific snippets) will cause runtime errors when that library is disabled.

### Library Structure

Each library file (`libraries/*.yaml`) contains:

```yaml
watchedResources:
  # Resources this library needs to watch
  ingresses:
    apiVersion: networking.k8s.io/v1
    resources: ingresses
    indexBy: ["metadata.namespace", "metadata.name"]

haproxyConfig:
  # ONLY base.yaml should define this
  # Other libraries will override it if they include this section!
  template: |
    # Full HAProxy configuration template

templateSnippets:
  # Reusable template snippets
  resource_ingress_backend-name:
    template: >-
      ing_{{ ingress.metadata.namespace }}_{{ ingress.metadata.name }}

validationTests:
  # Embedded validation tests for this library
  test-ingress-basic:
    description: Basic ingress routing
    fixtures: ...
    assertions: ...
```

### Plugin Pattern

Libraries use a **plugin pattern** where base.yaml defines extension points:

```yaml
# base.yaml
haproxyConfig:
  template: |
    frontend http-in
      # Extension point for routing backends
      {% include "resource_ingress_backends" %}
      {% include "resource_gateway_backends" %}
```

Libraries implement these extension points:

```yaml
# ingress.yaml
templateSnippets:
  resource_ingress_backends:
    template: |
      {%- for ingress in resources.ingresses.List() %}
      # Generate backends from ingress resources
      {%- endfor %}
```

**Critical Rule**: Libraries should ONLY provide `templateSnippets`, not override `haproxyConfig`. The base template calls your snippets via `{% include %}`.

**CRITICAL ARCHITECTURE RULE - base.yaml MUST Be Resource-Agnostic**:

**base.yaml MUST be completely resource-agnostic**. It must NOT access:

- `ingress.metadata.*`, `ingress.spec.*`
- `httproute.metadata.*`, `httproute.spec.*`
- `grpcroute.metadata.*`, `grpcroute.spec.*`
- Any other resource-specific fields or annotations

Resource-specific libraries (ingress.yaml, gateway.yaml, haproxytech.yaml) are responsible for:

1. Extracting annotations and resource-specific data
2. Performing resource-specific calculations
3. Setting generic context variables for base.yaml to consume

**Pattern**: Resource libraries extract data and set variables → base.yaml reads generic variables

**Example**:

```jinja2
{#- ingress.yaml or haproxytech.yaml (resource-specific) -#}
{%- set pod_maxconn = ingress.metadata.annotations["haproxy.org/pod-maxconn"] | default("") %}
{%- if pod_maxconn != "" %}
  {%- set pod_maxconn_value = calculate_per_pod_value(pod_maxconn) %}
{%- endif %}
{% include "util-backend-servers" %}

{#- base.yaml (resource-agnostic) -#}
{%- if pod_maxconn_value is defined %}
  server SRV_1 {{ endpoint.address }}:{{ endpoint.port }} maxconn {{ pod_maxconn_value }}
{%- endif %}
```

**Why This Matters**: This separation allows Gateway API and Ingress resources to coexist without base.yaml needing to know which resource type it's processing. Resource-specific logic stays in resource-specific libraries.

### Extension Point Reference

The base template uses `render_glob` to discover and render snippets from all libraries. Snippets are rendered in alphabetical order, so numeric prefixes control execution order.

| Pattern | Purpose | Contributing Libraries |
|---------|---------|----------------------|
| `features-*` | Feature registration (SSL, TLS certs) | gateway, haproxytech, ingress, ssl |
| `backends-*` | Backend definitions | gateway, ingress, ssl |
| `frontends-*` | Additional frontends (HTTPS, TCP) | ssl |
| `map-host-*` | Host map entries | gateway, ingress |
| `map-path-exact-*` | Exact path map entries | gateway, ingress |
| `map-path-prefix-*` | Prefix path map entries | gateway, ingress |
| `map-path-prefix-exact-*` | Prefix-exact map entries | gateway, ingress |
| `map-path-regex-*` | Regex path map entries | gateway, ingress, haproxy-ingress |
| `map-weighted-backend-*` | Weighted routing map | gateway |
| `frontend-matchers-advanced-*` | Advanced route matching (method, headers) | gateway |
| `frontend-filters-*` | Request/response filters | gateway, haproxytech |
| `backend-directives-*` | Backend configuration directives | haproxytech |
| `global-top-*` | Global sections (userlist, resolvers) | haproxytech |

**Extension Point Variable Passing:**

Extension points pass variables to child snippets via `inherit_context`. Understanding which variables are available is crucial for writing correct snippets.

| Extension Point | Available Variables | Passed From |
|-----------------|---------------------|-------------|
| `backend-directives-*` | `ingress`, `serverOpts`, `serviceName`, `port` | backends-500-ingress |
| `frontend-filters-*` | `ingress`, `rule`, `path` | ingress.yaml frontend loop |
| `features-*` | `globalFeatures` (as `gf`) | base.yaml features section |

**Example - backend-directives extension:**

```scriggo
{#- In backends-500-ingress (ingress.yaml) #}
{%- var serverOpts = map[string]any{"flags": []any{}} %}
{{- render_glob "backend-directives-*" inherit_context }}
{{ BackendServers(tostring(serviceName), 0, toint(port), serverOpts) }}

{#- In backend-directives-900-haproxytech (haproxytech.yaml) #}
{#- These variables are available via inherit_context: #}
{%- if ingress != nil %}
  {#- ingress: the current Ingress resource #}
  {#- serverOpts: map for accumulating server options #}
  {%- var snippet = ingress | dig("metadata", "annotations", "haproxy.org/backend-config-snippet") %}
{%- end %}
```

### Snippet Priority Numbering

Snippets use numeric prefixes (e.g., `backends-500-ingress`) to control execution order within `render_glob` patterns. Lower numbers execute first.

**Reserved ranges:**

| Range | Purpose | Examples |
|-------|---------|----------|
| 000-099 | Infrastructure/initialization | `features-050-ssl-initialization` |
| 100-199 | Feature registration, basic config | `features-100-gateway-tls`, `frontend-filters-100-haproxytech-basic-headers` |
| 200-299 | Access control, security | `frontend-filters-200-haproxytech-access-control` |
| 300-399 | CORS, header manipulation | `frontend-filters-300-haproxytech-cors` |
| 400-499 | Redirects, rewrites | `frontend-filters-400-haproxytech-ssl-redirect` |
| 500-599 | Core functionality | `backends-500-ingress`, `map-host-500-gateway` |
| 600-699 | Compatibility layers | `map-path-regex-600-haproxy-ingress` |
| 900-999 | Finalization, cleanup | `frontend-matchers-advanced-900-path-match` |

### Snippet Implementation Patterns

**Use macros (import pattern)** when:

- Snippet produces output that needs parameters
- Called multiple times with different inputs
- Output is inline within another template

```scriggo
{# Definition in util-backend-name-ingress #}
{% macro BackendNameIngress(ingress any, path any) string %}
  ...
{% end %}

{# Usage #}
{% import "util-backend-name-ingress" for BackendNameIngress %}
{{ BackendNameIngress(ingress, path) }}
```

**Use shared variables (render pattern)** when:

- Expensive computation should run once per render
- Result needed by multiple unrelated snippets
- Caching across template boundaries required

```scriggo
{# Definition in util-gateway-analysis #}
{%- if !has_cached("gateway_analysis") %}
  {%- shared["gateway_analysis"] = expensive_computation() %}
  {%- set_cached("gateway_analysis", true) %}
{%- end %}

{# Usage from any snippet #}
{{ render "util-gateway-analysis" }}
{%- var ga map[string]any = shared["gateway_analysis"] %}
```

## HAProxy File Path Requirements

**CRITICAL**: HAProxy **requires absolute paths** to locate auxiliary files (maps, error pages, SSL certificates).

### The Problem with Relative Paths

HAProxy does **NOT** work with relative file paths. When HAProxy validates or loads a configuration file, it resolves paths relative to its **working directory**, not relative to the configuration file location.

**Example of what DOESN'T work:**

```haproxy
errorfile 400 general/400.http          # WRONG - HAProxy can't find this
use_backend %[path,map(maps/path.map)]   # WRONG - HAProxy can't find this
```

When HAProxy runs `haproxy -c -f /tmp/haproxy.cfg`, it looks for `general/400.http` relative to its current working directory (likely `/`), not relative to `/tmp/`.

### The Correct Approach: Absolute Paths

HAProxy requires absolute paths:

**Production (in HAProxy Dataplane pods):**

```haproxy
errorfile 400 /etc/haproxy/general/400.http       # CORRECT
use_backend %[path,map(/etc/haproxy/maps/path.map)]  # CORRECT
```

**Validation (in controller temp directories):**

```haproxy
errorfile 400 /tmp/haproxy-validate-12345/general/400.http    # CORRECT
use_backend %[path,map(/tmp/haproxy-validate-12345/maps/path.map)]  # CORRECT
```

### Dual-PathResolver Pattern

The codebase implements a dual-PathResolver pattern to handle both production and validation:

**Production PathResolver** (Renderer component):

- Created once during component initialization
- Uses production paths: `/etc/haproxy/maps`, `/etc/haproxy/certs`, `/etc/haproxy/general`
- Files exist in HAProxy Dataplane containers
- See: `pkg/controller/renderer/component.go:113`

**Validation PathResolver** (Testrunner):

- Created per-test during validation
- Uses temp paths: `/tmp/haproxy-validate-12345/maps`, `/tmp/haproxy-validate-12345/certs`
- Files written to controller container's temp directories
- Isolated per test for parallel execution
- See: `pkg/controller/testrunner/runner.go:736`

### Why This Separation Matters

The controller container has `readOnlyRootFilesystem: true` for security. It cannot write to `/etc/haproxy/` during validation. Therefore:

1. **Production rendering** uses `/etc/haproxy/` paths (for deployment to HAProxy pods)
2. **Validation rendering** uses `/tmp/haproxy-validate-*/` paths (for controller validation)

Both use **absolute paths** because HAProxy requires them.

### Template Implementation

Templates use `pathResolver.GetPath()` to generate absolute paths:

```scriggo
{#- Template example -#}
errorfile 400 {{ pathResolver.GetPath("400.http", "file") }}

{#- Production output: -#}
errorfile 400 /etc/haproxy/general/400.http

{#- Validation output: -#}
errorfile 400 /tmp/haproxy-validate-12345/general/400.http
```

The PathResolver is passed via rendering context and resolves names to absolute paths based on the configured directories.

### Common Pitfall

**DO NOT** assume HAProxy works like most other tools that resolve paths relative to the config file location. This assumption has caused bugs multiple times. Always use absolute paths through PathResolver.

## Development Workflow

### Testing Library Changes

Since libraries are merged at Helm render time, you must test the **merged output**, not individual library files.

**Recommended: Use the Test Script**

The `scripts/test-templates.sh` script automates the correct workflow (helm template + yq + controller validate):

```bash
# Run all validation tests
./scripts/test-templates.sh

# Run specific test
./scripts/test-templates.sh --test test-httproute-method-matching

# Run test with debugging output
./scripts/test-templates.sh --test test-httproute-method-matching --dump-rendered --verbose

# Show all available tests
./scripts/test-templates.sh --output yaml | yq '.tests[].name'
```

**Why use the script?**

- Ensures you don't forget the helm template step
- Automatically includes `--api-versions` flag for Gateway API tests
- Handles error checking and temp file cleanup
- Provides helpful error messages

**Manual Workflow (Advanced)**

If you need custom Helm values or specific library combinations:

```bash
# 1. Render merged config with Helm and extract HAProxyTemplateConfig
helm template charts/haptic \
  --api-versions=gateway.networking.k8s.io/v1/GatewayClass \
  --set controller.templateLibraries.ingress.enabled=true \
  --set controller.templateLibraries.gateway.enabled=false \
  | yq 'select(.kind == "HAProxyTemplateConfig")' \
  > /tmp/merged-config.yaml

# 2. Validate merged configuration
make build
./bin/haptic-controller validate -f /tmp/merged-config.yaml

# 3. Run specific validation test
./bin/haptic-controller validate -f /tmp/merged-config.yaml \
  --test test-ingress-duplicate-backend-different-ports
```

**Why use `yq 'select(.kind == "HAProxyTemplateConfig")'`?**

`helm template` outputs **all** Kubernetes resources (Deployment, Service, ConfigMap, etc.). The `controller validate` command expects a single HAProxyTemplateConfig resource, so we filter for it using yq.

**IMPORTANT: Gateway API Tests**

Gateway API tests require the `--api-versions=gateway.networking.k8s.io/v1/GatewayClass` flag to simulate the presence of Gateway API CRDs. Without this flag, Helm's Capabilities check will skip merging the gateway library, and gateway validation tests will not be available.

The test script includes this flag automatically. If using the manual workflow, you MUST include it:

```bash
# Manual workflow - MUST include --api-versions flag
helm template charts/haptic \
  --api-versions=gateway.networking.k8s.io/v1/GatewayClass \
  | yq 'select(.kind == "HAProxyTemplateConfig")' \
  > /tmp/gateway-config.yaml
```

This flag is already used in CI (see `.gitlab-ci.yml`). The gateway library uses a Capabilities check (`templates/_helpers.tpl:86`) to only merge when Gateway API CRDs are detected.

### Testing Specific Libraries

Enable/disable libraries to test specific combinations:

```bash
# Test only ingress library (no gateway)
helm template charts/haptic \
  --set controller.templateLibraries.ingress.enabled=true \
  --set controller.templateLibraries.gateway.enabled=false \
  | yq 'select(.kind == "HAProxyTemplateConfig")' \
  > /tmp/ingress-only.yaml

# Test gateway library (no ingress)
helm template charts/haptic \
  --set controller.templateLibraries.ingress.enabled=false \
  --set controller.templateLibraries.gateway.enabled=true \
  | yq 'select(.kind == "HAProxyTemplateConfig")' \
  > /tmp/gateway-only.yaml

# Test with custom values
helm template charts/haptic \
  --values my-test-values.yaml \
  | yq 'select(.kind == "HAProxyTemplateConfig")' \
  > /tmp/custom-config.yaml
```

### Adding Validation Tests to Libraries

Libraries can include validation tests that are merged into the final config:

```yaml
# ingress.yaml
validationTests:
  test-ingress-basic:
    description: Basic ingress routing
    fixtures:
      services:
        - apiVersion: v1
          kind: Service
          metadata:
            name: my-service
            namespace: default
          spec:
            ports:
              - port: 80
      endpoints:
        - apiVersion: discovery.k8s.io/v1
          kind: EndpointSlice
          metadata:
            name: my-service-abc
            namespace: default
            labels:
              kubernetes.io/service-name: my-service
          endpoints:
            - addresses: ["10.0.0.1"]
          ports:
            - port: 8080
      ingresses:
        - apiVersion: networking.k8s.io/v1
          kind: Ingress
          metadata:
            name: my-ingress
            namespace: default
          spec:
            ingressClassName: haproxy
            rules:
              - host: example.com
                http:
                  paths:
                    - path: /
                      pathType: Prefix
                      backend:
                        service:
                          name: my-service
                          port:
                            number: 80
    assertions:
      - type: haproxy_valid
        description: HAProxy config must be valid

      - type: contains
        target: haproxy.cfg
        pattern: "backend ing_default_my-ingress_my-service_80"
        description: Must generate backend for ingress
```

**Test Execution:**

Tests run against the **merged configuration**, so they can validate cross-library interactions.

## Common Patterns

### Adding a New Resource Type

```yaml
# 1. Add to watchedResources
watchedResources:
  configmaps:
    apiVersion: v1
    resources: configmaps
    indexBy: ["metadata.namespace", "metadata.name"]

# 2. Create template snippets that use the resource
templateSnippets:
  resource_configmap_backends:
    template: |
      {%- for cm in resources.configmaps.List() %}
      # Process configmap
      {%- endfor %}
```

### Implementing Extension Points

If base.yaml defines an extension point like `{% include "resource_ingress_backends" %}`, implement it:

```yaml
templateSnippets:
  resource_ingress_backends:
    template: |
      {%- for ingress in resources.ingresses.List() %}
      backend {{ ingress.metadata.name }}
        # Backend configuration
      {%- endfor %}
```

### Annotation Template Documentation Standards

**Every annotation template MUST include comprehensive inline documentation** to prevent confusion about expected formats and behavior.

**Required Documentation Sections:**

```scriggo
{#-
  <Template Name>

  Documentation: <URL to official HAProxy Ingress or HAProxy docs>

  Annotations:
    - annotation.name: "<value-format>" (required/optional)
    - ...list all annotations this template uses...

  Resource Format (if template reads secrets, configmaps, etc.):
    Detailed explanation of expected structure, especially for base64-encoded data.

    IMPORTANT: Explicitly state format expectations (e.g., "hash only, NOT username:hash")

  Example:
    <Complete working example manifest>

  Generated HAProxy Config:
    <Show what HAProxy configuration this template produces>

  Notes:
    - Any gotchas, limitations, or special behaviors
    - Cross-references to related templates
-#}
```

**Real Example:**

See `libraries/haproxytech.yaml` lines 18-57 for the `top-level-annotation-haproxytech-auth` template which demonstrates proper documentation including:

- Link to HAProxy Ingress documentation
- List of all annotations
- Detailed secret format explanation with WARNING about htpasswd vs hash-only
- Example secret manifest
- Command to generate correct password hash
- Description of generated HAProxy config
- Deduplication behavior

**Why This Matters:**

Without inline documentation, developers must:

1. Search external documentation
2. Guess at format requirements
3. Potentially implement incorrect parsing logic

Proper documentation prevents bugs and makes templates self-documenting.

### Cross-Library Shared State (globalFeatures / gf)

Libraries communicate across boundaries using the `globalFeatures` map (commonly aliased as `gf`). This enables features like SSL to be configured in one library (ingress.yaml, gateway.yaml) and consumed by another (ssl.yaml).

**Pattern:**

```scriggo
{#- ssl.yaml: Initialize shared state during feature registration -#}
{%- if gf["sslPassthroughBackends"] == nil %}
  {%- gf["sslPassthroughBackends"] = []any{} %}
{%- end %}
{%- if gf["tlsCertificates"] == nil %}
  {%- gf["tlsCertificates"] = []any{} %}
{%- end %}

{#- ingress.yaml or gateway.yaml: Append data to shared state -#}
{%- var sslBackends []any = gf["sslPassthroughBackends"].([]any) %}
{%- gf["sslPassthroughBackends"] = append(sslBackends, backend) %}

{#- ssl.yaml: Consume shared state to generate output -#}
{%- var backends = gf["sslPassthroughBackends"] | fallback([]any{}) %}
{%- for _, backend := range backends.([]any) %}
  use_backend {{ backend["name"] }} if { req.ssl_sni -i {{ backend["sni"] }} }
{%- end %}
```

**Available Shared State Keys:**

| Key | Type | Purpose | Initialized By | Written By |
|-----|------|---------|----------------|------------|
| `sslPassthroughBackends` | `[]any` | SSL passthrough backend definitions | ssl.yaml | gateway.yaml, haproxytech.yaml |
| `tlsCertificates` | `[]any` | TLS certificate references for crt-list | ssl.yaml | gateway.yaml, ingress.yaml |

!!! warning "Map Key Consistency is Critical"
    All libraries **MUST** use the exact same map key names. The codebase uses **camelCase** for shared state keys. Using different key names (e.g., `tls_certificates` vs `tlsCertificates`) will cause silent failures where data written by one library is invisible to another.

**The `gf` Alias:**

`gf` is a shorthand alias for `globalFeatures`. Both refer to the same shared map. Use `gf` for brevity in templates:

```scriggo
{#- These are equivalent: #}
{%- var certs = globalFeatures["tlsCertificates"] %}
{%- var certs = gf["tlsCertificates"] %}
```

### Backend Deduplication

When multiple paths route to the same service+port, deduplicate backends:

```yaml
templateSnippets:
  resource_ingress_backends:
    template: |
      {#- Backend deduplication #}
      {% var seen = map[string]bool{} %}
      {%- for _, ingress := range resources.ingresses.List() %}
      {%- for _, path := range ingress.spec.paths %}
      {% var backend_key = path.service.name + "_" + path.service.port %}
      {%- if !seen[backend_key] %}
      {% seen[backend_key] = true %}

      backend {{ backend_key }}
        # Only generated once per unique service+port
      {%- end %}
      {%- end %}
      {%- end %}
```

### Server Options and Runtime API

To enable HAProxy runtime API updates without reloads, server options must be in `default-server`, not on individual server lines.

**Runtime-supported server fields (no reload):**

- `Weight`, `Address`, `Port` - Core properties
- `Maintenance` (`enabled`/`disabled`) - Server state
- `AgentCheck`, `AgentAddr`, `AgentSend`, `HealthCheckPort` - Agent checks

**Important:** The `disabled` and `enabled` options do NOT cause reloads. This is essential for the reserved slots pattern where unused slots are `disabled` and enabled at runtime when pods scale up.

**All other options trigger reloads** including: `check`, `proto`, `ssl`, `verify`, `ca-file`, `crt`

**Correct pattern in templates:**

```scriggo
backend ing_{{ ingress.metadata.name }}
    default-server check{{ BuildServerOptions(serverOpts) }}
    {{ BackendServers(serviceName, 10, port, nil, nil, backendKey) }}
```

The `BackendServers` macro generates server lines with only `address:port` plus `enabled` (for active servers) or `disabled` (for reserved slots), while all other options go in `default-server`.

**Example output:**

```haproxy
backend ing_default_my-ingress
    default-server check proto h2
    server SRV_1 10.0.0.1:8080 enabled
    server SRV_2 10.0.0.2:8080 enabled
    server SRV_3 127.0.0.1:1 disabled
```

**Why this matters:** When pods scale up/down, only the server's Address, Port, and enabled/disabled state change. If these are the only fields on server lines, the controller updates them via runtime API (no reload, no connection drops). If options like `check` are on server lines, any change requires a reload.

### Optimizing Expensive Computations with Utility Snippets

Libraries provide **utility snippets** that cache expensive computations. These snippets encapsulate all the caching complexity, making it easy to use cached data from any template.

**Problem**: Expensive computations run multiple times per render

Without caching, analyzing routes or scanning resources runs every time a snippet includes the computation:

```jinja2
{# snippet1.yaml - analyzes all HTTPRoutes #}
{%- for route in analyze_all_routes() %}  {# Expensive! #}

{# snippet2.yaml - analyzes all HTTPRoutes AGAIN #}
{%- for route in analyze_all_routes() %}  {# Runs again! #}

{# Result: expensive computation runs N times for N snippets #}
```

**Solution**: Use utility snippets for cached access

Utility snippets handle all caching internally. Just render them and use the result:

```go
{# Any snippet that needs route analysis #}
{{ render "util-gateway-analysis" }}

{# The gateway_analysis variable is now available with cached data #}
{%- for _, route := range gateway_analysis.sorted_routes %}
  ... process route ...
{%- end %}
```

**Available Utility Snippets:**

| Snippet | Library | Provides | Description |
|---------|---------|----------|-------------|
| `util-gateway-analysis` | gateway.yaml | `gateway_analysis` | HTTPRoute sorting, grouping, conflict detection |
| `util-gateway-ssl-passthrough` | gateway.yaml | `gateway_ssl_passthrough` | Gateway SSL passthrough backend scanning |
| `util-haproxytech-ssl-passthrough` | haproxytech.yaml | `haproxytech_ssl_passthrough` | Ingress SSL passthrough backend scanning |

**Example Usage:**

```go
{# Gateway route analysis - used by 7+ snippets #}
{{ render "util-gateway-analysis" }}
{%- for _, route := range gateway_analysis.sorted_routes %}
backend {{ route.metadata.namespace }}_{{ route.metadata.name }}
    {# ... backend config ... #}
{%- end %}

{# SSL passthrough backends - used by 2 snippets #}
{{ render "util-gateway-ssl-passthrough" }}
{%- for _, backend := range gateway_ssl_passthrough.backends %}
    use_backend {{ backend.name }} if { req.ssl_sni -i {{ backend.sni }} }
{%- end %}
```

**How It Works (Internal Architecture):**

The caching functions (`has_cached`, `get_cached`, `set_cached`) store values by key. When the same key is used across different template contexts, the cached data is retrieved:

1. First render: Checks if cached, runs expensive computation, stores result
2. Subsequent renders: Retrieves cached result
3. Works across different template contexts (haproxyConfig, map files, etc.)

```go
{# Inside util-gateway-analysis (simplified) #}
{% if !has_cached("gateway_analysis") %}
  {# Expensive route analysis runs ONCE (first render only) #}
  {% import "util-analyze-routes" for analyze_routes %}
  {% var result = analyze_routes(resources) %}
  {% set_cached("gateway_analysis", result) %}
{% end %}
{% var gateway_analysis = get_cached("gateway_analysis") %}
{# gateway_analysis now contains cached data from first computation #}
```

**Creating New Utility Snippets:**

When you have expensive computations used by multiple snippets:

```yaml
# In your library file (e.g., my-library.yaml)
templateSnippets:
  util-my-expensive-computation:
    template: |
      {#-
        My Expensive Computation Cache

        Description of what this computes and why it's expensive.

        After rendering this snippet, the following variable is available:
          my_computation - map containing:
            .results - list of computed results
            .lookup  - dict for fast lookups

        Usage:
          {{ render "util-my-expensive-computation" }}
          {%- for _, item := range my_computation.results %}
            ... use item ...
          {%- end %}
      -#}
      {% if !has_cached("my_computation") %}
        {# Your expensive computation here - runs only ONCE per render #}
        {% var results = []any{} %}
        {%- for _, resource := range resources.my_resources.List() %}
          {% results = append(results, resource) %}
        {%- end %}
        {% set_cached("my_computation", map[string]any{"results": results}) %}
      {% end %}
      {% var my_computation = get_cached("my_computation") %}
```

**Key Requirements:**

1. Use a descriptive cache key (string name)
2. Check with `has_cached()` before computing
3. Store with `set_cached()` after computing
4. Retrieve with `get_cached()` for use

**Performance Impact**: Reduces expensive computations from N to 1 per render (up to 70-90% reduction for heavy operations).

**When to Create a Utility Snippet:**

✅ **Good candidates:**

- Expensive loops over all resources (HTTPRoutes, Ingresses, etc.)
- Complex sorting, grouping, or conflict detection
- Resource scanning with filtering logic
- Any computation used by 2+ snippets

❌ **Don't create for:**

- Simple variable assignments
- Computations specific to one snippet
- Fast operations (under 10ms)

## Scriggo Templating Guide

**Scriggo is the template engine.** It uses Go's type system natively.

**Official Documentation:** <https://scriggo.com/templates>

### Template Syntax Overview

Scriggo uses three primary delimiter types:

| Syntax | Purpose | Example |
|--------|---------|---------|
| `{{ expr }}` | Output expression (show) | `{{ product.Name }}` |
| `{% stmt %}` | Statements/declarations | `{% if stock > 10 %}` |
| `{%% ... %%}` | Multi-line statement blocks | Complex logic |
| `{# comment #}` | Comments (nestable) | `{# TODO #}` |

**Show statement:** The `{{ }}` syntax is shorthand for `{% show expr %}`. You can show multiple expressions: `{% show 5 + 2, " = ", 7 %}`.

### Multi-line Statement Blocks (Preferred)

**Always prefer multi-line statement blocks (`{%% ... %%}`) over multiple single-line statements** when you have consecutive logic statements. This improves readability and reduces visual clutter.

**Single-line syntax** (`{% ... %}`): Use for single statements or when embedded in output:

```scriggo
{%- var name = "value" %}
{%- if condition %}output{% end %}
```

**Multi-line syntax** (`{%% ... %%}`): Use for blocks of consecutive statements. Inside multi-line blocks, use Go-style syntax with curly braces:

```scriggo
{%%
  var name = route.metadata.name
  var namespace = route.metadata.namespace
  var backendKey = namespace + "_" + name

  if !first_seen("backends", backendKey) {
    continue
  }
%%}
```

**When to use multi-line blocks:**

- ✅ Multiple variable declarations in sequence
- ✅ Complex conditional logic with multiple statements
- ✅ Loop setup with pre-computed variables
- ✅ Any block with 3+ consecutive statement lines

**When to use single-line:**

- ✅ Single statement followed by output
- ✅ Simple `if`/`for` wrapping output content
- ✅ Statements interspersed with template output

**Example refactoring:**

```scriggo
{#- AVOID: Many single-line statements #}
{%- var name = route.metadata.name %}
{%- var namespace = route.metadata.namespace %}
{%- var annotations = route.metadata.annotations %}
{%- var backendKey = namespace + "_" + name %}
{%- if !first_seen("backends", backendKey) %}
  {%- continue %}
{%- end %}

{#- PREFER: Multi-line block #}
{%%
  var name = route.metadata.name
  var namespace = route.metadata.namespace
  var annotations = route.metadata.annotations
  var backendKey = namespace + "_" + name

  if !first_seen("backends", backendKey) {
    continue
  }
%%}
```

### Variables and Types

**Variable declaration:** Variables require the `var` keyword or short assignment syntax:

```scriggo
{%- var name = "value" %}
{%- var count = 0 %}
{%- var items = []any{} %}
{%- var config = map[string]any{} %}

{#- Short declaration syntax #}
{%- welcome := "hello" %}
```

Type can be explicit or inferred from the assigned value. Uninitialized variables receive default values (empty string, 0, false, nil).

**Assignment (reassignment):** Once declared, variables can be reassigned with `=`. The type cannot change.

```scriggo
{%- name = "new value" %}
{%- count = count + 1 %}

{#- Compound operators supported #}
{%- count++ %}
{%- count += 5 %}

{#- Multiple assignment #}
{%- a, b = b, a %}
```

**Variable scope:**

- **File-level (outside blocks)**: Visible throughout the file and in extended/imported files
- **Within blocks** (macros, conditionals, loops): Visible from declaration to block end
- **Render statements**: Variables don't cross file boundaries unless passed via macro arguments

**Basic types:**

| Type | Description | Default |
|------|-------------|---------|
| `bool` | Boolean values (`true`/`false`) | `false` |
| `string` | Text in double quotes or backticks | `""` |
| `int` | Integer numeric values | `0` |
| `float64` | Floating-point numbers | `0.0` |

**Format types** (string-based with context-aware escaping):

- `html` - HTML code that won't be escaped in HTML contexts
- `css`, `js`, `json`, `markdown` - Similar context-aware types

**Collection types:**

```scriggo
{#- Slices (ordered sequences) #}
{%- var items = []any{} %}
{%- var names = []string{"alice", "bob"} %}
{%- var numbers = []int{1, 2, 3} %}

{#- Maps (key-value associations) #}
{%- var config = map[string]any{} %}
{%- var labels = map[string]string{"app": "web"} %}
```

**Slice operations:**

- Indexing: `s[0]`
- Length: `len(s)`
- Slicing: `s[1:3]` (end index excluded)
- Appending: `append(s, value)`

**Map operations:**

- Bracket notation: `map["key"]`
- Dot notation: `map.key`
- Iteration: `for key, value := range map`

### Control Flow

**If statement:**

```scriggo
{%- if condition %}
  content
{%- else if other_condition %}
  other content
{%- else %}
  fallback
{%- end %}
```

**Truthiness rules:** A condition is false for: `false`, `0`, `0.0`, `""`, `nil`, and empty collections (slices/maps). All other values are truthy.

**For loops:**

```scriggo
{#- For-in loop (most common) - note the "in" keyword #}
{%- for item in items %}
  {{ item }}
{%- end %}

{#- For-range with index and value #}
{%- for i, item := range items %}
  {{ i }}: {{ item }}
{%- end %}

{#- For with else (runs if collection is empty) #}
{%- for item in items %}
  {{ item }}
{%- else %}
  No items found
{%- end %}

{#- C-style for loop with condition #}
{%- for i := 0; i < 10; i++ %}
  {{ i }}
{%- end %}

{#- For with just condition (while-style) #}
{%- for condition %}
  content
{%- end %}
```

**Loop control:**

- `{% break %}` - Exit the loop immediately
- `{% continue %}` - Skip to the next iteration

**Switch statement:**

```scriggo
{%- switch value %}
{%- case "option1" %}
  First option
{%- case "option2", "option3" %}
  Second or third option
{%- default %}
  Default case
{%- end %}

{#- Switch without expression (uses boolean cases) #}
{%- switch %}
{%- case stock > 100 %}
  High stock
{%- case stock > 10 %}
  Medium stock
{%- default %}
  Low stock
{%- end %}
```

Only the first matching case executes (no fallthrough).

### Operators

**Comparison:** `==`, `!=`, `<`, `<=`, `>`, `>=`

**Arithmetic:** `+`, `-`, `*`, `/`, `%` (remainder for integers only)

**Logical operators:**

| Operator | Go-style | Template-style | Notes |
|----------|----------|----------------|-------|
| AND | `&&` | `and` | Returns `true`/`false`, accepts any type |
| OR | `\|\|` | `or` | Returns `true`/`false`, accepts any type |
| NOT | `!` | `not` | Returns `true`/`false`, accepts any type |

The template-style operators (`and`, `or`, `not`) differ from Go's boolean operators by accepting any type and evaluating truthiness. Unlike Jinja2 where `and`/`or` return one of the operands, Scriggo always returns boolean.

**String concatenation:** `+` (not `~` like Jinja2)

```scriggo
{%- var fullname = firstname + " " + lastname %}
```

**Contains operator:** `contains`, `not contains`

Template-specific operators for checking slice membership, map keys, and substring presence:

```scriggo
{%- if colors contains "red" %}
{%- if product.Name contains "bundle" %}
{%- if name not contains "test" %}
```

**Default operator:**

```scriggo
{{ value default "fallback" }}
```

!!! warning "Default Operator Limitation"
    The `default` operator only works with simple identifiers, not field access.
    Use `fallback()` function for field access: `{{ obj.field | fallback("default") }}`

### Functions and Filters

Scriggo supports both function call syntax and pipe syntax:

```scriggo
{#- Function syntax #}
{{ toLower(name) }}
{{ join(items, ", ") }}

{#- Pipe syntax (Jinja2-style) #}
{{ name | toLower() }}
{{ items | join(", ") }}
```

!!! warning "Pipe Operator Requires Parentheses"
    In Scriggo, the pipe operator requires a function call on the right side:
    - `{{ value | toLower() }}` ✓
    - `{{ value | toLower }}` ✗ (error: pipe operator requires function call)

**Style preferences:**

- **Prefer pipe syntax over nested function calls** for readability:

  ```scriggo
  {#- Good - reads left to right #}
  {{ value | dig("metadata", "name") | fallback("") | tostring() }}

  {#- Avoid - harder to read #}
  {{ tostring(fallback(dig(value, "metadata", "name"), "")) }}
  ```

- **Prefer dot notation over bracket notation** for map access when keys are valid identifiers:

  ```scriggo
  {#- Good - cleaner syntax #}
  {{ route.metadata.namespace }}
  {{ config.server.port }}

  {#- Use brackets only when necessary #}
  {{ labels["kubernetes.io/name"] }}  {# Key contains special chars #}
  {{ data[variableKey] }}              {# Dynamic key access #}
  ```

**Available functions:**

| Function | Description | Example |
|----------|-------------|---------|
| `tostring(v)` | Convert to string | `tostring(123)` → `"123"` |
| `toint(v)` | Convert to int | `toint("42")` → `42` |
| `tofloat(v)` | Convert to float64 | `tofloat("3.14")` → `3.14` |
| `len(v)` | Length of slice/map/string | `len(items)` |
| `toLower(s)` | Lowercase string | `toLower("ABC")` → `"abc"` |
| `toUpper(s)` | Uppercase string | `toUpper("abc")` → `"ABC"` |
| `trim(s)` / `strip(s)` | Trim whitespace | `trim("  x  ")` → `"x"` |
| `replace(s, old, new)` | Replace all occurrences | `replace("a-b", "-", "_")` |
| `split(s, sep)` | Split string | `split("a,b,c", ",")` → `[]string` |
| `join(slice, sep)` | Join slice | `join([]string{"a","b"}, ",")` → `"a,b"` |
| `hasPrefix(s, p)` | Check prefix | `hasPrefix("hello", "he")` → `true` |
| `hasSuffix(s, p)` | Check suffix | `hasSuffix("hello", "lo")` → `true` |
| `b64decode(s)` | Decode base64 | `b64decode("SGVsbG8=")` → `"Hello"` |
| `keys(m)` | Sorted map keys | `keys(config)` → `[]string` |
| `merge(m1, m2)` | Merge maps | `merge(base, overrides)` |
| `dig(obj, keys...)` | Navigate nested maps | `dig(obj, "meta", "name")` |
| `fallback(v, default)` | Return default if nil | `fallback(obj.field, "")` |
| `append(slice, item)` | Append to slice | `append(items, newItem)` |
| `toSlice(v)` | Convert to []any | `toSlice(maybeNil)` |
| `sort_by(slice, criteria)` | Sort by JSONPath | See sorting section |
| `glob_match(names, pattern)` | Filter by glob | `glob_match(templates, "backend-*")` |
| `first_seen(prefix, keys...)` | Deduplication helper | See deduplication section |
| `regex_search(s, pattern)` | Regex match | `regex_search(name, "^test")` |
| `sanitize_regex(s)` | Escape regex chars | `sanitize_regex("a.b")` → `"a\\.b"` |
| `indent(s, spaces, first)` | Indent lines | `indent(text, 4, true)` |

**Scriggo built-in functions** (see <https://scriggo.com/templates/builtins>):

| Function | Description | Example |
|----------|-------------|---------|
| `len(v)` | Length of string/slice/map | `len("hello")` → `5` |
| `runeCount(s)` | Character count (vs bytes) | `runeCount("日本")` → `2` |
| `abs(n)` | Absolute value | `abs(-5)` → `5` |
| `max(a, b)` | Maximum of two ints | `max(3, 7)` → `7` |
| `min(a, b)` | Minimum of two ints | `min(3, 7)` → `3` |
| `pow(x, y)` | Power (float64) | `pow(2.0, 3.0)` → `8.0` |
| `sort(slice)` | Sort slice | `sort([]int{3, 1, 2})` |
| `reverse(slice)` | Reverse slice | `reverse(items)` |
| `capitalize(s)` | Capitalize first letter | `capitalize("hello")` → `"Hello"` |
| `capitalizeAll(s)` | Capitalize all words | `capitalizeAll("hello world")` |
| `index(s, substr)` | Find substring index | `index("hello", "ll")` → `2` |
| `abbreviate(s, n)` | Truncate with ellipsis | `abbreviate("hello world", 8)` |
| `toKebab(s)` | CamelCase to kebab-case | `toKebab("borderTop")` → `"border-top"` |
| `sprintf(fmt, args...)` | Format string | `sprintf("%d items", count)` |
| `base64(s)` | Encode to base64 | `base64("hello")` |
| `hex(s)` | Encode to hex | `hex("AB")` → `"4142"` |
| `md5(s)` | MD5 hash | `md5("test")` |
| `sha1(s)` | SHA1 hash | `sha1("test")` |
| `sha256(s)` | SHA256 hash | `sha256("test")` |
| `queryEscape(s)` | URL encode | `queryEscape("a b")` → `"a+b"` |
| `htmlEscape(s)` | Escape HTML | `htmlEscape("<b>")` → `"&lt;b&gt;"` |
| `marshalJSON(v)` | Convert to JSON | `marshalJSON(obj)` |
| `unmarshalJSON(s)` | Parse JSON | `unmarshalJSON(jsonStr)` |
| `regexp(pattern)` | Compile regex | See regex section |
| `now()` | Current time | `now()` |
| `date(y, m, d, ...)` | Create time | `date(2024, 1, 15)` |

### Macros

Macros are reusable template functions. They must have **uppercase** first letter to be importable/exportable across files.

**Definition:**

```scriggo
{%- macro BackendName(namespace string, name string) %}
backend_{{ namespace }}_{{ name }}
{%- end %}
```

**Calling macros:**

```scriggo
{{ BackendName("default", "myservice") }}
```

**Macro parameters with types:**

```scriggo
{%- macro ProcessRoute(route map[string]any, index int) %}
  {#- route is typed as map[string]any #}
  {%- var name = route["name"].(string) %}
{%- end %}
```

Type annotations can be omitted if consecutive parameters share the same type:

```scriggo
{%- macro Image(url string, width, height int) %}
```

**Macro scope:** Macros can access global variables and other macros/variables declared earlier in the same file. Variables declared within a macro body remain local to that macro.

**Distraction-free macros:** In files with an `extends` declaration, parameter-less macros can use simplified syntax:

```scriggo
{% Main %}
  Content here...
```

This is equivalent to `{% macro Main() %}...{% end %}` and extends to the end of the file.

!!! note "Macro Parameter Types"
    Macro parameters can use any type declared in the template globals.
    For custom types like `ResourceStore`, you need to expose them via `reflect.TypeOf()`.
    See "Exposing Custom Types" section below.

### Extends and Import

**Extends declaration:** Allows a template to inherit layout from another file. Must appear at the beginning of the file before other declarations.

```scriggo
{% extends "/layouts/base.html" %}

{#- The layout file calls macros like {{ Title() }} and {{ Body() }} #}
{#- Child files define those macros to fill in the layout: #}

{% macro Title() %}My Page Title{% end %}

{% macro Body() %}
  <p>Page content here</p>
{% end %}
```

**Import declaration:** Retrieves declarations from other files.

```scriggo
{#- Import specific macros #}
{% import "util-backend-helpers" for BackendName %}
{% import "util-helpers" for Helper1, Helper2 %}

{#- Import all exported declarations #}
{% import "util-helpers" %}

{#- Import with prefix (namespace) #}
{% import utils "util-helpers" %}
{{ utils.BackendName("default", "svc") }}
```

**Export rules:**

- Only declarations with **uppercase first letter** are exported
- Imported files can only contain declarations (no standalone content outside macros)

### The using Statement

The `using` statement evaluates a block of content and makes it available through the special `itea` identifier:

```scriggo
{%- var content = itea; using %}
  <p>This content is assigned to the variable</p>
{%- end using %}

{{ content }}  {#- Outputs the evaluated content #}
```

**Type specification:** You can declare itea's type:

```scriggo
{% show itea; using markdown %}
# Markdown Heading
Some **bold** text.
{% end %}
```

Supported types: `string`, `html`, `markdown`, `css`, `js`, `json`

**Passing content to functions:**

```scriggo
{% sendEmail(from, to, itea); using %}
Hello {{ name }},
Your order has been shipped.
{% end %}
```

**Lazy evaluation with using macro:** Defer body execution until actually needed:

```scriggo
{% show Dialog("Warning", itea); using macro %}
  <p>This is only evaluated if Dialog actually uses its content parameter</p>
{% end %}
```

**Macro with parameters:**

```scriggo
{% show UserList(users, itea); using macro(user User) %}
  <li>{{ user.Name }} - {{ user.Email }}</li>
{% end %}
```

### Type Assertions

When working with `interface{}` (any) values, you need type assertions to access fields or use type-specific operations:

```scriggo
{#- Type assertions #}
{%- var name = value.(string) %}
{%- var items = value.([]any) %}
{%- var config = value.(map[string]any) %}

{#- Safe type assertion with check #}
{%- var name, ok = value.(string) %}
{%- if ok %}
  {{ name }}
{%- end %}
```

**Common type assertion patterns:**

```scriggo
{#- Accessing nested map values #}
{%- var data = obj["data"].(map[string]any) %}
{%- var name = data["name"].(string) %}

{#- Iterating over interface slice #}
{%- for _, item := range items.([]any) %}
  {%- var m = item.(map[string]any) %}
  {{ m["name"] }}
{%- end %}
```

### Template Inclusion (render Operator)

The `render` operator includes and processes template files, returning a string representation.

**Basic syntax:**

```scriggo
{{ render "template-name" }}
```

**Path types:**

- Absolute paths: `{{ render "/templates/header.html" }}`
- Relative paths: `{{ render "../shared/footer.html" }}`

**Scope isolation:** By default, rendered templates cannot access variables from the calling template. This is intentional for encapsulation.

**Render as expression:** The render operator returns a string, so it can be used in assignments:

```scriggo
{%- var header = render "header.html" %}
```

**Default expression (error handling):** When a file might not exist, use the `default` clause:

```scriggo
{%- promo := render "promotions.html" default "No promotions" %}
{{ render "specials.html" default render "no-specials.html" }}
```

The default expression is only evaluated if the primary file cannot be found.

### Passing Variables with inherit_context (Fork Feature)

**This is a fork-specific feature not in upstream Scriggo.**

The `inherit_context` modifier allows rendered templates to access local variables from the calling scope:

```scriggo
{%- var name = "World" %}
{%- var count = 42 %}
{{ render "greeting.html" inherit_context }}

{#- In greeting.html, name and count are accessible: #}
{#- Hello {{ name }}, count is {{ count }} #}
```

**When to use inherit_context:**

- Passing context to included templates without restructuring as macros
- Quick prototyping before converting to proper macro parameters
- Sharing computed values across multiple snippet includes

**When NOT to use:**

- For reusable templates (use macros with explicit parameters instead)
- When you need clear documentation of dependencies
- For templates that might be used in different contexts

### render_glob (Fork Feature)

**This is a fork-specific feature not in upstream Scriggo.**

The `render_glob` operator renders all templates matching a glob pattern:

```scriggo
{{ render_glob "backend-*" }}
{{ render_glob "features-gateway-*" }}
{{ render_glob "widgets/*.html" }}
```

**Glob patterns supported:**

- `*` matches any sequence of characters (not including path separator)
- `?` matches any single character
- `[abc]` matches any character in the set

**With inherit_context:**

```scriggo
{%- var config = loadConfig() %}
{{ render_glob "plugins/*.html" inherit_context }}
```

**Default for no matches:**

```scriggo
{{ render_glob "optional-*.html" default "" }}
```

**How it works:**

1. At compile time, Scriggo expands the glob pattern against the template filesystem
2. Matching templates are rendered in sorted order (alphabetical)
3. If no templates match, returns empty string (or default if specified)

**Example - rendering all backend snippets:**

```scriggo
{#- In base.yaml haproxyConfig template #}
{{ render_glob "backend-*" inherit_context }}

{#- This expands to render all matching snippets: #}
{#- backend-ingress, backend-gateway, backend-passthrough, etc. #}
```

### Exposing Custom Types to Templates

To use custom Go types in macro signatures, you must expose them via `reflect.TypeOf()` in the globals declarations. This is done in `pkg/templating/filters_scriggo.go`.

**Example - exposing a custom type:**

```go
// In filters_scriggo.go
import "reflect"

func registerScriggoRuntimeVars(decl native.Declarations) {
    // Variables (nil pointers for runtime injection)
    decl["resources"] = (*map[string]ResourceStore)(nil)

    // Types (for use in macro signatures)
    decl["ResourceStore"] = reflect.TypeOf(ResourceStore{})
    decl["MapStringAny"] = reflect.TypeOf(map[string]any{})
}
```

**Using the type in templates:**

```scriggo
{%- macro ProcessData(data MapStringAny) %}
  {#- data is now properly typed #}
  {%- var name = data["name"].(string) %}
{%- end %}
```

**Available global types for template use:**

| Declaration | Type | Purpose |
|-------------|------|---------|
| `resources` | `*map[string]ResourceStore` | Kubernetes resource stores |
| `pathResolver` | `*PathResolver` | File path resolution |
| `fileRegistry` | `*FileRegistrar` | Dynamic file registration |
| `shared` | `*map[string]interface{}` | Cross-template cache |
| `templateSnippets` | `*[]string` | Available snippet names |
| `globalFeatures` / `gf` | `map[string]any` | Cross-library shared state (see "Cross-Library Shared State" section) |

### Caching with has_cached/get_cached/set_cached

For expensive computations that should run only once per render:

```scriggo
{%- if !has_cached("analysis_key") %}
  {#- Expensive computation runs only once #}
  {%- var result = expensive_computation() %}
  {%- set_cached("analysis_key", result) %}
{%- end %}
{%- var cached_result = get_cached("analysis_key") %}
```

### Deduplication with first_seen

The `first_seen` function atomically checks if a key has been seen before:

```scriggo
{%- for _, item := range items %}
  {%- if first_seen("backends", item.namespace, item.name) %}
    {#- Only runs for first occurrence of this namespace+name combination #}
    backend {{ item.namespace }}_{{ item.name }}
  {%- end %}
{%- end %}
```

### Sorting with sort_by

Sort slices using JSONPath criteria:

```scriggo
{%- var sorted = items | sort_by([]string{
  "$.priority:desc",           {#- Descending by priority #}
  "$.path.value | length:desc", {#- Descending by path length #}
  "$.name",                    {#- Ascending by name #}
}) %}
```

**Sort modifiers:**

- `:desc` - Descending order (default is ascending)
- `:exists` - Sort by field existence (exists first)
- `| length` - Sort by length of value

### Whitespace Control

Control whitespace around template tags:

- `{%-` Strip whitespace before tag
- `-%}` Strip whitespace after tag

```scriggo
{%- for _, item := range items -%}
{{ item }}
{%- end -%}
```

### Scriggo vs Jinja2 Syntax Comparison

See also: <https://scriggo.com/templates/switch-from-jinja-to-scriggo>

**Key differences:**

| Feature | Jinja2 | Scriggo |
|---------|--------------|---------|
| Type system | Dynamic | Static (compile-time type checking) |
| Variable declaration | `{% set x = 1 %}` | `{% var x = 1 %}` or `{% x := 1 %}` |
| Variable reassignment | `{% set x = 2 %}` | `{% x = 2 %}` |
| End tags | `{% endif %}`, `{% endfor %}` | `{% end %}` or `{% end if %}`, `{% end for %}` |
| String concat | `x ~ y` | `x + y` |
| Default value | `x \| default(y)` | `x default y` or `fallback(x, y)` |
| Length | `x \| length` | `len(x)` |
| Import macro | `{% from "x" import y %}` | `{% import "x" for y %}` |
| Include | `{% include "x" %}` | `{{ render "x" }}` |
| Mutable state | `namespace(a=1)` | `map[string]any{"a": 1}` |

**Data structure syntax:**

| Type | Jinja2 | Scriggo |
|------|--------------|---------|
| List/Array | `[1, 2, 3]` | `[]int{1, 2, 3}` (typed) |
| Dictionary/Map | `{'key': 'value'}` | `map[string]string{"key": "value"}` |
| Tuple | `(1, 5, 2020)` | Use slice or map |

**Operator differences:**

| Operation | Jinja2 | Scriggo |
|-----------|--------------|---------|
| Logical AND/OR | Returns operand | Returns boolean |
| Contains check | `1 in [1, 2, 3]` | `[]int{1, 2, 3} contains 1` (reversed) |
| Conditional expr | `x if cond else y` | Use if statement |

**Loop syntax:**

```scriggo
{#- Jinja2 #}
{% for item in items %}
  {{ item }}
{% endfor %}

{#- Scriggo (for-in) #}
{% for item in items %}
  {{ item }}
{% end %}

{#- Scriggo (for-range with index) #}
{% for i, item := range items %}
  {{ i }}: {{ item }}
{% end %}
```

**Filter to function migration:**

| Jinja2 Filter | Scriggo Function |
|---------------|------------------|
| `{{ value\|abs }}` | `{{ abs(value) }}` |
| `{{ value\|length }}` | `{{ len(value) }}` |
| `{{ foo\|attr("bar") }}` | `{{ foo.bar }}` |
| `{{ items\|join(",") }}` | `{{ join(items, ",") }}` |
| `{{ s\|upper }}` | `{{ toUpper(s) }}` |
| `{{ s\|lower }}` | `{{ toLower(s) }}` |
| `{{ s\|capitalize }}` | `{{ capitalize(s) }}` |

**HTML escaping:**

- Jinja2: Auto-escapes by default, use `{{ value\|safe }}` to disable
- Scriggo: Auto-escapes in HTML context, cast to `html` type: `{{ html("<b>Bold</b>") }}`

**Block assignments (Jinja2 call blocks):**

```scriggo
{#- Jinja2 #}
{% set content %}
  HTML content here
{% endset %}

{#- Scriggo #}
{% var content = itea; using %}
  HTML content here
{% end using %}
```

## Common Pitfalls

### Overriding haproxyConfig in Libraries

**Problem**: Adding `haproxyConfig` to a library file.

```yaml
# ingress.yaml - WRONG!
haproxyConfig:
  template: |
    # This will override base.yaml's template!
```

**Why Bad**: The library merge uses `mustMergeOverwrite`, so your library's `haproxyConfig` will completely replace base.yaml's template, breaking other libraries.

**Solution**: Only define `templateSnippets`, let base.yaml call them via `{{ render "snippet-name" }}`.

### Testing Individual Library Files

**Problem**: Running `controller validate` directly on a library file.

```bash
# WRONG - library file is incomplete!
./bin/haptic-controller validate -f charts/haptic/libraries/ingress.yaml
```

**Why Bad**: Library files are meant to be merged. Testing them individually will fail because:

- Missing base template (`haproxyConfig`)
- Missing snippets from other libraries
- Missing watched resources from other libraries

**Solution**: Always test the merged Helm output:

```bash
# CORRECT
helm template charts/haptic \
  | yq 'select(.kind == "HAProxyTemplateConfig")' \
  | ./bin/haptic-controller validate -f -
```

### Missing watchedResources

**Problem**: Template uses resources not declared in `watchedResources`.

```yaml
templateSnippets:
  my-snippet:
    template: |
      {%- for _, svc := range resources.services.List() %}
      # ERROR: services not in watchedResources!
```

**Solution**: Declare all used resources:

```yaml
watchedResources:
  services:
    apiVersion: v1
    resources: services
    indexBy: ["metadata.namespace", "metadata.name"]
```

### Inconsistent Shared State Map Keys

**Problem**: Using different key names for the same shared state across libraries.

```scriggo
{#- ssl.yaml initializes with snake_case #}
{%- gf["tls_certificates"] = []any{} %}

{#- ingress.yaml writes with camelCase - WRONG KEY! #}
{%- gf["tlsCertificates"] = append(gf["tlsCertificates"].([]any), cert) %}

{#- ssl.yaml reads snake_case - gets empty array! #}
{%- var certs = gf["tls_certificates"] %}  {# Empty because ingress wrote to different key #}
```

**Why Bad**: Go maps are case-sensitive. `tls_certificates` and `tlsCertificates` are completely different keys. Data written to one key is invisible when reading the other. This causes subtle bugs where features silently fail.

**Solution**: Use consistent **camelCase** for all shared state keys:

```scriggo
{#- All libraries use the same key name #}
{%- gf["tlsCertificates"] = []any{} %}           {# ssl.yaml initializes #}
{%- gf["tlsCertificates"] = append(..., cert) %}  {# ingress.yaml writes #}
{%- var certs = gf["tlsCertificates"] %}          {# ssl.yaml reads #}
```

**Canonical Key Names:**

| Correct (camelCase) | Wrong (various) |
|---------------------|-----------------|
| `tlsCertificates` | `tls_certificates`, `TLSCertificates` |
| `sslPassthroughBackends` | `ssl_passthroughBackends`, `sslPassthrough_backends`, `ssl_passthrough_backends` |

### Annotation Ownership

**Problem**: Processing annotations in the wrong library.

```yaml
# ingress.yaml - WRONG!
templateSnippets:
  backends-500-ingress:
    template: |
      {#- This annotation belongs in haproxytech.yaml! #}
      {%- var snippet = ingress | dig("metadata", "annotations", "haproxy.org/backend-config-snippet") %}
```

**Why Bad**: The `haproxy.org/*` annotations are HAProxy-specific compatibility features. Placing them in ingress.yaml:

- Violates separation of concerns
- Makes annotation documentation harder to find
- Prevents gateway.yaml from using the same annotations

**Solution**: Process annotations in the library that owns them:

| Annotation Prefix | Owner Library |
|-------------------|---------------|
| `haproxy.org/*` | haproxytech.yaml |
| `haproxy-ingress.github.io/*` | haproxy-ingress.yaml |
| (none - standard fields) | ingress.yaml, gateway.yaml |

**Pattern for Annotation Libraries:**

```yaml
# haproxytech.yaml - processes haproxy.org/* annotations
templateSnippets:
  backend-directives-900-haproxytech-advanced:
    template: |
      {%- if ingress != nil %}
        {#- All haproxy.org/* annotations handled here #}
        {%- var snippet = ingress | dig("metadata", "annotations", "haproxy.org/backend-config-snippet") | fallback("") %}
        {%- if snippet != "" %}
      # haproxytech/backend-config-snippet
      {{ snippet }}
        {%- end %}
      {%- end %}
```

The annotation library receives the `ingress` variable via `inherit_context` from the calling snippet (backends-500-ingress).

### JSONPath Escaping in Labels

**Problem**: Label keys with dots (like `kubernetes.io/service-name`) break JSONPath.

```yaml
# WRONG
indexBy: ["metadata.labels.kubernetes.io/service-name"]
# Error: JSONPath thinks "io" is a field of "kubernetes"
```

**Solution**: Escape dots with double backslash:

```yaml
# CORRECT
indexBy: ["metadata.labels.kubernetes\\.io/service-name"]
```

## Chart Files Overview

```
charts/haptic/
├── Chart.yaml                   # Helm chart metadata
├── values.yaml                  # Default configuration values
├── README.md                    # User-facing chart documentation
├── CLAUDE.md                    # This file - development context
│
├── libraries/                   # Template libraries (merged at render time)
│   ├── base.yaml               # Core HAProxy template (defines haproxyConfig)
│   ├── ingress.yaml            # Kubernetes Ingress support
│   ├── gateway.yaml            # Gateway API support
│   ├── haproxytech.yaml        # HAProxy annotation compatibility
│   └── path-regex-last.yaml    # Alternative path matching order
│
├── templates/                   # Helm templates
│   ├── _helpers.tpl            # Template helper functions (library merging)
│   ├── haproxytemplateconfig.yaml  # Renders merged HAProxyTemplateConfig CRD
│   ├── deployment.yaml         # Controller deployment
│   ├── service.yaml            # Controller service
│   ├── clusterrole.yaml        # RBAC permissions
│   └── ...                     # Other K8s resources
│
└── crds/                        # Custom Resource Definitions
    └── haproxy-haptic.org_haproxytemplateconfigs.yaml
```

## Debugging Tips

### View Merged Template Output

```bash
# See the complete merged HAProxyTemplateConfig
helm template charts/haptic \
  | yq 'select(.kind == "HAProxyTemplateConfig")'
```

### Check Template Snippet Merging

```bash
# Extract just the templateSnippets section
helm template charts/haptic \
  | yq 'select(.kind == "HAProxyTemplateConfig") | .spec.templateSnippets | keys'
```

### Verify watchedResources

```bash
# See which resources will be watched
helm template charts/haptic \
  | yq 'select(.kind == "HAProxyTemplateConfig") | .spec.watchedResources | keys'
```

### Test Specific Library Combinations

```bash
# Disable all libraries, enable only one
helm template charts/haptic \
  --set controller.templateLibraries.ingress.enabled=false \
  --set controller.templateLibraries.gateway.enabled=false \
  --set controller.templateLibraries.haproxytech.enabled=true \
  | yq 'select(.kind == "HAProxyTemplateConfig")'
```

## Resources

- Helm template reference: <https://helm.sh/docs/chart_template_guide/>
- yq documentation: <https://github.com/mikefarah/yq>
- HAProxyTemplateConfig CRD: `crds/haproxy-haptic.org_haproxytemplateconfigs.yaml`
- Controller validation: `pkg/controller/testrunner/CLAUDE.md`
- Template engine: `pkg/templating/CLAUDE.md`

## Changelog Guidelines

The chart CHANGELOG (`charts/haptic/CHANGELOG.md`) documents Helm chart configuration changes. Keep entries concise - one line per change, focus on what changed. Avoid verbose justifications or explanations in parentheses.

**Include:**

- New Helm values and configuration options
- Changes to default values (replicas, resources, etc.)
- Service configuration changes (ports, types, annotations)
- RBAC and security context changes
- Template library additions/changes
- CRD updates

**Exclude:**

- Controller behavior and features (belong in controller CHANGELOG)
- Internal implementation details
- Development workflow changes

For controller changes, see the root `CHANGELOG.md`.
