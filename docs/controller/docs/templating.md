# Templating Guide

## Overview

The HAProxy Template Ingress Controller uses Gonja v2, a Jinja2-like template engine for Go, to generate HAProxy configurations from Kubernetes resources. You define templates that access watched Kubernetes resources, and the controller renders these templates whenever resources change, validates the output, and deploys it to HAProxy instances.

Templates are rendered automatically when any watched resource changes, during initial synchronization, or periodically for drift detection.

## What You Can Template

### HAProxy Configuration

The main `haproxyConfig` template generates the complete HAProxy configuration file:

```yaml
haproxyConfig:
  template: |
    global
        log stdout len 4096 local0 info
        daemon
        maxconn 4096

    defaults
        mode http
        timeout connect 5s
        timeout client 50s
        timeout server 50s

    frontend http
        bind *:80
        use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

    {% for ingress in resources.ingresses.List() %}
    backend {{ ingress.metadata.name }}
        balance roundrobin
    {% endfor %}
```

!!! important
    All auxiliary file references must use **absolute paths** from `/etc/haproxy/`. Use `pathResolver.GetPath()` to generate correct paths.

### Map Files

Map files generate HAProxy lookup tables stored in `/etc/haproxy/maps/`:

```yaml
maps:
  host.map:
    template: |
      {%- for ingress in resources.ingresses.List() %}
      {%- for rule in (ingress.spec.rules | default([]) | selectattr("http", "defined")) %}
      {{ rule.host }} {{ rule.host }}
      {%- endfor %}
      {%- endfor %}
```

### General Files

Auxiliary files like custom error pages stored in `/etc/haproxy/general/`:

```yaml
files:
  503.http:
    template: |
      HTTP/1.0 503 Service Unavailable
      Cache-Control: no-cache
      Connection: close
      Content-Type: text/html

      <html><body><h1>503 Service Unavailable</h1></body></html>
```

### SSL Certificates

SSL/TLS certificate files from Kubernetes Secrets stored in `/etc/haproxy/ssl/`:

```yaml
sslCertificates:
  example-com.pem:
    template: |
      {%- set secret = resources.secrets.GetSingle("default", "example-com-tls") %}
      {%- if secret %}
      {{ secret.data["tls.crt"] | b64decode }}
      {{ secret.data["tls.key"] | b64decode }}
      {%- endif %}
```

!!! note
    Certificate data in Secrets is base64-encoded. Use the `b64decode` filter to decode it.

### Template Snippets

Reusable template fragments included via `{% include "snippet-name" %}`:

```yaml
templateSnippets:
  backend-name:
    template: >-
      ing_{{ ingress.metadata.namespace }}_{{ ingress.metadata.name }}

  backend-servers:
    template: |
      {%- for endpoint_slice in resources.endpoints.Fetch(service_name) %}
      {%- for endpoint in (endpoint_slice.endpoints | default([])) %}
      {%- for address in (endpoint.addresses | default([])) %}
      server {{ endpoint.targetRef.name }} {{ address }}:{{ port }} check
      {%- endfor %}
      {%- endfor %}
      {%- endfor %}
```

## Template Syntax

Templates use Gonja v2 with Jinja2-like syntax. For complete syntax reference, see the [Gonja documentation](https://github.com/nikolalohinski/gonja).

### Control Structures

```jinja2
{# Loops #}
{% for ingress in resources.ingresses.List() %}
  backend {{ ingress.metadata.name }}
{% endfor %}

{# Conditionals #}
{% if ingress.spec.tls %}
  bind *:443 ssl crt {{ pathResolver.GetPath(ingress.metadata.name ~ ".pem", "cert") }}
{% endif %}

{# Variables #}
{% set service_name = path.backend.service.name %}
{% set port = path.backend.service.port.number | default(80) %}

{# Comments #}
{# This is a comment #}
```

### Common Filters

```jinja2
{{ path.backend.service.port.number | default(80) }}  {# Default values #}
{{ rule.host | lower }}                                {# String manipulation #}
{{ ingress.spec.rules | length }}                      {# Collection length #}
```

### Path Resolution

The `pathResolver.GetPath()` method resolves filenames to absolute paths:

```jinja2
{# Map files #}
use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]
{# Output: /etc/haproxy/maps/host.map #}

{# General files #}
errorfile 504 {{ pathResolver.GetPath("504.http", "file") }}
{# Output: /etc/haproxy/general/504.http #}

{# SSL certificates #}
bind *:443 ssl crt {{ pathResolver.GetPath("example.com.pem", "cert") }}
{# Output: /etc/haproxy/ssl/example.com.pem #}
```

**Arguments**: filename (required), type (`"map"`, `"file"`, `"cert"`, or `"crt-list"`)

### Custom Filters

| Filter | Description | Example |
|--------|-------------|---------|
| `b64decode` | Decode base64 strings | `{{ secret.data.password \| b64decode }}` |
| `glob_match` | Filter strings by glob pattern | `{{ snippets \| glob_match("backend-*") }}` |
| `regex_escape` | Escape regex special characters | `{{ path \| regex_escape }}` |
| `sort_by` | Sort by JSONPath expressions | `{{ routes \| sort_by(["$.priority:desc"]) }}` |
| `extract` | Extract values via JSONPath | `{{ routes \| extract("$.spec.rules[*].host") }}` |
| `group_by` | Group items by JSONPath | `{{ ingresses \| group_by("$.metadata.namespace") }}` |
| `transform` | Regex substitution on arrays | `{{ paths \| transform("^/api/v\\d+", "") }}` |
| `debug` | Output as JSON comment | `{{ routes \| debug("routes") }}` |
| `eval` | Show JSONPath evaluation | `{{ route \| eval("$.priority") }}` |

**sort_by modifiers**: `:desc` (descending), `:exists` (by field presence), `| length` (by length)

**Example - Route precedence sorting**:
```jinja2
{% set sorted = routes | sort_by([
    "$.match.method:exists:desc",
    "$.match.headers | length:desc",
    "$.match.path.value | length:desc"
]) %}
```

## Available Template Data

### The `resources` Variable

Templates access watched resources through the `resources` variable. Each store provides `List()`, `Fetch()`, and `GetSingle()` methods.

```jinja2
{# List all resources #}
{% for ingress in resources.ingresses.List() %}

{# Fetch by index keys (parameters match indexBy configuration) #}
{% for ingress in resources.ingresses.Fetch("default", "my-ingress") %}

{# Get single resource or null #}
{% set secret = resources.secrets.GetSingle("default", "my-secret") %}
```

### Index Configuration

The `indexBy` field determines what parameters `Fetch()` expects:

```yaml
watchedResources:
  ingresses:
    apiVersion: networking.k8s.io/v1
    resources: ingresses
    indexBy: ["metadata.namespace", "metadata.name"]
    # Fetch(namespace, name)

  endpoints:
    apiVersion: discovery.k8s.io/v1
    resources: endpointslices
    indexBy: ["metadata.labels.kubernetes\\.io/service-name"]
    # Fetch(service_name)
```

!!! tip
    Escape dots in JSONPath for labels: `kubernetes\\.io/service-name`

## Custom Template Variables

Add custom variables via `templatingSettings.extraContext`:

```yaml
spec:
  templatingSettings:
    extraContext:
      environment: production
      debug: true
      limits:
        maxConn: 10000
```

Access in templates:

```jinja2
{% if debug %}
  http-response set-header X-Debug %[be_name]
{% endif %}

global
  maxconn {{ limits.maxConn }}
```

## Authentication Annotations

Enable HTTP basic authentication via Ingress annotations:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: protected-app
  annotations:
    haproxy.org/auth-type: "basic-auth"
    haproxy.org/auth-secret: "my-auth-secret"
    haproxy.org/auth-realm: "Protected"
spec:
  rules:
    - host: app.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-service
                port:
                  number: 80
```

**Create authentication secrets** with crypt(3) SHA-512 password hashes:

```bash
HASH=$(openssl passwd -6 mypassword)
kubectl create secret generic my-auth-secret \
  --from-literal=admin=$(echo -n "$HASH" | base64 -w0)
```

Configure secrets store with `store: on-demand` to fetch secrets on-demand rather than watching all cluster secrets.

## Common Patterns

### Reserved Server Slots (Avoid Reloads)

Pre-allocate server slots to enable runtime API updates without reloads:

```jinja2
{%- set initial_slots = 10 %}
{%- set ns = namespace(active_endpoints=[]) %}

{# Collect endpoints #}
{%- for endpoint_slice in resources.endpoints.Fetch(service_name) %}
  {%- for endpoint in (endpoint_slice.endpoints | default([])) %}
    {%- for address in (endpoint.addresses | default([])) %}
      {%- set ns.active_endpoints = ns.active_endpoints + [{'address': address, 'port': port}] %}
    {%- endfor %}
  {%- endfor %}
{%- endfor %}

{# Fixed slots - active endpoints fill first, rest are disabled #}
{%- for i in range(1, initial_slots + 1) %}
  {%- if loop.index0 < ns.active_endpoints|length %}
    {%- set ep = ns.active_endpoints[loop.index0] %}
server SRV_{{ i }} {{ ep.address }}:{{ ep.port }} check
  {%- else %}
server SRV_{{ i }} 127.0.0.1:1 disabled
  {%- endif %}
{%- endfor %}
```

**Benefit**: Endpoint changes update server addresses via runtime API without dropping connections.

### Cross-Resource Lookups

Use fields from one resource to query another:

```jinja2
{% for ingress in resources.ingresses.List() %}
{% for rule in (ingress.spec.rules | default([])) %}
{% for path in (rule.http.paths | default([])) %}
  {% set service_name = path.backend.service.name %}
  {% set port = path.backend.service.port.number | default(80) %}

backend ing_{{ ingress.metadata.name }}_{{ service_name }}
    {%- for endpoint_slice in resources.endpoints.Fetch(service_name) %}
    {%- for endpoint in (endpoint_slice.endpoints | default([])) %}
    {%- for address in (endpoint.addresses | default([])) %}
    server {{ endpoint.targetRef.name }} {{ address }}:{{ port }} check
    {%- endfor %}
    {%- endfor %}
    {%- endfor %}
{% endfor %}
{% endfor %}
{% endfor %}
```

**Required indexing**:
```yaml
watchedResources:
  ingresses:
    indexBy: ["metadata.namespace", "metadata.name"]
  endpoints:
    indexBy: ["metadata.labels.kubernetes\\.io/service-name"]
```

### Safe Iteration

Use `default` filter to handle missing fields:

```jinja2
{% for endpoint in (endpoint_slice.endpoints | default([])) %}
  {% for address in (endpoint.addresses | default([])) %}
    server srv {{ address }}:80
  {% endfor %}
{% endfor %}
```

### Filtering with selectattr

Filter resources by attribute presence:

```jinja2
{% for rule in (ingress.spec.rules | default([]) | selectattr("http", "defined")) %}
  {# rule.http is guaranteed to exist #}
{% endfor %}
```

### Mutable Variables with namespace()

Accumulate values across loop iterations:

```jinja2
{% set ns = namespace(active_endpoints=[]) %}

{% for endpoint_slice in resources.endpoints.Fetch(service_name) %}
  {% for endpoint in (endpoint_slice.endpoints | default([])) %}
    {% set ns.active_endpoints = ns.active_endpoints + [{'address': endpoint.addresses[0]}] %}
  {% endfor %}
{% endfor %}

{% for ep in ns.active_endpoints %}
  server srv{{ loop.index }} {{ ep.address }}:80
{% endfor %}
```

### Whitespace Control

```jinja2
{%- for item in items %}   {# Strip before #}
{% for item in items -%}   {# Strip after #}

{%- filter indent(4, first=True) %}
{% include "server-list" %}
{%- endfilter %}
```

## Complete Example

Full ingress → service → endpoints chain with reserved slots:

```yaml
watchedResources:
  ingresses:
    apiVersion: networking.k8s.io/v1
    resources: ingresses
    indexBy: ["metadata.namespace", "metadata.name"]
  endpoints:
    apiVersion: discovery.k8s.io/v1
    resources: endpointslices
    indexBy: ["metadata.labels.kubernetes\\.io/service-name"]

maps:
  host.map:
    template: |
      {%- for ingress in resources.ingresses.List() %}
      {%- for rule in (ingress.spec.rules | default([])) %}
      {{ rule.host }} ing_{{ ingress.metadata.name }}
      {%- endfor %}
      {%- endfor %}

templateSnippets:
  backend-servers:
    template: |
      {%- set initial_slots = 10 %}
      {%- set ns = namespace(active_endpoints=[]) %}
      {%- for es in resources.endpoints.Fetch(service_name) %}
        {%- for ep in (es.endpoints | default([])) %}
          {%- for addr in (ep.addresses | default([])) %}
            {%- set ns.active_endpoints = ns.active_endpoints + [{'addr': addr}] %}
          {%- endfor %}
        {%- endfor %}
      {%- endfor %}
      {%- for i in range(1, initial_slots + 1) %}
        {%- if loop.index0 < ns.active_endpoints|length %}
      server SRV_{{ i }} {{ ns.active_endpoints[loop.index0].addr }}:{{ port }} check
        {%- else %}
      server SRV_{{ i }} 127.0.0.1:1 disabled
        {%- endif %}
      {%- endfor %}

haproxyConfig:
  template: |
    global
        daemon
        maxconn 4096

    defaults
        mode http
        timeout connect 5s
        timeout client 50s
        timeout server 50s

    frontend http
        bind *:80
        use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

    {% for ingress in resources.ingresses.List() %}
    {% for rule in (ingress.spec.rules | default([])) %}
    {% for path in (rule.http.paths | default([])) %}
    {%- set service_name = path.backend.service.name %}
    {%- set port = path.backend.service.port.number | default(80) %}

    backend ing_{{ ingress.metadata.name }}
        balance roundrobin
        {%- filter indent(4, first=True) %}
        {% include "backend-servers" %}
        {%- endfilter %}
    {% endfor %}
    {% endfor %}
    {% endfor %}
```

## See Also

- [Template Engine Reference](https://gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/blob/main/pkg/templating/README.md)
- [Gonja Documentation](https://github.com/nikolalohinski/gonja)
- [HAProxy Configuration Manual](https://docs.haproxy.org/2.9/configuration.html)
