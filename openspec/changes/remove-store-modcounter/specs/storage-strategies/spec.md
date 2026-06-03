## REMOVED Requirements

### Requirement: Modification Tracking

**Reason**: The `ModCounter` interface is removed. It was a write-only counter with no production
reader. Its only consumer — a cross-reconciliation `List` cache (`RenderService.listCache`, added in
`1c7bf7c5`) — was removed in `7c5ce40f` when the cached computation became cheap, and has been gone for
~4 months. Repeated per-render `List()` is already kept cheap by the `StoreWrapper` per-render snapshot,
which does not use a modification counter. If a cross-render `List` cache returns, a counter can be
re-added and this requirement re-introduced.

The removed requirement read: "MemoryStore and CachedStore SHALL implement the ModCounter interface. The
ModCount method SHALL return a monotonically increasing counter that is incremented on every mutation
(Add, Update, Delete, Clear) and a boolean true indicating tracking is supported." Its three scenarios
(ModCount increments on Add, ModCount increments on Clear, ModCount stable across reads) are removed
with it.

## MODIFIED Requirements

### Requirement: TypesStoreAdapter

TypesStoreAdapter SHALL bridge the structurally identical Store interfaces defined in pkg/k8s/types and pkg/stores by delegating all Store method calls to an inner store.

#### Scenario: Adapter delegates Get to inner store

WHEN Get is called on a TypesStoreAdapter
THEN the call SHALL be forwarded to the inner store's Get method with the same arguments.
