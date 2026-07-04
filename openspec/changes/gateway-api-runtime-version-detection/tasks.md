# Tasks: Gateway API Runtime Version Detection

## 1. Config schema (inert on its own; existing configs unchanged)

- [ ] 1.1 Add `apiVersions []string` and `optional bool` to `WatchedResource` in `pkg/core/config/types.go`, the CRD mirror in `pkg/apis/haproxytemplate/v1alpha1/types_config.go`, and the conversion in `pkg/controller/conversion/converter.go`; regenerate the HAProxyTemplateConfig CRD manifest
- [ ] 1.2 Add `requires []string` to templateSnippets and validationTests entries (config types, CRD mirror, conversion)
- [ ] 1.3 Validation in `pkg/core/config/validator.go`: apiVersion XOR apiVersions, non-empty list, `requires` names must be WatchedResources keys; unit tests for each rejection

## 2. Served-version resolution

- [ ] 2.1 Extract a shared resolver (candidate list → first served version via discovery/RESTMapper, generalizing `resolveKind` in `pkg/controller/typebootstrap_wiring.go`) that runs once per iteration before watcher setup and records the resolved version per watched resource
- [ ] 2.2 Thread the resolved version through all literal-consumers: `resourcewatcher.toGVR`, cached/on-demand store GVR, typebootstrap schema fetch, `webhook/config.go` rule extraction, `dryrunvalidator/resource_mapping.go`, `testrunner/fixtures.go` defaulting
- [ ] 2.3 Offline path: resolve candidates against `--schema-dir` CRD manifests (DirFetcher served versions) with identical selection logic; wire through `cmd/controller/validate.go`
- [ ] 2.4 Fail-fast: a required resource with no served candidate fails the iteration with an error naming resource + candidates, surfaced via `/healthz`; remove the possibility of an unbounded `WaitForCacheSync` for this cause; unit test with a fake discovery
- [ ] 2.5 `SetWatchErrorHandler` on bulk watchers (`pkg/k8s/watcher/watcher.go`): warn log with GVR + last-error timestamp, mirroring `single.go`

## 3. Availability-driven stripping

- [ ] 3.1 Config-load strip pass: drop unavailable optional watches; strip snippets/tests whose `requires` name them; runs before template compilation and validationTests
- [ ] 3.2 Unit tests: strip/retain matrix, no-requires untouched, strip happens for both live and `--schema-dir` resolution
- [ ] 3.3 Expose resolved version + availability on the introspection endpoint and as a gauge metric (`haptic_watched_resource_available{resource,version}`)

## 4. CRD-change reinitialization

- [ ] 4.1 CRD watcher (SingleWatcher-style informer on apiextensions CRDs, filtered to groups from WatchedResources) publishing a debounced trigger into the existing `ConfigChangeCh` reload path
- [ ] 4.2 Ignore irrelevant CRD churn (group filter, served-versions-changed check); unit tests with fake apiextensions objects
- [ ] 4.3 RBAC: chart ClusterRole gets get/list/watch on customresourcedefinitions

## 5. Render-context metadata

- [ ] 5.1 `resources.<name>.APIVersion()` accessor in `pkg/controller/rendercontext` (builder + store wrapper), typed and untyped paths; available in offline validate
- [ ] 5.2 Engine/context unit test pinning the accessor for both a resolved-list resource and a singular-apiVersion resource

## 6. Gateway library goes multi-version

- [ ] 6.1 `charts/haptic/charts/gateway/_index.yaml`: apiVersions preference lists per the verified serving matrix (core kinds [v1, v1beta1]; grpcroutes [v1, v1alpha2]; tlsroutes [v1, v1alpha3, v1alpha2]; tcproutes [v1, v1alpha2]; referencegrants [v1, v1beta1]; listenersets [v1]; backendtlspolicies [v1, v1alpha3] — v1alpha2 excluded); `optional: true` on every gateway kind
- [ ] 6.2 Add `requires` to every gateway snippet and validation test (core trio for the library-wide ones; kind-specific additions per fragment); delete the `_helm_load` Capabilities `enable` gate and the TCPRoute `inject`/`unset` block
- [ ] 6.3 Status macros (70/71/72/73) take the apiVersion from `resources.<name>.APIVersion()`; remove the eight hardcoded literals
- [ ] 6.4 Move GatewayClass creation from `templates/gatewayclass.yaml` into the gateway library's `k8sResources` at the resolved version
- [ ] 6.5 Widen the helm webhook rule apiVersions to each resource's candidate list
- [ ] 6.6 Field-generation audit: diff typed accesses against the oldest schema each candidate list can resolve to; dig()-guard newer fields (start with HTTPRoute `timeouts`, `rules[].name`)
- [ ] 6.7 Convert the TCPRoute compile-safe seams into the general per-kind pattern where other kinds need them (any shared snippet touching an optional kind)

## 7. Tests and CI

- [ ] 7.1 Commit old-release CRD schema bundles (`tests/schemas-ga-v1.0/`, `-v1.1/`, `-v1.4/`, `-v1.5/`) fetched from upstream release artifacts
- [ ] 7.2 Template tests for degraded profiles: run the merged config against each old bundle via `--schema-dir`, asserting the strip set and that the render passes controller validate
- [ ] 7.3 helm-unittest: per-kind strip-invariant cases modeled on the TCPRoute one (full inventory enumeration)
- [ ] 7.4 e2e matrix job: install a representative old Gateway API release (v1.1 and v1.5), deploy haptic, smoke-test active features and assert Ready + stripped set
- [ ] 7.5 Upgrade-in-place e2e: start on v1.5 standard, assert TCPRoute stripped; kubectl-apply v1.6 CRDs; assert TCPRoute routing activates with no helm operation and no pod restart
- [ ] 7.6 e2e for late installation: deploy haptic on a cluster with no Gateway API, assert Ready ingress-only; install CRDs; assert gateway features activate

## 8. Docs and changelog

- [ ] 8.1 Supported-version matrix + degradation semantics in `docs/controller/docs/supported-configuration.md` and `charts/haptic/docs/libraries/gateway.md` (fix the ListenerSet v1alpha2 doc drift while there)
- [ ] 8.2 Document `apiVersions`, `optional`, `requires` for custom-CRD operators (the fields are generic)
- [ ] 8.3 Controller CHANGELOG (runtime detection, fail-fast, CRD reinit) and chart CHANGELOG (multi-version gateway library, gate removal) entries
