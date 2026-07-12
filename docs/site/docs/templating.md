# Templating

## Overview

HAPTIC uses [Scriggo](https://scriggo.com/), a Go template engine, to generate HAProxy configurations from Kubernetes resources. The Helm chart ships with ready-to-use [template libraries](template-libraries.md) that cover standard Ingress and Gateway API use cases — you only need to write templates when you want to extend or replace that default behavior. Templates access watched Kubernetes resources, and the controller renders them whenever resources change, validates the output, and deploys it to HAProxy instances.

Templates are rendered automatically when any watched resource changes, during initial synchronization, or periodically for drift detection.

<div class="pg-embed" markdown data-scenario="ingress" data-facade="spec.templateSnippets.backends-500-ingress" data-tab="haproxy.cfg" data-title="See a template render — live" data-controls="tabs,provenance" data-height="480">
</div>

Hit **Run live** above to render the bundled Ingress example entirely in your browser. Edit the template on the left and watch `haproxy.cfg` update on the right — then switch tabs to see the `maps`, `files`, and `status` it also produces. Click any output line to jump to the template line that produced it, or **Open in full playground** to bring your changes into the full editor.

## What you can template

| Template Type | Use When |
|---------------|----------|
| `haproxyConfig` | Main HAProxy configuration (frontends, backends, global settings) |
| `maps` | HAProxy lookup tables for host/path routing decisions |
| `files` | Auxiliary files like custom error pages |
| `sslCertificates` | TLS certificate files assembled from Kubernetes Secrets |

### HAProxy Configuration

The main `haproxyConfig` template generates the complete HAProxy configuration file. This one loops over the watched Ingresses and emits a backend for each — run it, then add or edit an Ingress on the right and watch the backends change.

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="One backend per Ingress" data-height="480">

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: haproxy-config-demo
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      indexBy:
        - metadata.name
  maps:
    host.map:
      template: |
        {%- for _, ingress := range resources.ingresses.List() %}
        {%- for _, rule := range ingress.spec.rules %}
        {{ rule.host }} {{ ingress.metadata.name }}
        {%- end %}
        {%- end %}
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
      {%- for _, ingress := range resources.ingresses.List() %}
      backend {{ ingress.metadata.name }}
        balance roundrobin
      {%- end %}
```

```yaml
apiVersion: v1
kind: List
items:
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: shop
    spec:
      rules:
        - host: shop.example.com
          http:
            paths:
              - path: /
                pathType: Prefix
                backend:
                  service:
                    name: shop
                    port:
                      number: 80
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: blog
    spec:
      rules:
        - host: blog.example.com
          http:
            paths:
              - path: /
                pathType: Prefix
                backend:
                  service:
                    name: blog
                    port:
                      number: 80
```

</div>

!!! important
    Whenever your HAProxy config references a map file, error file, certificate, or crt-list, use `pathResolver.GetPath(filename, type)` instead of a hard-coded path. The controller deploys these files to a configurable directory (set in `spec.dataplane.mapsDir`, `sslCertsDir`, `generalStorageDir`) and `pathResolver` knows where they live, so the path stays correct even if you reconfigure those directories.

Now that you've seen a config render, try editing one. This template has no loops — just a static `frontend` — so you can focus on the edit-and-run cycle.

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-focus="11" data-title="Your turn: add an HSTS header" data-difficulty="1">

<p class="pg-task" markdown>Add a line to the `frontend web` section so every response carries a `Strict-Transport-Security` header, then hit **Run live** and watch line&nbsp;11 of the output. (Hint: HAProxy's `http-response set-header`.)</p>

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: hsts-demo
spec:
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend web
        bind *:80
        # TODO(you): add a line so every response carries an HSTS header
        default_backend app
      backend app
        server s1 127.0.0.1:8080 check
```

<details class="pg-solution" markdown>
<summary>Peek at the solution</summary>

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: hsts-demo
spec:
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend web
        bind *:80
        http-response set-header Strict-Transport-Security "max-age=31536000; includeSubDomains"
        default_backend app
      backend app
        server s1 127.0.0.1:8080 check
```

</details>
</div>

!!! note "Named and multiple `defaults` sections"
    The `haproxyConfig` template's rendered text *is* the HAProxy configuration — HAPTIC parses, validates, and deploys it as written, so any construct your HAProxy version accepts is available. That includes multiple named `defaults` sections: a `defaults <name>` block that later `frontend`, `backend`, or `listen` sections opt into with `from <name>`. HAPTIC's config comparator tracks each `defaults` section by name and creates, updates, or deletes them independently. The bundled `base` library ships a single unnamed `defaults` section; add named ones in your own template or snippets when a subset of sections needs different defaults.

### Map files

Each `maps` entry renders one HAProxy lookup table. They're written to `spec.dataplane.mapsDir` (default `/etc/haproxy/maps/`) on the HAProxy pod. This template turns each Ingress host into a backend-name entry — switch to the **maps** tab to read the generated `host.map`.

<div class="pg-embed" markdown data-tab="maps" data-controls="tabs,resources" data-title="A host → backend map" data-height="440">

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: map-demo
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      indexBy:
        - metadata.name
  maps:
    host.map:
      template: |
        {%- for _, ingress := range resources.ingresses.List() %}
        {%- for _, rule := range ingress.spec.rules %}
        {%- if len(rule.http.paths) > 0 %}
        {{ rule.host }} ing_{{ ingress.metadata.name }}
        {%- end %}
        {%- end %}
        {%- end %}
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend http
        bind *:80
        use_backend %[req.hdr(host),lower,map({{ pathResolver.GetPath("host.map", "map") }})]
```

```yaml
apiVersion: v1
kind: List
items:
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: shop
    spec:
      rules:
        - host: shop.example.com
          http:
            paths:
              - path: /
                pathType: Prefix
                backend:
                  service:
                    name: shop
                    port:
                      number: 80
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: blog
    spec:
      rules:
        - host: blog.example.com
          http:
            paths:
              - path: /
                pathType: Prefix
                backend:
                  service:
                    name: blog
                    port:
                      number: 80
```

</div>

### General files

Auxiliary files like custom error pages. Written to `spec.dataplane.generalStorageDir` (default `/etc/haproxy/general/`). The `errorfile` directive points HAProxy at the rendered file — open the **files** tab to see `503.http`.

<div class="pg-embed" markdown data-tab="files" data-controls="tabs" data-title="A custom 503 error page" data-height="440">

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: files-demo
spec:
  files:
    503.http:
      template: |
        HTTP/1.0 503 Service Unavailable
        Cache-Control: no-cache
        Connection: close
        Content-Type: text/html

        <html><body><h1>503 Service Unavailable</h1></body></html>
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend http
        bind *:80
        errorfile 503 {{ pathResolver.GetPath("503.http", "file") }}
        default_backend web
      backend web
        server s1 10.0.0.1:8080 check
```

</div>

### SSL certificates

SSL/TLS certificate files are assembled from Kubernetes Secrets. Written to `spec.dataplane.sslCertsDir` (default `/etc/haproxy/ssl/`). This reads a TLS Secret and concatenates its certificate and key into one PEM — the **certs** tab shows the result.

<div class="pg-embed" markdown data-tab="certs" data-controls="tabs,resources" data-title="A PEM assembled from a Secret" data-height="440">

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: cert-demo
spec:
  watchedResources:
    secrets:
      apiVersion: v1
      resources: secrets
      indexBy:
        - metadata.namespace
        - metadata.name
  sslCertificates:
    example-com.pem:
      template: |
        {%- var secret = resources.secrets.GetSingle("default", "example-com-tls") %}
        {%- if secret != nil %}
        {{ secret.data["tls.crt"] | b64decode() }}
        {{ secret.data["tls.key"] | b64decode() }}
        {%- end %}
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend web
        bind *:443 ssl crt {{ pathResolver.GetPath("example-com.pem", "cert") }}
        default_backend app
      backend app
        server s1 10.0.0.1:8080 check
```

```yaml
apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: Secret
    type: kubernetes.io/tls
    metadata:
      name: example-com-tls
      namespace: default
    data:
      tls.crt: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURUekNDQWplZ0F3SUJBZ0lVV3ZyRGg3bVB5ck5rclB2N1FjeWQ1cXBZVFZFd0RRWUpLb1pJaHZjTkFRRUwKQlFBd056RVVNQklHQTFVRUF3d0xaWGhoYlhCc1pTNWpiMjB4SHpBZEJnTlZCQW9NRmtoQlVGUkpReUJRYkdGNQpaM0p2ZFc1a0lFUmxiVzh3SGhjTk1qWXdOekE1TWpNd056QTJXaGNOTXpZd056QTJNak13TnpBMldqQTNNUlF3CkVnWURWUVFEREF0bGVHRnRjR3hsTG1OdmJURWZNQjBHQTFVRUNnd1dTRUZRVkVsRElGQnNZWGxuY205MWJtUWcKUkdWdGJ6Q0NBU0l3RFFZSktvWklodmNOQVFFQkJRQURnZ0VQQURDQ0FRb0NnZ0VCQUxtYXBnQlZTNERmQ29jcApNUk1ocnIxeG42M1RCL3plL2kxT3hQV1k5eUhmc0hOelZPakRUT054elE1SERVMVFBUFZXb2I0YmlKemZWbDF6Cm5qVCs4MkordXVUZWVCbWUxcFJhRUhyNjgvbWxCelAvM3V0NDBDNlJ1Y0xSbzVWYlVvd3d2WnpOVHJGbW1Jdk4KcDdXdVNsWDFhTFBSSENvRE0zYUtndU94MS9MdHl6TGw3eGtPdkRBa0ZoYmNWc0tVSUFzb01KaWliREYrdzBYZApXenJDUmZOSDdzMjNldTBDRDBnZk1lT0lTV3R5MU40SWRUT2NBcGU4aWpMNi80SkJYOG51NmFhOXMwd3JmMXhpCm9yeEhEV2dDMFpva21EMGlvZ0NYaWptNXFJUGZySnZ5NkMyNzgrRnErK2I3ZzR0dzlFdjlmS1YyeGJYUjdNVTQKTTNRaUZpVUNBd0VBQWFOVE1GRXdIUVlEVlIwT0JCWUVGSUQzOG51WmszaklHQVRVZWMzV3pwMi9tNmpxTUI4RwpBMVVkSXdRWU1CYUFGSUQzOG51WmszaklHQVRVZWMzV3pwMi9tNmpxTUE4R0ExVWRFd0VCL3dRRk1BTUJBZjh3CkRRWUpLb1pJaHZjTkFRRUxCUUFEZ2dFQkFHQmFYa1JhcTRReEoxTDl2WHdnemlyWjR1dzltRzBWL1gzVkNtUDUKVXhicnJrQ3JiZzZEYURYRWpUTEk5bm92VVFmK2NaMWhPRDI0TDN4d1dvUHZ2Z25BNlBlR240c2F1Q0Z0WFNrSwp5RzZOemFrWmdjdHY0OHUzQnNLUDRJenZmTVRhZENNWmlyb2xMV0MrWWlDc1doSVRSR1RSd3JnVXlwN3JiTVgzCk9uNXpEYlU3MjU4RXhiN01NYlBvMlpJRWZZcUErKzIzVlZ6alBQamR4Yy81NjhLZTFPZUhKenR3SG5ENmk3WVAKM3NaTyt0dC83OU5TQlBUNk5TcUg2eWdGWUpCMWpYOWhYKzA1VHJzb010UnVUMmFsU1duY2VVOHJRd2dYalFLVQpiZnUrVE4xdnBrVjk0ZFZERnVKRFhhWFIyQ0ptUmVTM1prWDlJYWxNc1cvTHpwWT0KLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo=
      tls.key: LS0tLS1CRUdJTiBQUklWQVRFIEtFWS0tLS0tCk1JSUV2UUlCQURBTkJna3Foa2lHOXcwQkFRRUZBQVNDQktjd2dnU2pBZ0VBQW9JQkFRQzVtcVlBVlV1QTN3cUgKS1RFVElhNjljWit0MHdmODN2NHRUc1QxbVBjaDM3QnpjMVRvdzB6amNjME9SdzFOVUFEMVZxRytHNGljMzFaZApjNTQwL3ZOaWZycmszbmdabnRhVVdoQjYrdlA1cFFjei85N3JlTkF1a2JuQzBhT1ZXMUtNTUwyY3pVNnhacGlMCnphZTFya3BWOVdpejBSd3FBek4yaW9ManNkZnk3Y3N5NWU4WkRyd3dKQllXM0ZiQ2xDQUxLRENZb213eGZzTkYKM1ZzNndrWHpSKzdOdDNydEFnOUlIekhqaUVscmN0VGVDSFV6bkFLWHZJb3krditDUVYvSjd1bW12Yk5NSzM5YwpZcUs4Uncxb0F0R2FKSmc5SXFJQWw0bzV1YWlEMzZ5Yjh1Z3R1L1BoYXZ2bSs0T0xjUFJML1h5bGRzVzEwZXpGCk9ETjBJaFlsQWdNQkFBRUNnZ0VBRW4zcmN4WU1ienNKbi96RkpHeFRMaEcvZ0lDSmg3S3A3VmF2UGU3dkZHTm0KZjZJcWdBUlJTVW5oemIzYmYrdnNKSVZzbVBYQ1R5cmJQblZSK21LNldnSlpXWXNtdVJxL3Mwa2o0alRWa1BaVgp1T01SMFRFWXdNTUpHSFZ0a0dob1dZcFRvZWM4bzJVZTVyTG5OaTAydjhpekZWTk10SXpjR0QvbG1ZenpBSU53CkV0UFJRRHdsMks1NDFFckdZTjA1c2RyQmFWNkFFdjRFWHh4cldzVXJCK3k2cW1XQ1kvUDdSUHkwNzFCVHJnTmUKSkhYUnk5NnJOSE9DUHZYK1kzQWRYSGw4T01yMTV0M3IyMVVlMmpqVlltY29UT1pSTTVMSjN2emRRSEFESFV4ZQoyZUFORXJkWGNNdVgyUi9wK0IvNnBtUE1LVTJLT2JJeWlOK1p0Zm9ya3dLQmdRRHFLZ083Z1BqY0RIVGc4bEdaCk14Z282emErL1VaOUN2K2JMTzk2RzBzWlpkUEJpYjR0cStvMXRnSXlqWjZ5SHBzbTBpanRSZHhjZEtuQXlIcUcKNmRwU3pJbXlUQU9DV3JsbkFFY05XQitIeTR1cTVuMUY3M0VrSitiYi9saDRUbm94SmFSeEIweDM3QjJlRVhBcQppUkhjeGdyKzljOTU3ajVuSk5RWnJ2eE1id0tCZ1FESzZXZW9jcEdSeFoxM1ZUYUVrWERFL3ZQaVBpWVJBWEZjCmVQUmVrNnhZbVAxdmxDVUdpK2VPNGgyTW9ycEoxWVBlbDBzcHNDTCs2bk5ZV0Z2K3cyUjlsb0RqY1BOSnY0WGQKdkdGeFRzS0Zkdlp0ZkxodVpqeXljM01FeWRpckt3dmpuK2lieHo2NWZOdWtWcjFhSlExQnUvN2wycmJTSEsxbwpzSERiOENsNHF3S0JnQVhMb0dnRm15TW5FOFYxZWR1R3pqUkZEZ2ZRRU95TFZ5UXFDb3RGSGFpMVFuWnB5RkV0CkRoRGlQayt0L1oxKzhHd1hpM2ZENE41UTdOcWVtNW0zTS9ZVXBkdkowZFJxRm1pY015WDdabHhnQjBibGlYZ3YKb3VjNExaaUlSUHhGUlBUdWI1RjBrc250Q0JhZmE5MUJveldKbVVBU0tWNWxMUm8wYVNOeGwwRDFBb0dCQU1hVgpWV0J5OStwdE42WFJYTEN6VW1WSmkwL1JPUm9OaW05UTVRQW1rRmFKTEFkbU9qSkUrOU5Ia2xuUDdIZFVJbUhYCk9iVkw3NFFCMmU4TlVzTnJZTTdVVzhHOENpNFQ1YVJUdUIzWFVlS2l3WnYzb3R4UTdIaE5LclQyQWpuS3dERCsKai96ZEs1TUhFa0tzclZZcXl1V1pZbVo3L2M1MlNIUWJzZWhlQzRoUEFvR0FDNW9zY2NqQlpiK2xMOW9lMnp1WgpZQ0pDMjNzQnB2bnc2cmFBdXMzZXBFdDVXQnBxL0t0cmhEVjBvL1FaVU1JUEtOM3d3dUxyd01pM0VsMHNLand2CmtHNGxhRThhU1BGek16TjBVdTRXbEhCY01xT2N3UVpVUzIwM2o4eTl3SjVtdVllNU9FMzRUdndOQ3dtVFZXNkcKK3RkNElYaHgvMGpEbXZaSzNjRDd5V3M9Ci0tLS0tRU5EIFBSSVZBVEUgS0VZLS0tLS0K
```

</div>

!!! note
    Certificate data in Secrets is base64-encoded. Use the `b64decode` filter to decode it.

### Template snippets

Reusable template fragments are included via `{{ render "snippet-name" }}` — or `{{ render_glob "pattern" }}` to pull in every match at once. This config keeps each backend in its own snippet and stitches them into the config with `render_glob`, which renders matches in alphabetical order.

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-controls="tabs" data-title="Snippets assembled with render_glob" data-height="460">

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: snippet-demo
spec:
  templateSnippets:
    backend-api:
      template: |
        backend api
          server s1 10.0.1.5:9000 check
    backend-web:
      template: |
        backend web
          server s1 10.0.0.1:8080 check
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend http
        bind *:80
        default_backend web
      {{ render_glob "backend-*" }}
```

</div>

Include a single snippet in a template:

```go
{{ render "backend-name" }}
```

Include all snippets matching a glob pattern (rendered in alphabetical order):

```go
{{ render_glob "backend-*" }}
```

Pass local variables to rendered snippets with `inherit_context`:

```go
{%- var service_name = "my-service" %}
{{ render "backend-servers" inherit_context }}
```

### Post-processing

The `haproxyConfig` section supports a `postProcessing` list that transforms the rendered output before deployment. Post-processors run sequentially on the rendered configuration.

Available types:

| Type | Description |
|------|-------------|
| `regex_replace` | Line-by-line regex find/replace (`pattern` and `replace` params) |
| `template` | Scriggo template transformation with access to the rendered output via the `input` variable (`source` param) |

The config below renders a `__REGION__` marker, then runs two post-processors in order: a `template` step rewrites the marker to a value, and a `regex_replace` step renames the header. The **haproxy.cfg** tab shows the final, post-processed output.

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-controls="tabs" data-title="Rewriting the output after render" data-height="460">

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: postproc-demo
spec:
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend http
        bind *:80
        http-response set-header X-Region __REGION__
        default_backend web
      backend web
        server s1 10.0.0.1:8080 check
    postProcessing:
      - type: template
        params:
          source: |
            {%- if strings_contains(input, "__REGION__") -%}
            {{ replace(input, "__REGION__", "eu-west-1") }}
            {%- else -%}
            {{ input }}
            {%- end -%}
      - type: regex_replace
        params:
          pattern: "X-Region"
          replace: "X-Deployment-Region"
```

</div>

The `template` post-processor receives the fully rendered output as the `input` variable and has access to all standard Scriggo builtins (`regexp`, `replace`, `len`, `tostring`, etc.). Its output becomes the new rendered content.

## Template syntax

For complete syntax reference, see the [Scriggo documentation](https://scriggo.com/templates).

### Control structures

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

### Helper functions

Beyond Scriggo's built-ins, HAPTIC adds helpers for the patterns ingress templates need: nil-safe navigation (`dig`, `fallback`, `toSlice`), string and map utilities, deduplication (`first_seen`), sorting (`sort_by`), and version gates (`semver_gte`). The [Template Reference](./template-reference.md#functions-and-filters) lists every function with its calling styles and an example each.

Try the helpers live in a pure Scriggo scratchpad — no config, no resources, just
the template language and every function from the reference. Edit it and watch the output.

<div class="pg-embed" markdown data-scriggo data-title="Scriggo scratchpad — try the helpers" data-height="360">

```go
{# Every helper from the Template Reference is available here. Edit freely. #}
{%- var envs = []any{"prod", "dev", "staging"} %}
{%- var sorted = envs | sort_by([]string{"$"}) %}
{%- for _, e := range sorted %}
backend {{ e }}
  server app {{ toLower(tostring(e)) }}.svc:80
{%- end %}
```

</div>

Ready for a challenge? Sort a list of backends heaviest-first, breaking ties by
name. Edit the template to fix the sort — or peek at the solution.

<div class="pg-embed" markdown data-scriggo data-title="Challenge: sort by two keys" data-difficulty="2" data-height="380">

<p class="pg-task" markdown>List the backends heaviest-first, breaking ties by name. Fix the `sorted` line with `sort_by`, then hit **Run live**.</p>

```go
{# Challenge: list the backends heaviest-first, ties broken by name.
   sort_by(items, criteria) sorts a []any by criteria like "$.field:desc". #}
{%- var backends = []any{
    map[string]any{"name": "web", "weight": 10},
    map[string]any{"name": "api", "weight": 30},
    map[string]any{"name": "cache", "weight": 30},
} %}
{#- TODO: sort by weight (desc), then name (asc). Fix the next line. -#}
{%- var sorted = backends %}
{%- for _, be := range sorted %}
server {{ be["name"] }} weight {{ be["weight"] }}
{%- end %}
```

<details class="pg-solution" markdown>
<summary>Solution</summary>

`$.weight:desc` sorts by weight descending; `$.name` breaks ties alphabetically.

```go
{%- var backends = []any{
    map[string]any{"name": "web", "weight": 10},
    map[string]any{"name": "api", "weight": 30},
    map[string]any{"name": "cache", "weight": 30},
} %}
{%- var sorted = backends | sort_by([]string{"$.weight:desc", "$.name"}) %}
{%- for _, be := range sorted %}
server {{ be["name"] }} weight {{ be["weight"] }}
{%- end %}
```

</details>

</div>

Next, use `first_seen` to collapse duplicates — a real pattern when several
routes point at the same backend and you must emit each `backend` block exactly
once.

<div class="pg-embed" markdown data-scriggo data-title="Challenge: emit each backend only once" data-difficulty="3" data-height="380">

<p class="pg-task" markdown>Several routes share a service; emit one `backend` line per unique service instead of one per route.</p>

```go
{%- var routes = []any{
    map[string]any{"host": "a.example.com", "service": "api"},
    map[string]any{"host": "b.example.com", "service": "api"},
    map[string]any{"host": "c.example.com", "service": "web"},
} -%}
{% for _, r := range routes -%}
{%- var svc = r | dig("service") | fallback("") -%}
{#- TODO: a service can back many hosts — emit each backend only once -#}
backend {{ svc }}
{% end -%}
```

<details class="pg-solution" markdown>
<summary>Peek at the solution</summary>

Gate the emit on `first_seen("backend", svc)` — it returns `true` only the first time it sees each service key, so the repeat is skipped.

```go
{%- var routes = []any{
    map[string]any{"host": "a.example.com", "service": "api"},
    map[string]any{"host": "b.example.com", "service": "api"},
    map[string]any{"host": "c.example.com", "service": "web"},
} -%}
{% for _, r := range routes -%}
{%- var svc = r | dig("service") | fallback("") -%}
{% if first_seen("backend", svc) -%}
backend {{ svc }}
{% end -%}
{% end -%}
```

</details>

</div>

### Path resolution

`pathResolver.GetPath(filename, type)` returns the path HAProxy should use to reference a rendered auxiliary file — `type` is one of `"map"`, `"file"`, `"cert"`, or `"crt-list"`. Use it instead of writing paths by hand so the controller and HAProxy agree on where files live. The [Template Reference](./template-reference.md#pathresolver) shows one example per file type and explains how the returned paths resolve against HAProxy's `default-path` directive (and what to keep if you replace the chart's base library).

## Available template data

### Context variables

Templates receive a set of top-level variables: `resources` (the watched-resource stores), `pathResolver`, `capabilities` (HAProxy feature flags), `currentConfig` (the previously deployed config), `shared` (a compute-once cache), `extraContext`, and more. The [Template Reference](./template-reference.md#context-variables) documents each one. The one you'll use constantly is `resources`, covered next.

### The `resources` variable

Templates access watched resources through the `resources` variable. Each store provides `List()`, `Fetch()`, and `GetSingle()` methods.

!!! note
    The keys available under `resources.*` are determined by the `watchedResources` configuration. See [Watching Resources](./watching-resources.md) to add resource types beyond the defaults.

```go
{# List all resources #}
{% for _, ingress := range resources.ingresses.List() %}

{# Fetch by index keys (parameters match indexBy configuration) #}
{% for _, ingress := range resources.ingresses.Fetch("default", "my-ingress") %}

{# Get single resource or nil #}
{% var secret = resources.secrets.GetSingle("default", "my-secret") %}
```

### Typed resource access

When a schema is loaded for a watched resource (live in production, or via `--schema-dir` offline), both the `resources.<name>` store wrapper **and** a top-level global named `<name>` return typed pointers instead of `map[string]any`. Field access goes through the strongly typed struct, so a misspelled field is a compile-time error rather than a silently-`nil` `dig()`.

A typed field resolves by **either** its Go-PascalCase name **or** its lowercase JSON tag: `gw.metadata.name` and `gw.Metadata.Name` reach the same field, because the engine falls back to the JSON tag when the Go field name doesn't match. That's why the lowercase `ingress.spec.rules` / `ingress.metadata.name` examples elsewhere on this page are typed access too — not untyped `dig()`. The code blocks below use the PascalCase form to make the struct mapping explicit, but either spelling compiles.

```go
{# Typed access — fields resolve at engine compile time #}
{%- for _, gw := range resources.gateways.List() %}
  # {{ gw.Metadata.Namespace }}/{{ gw.Metadata.Name }}: {{ len(gw.Spec.Listeners) }} listeners
{%- end %}

{# Identical behaviour via the typed top-level global #}
{%- for _, gw := range gateways %}
  # {{ gw.Metadata.Namespace }}/{{ gw.Metadata.Name }}
{%- end %}
```

**Typed return types.** With a schema loaded, every store method returns typed pointers:

| Call | Return type |
|------|-------------|
| `resources.<name>.List()` | `[]*resources.<name>.T` |
| `resources.<name>.Fetch(keys...)` | `[]*resources.<name>.T` |
| `resources.<name>.GetSingle(keys...)` | `*resources.<name>.T` (nil if not found) |

Without a schema (for example, `haptic-controller validate` without `--schema-dir`), the same calls fall back to `[]any` / `map[string]any` exactly as before. The chart's `dig()`-based snippets work in either mode.

**`<name>.T` is a usable type expression.** Macros, var declarations, type assertions, slice types, and type-switch case clauses all accept it:

```go
{# Macro parameter typed against one kind #}
{% macro RenderGateway(gw *resources.gateways.T) %}
  # gw.Metadata.Name is statically typed here
  # {{ gw.Metadata.Name }}
{% end %}

{# Type-switch dispatch across multiple kinds (polymorphic `any` boundary) #}
{%- switch r := routeInfo["route"].(type) %}
{%- case *resources.httproutes.T %}
  # r is statically *resources.httproutes.T inside this branch
  # {{ r.Metadata.Name }}: {{ len(r.Spec.Rules) }} rules
{%- case *resources.grpcroutes.T %}
  # {{ r.Metadata.Name }} (gRPC)
{%- case *resources.tlsroutes.T %}
  # {{ r.Metadata.Name }} (TLS passthrough)
{%- end %}

{# Slice type for sharded parallel rendering #}
{% var shard []*resources.gateways.T = shard_slice(allGateways, i, n) %}
```

The type-switch case-clause form is the canonical pattern for chart code that crosses a polymorphic `any` boundary — the chart's `gateway` library uses it inside `60-frontend.yaml` to dispatch on HTTPRoute / GRPCRoute / TLSRoute. `shard_slice` is type-preserving: when its input is a typed slice, the result is the same typed slice (not `[]any`), so the downstream loop variable stays statically typed.

**Field name convention:** Go-PascalCase of the JSON tag, with NO acronym preservation. This matters because chart authors are used to upstream Go-style names (`APIVersion`, `IPBlock`) — those don't apply here. (Where the JSON tag already has an uppercase acronym, like `loadBalancerIP`, the typed field keeps it — `LoadBalancerIP` — which happens to match upstream; only rune 0 is ever changed.)

| JSON tag (source YAML)   | Typed field          |
|--------------------------|----------------------|
| `metadata`               | `Metadata`           |
| `spec`                   | `Spec`               |
| `apiVersion`             | `ApiVersion`         |
| `tls`                    | `Tls`                |
| `ingressClassName`       | `IngressClassName`   |
| `matchLabels`            | `MatchLabels`        |
| `clusterIP`              | `ClusterIP`          |
| `loadBalancerIP`         | `LoadBalancerIP`     |
| `kubernetes.io/foo`      | `Kubernetes_io_foo` (non-letter/digit → `_`) |

Templates write `gw.ApiVersion`, not `gw.APIVersion`. Why the convention works this way — and the regression canary that pins it — is covered in [Typed Access Internals](./template-reference.md#typed-access-internals).

**Inside a typed scope** (typed for-range, typed macro parameter, type-switch case branch) use direct field access — no `dig()`, no `tostring()`, no `fallback()` on already-typed primitives. Reach for `dig()` only at genuine polymorphic boundaries (a `routeInfo["route"]` switch entry, an `any` macro parameter, a `shared.Get(...)` return, a ConfigMap with no schema bundled, a `listenerOwner` that may be a Gateway or a ListenerSet, etc.). Mixed-shape chart code — some snippets typed, some not — is the expected adoption pattern, and `dig()` navigates typed structs by JSON tag, so a snippet ported one at a time keeps working without churning its callers.

**Optional fields normalise to nil through `dig()`.** A typegen-produced struct field whose schema entry is *not* in the OpenAPI `required` list carries a `json:"…,omitempty"` tag; `dig()` returns nil when such a field's value is the type's zero value (`""`, `0`, `false`, empty slice). The universal `dig(obj, "field") | fallback(default)` chart pattern therefore behaves identically across typed and untyped shapes — without the normalisation, an unpopulated optional string would return `""`, `fallback()` would skip, and downstream key composition would silently produce malformed strings. Required fields keep their zero values intact.

**Schema source.** Typed shapes are generated from each resource's OpenAPI v3 schema:

- **Production:** the controller fetches schemas live from the kube-apiserver — CRDs via their embedded `openAPIV3Schema`, Kubernetes core resources via the apiserver's OpenAPI v3 endpoint.
- **Offline (`haptic-controller validate` / chart `validationTests` / `scripts/test-templates.sh`):** schemas come from a directory passed via `--schema-dir` (or `HAPTIC_SCHEMA_DIR` env var). The directory accepts full CRD YAMLs (`kubectl get crd X -o yaml` output) and bare OpenAPI v3 `spec.Schema` files with an `x-kubernetes-group-version-kind` extension. Without `--schema-dir`, no resources receive typed support; templates that reach for typed access in that case fail at engine compile time with a clear "no schema for X" pointer back to `--schema-dir`.

This repo's `tests/schemas/` bundles schemas for both the Gateway API CRDs / haptic CRDs *and* the Kubernetes built-ins the chart watches (Namespace, Service, Secret, EndpointSlice, Ingress). All built-ins are CRD-wrapped so the offline GVK resolver picks up the (`apiVersion`, plural) mapping — `haptic-controller validate --schema-dir tests/schemas` therefore unlocks typed access for every chart-watched resource, not just the CRDs. The chart-test script auto-wires this directory; copy it into your own project's schema-dir if you reuse the bundled libraries. To refresh from a running cluster, run `scripts/fetch-k8s-openapi-schemas.sh` (queries `kubectl get --raw '/openapi/v3/...'`, inlines `$ref`s, emits CRD-wrapped YAML).

### Index Configuration

The `indexBy` field on a `watchedResources` entry determines what parameters `Fetch()` expects — see [Watching Resources — Indexing](./watching-resources.md#indexing-indexby) for index shapes, prefix scans, and the dot-escaping rule for label keys.

## Custom template variables

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
{% if extraContext.debug %}
  http-response set-header X-Debug %[be_name]
{% end %}

global
  maxconn {{ extraContext.limits.maxConn }}
```

## Common patterns

### Reading a custom annotation

Custom annotations are the usual way to let application teams opt individual Ingresses into behavior your templates control, without a controller fork or a new release. Read the annotation off the resource and branch on its value.

The config below defines the `haptic.example.com/balance` annotation: when an Ingress carries it, its backend uses that load-balancing algorithm; otherwise it falls back to `roundrobin`. The `shop` Ingress sets `leastconn`; `blog` sets nothing. Run it, then edit either Ingress's annotation in the **Resources** panel and watch the `balance` line follow.

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="A custom annotation drives the balance algorithm" data-height="480">

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: custom-annotation-demo
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      indexBy: ["metadata.namespace", "metadata.name"]
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      {%- for _, ingress := range resources.ingresses.List() %}
      backend {{ ingress.metadata.name }}
        {%- var algo = ingress.metadata.annotations["haptic.example.com/balance"] %}
        {%- if algo != "" %}
        balance {{ algo }}
        {%- else %}
        balance roundrobin
        {%- end %}
        server app {{ ingress.metadata.name }}.svc:80 check
      {%- end %}
```

```yaml
apiVersion: v1
kind: List
items:
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: shop
      namespace: default
      annotations:
        haptic.example.com/balance: leastconn
    spec:
      rules:
        - host: shop.example.com
          http:
            paths:
              - path: /
                pathType: Prefix
                backend:
                  service:
                    name: shop
                    port:
                      number: 80
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: blog
      namespace: default
    spec:
      rules:
        - host: blog.example.com
          http:
            paths:
              - path: /
                pathType: Prefix
                backend:
                  service:
                    name: blog
                    port:
                      number: 80
```

</div>

`ingress.metadata.annotations` is a typed `map[string]string`, so indexing an absent key returns `""` — the `algo != ""` check covers both a missing annotation and an empty one. Pick an annotation prefix you own (here `haptic.example.com/`) so it can't collide with another controller's. The same read-and-branch pattern drives rate limits, header rewrites, custom ACLs — anything HAProxy can express. In the chart, place the snippet under a `features-*` or `backend-directives-*` extension point so the bundled libraries pick it up (see [Template Libraries](template-libraries.md#injecting-custom-configuration)).

### Reserved server slots (avoid reloads)

Pre-allocate server slots so endpoint changes update server addresses through the runtime API instead of triggering a reload. Run this to watch the active endpoints fill the low-numbered slots while the spares stay `disabled`:

<div class="pg-embed" markdown data-scriggo data-title="Reserved server slots" data-height="360">

```go
{# Reserved slots: real endpoints fill the low-numbered slots; the spare
   slots stay `disabled` so HAProxy can enable them at runtime — no reload.
   Add a third endpoint and re-run to watch a spare slot light up. #}
{%- var initial_slots = 5 %}
{%- var active_endpoints = []any{
    map[string]any{"address": "10.244.1.10", "port": 8080},
    map[string]any{"address": "10.244.2.11", "port": 8080},
} %}
default-server check
{%- for i := 1; i <= initial_slots; i++ %}
  {%- if i-1 < len(active_endpoints) %}
    {%- var ep = active_endpoints[i-1] %}
server SRV_{{ i }} {{ ep["address"] }}:{{ ep["port"] }} enabled
  {%- else %}
server SRV_{{ i }} 192.0.2.1:1 disabled
  {%- end %}
{%- end %}
```

</div>

**Benefit**: Endpoint changes update server addresses via runtime API without dropping connections.

!!! tip "Maximize Runtime API Usage"
    Keep server lines minimal - only `address:port` plus `enabled` or `disabled`. Place all other options (`check`, `proto h2`, SSL settings) in the `default-server` directive:

    ```haproxy
    backend my-backend
        default-server check proto h2
        server SRV_1 10.0.0.1:8080 enabled
        server SRV_2 10.0.0.2:8080 enabled
        server SRV_3 192.0.2.1:1 disabled
    ```

    The Dataplane API can update Address, Port, and enabled/disabled state at runtime without reloading HAProxy. Both `enabled` and `disabled` are runtime-supported, enabling the reserved slots pattern. Options like `check` on individual server lines trigger reloads on any change.

### Cross-Resource Lookups

Use a field from one resource to query another. Each Ingress's backend service name drives a `Fetch()` into the matching EndpointSlices — run it, then edit the Ingress or the endpoints and watch the backend servers change:

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Ingress → EndpointSlice lookup" data-height="460">

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: cross-resource-demo
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      indexBy: ["metadata.namespace", "metadata.name"]
    endpoints:
      apiVersion: discovery.k8s.io/v1
      resources: endpointslices
      indexBy: ["metadata.labels.kubernetes\\.io/service-name"]
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      {%- for _, ing := range resources.ingresses.List() %}
      {%- for _, rule := range ing.spec.rules %}
      {%- for _, path := range rule.http.paths %}
      {%- var svc = path.backend.service.name %}
      {%- var port = fallback(path.backend.service.port.number, 80) %}
      backend ing_{{ ing.metadata.name }}_{{ svc }}
        {%- for _, es := range resources.endpoints.Fetch(svc) %}
        {%- for _, ep := range es.endpoints %}
        {%- for _, addr := range ep.addresses %}
        server {{ fallback(ep.targetRef.name, addr) }} {{ addr }}:{{ port }} check
        {%- end %}
        {%- end %}
        {%- end %}
      {%- end %}
      {%- end %}
      {%- end %}
```

```yaml
apiVersion: v1
kind: List
items:
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: shop
      namespace: storefront
    spec:
      rules:
        - host: shop.example.com
          http:
            paths:
              - path: /
                pathType: Prefix
                backend:
                  service:
                    name: shop
                    port:
                      number: 80
  - apiVersion: discovery.k8s.io/v1
    kind: EndpointSlice
    metadata:
      name: shop-a1b2
      namespace: storefront
      labels:
        kubernetes.io/service-name: shop
    addressType: IPv4
    endpoints:
      - addresses: [10.244.1.10]
        targetRef: {name: shop-pod-1}
        conditions: {ready: true}
      - addresses: [10.244.2.11]
        targetRef: {name: shop-pod-2}
        conditions: {ready: true}
```

</div>

The two `indexBy` entries above are what make the lookup work: `ingresses` is indexed by namespace + name, and `endpoints` is indexed by the `kubernetes.io/service-name` label so `Fetch(svc)` returns every EndpointSlice for that service (dots in label keys need escaping — see [Watching Resources — Indexing](./watching-resources.md#indexing-indexby)).

### Safe Iteration

Wrap every field access in `dig(...) | toSlice()` so a missing field yields an empty range instead of a panic. The second endpoint below has no `addresses`, so it's skipped rather than breaking the render:

<div class="pg-embed" markdown data-scriggo data-title="Safe iteration over missing fields" data-height="320">

```go
{# dig()+toSlice() never panics on a missing field, so the endpoint with
   no addresses is skipped instead of breaking the render. #}
{%- var endpoints = []any{
    map[string]any{"addresses": []any{"10.0.0.1"}},
    map[string]any{},
} %}
{%- for _, ep := range endpoints %}
{%- for _, addr := range ep | dig("addresses") | toSlice() %}
server srv {{ addr }}:80
{%- end %}
{%- end %}
```

</div>

### Filtering with conditionals

Test a field before you use it to skip resources that lack it. Only the rule with an `http` section produces a backend line; the bare TCP host is filtered out:

<div class="pg-embed" markdown data-scriggo data-title="Filter by field presence" data-height="320">

```go
{# Only rules that have an http section become backends. #}
{%- var rules = []any{
    map[string]any{"host": "web.example.com", "http": map[string]any{"paths": []any{}}},
    map[string]any{"host": "tcp.example.com"},
} %}
{%- for _, rule := range rules %}
{%- if dig(rule, "http") != nil %}
backend {{ dig(rule, "host") | tostring() }}
{%- end %}
{%- end %}
```

</div>

### Challenge: Add health checks

Put the loop-and-`dig` pattern to work:

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-focus="13-16" data-title="Challenge: give every server a health check" data-difficulty="1">

<p class="pg-task" markdown>This config renders two backends from an inline list, but the generated `server` lines have no health checking — HAProxy keeps routing to a pod even after it dies. Add `check` to the generated `server` line so every server gets an active health check.</p>

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: health-check-demo
spec:
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend http
        bind *:80
        default_backend web
      {%- var backends = []any{
        map[string]any{"name": "web", "servers": []any{"10.0.0.1:8080", "10.0.0.2:8080"}},
        map[string]any{"name": "api", "servers": []any{"10.0.1.5:9000"}},
      } %}
      {%- for _, be := range backends %}
      backend {{ be | dig("name") | tostring() }}
      {%- for i, addr := range be | dig("servers") | toSlice() %}
        server srv{{ i }} {{ addr | tostring() }}
      {%- end %}
      {%- end %}
```

<details class="pg-solution" markdown>
<summary>Peek at the solution</summary>

Append `check` to the `server` line inside the loop, so HAProxy health-checks each pod and stops sending traffic to unhealthy ones. Pair it with `init-addr last` when a server address is a DNS name, so HAProxy still starts if the name is briefly unresolvable.

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: health-check-demo
spec:
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
        daemon
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend http
        bind *:80
        default_backend web
      {%- var backends = []any{
        map[string]any{"name": "web", "servers": []any{"10.0.0.1:8080", "10.0.0.2:8080"}},
        map[string]any{"name": "api", "servers": []any{"10.0.1.5:9000"}},
      } %}
      {%- for _, be := range backends %}
      backend {{ be | dig("name") | tostring() }}
      {%- for i, addr := range be | dig("servers") | toSlice() %}
        server srv{{ i }} {{ addr | tostring() }} check
      {%- end %}
      {%- end %}
```

</details>

</div>

### Challenge: Default a missing port

Combine `dig()` with `fallback()` to supply a default when a field is absent:

<div class="pg-embed" markdown data-scriggo data-title="Challenge: default a missing port to 80" data-difficulty="2" data-height="380">

<p class="pg-task" markdown>One service omits `spec.port`; give every `server` line a port, defaulting to 80 when the field is absent.</p>

```go
{%- var services = []any{
    map[string]any{"name": "api",   "spec": map[string]any{"port": 8080}},
    map[string]any{"name": "web",   "spec": map[string]any{"port": 3000}},
    map[string]any{"name": "cache", "spec": map[string]any{}},
} -%}
{% for _, svc := range services -%}
{%- var name = svc | dig("name") | fallback("") -%}
{#- TODO: cache has no spec.port — dig() returns nil and the port comes out blank -#}
{%- var port = svc | dig("spec", "port") -%}
server {{ name }} {{ name }}.svc:{{ port }}
{% end -%}
```

<details class="pg-solution" markdown>
<summary>Peek at the solution</summary>

Keep the raw `dig` result, pipe it through `fallback(80)`, and use a `nil` check to flag the line that was defaulted.

```go
{%- var services = []any{
    map[string]any{"name": "api",   "spec": map[string]any{"port": 8080}},
    map[string]any{"name": "web",   "spec": map[string]any{"port": 3000}},
    map[string]any{"name": "cache", "spec": map[string]any{}},
} -%}
{% for _, svc := range services -%}
{%- var name = svc | dig("name") | fallback("") -%}
{%- var portVal = svc | dig("spec", "port") -%}
{%- var port = portVal | fallback(80) -%}
server {{ name }} {{ name }}.svc:{{ port }}{% if portVal == nil %}  # default port{% end %}
{% end -%}
```

</details>

</div>

### Mutable variables

Accumulate values across nested loops with `append`, then emit the collected result. This flattens every endpoint address into one numbered server list:

<div class="pg-embed" markdown data-scriggo data-title="Accumulate with append" data-height="360">

```go
{# Collect every address across nested loops, then emit them with a
   running index. #}
{%- var addresses = []any{} %}
{%- var slices = []any{
    map[string]any{"endpoints": []any{
        map[string]any{"addresses": []any{"10.0.0.1"}},
        map[string]any{"addresses": []any{"10.0.0.2"}},
    }},
    map[string]any{"endpoints": []any{
        map[string]any{"addresses": []any{"10.0.0.3"}},
    }},
} %}
{%- for _, es := range slices %}
{%- for _, ep := range es | dig("endpoints") | toSlice() %}
{%- for _, addr := range ep | dig("addresses") | toSlice() %}
{%- addresses = append(addresses, addr) %}
{%- end %}
{%- end %}
{%- end %}
{%- for i, addr := range addresses %}
server srv{{ i + 1 }} {{ addr }}:80
{%- end %}
```

</div>

### Whitespace control

Add `-` inside a tag to trim adjacent whitespace: `{%-` strips whitespace before the tag, `-%}` strips whitespace after it.

```go
{%- for _, item := range items %}   {# Strip before #}
{% for _, item := range items -%}   {# Strip after #}
{%- for _, item := range items -%}  {# Strip both #}
```

The stripped loop below renders one clean line per environment. Delete a dash and re-run to see the blank lines it was removing:

<div class="pg-embed" markdown data-scriggo data-title="Whitespace control" data-height="300">

```go
{# `{%-` strips the newline before the tag and `-%}` strips the one after,
   so this loop renders tight lines instead of a gap-filled block. #}
{%- var envs = []any{"prod", "staging", "dev"} %}
{%- for _, env := range envs %}
server {{ env }}.svc:80
{%- end %}
```

</div>

## Status patches

Templates can register status patches for Kubernetes resources using the `statusPatch()` function. The controller applies these patches to the `/status` subresource via Server-Side Apply (SSA) after each reconciliation phase.

This allows templates to report processing results back to resources (for example, setting `Accepted` and `Programmed` conditions on Gateways, or propagating LoadBalancer addresses to Ingress status) without the controller needing to understand any specific resource's status schema.

### `statusPatch()`

Registers a status patch for a Kubernetes resource with outcome-keyed variants. Each variant's value is the resource's `.status` content directly (for example, `conditions`, `loadBalancer`) — the controller writes it under `.status` via SSA, so don't wrap it in another `status` key:

```go
{% statusPatch(namespace, name, apiVersion, kind, map[string]any{
    "deployed": map[string]any{
        "conditions": []any{
            condition("Accepted", "True", "Accepted", "Resource accepted", generation, transitionTime(dig(resource, "status", "conditions"), "Accepted", "True")),
        },
    },
    "deployFailed": map[string]any{
        "conditions": []any{
            condition("Accepted", "True", "Accepted", "Resource accepted", generation, transitionTime(dig(resource, "status", "conditions"), "Accepted", "True")),
            condition("Programmed", "False", "AddressNotAssigned", "No address available", generation, transitionTime(dig(resource, "status", "conditions"), "Programmed", "False")),
        },
    },
}) %}
```

Templates render all variants upfront; the controller selects the variant matching the pipeline outcome (`rendered`, `deployed`, `renderFailed`, or `deployFailed`). The [Template Reference](./template-reference.md#statuspatch) lists the parameters and when each variant applies.

### `condition()`

Creates a `metav1.Condition`-compatible map. Run it — `toJSON` makes the returned map visible:

<div class="pg-embed" markdown data-scriggo data-title="condition() builds a status condition" data-height="220">

```go
{# condition() returns a metav1.Condition-shaped map; pipe it through toJSON to see it. #}
{{ condition("Accepted", "True", "Accepted", "Resource is accepted", 1, "2024-01-01T00:00:00Z") | toJSON() }}
```

</div>

The parameter list is in the [Template Reference](./template-reference.md#condition).

### `transitionTime()`

Returns the correct `lastTransitionTime` for a condition: preserves the existing timestamp if the condition status hasn't changed, or returns the current time if it has changed or doesn't exist yet. The first argument is the resource's existing conditions list — navigate to it yourself with `dig(resource, "status", "conditions")`, so the helper stays agnostic to where a given resource keeps its conditions. Run the demo with a literal conditions list:

<div class="pg-embed" markdown data-scriggo data-title="transitionTime() keeps or refreshes a timestamp" data-height="320">

```go
{# In a real template you'd navigate to the existing conditions with
   dig(resource, "status", "conditions"); here it's a literal so the demo runs. #}
{%- var existing = []any{
    map[string]any{"type": "Accepted", "status": "True", "lastTransitionTime": "2024-01-01T00:00:00Z"},
} %}
{# Status still "True" -> the existing 2024 timestamp is preserved: #}
unchanged: {{ transitionTime(existing, "Accepted", "True") }}
{# Status flipped to "False" -> a fresh current timestamp is returned: #}
changed:   {{ transitionTime(existing, "Accepted", "False") }}
```

</div>

For resources with nested condition arrays (for example, Gateway API Route `parents[]`), navigate to the parent's conditions first — see the [Template Reference](./template-reference.md#transitiontime) for the pattern.

### Using status patches in custom templates

In the chart, status patch snippets should use the `status-patches-*` extension point (priority 200). This renders after feature analysis but before complex config generation, ensuring patches are captured even if later rendering fails.

The embed below is a self-contained version that patches an Ingress with typed field access. Run it and open the **status** tab to see the `.status.conditions` HAPTIC would write back:

<div class="pg-embed" markdown data-tab="status" data-controls="tabs,resources" data-title="Emit a status patch" data-height="440">

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: status-patch-demo
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      indexBy: ["metadata.namespace", "metadata.name"]
  haproxyConfig:
    template: |
      global
        log stdout format raw local0
      defaults
        mode http
        timeout connect 5s
        timeout client 30s
        timeout server 30s
      frontend web
        bind :80
        default_backend app
      backend app
        server s1 127.0.0.1:8080 check
      {%- for _, ingress := range resources.ingresses.List() %}
      {%%
        var ns = ingress.metadata.namespace
        var name = ingress.metadata.name
        var gen = fallback(ingress.metadata.generation, 0)
        // Ingress status has no typed conditions field, so reach for it with dig.
        var existing = dig(ingress, "status", "conditions")
        statusPatch(ns, name, "networking.k8s.io/v1", "Ingress", map[string]any{
          "deployed": map[string]any{
            "conditions": []any{
              condition("Ready", "True", "Deployed", "Ingress programmed into HAProxy", gen, transitionTime(existing, "Ready", "True")),
            },
          },
        })
      %%}
      {%- end %}
```

```yaml
apiVersion: v1
kind: List
items:
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: demo
      namespace: shop
      generation: 3
    spec:
      rules:
        - host: demo.example.com
          http:
            paths:
              - path: /
                pathType: Prefix
                backend:
                  service:
                    name: demo
                    port:
                      number: 80
```

</div>

The built-in Ingress and Gateway API libraries already include status patch snippets. You only need custom status patches for resources not covered by the default libraries.

## Complete example

Full ingress → service → endpoints chain with reserved slots, using typed access throughout. Press **Run live**, open the **maps** tab for the host map, and edit the resources to add or remove endpoints:

<div class="pg-embed" markdown data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Ingress → endpoints, with reserved slots" data-height="560">

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
      {%- for _, rule := range ingress.spec.rules %}
      {{ rule.host }} ing_{{ ingress.metadata.name }}
      {%- end %}
      {%- end %}

templateSnippets:
  backend-servers:
    template: |
      {%- var initial_slots = 10 %}
      {%- var active_endpoints = []map[string]any{} %}
      {%- for _, es := range resources.endpoints.Fetch(service_name) %}
        {%- for _, ep := range es.endpoints %}
          {%- for _, addr := range ep.addresses %}
            {%- active_endpoints = append(active_endpoints, map[string]any{"addr": addr}) %}
          {%- end %}
        {%- end %}
      {%- end %}
      {%- for i := 1; i <= initial_slots; i++ %}
        {%- if i-1 < len(active_endpoints) %}
      server SRV_{{ i }} {{ active_endpoints[i-1]["addr"] }}:{{ port }} check
        {%- else %}
      server SRV_{{ i }} 192.0.2.1:1 disabled
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
    {% for _, rule := range ingress.spec.rules %}
    {% for _, path := range rule.http.paths %}
    {%- var service_name = path.backend.service.name %}
    {%- var port = fallback(path.backend.service.port.number, 80) %}

    backend ing_{{ ingress.metadata.name }}
        balance roundrobin
        {{ render "backend-servers" inherit_context }}
    {% end %}
    {% end %}
    {% end %}
```

```yaml
apiVersion: v1
kind: List
items:
  - apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata: { name: shop, namespace: storefront }
    spec:
      rules:
        - host: shop.example.com
          http:
            paths:
              - path: /
                pathType: Prefix
                backend:
                  service:
                    name: shop-svc
                    port:
                      number: 8080
  - apiVersion: discovery.k8s.io/v1
    kind: EndpointSlice
    metadata:
      name: shop-svc-abc
      namespace: storefront
      labels:
        kubernetes.io/service-name: shop-svc
    endpoints:
      - addresses: ["10.244.1.10"]
      - addresses: ["10.244.1.11"]
    ports:
      - port: 8080
```

</div>

## See also

- [Template Reference](./template-reference.md) — context variables, functions and filters, `pathResolver`, status-patch parameters
- [Validation Tests](./validation-tests.md) — assert on rendered output before it reaches a cluster
- [Watching Resources](./watching-resources.md) — stores, indexing, selectors, and debounce
- [Template Engine Reference](https://gitlab.com/haproxy-haptic/haptic/blob/main/pkg/templating/README.md)
- [Scriggo Documentation](https://scriggo.com/templates)
- [HAProxy Configuration Manual](https://www.haproxy.com/documentation/haproxy-configuration-manual/latest/)
