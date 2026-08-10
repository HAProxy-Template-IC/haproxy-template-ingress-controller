# Validation tests

## Overview

Validation tests render your templates against fixture resources and assert on the output — broken templates and invalid HAProxy config fail before they reach a cluster. Tests are embedded in the HAProxyTemplateConfig CRD. You run them locally with the CLI (this page), and the controller also runs them automatically before any config reaches HAProxy.

Beyond running the controller (`haptic-controller run`), the controller binary provides `validate` (this page) and `benchmark` (template render timing). To audit another controller's Ingresses before switching to HAPTIC, use the migration report in the [playground](/playground/) — see [Migrating: Step 0](migrating.md#step-0-check-what-changes).

!!! note "Tests also run automatically before deployment"
    The same suite runs at two gates besides the CLI, so a config whose tests fail never reaches HAProxy:

    - **Live config change** — the controller re-runs the suite whenever a config changes. A change whose tests fail is refused, `haptic_config_rejected_total{validator="validationtests"}` increments, and the last-good config keeps serving. The budget scales with suite size (a 25s floor plus ~100 ms per test), so a large suite isn't cut off mid-run.
    - **Startup load gate** — the suite also runs on every fresh or upgraded controller pod, with a much larger budget since there is no scatter-gather deadline. A failing initial config crash-loops the pod rather than serving untested config, and the reason is stamped on `status.conditions[Validated]` with reason `LoadGateFailed`.

    There is **no** admission webhook for `HAProxyTemplateConfig` — a configuration is a set (the config plus its `libraryRefs` libraries), and admission sees one object at a time, so a per-object webhook would deny change sets whose end state is correct. To gate a config *before* it reaches the cluster, run [`haptic-controller preflight`](operations/validate-before-deploy.md) in your pipeline.

    The `validate` CLI, `preflight`, and both in-cluster gates run the identical suite through the same runner, so a passing local `validate` run predicts a clean load.

## Quick start

1. Add a `validationTests` section to your HAProxyTemplateConfig:

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

2. Download `haptic-controller` for your platform from the [releases page](https://gitlab.com/haproxy-haptic/haptic/-/releases). The `validate` subcommand is the controller binary running in validation mode.

3. Run the tests:

    ```bash
    haptic-controller validate -f my-config.yaml
    ```

To validate the config currently deployed in your cluster instead of a local file:

```bash
haptic-controller config view --input --namespace haptic > /tmp/haptic-config.yaml
haptic-controller validate -f /tmp/haptic-config.yaml
```

A Helm install spreads the configuration across one object per enabled template
library, so dumping a single object would only give you part of it — `config view
--input` merges the set the same way the controller does. Each library's tests
ship in that library's object and all of them run together as one suite against
the merged config, exactly as before.

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

## Test structure

Each test consists of:

| Component | Description |
|-----------|-------------|
| **Name** | Unique identifier (kebab-case, for example `test-ingress-tls-routing`) |
| **Description** | What the test verifies |
| **Fixtures** | Simulated Kubernetes resources |
| **Assertions** | Checks on rendered output |
| **HTTP fixtures** (`httpResources`) | Optional — mocked responses for `http.Fetch()` URLs (see [HTTP Fixtures](#http-fixtures)) |
| **Min HAProxy version** (`minHAProxyVersion`) | Optional — skip the test unless the HAProxy version under test is at least this (for version-gated features) |
| **Extra context** (`extraContext`) | Optional — per-test values deep-merged into the global `templatingSettings.extraContext`: nested maps merge key by key with per-test leaves winning, so overriding one key keeps its siblings. Pin every value your assertions depend on — a sibling you leave unset keeps its deployment-configured value. To pin an exact key set instead of merging, give the nested map `__replace__: true`: it replaces the deployment's map at that key wholesale, and the sentinel is stripped from the result |
| **Current config** (`currentConfig`) | Optional — an existing `haproxy.cfg` the render treats as the current config, exercising slot-preservation / reload-vs-runtime logic |
| **Current files** (`currentFiles`) | Optional — filename → content of the general files already deployed, exposed to templates as `currentFiles`; use it for templates that read their own prior output, such as self-rotating TLS session-ticket keys |
| **Requires** (`requires` / `requiresFields`) | Optional — strip the test when a watched resource or schema field is unavailable (see [Conditional Tests](#conditional-tests-requires-and-requiresfields)) |

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

### HTTP fixtures

Mock HTTP responses for templates using `http.Fetch()`:

```yaml
httpResources:
  - url: "http://blocklist.example.com/list.txt"
    content: |
      blocked-value-1
      blocked-value-2
```

Templates calling `http.Fetch()` for unmocked URLs fail with an error. Define shared HTTP fixtures in the `_global` test to make them available to all tests.

### Fixture keys

Fixture keys name `watchedResources` entries, with one reserved exception: `haproxy-pods` populates the auto-injected HAProxy pod store that templates read as `controller.haproxy_pods`. Its entries default to `apiVersion: v1` / `kind: Pod` and are indexed by namespace and name. Any other key fails the test with `resource type "<key>" in fixtures not found in watched resources`.

### The reserved `_global` entry

A test named `_global` is a shared baseline rather than a test. Its `fixtures`, `httpResources` and `extraContext` feed **every** test in the suite, and its own assertions are never executed — so it's the one place to put a fixture set several tests need. It's also the one test name that more than one object of a merged set may each contribute to; every other name must be unique across the merged set.

### Conditional Tests (`requires` and `requiresFields`)

`requires` lists `watchedResources` keys the test depends on. When an optional
watched resource named there is unavailable (no candidate API version served
by the cluster), the test is stripped from the effective configuration at load
time — the same mechanism `templateSnippets` use.

`requiresFields` goes one level deeper: a list of schema field paths in the
form `<watchedResourceKey>.<field.path>`:

```yaml
validationTests:
  test-httproute-cors-filter:
    requires: [httproutes]
    requiresFields: [httproutes.spec.rules.filters.cors]
    # ...
```

When any listed field is absent from the resolved schema generation of its
watched resource, the test is stripped at load time. This covers clusters
that serve the resource at the same API version as newer releases but with
an older schema generation lacking the field (for example, Gateway API v1.1
serves `httproutes` at `v1` without the Cross-Origin Resource Sharing (CORS) filter — the apiserver prunes
the field from fixtures, the feature never activates, and without stripping
the test would fail the fail-closed load gate). The first dot-segment must
name a `watchedResources` key; array levels in the remaining path are
descended transparently (`spec.rules.filters.cors` matches the field inside
the `rules[]` / `filters[]` items). The current stripping outcome is visible
at `/debug/vars/effectiveConfigResolution`.

## Assertion types

### Assertion Targets

The `contains`, `not_contains`, `match_count`, `equals`, and `match_order` assertion types share a `target` field selecting which rendered output to check (resolved by `pkg/controller/testrunner/assertion_helpers.go`):

| Target | What's checked |
|--------|----------------|
| `haproxy.cfg` (or empty) | The rendered main HAProxy configuration |
| `map:<name>` | A rendered map file. `<name>` matches against either the full path or the basename |
| `file:<name>` | A rendered general file (error pages, etc.), matched by filename |
| `cert:<name>` | A rendered SSL certificate, matched by basename |
| `crt-list:<name>` | A rendered crt-list file (registered by a template via `fileRegistry.Register("crt-list", …)`), matched by basename |
| `k8s:<template-name>` | The rendered YAML of a `spec.k8sResources` template (potentially multi-doc with `---`), so you can assert on emitted Kubernetes resources |
| `status:<ns>/<name>:<phase>` | The JSON status payload a `statusPatch()` call emitted for resource `<ns>/<name>` in the given pipeline phase (`rendered`, `deployed`, `renderFailed`, or `deployFailed`) — the way to test status-patch templates |
| `events` | The Kubernetes Events the templates recorded via `recordEvent()`, one per line as `<Type> <Reason> <apiVersion> <Kind> <ns>/<name>: <message>` |
| `rendering_error` | The simplified render error string, populated only when the render itself failed. Use this on negative tests where you expect rendering to be rejected |

!!! warning "Unknown targets fall back to `haproxy.cfg` silently"
    Typos in `target:` won't error — they'll just match the wrong content. Sanity-check via `--dump-rendered` if an assertion behaves unexpectedly.

### `haproxy_valid`

Validates HAProxy configuration syntax using the HAProxy binary:

```yaml
- type: haproxy_valid
  description: Configuration must be syntactically valid
```

Every test should include this assertion.

### `contains`

Verifies target content matches a regex pattern:

```yaml
- type: contains
  target: haproxy.cfg
  pattern: "backend api-production"
  description: Must create backend for API service
```

### `not_contains`

Verifies target content **doesn't** match a pattern:

```yaml
- type: not_contains
  target: haproxy.cfg
  pattern: "ssl-verify none"
  description: Must not disable SSL verification
```

### `equals`

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

### `jsonpath`

Evaluates a JSONPath expression against the template rendering context and compares the single result to `expected`. JSONPath reads plain values from the context — it can't invoke the store methods templates use (`resources.services.List()`), so it fits scalar context values (for example a `spec.templatingSettings.extraContext` key, which is injected into the context by name):

```yaml
- type: jsonpath
  jsonpath: "{.environment}"     # set via spec.templatingSettings.extraContext.environment
  expected: "production"
  description: extraContext.environment is wired through
```

To assert on watched resources or rendered output, use `contains`, `match_count`, or `equals` against `haproxy.cfg` (or a `map:` / `file:` target) instead.

### `match_count`

Asserts that a regex pattern matches an exact number of times in the target. Useful for catching duplicate or missing entries:

```yaml
- type: match_count
  target: haproxy.cfg
  pattern: "^backend "
  expected: "3"          # string — parsed as integer
  description: Exactly 3 backends must be generated
```

### `match_order`

Asserts that multiple patterns appear in the target in the listed order. Critical for HAProxy first-match-wins constructs (Gateway API route precedence, ordering of ACLs):

```yaml
- type: match_order
  target: map:path-prefix.map
  patterns:
    - "^/api/v2/users"   # must come before /api/v2
    - "^/api/v2"         # must come before /api
    - "^/api"
  description: Path map entries must be sorted most-specific-first
```

### `deterministic`

Renders the templates a second time with the same inputs and asserts the output is byte-for-byte identical. Catches unstable map ordering, time-dependent values, and other sources of non-determinism:

```yaml
- type: deterministic
  description: Repeated renders must produce identical output
```

The check covers `haproxy.cfg` and every auxiliary file the template produced; no `target` or `pattern` is needed.

## Running tests

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

# Typed watched-resource access — point at a directory of schemas
haptic-controller validate -f config.yaml --schema-dir tests/schemas
# Equivalent: HAPTIC_SCHEMA_DIR=tests/schemas haptic-controller validate ...
```

The `haptic-controller validate` command shells out to the `haproxy` binary on your `PATH` — both to detect the HAProxy version during setup (`haproxy -v`) and for the `haproxy_valid` assertions (`haproxy -c`). Install HAProxy locally (for example via your package manager) and ensure it's on `PATH`; if no `haproxy` is found, `validate` fails fast with a clear error (it doesn't silently fall back to a syntax-only check). To validate against a specific HAProxy version, run the matching per-version controller image, which bundles that version.

Templates that use typed watched-resource access need `--schema-dir` (or `HAPTIC_SCHEMA_DIR`); without it they fail at engine compile time with a "no schema for X" error, while untyped `dig()`-based templates validate fine — see [Templating — Typed Resource Access](./templating.md#typed-resource-access) for where schemas come from and what the repo's bundled `tests/schemas/` directory covers.

Exit code 0 means all tests passed.

### Run in CI

Run `validate` as a pipeline step to block a broken config before it merges. The job fails when `validate` exits non-zero, so a template error or a failing test stops the pipeline. Use the per-version controller image: it bundles both `haptic-controller` and the matching `haproxy` binary, so `haproxy_valid` assertions run with no extra setup. Pick the tag whose HAProxy version matches your deployment (see [HAProxy Versions](operations/haproxy-versions.md)).

GitLab CI (`.gitlab-ci.yml`) — override the image entrypoint so the job's `script` shell runs:

```yaml
validate-haptic-config:
  image:
    name: registry.gitlab.com/haproxy-haptic/haptic:0.2.0-alpha.1-haproxy3.4
    entrypoint: [""]
  script:
    - haptic-controller validate -f config.yaml
```

GitHub Actions (`.github/workflows/validate.yml`):

```yaml
jobs:
  validate:
    runs-on: ubuntu-latest
    container:
      image: registry.gitlab.com/haproxy-haptic/haptic:0.2.0-alpha.1-haproxy3.4
    steps:
      - uses: actions/checkout@v4
      - run: haptic-controller validate -f config.yaml
```

### Output example

```
✓ test-basic-routing (0.125s)
  ✓ HAProxy configuration must be syntactically valid
  ✓ Must have frontend

✗ test-tls-config (0.089s)
  ✗ Must have SSL certificate
    Error: pattern "ssl crt" not found in haproxy.cfg

Tests: 1 passed, 1 failed, 2 total (0.214s)
```

## Debugging failed tests

### `--verbose`

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

### `--dump-rendered`

Shows all rendered content after test results:

```bash
haptic-controller validate -f config.yaml --dump-rendered
```

### `--trace-templates`

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

### `--profile-includes`

Lists the slowest 20 `render` / `render_glob` / macro invocations with cumulative timing — useful when `--trace-templates` shows a slow top-level template and you need to find which include is responsible:

```bash
haptic-controller validate -f config.yaml --profile-includes
```

### `--debug-filters`

Logs every comparison made by sort filters (`sort_by`) and similar operations, with the input types and the comparison result. Useful when route precedence or map ordering doesn't match what you expected:

```bash
haptic-controller validate -f config.yaml --debug-filters
```

### Combining flags

```bash
# Comprehensive end-to-end debugging
haptic-controller validate -f config.yaml --verbose --dump-rendered --trace-templates --profile-includes
```

**Workflow**: start with `--verbose` to see *what* failed, add `--dump-rendered` to see the *full content* you produced, add `--trace-templates` (and optionally `--profile-includes`) to see *where* time is spent, and reach for `--debug-filters` only when sort behaviour itself is suspect.

## Testing strategies

### Test organization

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

### Testing template errors

A negative test passes when the render fails *as expected*. Assert on the `rendering_error` target (see [Assertion Targets](#assertion-targets)) so the deliberate `fail()` is treated as the pass condition — without it, the failed render marks the whole test red:

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

### Testing auxiliary files

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

## Best practices

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
| JSONPath returns no results | Check the path; `jsonpath` reads scalar context values (for example `extraContext` keys), not the resource stores — assert on resources with `contains` / `match_count` |

## Complete example

A full Ingress → Service routing config with its tests. Press **Run live**, then open the **tests** tab to watch every assertion evaluate:

<div class="pg-embed" markdown data-tab="tests" data-controls="tabs" data-title="Ingress routing with validation tests" data-height="560">

```yaml
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

</div>

## See also

- [Templating Guide](./templating.md) - Template syntax
- [Supported Configuration](./supported-configuration.md) - HAProxy directives
- [Troubleshooting](./troubleshooting.md) - Common issues
