# status-patch-engine Specification

## Purpose

Defines template-driven status patching: templates register per-resource status payloads during rendering via the statusPatch function, keyed by outcome-phase variants (rendered, deployed, renderFailed, validateFailed, deployFailed), so the controller writes status for any watched resource without kind-specific Go. The StatusApplier selects the variant matching each pipeline lifecycle event and applies it to the resource's status subresource via Server-Side Apply under phase-scoped field managers. Helper functions (condition, transitionTime, toJSON) let templates build Kubernetes-conventional condition payloads.

## Requirements

### Requirement: statusPatch Template Function

The `statusPatch` function SHALL register a status patch during template rendering. It SHALL accept namespace (string), name (string), apiVersion (string), kind (string), and a variants map (map[string]interface{}). The variants map SHALL use outcome phase keys: `rendered`, `deployed`, `renderFailed`, `deployFailed`. Each key's value SHALL be a map[string]interface{} representing the desired `.status` content for that phase. At least one variant key SHALL be required; omitted phases result in no status update for that phase. Multiple calls for the same resource (identified by namespace+name+apiVersion+kind) SHALL merge their variant maps, with later calls overriding earlier ones for the same variant key.

#### Scenario: Register a status patch with deployed variant

- **WHEN** a template calls `statusPatch("default", "my-ingress", "networking.k8s.io/v1", "Ingress", map[string]interface{}{"deployed": map[string]interface{}{"loadBalancer": map[string]interface{}{"ingress": addresses}}})`
- **THEN** the StatusPatchCollector SHALL contain one entry for `default/my-ingress` of kind `Ingress` with the `deployed` variant populated

#### Scenario: Multiple calls for same resource merge variants

- **WHEN** one snippet calls `statusPatch("default", "my-route", "gateway.networking.k8s.io/v1", "HTTPRoute", map[string]interface{}{"rendered": renderedStatus})` and another snippet calls `statusPatch("default", "my-route", "gateway.networking.k8s.io/v1", "HTTPRoute", map[string]interface{}{"deployed": deployedStatus})`
- **THEN** the StatusPatchCollector SHALL contain one entry for `default/my-route` with both `rendered` and `deployed` variants populated

#### Scenario: Later call overrides same variant key

- **WHEN** snippet A calls `statusPatch("default", "my-gw", "gateway.networking.k8s.io/v1", "Gateway", map[string]interface{}{"deployed": statusA})` and snippet B calls `statusPatch("default", "my-gw", "gateway.networking.k8s.io/v1", "Gateway", map[string]interface{}{"deployed": statusB})`
- **THEN** the StatusPatchCollector SHALL contain the `deployed` variant from snippet B for `default/my-gw`

#### Scenario: Missing variant key results in no-op for that phase

- **WHEN** a template registers a status patch with only `"deployed"` and `"deployFailed"` variants
- **THEN** the StatusApplier SHALL not perform any SSA call for this resource during the `rendered` or `renderFailed` phases

### Requirement: condition Template Helper Function

The `condition` function SHALL construct a map[string]interface{} matching the metav1.Condition structure. It SHALL accept type (string), status (string), reason (string), message (string), observedGeneration (interface{}), and lastTransitionTime (string). The returned map SHALL contain keys: `type`, `status`, `reason`, `message`, `observedGeneration`, and `lastTransitionTime`. Empty message values SHALL be included as empty strings. The observedGeneration parameter SHALL accept both int and float64 types (to handle JSON number unmarshaling from Kubernetes resources).

#### Scenario: Build a condition with all fields

- **WHEN** a template calls `condition("Accepted", "True", "Accepted", "Route accepted", 5, "2025-01-01T00:00:00Z")`
- **THEN** the returned map SHALL contain `{"type": "Accepted", "status": "True", "reason": "Accepted", "message": "Route accepted", "observedGeneration": 5, "lastTransitionTime": "2025-01-01T00:00:00Z"}`

#### Scenario: Build a condition with empty message

- **WHEN** a template calls `condition("Programmed", "True", "Programmed", "", 3, "2025-01-01T00:00:00Z")`
- **THEN** the returned map SHALL contain `"message": ""`

### Requirement: transitionTime Template Helper Function

The `transitionTime` function SHALL determine the correct `lastTransitionTime` for a condition by comparing the new status value against the resource's current condition status. It SHALL accept a resource (interface{}, the full Kubernetes resource object), a conditionType (string), and a newStatus (string). It SHALL search the resource's `.status.conditions` array (accessed via dig-style traversal) for a condition matching the given type. If a matching condition exists and its `.status` field equals newStatus, the function SHALL return the existing condition's `lastTransitionTime` value. If no matching condition exists, or the status has changed, the function SHALL return the current time in RFC 3339 format. The function SHALL also support searching in `.status.parents[].conditions` for route resources by accepting an optional parentIndex (int) parameter.

#### Scenario: Status unchanged preserves existing transition time

- **WHEN** a resource has an existing condition `{type: "Accepted", status: "True", lastTransitionTime: "2025-01-01T00:00:00Z"}` and `transitionTime(resource, "Accepted", "True")` is called
- **THEN** the function SHALL return `"2025-01-01T00:00:00Z"`

#### Scenario: Status changed returns current time

- **WHEN** a resource has an existing condition `{type: "Accepted", status: "True", lastTransitionTime: "2025-01-01T00:00:00Z"}` and `transitionTime(resource, "Accepted", "False")` is called
- **THEN** the function SHALL return the current time in RFC 3339 format

#### Scenario: No existing condition returns current time

- **WHEN** a resource has no condition with type "Programmed" and `transitionTime(resource, "Programmed", "True")` is called
- **THEN** the function SHALL return the current time in RFC 3339 format

#### Scenario: Search in route parent conditions

- **WHEN** a route resource has `.status.parents[0].conditions` containing `{type: "Accepted", status: "True", lastTransitionTime: "2025-06-01T12:00:00Z"}` and `transitionTime(resource, "Accepted", "True", 0)` is called with parentIndex 0
- **THEN** the function SHALL return `"2025-06-01T12:00:00Z"`

### Requirement: toJSON Template Filter

The `toJSON` filter SHALL serialize any Go value to a JSON string. It SHALL handle maps, slices, strings, numbers, booleans, and nil values. Nil values SHALL serialize to `"null"`. The output SHALL be a valid JSON string suitable for embedding in structured output. The function SHALL be registered as both a filter (piped usage) and a standalone function.

#### Scenario: Serialize a map to JSON

- **WHEN** a template calls `toJSON(map[string]interface{}{"key": "value"})`
- **THEN** the output SHALL be `{"key":"value"}`

#### Scenario: Serialize a string to JSON

- **WHEN** a template calls `toJSON("hello")`
- **THEN** the output SHALL be `"hello"` (with JSON quotes)

#### Scenario: Serialize nil to JSON

- **WHEN** a template calls `toJSON(nil)`
- **THEN** the output SHALL be `null`

### Requirement: StatusPatchCollector Thread Safety

The StatusPatchCollector SHALL be safe for concurrent writes from multiple goroutines. It SHALL use a sync.Mutex to protect the internal patch map. The collector SHALL be created per render cycle (like FileRegistry) and passed to templates via the render context. After rendering completes, the collector SHALL provide a method to retrieve all collected patches as a slice.

#### Scenario: Concurrent writes from sharded goroutines

- **WHEN** 4 goroutines concurrently call `statusPatch()` for different resources
- **THEN** all 4 patches SHALL be collected without data races or lost writes

#### Scenario: Concurrent writes for same resource from different goroutines

- **WHEN** 2 goroutines concurrently call `statusPatch()` for the same resource with different variant keys
- **THEN** both variants SHALL be present in the merged result for that resource

#### Scenario: Collector returns all patches after rendering

- **WHEN** rendering completes and `Patches()` is called on the collector
- **THEN** it SHALL return a slice containing all registered StatusPatch entries

### Requirement: StatusApplier Event Adapter Component

The StatusApplier SHALL be an all-replica subscriber that applies patches only while leader, and SHALL be STATELESS on the success path: patches ride the event that triggers each apply, and there is no cached-patches side channel (a cache overwritten on every render allowed render N+1's patches to be written for deploy N — the removed race). The event-to-variant mapping SHALL be: `ResourcesAppliedEvent` applies the `rendered` variant (published by the ResourceApplier after the same render's resources exist, so conditions never precede infrastructure); `DeploymentCompletedEvent` applies the `deployed` variant only when Total and Succeeded are both greater than zero; `DeploymentSkippedEvent` applies the `deployed` variant (the data plane already converged on this config); `ReconciliationFailedEvent` applies `renderFailed` for the render phase, `validateFailed` for the validation phase, and `deployFailed` otherwise. Patches for failure events are the last successful render's snapshot forwarded by the Coordinator.

Each phase SHALL apply to the `/status` subresource via Server-Side Apply with Force under its OWN field manager, `haptic-<phase>` (for example `haptic-rendered`, `haptic-deployed`), so phases own disjoint condition entries under listType=map semantics and never relinquish each other's conditions. Per-event applies SHALL fan out with bounded concurrency of 64. The SSA-skip checksum cache SHALL be keyed by phase plus namespace/name/GVR and bounded at 65536 entries with a wholesale reset when full; a NotFound apply result SHALL be skipped silently without caching (benign delete race under churn). GVRs SHALL resolve via the RESTMapper, with a one-shot reset-and-retry on a no-match so late-registered CRDs resolve without a controller restart. `BecameLeaderEvent` SHALL set the leader flag and clear the checksum cache (no patch replay — the Reconciler's fresh reconciliation supplies current patches); `LostLeadershipEvent` SHALL clear the leader flag.

#### Scenario: Deployed variant applied from the deploy's own patches

- **WHEN** a `DeploymentCompletedEvent` with Succeeded > 0 arrives carrying status patches
- **THEN** the StatusApplier SHALL apply each patch's `deployed` variant from the event payload itself, describing exactly the config that deploy shipped

#### Scenario: Zero-success deployment applies nothing

- **WHEN** a `DeploymentCompletedEvent` arrives with Total 0 or Succeeded 0
- **THEN** no `deployed` variant SHALL be applied

#### Scenario: Skipped deployment still marks deployed

- **WHEN** a `DeploymentSkippedEvent` arrives because the data plane is already at the rendered config
- **THEN** the StatusApplier SHALL apply the `deployed` variant so status-only deltas do not stay at CRD defaults forever

#### Scenario: Rendered variant waits for resources

- **WHEN** a render completes
- **THEN** the `rendered` variant SHALL be applied on the subsequent `ResourcesAppliedEvent`, after the same render's k8sResources were applied

#### Scenario: Failure phase selects the variant

- **WHEN** a `ReconciliationFailedEvent` arrives with phase "validation"
- **THEN** the StatusApplier SHALL apply the `validateFailed` variant from the event's (last-good-render) patches

#### Scenario: Phase-scoped field managers avoid condition tug-of-war

- **WHEN** the rendered variant writes Accepted and the deployed variant later writes Programmed on the same resource
- **THEN** the two applies SHALL use the field managers `haptic-rendered` and `haptic-deployed` respectively, and the deployed apply SHALL NOT relinquish the Accepted condition

#### Scenario: Deleted resource skipped silently

- **WHEN** an SSA apply returns NotFound because the resource was deleted between render and apply
- **THEN** the StatusApplier SHALL treat it as a skip — no error log, no failure event, no checksum cached

#### Scenario: Leadership transition clears checksum cache

- **WHEN** a `BecameLeaderEvent` is received
- **THEN** the StatusApplier SHALL clear its checksum cache and rely on the fresh reconciliation for new patches

#### Scenario: Lost leadership stops applying patches

- **WHEN** a `LostLeadershipEvent` is received
- **THEN** the StatusApplier SHALL clear its leader flag and apply no further patches until re-elected

### Requirement: Checksum-Based Skip Optimization

The StatusApplier SHALL maintain a map of `(namespace, name, gvr)` → SHA-256 checksum for the last successfully applied status patch. Before performing an SSA call, it SHALL compute the checksum of the new patch payload and compare it with the cached checksum. If the checksums match, the SSA call SHALL be skipped. The checksum cache SHALL be cleared when the component receives a `BecameLeaderEvent` to ensure the new leader establishes field ownership.

#### Scenario: Unchanged patch skipped

- **WHEN** the status patch for `default/my-ingress` has the same checksum as the last applied patch
- **THEN** the StatusApplier SHALL skip the SSA call for that resource

#### Scenario: Changed patch applied

- **WHEN** the status patch for `default/my-ingress` has a different checksum than the last applied patch
- **THEN** the StatusApplier SHALL perform the SSA call and update the cached checksum

#### Scenario: New resource always applied

- **WHEN** a status patch is registered for a resource not in the checksum cache
- **THEN** the StatusApplier SHALL perform the SSA call and add the checksum to the cache

#### Scenario: Removed resource cleaned from cache

- **WHEN** a resource that was previously in the checksum cache is no longer present in the latest status patches
- **THEN** the StatusApplier SHALL remove that resource's entry from the checksum cache

### Requirement: Status Update Events

The StatusApplier SHALL publish `StatusUpdateCompletedEvent` after successfully applying all status patches for a reconciliation cycle. The event SHALL include the count of applied patches, the count of skipped patches (checksum match), and the total duration. The StatusApplier SHALL publish `StatusUpdateFailedEvent` when an SSA call fails. The event SHALL include the target resource (namespace, name, GVR), the error, and whether the failure is retriable.

#### Scenario: Successful status update publishes completion event

- **WHEN** all status patches for a reconciliation cycle are applied successfully (3 applied, 2 skipped)
- **THEN** a `StatusUpdateCompletedEvent` SHALL be published with `AppliedCount: 3`, `SkippedCount: 2`

#### Scenario: Failed SSA call publishes failure event

- **WHEN** an SSA call for `default/my-route` fails with a conflict error
- **THEN** a `StatusUpdateFailedEvent` SHALL be published with the resource identity and error details
