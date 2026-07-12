# Configuration model

The controller is headless: no CLI flags carry domain configuration, no config files are mounted. Operators interact with it through the [`HAProxyTemplateConfig` CRD](../../crd-reference.md) (primary configuration), the credentials `Secret` it references ([Security — Credentials](../../operations/security.md#credentials)), and the HTTP surface — `/metrics` ([Monitoring](../../operations/monitoring.md)) plus `/healthz` and `/debug/*` on a shared listener ([Debugging](../../operations/debugging.md)).

Structured logfmt logs on stdout (via `slog.NewTextHandler`) round out the operational surface — the level is set by `LOG_LEVEL` at startup, then dynamically overridden at runtime by the CRD's `spec.logging.level` once the controller's configloader picks it up.

## What the CRD covers

`HAProxyTemplateConfig.spec` is the single source of truth for controller behaviour. It has four top-level groups:

- **Runtime settings** — `controller` (including `controller.configPublishing`), `dataplane`, `logging`, `templatingSettings`.
- **Resource watching** — `podSelector`, `watchedResources`, `watchedResourcesIgnoreFields`. (HTTP fetching is driven by the `http.Fetch()` template function — URLs that appear in templates are auto-registered; there is no top-level `spec.httpResources` field, only `validationTests[].httpResources` (a sibling of `fixtures`, not nested inside it) for mocking responses during tests.)
- **Templates** — `haproxyConfig`, `templateSnippets`, `maps`, `files`, `sslCertificates`, `k8sResources` (declarative Kubernetes resources rendered and applied via Server-Side Apply).
- **Validation** — `validationTests`, the per-resource `enableValidationWebhook` flag, and `validators` (pluggable external validator sidecars).

The full field reference (types, defaults, validation rules) lives in [CRD Reference](../../crd-reference.md), which also opens with a runnable minimal example; the installation walkthrough is [Getting Started](../../getting-started.md). This page shows how the pieces compose.

## Configuration layers

Users commonly compose configuration from three layers, in order of precedence:

1. **Template libraries** shipped in the Helm chart (base, SSL, ingress, gateway, haproxytech). These are merged into a single rendered `HAProxyTemplateConfig`.
2. **`controller.config`** in Helm values — anything set here is merged on top of library output.
3. **Direct `HAProxyTemplateConfig` edits** (via `kubectl edit htplcfg`) for ad-hoc overrides.

Because templates are just strings inside a CRD, the chart layers and the user's own values can both contribute snippets and be composed at render time. See [Templating Guide](../../templating.md) for how snippets and extension points interact.

## Reloading behaviour

Changes to the `HAProxyTemplateConfig` resource trigger an internal **reinitialization loop**: the controller cancels its current iteration, re-validates the new config, and restarts all components against it. No pod restart is required. The Secret referenced by `credentialsSecretRef` is watched the same way, so credential rotation is picked up live.
