# Validation Tests

## Overview

Validation tests render your templates against fixture resources and assert on the output — broken templates and invalid HAProxy config fail before they reach a cluster. Tests are embedded in the HAProxyTemplateConfig CRD and run locally using the CLI.

Beyond `run`, the controller binary provides `validate` (this page), `benchmark` (template render timing), and `migrate-check` (audit another controller's Ingresses before switching to HAPTIC — see [Migrating: Step 0](migrating.md#step-0-check-what-will-change)).

## Quick Start

`haptic-controller validate` is the controller binary running in validation mode. Download it for your platform from the [releases page](https://gitlab.com/haproxy-haptic/haptic/-/releases) and run it locally:

```bash
haptic-controller validate -f my-config.yaml
```

To validate the config currently deployed in your cluster:

```bash
kubectl get haproxytemplateconfig -n haptic haptic-config -o yaml > /tmp/haptic-config.yaml
haptic-controller validate -f /tmp/haptic-config.yaml
```

Add a `validationTests` section to your HAProxyTemplateConfig:

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: my-config
spec:
  # ... template configuration ...

  validationTests:
    test-basic-frontend:
      description: Frontend should be created with correct settings
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
      assertions:
        - type: haproxy_valid
          description: Configuration must be syntactically valid

        - type: contains
          target: haproxy.cfg
          pattern: "frontend.*default"
          description: Must have default frontend
```

Run tests:

```bash
haptic-controller validate -f my-config.yaml
```

Or run tests right here — this is a complete config with a `validationTests` block. Press **Run live**, then open the **tests** tab to see each assertion pass or fail:

<div class="pg-embed" markdown data-tab="tests" data-controls="tabs" data-title="Validation tests, live" data-height="560">

```yaml
watchedResources:
  services:
    apiVersion: v1
    resources: services
    indexBy:
      - metadata.namespace
      - metadata.name

haproxyConfig:
  template: |
    global
      maxconn 1000

    defaults
      mode http
      timeout connect 5s
      timeout client 30s
      timeout server 30s

    frontend http
      bind :8080
      default_backend not-found
    {%- for _, svc := range resources.services.List() %}
    backend {{ svc.metadata.namespace }}_{{ svc.metadata.name }}
      server app {{ svc.metadata.name }}.{{ svc.metadata.namespace }}.svc:80
    {%- end %}

    backend not-found
      http-request deny deny_status 404

# Tests render this config against fixture resources and check the output.
validationTests:
  one-backend-per-service:
    description: Each Service becomes its own backend
    fixtures:
      services:
        - apiVersion: v1
          kind: Service
          metadata:
            name: shop
            namespace: storefront
          spec:
            ports:
              - port: 80
    assertions:
      - type: haproxy_valid
        description: Rendered config is valid
      - type: contains
        target: haproxy.cfg
        pattern: "backend storefront_shop"
        description: A backend exists for the shop Service
      - type: match_count
        target: haproxy.cfg
        pattern: "(?m)^backend "
        expected: "2"
        description: Exactly two backends (shop + not-found)
```

<p class="pg-task" markdown>Add a second Service to the <code>fixtures</code> block, then bump the <code>match_count</code> assertion's <code>expected</code> to <code>"3"</code> and re-run — watch it stay green. Set it back to <code>"2"</code> to see the assertion turn red.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

The **tests** tab auto-runs on load: all three assertions pass (green). In the browser, `haproxy_valid` runs the pure-Go syntax + schema check — it's tagged **syntax + schema** because the `haproxy -c` binary can't run in a browser. Editing the config and pressing **↻ Re-run tests** re-evaluates every assertion.

</details>

</div>

## Test Structure

Each test consists of:

| Component | Description |
|-----------|-------------|
| **Name** | Unique identifier (kebab-case, e.g., `test-ingress-tls-routing`) |
| **Description** | What the test verifies |
| **Fixtures** | Simulated Kubernetes resources |
| **Assertions** | Checks on rendered output |
| **HTTP fixtures** (`httpResources`) | Optional — mocked responses for `http.Fetch()` URLs (see [HTTP Fixtures](#http-fixtures)) |
| **Min HAProxy version** (`minHAProxyVersion`) | Optional — skip the test unless the HAProxy version under test is at least this (for version-gated features) |
| **Extra context** (`extraContext`) | Optional — per-test values merged into the render context, overriding the global `templatingSettings.extraContext` |
| **Current config** (`currentConfig`) | Optional — an existing `haproxy.cfg` the render treats as the current config, exercising slot-preservation / reload-vs-runtime logic |

### Fixtures

Fixtures simulate Kubernetes resources:

```yaml
fixtures:
  services:
    - apiVersion: v1
      kind: Service
      metadata:
        name: api
        namespace: production
      spec:
        ports:
          - port: 80
  ingresses:
    - apiVersion: networking.k8s.io/v1
      kind: Ingress
      metadata:
        name: main
        namespace: production
      spec:
        rules:
          - host: api.example.com
            http:
              paths:
                - path: /
                  pathType: Prefix
                  backend:
                    service:
                      name: api
                      port:
                        number: 80
```

### HTTP Fixtures

Mock HTTP responses for templates using `http.Fetch()`:

```yaml
httpResources:
  - url: "http://blocklist.example.com/list.txt"
    content: |
      blocked-value-1
      blocked-value-2
```

Templates calling `http.Fetch()` for unmocked URLs fail with an error. Define shared HTTP fixtures in the `_global` test to make them available to all tests.

## Assertion Types

### haproxy_valid

Validates HAProxy configuration syntax using the HAProxy binary:

```yaml
- type: haproxy_valid
  description: Configuration must be syntactically valid
```

Every test should include this assertion.

### contains

Verifies target content matches a regex pattern:

```yaml
- type: contains
  target: haproxy.cfg
  pattern: "backend api-production"
  description: Must create backend for API service
```

**Targets** (resolved by `pkg/controller/testrunner/assertion_helpers.go`, shared by `contains` / `not_contains` / `match_count` / `equals` / `match_order`):

| Target | What's checked |
|--------|----------------|
| `haproxy.cfg` (or empty) | The rendered main HAProxy configuration |
| `map:<name>` | A rendered map file. `<name>` matches against either the full path or the basename |
| `file:<name>` | A rendered general file (error pages, etc.), matched by filename |
| `cert:<name>` | A rendered SSL certificate, matched by basename |
| `crt-list:<name>` | A rendered crt-list file, matched by basename. Requires HAProxy 3.2+ |
| `k8s:<template-name>` | The rendered YAML of a `spec.k8sResources` template (potentially multi-doc with `---`), so you can assert on emitted Kubernetes resources |
| `status:<ns>/<name>:<phase>` | The JSON status payload a `statusPatch()` call emitted for resource `<ns>/<name>` in the given pipeline phase (`rendered`, `deployed`, `renderFailed`, or `deployFailed`) — the way to test status-patch templates |
| `rendering_error` | The simplified render error string, populated only when the render itself failed. Use this on negative tests where you expect rendering to be rejected |

Unknown targets fall back to `haproxy.cfg` silently — typos in `target:` won't error, they'll just match the wrong content. Sanity-check via `--dump-rendered` if an assertion behaves unexpectedly.

### not_contains

Verifies target content does NOT match a pattern:

```yaml
- type: not_contains
  target: haproxy.cfg
  pattern: "ssl-verify none"
  description: Must not disable SSL verification
```

### equals

Checks entire content matches exactly:

```yaml
- type: equals
  target: map:hostnames.map
  expected: |
    api.example.com backend-api
    www.example.com backend-web
  description: Hostname map must match exactly
```

Use for small, deterministic files. Not recommended for large configs.

### jsonpath

Evaluates a JSONPath expression against the template rendering context and compares the single result to `expected`. JSONPath reads plain values from the context — it can't invoke the store methods templates use (`resources.services.List()`), so it fits scalar context values (for example a `spec.templatingSettings.extraContext` key, which is injected into the context by name):

```yaml
- type: jsonpath
  jsonpath: "{.environment}"     # set via spec.templatingSettings.extraContext.environment
  expected: "production"
  description: extraContext.environment is wired through
```

To assert on watched resources or rendered output, use `contains`, `match_count`, or `equals` against `haproxy.cfg` (or a `map:` / `file:` target) instead.

### match_count

Asserts that a regex pattern matches an exact number of times in the target. Useful for catching duplicate or missing entries:

```yaml
- type: match_count
  target: haproxy.cfg
  pattern: "^backend "
  expected: "3"          # string — parsed as integer
  description: Exactly 3 backends must be generated
```

### match_order

Asserts that multiple patterns appear in the target in the listed order. Critical for HAProxy first-match-wins constructs (Gateway API route precedence, ACL ordering):

```yaml
- type: match_order
  target: map:path-prefix.map
  patterns:
    - "^/api/v2/users"   # must come before /api/v2
    - "^/api/v2"         # must come before /api
    - "^/api"
  description: Path map entries must be sorted most-specific-first
```

### deterministic

Renders the templates a second time with the same inputs and asserts the output is byte-for-byte identical. Catches unstable map ordering, time-dependent values, and other sources of non-determinism:

```yaml
- type: deterministic
  description: Repeated renders must produce identical output
```

The check covers `haproxy.cfg` and every auxiliary file the template produced; no `target` or `pattern` is needed.

## Running Tests

```bash
# Run all tests
haptic-controller validate -f config.yaml

# Run specific test
haptic-controller validate -f config.yaml --test test-basic-routing

# Output formats
haptic-controller validate -f config.yaml --output json
haptic-controller validate -f config.yaml --output yaml

# Parallelism (0=auto-detect CPUs, 1=sequential)
haptic-controller validate -f config.yaml --workers 4

# Typed watched-resource access (needed when templates use *resources.X.T
# field access). Point at a directory of CRD YAMLs / OpenAPI v3 schemas;
# the repo bundles its Gateway API + haptic CRDs + K8s built-in schemas
# under tests/schemas/, which the chart-test script auto-wires.
haptic-controller validate -f config.yaml --schema-dir tests/schemas
# Equivalent: HAPTIC_SCHEMA_DIR=tests/schemas haptic-controller validate ...
```

The `haptic-controller validate` command shells out to the `haproxy` binary on your `PATH` — both to detect the HAProxy version during setup (`haproxy -v`) and for the `haproxy_valid` assertions (`haproxy -c`). Install HAProxy locally (e.g. via your package manager) and ensure it is on `PATH`; if no `haproxy` is found, `validate` fails fast with a clear error (it does not silently fall back to a syntax-only check). To validate against a specific HAProxy version, run the matching per-version controller image, which bundles that version.

If a template reaches for typed watched-resource access (`resources.gateways.List()` returning `[]*resources.gateways.T`, or a `case *resources.httproutes.T` type-switch branch) and no `--schema-dir` was supplied, validation fails at engine compile time with a clear "no schema for X" pointer back to the flag. Charts that stick to the untyped `resources["<name>"]` / `dig()` path validate fine without the flag.

Exit code 0 means all tests passed.

### Output Example

```
✓ test-basic-routing (0.125s)
  ✓ HAProxy configuration must be syntactically valid
  ✓ Must have frontend

✗ test-tls-config (0.089s)
  ✗ Must have SSL certificate
    Error: pattern "ssl crt" not found in haproxy.cfg

Tests: 1 passed, 1 failed, 2 total (0.214s)
```

## Debugging Failed Tests

### --verbose

Shows content preview for failed assertions:

```bash
haptic-controller validate -f config.yaml --verbose
```

```
✗ test-gateway-routing
  ✗ Path map must have correct weight
    Error: pattern "MULTIBACKEND:100:" not found in map:path-prefix.map
    Content preview:
      split.example.com/app MULTIBACKEND:0:default_split-route_0/
```

### --dump-rendered

Shows all rendered content after test results:

```bash
haptic-controller validate -f config.yaml --dump-rendered
```

### --trace-templates

Shows top-level template execution order and timing:

```bash
haptic-controller validate -f config.yaml --trace-templates
```

```
Rendering: haproxy.cfg
Completed: haproxy.cfg (0.007ms)
Rendering: path-prefix.map
Completed: path-prefix.map (3.347ms)
```

!!! note
    This shows only top-level template renders. To see the full call tree including
    `render_glob`, `render`, and macro invocations, combine with `--profile-includes`:

    ```bash
    haptic-controller validate -f config.yaml --trace-templates --profile-includes
    ```

### --profile-includes

Lists the slowest 20 `render` / `render_glob` / macro invocations with cumulative timing — useful when `--trace-templates` shows a slow top-level template and you need to find which include is responsible:

```bash
haptic-controller validate -f config.yaml --profile-includes
```

### --debug-filters

Logs every comparison made by sort filters (`sort_by`) and similar operations, with the input types and the comparison result. Useful when route precedence or map ordering doesn't match what you expected:

```bash
haptic-controller validate -f config.yaml --debug-filters
```

### Combining Flags

```bash
# Comprehensive end-to-end debugging
haptic-controller validate -f config.yaml --verbose --dump-rendered --trace-templates --profile-includes
```

**Workflow**: start with `--verbose` to see *what* failed, add `--dump-rendered` to see the *full content* you produced, add `--trace-templates` (and optionally `--profile-includes`) to see *where* time is spent, and reach for `--debug-filters` only when sort behaviour itself is suspect.

## Testing Strategies

### Test Organization

Group tests by feature:

```yaml
validationTests:
  # Basic functionality
  test-basic-http-routing:
    description: HTTP routing for simple service

  # TLS/SSL
  test-tls-termination:
    description: TLS termination with certificate

  # Edge cases
  test-empty-services:
    description: Handle case with no backend services
```

### Testing Template Errors

A negative test passes when the render fails *as expected*. Assert on the `rendering_error` target (see [contains targets](#contains)) so the deliberate `fail()` is treated as the pass condition — without it, the failed render marks the whole test red:

```yaml
test-no-services-error:
  description: Should fail when no services exist
  fixtures:
    services: []
  assertions:
    - type: contains
      target: rendering_error
      pattern: "no services configured"
      description: Render is rejected with the expected fail() message
```

### Testing Auxiliary Files

```yaml
test-hostname-map:
  description: Hostname map should contain all ingress hosts
  fixtures:
    ingresses:
      - metadata:
          name: main
        spec:
          rules:
            - host: api.example.com
  assertions:
    - type: contains
      target: map:hostnames.map
      pattern: "api.example.com"
```

## Best Practices

1. **Test early**: Add tests as you develop templates
2. **Keep tests fast**: Use minimal fixtures
3. **Be descriptive**: Name each test after the behaviour it checks and write the description as the requirement being verified
4. **Test edge cases**: Empty inputs, many inputs, invalid data

```yaml
# Good
test-ingress-tls-routing:
  description: Ingress with TLS should create HTTPS frontend

# Bad
test1:
  description: Test
```

## Troubleshooting

| Problem | Solution |
|---------|----------|
| "haproxy: command not found" | Install HAProxy locally (the validator invokes `haproxy -c` on your `PATH`) |
| "template rendering failed" | Check for undefined variables, missing filters |
| Pattern not matching | Escape regex chars, check whitespace, use simpler patterns |
| JSONPath returns no results | Check the path; jsonpath reads scalar context values (e.g. `extraContext` keys), not the resource stores — assert on resources with `contains` / `match_count` |

## Complete Example

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: ingress-routing
spec:
  watchedResources:
    services:
      apiVersion: v1
      resources: services
      indexBy: ["metadata.namespace", "metadata.name"]
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      indexBy: ["metadata.namespace", "metadata.name"]

  haproxyConfig:
    template: |
      global
        daemon

      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s

      frontend http
        bind :80
        {% for _, ingress := range resources.ingresses.List() %}
        {% for _, rule := range ingress.spec.rules %}
        acl host_{{ replace(rule.host, ".", "_") }} hdr(host) -i {{ rule.host }}
        use_backend {{ replace(rule.host, ".", "_") }}_backend if host_{{ replace(rule.host, ".", "_") }}
        {% end %}
        {% end %}

      {% for _, ingress := range resources.ingresses.List() %}
      {% for _, rule := range ingress.spec.rules %}
      backend {{ replace(rule.host, ".", "_") }}_backend
        balance roundrobin
        {% var svc_name = rule.http.paths[0].backend.service.name %}
        {% var svc = resources.services.GetSingle(ingress.metadata.namespace, svc_name) %}
        {% if svc != nil %}
        server svc1 {{ svc.spec.clusterIP }}:{{ svc.spec.ports[0].port }} check
        {% end %}
      {% end %}
      {% end %}

  validationTests:
    test-single-ingress:
      description: Single ingress should create frontend ACL and backend
      fixtures:
        services:
          - apiVersion: v1
            kind: Service
            metadata:
              name: api
              namespace: default
            spec:
              clusterIP: 10.0.0.100
              ports:
                - port: 80
        ingresses:
          - apiVersion: networking.k8s.io/v1
            kind: Ingress
            metadata:
              name: main
              namespace: default
            spec:
              rules:
                - host: api.example.com
                  http:
                    paths:
                      - path: /
                        backend:
                          service:
                            name: api
                            port:
                              number: 80
      assertions:
        - type: haproxy_valid
          description: Configuration must be valid

        - type: contains
          target: haproxy.cfg
          pattern: "acl host_api_example_com hdr\\(host\\) -i api.example.com"
          description: Must have ACL for api.example.com

        - type: contains
          target: haproxy.cfg
          pattern: "backend api_example_com_backend"
          description: Must have backend for api.example.com

        - type: contains
          target: haproxy.cfg
          pattern: "server svc1 10.0.0.100:80 check"
          description: Must have server pointing to service ClusterIP
```

## See Also

- [Templating Guide](./templating.md) - Template syntax
- [Supported Configuration](./supported-configuration.md) - HAProxy directives
- [Troubleshooting](./troubleshooting.md) - Common issues
