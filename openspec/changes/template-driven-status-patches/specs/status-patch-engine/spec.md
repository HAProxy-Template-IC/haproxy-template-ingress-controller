# Status Patch Engine

Template functions, collection infrastructure, and application component for template-driven Kubernetes resource status updates.

## ADDED Requirements

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

The StatusApplier SHALL be a leader-only event adapter component. It SHALL subscribe to `TemplateRenderedEvent` (to cache latest status patches), `ReconciliationCompletedEvent` (to apply `deployed` variants), `ReconciliationFailedEvent` (to apply failure variants), and `BecameLeaderEvent`/`LostLeadershipEvent` (for leadership transitions). On `ReconciliationCompletedEvent`, it SHALL apply the `deployed` variant for each patch. On render failure (detected from `ReconciliationFailedEvent` phase), it SHALL apply the `renderFailed` variant. On deployment failure, it SHALL apply the `deployFailed` variant. On successful render but before deployment, it SHALL apply the `rendered` variant. It SHALL apply patches via Server-Side Apply to the `/status` subresource using `k8s.io/client-go/dynamic` with `fieldManager: "haptic"`. It SHALL resolve apiVersion+kind to GVR via the REST mapper utility component.

#### Scenario: Apply deployed variants on successful reconciliation

- **WHEN** a `ReconciliationCompletedEvent` is received and the latest status patches include resources with `deployed` variants
- **THEN** the StatusApplier SHALL apply each resource's `deployed` variant via SSA to the `/status` subresource

#### Scenario: Apply renderFailed variants on render failure

- **WHEN** a `ReconciliationFailedEvent` with a render phase error is received and cached status patches include `renderFailed` variants
- **THEN** the StatusApplier SHALL apply each resource's `renderFailed` variant via SSA

#### Scenario: Apply deployFailed variants on deployment failure

- **WHEN** a `ReconciliationFailedEvent` with a deployment phase error is received and cached status patches include `deployFailed` variants
- **THEN** the StatusApplier SHALL apply each resource's `deployFailed` variant via SSA

#### Scenario: Skip patch when variant is absent

- **WHEN** a resource has no `deployFailed` variant registered and a deployment failure occurs
- **THEN** the StatusApplier SHALL not perform an SSA call for that resource

#### Scenario: Leadership transition clears checksum cache

- **WHEN** a `BecameLeaderEvent` is received
- **THEN** the StatusApplier SHALL clear its lastAppliedPatches checksum cache to force re-application on next cycle

#### Scenario: Lost leadership stops applying patches

- **WHEN** a `LostLeadershipEvent` is received
- **THEN** the StatusApplier SHALL stop applying status patches and clear pending state

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
