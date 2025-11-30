# Validation Tests

This guide explains how to write and run validation tests for HAProxyTemplateConfig templates.

## Overview

Validation tests allow you to verify that your templates render correctly and produce valid HAProxy configurations.
Tests are embedded directly in the HAProxyTemplateConfig CRD and can be executed locally using the CLI.

**Benefits:**

- Catch template errors before deployment
- Verify configuration changes don't break existing functionality
- Document expected behavior with concrete examples
- Test edge cases and error conditions

## Quick Start

Add a `validationTests` section to your HAProxyTemplateConfig:

```yaml
apiVersion: haproxy-template-ic.github.io/v1alpha1
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
                  targetPort: 8080
      assertions:
        - type: haproxy_valid
          description: Configuration must be syntactically valid

        - type: contains
          target: haproxy.cfg
          pattern: "frontend.*default"
          description: Must have default frontend
```

Run the tests:

```bash
controller validate -f my-config.yaml
```

## Test Structure

Each validation test consists of:

1. **Name**: Unique identifier for the test
2. **Description**: Human-readable explanation of what the test verifies
3. **Fixtures**: Test data (Kubernetes resources) to use during rendering
4. **Assertions**: Checks to perform on the rendered output

### Test Name

- Must be unique within the config
- Use lowercase with hyphens (kebab-case)
- Be descriptive: `test-ingress-tls-routing` not `test1`

### Fixtures

Fixtures simulate Kubernetes resources that would be watched by the controller:

```yaml
fixtures:
  services: # Resource type must match watchedResources config
    - apiVersion: v1
      kind: Service
      metadata:
        name: api
        namespace: production
      spec:
        ports:
          - port: 80
            targetPort: 8080

  ingresses:
    - apiVersion: networking.k8s.io/v1
      kind: Ingress
      metadata:
        name: main-ingress
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

**Fixture Guidelines:**

- Include only resources needed for the test
- Use realistic data that represents actual use cases
- Test both common cases and edge cases
- Keep fixtures minimal but complete

### HTTP Fixtures

For templates that use `http.Fetch()` to retrieve external content, you can provide mock HTTP responses using `httpResources`:

```yaml
validationTests:
  test-http-blocklist:
    description: Template should generate blocklist from HTTP-fetched content
    fixtures:
      ingresses:
        - apiVersion: networking.k8s.io/v1
          kind: Ingress
          metadata:
            name: my-ingress
            namespace: default
          # ...
    httpResources:
      - url: "http://blocklist.example.com/list.txt"
        content: |
          blocked-value-1
          blocked-value-2
          blocked-value-3
    assertions:
      - type: contains
        target: map:blocklist.map
        pattern: "blocked-value-1"
        description: Blocklist map should contain fetched values
```

When a template calls `http.Fetch("http://blocklist.example.com/list.txt")`, it receives the fixture content instead of making an actual HTTP request.

**Missing Fixture Error:**

If a template calls `http.Fetch()` for a URL that doesn't have a matching fixture, the test **fails with an error**:

```
Error: http.Fetch: no fixture defined for URL: http://example.com/data.txt
       (add an httpResources fixture for this URL)
```

This ensures all HTTP dependencies are explicitly mocked in tests.

**Global HTTP Fixtures:**

You can define HTTP fixtures in the `_global` test to share them across all tests:

```yaml
validationTests:
  _global:
    fixtures: {}
    httpResources:
      - url: "http://shared-config.example.com/config.json"
        content: |
          {"setting": "value"}
    assertions: []

  test-uses-shared-config:
    description: Test that uses globally defined HTTP fixture
    fixtures:
      services:
        - # ...
    # httpResources from _global are automatically available
    assertions:
      - type: haproxy_valid
```

Test-specific HTTP fixtures override global fixtures for the same URL.

**HTTP Fixture Properties:**

| Property | Required | Description |
|----------|----------|-------------|
| `url` | Yes | The HTTP URL that will be matched when templates call `http.Fetch()` |
| `content` | Yes | The response body content to return |

**Notes:**

- HTTP fixtures are matched by exact URL (no pattern matching)
- Options passed to `http.Fetch()` (delay, timeout, auth) are ignored in test mode
- Empty content is valid: `content: ""`

### Assertions

Five assertion types are available:

#### 1. haproxy_valid

Validates that the rendered HAProxy configuration is syntactically valid using the HAProxy binary.

```yaml
assertions:
  - type: haproxy_valid
    description: HAProxy configuration must be syntactically valid
```

**When to use:**

- Every test should include this assertion
- Catches syntax errors, invalid directives, missing sections

**Requirements:**

- HAProxy binary must be in PATH or specified with `--haproxy-binary`

#### 2. contains

Verifies that the target content matches a regex pattern.

```yaml
assertions:
  - type: contains
    target: haproxy.cfg
    pattern: "backend api-production"
    description: Must create backend for API service

  - type: contains
    target: haproxy.cfg
    pattern: "bind :443 ssl crt /etc/haproxy/ssl/cert.pem"
    description: HTTPS frontend must use SSL certificate
```

**Parameters:**

- `target`: What to check (`haproxy.cfg`, `map:<name>`, `file:<name>`, `cert:<name>`)
- `pattern`: Regular expression to match
- `description`: Why this pattern should exist

**Pattern Tips:**

- Use `.*` for wildcards: `frontend.*production`
- Escape special regex characters: `\.` for literal dot
- Keep patterns specific enough to catch regressions

#### 3. not_contains

Verifies that the target content does NOT match a pattern.

```yaml
assertions:
  - type: not_contains
    target: haproxy.cfg
    pattern: "server.*127.0.0.1"
    description: Should not have localhost servers in production

  - type: not_contains
    target: haproxy.cfg
    pattern: "ssl-verify none"
    description: Must not disable SSL verification
```

**Use cases:**

- Verify deprecated patterns are removed
- Ensure security-sensitive config is not present
- Check that test/debug settings don't leak to production

#### 4. equals

Checks that the entire target content exactly matches the expected value.

```yaml
assertions:
  - type: equals
    target: map:hostnames.map
    expected: |
      api.example.com backend-api
      www.example.com backend-web
    description: Hostname map must contain exactly these entries
```

**When to use:**

- Small, deterministic files (maps, simple configs)
- When order and whitespace matter
- Verifying complete file contents

**Not recommended for:**

- Large HAProxy configs (too brittle)
- Content with timestamps or dynamic values

#### 5. jsonpath

Queries the template rendering context using JSONPath expressions.

```yaml
assertions:
  - type: jsonpath
    jsonpath: "{.resources.services.List()[0].metadata.name}"
    expected: "my-service"
    description: First service should be my-service

  - type: jsonpath
    jsonpath: "{.template_snippets[0]}"
    expected: "logging"
    description: First snippet should be logging
```

**Template Context Structure:**

```json
{
  "resources": {
    "services": <StoreWrapper
    with
    .List()
    method>,
    "ingresses": <StoreWrapper>
  },
  "template_snippets": [
    "snippet1",
    "snippet2"
  ]
}
```

**JSONPath Examples:**

```yaml
# Count resources
- jsonpath: "{.resources.services.List() | length}"
  expected: "3"

# Check resource field
- jsonpath: "{.resources.ingresses.List()[0].spec.rules[0].host}"
  expected: "api.example.com"

# Verify snippet order
- jsonpath: "{.template_snippets[0]}"
  expected: "logging"
```

## Testing Strategies

### Test Organization

Group related tests by feature or scenario:

```yaml
validationTests:
  # Basic functionality
  test-basic-http-routing:
    description: HTTP routing for simple service
    # ...

  test-basic-load-balancing:
    description: Load balancing across multiple servers
    # ...

  # TLS/SSL
  test-tls-termination:
    description: TLS termination with certificate
    # ...

  test-tls-passthrough:
    description: TLS passthrough for end-to-end encryption
    # ...

  # Edge cases
  test-empty-services:
    description: Handle case with no backend services
    # ...

  test-invalid-port:
    description: Gracefully handle invalid port numbers
    # ...
```

### Testing Template Errors

Test that templates fail gracefully with `fail()` function:

```jinja
{% if not services.List() %}
  {{ fail("At least one service is required") }}
{% endif %}
```

Test file:

```yaml
validationTests:
  test-no-services-error:
    description: Should fail gracefully when no services exist
    fixtures:
      services: [ ]  # Empty services
    assertions:
    # This test will fail at rendering stage
    # The test runner will capture the fail() message
```

Expected output:

```
✗ test-no-services-error
  ✗ Template rendering failed
    Error: At least one service is required
```

### Testing Multiple Scenarios

Use fixtures to test different deployment configurations:

```yaml
validationTests:
  # Production scenario
  test-production-setup:
    fixtures:
      services:
        - name: api
          namespace: production
        - name: web
          namespace: production
    assertions:
      - type: contains
        pattern: "production"

  # Staging scenario
  test-staging-setup:
    fixtures:
      services:
        - name: api
          namespace: staging
    assertions:
      - type: contains
        pattern: "staging"

  # Development scenario
  test-dev-setup:
    fixtures:
      services:
        - name: api
          namespace: dev
    assertions:
      - type: contains
        pattern: "maxconn 100"  # Lower limits for dev
```

### Testing Auxiliary Files

Test maps, general files, and SSL certificates:

```yaml
validationTests:
  - name: test-hostname-map
    description: Hostname map should contain all ingress hosts
    fixtures:
      ingresses:
        - metadata:
            name: main
          spec:
            rules:
              - host: api.example.com
              - host: www.example.com
    assertions:
      - type: contains
        target: map:hostnames.map
        pattern: "api.example.com"

      - type: contains
        target: map:hostnames.map
        pattern: "www.example.com"

  - name: test-error-page
    description: Custom error page should be generated
    fixtures:
      services: [ ]
    assertions:
      - type: contains
        target: file:500.http
        pattern: "Internal Server Error"
```

## Running Tests

### CLI Usage

```bash
# Run all tests
controller validate -f config.yaml

# Run specific test
controller validate -f config.yaml --test test-basic-routing

# Output as JSON (for CI/CD)
controller validate -f config.yaml --output json

# Output as YAML
controller validate -f config.yaml --output yaml

# Use custom HAProxy binary
controller validate -f config.yaml --haproxy-binary /usr/local/bin/haproxy
```

### Exit Codes

- **0**: All tests passed
- **Non-zero**: One or more tests failed

Use in CI/CD pipelines:

```bash
#!/bin/bash
controller validate -f config.yaml
if [ $? -ne 0 ]; then
  echo "Validation tests failed!"
  exit 1
fi
```

### Output Formats

**Summary (default):**

```
✓ test-basic-routing (0.125s)
  Basic HTTP routing configuration
  ✓ HAProxy configuration must be syntactically valid
  ✓ Must have frontend
  ✓ Must have backend

✗ test-tls-config (0.089s)
  TLS configuration
  ✗ Must have SSL certificate
    Error: pattern "ssl crt" not found in haproxy.cfg

Tests: 1 passed, 1 failed, 2 total (0.214s)
```

**JSON:**

```json
{
  "totalTests": 2,
  "passedTests": 1,
  "failedTests": 1,
  "duration": 0.214,
  "tests": [
    {
      "testName": "test-basic-routing",
      "description": "Basic HTTP routing configuration",
      "passed": true,
      "duration": 0.125,
      "assertions": [
        ...
      ]
    }
  ]
}
```

**YAML:**

```yaml
totalTests: 2
passedTests: 1
failedTests: 1
duration: 0.214
tests:
  - testName: test-basic-routing
    description: Basic HTTP routing configuration
    passed: true
    duration: 0.125
    assertions: [ ... ]
```

## Debugging Failed Tests

When validation tests fail, the `controller validate` command provides several flags to help diagnose issues quickly.

### Quick Debugging with --verbose

The `--verbose` flag shows rendered content preview for failed assertions:

```bash
controller validate -f config.yaml --verbose
```

**Example output:**

```
✗ test-gateway-routing (0.004s)
  ✗ Path map must use MULTIBACKEND qualifier with total weight 100
    Error: pattern "split.example.com/app MULTIBACKEND:100:default_split-route_0/" not found in map:path-prefix.map (target size: 61 bytes). Hint: Use --verbose to see content preview
    Target: map:path-prefix.map (61 bytes)
    Content preview:
      split.example.com/app MULTIBACKEND:0:default_split-route_0/

    Hint: Use --dump-rendered to see full content
```

**What it shows:**
- Target file name and size (61 bytes)
- First 200 characters of actual rendered content
- Hint about `--dump-rendered` for full content

**When to use:**
- First step when tests fail
- Quick check of what was actually rendered vs expected
- Debugging pattern mismatches or unexpected values

### Complete Output with --dump-rendered

The `--dump-rendered` flag outputs all rendered content after test results:

```bash
controller validate -f config.yaml --dump-rendered
```

**Output structure:**

```
Tests: 0 passed, 1 failed, 1 total (0.003s)

================================================================================
RENDERED CONTENT
================================================================================

## Test: test-gateway-routing

### haproxy.cfg
--------------------------------------------------------------------------------
global
  daemon
  maxconn 4000

defaults
  mode http
  timeout connect 5s
--------------------------------------------------------------------------------

### Map Files

#### path-prefix.map
--------------------------------------------------------------------------------
split.example.com/app MULTIBACKEND:0:default_split-route_0/

--------------------------------------------------------------------------------
```

**What it shows:**
- Complete HAProxy configuration
- All map files with names
- General files (error pages, etc.)
- SSL certificates

**When to use:**
- Need to see complete rendered output
- Debugging complex template logic
- Verifying exact output format
- Creating bug reports with full context

### Template Execution Trace

The `--trace-templates` flag shows which templates were rendered and timing information:

```bash
controller validate -f config.yaml --trace-templates
```

**Output:**

```
TEMPLATE EXECUTION TRACE
================================================================================
Rendering: haproxy.cfg
Completed: haproxy.cfg (0.007ms)
Rendering: path-prefix.map
Completed: path-prefix.map (3.347ms)
Rendering: weighted-multi-backend.map
Completed: weighted-multi-backend.map (2.105ms)
```

**What it shows:**
- Order of template rendering
- Duration for each template in milliseconds
- Nesting depth for includes (shown via indentation)

**When to use:**
- Understanding template execution order
- Performance debugging (identify slow templates)
- Verifying template includes work correctly
- Debugging missing or unexpected renders

### Enhanced Default Error Messages

All error messages now include helpful context without requiring any flags:

**Before (old format):**
```
Error: pattern "X" not found in map:path-prefix.map
```

**After (enhanced format):**
```
Error: pattern "X" not found in map:path-prefix.map (target size: 61 bytes). Hint: Use --verbose to see content preview
```

**Benefits:**
- Immediate visibility into target size
- Clear hints about available debugging options
- No need to re-run with flags for basic info

### Combining Flags

Flags can be combined for comprehensive debugging:

```bash
controller validate -f config.yaml --verbose --dump-rendered --trace-templates
```

This provides:
1. Content previews for all failed assertions (`--verbose`)
2. Complete rendered files (`--dump-rendered`)
3. Template execution trace (`--trace-templates`)

**Recommended workflow:**
1. Start with `--verbose` for quick diagnosis
2. Add `--dump-rendered` if you need full content
3. Add `--trace-templates` for performance or execution flow issues

### Structured Output with Rendered Content

JSON and YAML output formats now include rendered content and target metadata:

```bash
controller validate -f config.yaml --output yaml
```

**New fields in output:**

```yaml
tests:
  - testName: test-gateway-routing
    passed: false
    # Rendered content (available for all tests)
    renderedConfig: |
      global
        daemon
    renderedMaps:
      path-prefix.map: |
        split.example.com/app MULTIBACKEND:0:default_split-route_0/
    renderedFiles: {}
    renderedCerts: {}

    assertions:
      - type: contains
        passed: false
        error: "pattern not found"
        # Target metadata (available for all assertions)
        target: "map:path-prefix.map"
        targetSize: 61
        targetPreview: "split.example.com/app MULTIBACKEND:0:..."
```

**Use cases:**
- CI/CD integration with detailed failure context
- Automated debugging scripts
- Archiving test results with full rendered content

### Troubleshooting Tips

**Map file appears empty or has unexpected content:**

```bash
# See exactly what was rendered
controller validate -f config.yaml --dump-rendered
```

Check for:
- Missing template logic
- Empty loops (no resources match)
- Incorrect variable names

**Template not generating expected output:**

```bash
# See which templates were actually rendered
controller validate -f config.yaml --trace-templates
```

If a template is missing from the trace:
- Check template name spelling
- Verify includes are correct
- Check conditional logic preventing execution

**Pattern not matching rendered content:**

```bash
# See actual content vs expected pattern
controller validate -f config.yaml --verbose
```

Common issues:
- Whitespace differences (extra newlines, spaces)
- Case sensitivity in patterns
- Regex special characters need escaping
- Multiline patterns require `(?m)` flag

**Slow validation or timeouts:**

```bash
# Identify slow templates
controller validate -f config.yaml --trace-templates
```

Templates taking >10ms may need optimization:
- Simplify complex loops
- Reduce nested includes
- Cache expensive computations
- Split large templates

## Best Practices

### 1. Test Early and Often

Add tests as you develop templates:

```bash
# Development workflow
vim config.yaml  # Edit templates
controller validate -f config.yaml  # Run tests
# Fix any failures, repeat
```

### 2. Keep Tests Fast

- Use minimal fixtures (only what's needed)
- Avoid excessive pattern matching
- Group related assertions in single tests

### 3. Make Tests Readable

```yaml
# Good: Clear, descriptive
- name: test-ingress-tls-routing
  description: Ingress with TLS should create HTTPS frontend and route to backend
  assertions:
    - type: contains
      pattern: "bind :443 ssl"
      description: HTTPS frontend must bind to port 443 with SSL

# Bad: Unclear
- name: test1
  description: Test
  assertions:
    - type: contains
      pattern: "443"
```

### 4. Test Edge Cases

```yaml
validationTests:
  # Normal case
  - name: test-single-service
    fixtures:
      services: [ ... ]
    # ...

  # Edge case: no services
  - name: test-no-services
    fixtures:
      services: [ ]
    # ...

  # Edge case: many services
  - name: test-many-services
    fixtures:
      services: [ ... 50 services ... ]
    # ...
```

### 5. Document Expected Behavior

Use test descriptions to document template behavior:

```yaml
- name: test-weighted-load-balancing
  description: |
    Services with weight annotation should use weighted round-robin.
    Weight is specified via 'haproxy.weight' annotation.
    Default weight is 1 if not specified.
```

## Troubleshooting

### Test Fails with "haproxy: command not found"

**Problem**: HAProxy binary not in PATH.

**Solution**: Specify binary location:

```bash
controller validate -f config.yaml --haproxy-binary /usr/local/bin/haproxy
```

### Test Fails with "template rendering failed"

**Problem**: Template syntax error or failed assertion from `fail()`.

**Solution**: Check the error message for details. Common issues:

- Undefined variables: `{{ undefined_var }}`
- Missing filters: `{{ value | missing_filter }}`
- Logic errors in conditionals

### Pattern Not Matching

**Problem**: `contains` assertion fails but content looks correct.

**Solution**:

1. Check regex syntax (escape special characters)
2. Check for whitespace differences
3. Use simpler patterns: `api` instead of `backend\s+api.*`
4. Add `-o json` to see full rendered config

### JSONPath Returns No Results

**Problem**: JSONPath assertion fails with "returned no results".

**Solution**:

- Verify the path syntax: `{.resources.services}`
- Check if the resource type exists in fixtures
- Use `List()` method to access resources: `{.resources.services.List()}`

## Examples

### Complete Example: Ingress Routing

```yaml
apiVersion: haproxy-template-ic.github.io/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: ingress-routing
spec:
  watchedResources:
    services:
      apiVersion: v1
      resources: services
      indexBy: [ "metadata.namespace", "metadata.name" ]
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      indexBy: [ "metadata.namespace", "metadata.name" ]

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
        {% for ingress in resources.ingresses.List() %}
        {% for rule in ingress.spec.rules %}
        acl host_{{ rule.host | replace(".", "_") }} hdr(host) -i {{ rule.host }}
        use_backend {{ rule.host | replace(".", "_") }}_backend if host_{{ rule.host | replace(".", "_") }}
        {% endfor %}
        {% endfor %}

      {% for ingress in resources.ingresses.List() %}
      {% for rule in ingress.spec.rules %}
      backend {{ rule.host | replace(".", "_") }}_backend
        balance roundrobin
        {% set svc_name = rule.http.paths[0].backend.service.name %}
        {% set svc = resources.services.Get(ingress.metadata.namespace, svc_name) %}
        {% if svc %}
        server svc1 {{ svc.spec.clusterIP }}:{{ svc.spec.ports[0].port }} check
        {% endif %}
      {% endfor %}
      {% endfor %}

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

## Next Steps

- Read the [Template Guide](./templating.md) for template syntax
- See [Supported Configuration](./supported-configuration.md) for HAProxy directives
- Check [Troubleshooting](./troubleshooting.md) for common issues
