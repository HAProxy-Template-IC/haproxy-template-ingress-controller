## User Interface Design

This is a headless controller with no graphical user interface. Interaction occurs through:

1. **ConfigMap**: Primary configuration interface
2. **Kubernetes Resources**: Watched resources (Ingress, Service, etc.)
3. **Metrics Endpoint**: Prometheus metrics on `:9090/metrics` (configurable)
4. **Health Endpoint**: Liveness/readiness on `:8080/healthz` (configurable)
5. **Debug Endpoint**: Runtime introspection on configurable port (disabled by default, typically `:6060/debug/vars` when enabled) with JSONPath support and pprof
6. **Logs**: Structured JSON logs for operational visibility

## Configuration Example

The following example demonstrates a complete controller configuration with all major features:

```yaml
pod_selector:
  match_labels:
    app: haproxy
    component: loadbalancer

# Grouped controller configuration (previously CLI options)
controller:
  healthz_port: 8080
  metrics_port: 9090

logging:
  verbose: 2  # 0=WARNING, 1=INFO, 2=DEBUG

validation:
  dataplane_host: localhost
  dataplane_port: 5555

# Fields to omit from indexed resources (reduces memory usage)
watched_resources_ignore_fields:
  - metadata.managedFields

watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    # Enable validation webhook for Ingress resources to prevent faulty configs
    enable_validation_webhook: true
    # Default indexing by namespace and name for standard iteration
    index_by: ["metadata.namespace", "metadata.name"]

  endpoints:
    api_version: discovery.k8s.io/v1
    kind: EndpointSlice
    # Leave validation disabled for critical resources like EndpointSlices
    enable_validation_webhook: false
    # Custom indexing by service name for O(1) service-to-endpoints matching
    index_by: ["metadata.labels['kubernetes.io/service-name']"]

  secrets:
    api_version: v1
    kind: Secret
    # Enable validation for TLS secrets to catch certificate issues early
    enable_validation_webhook: true
    # Index by namespace and type for efficient TLS secret lookup
    index_by: ["metadata.namespace", "type"]

  services:
    api_version: v1
    kind: Service
    enable_validation_webhook: false
    # Index by namespace and app label for cross-resource matching
    index_by: ["metadata.namespace", "metadata.labels['app']"]

template_snippets:
  backend-name:
    name: backend-name
    template: |
      ing_{{ ingress.metadata.namespace }}_{{ ingress.metadata.name }}_{{ path.backend.service.name }}_{{ path.backend.service.port.name | default(path.backend.service.port.number) }}

  path-map-entry:
    name: path-map-entry
    template: |
      {{ "" }}
      {% for ingress in resources.ingresses.List() %}
      {% for rule in (ingress.spec.rules | default([]) | selectattr("http", "defined")) %}
      {% for path in (rule.http.paths | default([]) | selectattr("path", "defined") | selectattr("pathType", "in", path_types)) %}
      {{ rule.host }}{{ path.path }} {% include "backend-name" %}{{ suffix }}
      {% endfor %}
      {% endfor %}
      {% endfor %}

  validate-ingress:
    name: validate-ingress
    template: |
      {#- Validation snippet for ingress resources #}
      {%- if not ingress.spec %}
        {% do register_error('ingresses', ingress.metadata.uid, 'Ingress missing spec') %}
      {%- endif %}
      {%- if ingress.spec.rules %}
        {%- for rule in ingress.spec.rules %}
          {%- if not rule.host %}
            {% do register_error('ingresses', ingress.metadata.uid, 'Ingress rule missing host') %}
          {%- endif %}
        {%- endfor %}
      {%- endif %}

  backend-servers:
    name: backend-servers
    template: |
      {#- Pre-allocated server pool with auto-expansion #}
      {%- set initial_slots = 10 %}  {#- Single place to adjust initial slots #}

      {#- Collect active endpoints #}
      {%- set active_endpoints = [] %}
      {%- for endpoint_slice in resources.get('endpoints', {}).get_indexed(service_name) %}
        {%- for endpoint in endpoint_slice.get('endpoints') | default([], true) %}
          {%- for address in endpoint.addresses %}
            {%- set _ = active_endpoints.append({'name': endpoint.targetRef.name, 'address': address, 'port': port}) %}
          {%- endfor %}
        {%- endfor %}
      {%- endfor %}

      {#- Calculate required slots using mathematical approach #}
      {%- set active_count = active_endpoints|length %}
      # active count = {{ active_count }}
      {%- if initial_slots > 0 and active_count > 0 %}
        {%- set ratio = active_count / initial_slots %}
        {%- set power_of_two = [0, ratio | log(2) | round(0, 'ceil')] | max %}
      {%- else %}
        {%- set power_of_two = 0 %}
      {%- endif %}
      {%- set max_servers = initial_slots * (2 ** power_of_two) | int %}
      # max servers = {{ max_servers }}

      {#- Generate all server slots with fixed names #}
      {%- for i in range(1, max_servers + 1) %}
        {%- if loop.index0 < active_endpoints|length %}
          {#- Active server with real endpoint #}
          {%- set endpoint = active_endpoints[loop.index0] %}
        server SRV_{{ i }} {{ endpoint.address }}:{{ endpoint.port }}
        {%- else %}
          {#- Disabled placeholder server #}
        server SRV_{{ i }} 127.0.0.1:1 disabled
        {%- endif %}
      {%- endfor %}

  ingress-backends:
    name: ingress-backends
    template: |
      {#- Generate all backend definitions from ingress resources #}
      {#- Usage: {% include "ingress-backends" %} #}
      {%- for _, ingress in resources.get('ingresses', {}).items() %}
      {% include "validate-ingress" %}
      {%- if ingress.spec and ingress.spec.rules %}
      {%- for rule in ingress.spec.rules %}
      {%- if rule.http and rule.http.paths %}
      {%- for path in rule.http.paths %}
      {%- if path.backend and path.backend.service %}
      {%- set service_name = path.backend.service.name %}
      {%- set port = path.backend.service.port.number | default(80) %}
      backend {% include "backend-name" %}
        balance roundrobin
        option httpchk GET {{ path.path | default('/') }}
        default-server check
        {% include "backend-servers" %}
      {%- endif %}
      {%- endfor %}
      {%- endif %}
      {%- endfor %}
      {%- endif %}
      {%- endfor %}

maps:
  host.map:
    template: |
      {%- for _, ingress in resources.get('ingresses', {}).items() %}
      {%- for rule in (ingress.spec.get('rules', []) | selectattr("http", "defined")) %}
      {%- set host_without_asterisk = rule.host | replace('*', '', 1) %}
      {{ host_without_asterisk }} {{ host_without_asterisk }}
      {%- endfor %}
      {%- endfor %}

  path-exact.map:
    template: |
      # This map is used to match the host header (without ":port") concatenated with the requested path (without query params) to an HAProxy backend defined in haproxy.cfg.
      # It should be used with the equality string matcher. Example:
      #   http-request set-var(txn.path_match) var(txn.host_match),concat(,txn.path,),map(/etc/haproxy/maps/path-exact.map)
      {%- set path_types = ["Exact"] %}
      {%- set suffix = "" %}
      {% include "path-map-entry" %}

  path-prefix-exact.map:
    template: |
      # This map is used to match the host header (without ":port") concatenated with the requested path (without query params) to an HAProxy backend defined in haproxy.cfg.

      {%- for ingress in resources.ingresses.List() -%}
      {% for rule in (ingress.spec.rules | default([]) | selectattr("http", "defined")) %}
      {% for path in (rule.http.paths | default([]) | selectattr("path", "defined") | selectattr("pathType", "in", ["Prefix", "ImplementationSpecific"])) %}
      {{ rule.host }}{{ path.path }} ing_{{ ingress.metadata.namespace }}_{{ ingress.metadata.name }}_{{ path.backend.service.name }}_{{ path.backend.service.port.name | default(path.backend.service.port.number) }}
      {% endfor %}
      {% endfor %}
      {% endfor %}

  path-prefix.map:
    template: |
      # This map is used to match the host header (without ":port") concatenated with the requested path (without query params) to an HAProxy backend defined in haproxy.cfg.
      # It should be used with the prefix string matcher. Example:
      #   http-request set-var(txn.path_match) var(txn.host_match),concat(,txn.path,),map_beg(/etc/haproxy/maps/path-prefix.map)
      {%- set path_types = ["Prefix", "ImplementationSpecific"] %}
      {%- set suffix = "/" %}
      {% include "path-map-entry" %}

files:
  400.http:
    template: |
      HTTP/1.0 400 Bad Request
      Cache-Control: no-cache
      Connection: close
      Content-Type: text/html

      <html><body><h1>400 Bad Request</h1>
      <p>Your browser sent a request that this server could not understand.</p>
      </body></html>

  403.http:
    template: |
      HTTP/1.0 403 Forbidden
      Cache-Control: no-cache
      Connection: close
      Content-Type: text/html

      <html><body><h1>403 Forbidden</h1>
      <p>You don't have permission to access this resource.</p>
      </body></html>

  408.http:
    template: |
      HTTP/1.0 408 Request Time-out
      Cache-Control: no-cache
      Connection: close
      Content-Type: text/html

      <html><body><h1>408 Request Time-out</h1>
      <p>Your browser didn't send a complete request in time.</p>
      </body></html>

  500.http:
    template: |
      HTTP/1.0 500 Internal Server Error
      Cache-Control: no-cache
      Connection: close
      Content-Type: text/html

      <html><body><h1>500 Internal Server Error</h1>
      <p>An internal server error occurred.</p>
      </body></html>

  502.http:
    template: |
      HTTP/1.0 502 Bad Gateway
      Cache-Control: no-cache
      Connection: close
      Content-Type: text/html

      <html><body><h1>502 Bad Gateway</h1>
      <p>The server received an invalid response from an upstream server.</p>
      </body></html>

  503.http:
    template: |
      HTTP/1.0 503 Service Unavailable
      Cache-Control: no-cache
      Connection: close
      Content-Type: text/html

      <html><body><h1>503 Service Unavailable</h1>
      <p>No server is available to handle this request.</p>
      </body></html>

  504.http:
    template: |
      HTTP/1.0 504 Gateway Time-out
      Cache-Control: no-cache
      Connection: close
      Content-Type: text/html

      <html><body><h1>504 Gateway Time-out</h1>
      <p>The server didn't respond in time.</p>
      </body></html>

haproxy_config:
  template: |
    global
      log stdout len 4096 local0 info
      chroot /var/lib/haproxy
      user haproxy
      group haproxy
      daemon
      ca-base /etc/ssl/certs
      crt-base /etc/haproxy/certs
      tune.ssl.default-dh-param 2048

    defaults
      mode http
      log global
      option httplog
      option dontlognull
      option log-health-checks
      option forwardfor
      option httpchk GET /
      timeout connect 5000
      timeout client 50000
      timeout server 50000
      errorfile 400 {{ pathResolver.GetPath("400.http", "file") }}
      errorfile 403 {{ pathResolver.GetPath("403.http", "file") }}
      errorfile 408 {{ pathResolver.GetPath("408.http", "file") }}
      errorfile 500 {{ pathResolver.GetPath("500.http", "file") }}
      errorfile 502 {{ pathResolver.GetPath("502.http", "file") }}
      errorfile 503 {{ pathResolver.GetPath("503.http", "file") }}
      errorfile 504 {{ pathResolver.GetPath("504.http", "file") }}

    frontend status
      bind *:8404
      no log
      http-request return status 200 content-type text/plain string "OK" if { path /healthz }
      http-request return status 200 content-type text/plain string "READY" if { path /ready }

    frontend http_frontend
      bind *:80

      # Set a few variables
      http-request set-var(txn.base) base
      http-request set-var(txn.path) path
      http-request set-var(txn.host) req.hdr(Host),field(1,:),lower
      http-request set-var(txn.host_match) var(txn.host),map(/etc/haproxy/maps/host.map)
      http-request set-var(txn.host_match) var(txn.host),regsub(^[^.]*,,),map(/etc/haproxy/maps/host.map,'') if !{ var(txn.host_match) -m found }
      http-request set-var(txn.path_match) var(txn.host_match),concat(,txn.path,),map(/etc/haproxy/maps/path-exact.map)
      http-request set-var(txn.path_match) var(txn.host_match),concat(,txn.path,),map(/etc/haproxy/maps/path-prefix-exact.map) if !{ var(txn.path_match) -m found }
      http-request set-var(txn.path_match) var(txn.host_match),concat(,txn.path,),map_beg(/etc/haproxy/maps/path-prefix.map) if !{ var(txn.path_match) -m found }

      # Use path maps for routing
      use_backend %[var(txn.path_match)]

      # Default backend
      default_backend default_backend

    {% include "ingress-backends" %}

    backend default_backend
        http-request return status 404
```

**Configuration Highlights**:

1. **Pod Selector**: Identifies HAProxy pods using `app: haproxy` and `component: loadbalancer` labels

2. **Watched Resources**: Four resource types with strategic indexing:
   - **Ingresses**: Indexed by namespace and name for iteration, validation webhook enabled
   - **EndpointSlices**: Indexed by service name for O(1) endpoint lookup
   - **Secrets**: Indexed by namespace and type for TLS certificate management
   - **Services**: Indexed by namespace and app label for cross-resource matching

3. **Template Snippets**: Reusable template components:
   - **backend-name**: Generates consistent backend names from ingress metadata
   - **path-map-entry**: Creates map entries for different path types
   - **validate-ingress**: Validates ingress resources during rendering
   - **backend-servers**: Dynamic server pool with auto-expansion (powers-of-two scaling)
   - **ingress-backends**: Generates complete backend definitions from ingresses

4. **Maps**: Three routing maps for different match types:
   - **host.map**: Host-based routing with wildcard support
   - **path-exact.map**: Exact path matching
   - **path-prefix.map**: Prefix-based path matching

5. **Files**: HTTP error response pages (400, 403, 408, 500, 502, 503, 504)

6. **HAProxy Configuration**: Complete configuration with:
   - Global settings and defaults
   - Status frontend for health checks
   - HTTP frontend with advanced routing using maps
   - Dynamic backend generation via template inclusion

This configuration demonstrates production-ready patterns including resource indexing optimization, validation webhooks for critical resources, and dynamic backend scaling.

