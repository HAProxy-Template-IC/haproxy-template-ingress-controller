## Why

Kubernetes ingress controllers are expected to update the `.status` field of Ingress and Gateway API resources after processing them. This feedback loop is essential: DNS controllers read Ingress status to create records, `kubectl get ingress` shows the load balancer address, and Gateway API conditions (`Accepted`, `Programmed`, `ResolvedRefs`) tell users whether their configuration was understood and applied. Without status updates, the controller is a black box from the user's perspective.

This controller's core value proposition is genericity — templates define all behavior, the controller is a dumb execution engine. Status updates must follow the same principle: templates produce status patches, the controller applies them. No hardcoded knowledge of Ingress, Gateway, or any other resource type.

## What Changes

- New `statusPatch()` template function that registers status patches during rendering, following the same side-effect pattern as `fileRegistry.Register()`
- New `condition()` and `transitionTime()` template helper functions for constructing Kubernetes condition objects with correct `lastTransitionTime` semantics
- New `toJSON()` template filter for general-purpose JSON serialization
- `StatusPatchCollector` added to the render context, thread-safe for parallel template execution via `go` goroutines
- `StatusPatch` output type added to `RenderResult` alongside `HAProxyConfig` and `AuxiliaryFiles`
- New `StatusApplier` event adapter component that applies patches via Server-Side Apply to the `/status` subresource with checksum-based skip optimization
- `statusPatch()` supports outcome-keyed variants (`rendered`, `deployed`, `renderFailed`, `deployFailed`) so templates control status content for every pipeline lifecycle phase — the controller selects variants, never inspects payloads
- Status patch snippets render early (priority 200) in the render_glob order, so patches are captured even when later config generation fails
- New `status-patches-*` extension point in the base library
- Ingress and Gateway API template libraries extended with status patch snippets using existing sharded parallel patterns
- Address discovery via namespace-scoped watch of the controller's LoadBalancer Service

## Capabilities

### New Capabilities

- `status-patch-engine`: Template functions (`statusPatch`, `condition`, `transitionTime`, `toJSON`), StatusPatchCollector, outcome-variant selection, and StatusApplier component
- `status-patch-libraries`: Library snippets for Ingress and Gateway API status updates, address discovery via controller Service watch, and the `status-patches-*` extension point

### Modified Capabilities

- `template-engine`: Render method returns status patches alongside existing outputs; render context includes StatusPatchCollector
- `template-libraries`: Base library gains `status-patches-*` extension point at priority 200; ingress and gateway libraries gain status patch snippets
- `haproxy-config-generation`: Pipeline propagates status patches through events; Coordinator handles phase-based variant selection

## Impact

- **pkg/templating**: New template functions and StatusPatchCollector type
- **pkg/controller/rendercontext**: StatusPatchCollector added to render context builder
- **pkg/controller/renderer**: RenderResult extended with StatusPatches field
- **pkg/controller/pipeline**: PipelineResult extended; status patches propagated
- **pkg/controller/events**: New event types for status update outcomes
- **pkg/controller/statusapplier**: New event adapter component (leader-only)
- **pkg/controller/reconciler**: Coordinator gains phase-based variant selection logic
- **charts/haptic/libraries/base.yaml**: New `status-patches-*` extension point
- **charts/haptic/libraries/ingress.yaml**: Status patch snippet + controller_services watchedResource
- **charts/haptic/libraries/gateway.yaml**: Status patch snippets for Gateway, HTTPRoute, GRPCRoute
- **charts/haptic/values.yaml**: No new user-facing configuration required (address auto-discovered)
