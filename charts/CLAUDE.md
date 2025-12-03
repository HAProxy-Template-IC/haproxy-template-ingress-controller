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
{{- define "haproxy-template-ic.mergeLibraries" -}}
{{- $merged := dict }}
# Load each library in order using mustMergeOverwrite
# Later libraries override earlier ones for the same keys
{{- $merged = mustMergeOverwrite $merged $baseLibrary }}
{{- $merged = mustMergeOverwrite $merged $ingressLibrary }}
# ... etc
{{- end }}
```

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

```gonja
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
helm template charts/haproxy-template-ic \
  --api-versions=gateway.networking.k8s.io/v1/GatewayClass \
  --set controller.templateLibraries.ingress.enabled=true \
  --set controller.templateLibraries.gateway.enabled=false \
  | yq 'select(.kind == "HAProxyTemplateConfig")' \
  > /tmp/merged-config.yaml

# 2. Validate merged configuration
make build
./bin/controller validate -f /tmp/merged-config.yaml

# 3. Run specific validation test
./bin/controller validate -f /tmp/merged-config.yaml \
  --test test-ingress-duplicate-backend-different-ports
```

**Why use `yq 'select(.kind == "HAProxyTemplateConfig")'`?**

`helm template` outputs **all** Kubernetes resources (Deployment, Service, ConfigMap, etc.). The `controller validate` command expects a single HAProxyTemplateConfig resource, so we filter for it using yq.

**IMPORTANT: Gateway API Tests**

Gateway API tests require the `--api-versions=gateway.networking.k8s.io/v1/GatewayClass` flag to simulate the presence of Gateway API CRDs. Without this flag, Helm's Capabilities check will skip merging the gateway library, and gateway validation tests will not be available.

The test script includes this flag automatically. If using the manual workflow, you MUST include it:

```bash
# Manual workflow - MUST include --api-versions flag
helm template charts/haproxy-template-ic \
  --api-versions=gateway.networking.k8s.io/v1/GatewayClass \
  | yq 'select(.kind == "HAProxyTemplateConfig")' \
  > /tmp/gateway-config.yaml
```

This flag is already used in CI (see `.gitlab-ci.yml`). The gateway library uses a Capabilities check (`templates/_helpers.tpl:86`) to only merge when Gateway API CRDs are detected.

### Testing Specific Libraries

Enable/disable libraries to test specific combinations:

```bash
# Test only ingress library (no gateway)
helm template charts/haproxy-template-ic \
  --set controller.templateLibraries.ingress.enabled=true \
  --set controller.templateLibraries.gateway.enabled=false \
  | yq 'select(.kind == "HAProxyTemplateConfig")' \
  > /tmp/ingress-only.yaml

# Test gateway library (no ingress)
helm template charts/haproxy-template-ic \
  --set controller.templateLibraries.ingress.enabled=false \
  --set controller.templateLibraries.gateway.enabled=true \
  | yq 'select(.kind == "HAProxyTemplateConfig")' \
  > /tmp/gateway-only.yaml

# Test with custom values
helm template charts/haproxy-template-ic \
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

```gonja
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

### Backend Deduplication

When multiple paths route to the same service+port, deduplicate backends:

```yaml
templateSnippets:
  resource_ingress_backends:
    template: |
      {#- Using string-based deduplication due to Gonja PyString compatibility issues #}
      {%- set ns = namespace(seen="") %}
      {%- for ingress in resources.ingresses.List() %}
      {%- for path in ingress.spec.paths %}
      {%- set backend_key = path.service.name ~ "_" ~ path.service.port %}
      {%- if ("|" ~ backend_key ~ "|") not in ns.seen %}
      {%- set ns.seen = ns.seen ~ "|" ~ backend_key ~ "|" %}

      backend {{ backend_key }}
        # Only generated once per unique service+port
      {%- endif %}
      {%- endfor %}
      {%- endfor %}
```

**Note**: This uses the string-based deduplication pattern with delimiters. Backend keys generated from template expressions hit Gonja PyString compatibility issues with list membership checks. See [String-Based vs List-Based Deduplication](#string-based-vs-list-based-deduplication) for details.

### Optimizing Expensive Computations with compute_once

The `compute_once` custom tag prevents redundant execution of expensive template computations when the same template section is included multiple times.

**Problem**: Complex route analysis that runs 4 times per render

Without optimization, if you analyze routes in multiple places, the analysis runs every time:

```jinja2
{# snippet1.yaml #}
{%- from "analyze_routes" import analyze_routes %}
{%- set analysis = namespace(path_groups={}, sorted_routes=[]) %}
{{- analyze_routes(analysis, resources) -}}  {# Analyzes ALL routes #}

{# snippet2.yaml #}
{%- from "analyze_routes" import analyze_routes %}
{%- set analysis = namespace(path_groups={}, sorted_routes=[]) %}
{{- analyze_routes(analysis, resources) -}}  {# Analyzes ALL routes AGAIN #}

{# Result: analyze_routes runs N times for N snippets #}
```

**Solution**: Use `compute_once` to execute expensive computations only once

```jinja2
{# main template or early in base.yaml #}
{%- set analysis = namespace(path_groups={}, sorted_routes=[], all_routes=[]) %}

{# snippet1.yaml #}
{%- compute_once analysis %}
  {%- from "analyze_routes" import analyze_routes %}
  {{- analyze_routes(analysis, resources) -}}
{%- endcompute_once %}
{# Use analysis.sorted_routes here #}

{# snippet2.yaml #}
{%- compute_once analysis %}
  {%- from "analyze_routes" import analyze_routes %}
  {{- analyze_routes(analysis, resources) -}}  {# SKIPPED - already computed #}
{%- endcompute_once %}
{# Uses same analysis results #}
```

**Key Points:**

1. **Variable must be created BEFORE compute_once block**:
   ```jinja2
   {%- set analysis = namespace(path_groups={}) %}  {# Create first #}
   {%- compute_once analysis %}  {# Then guard computation #}
     {%- set analysis.path_groups = ... %}
   {%- endcompute_once %}
   ```

2. **Use namespace() for mutable state**:
   - Allows modifications to persist across includes
   - Same namespace variable shared across all templates in one render

3. **Computation runs once per Render() call**:
   - First include executes the body
   - Subsequent includes skip the body
   - Cache automatically cleared between renders

**Real-World Example from gateway.yaml**:

The Gateway API library uses `compute_once` to optimize route analysis (see `libraries/gateway.yaml:323-328`, `499-504`):

```jinja2
{#- gateway.yaml lines 323-328 -#}
{%- set analysis = namespace(path_groups={}, sorted_routes=[], all_routes=[]) %}
{%- compute_once analysis %}
  {%- from "analyze_routes" import analyze_routes %}
  {{- analyze_routes(analysis, resources) -}}
{%- endcompute_once %}

{#- Used in resource_gateway_path-map-entry (called 3 times for different map files) -#}
{%- for route in analysis.sorted_routes %}
  {# Generate path map entries #}
{%- endfor %}

{#- Used in advanced-matcher-gateway (called 1 time) -#}
{%- for route in analysis.sorted_routes %}
  {# Generate advanced matchers #}
{%- endfor %}
```

**Performance Impact**: Reduces analyze_routes calls from 4 to 1 per render (75% reduction).

**When to Use compute_once**:

✅ **Good use cases:**
- Expensive loops over large resource lists (all HTTPRoutes, all Ingresses)
- Complex sorting, grouping, or conflict detection
- Template sections included from multiple places
- Macros that perform heavy computation

❌ **Don't use for:**
- Simple variable assignments
- Computations that need different inputs each time
- Code that should run multiple times with different state

**Debugging**:

Enable template tracing to verify the optimization:

```go
engine.EnableTracing()
output, _ := engine.Render("haproxy.cfg", context)
trace := engine.GetTraceOutput()

// Verify expensive template only appears once in trace
// (Note: tracing shows top-level renders, not individual includes)
```

**Common Mistake**:

```jinja2
{# WRONG - variable doesn't exist before compute_once #}
{%- compute_once analysis %}
  {%- set analysis = namespace(...) %}  {# ERROR: can't create here #}
{%- endcompute_once %}

{# CORRECT - create variable first #}
{%- set analysis = namespace(...) %}
{%- compute_once analysis %}
  {%- set analysis.data = expensive_computation() %}
{%- endcompute_once %}
```

## Gonja/Jinja2 Templating Pitfalls

These are common mistakes when writing Gonja templates. Understanding these will prevent bugs and make your templates more reliable.

### List Manipulation - Use `append()` Not `+`

**Problem**: List concatenation with `+` operator doesn't work reliably with Gonja.

```gonja
{# WRONG - doesn't work properly #}
{%- set ns = namespace(items=[]) %}
{%- for item in collection %}
  {%- set ns.items = ns.items + [item] %}  {# Creates new list, doesn't update namespace #}
{%- endfor %}

{# CORRECT - use append method #}
{%- set ns = namespace(items=[]) %}
{%- for item in collection %}
  {%- set _ = ns.items.append(item) %}  {# Modifies list in place #}
{%- endfor %}
```

**Why**: Gonja's list type has an `append()` method that modifies the list in place. The `+` operator creates a new list but doesn't update the namespace variable.

**Documentation**: https://github.com/NikolaLohinski/gonja/blob/master/docs/methods.md#the-list-type

**CRITICAL - Namespace Attributes Return Copies:**

When you access a namespace attribute in a loop, Gonja gives you a **COPY**, not a reference. This means modifications to the copy are lost:

```gonja
{# WRONG - modifications lost each iteration! #}
{%- set ns = namespace(items=[]) %}
{%- for item in collection %}
  {%- set _ = ns.items.append(item) %}  {# Modifies COPY, lost next iteration #}
{%- endfor %}
{{ ns.items | length }}  {# Still 0! #}

{# CORRECT - reassign the returned value #}
{%- set ns = namespace(items=[]) %}
{%- for item in collection %}
  {%- set ns.items = ns.items.append(item) %}  {# Returns modified copy, reassign to namespace #}
{%- endfor %}
{{ ns.items | length }}  {# Correct! #}

{# ALSO CORRECT - for dicts #}
{%- set ns = namespace(config={}) %}
{%- for item in items %}
  {%- set ns.config = ns.config.update({item.key: item.value}) %}
{%- endfor %}
```

**Why This Matters**: Our custom `append()` and `update()` methods return the modified collection specifically to enable this reassignment pattern. Without the reassignment, changes are lost because you're modifying a copy that gets discarded at the end of each iteration.

**When to Use Each Pattern:**
- **Regular variables**: Use `{%- set _ = list.append(item) %}` (in-place modification works)
- **Namespace attributes**: Use `{%- set ns.list = ns.list.append(item) %}` (must reassign returned value)
- **Lists nested in dicts in namespaces**: Use get-append-update pattern (see below)

**Lists Nested in Dicts (Advanced Pattern):**

When you have a dict in a namespace, and that dict contains lists as values, you need a three-step pattern:

```gonja
{# WRONG - modifications lost! #}
{%- set ns = namespace(groups={}) %}
{%- set ns.groups = ns.groups.update({"key": []}) %}
{%- set _ = ns.groups["key"].append(item) %}  {# ns.groups["key"] returns COPY! #}

{# CORRECT - get, append with reassignment, update #}
{%- set ns = namespace(groups={}) %}
{%- set ns.groups = ns.groups.update({"key": []}) %}
{%- set current_list = ns.groups["key"] %}            {# Get copy of list #}
{%- set current_list = current_list.append(item) %}   {# Append returns modified copy #}
{%- set ns.groups = ns.groups.update({"key": current_list}) %}  {# Update dict with new list #}
```

**Why**: `ns.groups["key"]` returns a COPY of the list. Appending to this copy doesn't modify the dict. You must extract the list, modify it, then update the dict with the modified list.

### Mutable State with `namespace()`

**Problem**: Variables in Jinja2/Gonja are block-scoped and immutable across blocks.

```gonja
{# WRONG - counter doesn't persist across loop iterations #}
{%- set count = 0 %}
{%- for item in items %}
  {%- set count = count + 1 %}  {# This creates a NEW variable scoped to this block #}
{%- endfor %}
{{ count }}  {# Still 0! #}

{# CORRECT - use namespace for mutable state #}
{%- set ns = namespace(count=0) %}
{%- for item in items %}
  {%- set ns.count = ns.count + 1 %}  {# Modifies the namespace attribute #}
{%- endfor %}
{{ ns.count }}  {# Correct value! #}
```

**CRITICAL - Dictionaries Also Need Namespace**:

```gonja
{# WRONG - dict doesn't persist across iterations! #}
{%- set groups = {} %}
{%- for item in items %}
  {%- set groups = groups.update({item.key: item.value}) %}  {# Creates NEW dict each time #}
{%- endfor %}
{{ groups }}  {# Empty dict! #}

{# CORRECT - wrap dict in namespace #}
{%- set ns = namespace(groups={}) %}
{%- for item in items %}
  {%- set ns.groups = ns.groups.update({item.key: item.value}) %}
{%- endfor %}
{{ ns.groups }}  {# Contains all items! #}

{# ALSO CORRECT - for complex dict building #}
{%- set ns_groups = namespace(data={}) %}
{%- for item in items %}
  {%- if item.key not in ns_groups.data %}
    {%- set ns_groups.data = ns_groups.data.update({item.key: []}) %}
  {%- endif %}
  {%- set _ = ns_groups.data[item.key].append(item.value) %}
{%- endfor %}
{%- set final_groups = ns_groups.data %}  {# Extract final dict #}
```

**When to Use**:
- Counters and accumulators
- Deduplication tracking (seen lists/strings)
- Building dictionaries/maps dynamically
- Any state that needs to persist across loop iterations or conditional blocks

**Common Namespace Patterns**:
```gonja
{%- set ns = namespace(
    count=0,
    seen=[],
    items=[],
    groups={},
    found=false
) %}
```

### String-Based vs List-Based Deduplication

Two approaches for tracking seen items to prevent duplicates:

**List-Based Approach** (works with custom append() override):
```gonja
{%- set ns = namespace(seen=[]) %}
{%- for item in items %}
  {%- if item not in ns.seen %}
    {%- set ns.seen = ns.seen.append(item) %}
    {# Generate item #}
  {%- endif %}
{%- endfor %}
```

**String-Based Approach** (alternative without custom methods):
```gonja
{%- set ns = namespace(seen="") %}
{%- for item in items %}
  {%- if ("|" ~ item ~ "|") not in ns.seen %}
    {%- set ns.seen = ns.seen ~ "|" ~ item ~ "|" %}
    {# Generate item #}
  {%- endif %}
{%- endfor %}
```

**Root Cause - Go Interface Equality**:

Gonja's `Contains()` method uses Go's `==` operator on `interface{}` values:
```go
// From exec/value.go Contains() implementation
for i := 0; i < resolved.Len(); i++ {
    item := resolved.Index(i)
    if other.Interface() == item.Interface() {  // Object identity comparison!
        return true
    }
}
```

This compares **object identity**, not **string values**. Each template expression with `~` creates a NEW `*Value` object wrapping the string. Even though the string values are identical, the `*Value` objects are different, so `==` returns false.

**Why List-Based Works for Direct Fields**:
- `secret.metadata.name` returns an EXISTING `*Value` object from the resource store
- When we check `if name not in seen` and later encounter the SAME resource, we're comparing the SAME `*Value` object reference
- Object identity comparison succeeds because it's literally the same object

**Why List-Based Fails for Computed Keys**:
- `namespace ~ "_" ~ name` creates a NEW `*Value` object each time it's evaluated
- When we check `if key not in seen`, we're comparing DIFFERENT `*Value` object instances
- Object identity comparison fails even though string values are identical

**Why String-Based Works**:
- String membership uses `strings.Contains()` for substring matching
- Delimiter pattern `"|key|"` ensures exact matches
- Works with any string value regardless of `*Value` object identity

**Attempted Solutions That Failed**:
1. **Using `| string` filter**: Still creates `*Value` objects, doesn't help
2. **Using `"" ~` prefix**: Same issue, creates new `*Value` objects
3. **Using Go maps**: Would work BUT Gonja doesn't support `ns.map[key] = value` syntax
   - Error: `Can't write field "key_name"`
   - Gonja only allows setting namespace attributes like `ns.field = value`
   - Cannot set individual map keys within a namespace

### Custom Gonja Overrides for Mutable State Patterns

To enable idiomatic mutable state patterns in templates, we override three Gonja built-ins in `pkg/templating/engine.go`:

**1. Custom "in" Test**

Gonja's built-in `in` test uses Go's `interface{} ==` (object identity) instead of value comparison. Each template expression with `~` creates a NEW `*Value` object, so even identical strings fail equality checks.

Our `testInFixed()` compares string values using `.String()` method:

```go
// For lists: iterate and compare string values
inStr := in.String()
for i := 0; i < resolved.Len(); i++ {
    item := exec.ToValue(resolved.Index(i))
    if inStr == item.String() {  // Value comparison, not identity
        return true, nil
    }
}
```

**2. Custom list.append() Method**

Gonja's built-in `append()` returns `nil` instead of the modified list, breaking the pattern `{%- set ns.seen = ns.seen.append(key) %}`.

Our custom `append()` returns `selfValue.Interface()` after modification:

```go
"append": func(_ []interface{}, selfValue *exec.Value, arguments *exec.VarArgs) (interface{}, error) {
    // ... extract argument x ...

    // Modify list in-place (same as builtin)
    *selfValue = *exec.ToValue(reflect.Append(selfValue.Val, reflect.ValueOf(exec.ToValue(x))))

    // RETURN the modified list instead of nil
    return selfValue.Interface(), nil
},
```

**3. Custom dict.update() Method**

Python's `dict.update()` returns `None`, and Gonja doesn't include an `update()` method at all. This prevents the pattern `{%- set ns.config = ns.config.update({"key": "val"}) %}`.

Our custom `update()` returns the modified dict:

```go
"update": func(self map[string]interface{}, selfValue *exec.Value, arguments *exec.VarArgs) (interface{}, error) {
    var other map[string]interface{}
    if err := arguments.Take(
        exec.PositionalArgument("other", nil, exec.DictArgument(&other)),
    ); err != nil {
        return nil, exec.ErrInvalidCall(err)
    }

    // Update dict in-place
    for k, v := range other {
        self[k] = v
    }

    // RETURN the modified dict instead of nil
    return self, nil
},
```

**Trade-offs:**

✅ Enables clean, idiomatic template syntax for both lists and dicts
✅ Aligns with Jinja2 behavior expectations
✅ Fixes "in" operator, append(), and adds update()
❌ Must maintain copies of list methods (`reverse`, `copy`) and dict methods (`keys`, `items`)
❌ Need to sync with Gonja if new methods are added

**Why This Approach:**

Gonja/Jinja2 is THE ONLY mainstream template engine supporting mutable state in loops via `namespace()`. Every alternative (Pongo2, Stick, text/template, Handlebars, Mustache) either doesn't support this or requires moving logic to Go code, breaking our library architecture.

The maintenance cost of keeping 5 methods in sync is acceptable for enabling self-contained template libraries with inline state manipulation.

**When to Use Each**:

- **List-based**: Now works for ALL cases (direct fields and computed expressions)
  - Example: `secret.metadata.name`, `namespace ~ "_" ~ name ~ "_" ~ port`
  - Preferred for clean, idiomatic syntax

- **String-based**: Alternative if avoiding custom method overrides
  - Example: `("|" ~ key ~ "|") not in ns.seen`
  - Verbose but requires no Gonja modifications

**Recommended Patterns**:

Use namespace with custom methods for mutable state:

**List deduplication:**
```gonja
{%- set ns = namespace(seen=[]) %}
{%- for ingress in resources.ingresses.List() %}
  {%- set key = ingress.metadata.namespace ~ "_" ~ ingress.metadata.name %}
  {%- if key not in ns.seen %}
    {%- set ns.seen = ns.seen.append(key) %}
    {# Generate backend #}
  {%- endif %}
{%- endfor %}
```

**Dict accumulation:**
```gonja
{%- set ns = namespace(config={}) %}
{%- for rule in ingress.spec.rules %}
  {%- set ns.config = ns.config.update({
      rule.host: {
        "paths": rule.http.paths | length,
        "tls": rule.host in tls_hosts
      }
    }) %}
{%- endfor %}
{# ns.config now contains all host configurations #}
```

**Why `{% set %}` is Required:**

Note that you MUST use `{% set ns.field = ns.field.method() %}` syntax. Using `{{ ns.field.method() }}` does NOT work:

- `{{ }}` evaluates and outputs the return value but doesn't perform assignment
- `{% set %}` evaluates and assigns the result to the namespace attribute
- Variables set inside loops are local to each iteration (Jinja2 scoping rules)
- Only namespace attributes persist across loop iterations

### JSONPath Escaping for Label Keys

**Problem**: Label keys with dots (e.g., `kubernetes.io/service-name`) break JSONPath parsing.

```yaml
# WRONG
indexBy: ["metadata.labels.kubernetes.io/service-name"]
# Error: JSONPath thinks "io" is a field of "kubernetes"

# CORRECT - escape dots with double backslash
indexBy: ["metadata.labels.kubernetes\\.io/service-name"]
```

**Why**: JSONPath uses `.` as field separator. Literal dots in field names must be escaped with `\\.` (double backslash because YAML also escapes).

### Whitespace Control

**Problem**: Unintended whitespace in generated HAProxy config.

```gonja
{# WRONG - generates extra blank lines #}
{% for item in items %}
backend {{ item.name }}
  server srv1 {{ item.ip }}:{{ item.port }}
{% endfor %}

{# CORRECT - strip whitespace with - #}
{%- for item in items %}
backend {{ item.name }}
  server srv1 {{ item.ip }}:{{ item.port }}
{%- endfor %}

{# CORRECT - preserve specific whitespace with + #}
backend {%+ include "backend-name" +%}  {# Preserves space before name #}
```

**Whitespace Control Characters**:
- `{%-`: Strip whitespace before tag
- `-%}`: Strip whitespace after tag
- `{%+`: Preserve whitespace before tag
- `+%}`: Preserve whitespace after tag

**Example Issue**: Backend name generation
```gonja
{# WRONG - creates "backend  name" (two spaces) #}
template: >-
  {{- " " -}}{{ name }}
# Used in: backend {%+ include "template" +%}
# Result: "backend " + " name" = "backend  name"

{# CORRECT #}
template: >-
  {{- "" -}}{{ name }}
# Result: "backend " + "name" = "backend name"
```

### Variable Scope in Loops and Conditionals

**Problem**: Variables defined inside blocks don't persist outside.

```gonja
{# WRONG - backend_name not available outside if block #}
{%- for ingress in ingresses %}
  {%- if ingress.metadata.annotations.auth %}
    {%- set backend_name = "auth_" ~ ingress.metadata.name %}
  {%- endif %}
  backend {{ backend_name }}  {# ERROR: backend_name undefined! #}
{%- endfor %}

{# CORRECT - define before conditional #}
{%- for ingress in ingresses %}
  {%- set backend_name = "default_" ~ ingress.metadata.name %}
  {%- if ingress.metadata.annotations.auth %}
    {%- set backend_name = "auth_" ~ ingress.metadata.name %}
  {%- endif %}
  backend {{ backend_name }}  {# Works! #}
{%- endfor %}

{# ALSO CORRECT - use namespace for complex cases #}
{%- for ingress in ingresses %}
  {%- set ns = namespace(backend_name="") %}
  {%- if ingress.metadata.annotations.auth %}
    {%- set ns.backend_name = "auth_" ~ ingress.metadata.name %}
  {%- else %}
    {%- set ns.backend_name = "default_" ~ ingress.metadata.name %}
  {%- endif %}
  backend {{ ns.backend_name }}
{%- endfor %}
```

### Filter vs Function Confusion

**Filters** (pipe syntax):
```gonja
{{ value | b64decode }}
{{ string | upper }}
{{ list | length }}
```

**Functions** (call syntax):
```gonja
{{ fail("error message") }}
{{ range(1, 10) }}
```

**Macros** (must import first):
```gonja
{%- from "macros" import my_macro -%}
{{ my_macro(value) }}
```

**Common Mistake**:
```gonja
{# WRONG - can't call filters as functions #}
{{ b64decode(value) }}

{# CORRECT #}
{{ value | b64decode }}
```

### Base64 Decoding Secret Data

**Important**: Kubernetes Secret `data` fields are already base64-encoded.

```gonja
{# Secret in Kubernetes #}
apiVersion: v1
kind: Secret
data:
  password: JGFwcjEk...  # This is base64-encoded

{# CORRECT - decode to get actual value #}
{%- set password = secret.data.password | b64decode %}
user admin password {{ password }}

{# WRONG - using encoded value directly #}
user admin password {{ secret.data.password }}  # Will be gibberish!
```

**Format Gotchas**:
- HAProxy auth secrets: Value should be hash only (e.g., `$2y$05$...`)
- NOT htpasswd format: Don't include `username:` prefix
- Generate correctly: `htpasswd -nbB user pass | cut -d: -f2 | base64 -w0`

### Set Assignment to Discard Return Values

**Problem**: Some methods return `None` but template expects no output.

```gonja
{# WRONG - append() returns None which gets rendered #}
{%- set ns = namespace(items=[]) %}
{{ ns.items.append("item") }}  {# Renders "None" in output! #}

{# CORRECT - assign to _ to discard return value #}
{%- set _ = ns.items.append("item") %}  {# No output #}
```

**When Needed**:
- `list.append(item)` → returns `None`
- `dict.update(other)` → returns `None`
- Any method that modifies in-place

### Include Path Resolution

**Problem**: Template not found errors with includes.

```gonja
{# WRONG - looking for file path #}
{% include "libraries/snippet.yaml" %}

{# CORRECT - snippet name from templateSnippets #}
{% include "snippet-name" %}
```

**How Includes Work**:
1. `{% include "name" %}` looks for `templateSnippets.name`
2. Not file paths - all snippets merged into one namespace
3. Use `include_matching("pattern-*")` macro for glob patterns

### Debugging Template Rendering

**Useful Techniques**:

```gonja
{# 1. Print variable type and value #}
{#- DEBUG: {{ variable }} (type: {{ variable | type }}) -#}

{# 2. Conditional debugging #}
{%- if debug | default(false) %}
  {#- DEBUG: Processing {{ item.name }} -#}
{%- endif %}

{# 3. Comment-based markers #}
{#- === START: processing ingresses === -#}
{%- for ingress in ingresses %}
  ...
{%- endfor %}
{#- === END: processing ingresses === -#}

{# 4. Generate debug comments in output #}
# DEBUG: Generated by template {{ template_name }}
# DEBUG: Resource count: {{ resources | length }}
```

**Using Controller Logs**:
```bash
# Run with debug to see template rendering
./bin/controller validate -f config.yaml 2>&1 | grep "component=test-runner"
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

**Solution**: Only define `templateSnippets`, let base.yaml call them via `{% include %}`.

### Testing Individual Library Files

**Problem**: Running `controller validate` directly on a library file.

```bash
# WRONG - library file is incomplete!
./bin/controller validate -f charts/haproxy-template-ic/libraries/ingress.yaml
```

**Why Bad**: Library files are meant to be merged. Testing them individually will fail because:
- Missing base template (`haproxyConfig`)
- Missing snippets from other libraries
- Missing watched resources from other libraries

**Solution**: Always test the merged Helm output:

```bash
# CORRECT
helm template charts/haproxy-template-ic \
  | yq 'select(.kind == "HAProxyTemplateConfig")' \
  | ./bin/controller validate -f -
```

### Missing watchedResources

**Problem**: Template uses resources not declared in `watchedResources`.

```yaml
templateSnippets:
  my-snippet:
    template: |
      {%- for svc in resources.services.List() %}
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
charts/haproxy-template-ic/
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
    └── haproxy-template-ic.gitlab.io_haproxytemplateconfigs.yaml
```

## Debugging Tips

### View Merged Template Output

```bash
# See the complete merged HAProxyTemplateConfig
helm template charts/haproxy-template-ic \
  | yq 'select(.kind == "HAProxyTemplateConfig")'
```

### Check Template Snippet Merging

```bash
# Extract just the templateSnippets section
helm template charts/haproxy-template-ic \
  | yq 'select(.kind == "HAProxyTemplateConfig") | .spec.templateSnippets | keys'
```

### Verify watchedResources

```bash
# See which resources will be watched
helm template charts/haproxy-template-ic \
  | yq 'select(.kind == "HAProxyTemplateConfig") | .spec.watchedResources | keys'
```

### Test Specific Library Combinations

```bash
# Disable all libraries, enable only one
helm template charts/haproxy-template-ic \
  --set controller.templateLibraries.ingress.enabled=false \
  --set controller.templateLibraries.gateway.enabled=false \
  --set controller.templateLibraries.haproxytech.enabled=true \
  | yq 'select(.kind == "HAProxyTemplateConfig")'
```

## Resources

- Helm template reference: https://helm.sh/docs/chart_template_guide/
- yq documentation: https://github.com/mikefarah/yq
- HAProxyTemplateConfig CRD: `crds/haproxy-template-ic.gitlab.io_haproxytemplateconfigs.yaml`
- Controller validation: `pkg/controller/testrunner/CLAUDE.md`
- Template engine: `pkg/templating/CLAUDE.md`
