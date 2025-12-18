# Templating Guide

## Overview

The HAProxy Template Ingress Controller uses [Scriggo](https://scriggo.com/), a Go template engine, to generate HAProxy configurations from Kubernetes resources. You define templates that access watched Kubernetes resources, and the controller renders these templates whenever resources change, validates the output, and deploys it to HAProxy instances.

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

    {% for _, ingress := range resources.ingresses.List() %}
    backend {{ ingress.metadata.name }}
        balance roundrobin
    {% end %}
```

!!! important
    All auxiliary file references must use **absolute paths** from `/etc/haproxy/`. Use `pathResolver.GetPath()` to generate correct paths.

### Map Files

Map files generate HAProxy lookup tables stored in `/etc/haproxy/maps/`:

```yaml
maps:
  host.map:
    template: |
      {%- for _, ingress := range resources.ingresses.List() %}
      {%- for _, rule := range fallback(ingress.spec.rules, []any{}) %}
      {%- if rule.http != nil %}
      {{ rule.host }} {{ rule.host }}
      {%- end %}
      {%- end %}
      {%- end %}
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
      {%- var secret = resources.secrets.GetSingle("default", "example-com-tls") %}
      {%- if secret != nil %}
      {{ b64decode(secret.data["tls.crt"]) }}
      {{ b64decode(secret.data["tls.key"]) }}
      {%- end %}
```

!!! note
    Certificate data in Secrets is base64-encoded. Use the `b64decode` filter to decode it.

### Template Snippets

Reusable template fragments included via `{% render "snippet-name" %}`:

```yaml
templateSnippets:
  backend-name:
    template: >-
      ing_{{ ingress.metadata.namespace }}_{{ ingress.metadata.name }}

  backend-servers:
    template: |
      {%- for _, endpoint_slice := range resources.endpoints.Fetch(service_name) %}
      {%- for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
      {%- for _, address := range fallback(endpoint.addresses, []any{}) %}
      server {{ endpoint.targetRef.name }} {{ address }}:{{ port }} check
      {%- end %}
      {%- end %}
      {%- end %}
```

## Template Syntax

Templates use Scriggo's template syntax. For complete syntax reference, see the [Scriggo documentation](https://scriggo.com/templates).

### Control Structures

```go
{# Loops #}
{% for _, ingress := range resources.ingresses.List() %}
  backend {{ ingress.metadata.name }}
{% end %}

{# Conditionals #}
{% if ingress.spec.tls != nil %}
  bind *:443 ssl crt {{ pathResolver.GetPath(ingress.metadata.name + ".pem", "cert") }}
{% end %}

{# Variables #}
{% var service_name = path.backend.service.name %}
{% var port = fallback(path.backend.service.port.number, 80) %}

{# Comments #}
{# This is a comment #}
```

### Common Functions

```go
{{ fallback(path.backend.service.port.number, 80) }}  {# Default values #}
{{ toLower(rule.host) }}                               {# String manipulation #}
{{ len(ingress.spec.rules) }}                          {# Collection length #}
```

### Path Resolution

The `pathResolver.GetPath()` method resolves filenames to absolute paths:

```go
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

```go
{% var sorted = sort_by(routes, []string{
    "$.match.method:exists:desc",
    "$.match.headers | length:desc",
    "$.match.path.value | length:desc",
}) %}
```

## Available Template Data

### The `resources` Variable

Templates access watched resources through the `resources` variable. Each store provides `List()`, `Fetch()`, and `GetSingle()` methods.

```go
{# List all resources #}
{% for _, ingress := range resources.ingresses.List() %}

{# Fetch by index keys (parameters match indexBy configuration) #}
{% for _, ingress := range resources.ingresses.Fetch("default", "my-ingress") %}

{# Get single resource or nil #}
{% var secret = resources.secrets.GetSingle("default", "my-secret") %}
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

```go
{% if debug %}
  http-response set-header X-Debug %[be_name]
{% end %}

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

```go
{%- var initial_slots = 10 %}
{%- var active_endpoints = []map[string]any{} %}

{# Collect endpoints #}
{%- for _, endpoint_slice := range resources.endpoints.Fetch(service_name) %}
  {%- for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
    {%- for _, address := range fallback(endpoint.addresses, []any{}) %}
      {%- active_endpoints = append(active_endpoints, map[string]any{"address": address, "port": port}) %}
    {%- end %}
  {%- end %}
{%- end %}

{# Fixed slots - active endpoints fill first, rest are disabled #}
{%- for i := 1; i <= initial_slots; i++ %}
  {%- if i-1 < len(active_endpoints) %}
    {%- var ep = active_endpoints[i-1] %}
server SRV_{{ i }} {{ ep["address"] }}:{{ ep["port"] }} check
  {%- else %}
server SRV_{{ i }} 127.0.0.1:1 disabled
  {%- end %}
{%- end %}
```

**Benefit**: Endpoint changes update server addresses via runtime API without dropping connections.

### Cross-Resource Lookups

Use fields from one resource to query another:

```go
{% for _, ingress := range resources.ingresses.List() %}
{% for _, rule := range fallback(ingress.spec.rules, []any{}) %}
{% for _, path := range fallback(rule.http.paths, []any{}) %}
  {% var service_name = path.backend.service.name %}
  {% var port = fallback(path.backend.service.port.number, 80) %}

backend ing_{{ ingress.metadata.name }}_{{ service_name }}
    {%- for _, endpoint_slice := range resources.endpoints.Fetch(service_name) %}
    {%- for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
    {%- for _, address := range fallback(endpoint.addresses, []any{}) %}
    server {{ endpoint.targetRef.name }} {{ address }}:{{ port }} check
    {%- end %}
    {%- end %}
    {%- end %}
{% end %}
{% end %}
{% end %}
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

Use `fallback` function to handle missing fields:

```go
{% for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
  {% for _, address := range fallback(endpoint.addresses, []any{}) %}
    server srv {{ address }}:80
  {% end %}
{% end %}
```

### Filtering with Conditionals

Filter resources by attribute presence:

```go
{% for _, rule := range fallback(ingress.spec.rules, []any{}) %}
  {% if rule.http != nil %}
  {# rule.http is guaranteed to exist #}
  {% end %}
{% end %}
```

### Mutable Variables

Accumulate values across loop iterations using Go-style variable assignment:

```go
{% var active_endpoints = []map[string]any{} %}

{% for _, endpoint_slice := range resources.endpoints.Fetch(service_name) %}
  {% for _, endpoint := range fallback(endpoint_slice.endpoints, []any{}) %}
    {% active_endpoints = append(active_endpoints, map[string]any{"address": endpoint.addresses[0]}) %}
  {% end %}
{% end %}

{% for i, ep := range active_endpoints %}
  server srv{{ i + 1 }} {{ ep["address"] }}:80
{% end %}
```

### Whitespace Control

```go
{%- for _, item := range items %}   {# Strip before #}
{% for _, item := range items -%}   {# Strip after #}

{# Use show for indented output #}
    {% show render("server-list") %}
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
      {%- for _, ingress := range resources.ingresses.List() %}
      {%- for _, rule := range fallback(ingress.spec.rules, []any{}) %}
      {{ rule.host }} ing_{{ ingress.metadata.name }}
      {%- end %}
      {%- end %}

templateSnippets:
  backend-servers:
    template: |
      {%- var initial_slots = 10 %}
      {%- var active_endpoints = []map[string]any{} %}
      {%- for _, es := range resources.endpoints.Fetch(service_name) %}
        {%- for _, ep := range fallback(es.endpoints, []any{}) %}
          {%- for _, addr := range fallback(ep.addresses, []any{}) %}
            {%- active_endpoints = append(active_endpoints, map[string]any{"addr": addr}) %}
          {%- end %}
        {%- end %}
      {%- end %}
      {%- for i := 1; i <= initial_slots; i++ %}
        {%- if i-1 < len(active_endpoints) %}
      server SRV_{{ i }} {{ active_endpoints[i-1]["addr"] }}:{{ port }} check
        {%- else %}
      server SRV_{{ i }} 127.0.0.1:1 disabled
        {%- end %}
      {%- end %}

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

    {% for _, ingress := range resources.ingresses.List() %}
    {% for _, rule := range fallback(ingress.spec.rules, []any{}) %}
    {% for _, path := range fallback(rule.http.paths, []any{}) %}
    {%- var service_name = path.backend.service.name %}
    {%- var port = fallback(path.backend.service.port.number, 80) %}

    backend ing_{{ ingress.metadata.name }}
        balance roundrobin
        {% show render("backend-servers") %}
    {% end %}
    {% end %}
    {% end %}
```

## See Also

- [Template Engine Reference](https://gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/blob/main/pkg/templating/README.md)
- [Scriggo Documentation](https://scriggo.com/templates)
- [HAProxy Configuration Manual](https://docs.haproxy.org/2.9/configuration.html)
