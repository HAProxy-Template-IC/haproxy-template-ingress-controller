# Proposal: Gateway API Runtime Version Detection

## Why

HAPTIC hard-pins every watched resource's apiVersion in the chart and resolves feature availability once, at helm render time, via `.Capabilities`. Two consequences are unacceptable for a controller that cannot make assumptions about the operator's cluster: any Gateway API install older than the pinned versions bricks the controller (a watch on an unserved GVR never syncs, so the controller never becomes Ready — or template compilation fails outright), and an in-place Gateway API CRD upgrade or installation silently breaks or stays invisible until someone re-runs helm. The controller must support **all** Gateway API releases and adapt to CRD changes **at runtime**, without violating RULE #1 (resource-agnostic Go).

## What Changes

- `watchedResources` entries accept an ordered `apiVersions` preference list (singular `apiVersion` remains as sugar) and an `optional: true` flag. At iteration start the controller resolves each entry to the first candidate the apiserver actually serves, using the discovery/RESTMapper already in-process.
- The resolved version — not the config literal — feeds every consumer: informer GVR, on-demand store GVR, typegen schema fetch, webhook GVK registration, dry-run overlay mapping, and validation-test fixture defaulting.
- `templateSnippets` and `validationTests` entries accept a `requires: [<resource-name>, ...]` list. When an optional resource has no served candidate, the controller drops the watch and strips every element that requires it from the effective config at load time — the runtime, discovery-driven replacement for the chart's `_helm_load` `enable`/`unset` Capabilities gating.
- A required (non-optional) resource with no served candidate fails the iteration fast with a named error surfaced in `/healthz`, replacing today's silent infinite cache-sync hang. Bulk watchers get a watch-error handler for observability.
- The render context exposes the resolved apiVersion per watched resource (`resources.<name>.APIVersion()`), so status macros stop hardcoding version literals.
- A CRD watch (apiextensions.k8s.io, filtered to groups appearing in `watchedResources`) funnels served-version changes into the existing config-reload iteration restart. Late installation, in-place upgrade, and serving-removal all converge without helm or pod restarts.
- The gateway chart library declares per-kind version preference lists covering the full Gateway API release history (per the verified serving matrix), marks all gateway resources optional, annotates snippets/tests with `requires`, reads the resolved version in status macros, and moves the GatewayClass object from a helm template into runtime-rendered `k8sResources`. The helm `Capabilities` load gate and the TCPRoute `inject`/`unset` block are deleted.
- Typed field accesses newer than a kind's oldest resolvable schema generation are `dig()`-guarded (the discipline already used for experimental-channel fields).
- Testing: committed old-release CRD schema bundles make old-cluster scenarios unit-testable offline via `--schema-dir`; CI gains an e2e matrix over representative Gateway API releases and an upgrade-in-place e2e proving CRD upgrades apply without helm redeployment.

No breaking changes for operators: existing configs (singular `apiVersion`, no `optional`, no `requires`) keep exactly today's semantics.

## Capabilities

### New Capabilities

- `runtime-version-detection`: resolving watched resources to served API versions via live discovery, optional-resource feature stripping, fail-fast on required-unserved, and CRD-watch-triggered reinitialization.

### Modified Capabilities

- `configuration-management`: `watchedResources` schema gains `apiVersions` (ordered list) and `optional`; `templateSnippets`/`validationTests` gain `requires`; config load applies availability-driven stripping.
- `kubernetes-resource-watching`: bulk watchers SHALL register a watch-error handler (observability surface for mid-run serving removal); watcher setup consumes a resolved GVR rather than a config literal.
- `template-engine`: the render context exposes per-watched-resource metadata (resolved apiVersion) alongside the existing store surface.
- `template-libraries`: the gateway library becomes multi-version (preference lists, `requires` annotations, resolved-version status patches, runtime-rendered GatewayClass, no helm Capabilities gating).

## Impact

- **Go (all generic, no resource names)**: `pkg/controller/resourcewatcher` (resolution before GVR construction), `pkg/controller/typebootstrap_wiring.go` (resolution becomes authoritative, shared), `pkg/core/config` + `pkg/apis/haproxytemplate` + `pkg/controller/conversion` (schema fields), config-load stripping pass, `pkg/controller/webhook` (resolved GVK), `pkg/controller/dryrunvalidator`, `pkg/controller/testrunner` (fixture defaulting), `pkg/controller/rendercontext` (metadata accessor), new CRD SingleWatcher wired to the existing `ConfigChangeCh` reload path, `pkg/k8s/watcher` (watch-error handler).
- **Chart**: `charts/haptic/charts/gateway/*` (preference lists, `requires`, status macros, GatewayClass as `k8sResources`), `templates/_libraries.tpl` (gate/inject/unset removal), `templates/gatewayclass.yaml` (deleted), webhook rule apiVersions widened to the candidate lists.
- **CRD**: `haproxy-haptic.org_haproxytemplateconfigs.yaml` schema additions (`apiVersions`, `optional`, `requires`).
- **Tests/CI**: `tests/schemas-ga-*` bundles, template tests for degraded profiles, e2e matrix + upgrade-in-place job.
- **Docs**: supported-version statement in `docs/controller/docs/supported-configuration.md` and the gateway library docs.
