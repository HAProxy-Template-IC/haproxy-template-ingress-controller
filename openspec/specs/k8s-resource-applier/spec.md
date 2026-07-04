# k8s-resource-applier Specification

## Purpose

Defines how template-declared Kubernetes resources (spec.k8sResources) are reconciled onto the cluster: the ResourceApplier consumes the rendered resources carried on each ReconciliationCompletedEvent and applies them via Server-Side Apply, injecting ownership metadata, pruning resources that vanish from the render, and recovering orphans across controller restarts. The applier is resource-agnostic — it applies whatever the templates emit, with no kind-specific Go.

## Requirements

### Requirement: Server-Side Apply of Rendered Resources

The ResourceApplier SHALL be an all-replica subscriber that applies only while leader. It SHALL read the rendered resources directly from each ReconciliationCompletedEvent (stateless on the success path — no side-channel cache, so no latest-versus-completed race), and apply each via Server-Side Apply with field manager "haptic" and Force=true, with bounded concurrency of 16. A SHA-256 checksum cache keyed by namespace/name/GVR SHALL skip the SSA round-trip when the payload matches the last applied value; the cache SHALL be cleared on BecameLeaderEvent so a new leader re-establishes field ownership. GVRs SHALL be resolved through the RESTMapper (cluster discovery data, no guessed plurals), with a one-shot discovery reset and retry on a no-match so late-registered CRDs resolve without a controller restart. Consecutive reconciliation-completed events SHALL coalesce latest-wins in the component mailbox.

#### Scenario: Unchanged resource skips the API call

- **WHEN** a rendered resource's payload checksum matches the cached checksum for its key
- **THEN** no SSA call SHALL be made for that resource

#### Scenario: New leader re-applies

- **WHEN** a replica becomes leader
- **THEN** the checksum cache SHALL be cleared so the next reconciliation applies every rendered resource at least once

#### Scenario: Follower does not apply

- **WHEN** a ReconciliationCompletedEvent arrives on a non-leader replica
- **THEN** no SSA call SHALL be made

### Requirement: Ownership Metadata Injection

For every full-ownership resource the applier SHALL inject the managed-by label (haproxy-haptic.org/managed-by, value defaulting to "haptic-controller") and an ownerReferences entry pointing at the owning HAProxyTemplateConfig CR with controller=true and blockOwnerDeletion=true, so Kubernetes garbage collection cascade-deletes the rendered resources when the CR is removed. The ownerReference SHALL be skipped when no CR identity was supplied (empty UID) or when the resource's namespace differs from the controller's own namespace (Kubernetes rejects cross-namespace ownerRefs). The injection SHALL operate on a copy — the rendered object handed in by the pipeline is never mutated.

#### Scenario: CR deletion cascades

- **WHEN** the HAProxyTemplateConfig CR is deleted
- **THEN** Kubernetes garbage collection SHALL delete every full-ownership resource the applier created, via the injected ownerReference

#### Scenario: Cross-namespace resource gets no ownerRef

- **WHEN** a rendered resource targets a namespace other than the controller's own
- **THEN** the managed-by label SHALL still be injected but no ownerReference SHALL be added

### Requirement: Partial-Ownership Opt-Out

A rendered resource annotated haproxy-haptic.org/ownership="partial" SHALL be treated as jointly owned with another field manager (for example helm or argocd): the applier SHALL NOT inject the managed-by label, SHALL NOT inject an ownerReference, and SHALL NOT track the resource for orphan deletion — a partial resource vanishing from the render releases haptic's SSA-owned fields but never deletes the object. The ownership annotation itself SHALL always be stripped from the payload before SSA; it is a controller-internal flag. Any other annotation value, or its absence, means full ownership.

#### Scenario: Partial resource never orphan-deleted

- **WHEN** a partial-ownership resource disappears from a later render
- **THEN** the applier SHALL NOT delete it

#### Scenario: Internal annotation never reaches the apiserver

- **WHEN** a rendered resource carries the ownership annotation
- **THEN** the SSA payload SHALL NOT contain that annotation

### Requirement: Render-Diff Orphan Pruning

After each apply pass the applier SHALL delete every full-ownership resource that was applied in a previous pass but is absent from the new rendered set (tracked via its applied-keys map). NotFound on delete SHALL be tolerated. A failed delete SHALL keep the resource in the applied-keys map so the prune retries on the next reconciliation.

#### Scenario: Resource removed from the render is deleted

- **WHEN** a resource present in the previous render is absent from the current one
- **THEN** the applier SHALL delete it from the cluster

#### Scenario: Failed delete retried next cycle

- **WHEN** an orphan delete fails with an error other than NotFound
- **THEN** the resource SHALL remain tracked and the delete SHALL be retried on the next reconciliation

### Requirement: Startup-Orphan Recovery

On BecameLeaderEvent the applier SHALL rebuild its applied-keys map from cluster state: enumerate the namespace-scoped resource types the cluster serves (discovery), list each type in the controller's namespace by the managed-by label, and stage only objects whose ownerReferences include the owning CR's UID — excluding label-inherited children created by other controllers (for example EndpointSlices stamped with a Service's labels). Recovery SHALL be best-effort: types without list and delete verbs, subresources, and list calls failing with Forbidden or NotFound SHALL be skipped silently, and partial discovery results SHALL be processed rather than aborting. When no discovery client is configured or the CR UID is empty, the UID filter degrades to recovering everything labelled.

#### Scenario: Orphan surviving a controller-down deletion is pruned

- **WHEN** a resource was applied before a controller restart and its desired state was removed while the controller was down
- **THEN** the new leader SHALL recover it into the applied-keys map via label plus ownerRef-UID match, and the next reconciliation SHALL prune it

#### Scenario: Label-inherited children excluded

- **WHEN** an object carries the managed-by label but its ownerReferences do not include the CR's UID
- **THEN** recovery SHALL NOT stage it for orphan tracking

### Requirement: Namespace Restriction Wiring

The applier SHALL support a RestrictToOwnNamespace mode that refuses (logs and skips, without failing the reconciliation) any rendered resource that is cluster-scoped or targets a namespace other than the controller's own. The controller wires this mode OFF in production: cross-namespace applies are permitted at the applier boundary and the chart's RBAC grants are the actual security gate — a misbehaving template gets Forbidden from the apiserver for anything the granted Role does not cover.

#### Scenario: Restricted mode refuses foreign namespaces

- **WHEN** RestrictToOwnNamespace is enabled and a rendered resource targets another namespace
- **THEN** the applier SHALL skip it with a warning and continue the pass

#### Scenario: Production relies on RBAC

- **WHEN** the controller runs with its production wiring
- **THEN** cross-namespace applies SHALL reach the apiserver and succeed or fail per the granted RBAC

### Requirement: ResourcesAppliedEvent Ordering

After completing an apply-and-prune pass, the applier SHALL publish a ResourcesAppliedEvent carrying the cycle's status patches (forwarded from the ReconciliationCompletedEvent, with correlation propagated). The StatusApplier writes the "rendered" status variant on this event — not on render completion — so status conditions can never precede the infrastructure resources they describe.

#### Scenario: Conditions follow infrastructure

- **WHEN** a render emits both k8sResources and status patches
- **THEN** the ResourcesAppliedEvent carrying the patches SHALL be published only after the resources were applied
