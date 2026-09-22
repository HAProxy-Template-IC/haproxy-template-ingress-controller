# Validation test reference

Look up fixture fields, test requirements, and assertion types. For a first test
and commands to run it, use [Test your templates](validation-tests.md).

Define tests under `controller.config.validationTests` in Helm values or
`spec.validationTests` in an `HAProxyTemplateConfig` or `HAProxyTemplateLibrary`.

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
| **Current servers** (`currentServers`) | Optional — the servers a previous deployment had, keyed by backend and server name, exposed to templates as `currentConfig.ServerIndex`; use it to exercise slot-preservation logic (see [Current servers](#current-servers)) |
| **Current config** (`currentConfig`) | Deprecated — a raw `haproxy.cfg` the runner parses down to the same server index. Use `currentServers` |
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

### Current servers

Use `currentServers` to simulate a previous deployment. Key entries by backend
name, then server name. Templates read this data through `currentConfig.ServerIndex`.
For example, test that existing pods retain their server names during a rollout:

```yaml
currentServers:
  default_api_svc_api_80:
    api-pod-1: {address: 10.0.0.1, port: 8080}
    api-pod-2: {address: 10.0.0.2, port: 8080}
```

Without `currentServers`, `currentConfig` is nil — the first-deployment case.

The deprecated `currentConfig` fixture field accepts a raw `haproxy.cfg` and
extracts its server index. Use `currentServers` for new tests. Setting both fields
fails the test.

### Fixture keys

Fixture keys name `watchedResources` entries, with one reserved exception: `haproxy-pods` populates the auto-injected HAProxy pod store that templates read as `controller.haproxy_pods`. Its entries default to `apiVersion: v1` / `kind: Pod` and are indexed by namespace and name. Any other key fails the test with `resource type "<key>" in fixtures not found in watched resources`.

### The reserved `_global` entry

Put shared `fixtures`, `httpResources`, and `extraContext` in an entry named
`_global`. They apply to every test; assertions on `_global` don't run. Multiple
libraries can contribute to this entry. All other test names must be unique
across the merged configuration.

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

HAPTIC skips the test if any listed field is missing from the resource's schema.
This lets a library support older CRD versions that lack a feature. The first
path segment must name a `watchedResources` entry. Array levels are implicit:
`httproutes.spec.rules.filters.cors` addresses the CORS field inside each rule's
filters. Inspect skipped tests at `/debug/vars/effectiveConfigResolution`.

## Assertion types

### Assertion Targets

The `contains`, `not_contains`, `match_count`, `equals`, and `match_order` assertion types share a `target` field selecting which rendered output to check:

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

Include this assertion when the test expects valid HAProxy output. Tests that
expect a render error instead assert that error; they can't also assert valid output.

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
  pattern: "(?m)^backend "
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
