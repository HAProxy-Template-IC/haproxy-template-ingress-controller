---
description: "HAPTIC (HAProxy Template Ingress Controller) is a template-driven HAProxy ingress controller for Kubernetes. Watch any resource, render templates, and deploy to HAProxy via Dataplane API."
hide:
  - navigation
---

# HAPTIC

**HAPTIC** (HAProxy Template Ingress Controller) is a template-driven [HAProxy](https://www.haproxy.org/) Ingress Controller for Kubernetes that generates HAProxy configurations using [Scriggo](https://scriggo.com/) templates and deploys them via the [HAProxy Dataplane API](https://github.com/haproxytech/dataplaneapi).

<div class="hx-pipeline" role="img" aria-label="How HAPTIC works: cluster resources feed your templates, the rendered config is validated, then deployed to the HAProxy fleet">
  <div class="hx-group">
    <span class="hx-cap">Your cluster</span>
    <span class="hx-chip">🌐 Ingress</span>
    <span class="hx-chip">🔀 Gateway API</span>
    <span class="hx-chip">🧩 Any CRD</span>
  </div>
  <div class="hx-link" aria-hidden="true"><i></i></div>
  <div class="hx-card hx-hot">
    <span class="hx-cap">Templates</span>
    <strong>Your templates</strong>
    <small>full control over haproxy.cfg</small>
  </div>
  <div class="hx-link" aria-hidden="true"><i></i></div>
  <div class="hx-card">
    <span class="hx-cap">Gate</span>
    <strong>Validated</strong>
    <small>schema&nbsp;+&nbsp;haproxy&nbsp;-c</small>
  </div>
  <div class="hx-link" aria-hidden="true"><i></i></div>
  <div class="hx-card">
    <span class="hx-cap">Fleet</span>
    <strong>HAProxy</strong>
    <small>reload-free updates<br>where possible</small>
  </div>
</div>

!!! note "Community Project"
    This is an independent community project and is not affiliated with or endorsed by HAProxy Technologies.

## What is HAPTIC?

HAPTIC is an event-driven Kubernetes controller that:

- **Watches any Kubernetes resource** - Ingresses, Services, Secrets, Gateway API resources, or any custom resource type you configure
- **Renders Scriggo templates** - A Go-native template engine
- **Validates before deployment** - Every rendered config passes syntax, schema, and `haproxy -c` checks before it reaches your load balancers
- **Deploys configurations** to HAProxy pods via the Dataplane API

Unlike traditional ingress controllers with hardcoded configuration logic, HAPTIC uses a template-driven approach that gives you full control over the generated HAProxy configuration. This means you can:

- **Define custom annotations** that your platform users can use, implemented with just a few lines of template code
- **Support new standards** like Gateway API without waiting for controller updates
- **Watch domain-specific CRDs** and generate HAProxy configuration from any Kubernetes resource type

## Key Features

### Template-Driven Flexibility

Traditional ingress controllers embed configuration logic in code. HAPTIC inverts this:

- **Full HAProxy access** - If HAProxy supports it, your templates can emit it — every section, every directive in the [configuration manual](https://www.haproxy.com/documentation/haproxy-configuration-manual/latest/)
- **Add features without code changes** - New directives are template updates, not controller releases
- **Rich template context** - Access any Kubernetes resource, fetch external data via HTTP, and use controller state in your templates
- **Everything is templatable** - Generate not just `haproxy.cfg` but also map files, SSL certificates, CRT-lists, and custom auxiliary files

### Production Ready

- **High availability** - Leader election with automatic failover
- **Layered validation** - Admission webhook, template validation, and tests you can run in CI before anything reaches a cluster
- **Observability** - Prometheus metrics, structured logging, and debug endpoints

!!! note "Ready to use out of the box"
    The [Helm chart](deploying-with-helm.md) ships with [Template Libraries](template-libraries.md) enabled by default. They cover Kubernetes Ingress and Gateway API resources with annotation support comparable to existing HAProxy ingress controllers — no template authoring required. Customizing or extending the templates is entirely optional.

## Architecture

The controller follows an event-driven architecture where changes to Kubernetes resources trigger a pipeline that renders templates, validates the output, and syncs configurations to HAProxy pods.

<div class="hx-pipeline hx-arch" role="img" aria-label="Runtime architecture: the controller pod watches the Kubernetes API, renders and validates the config, and pushes it to each HAProxy pod's Dataplane API">
  <div class="hx-group">
    <span class="hx-cap">Kubernetes API</span>
    <span class="hx-chip">🗂️ Any resource</span>
    <small>Ingress · Gateway · CRDs</small>
  </div>
  <div class="hx-link" aria-hidden="true"><i></i></div>
  <div class="hx-group hx-pod">
    <span class="hx-cap">Controller pod</span>
    <span class="hx-chip">👀 Watcher</span>
    <span class="hx-vlink" aria-hidden="true"></span>
    <span class="hx-chip">📝 Template engine</span>
    <span class="hx-vlink" aria-hidden="true"></span>
    <span class="hx-chip">🛡️ Validator</span>
  </div>
  <div class="hx-link" aria-hidden="true"><i></i></div>
  <div class="hx-group hx-pod">
    <span class="hx-cap">HAProxy pod</span>
    <span class="hx-chip">🔌 Dataplane API</span>
    <span class="hx-vlink" aria-hidden="true"></span>
    <span class="hx-chip">⚡ HAProxy</span>
  </div>
</div>

Key components:

- **Watcher** - Subscribes to Kubernetes API for configured resource types
- **Template Engine** - Renders Scriggo templates with resource data as context
- **Validator** - Runs syntax, schema, and `haproxy -c` checks on the rendered config so broken configs never deploy
- **Dataplane Syncer** - Applies configuration changes to HAProxy pods via the Dataplane API

## Quick Start

```bash
helm install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic --version 0.2.0-alpha.1 --namespace haptic --create-namespace
```

This installs both the controller and a 2-replica HAProxy Deployment, plus the default template libraries that cover Ingress and Gateway API out of the box. For the full walkthrough — including a sample app and end-to-end verification — see [Getting Started](getting-started.md).

### Inspect the Deployed Configuration

Once you have an Ingress (or Gateway, HTTPRoute, …) the controller writes the rendered HAProxy config to a read-only `HAProxyCfg` CRD on every reconciliation:

```bash
kubectl describe haproxycfg -n haptic
```

!!! note "CRD short names"
    `HAProxyCfg` (singular `haproxycfg`, short name `hpcfg`) is the *output*. The *input* — templates, watched resources, dataplane settings — lives in `HAProxyTemplateConfig` (short names `htplcfg`, `haptpl`). Edit that one, not `HAProxyCfg`. Use `kubectl describe` rather than `kubectl get -o yaml`, since the latter renders multiline configs as literal `\n`.

### What Makes HAPTIC Different

Templates are the difference. Suppose your platform users want a custom annotation that injects an `X-Request-ID` header for tracing. One snippet — no controller fork, no waiting for a release (with the Helm chart you'd place it under `controller.config.templateSnippets` in your values):

<div class="pg-embed" markdown data-scenario="extend" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="A custom annotation, implemented as one snippet" data-height="440">

<p class="pg-task" markdown>The `frontend-filters-300-request-id` snippet under `templateSnippets` implements the annotation. In the **Resources** panel, change the `shop` Ingress's `example.com/request-id-header` value to `X-Trace-ID` — or remove the annotation — and watch the `http-request set-header` line in `haproxy.cfg` follow.</p>

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
spec:
  templateSnippets:
    # The frontend-filters-* glob picks this up automatically; the 300 prefix
    # places it alongside the built-in header-manipulation snippets.
    frontend-filters-300-request-id:
      template: |
        {%- for _, ingress := range resources.ingresses.List() %}
        {%- var header = ingress | dig("metadata", "annotations", "example.com/request-id-header") | fallback("") | tostring() %}
        {%- if header != "" %}
        http-request set-header {{ header }} %[uuid()]
        {%- end %}
        {%- end %}
```

</div>

Users opt in per-Ingress with `example.com/request-id-header: "X-Request-ID"`. The same pattern works for rate limiting, header rewrites, custom ACLs — anything HAProxy can express. Override any snippet, replace the main template, or disable all libraries and start from scratch. See the [Templating Guide](templating.md).

## Where to Go Next

- **Essential**: [Getting Started](getting-started.md) → [Templating](templating.md) → [CRD Reference](crd-reference.md)
- **Custom resources beyond Ingress**: [Watching Resources](watching-resources.md)
- **Template tests for CI/CD**: [Validation Tests](validation-tests.md)
- **Reference**: [Supported Configuration](supported-configuration.md), [Troubleshooting](troubleshooting.md)
- **Helm chart configuration**: [Deploying with Helm](deploying-with-helm.md)
