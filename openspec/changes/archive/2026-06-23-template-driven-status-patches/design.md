## Context

The controller processes Kubernetes resources (Ingress, Gateway API, custom CRDs) through templates to generate HAProxy configuration. However, it does not report processing results back to these resources via `.status` updates. This is a gap: DNS controllers, monitoring tools, and `kubectl` all depend on status to understand whether resources are accepted and programmed.

The controller's core principle is genericity — templates define all behavior. Status updates must follow this principle: templates produce status patches, the controller applies them without understanding the payload content.

Currently, the render pipeline produces:

- `HAProxyConfig` (string) — the main config
- `AuxiliaryFiles` — maps, certs, general files, crt-lists

These are generated during rendering and consumed after deployment. Status patches will be a new output type following the same pattern.

Relevant existing patterns:

- `FileRegistry.Register()` — side-effect template function that collects auxiliary files during rendering
- `SharedContext.ComputeIfAbsent()` — thread-safe caching for parallel renders
- `shard_slice()` + `go` keyword — parallel processing of large resource collections
- `first_seen()` — thread-safe deduplication across parallel goroutines
- `status-patches-*` extension point at priority 200 — renders after feature analysis but before complex config generation

## Goals / Non-Goals

**Goals:**

- Templates fully control status patch content for any resource type, including all conditions, addresses, and custom fields
- Templates define status content for each pipeline lifecycle phase (rendered, deployed, renderFailed, deployFailed) via outcome-keyed variants — the controller selects variants without inspecting payloads
- Status patch snippets render early (priority 200) so patches are captured even when later config generation fails
- Status patch collection is thread-safe and compatible with existing sharded parallel rendering via `go` goroutines
- Status patches are applied via Server-Side Apply (SSA) to the `/status` subresource with field ownership
- Unchanged patches are skipped via checksum comparison to minimize API calls
- Template libraries ship ready-made status patch snippets for Ingress and Gateway API resources
- Address discovery uses a namespace-scoped watch of the controller's LoadBalancer Service

**Non-Goals:**

- Controller-side understanding of any specific resource type's status schema
- Real-time status updates during rendering (status reflects the completed pipeline phase)
- Status updates for resources not managed by templates (e.g., controller's own CRDs — handled by existing StatusUpdater)
- Webhook-triggered status updates (only reconciliation-triggered)

## Decisions

### Decision 1: Status patches as template side-effects via `statusPatch()` function

**Choice**: A `statusPatch()` template function registers patches during rendering, following the `FileRegistry.Register()` pattern.

**Alternatives considered**:

- *Text templates producing JSON*: Error-prone (comma handling, quoting), no validation at render time, harder to parallelize safely.
- *Hardcoded per-resource-type status handlers*: Breaks genericity. Users with custom CRDs would need Go code changes.
- *Configuration-driven rules in CRD spec*: Cannot express complex condition logic without reinventing a programming language in YAML.

**Rationale**: Side-effect registration during rendering is an established pattern in this codebase. It avoids JSON text construction, enables type-safe collection, and keeps all status logic in templates where it belongs.

### Decision 2: Outcome-keyed variants for lifecycle-aware status

**Choice**: `statusPatch()` accepts a map of outcome variants: `rendered`, `deployed`, `renderFailed`, `deployFailed`. The controller selects the appropriate variant based on pipeline outcome.

```
statusPatch(namespace, name, apiVersion, kind, {
    "rendered":     { ... },   // applied after successful render
    "deployed":     { ... },   // applied after successful deployment
    "renderFailed": { ... },   // applied when later render phases fail
    "deployFailed": { ... },   // applied when deployment fails
})
```

**Alternatives considered**:

- *Single status payload applied after deployment only*: Cannot communicate acceptance before programming, no feedback on failures.
- *Controller-generated Programmed condition*: Breaks genericity — controller would need to know condition names and semantics.
- *Re-render on failure with error context*: Adds a second render pass, complicates the pipeline.

**Rationale**: Templates render all variants upfront. The controller is a dumb phase selector — it picks `"deployed"` on success, `"deployFailed"` on failure, without parsing the payload. This keeps all status semantics in templates while enabling phase-appropriate responses.

### Decision 3: Early rendering at priority 200

**Choice**: Status patch snippets use the `status-patches-*` extension point at priority 200, rendering after `features-*` (050-150) but before `backends-*` and `frontends-*` (500).

**Rationale**: Feature analysis (route resolution, TLS registration, address discovery) happens in `features-*` snippets. Status patches need this analysis. Complex config generation (backends, frontends, maps) happens at priority 500 and is the most likely failure point. By rendering status at 200, patches are already collected when a 500-level snippet fails. The `renderFailed` variant can then be applied.

### Decision 4: Server-Side Apply for patch application

**Choice**: Apply patches via SSA (`types.ApplyPatchType`) to the `/status` subresource with `fieldManager: "haptic"`.

**Alternatives considered**:

- *JSON Patch (RFC 6902)*: Requires computing current-to-desired diffs, templates would need to produce operation arrays rather than desired state.
- *JSON Merge Patch (RFC 7386)*: Cannot handle arrays with merge semantics (Gateway API `parents[]` would be replaced wholesale).
- *Strategic Merge Patch*: Kubernetes-specific, but SSA supersedes it with better field ownership.

**Rationale**: SSA lets templates declare desired state. The controller doesn't compute diffs. Field ownership means `haptic` only manages fields it sets — other controllers' status fields (different field managers) are preserved. Array merge strategies in Gateway API CRDs (e.g., `parents[]` keyed by `parentRef+controllerName`) work correctly with SSA.

### Decision 5: StatusPatchCollector in render context

**Choice**: A `StatusPatchCollector` (similar to `FileRegistry`) is added to the render context. The `statusPatch()` function writes to it. The collector is thread-safe via `sync.Mutex`.

**Rationale**: Follows the `FileRegistry` pattern exactly. Thread-safety is required because templates use `go` goroutines for sharded parallel rendering. The collector merges patches by `(namespace, name, apiVersion, kind)` — multiple snippets can contribute to the same resource's status.

### Decision 6: Checksum-based skip optimization

**Choice**: The `StatusApplier` maintains `map[resourceKey]checksum` of last-applied patches. Patches with unchanged checksums are skipped. The cache is cleared on leadership transitions (force re-apply to claim field ownership).

**Rationale**: Status patches rarely change between reconciliation cycles (only when resources are added/removed/modified). Skipping unchanged SSA calls reduces API server load. Clearing on leadership transition ensures the new leader establishes field ownership.

### Decision 7: Address discovery via controller Service watch

**Choice**: The library adds a namespace-scoped `watchedResources` entry for Services in the controller namespace, filtered by label selector. Templates read `.status.loadBalancer.ingress` to populate addresses.

**Alternatives considered**:

- *Explicit configuration in values.yaml*: Requires users to know their load balancer IP before deployment.
- *Watch Nodes for bare-metal NodePort*: More complex, less common use case.

**Rationale**: Auto-discovery from the LoadBalancer Service covers the most common deployment model. The namespace-scoped watch is consistent with existing patterns (the controller already watches its own CRDs in its namespace). Falls back gracefully — if no address is available, no address status is emitted.

### Decision 8: Parallel rendering with sharded status patches

**Choice**: Status patch library snippets use the same `ShardedX` macro pattern with `shard_slice()` and `go` keyword for parallel execution, matching existing ingress/gateway library patterns.

**Rationale**: Status patch generation loops over the same resource collections as backend/map generation. For large clusters (thousands of ingresses), sequential iteration would be a bottleneck. The sharding pattern is proven and the `StatusPatchCollector`'s mutex-based thread safety supports concurrent writes.

## Risks / Trade-offs

**[Risk] Status patch snippets fail during rendering** → Status patches render early (priority 200) and contain simple logic (iterate resources, build conditions). If a status snippet itself fails, it's a bug in the template library, not a runtime condition. The library ships with tests for status snippets.

**[Risk] SSA field ownership conflicts with other controllers** → Each controller uses a distinct `fieldManager`. SSA preserves fields owned by other managers. This is the designed behavior of SSA. If two controllers try to manage the same condition type on the same resource, SSA will detect the conflict — this is an operational misconfiguration, not a software bug.

**[Risk] API server load from status updates** → Mitigated by checksum-based skip. In steady state (no resource changes), zero SSA calls are made. On changes, only affected resources get updated. For large clusters, status updates are O(changed resources), not O(total resources).

**[Risk] `transitionTime()` requires reading current status from stores** → Resources in stores include their `.status` field. The `transitionTime()` helper reads the existing condition's `lastTransitionTime` and preserves it when the status value hasn't changed. This means the checksum also stays stable when conditions haven't changed, feeding into the skip optimization.

**[Trade-off] Outcome variants are rendered optimistically** → All four variants are rendered even though only one will be applied. This adds marginal rendering cost but is negligible compared to the main config generation. The alternative (conditional rendering based on pipeline outcome) would require a second render pass.

**[Trade-off] Priority 200 means status patches can't reference config generation results** → Status patches render before backends and frontends. They can reference feature analysis results (route resolution, TLS certs, addresses) but not generated backend names or frontend logic. This is acceptable because status conditions are about resource acceptance and reference resolution, not about generated config details.
