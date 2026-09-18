---
description: "HAPTIC (HAProxy Template Ingress Controller) is a template-driven HAProxy ingress controller for Kubernetes. Watch any resource, render templates, and apply them to your HAProxy fleet."
hide:
  - navigation
---

# HAPTIC

**HAPTIC** (HAProxy Template Ingress Controller) is a template-driven [HAProxy](https://www.haproxy.org/) Ingress Controller for Kubernetes that generates HAProxy configurations using [Scriggo](https://scriggo.com/) templates and applies them to your HAProxy fleet — reloading only when the change needs one.

<div class="hx-pipeline" role="img" aria-label="How HAPTIC works: cluster resources feed your templates, and the HAPTIC agent applies the resulting configuration to HAProxy">
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
    <span class="hx-cap">Deployment</span>
    <strong>HAPTIC agent</strong>
    <small>runtime updates or reload</small>
  </div>
  <div class="hx-link" aria-hidden="true"><i></i></div>
  <div class="hx-card">
    <span class="hx-cap">Fleet</span>
    <strong>HAProxy</strong>
    <small>reload-free updates<br>where possible</small>
  </div>
</div>

!!! note "Community Project"
    This is an independent community project and isn't affiliated with or endorsed by HAProxy Technologies.

<a id="what-is-haptic"></a>

## What's HAPTIC?

HAPTIC is an event-driven Kubernetes controller that:

- **Watches any Kubernetes resource** - Ingresses, Services, Secrets, Gateway API resources, or any custom resource type you configure
- **Renders Scriggo templates** - A Go-native template engine
- **Checks configurations** - Admission and config loading run `haproxy -c` synchronously. Reconciliation checks HAProxy configuration alongside deployment and runs configured auxiliary-file validators before dispatch; see [validation behavior](operations/debugging.md#haproxy-refused-the-config-the-fleet-was-given-configvalidatedfalse).
- **Applies configurations** to HAProxy pods through the HAPTIC agent, which runs map, certificate and server changes on the live worker instead of reloading

Unlike traditional ingress controllers with hardcoded configuration logic, HAPTIC uses a template-driven approach that gives you full control over the generated HAProxy configuration. This means you can:

- **Define custom annotations** that your platform users can use, implemented with just a few lines of template code
- **Support new standards** like Gateway API without waiting for controller updates
- **Watch domain-specific CRDs** and generate HAProxy configuration from any Kubernetes resource type

## Key features

### Routing and application policies

The bundled libraries provide these capabilities without custom templates:

| Capability | What you configure |
| --- | --- |
| [Ingress and Gateway API](libraries/gateway.md) | HTTPRoute, GRPCRoute, TLSRoute, TCPRoute, ListenerSet, weighted routing, mirroring, backend TLS, and frontend client-certificate authentication. |
| [Native annotations](libraries/haptic-annotations.md) | API-key, JSON Web Token (JWT), and HMAC authentication, consumer groups, redirects, CORS, compression, and bandwidth limits. |
| [Shared rate limits](libraries/haptic-annotations.md#rate-and-bandwidth-limiting) and [response caching](libraries/haptic-annotations.md#shared-response-cache) | Fleet-wide request budgets with Valkey and a shared Varnish cache, both opt-in. |
| [Request validation](libraries/haptic-annotations.md#api-gateway) and [WAF policies](libraries/haptic-annotations.md#reusable-waf-policies) | JSON Schema request validation and reusable Coraza Web Application Firewall (WAF) policies, including namespace-scoped policies. |
| [Governance](operations/governance.md) | Administrator-defined defaults and constraints on any watched resource, with audit or admission rejection. |
| [TLS configuration](ssl-certificates.md) | Certificate rotation, dual RSA/ECDSA certificates, shared session-ticket keys, and cipher/protocol policy. |
| [Migration libraries](migrating.md) | Opt-in compatibility with ingress-nginx, haproxy-ingress, and HAProxy Technologies annotations. |

### Templates and resource APIs

[Scriggo templates](templating.md) can read typed fields and use collection
pipelines, fetch external data, and generate HAProxy configuration, certificates,
and auxiliary files. The controller discovers [resource versions and schemas](watching-resources.md)
at runtime and adapts when watched CRDs change.

Share snippets through [`HAProxyTemplateLibrary`](crd-reference.md#haproxytemplatelibrary)
resources. Use [`k8sResources`](crd-reference.md#k8sresources) to manage related
Kubernetes objects, including fields whose initial value belongs to the template
but whose running value follows an operator or autoscaling controller.

Try templates and their validation fixtures in the browser [playground](https://haproxy-haptic.org/playground/)
or edit the live examples throughout these docs.

### Deployment and operations

- **Runtime updates:** The [HAPTIC agent](supported-configuration.md) applies eligible map, certificate, and server changes without reloading. HAProxy 3.4 also supports eligible backend additions and removals; new listeners, backend profiles, and inline rules still require a reload.
- **Incremental rendering:** Unchanged template results are reused, and [follower replicas](operations/high-availability.md) keep their render state warm. See [performance and sizing](operations/performance.md) for resource requirements.
- **Validation:** [`haptic preflight`](operations/validate-before-deploy.md) and Helm hooks check a candidate before rollout. Configuration loading enforces embedded tests; admission validates proposed routing changes, including [pluggable output validators](operations/pluggable-validators.md).
- **Deployment inspection:** [`haptic diff`](development/design/deployment.md) predicts reloads, and [`haptic agent state`](development/agent.md) reports a pod's applied configuration and recovery state.
- **Observability:** [JSON access logs and per-route metrics](operations/monitoring.md) identify the route, backend, and policy outcomes. Optional [distributed tracing](reference.md#logging-and-templating) exports request and upstream spans through Vector.

!!! warning "Project maturity"
    HAPTIC uses pre-1.0 versioning, and its custom resources use API version `v1alpha1`. Minor releases can change APIs and configuration. Pin an exact chart version (`--version 0.2.0-alpha.3`) and read the [changelog](changelog.md) before you upgrade.

!!! note "Ready to use out of the box"
    The [Helm chart](deploying-with-helm.md) enables Ingress, Gateway API, and [HAPTIC annotations](libraries/haptic-annotations.md) by default. Enable a vendor annotation library when [migrating](migrating.md) from another controller. Write custom templates only for behavior the bundled libraries don't cover.

## Architecture

Resource changes trigger rendering and per-pod deployment. Admission checks proposed changes; reconciliation runs auxiliary-file validation before dispatch and HAProxy checks alongside deployment.

<div class="hx-pipeline hx-arch" role="img" aria-label="Runtime architecture: the controller watches the Kubernetes API, renders configuration, and sends each pod its changes through the HAPTIC agent">
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
    <span class="hx-chip">📤 Deployer</span>
  </div>
  <div class="hx-link" aria-hidden="true"><i></i></div>
  <div class="hx-group hx-pod">
    <span class="hx-cap">HAProxy pod</span>
    <span class="hx-chip">🔌 HAPTIC agent</span>
    <span class="hx-vlink" aria-hidden="true"></span>
    <span class="hx-chip">⚡ HAProxy</span>
  </div>
</div>

Key components:

- **Watcher** - Subscribes to Kubernetes API for configured resource types
- **Template Engine** - Renders Scriggo templates with resource data as context
- **Validator** - Checks admission requests and rendered output; the agent rejects configurations its HAProxy binary can't load
- **Deployer** - Decides per pod whether a change can run on the live worker or needs a reload, and sends it to that pod's agent

## Quick start

Use Kubernetes 1.33 or newer. For existing installations, follow the
[0.2 upgrade guide](upgrading-to-0.2.md) before reusing your values.

```bash
helm install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic --version 0.2.0-alpha.3 --namespace haptic --create-namespace
```

This installs both the controller and a 2-replica HAProxy Deployment, plus the default template libraries that cover Ingress and Gateway API out of the box. For the full walkthrough — including a sample app, end-to-end verification, and inspecting the rendered config the controller publishes as a `HAProxyCfg` resource — see [Getting Started](getting-started.md).

## What makes HAPTIC different

Add a custom annotation with a template snippet. This example lets each Ingress choose a request-ID header for its own backends. With Helm, put the snippet under `controller.config.templateSnippets`.

<div class="pg-embed" markdown data-scenario="extend" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="A custom annotation, implemented as one snippet" data-height="440">

<p class="pg-task" markdown>The `backend-directives-300-request-id` snippet adds the header to the annotated Ingress's backends. In the **Resources** panel, change the `shop` Ingress's `example.com/request-id-header` value to `X-Trace-ID` — or remove the annotation — and watch the `http-request set-header` line in `haproxy.cfg` follow.</p>

```yaml
controller:
  config:
    templateSnippets:
      backend-directives-300-request-id:
        template: |
          {%- if ingress != nil %}
          {%- var header = ingress.metadata.annotations["example.com/request-id-header"] %}
          {%- if header != "" %}
          {%- if !regex_search(header, "^[A-Za-z0-9-]+$") %}
          {{ fail("example.com/request-id-header must contain only letters, digits, or hyphens.") }}
          {%- end %}
          http-request set-header {{ header }} %[uuid()]
          {%- end %}
          {%- end %}
```

</div>

Set `example.com/request-id-header: "X-Request-ID"` on an Ingress to enable the header. This example accepts letters, digits, and hyphens in the header name. The backend hook scopes the rule to that Ingress; changing it requires a reload. See the [Templating Guide](templating.md) for more examples.

## Where to go next

- **Essential**: [Getting Started](getting-started.md) → [Templating](templating.md) → [CRD Reference](crd-reference.md)
- **Replacing ingress-nginx or haproxy-ingress?** [Migrating](migrating.md)
- **Custom resources beyond Ingress**: [Watching Resources](watching-resources.md)
- **Template tests for CI/CD**: [Validation Tests](validation-tests.md)
- **Reference**: [Supported Configuration](supported-configuration.md), [Troubleshooting](troubleshooting.md)
- **Helm chart configuration**: [Deploying with Helm](deploying-with-helm.md)

## Reading the docs as an AI agent

Every page is also served as raw Markdown: append `index.md` to any page URL (for example this page's Markdown is at [`index.md`](index.md)). Two site-wide maps help agents crawl the whole site:

- `llms.txt` — a link index of every page's Markdown endpoint, following the [llmstxt.org](https://llmstxt.org/) convention
- `llms-full.txt` — every page's Markdown concatenated into one document

Fetch them from the documentation version root, for example `https://haproxy-haptic.org/docs/dev/llms.txt`.

## Contributing to the docs

For a quick fix — a typo or a clearer sentence — click the pencil icon above any page title. It opens that page's Markdown in GitLab's web editor and turns your change into a merge request.

For larger changes, edit the sources under `docs/site/docs/` in the [repository](https://gitlab.com/haproxy-haptic/haptic) and preview them locally:

```bash
cd docs/site
mkdocs serve
```

This serves the site at `http://127.0.0.1:8000/` and reloads on save.
