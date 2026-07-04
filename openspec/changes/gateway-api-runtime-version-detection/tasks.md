# Tasks: Gateway API Runtime Version Detection

## 1. Config schema (inert on its own; existing configs unchanged)

- [x] 1.1 Add `apiVersions []string` and `optional bool` to `WatchedResource` in `pkg/core/config/types.go`, the CRD mirror in `pkg/apis/haproxytemplate/v1alpha1/types_config.go`, and the conversion in `pkg/controller/conversion/converter.go`; regenerate the HAProxyTemplateConfig CRD manifest
- [x] 1.2 Add `requires []string` to templateSnippets and validationTests entries (config types, CRD mirror, conversion)
- [x] 1.3 Validation in `pkg/core/config/validator.go`: apiVersion XOR apiVersions, non-empty list, `requires` names must be WatchedResources keys; unit tests for each rejection

## 2. Served-version resolution

Implementation note: instead of threading a resolver through six consumers,
resolution produces an EFFECTIVE config at the head of the iteration (and in
ConfigChangeHandler before validation): resolved entries carry the served
version in the plain APIVersion field, so every literal-APIVersion consumer
works unchanged. The gauge metric from 3.3 is covered by the
/debug/vars/effectiveConfigResolution introspection variable instead
(metrics registry is iteration-scoped; introspection is where operators
already look for watch-set state).

- [x] 2.1 Extract a shared resolver (candidate list → first served version via discovery/RESTMapper, generalizing `resolveKind` in `pkg/controller/typebootstrap_wiring.go`) that runs once per iteration before watcher setup and records the resolved version per watched resource
- [x] 2.2 Thread the resolved version through all literal-consumers: `resourcewatcher.toGVR`, cached/on-demand store GVR, typebootstrap schema fetch, `webhook/config.go` rule extraction, `dryrunvalidator/resource_mapping.go`, `testrunner/fixtures.go` defaulting
- [x] 2.3 Offline path: resolve candidates against `--schema-dir` CRD manifests (DirFetcher served versions) with identical selection logic; wire through `cmd/controller/validate.go`
- [x] 2.4 Fail-fast: a required resource with no served candidate fails the iteration with an error naming resource + candidates, surfaced via `/healthz`; remove the possibility of an unbounded `WaitForCacheSync` for this cause; unit test with a fake discovery
- [x] 2.5 `SetWatchErrorHandler` on bulk watchers (`pkg/k8s/watcher/watcher.go`): warn log with GVR + last-error timestamp, mirroring `single.go`

## 3. Availability-driven stripping

- [x] 3.1 Config-load strip pass: drop unavailable optional watches; strip snippets/tests whose `requires` name them; runs before template compilation and validationTests
- [x] 3.2 Unit tests: strip/retain matrix, no-requires untouched, strip happens for both live and `--schema-dir` resolution
- [x] 3.3 Expose resolved version + availability on the introspection endpoint and as a gauge metric (`haptic_watched_resource_available{resource,version}`)

## 4. CRD-change reinitialization

- [x] 4.1 CRD watcher (SingleWatcher-style informer on apiextensions CRDs, filtered to groups from WatchedResources) publishing a debounced trigger into the existing `ConfigChangeCh` reload path
- [x] 4.2 Ignore irrelevant CRD churn (group filter, served-versions-changed check); unit tests with fake apiextensions objects
- [x] 4.3 RBAC: chart ClusterRole gets get/list/watch on customresourcedefinitions

## 5. Render-context metadata

- [x] 5.1 `resources.<name>.APIVersion()` accessor in `pkg/controller/rendercontext` (builder + store wrapper), typed and untyped paths; available in offline validate
- [x] 5.2 Engine/context unit test pinning the accessor for both a resolved-list resource and a singular-apiVersion resource

## 6. Gateway library goes multi-version

- [x] 6.1 `charts/haptic/charts/gateway/_index.yaml`: apiVersions preference lists per the verified serving matrix (core kinds [v1, v1beta1]; grpcroutes [v1, v1alpha2]; tlsroutes [v1, v1alpha3, v1alpha2]; tcproutes [v1, v1alpha2]; referencegrants [v1, v1beta1]; listenersets [v1]; backendtlspolicies [v1, v1alpha3] — v1alpha2 excluded); `optional: true` on every gateway kind
- [x] 6.2 Add `requires` to every gateway snippet and validation test (core trio for the library-wide ones; kind-specific additions per fragment); delete the `_helm_load` Capabilities `enable` gate and the TCPRoute `inject`/`unset` block
- [x] 6.3 Status macros (70/71/72/73) take the apiVersion from `resources.<name>.APIVersion()`; remove the eight hardcoded literals
- [x] 6.4 Move GatewayClass creation from `templates/gatewayclass.yaml` into the gateway library's `k8sResources` at the resolved version
- [x] 6.5 Widen the helm webhook rule apiVersions to each resource's candidate list
- [x] 6.6 Field-generation audit: diff typed accesses against the oldest schema each candidate list can resolve to; dig()-guard newer fields (start with HTTPRoute `timeouts`, `rules[].name`)
- [x] 6.7 Convert the TCPRoute compile-safe seams into the general per-kind pattern where other kinds need them (any shared snippet touching an optional kind)

### Phase-6 status note (2026-07-04)

Done: version lists + optional (6.1), requires annotations script-generated
from the transitive snippet dependency graph (6.2), Capabilities gate +
TCPRoute inject/unset deleted, clusterrole/webhook rules cover candidate
lists (6.5), GatewayClass moved to runtime k8sResources behind the
util-emit-gatewayclass seam (6.4), NOTES.txt reworded, loader tests reworked
to the runtime model. Full v1.6 profile is byte-stable green (357 template
tests, 220 chart unittests).

REMAINING (the seam surgery, 6.3 + 6.6 + 6.7): degraded profiles COMPILE and
strip correctly but OVER-strip — shared snippets whose transitive requires
include late kinds lose whole features. Verified worklist (validate the
rendered config against tests/schemas-ga-v1.1|v1.4|v1.5 merged with the
non-gateway schemas from tests/schemas/):

1. features-110-gateway-frontend-mtls (requires listenersets): split the
   ListenerSet part out so Gateway mTLS survives on <v1.5; its gf keys
   (gatewayListenerMTLSConfig, mtlsBlockedListeners) are hard-cast by
   surviving readers (16-crtlist-per-listener:89-101, 18-bind-per-gateway) —
   make readers comma-ok tolerant AND seam the producer.
2. util-effective-listeners (requires listenersets): seam the ListenerSet
   merge behind a contrib snippet (TCPRoute count-contrib pattern) so the
   entire frontend/map/filter pipeline stops requiring listenersets.
3. util-analyze-routes / util-gateway-analysis / 60-frontend type-switch:
   seam tlsroutes (and keep grpcroutes baseline); the polymorphic route
   switch cases on *resources.tlsroutes.T need kind-contrib isolation.
4. 30-backends BackendTLSPolicy lookups: seam backendtlspolicies.
5. 70/71 status: TLSRoute status registration to its own status-patches-*
   snippet (mirror status-patches-210-gateway-tcproute); the
   allRoutesForCount TLSRoute loop through a count-contrib seam.
6. Task 6.3: status macros still hardcode gateway.networking.k8s.io/v1 —
   switch to resources.<name>.APIVersion() (matters on clusters resolving
   v1beta1/v1alpha3).
7. Re-run the transitive requires generation after each seam (the script is
   the analysis block in the session scratchpad; regenerate from the merged
   config) — requires must SHRINK as seams land.
8. test-templates.sh: drop the now-inert --api-versions flags and add
   degraded-profile runs (assemble merged schema dirs from
   tests/schemas-ga-* + non-gateway tests/schemas files).

## 7. Tests and CI

- [x] 7.1 Commit old-release CRD schema bundles (`tests/schemas-ga-v1.0/`, `-v1.1/`, `-v1.4/`, `-v1.5/`) fetched from upstream release artifacts
- [x] 7.2 Template tests for degraded profiles: run the merged config against each old bundle via `--schema-dir`, asserting the strip set and that the render passes controller validate
- [x] 7.3 OBSOLETE as specified: stripping moved from helm render time to the controller, so helm-unittest cannot observe it. Replaced by (a) loader-test assertions that kinds carry requires annotations + candidate lists, (b) the conversion-package strip unit tests, and (c) the per-bundle degraded-profile validate runs with exact expected-failure allowlists
- [ ] 7.4 e2e matrix job (DEFERRED, follow-up): per-release degraded correctness is already pinned offline by the schema-bundle runs on every CI pipeline, and the runtime transition is pinned by 7.5; a live old-release matrix adds cluster-infra cost for marginal coverage. Revisit alongside the nightly-soak work from issue #58
- [x] 7.5 Upgrade-in-place e2e: start on v1.5 standard, assert TCPRoute stripped; kubectl-apply v1.6 CRDs; assert TCPRoute routing activates with no helm operation and no pod restart
- [x] 7.6 covered by TestGatewayAPICRDUpgradeInPlace: the delete-then-reinstall cycle proves both directions of late installation (feature strip on removal, activation on install, no pod restart); a no-gateway-API-at-all cluster variant is the same code path with a larger strip set (pinned offline by the loader + strip unit tests)

## 8. Docs and changelog

- [x] 8.1 Supported-version matrix + degradation semantics in `docs/controller/docs/supported-configuration.md` and `charts/haptic/docs/libraries/gateway.md` (fix the ListenerSet v1alpha2 doc drift while there)
- [x] 8.2 Document `apiVersions`, `optional`, `requires` for custom-CRD operators (the fields are generic)
- [x] 8.3 Controller CHANGELOG (runtime detection, fail-fast, CRD reinit) and chart CHANGELOG (multi-version gateway library, gate removal) entries
