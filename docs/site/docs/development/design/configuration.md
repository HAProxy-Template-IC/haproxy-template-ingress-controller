# Configuration model

The controller is headless: no CLI flags carry domain configuration, no config files are mounted. Operators interact with it through the [`HAProxyTemplateConfig` CRD](../../crd-reference.md) (primary configuration), the credentials `Secret` it references ([Security — Credentials](../../operations/security.md#credentials)), and the HTTP surface — `/metrics` ([Monitoring](../../operations/monitoring.md)) plus `/healthz` and `/debug/*` on a shared listener ([Debugging](../../operations/debugging.md)).

Structured logfmt logs on stdout (via `slog.NewTextHandler`) round out the operational surface — the level is set by `LOG_LEVEL` at startup, then dynamically overridden at runtime by the CRD's `spec.logging.level` once the controller's configloader picks it up.

## What the CRD covers

`HAProxyTemplateConfig.spec` is the source of truth for controller behaviour. The controller reads an ordered list of these resources and merges them, later wins, so the **merged** spec is what everything downstream sees. It has four top-level groups:

- **Runtime settings** — `controller` (including `controller.configPublishing`), `dataplane`, `logging`, `templatingSettings`.
- **Resource watching** — `podSelector`, `watchedResources`, `watchedResourcesIgnoreFields`. (HTTP fetching is driven by the `http.Fetch()` template function — URLs that appear in templates are auto-registered; there is no top-level `spec.httpResources` field, only `validationTests[].httpResources` (a sibling of `fixtures`, not nested inside it) for mocking responses during tests.)
- **Templates** — `haproxyConfig`, `templateSnippets`, `maps`, `files`, `sslCertificates`, `k8sResources` (declarative Kubernetes resources rendered and applied via Server-Side Apply).
- **Validation** — `validationTests`, the per-resource `enableValidationWebhook` flag, and `validators` (pluggable external validator sidecars).

The full field reference (types, defaults, validation rules) lives in [CRD Reference](../../crd-reference.md), which also opens with a runnable minimal example; the installation walkthrough is [Getting Started](../../getting-started.md). This page shows how the pieces compose.

## Configuration layers

Users commonly compose configuration from three layers, in order of precedence:

1. **Template libraries** shipped in the Helm chart (base, SSL, ingress, gateway, haproxytech, …). The chart renders each enabled one as its own `HAProxyTemplateConfig`, named `<configName>-<library>`.
2. **`controller.config`** in Helm values — rendered as `<configName>` and merged last, so anything set here wins over every library.
3. **Direct `HAProxyTemplateConfig` edits** (via `kubectl edit htplcfg <configName>`) for ad-hoc overrides. Edit only the operator object; the library objects are chart output that `helm upgrade` overwrites.

The controller performs the merge at startup, in the order given by the `CRD_NAME`
environment variable on its Deployment. It uses the same merge primitive Helm's
`mustMergeOverwrite` does, so the result is what a chart-side merge would have
produced — that equivalence is why the merge could move without changing any
rendered output. `migrationCoverage` is the one field that accumulates instead of
being overwritten. See [ADR-0014](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/docs/adr/0014-per-library-config-objects.md).

One object per library exists because the single merged object had reached 99.4%
of the ~1.5 MiB limit Kubernetes enforces per object. `make cr-size-check` gates
each object on every chart-test run.

Because templates are just strings inside a CRD, the chart layers and the user's own values can both contribute snippets and be composed at render time. See [Templating Guide](../../templating.md) for how snippets and extension points interact.

## Reloading behaviour

Changes to any of the `HAProxyTemplateConfig` resources trigger an internal **reinitialization loop**: the controller re-merges the set, cancels its current iteration, re-validates the new config, and restarts all components against it. Each resource has its own watcher, so a `helm upgrade` that rewrites several of them produces a burst of changes; the reinitialization debounce collapses them into one restart. No pod restart is required. The Secret referenced by `credentialsSecretRef` is watched the same way, so credential rotation is picked up live.
