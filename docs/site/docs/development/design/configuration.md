# Configuration model

The controller is headless: no CLI flags carry domain configuration, no config files are mounted. Operators interact with it through the [`HAProxyTemplateConfig` CRD](../../crd-reference.md) (primary configuration), the credentials `Secret` it references ([Security — Credentials](../../operations/security.md#credentials)), and the HTTP surface — `/metrics` ([Monitoring](../../operations/monitoring.md)) plus `/healthz` and `/debug/*` on a shared listener ([Debugging](../../operations/debugging.md)).

Structured logfmt logs on stdout (via `slog.NewTextHandler`) round out the operational surface — the level is set by `LOG_LEVEL` at startup, then dynamically overridden at runtime by the CRD's `spec.logging.level` once the controller's configloader picks it up.

## What the CRD covers

`HAProxyTemplateConfig.spec` is the source of truth for controller behaviour. There is one such object; it pulls in `HAProxyTemplateLibrary` objects through an ordered `spec.libraryRefs` and the controller merges them, later wins, with the config itself last — so the **merged** spec is what everything downstream sees. It has four top-level groups:

- **Runtime settings** — `controller` (including `controller.configPublishing`), `dataplane`, `logging`, `templatingSettings`.
- **Resource watching** — `podSelector`, `watchedResources`, `watchedResourcesIgnoreFields`. (HTTP fetching is driven by the `http.Fetch()` template function — URLs that appear in templates are auto-registered; there is no top-level `spec.httpResources` field, only `validationTests[].httpResources` (a sibling of `fixtures`, not nested inside it) for mocking responses during tests.)
- **Templates** — `haproxyConfig`, `templateSnippets`, `maps`, `files`, `sslCertificates`, `k8sResources` (declarative Kubernetes resources rendered and applied via Server-Side Apply).
- **Validation** — `validationTests`, the per-resource `enableValidationWebhook` flag, and `validators` (pluggable external validator sidecars).

The full field reference (types, defaults, validation rules) lives in [CRD Reference](../../crd-reference.md), which also opens with a runnable minimal example; the installation walkthrough is [Getting Started](../../getting-started.md). This page shows how the pieces compose.

## Configuration layers

Users commonly compose configuration from three layers, in order of precedence:

1. **Template libraries** shipped in the Helm chart (base, SSL, ingress, gateway, haproxytech, …). The chart renders each enabled one as its own `HAProxyTemplateLibrary`, named `<configName>-<library>`. A library carries content only — `templateSnippets`, `validationTests`, `maps`, `files`, `sslCertificates`, `k8sResources`, `templatingSettings`, `haproxyConfig` — never `podSelector`, `watchedResources` or `dataplane`.
2. **`controller.config`** in Helm values — rendered as the single `HAProxyTemplateConfig` named `<configName>` and merged last, so anything set here wins over every library.
3. **Direct `HAProxyTemplateConfig` edits** (via `kubectl edit htplcfg <configName>`) for ad-hoc overrides. That object stays small — about 1% of etcd's per-object limit — because the bulk lives in the libraries. Editing a library in place works too and takes effect immediately; `helm upgrade` overwrites it.

Merge order is declared once, in `spec.libraryRefs`, and nowhere else. Each entry
also names a `revision` that the referenced object must report: the controller
compares the two strings and never derives either from the content, so a
half-applied set shows up as a mismatch and the controller keeps serving the
last-good configuration rather than rendering a set with a library missing. An
in-place edit leaves the revision untouched, which is why it takes effect
immediately.

The merge uses the same primitive Helm's `mustMergeOverwrite` does, so the result
is what a chart-side merge would have produced. `migrationCoverage` accumulates
instead of being overwritten, and `validationTests` from every source are combined — a test name
defined by two sources is an error naming both. See
[ADR-0017](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/docs/adr/0017-template-library-kind.md).

One object per library exists because the single merged object had reached 99.4%
of the ~1.5 MiB limit Kubernetes enforces per object. `make cr-size-check` gates
each object on every chart-test run.

Because templates are just strings inside a CRD, the chart layers and the user's own values can both contribute snippets and be composed at render time. See [Templating Guide](../../templating.md) for how snippets and extension points interact.

## Reloading behaviour

Changes to any of the `HAProxyTemplateConfig` resources trigger an internal **reinitialization loop**: the controller re-merges the set, cancels its current iteration, re-validates the new config, and restarts all components against it. Each resource has its own watcher, so a `helm upgrade` that rewrites several of them produces a burst of changes; the reinitialization debounce collapses them into one restart. No pod restart is required. The Secret referenced by `credentialsSecretRef` is watched the same way, so credential rotation is picked up live.
