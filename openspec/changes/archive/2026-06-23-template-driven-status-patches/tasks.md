## 1. StatusPatchCollector and Types

- [x] 1.1 Define `StatusPatch` type in `pkg/templating` with fields: Namespace, Name, APIVersion, Kind, Variants (map[string]map[string]interface{} keyed by phase)
- [x] 1.2 Implement `StatusPatchCollector` in `pkg/templating` with mutex-protected map, `Register()` method (merges by resource key), and `Patches()` method returning `[]StatusPatch`
- [x] 1.3 Write unit tests for StatusPatchCollector: basic registration, merge behavior, concurrent writes from multiple goroutines, Patches() returns all entries

## 2. Template Functions

- [x] 2.1 Implement `statusPatch()` template function in `pkg/templating/filters_scriggo.go` that writes to StatusPatchCollector from render context
- [x] 2.2 Implement `condition()` template helper function returning map[string]interface{} with metav1.Condition structure
- [x] 2.3 Implement `transitionTime()` template helper function that searches resource `.status.conditions` (and `.status.parents[].conditions` with optional parentIndex) for matching type, returns existing lastTransitionTime if status unchanged or current time otherwise
- [x] 2.4 Implement `toJSON()` filter function using encoding/json.Marshal, registered as both filter and standalone function
- [x] 2.5 Register all new functions in the Scriggo engine declarations
- [x] 2.6 Write unit tests for each template function: condition() field mapping, transitionTime() with unchanged/changed/missing conditions, transitionTime() with parent conditions, toJSON() with maps/strings/nil/slices

## 3. Render Context Integration

- [x] 3.1 Add `StatusPatchCollector` to render context in `pkg/controller/rendercontext/builder.go` Build() method (new `statusPatchCollector` key, same lifecycle as FileRegistry)
- [x] 3.2 Add `WithStatusPatchCollector` option or create collector automatically in Build() and return it alongside FileRegistry
- [x] 3.3 Update all Build() callers (renderer, testrunner, benchmark, dryrunvalidator) to handle the new StatusPatchCollector return value

## 4. Pipeline and Event Propagation

- [x] 4.1 Add `StatusPatches []StatusPatch` field to `RenderResult` in `pkg/controller/renderer/service.go`
- [x] 4.2 Add `StatusPatches []StatusPatch` field to `PipelineResult` in `pkg/controller/pipeline/pipeline.go`
- [x] 4.3 Update `RenderService.Render()` to extract patches from StatusPatchCollector after template rendering and include in RenderResult
- [x] 4.4 Update Pipeline.Execute() to propagate StatusPatches from RenderResult to PipelineResult
- [x] 4.5 Add `StatusPatches` field to `TemplateRenderedEvent` in `pkg/controller/events/`
- [x] 4.6 Update Coordinator to include StatusPatches in TemplateRenderedEvent and cache latest patches for render failure scenarios

## 5. Status Update Events

- [x] 5.1 Define `StatusUpdateCompletedEvent` in `pkg/controller/events/` with AppliedCount, SkippedCount, DurationMs fields
- [x] 5.2 Define `StatusUpdateFailedEvent` in `pkg/controller/events/` with Namespace, Name, GVR, Error, Retriable fields
- [x] 5.3 Define `StatusPatchPhase` string type with constants: `StatusPatchPhaseRendered`, `StatusPatchPhaseDeployed`, `StatusPatchPhaseRenderFailed`, `StatusPatchPhaseDeployFailed`
- [x] 5.4 Update Commentator to log new status update events

## 6. StatusApplier Component

- [x] 6.1 Create `pkg/controller/statusapplier/` package with component struct, constructor (subscribes in constructor per pattern), and Run() event loop
- [x] 6.2 Implement event handling: cache TemplateRenderedEvent patches, handle ReconciliationCompletedEvent (apply `deployed`), handle ReconciliationFailedEvent (apply `renderFailed` or `deployFailed` based on phase)
- [x] 6.3 Implement `rendered` variant application after successful render (triggered by TemplateRenderedEvent before deployment)
- [x] 6.4 Implement SSA patch application using `k8s.io/client-go/dynamic` with apiVersion+kind → GVR resolution via REST mapper, fieldManager "haptic", subResource "status"
- [x] 6.5 Implement checksum-based skip optimization: map[(ns,name,gvr)]sha256, compare before SSA call, update on success, clean stale entries
- [x] 6.6 Implement leadership handling: subscribe to BecameLeaderEvent (clear checksum cache, replay cached patches), LostLeadershipEvent (clear pending state)
- [x] 6.7 Publish StatusUpdateCompletedEvent and StatusUpdateFailedEvent appropriately
- [x] 6.8 Write unit tests for StatusApplier: variant selection per phase, checksum skip, leadership transitions, SSA call construction

## 7. Controller Startup Integration

- [x] 7.1 Instantiate StatusApplier in controller.go staged startup (Stage 5, all-replica component)
- [x] 7.2 Wire dynamic client and REST mapper into StatusApplier constructor
- [x] 7.3 Verify StatusApplier subscribes in constructor and starts in Start() (per event bus pattern)

## 8. Base Library Extension Point

- [x] 8.1 Add `render_glob "status-patches-*"` to base.yaml haproxyConfig template at priority 200 position (after features, before backends), wrapped to suppress output
- [x] 8.2 Verify existing validation tests still pass with the new extension point
- [x] 8.3 Run `./scripts/test-templates.sh` to confirm no regressions

## 9. Address Discovery Library

- [x] 9.1 Add `controller_services` watchedResource entry to base.yaml (or a new library file): namespace-scoped v1/services with label selector `app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller`
- [x] 9.2 Add `features-075-address-discovery` snippet that reads controller service LoadBalancer status and stores addresses in `gf["addresses"]` (priority 075: after ssl init at 050, before TLS at 100)
- [x] 9.3 Write validation test for address discovery with a mock controller service fixture

## 10. Ingress Status Patch Library

- [x] 10.1 Add `status-patches-200-ingress` snippet to ingress.yaml using sharded parallel pattern (ShardedIngressStatusPatches macro with shard_slice + go)
- [x] 10.2 Implement GenerateIngressStatusPatches macro: for each ingress, call statusPatch() with `deployed` variant (loadBalancer addresses) and `deployFailed` variant (empty addresses), skip if gf["addresses"] is nil
- [x] 10.3 Write validation test with ingress fixtures and mock controller service verifying status patches are registered

## 11. Gateway Status Patch Library

- [x] 11.1 Add `status-patches-200-gateway` snippet to gateway.yaml using sharded parallel pattern
- [x] 11.2 Implement Gateway status patches: Accepted, ResolvedRefs, Programmed conditions with observedGeneration and transitionTime(), plus addresses in Gateway address format
- [x] 11.3 Implement HTTPRoute status patches: iterate parentRefs, build parents[] entries with controllerName and conditions, check backend service existence for ResolvedRefs
- [x] 11.4 Implement GRPCRoute status patches following HTTPRoute pattern
- [x] 11.5 Write validation tests for Gateway, HTTPRoute, and GRPCRoute status patches with appropriate fixtures

## 12. Integration Testing and Verification

- [x] 12.1 Run full `make lint` and fix all linting issues
- [x] 12.2 Run `make test` and fix all test failures
- [x] 12.3 Run `./scripts/test-templates.sh` and verify all template tests pass
- [ ] 12.4 Test end-to-end in dev environment: deploy, verify Ingress gets LoadBalancer address in status, verify Gateway/HTTPRoute get conditions
