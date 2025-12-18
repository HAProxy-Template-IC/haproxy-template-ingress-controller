# Validation Tests

## Overview

Validation tests verify that your templates render correctly and produce valid HAProxy configurations. Tests are embedded in the HAProxyTemplateConfig CRD and run locally using the CLI.

## Quick Start

Add a `validationTests` section to your HAProxyTemplateConfig:

```yaml
apiVersion: haproxy-template-ic.gitlab.io/v1alpha1
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
controller validate -f my-config.yaml
```

## Test Structure

Each test consists of:

| Component | Description |
|-----------|-------------|
| **Name** | Unique identifier (kebab-case, e.g., `test-ingress-tls-routing`) |
| **Description** | What the test verifies |
| **Fixtures** | Simulated Kubernetes resources |
| **Assertions** | Checks on rendered output |

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

**Targets**: `haproxy.cfg`, `map:<name>`, `file:<name>`, `cert:<name>`

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

Queries template rendering context:

```yaml
- type: jsonpath
  jsonpath: "{.resources.services.List()[0].metadata.name}"
  expected: "my-service"
  description: First service should be my-service
```

## Running Tests

```bash
# Run all tests
controller validate -f config.yaml

# Run specific test
controller validate -f config.yaml --test test-basic-routing

# Output formats
controller validate -f config.yaml --output json
controller validate -f config.yaml --output yaml

# Custom HAProxy binary
controller validate -f config.yaml --haproxy-binary /usr/local/bin/haproxy
```

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
controller validate -f config.yaml --verbose
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
controller validate -f config.yaml --dump-rendered
```

### --trace-templates

Shows top-level template execution order and timing:

```bash
controller validate -f config.yaml --trace-templates
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
    controller validate -f config.yaml --trace-templates --profile-includes
    ```

### Combining Flags

```bash
controller validate -f config.yaml --verbose --dump-rendered --trace-templates
```

**Workflow**: Start with `--verbose`, add `--dump-rendered` for full content, add `--trace-templates` for execution flow.

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

Test `fail()` assertions:

```yaml
test-no-services-error:
  description: Should fail when no services exist
  fixtures:
    services: []
  # Test fails at rendering with fail() message
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
3. **Be descriptive**: Clear names and descriptions
4. **Test edge cases**: Empty inputs, many inputs, invalid data
5. **Document behavior**: Use descriptions to explain expected behavior

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
| "haproxy: command not found" | Use `--haproxy-binary /path/to/haproxy` |
| "template rendering failed" | Check for undefined variables, missing filters |
| Pattern not matching | Escape regex chars, check whitespace, use simpler patterns |
| JSONPath returns no results | Verify path syntax, use `.List()` for resources |

## Complete Example

```yaml
apiVersion: haproxy-template-ic.gitlab.io/v1alpha1
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
