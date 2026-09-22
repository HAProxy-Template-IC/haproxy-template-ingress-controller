# Report status from a template

Report the result of a configuration change on a watched resource with
`statusPatch()`. The bundled Ingress and Gateway API libraries already report
status; use custom patches for resources or conditions your own templates manage.

## Choose when to report status {#statuspatch}

Pass the watched resource and a map of status variants to `statusPatch()`.
Use `deployed` for a successful configuration deployment, `deployFailed` for a
failed deployment, `rendered` for successful rendering, and `renderFailed` for
an error later in rendering. The controller selects the variant for the outcome.

Each variant contains the fields inside `.status`; don't wrap it in another
`status` key. See the [function reference](template-reference.md#statuspatch)
for the full contract.

## Build a condition {#condition}

Use `condition()` to build a Kubernetes condition, including its status, reason,
message, and observed resource generation. `toJSON()` displays the result:

<div class="pg-embed" markdown data-scriggo data-title="condition() builds a status condition" data-height="220">

<p class="pg-task">Change the message and inspect the resulting condition.</p>

```go
{{ condition("Accepted", "True", "Accepted", "Resource is accepted", 1, "2024-01-01T00:00:00Z") | toJSON() }}
```

</div>

The parameter list is in the [Template Reference](./template-reference.md#condition).

## Preserve the transition time {#transitiontime}

Use `transitionTime()` with the resource's existing conditions. It keeps
`lastTransitionTime` when the status is unchanged and returns the current time
when the status changes or the condition is new.

<div class="pg-embed" markdown data-scriggo data-title="transitionTime() keeps or refreshes a timestamp" data-height="320">

<p class="pg-task">Run the example and compare the unchanged and changed timestamps.</p>

```go
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

## Register the patch {#using-status-patches-in-custom-templates}

When extending the chart, put patches in a `status-patches-*` template snippet
so they're available even if later configuration rendering fails.

The example below registers a Gateway condition. Run it and open the **status**
tab to inspect the proposed patch. A resource's API schema must support the status
fields you write; Ingress, for example, has no `status.conditions` field.

<div class="pg-embed" markdown data-tab="status" data-controls="tabs,resources" data-title="Emit a status patch" data-height="440">

<p class="pg-task">Change the Gateway generation to 4. Find the matching <code>observedGeneration</code> in the status output.</p>

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: status-patch-demo
spec:
  watchedResources:
    gateways:
      apiVersion: gateway.networking.k8s.io/v1
      resources: gateways
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
      {%- for _, gateway := range resources.gateways.List() %}
      {%%
        var gen = gateway.metadata.generation
        var existing = dig(gateway, "status", "conditions")
        statusPatch(gateway, map[string]any{
          "deployed": map[string]any{
            "conditions": []any{
              condition("Programmed", "True", "Programmed", "Configuration deployed", gen, transitionTime(existing, "Programmed", "True")),
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
  - apiVersion: gateway.networking.k8s.io/v1
    kind: Gateway
    metadata:
      name: demo
      namespace: shop
      generation: 3
    spec:
      gatewayClassName: haptic
      listeners:
        - name: http
          protocol: HTTP
          port: 80
```

</div>

The browser shows the proposed patch without updating a cluster. In a running
installation, the resource schema must expose the status fields, and the
controller needs permission to patch that resource's `/status` subresource.
