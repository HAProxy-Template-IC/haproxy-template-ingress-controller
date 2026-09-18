---
description: "HAPTIC (HAProxy Template Ingress Controller) is a template-driven HAProxy ingress controller for Kubernetes. Watch any resource, render templates, and apply them to your HAProxy fleet."
hide:
  - navigation
---

# HAPTIC

**HAPTIC** (HAProxy Template Ingress Controller) routes Kubernetes traffic through
[HAProxy](https://www.haproxy.org/). Use the bundled Ingress and Gateway API
template libraries, or write [Scriggo templates](templating.md) for your own annotations,
routing rules, and resource types.

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

The controller watches the resources you select, renders their fields into
configuration, and sends updates to an agent in each HAProxy pod. Routing behavior
lives in templates, so you can change it without modifying the controller's Go code.

Choose a starting point:

| Your task | Start here |
| --- | --- |
| Install HAPTIC and route a sample app | [Getting started](getting-started.md) |
| Replace an existing ingress controller | [Migration guide](migrating.md) |
| Upgrade an existing HAPTIC installation | [Upgrade to 0.2](upgrading-to-0.2.md) |
| Add routing behavior or custom annotations | [Templating guide](templating.md) |
| Try a template before installing | [Browser playground](https://haproxy-haptic.org/playground/) |

## Key features

### Routing and application policies

The bundled libraries provide these capabilities without custom templates:

| Capability | What you configure |
| --- | --- |
| [Ingress](libraries/ingress.md) and [Gateway API](libraries/gateway.md) | HTTP and gRPC routing, TLS, and backend health checks. Gateway API also provides TCPRoute, TLSRoute, ListenerSet, weighted routing, and mirroring. |
| [Native annotations](libraries/haptic-annotations.md) | API-key, JSON Web Token (JWT), and HMAC authentication, consumer groups, redirects, CORS, compression, and bandwidth limits. |
| [Shared rate limits](libraries/haptic-annotations.md#rate-and-bandwidth-limiting) and [response caching](libraries/haptic-annotations.md#shared-response-cache) | Fleet-wide request budgets with Valkey and a shared Varnish cache, both opt-in. |
| [Request validation](libraries/haptic-annotations.md#api-gateway) and [WAF policies](libraries/haptic-annotations.md#reusable-waf-policies) | JSON Schema request validation and reusable Coraza Web Application Firewall (WAF) policies, including namespace-scoped policies. |
| [Governance](operations/governance.md) | Administrator-defined defaults and constraints on any watched resource, with audit or admission rejection. |
| [TLS configuration](ssl-certificates.md) | Certificate rotation, dual RSA/ECDSA certificates, shared session-ticket keys, and cipher/protocol policy. |
| [Migration libraries](migrating.md) | Opt-in compatibility with ingress-nginx, haproxy-ingress, and HAProxy Technologies annotations. |

### Templates and resource APIs

[Scriggo templates](templating.md) read resource fields such as
`service.metadata.name`, filter and group resources, and fetch external data.
Use them to generate HAProxy configuration, certificates, and auxiliary files. The controller discovers [resource versions and schemas](watching-resources.md)
at runtime and adapts when watched CRDs change.

Share snippets through [`HAProxyTemplateLibrary`](crd-reference.md#haproxytemplatelibrary)
resources. Use [`k8sResources`](crd-reference.md#k8sresources) to manage related
Kubernetes objects. Mark a field as create-only to set its initial value while
preserving later changes by an operator or autoscaling controller.

Try templates and their validation fixtures in the browser [playground](https://haproxy-haptic.org/playground/)
or edit the live examples throughout these docs.

### Deployment and operations

- **Runtime updates:** Scale pods, rotate certificates, and change routing-map entries without reloading HAProxy. HAProxy 3.4 can also add or remove backends that reuse existing settings; new listeners and rules require a reload. See [how HAPTIC chooses runtime updates](libraries/reload-free.md).
- **Incremental rendering:** Unchanged template results are reused, and [follower replicas](operations/high-availability.md) keep their render state warm. See [performance and sizing](operations/performance.md) for resource requirements.
- **Validation:** [`haptic preflight`](operations/validate-before-deploy.md) and Helm hooks check a candidate before rollout. Configuration loading enforces embedded tests; admission validates proposed routing changes, including [pluggable output validators](operations/pluggable-validators.md).
- **Deployment inspection:** [`haptic diff`](development/design/deployment.md) predicts reloads, and [`haptic agent state`](development/agent.md) reports a pod's applied configuration and recovery state.
- **Observability:** [JSON access logs and per-route metrics](operations/monitoring.md) identify the route, backend, and policy outcomes. Optional [distributed tracing](reference.md#logging-and-templating) exports request and upstream spans through [Vector](https://vector.dev/).

!!! warning "Project maturity"
    HAPTIC uses pre-1.0 versioning, and its custom resources use API version `v1alpha1`. Minor releases can change APIs and configuration. Pin an exact chart version (`--version 0.2.0-alpha.3`) and read the [changelog](changelog.md) before you upgrade.

## Architecture

The controller renders configuration; the agent in each HAProxy pod applies it.
Admission and configuration loading run `haproxy -c` before accepting a change.
During reconciliation, auxiliary-file checks run before dispatch, while HAProxy
checks run alongside deployment. Each agent rejects configuration that its own
HAProxy binary can't load. See [validation behavior](operations/debugging.md#haproxy-refused-the-config-the-fleet-was-given-configvalidatedfalse)
and the [architecture overview](development/design/architecture-overview.md).

## Quick start

Use Kubernetes 1.33 or newer. For existing installations, follow the
[0.2 upgrade guide](upgrading-to-0.2.md) before reusing your values.

```bash
helm install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic --version 0.2.0-alpha.3 --namespace haptic --create-namespace
```

This installs the controller, two HAProxy replicas, and the default routing
libraries. Follow [Getting started](getting-started.md) to deploy a sample app,
inspect the generated configuration, and test a route.

## What makes HAPTIC different

Add a custom annotation with a template snippet. This example lets each Ingress choose a request-ID header for its own backends. With Helm, put the snippet under `controller.config.templateSnippets`.

<div class="pg-embed" markdown data-scenario="extend" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="A custom annotation, implemented as one snippet" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, change the `shop` Ingress's `example.com/request-id-header` value to `X-Trace-ID`. Check the `http-request set-header` line in `haproxy.cfg`, then remove the annotation to remove the line.</p>

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

- [Watch additional resources](watching-resources.md), including your own custom types.
- [Test your templates](validation-tests.md) with resource fixtures and output assertions.
- [Configure the Helm chart](deploying-with-helm.md) for your cluster.
- [Check supported HAProxy changes](supported-configuration.md) and reload requirements.
- [Diagnose a problem](troubleshooting.md) with routing, configuration, or deployment.

## Reading the docs as an AI agent

Install the [HAPTIC agent skill](agent-skill.md) for Scriggo customization,
resource access, and validation workflows, with runnable examples.

Every page is also served as raw Markdown: append `index.md` to any page URL (for example this page's Markdown is at [`index.md`](index.md)). Two site-wide maps help agents crawl the whole site:

- `llms.txt` — a link index of every page's Markdown endpoint, following the [llmstxt.org](https://llmstxt.org/) convention
- `llms-full.txt` — every page's Markdown concatenated into one document

Fetch them from the documentation version root, for example `https://haproxy-haptic.org/docs/dev/llms.txt`.

## Contributing to the docs

To edit a page, select the pencil icon above its title. GitLab opens the Markdown
source, where you can propose a change through a merge request.

For larger changes, edit the sources under `docs/site/docs/` in the [repository](https://gitlab.com/haproxy-haptic/haptic) and preview them locally:

```bash
cd docs/site
mkdocs serve
```

This serves the site at `http://127.0.0.1:8000/` and reloads on save.
