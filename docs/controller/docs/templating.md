# Templating Guide

## Overview

The HAProxy Template Ingress Controller uses Gonja v2, a Jinja2-like template engine for Go, to generate HAProxy configurations from Kubernetes resources. You define templates that access watched Kubernetes resources, and the controller renders these templates whenever resources change, validates the output, and deploys it to HAProxy instances.

**Why use templates:**
- Complete control over HAProxy configuration without annotation limitations
- Access to all HAProxy features (ACLs, stick tables, rate limiting, custom error pages)
- Define your own data model using any Kubernetes resources
- Reusable template snippets for common patterns
- Type-safe template rendering with early error detection

Templates are rendered automatically when any watched resource changes, during initial synchronization, or periodically for drift detection.

## What You Can Template

The controller supports five types of templatable components, each serving a specific purpose in HAProxy configuration.

### HAProxy Configuration

The main `haproxy_config` template generates the complete HAProxy configuration file (`/etc/haproxy/haproxy.cfg`).

```yaml
haproxy_config:
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
        # Use map files for routing
        use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

    {% for ingress in resources.ingresses.List() %}
    backend {{ ingress.metadata.name }}
        balance roundrobin
        # Backend configuration here
    {% endfor %}
```

> [!IMPORTANT]
> All auxiliary file references in HAProxy configuration must use **absolute paths** from `/etc/haproxy/`. For example, use `/etc/haproxy/maps/host.map` for map files, `/etc/haproxy/ssl/cert.pem` for SSL certificates, and `/etc/haproxy/general/error.http` for general files. This requirement exists because the HAProxy Dataplane API validation needs absolute paths to verify files exist before applying configuration changes.

### Map Files

Map files (`maps`) generate HAProxy map files for backend routing, ACL matching, and other lookup operations. Maps are stored in `/etc/haproxy/maps/` and referenced in HAProxy configuration using absolute paths like `/etc/haproxy/maps/host.map`.

```yaml
maps:
  host.map:
    template: |
      # Map host headers to normalized host values
      {%- for ingress in resources.ingresses.List() %}
      {%- for rule in (ingress.spec.rules | default([]) | selectattr("http", "defined")) %}
      {{ rule.host }} {{ rule.host }}
      {%- endfor %}
      {%- endfor %}

  path-prefix.map:
    template: |
      # Map host+path to backend names using prefix matching
      {%- for ingress in resources.ingresses.List() %}
      {%- for rule in (ingress.spec.rules | default([])) %}
      {%- for path in (rule.http.paths | default([])) %}
      {{ rule.host }}{{ path.path }}/ backend_{{ ingress.metadata.name }}
      {%- endfor %}
      {%- endfor %}
      {%- endfor %}
```

**Use maps for:**
- Host-based backend selection
- Path-based routing with prefix or exact matching
- ACL lookups for access control
- Rate limiting based on client characteristics

### General Files

General files (`files`) generate auxiliary files like custom error pages. Files are stored in `/etc/haproxy/general/` and referenced in HAProxy configuration using absolute paths like `/etc/haproxy/general/503.http`.

```yaml
files:
  503.http:
    template: |
      HTTP/1.0 503 Service Unavailable
      Cache-Control: no-cache
      Connection: close
      Content-Type: text/html

      <html><body><h1>503 Service Unavailable</h1>
      <p>No server is available to handle this request.</p>
      </body></html>

  maintenance.html:
    template: |
      <html><body>
      <h1>Maintenance Mode</h1>
      <p>System will be back online at {{ maintenance_end_time }}.</p>
      </body></html>
```

**Use general files for:**
- Custom error pages (400, 403, 404, 500, 503, etc.)
- Maintenance mode pages
- Health check responses
- Static content served by HAProxy

### SSL Certificates

SSL certificates (`ssl_certificates`) generate SSL/TLS certificate files from Kubernetes Secret data. Certificates are stored in `/etc/haproxy/ssl/` and referenced in HAProxy configuration using absolute paths like `/etc/haproxy/ssl/example-com.pem`.

```yaml
ssl_certificates:
  example-com.pem:
    template: |
      {%- for secret in resources.secrets.Fetch("default", "kubernetes.io/tls") %}
      {%- if secret.metadata.name == "example-com-tls" %}
      {{ secret.data.tls_crt | b64decode }}
      {{ secret.data.tls_key | b64decode }}
      {%- endif %}
      {%- endfor %}
```

**Use SSL certificates for:**
- TLS termination at HAProxy
- Client certificate authentication
- Backend SSL connections

> [!NOTE]
> Certificate data in Secrets is base64-encoded. Use the `b64decode` filter to decode it in your templates.

### Template Snippets

Template snippets (`template_snippets`) define reusable template fragments that can be included in other templates. This promotes code reuse and keeps templates maintainable.

```yaml
template_snippets:
  backend-name:
    name: backend-name
    template: >-
      ing_{{ ingress.metadata.namespace }}_{{ ingress.metadata.name }}_{{ path.backend.service.name }}

  backend-servers:
    name: backend-servers
    template: |
      {%- for endpoint_slice in resources.endpoints.Fetch(service_name) %}
      {%- for endpoint in (endpoint_slice.endpoints | default([])) %}
      {%- for address in (endpoint.addresses | default([])) %}
      server {{ endpoint.targetRef.name }} {{ address }}:{{ port }} check
      {%- endfor %}
      {%- endfor %}
      {%- endfor %}
```

Include snippets using `{% include "snippet-name" %}`:

```jinja2
backend {% include "backend-name" %}
    balance roundrobin
    {% include "backend-servers" %}
```

## Template Syntax

Templates use Gonja v2, which provides Jinja2-like syntax for Go. The syntax is familiar if you've used Jinja2, Django templates, or Ansible.

### Control Structures

**Loops** iterate over collections:

```jinja2
{% for ingress in resources.ingresses.List() %}
  backend {{ ingress.metadata.name }}
      server srv1 192.168.1.10:80
{% endfor %}
```

**Conditionals** control template logic:

```jinja2
{% if ingress.spec.tls %}
  {% set cert_name = ingress.metadata.name ~ ".pem" %}
  bind *:443 ssl crt {{ pathResolver.GetPath(cert_name, "cert") }}
{% else %}
  bind *:80
{% endif %}
```

**Variables** store values for reuse:

```jinja2
{% set service_name = path.backend.service.name %}
{% set port = path.backend.service.port.number | default(80) %}
```

**Comments** document your templates:

```jinja2
{# This backend handles all traffic for the production namespace #}
backend production_backend
    balance roundrobin
```

For complete syntax reference, see the [Gonja documentation](https://github.com/nikolalohinski/gonja).

### Filters

Filters transform values in templates using the pipe (`|`) operator.

**Common filters:**

```jinja2
{# Provide default values for missing data #}
{{ path.backend.service.port.number | default(80) }}

{# String manipulation #}
{{ rule.host | lower }}
{{ ingress.metadata.name | upper }}

{# Get collection length #}
{{ ingress.spec.rules | length }}

{# Indent text blocks #}
{%- filter indent(2, first=True) %}
{% include "backend-servers" %}
{%- endfilter %}
```

**Context method - pathResolver.GetPath():**

The `pathResolver.GetPath()` method resolves filenames to absolute paths based on file type. This simplifies template writing by automatically constructing correct absolute paths for HAProxy auxiliary files.

The `pathResolver` is a context variable that provides path resolution based on the controller's dataplane configuration.

```jinja2
{# Map files - resolve to maps directory #}
use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]
{# Output: use_backend %[req.hdr(host),lower,map(/etc/haproxy/maps/host.map)] #}

{# General files - resolve to general directory #}
errorfile 504 {{ pathResolver.GetPath("504.http", "file") }}
{# Output: errorfile 504 /etc/haproxy/general/504.http #}

{# SSL certificates - resolve to SSL directory #}
bind *:443 ssl crt {{ pathResolver.GetPath("example.com.pem", "cert") }}
{# Output: bind *:443 ssl crt /etc/haproxy/ssl/example.com.pem #}

{# Use with variables #}
{% set cert_name = ingress.metadata.name ~ ".pem" %}
bind *:443 ssl crt {{ pathResolver.GetPath(cert_name, "cert") }}
```

**Arguments:**
- **filename** (required): The base filename without directory path
- **type** (required): File type - `"map"`, `"file"`, `"cert"`, or `"crt-list"`

The method uses the paths configured in `dataplane.maps_dir`, `dataplane.ssl_certs_dir`, and `dataplane.general_storage_dir` from your controller configuration.

**Custom filter - glob_match:**

The `glob_match` filter filters lists of strings using glob patterns with `*` (match any characters) and `?` (match single character) wildcards.

```jinja2
{# Filter template snippet names by pattern #}
{% set backend_snippets = template_snippets | glob_match("backend-annotation-*") %}
{% for snippet_name in backend_snippets %}
  {% include snippet_name %}
{% endfor %}

{# Filter resource names #}
{% set prod_ingresses = resources.ingresses.List() | map(attribute='metadata.name') | glob_match("prod-*") %}
```

**Arguments:**
- **input** (required): List of strings to filter
- **pattern** (required): Glob pattern string

**Returns:** List of strings matching the pattern

**Custom filter - b64decode:**

The `b64decode` filter decodes base64-encoded strings. This is essential for accessing Kubernetes Secret data, as Kubernetes automatically base64-encodes all secret values.

```jinja2
{# Decode secret data #}
{% set secret = resources.secrets.GetSingle("default", "my-secret") %}
{% if secret %}
  password: {{ secret.data.password | b64decode }}
{% endif %}

{# Decode credentials for HAProxy userlist #}
userlist auth_users
{% for username in secret.data %}
  user {{ username }} password {{ secret.data[username] | b64decode }}
{% endfor %}
```

**Arguments:**
- **input** (required): Base64-encoded string

**Returns:** Decoded plaintext string

**Custom filter - regex_escape:**

The `regex_escape` filter escapes special regex characters for safe use in HAProxy ACL patterns. Essential when constructing regex patterns from user-controlled data.

```jinja2
{# Escape path prefixes for regex matching #}
{% set path_pattern = route.match.path.value | regex_escape %}
acl path_match path_reg ^{{ path_pattern }}

{# Example with Gateway API HTTPRoute path matching #}
{% for route in httproutes %}
  {% set escaped_path = route.spec.rules[0].matches[0].path.value | regex_escape %}
  use_backend backend_{{ route.metadata.name }} if { path_reg ^{{ escaped_path }} }
{% endfor %}
```

**Custom filter - sort_by:**

The `sort_by` filter sorts arrays of objects by JSONPath expressions. Supports multiple sort criteria with modifiers.

```jinja2
{# Sort routes by priority (descending) then name #}
{% set sorted_routes = routes | sort_by(["$.priority:desc", "$.name"]) %}

{# Gateway API route precedence: method > headers > query params > path specificity #}
{% set sorted = routes | sort_by([
    "$.match.method:exists:desc",
    "$.match.headers | length:desc",
    "$.match.queryParams | length:desc",
    "$.match.path.value | length:desc"
]) %}
{% for route in sorted %}
  {# Routes now ordered by Gateway API precedence rules #}
  use_backend {{ route.backend }} if {{ route.acl }}
{% endfor %}
```

**Available modifiers:**
- `:desc` - Sort in descending order
- `:exists` - Sort by field presence (present items first)
- `| length` - Sort by string or array length

**Custom filter - extract:**

The `extract` filter extracts values from objects using JSONPath expressions. Automatically flattens nested arrays.

```jinja2
{# Extract all HTTP methods from Gateway API routes #}
{% set all_methods = httproutes | extract("$.spec.rules[*].matches[*].method") %}
{# Returns: ["GET", "POST", "PUT", "DELETE"] #}

{# Extract service names from ingresses #}
{% set services = ingresses | extract("$.spec.rules[*].http.paths[*].backend.service.name") %}

{# Use extracted values for ACL construction #}
{% set hostnames = ingresses | extract("$.spec.rules[*].host") | unique %}
{% for host in hostnames %}
  acl is_{{ host | replace(".", "_") }} hdr(host) -i {{ host }}
{% endfor %}
```

**Custom filter - group_by:**

The `group_by` filter groups array items by the value of a JSONPath expression.

```jinja2
{# Group ingresses by namespace for multi-tenant configuration #}
{% set by_namespace = ingresses | group_by("$.metadata.namespace") %}
{% for namespace, ingresses in by_namespace.items() %}
  # Namespace: {{ namespace }} ({{ ingresses|length }} ingresses)
  {% for ingress in ingresses %}
    backend ing_{{ namespace }}_{{ ingress.metadata.name }}
  {% endfor %}
{% endfor %}

{# Group routes by priority level #}
{% set by_priority = routes | group_by("$.priority") %}
{% for priority in by_priority.keys() | sort | reverse %}
  # Priority {{ priority }} routes
  {% for route in by_priority[priority] %}
    {# Process high-priority routes first #}
  {% endfor %}
{% endfor %}
```

**Custom filter - transform:**

The `transform` filter applies regex substitution to array elements.

```jinja2
{# Strip API version prefixes from paths #}
{% set paths = ["/api/v1/users", "/api/v1/posts", "/api/v2/comments"] %}
{% set clean_paths = paths | transform("^/api/v\\d+", "") %}
{# Returns: ["/users", "/posts", "/comments"] #}

{# Normalize hostname formats #}
{% set hosts = ["www.example.com", "api.example.com"] %}
{% set domains = hosts | transform("^[^.]+\\.", "") %}
{# Returns: ["example.com", "example.com"] #}
```

**Custom filter - debug:**

The `debug` filter outputs variables as JSON-formatted HAProxy comments. Useful for template development and troubleshooting.

```jinja2
{# Debug route structure during development #}
{{ routes | debug("available-routes") }}

{# Output:
# DEBUG available-routes:
# [
#   {
#     "name": "api-route",
#     "priority": 100,
#     "match": {"method": "GET", "path": "/api"}
#   }
# ]
#}

{# Compare before/after transformations #}
{{ routes | debug("before-sorting") }}
{% set sorted = routes | sort_by(["$.priority:desc"]) %}
{{ sorted | debug("after-sorting") }}
```

**Custom filter - eval:**

The `eval` filter evaluates JSONPath expressions and shows results with type information. Useful for testing sort_by criteria.

```jinja2
{# Test sort criteria before applying #}
{% for route in routes %}
  Route: {{ route.name }}
    Priority: {{ route | eval("$.priority") }}
    Has method: {{ route | eval("$.match.method:exists") }}
    Header count: {{ route | eval("$.match.headers | length") }}
{% endfor %}

{# Understand why items are sorted in a specific order #}
{% for item in items %}
  {{ item | eval("$.weight:desc") }}  {# Shows: 100 (int), 50 (int), 10 (int) #}
{% endfor %}
```

For the complete list of built-in filters, see [Gonja filters](https://github.com/nikolalohinski/gonja#filters).

### Functions

Gonja provides built-in functions for common operations. See the [Gonja documentation](https://github.com/nikolalohinski/gonja#functions) for available functions.

## Available Template Data

Templates have access to the `resources` variable, which contains stores for all watched Kubernetes resource types.

### The `resources` Variable

The `resources` variable is a collection of stores, one for each resource type you configure in `watched_resources`. Each store provides `List()` and `Get()` methods for accessing resources.

**Structure:**

```yaml
resources:
  ingresses:    # Store for Ingress resources (if configured)
  services:     # Store for Service resources (if configured)
  endpoints:    # Store for EndpointSlice resources (if configured)
  secrets:      # Store for Secret resources (if configured)
  # ... any other configured resource types
```

The store names match the keys in your `watched_resources` configuration.

### Using List() Method

The `List()` method returns all resources of a specific type. Use this to iterate over all resources.

**Example:**

```jinja2
{# Iterate over all Ingress resources #}
{% for ingress in resources.ingresses.List() %}
backend {{ ingress.metadata.name }}
    balance roundrobin
    server srv1 192.168.1.10:80
{% endfor %}
```

**When to use List():**
- Generate configuration for all resources of a type
- Build map files with all hosts/paths
- Count resources for capacity planning

### Using Get() Method

The `Get()` method returns resources matching specific index keys. The parameters you provide to `Get()` are determined by the `index_by` configuration for that resource type.

**How indexing works:**

```yaml
watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    index_by: ["metadata.namespace", "metadata.name"]
    # Get() expects: Get(namespace, name)

  endpoints:
    api_version: discovery.k8s.io/v1
    kind: EndpointSlice
    index_by: ["metadata.labels.kubernetes\\.io/service-name"]
    # Get() expects: Get(service_name)
```

**Examples:**

```jinja2
{# Get specific ingress by namespace and name #}
{% for ingress in resources.ingresses.Fetch("default", "my-ingress") %}
  {# Usually returns 0 or 1 items #}
{% endfor %}

{# Get all endpoint slices for a service #}
{% set service_name = path.backend.service.name %}
{% for endpoint_slice in resources.endpoints.Fetch(service_name) %}
  {# Returns all endpoint slices labeled with this service name #}
{% endfor %}
```

**When to use Get():**
- Look up specific resources by key
- Find related resources (e.g., endpoints for a service)
- Implement cross-resource matching patterns

### Index Configuration

The `index_by` field in `watched_resources` determines:
1. How resources are indexed in the store
2. What parameters `Get()` expects
3. Query performance (O(1) lookups with proper indexing)

**Common indexing patterns:**

```yaml
# By namespace and name (most resources)
index_by: ["metadata.namespace", "metadata.name"]
# Get(namespace, name) returns specific resource

# By service name (endpoint slices)
index_by: ["metadata.labels.kubernetes\\.io/service-name"]
# Get(service_name) returns all endpoint slices for that service

# By type (secrets)
index_by: ["metadata.namespace", "type"]
# Get(namespace, type) returns all secrets of that type in namespace
```

> [!TIP]
> Escape dots in JSONPath expressions for labels: `kubernetes\\.io/service-name`

## Custom Template Variables

You can add custom variables to the template context using `templatingSettings.extraContext`. These variables are available in all templates, allowing you to configure template behavior without modifying controller code.

### Configuration

Define custom variables in your HAProxyTemplateConfig CRD or ConfigMap configuration:

**Using CRD:**

```yaml
apiVersion: haproxy-template-ic.github.io/v1alpha1
kind: HAProxyTemplateConfig
spec:
  templatingSettings:
    extraContext:
      debug:
        enabled: true
        verboseHeaders: false
      environment: production
      featureFlags:
        rateLimiting: true
        caching: false
      customTimeout: 30
```

**Using ConfigMap:**

```yaml
# config.yaml in ConfigMap
templating_settings:
  extra_context:
    debug:
      enabled: true
      verbose_headers: false
    environment: production
    feature_flags:
      rate_limiting: true
      caching: false
    custom_timeout: 30
```

### Accessing Custom Variables

Custom variables are merged at the top level of the template context. Access them directly without prefixes:

```jinja2
{% if debug.enabled %}
  # Debug mode - add diagnostic headers
  http-response set-header X-HAProxy-Backend %[be_name]
  http-response set-header X-HAProxy-Server %[srv_name]
{% endif %}

{% if environment == "production" %}
  # Production-specific settings
  timeout client {{ customTimeout }}s
  timeout server {{ customTimeout }}s
{% else %}
  # Development settings
  timeout client 300s
  timeout server 300s
{% endif %}

{% if featureFlags.rateLimiting %}
  # Rate limiting configuration
  stick-table type ip size 100k expire 30s store http_req_rate(10s)
  http-request track-sc0 src
  http-request deny if { sc_http_req_rate(0) gt 100 }
{% endif %}
```

### Use Cases

**Environment-Specific Configuration:**

```yaml
extraContext:
  environment: staging
  limits:
    maxConn: 1000
    timeout: 10
```

```jinja2
global
  maxconn {% if environment == "production" %}10000{% else %}{{ limits.maxConn }}{% endif %}

defaults
  timeout connect {{ limits.timeout }}s
```

**Feature Flags:**

```yaml
extraContext:
  features:
    compression: true
    http2: false
```

```jinja2
{% if features.compression %}
  compression algo gzip
  compression type text/html text/plain text/css
{% endif %}

{% if features.http2 %}
  bind *:443 ssl crt /etc/haproxy/ssl/ alpn h2,http/1.1
{% else %}
  bind *:443 ssl crt /etc/haproxy/ssl/
{% endif %}
```

**Debug Headers:**

```yaml
extraContext:
  debug: true
```

```jinja2
{% if debug %}
  # Add diagnostic headers showing routing decisions
  http-response set-header X-Gateway-Matched-Route %[var(txn.matched_route)]
  http-response set-header X-Gateway-Backend %[var(txn.backend_name)]
{% endif %}
```

### Supported Value Types

The `extraContext` field accepts any valid JSON value:
- **Strings**: `environment: "production"`
- **Numbers**: `timeout: 30`, `limit: 1.5`
- **Booleans**: `enabled: true`
- **Objects**: `debug: { enabled: true, level: 2 }`
- **Arrays**: `allowedIPs: ["10.0.0.1", "10.0.0.2"]`

## Authentication Annotations

The controller provides built-in support for HAProxy basic authentication through Ingress annotations. When you add authentication annotations to an Ingress, the controller automatically generates HAProxy userlist sections and configures `http-request auth` directives.

### Basic Authentication

Use these annotations on Ingress resources to enable HTTP basic authentication:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: protected-app
  annotations:
    haproxy.org/auth-type: "basic-auth"
    haproxy.org/auth-secret: "my-auth-secret"
    haproxy.org/auth-realm: "Protected Application"
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

**Annotations:**
- `haproxy.org/auth-type`: Authentication type (currently only `"basic-auth"` is supported)
- `haproxy.org/auth-secret`: Name of the Kubernetes Secret containing credentials (format: `"secret-name"` or `"namespace/secret-name"`)
- `haproxy.org/auth-realm`: HTTP authentication realm displayed to users (optional, defaults to `"Restricted Area"`)

### Creating Authentication Secrets

Secrets must contain username-password pairs where values are **base64-encoded crypt(3) SHA-512 password hashes**.

**Generate password hashes:**

```bash
# Generate SHA-512 hash and encode for Kubernetes
HASH=$(openssl passwd -6 mypassword)
kubectl create secret generic my-auth-secret \
  --from-literal=admin=$(echo -n "$HASH" | base64 -w0) \
  --from-literal=user=$(echo -n "$HASH" | base64 -w0)
```

**Secret structure:**

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-auth-secret
type: Opaque
data:
  # Each key is a username, value is base64-encoded password hash
  admin: JDYkMVd3c2YxNmprcDBkMVBpTyRkS3FHUTF0SW0uOGF1VlJIcVA3dVcuMVV5dVNtZ3YveEc3dEFiOXdZNzc1REw3ZGE0N0hIeVB4ZllDS1BMTktZclJvMHRNQWQyQk1YUHBDd2Z5ZW03MA==
  user: JDYkbkdxOHJ1T2kyd3l4MUtyZyQ1a2d1azEzb2tKWmpzZ2Z2c3JqdmkvOVoxQjZIbDRUcGVvdkpzb2lQeHA2eGRKWUpha21wUmIwSUVHb1ZUSC8zRzZrLmRMRzBuVUNMWEZnMEhTRTJ5MA==
```

### Secret Sharing

Multiple Ingress resources can reference the same authentication secret. The controller automatically deduplicates userlist generation, so the HAProxy userlist is created only once regardless of how many Ingresses use it.

```yaml
# First ingress
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: app1
  annotations:
    haproxy.org/auth-type: "basic-auth"
    haproxy.org/auth-secret: "shared-auth"  # Shared secret
    haproxy.org/auth-realm: "App 1"
# ...

---
# Second ingress using same secret
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: app2
  annotations:
    haproxy.org/auth-type: "basic-auth"
    haproxy.org/auth-secret: "shared-auth"  # Same secret
    haproxy.org/auth-realm: "App 2"
# ...
```

### On-Demand Secret Access

Authentication secrets are fetched on-demand during template rendering. You must configure the secrets store with `store: on-demand` in your `watched_resources`:

```yaml
watched_resources:
  secrets:
    api_version: v1
    kind: Secret
    store: on-demand
    index_by: ["metadata.namespace", "metadata.name"]
```

This allows the controller to fetch only the secrets referenced by Ingress annotations rather than watching all secrets in the cluster.

## Tips & Tricks

These patterns solve common challenges when templating HAProxy configurations.

### Reserved Server Slots Pattern (Avoid Reloads)

**Problem**: Adding or removing servers in HAProxy backends requires a process reload, which can briefly interrupt existing connections.

**Solution**: Pre-allocate a fixed number of server slots with static names. Active endpoints fill slots with real addresses, while unused slots contain disabled placeholders.

**How it works:**

```jinja2
{%- set initial_slots = 10 %}  {# Adjust based on expected endpoints #}

{# Collect active endpoints #}
{%- set ns = namespace(active_endpoints=[]) %}
{%- for endpoint_slice in resources.endpoints.Fetch(service_name) %}
  {%- for endpoint in (endpoint_slice.endpoints | default([])) %}
    {%- for address in (endpoint.addresses | default([])) %}
      {%- set ns.active_endpoints = ns.active_endpoints + [{'name': endpoint.targetRef.name, 'address': address, 'port': port}] %}
    {%- endfor %}
  {%- endfor %}
{%- endfor %}

{# Generate fixed server slots #}
{%- for i in range(1, initial_slots + 1) %}
  {%- if loop.index0 < ns.active_endpoints|length %}
    {# Active server with real endpoint #}
    {%- set endpoint = ns.active_endpoints[loop.index0] %}
server SRV_{{ i }} {{ endpoint.address }}:{{ endpoint.port }} check
  {%- else %}
    {# Disabled placeholder server #}
server SRV_{{ i }} 127.0.0.1:1 disabled
  {%- endif %}
{%- endfor %}
```

**Benefits:**
- Endpoint changes only update server addresses via runtime API (no reload)
- Server names remain stable (`SRV_1`, `SRV_2`, etc.)
- HAProxy can update addresses without dropping connections

**Auto-expansion:**
You can implement automatic slot doubling when all slots are filled:

```jinja2
{%- set active_count = ns.active_endpoints|length %}
{%- if active_count > initial_slots %}
  {%- set max_servers = initial_slots * 2 %}  {# Double when full #}
{%- else %}
  {%- set max_servers = initial_slots %}
{%- endif %}
```

### Matching Resources Across Types

**Pattern**: Use fields from one resource type to query another resource type, enabling cross-resource relationships.

**Example**: Matching Ingress resources with their corresponding EndpointSlices:

```jinja2
{# Step 1: Iterate over ingresses #}
{% for ingress in resources.ingresses.List() %}
{% for rule in (ingress.spec.rules | default([])) %}
{% for path in (rule.http.paths | default([])) %}

  {# Step 2: Extract service name from ingress #}
  {% set service_name = path.backend.service.name %}
  {% set port = path.backend.service.port.number | default(80) %}

  {# Step 3: Look up endpoint slices for this service #}
  backend ing_{{ ingress.metadata.name }}_{{ service_name }}
      balance roundrobin
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

**Key insight**: The index configuration enables this pattern. EndpointSlices are indexed by `metadata.labels.kubernetes\.io/service-name`, so `Get(service_name)` returns all endpoint slices for that service.

**Other cross-resource patterns:**

**Services with Endpoints:**
```jinja2
{% for service in resources.services.List() %}
  {% set service_name = service.metadata.name %}
  {# Look up endpoints #}
  {% for endpoint_slice in resources.endpoints.Fetch(service_name) %}
    {# Process endpoints #}
  {% endfor %}
{% endfor %}
```

**Ingresses with TLS Secrets:**
```jinja2
{% for ingress in resources.ingresses.List() %}
  {% if ingress.spec.tls %}
    {% for tls in ingress.spec.tls %}
      {% set secret_name = tls.secretName %}
      {% set namespace = ingress.metadata.namespace %}
      {# Look up TLS secret #}
      {% for secret in resources.secrets.Fetch(namespace, secret_name) %}
        {# Use cert data: secret.data.tls_crt | b64decode #}
      {% endfor %}
    {% endfor %}
  {% endif %}
{% endfor %}
```

**Required index configuration:**
```yaml
watched_resources:
  ingresses:
    index_by: ["metadata.namespace", "metadata.name"]

  endpoints:
    index_by: ["metadata.labels.kubernetes\\.io/service-name"]

  secrets:
    index_by: ["metadata.namespace", "metadata.name"]
```

### Safe Iteration with default Filter

Always use the `default` filter when iterating over optional fields to prevent template errors when fields are missing or null.

```jinja2
{# Safe: Returns empty array if field is missing #}
{% for endpoint in (endpoint_slice.endpoints | default([])) %}
  {% for address in (endpoint.addresses | default([])) %}
    server srv {{ address }}:80
  {% endfor %}
{% endfor %}

{# Unsafe: Fails if endpoints field is null #}
{% for endpoint in endpoint_slice.endpoints %}
  {# ERROR if endpoints is null #}
{% endfor %}
```

### Filtering with selectattr

Use `selectattr` to filter resources that have specific attributes, useful for optional Kubernetes fields.

```jinja2
{# Only process rules that have HTTP configuration #}
{% for rule in (ingress.spec.rules | default([]) | selectattr("http", "defined")) %}
  {# rule.http is guaranteed to exist #}
  {% for path in rule.http.paths %}
    {# Process paths #}
  {% endfor %}
{% endfor %}

{# Only process paths with specific pathType #}
{% set path_types = ["Prefix", "Exact"] %}
{% for path in (rule.http.paths | default([]) | selectattr("pathType", "in", path_types)) %}
  {# Only Prefix and Exact paths #}
{% endfor %}
```

### Maintaining Variables Across Loop Scopes

Jinja2/Gonja scoping rules prevent modifying variables inside loops. Use `namespace()` to create a mutable container for accumulating values across iterations.

```jinja2
{# Create namespace for mutable list #}
{% set ns = namespace(active_endpoints=[]) %}

{# Append items inside loops #}
{% for endpoint_slice in resources.endpoints.Fetch(service_name) %}
  {% for endpoint in (endpoint_slice.endpoints | default([])) %}
    {% for address in (endpoint.addresses | default([])) %}
      {% set ns.active_endpoints = ns.active_endpoints + [{'address': address, 'port': port}] %}
    {% endfor %}
  {% endfor %}
{% endfor %}

{# Use accumulated list #}
{% for endpoint in ns.active_endpoints %}
  server srv{{ loop.index }} {{ endpoint.address }}:{{ endpoint.port }}
{% endfor %}
```

### Template Snippet Composition

Break complex templates into reusable snippets for better maintainability and code reuse.

**Define snippets:**

```yaml
template_snippets:
  backend-name:
    name: backend-name
    template: >-
      ing_{{ ingress.metadata.namespace }}_{{ ingress.metadata.name }}_{{ path.backend.service.name }}

  backend-servers:
    name: backend-servers
    template: |
      {% set service_name = path.backend.service.name %}
      {% set port = path.backend.service.port.number %}
      {% include "reserved-server-slots" %}
```

**Use snippets:**

```jinja2
{# Include snippet inline #}
backend {% include "backend-name" %}
    balance roundrobin
    {%- filter indent(4) %}
    {% include "backend-servers" %}
    {%- endfilter %}
```

**Pass variables to snippets:**

```jinja2
{# Set variables before including snippet #}
{% set service_name = "my-service" %}
{% set port = 8080 %}
{% include "backend-servers" %}
```

### Indentation Control

Control whitespace in generated output using the `-` marker to strip whitespace.

```jinja2
{# Strip whitespace before tag #}
{%- for item in items %}
  {{ item }}
{%- endfor %}

{# Strip whitespace after tag #}
{% for item in items -%}
  {{ item }}
{% endfor -%}

{# Indent included content #}
backend my_backend
    {%- filter indent(4, first=True) %}
    {% include "server-list" %}
    {%- endfilter %}
```

**Result with indentation:**
```
backend my_backend
    server srv1 192.168.1.10:80
    server srv2 192.168.1.11:80
```

## Complete Examples

### Example 1: Simple Host-Based Routing

Basic Ingress to backend mapping with map-based routing:

```yaml
# Configuration
watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    index_by: ["metadata.namespace", "metadata.name"]

maps:
  host.map:
    template: |
      {%- for ingress in resources.ingresses.List() %}
      {%- for rule in (ingress.spec.rules | default([])) %}
      {{ rule.host }} backend_{{ ingress.metadata.name }}
      {%- endfor %}
      {%- endfor %}

haproxy_config:
  template: |
    global
        daemon

    defaults
        mode http
        timeout connect 5s
        timeout client 50s
        timeout server 50s

    frontend http
        bind *:80
        # Use map for host-based routing
        use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

    {% for ingress in resources.ingresses.List() %}
    backend backend_{{ ingress.metadata.name }}
        balance roundrobin
        server srv1 192.168.1.10:80 check
    {% endfor %}
```

### Example 2: Path-Based Routing with Maps

Using map files for both host and path-based routing:

```yaml
maps:
  host.map:
    template: |
      {%- for ingress in resources.ingresses.List() %}
      {%- for rule in (ingress.spec.rules | default([])) %}
      {{ rule.host }} {{ rule.host }}
      {%- endfor %}
      {%- endfor %}

  path-prefix.map:
    template: |
      {%- for ingress in resources.ingresses.List() %}
      {%- for rule in (ingress.spec.rules | default([])) %}
      {%- for path in (rule.http.paths | default([]) | selectattr("pathType", "equalto", "Prefix")) %}
      {{ rule.host }}{{ path.path }}/ backend_{{ ingress.metadata.name }}_{{ path.backend.service.name }}
      {%- endfor %}
      {%- endfor %}
      {%- endfor %}

haproxy_config:
  template: |
    frontend http
        bind *:80

        # Normalize host header
        http-request set-var(txn.host) req.hdr(Host),field(1,:),lower
        http-request set-var(txn.host_match) var(txn.host),map({{ pathResolver.GetPath("host.map", "map") }})

        # Path-based routing with prefix matching
        http-request set-var(txn.backend) var(txn.host_match),concat(,txn.path,),map_beg({{ pathResolver.GetPath("path-prefix.map", "map") }})

        use_backend %[var(txn.backend)]
        default_backend default_backend

    {% for ingress in resources.ingresses.List() %}
    {% for rule in (ingress.spec.rules | default([])) %}
    {% for path in (rule.http.paths | default([])) %}
    backend backend_{{ ingress.metadata.name }}_{{ path.backend.service.name }}
        balance roundrobin
        server srv1 192.168.1.10:80 check
    {% endfor %}
    {% endfor %}
    {% endfor %}

    backend default_backend
        http-request return status 404
```

### Example 3: Dynamic Backend Servers with Cross-Resource Lookups

Complete ingress → service → endpoints chain using reserved slots pattern:

```yaml
watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    index_by: ["metadata.namespace", "metadata.name"]

  endpoints:
    api_version: discovery.k8s.io/v1
    kind: EndpointSlice
    index_by: ["metadata.labels.kubernetes\\.io/service-name"]

template_snippets:
  backend-servers-with-slots:
    name: backend-servers-with-slots
    template: |
      {%- set initial_slots = 10 %}

      {# Collect active endpoints using indexed lookup #}
      {%- set ns = namespace(active_endpoints=[]) %}
      {%- for endpoint_slice in resources.endpoints.Fetch(service_name) %}
        {%- for endpoint in (endpoint_slice.endpoints | default([])) %}
          {%- for address in (endpoint.addresses | default([])) %}
            {%- set ns.active_endpoints = ns.active_endpoints + [{'name': endpoint.targetRef.name, 'address': address, 'port': port}] %}
          {%- endfor %}
        {%- endfor %}
      {%- endfor %}

      {# Generate fixed server slots #}
      {%- for i in range(1, initial_slots + 1) %}
        {%- if loop.index0 < ns.active_endpoints|length %}
          {%- set endpoint = ns.active_endpoints[loop.index0] %}
      server SRV_{{ i }} {{ endpoint.address }}:{{ endpoint.port }} check
        {%- else %}
      server SRV_{{ i }} 127.0.0.1:1 disabled
        {%- endif %}
      {%- endfor %}

haproxy_config:
  template: |
    global
        daemon
        maxconn 4096

    defaults
        mode http
        timeout connect 5s
        timeout client 50s
        timeout server 50s
        option httpchk GET /healthz

    frontend http
        bind *:80
        use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]

    {% for ingress in resources.ingresses.List() %}
    {% for rule in (ingress.spec.rules | default([])) %}
    {% for path in (rule.http.paths | default([])) %}
    {%- set service_name = path.backend.service.name %}
    {%- set port = path.backend.service.port.number | default(80) %}

    backend ing_{{ ingress.metadata.name }}_{{ service_name }}
        balance roundrobin
        default-server check inter 2s
        {%- filter indent(4, first=True) %}
        {% include "backend-servers-with-slots" %}
        {%- endfilter %}

    {% endfor %}
    {% endfor %}
    {% endfor %}
```

**Result**: Endpoint changes only update server addresses via HAProxy runtime API without reloads. Server slot names (`SRV_1`, `SRV_2`, etc.) remain stable.

## See Also

- [Template Engine Reference](../pkg/templating/README.md) - Detailed templating API and error handling
- [Configuration Reference](supported-configuration.md) - Complete configuration schema
- [Gonja Documentation](https://github.com/nikolalohinski/gonja) - Full template syntax reference
- [Helm Chart Values](../charts/haproxy-template-ic/values.yaml) - Production-ready template examples
- [HAProxy Configuration Manual](https://docs.haproxy.org/2.9/configuration.html) - HAProxy configuration reference
