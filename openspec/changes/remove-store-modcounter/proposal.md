## Why

The `ModCounter` store interface (`pkg/stores.ModCounter`, `ModCount() (uint64, bool)`) and the
per-store `modCount uint64` counters in `MemoryStore` / `CachedStore` are **write-only dead code**.
Every mutation (`Add`/`Update`/`Delete`/`Clear`) bumps the counter, but **no production code reads it**
— the only references are the increment sites, the interface declaration, the `TypesStoreAdapter`
delegation, and the mechanism's own unit test.

The counter's sole intended purpose was cross-reconciliation cache invalidation: a caching layer above
the store memoising an expensive `List()`-derived computation, rebuilding only when `modCount` changed.
That consumer **did exist** — commit `1c7bf7c5` ("perf: cache unwrapped List results across
reconciliations") added `RenderService.listCache` keyed on `modCount` — and was **removed two hours
later** in `7c5ce40f` when the cached computation became cheap (the float-to-int conversion moved to
per-resource-change). It has been gone for ~4 months. The counter is now a tombstone.

The per-render `List()` cost it might otherwise address is already handled without it: every chart
`resources.<name>.List()` call routes through `rendercontext.StoreWrapper`, which pins one
`Store.List()` snapshot per render (the wrapper is rebuilt fresh each render). Repeated `List()` within
a render is O(1); the snapshot keys on the wrapper's lifetime, not on `modCount`.

Carrying a write-only counter plus a live `SHALL` spec requirement for a deleted consumer is dead
weight, against this project's standing rule of not keeping code alive only for its own tests. If a
cross-render `List` cache ever returns, re-adding a clean counter (and re-amending this spec) is a
small, well-understood change — the previous implementation is in git history.

## What Changes

- Remove the `modCount` field, its mutation increments, and the `ModCount()` method from `MemoryStore`
  and `CachedStore`.
- Remove the `ModCounter` interface, the `TypesStoreAdapter.ModCount()` delegation, and the
  `var _ ModCounter` assertion from `pkg/stores/provider.go`.
- Delete the mechanism's tests and the `ModCount` methods on test fakes.
- Drop the `modCount` / `ModCounter` references from the `pkg/k8s/store` package docs (the zero-copy-reads
  explanation stays; only the modCount guidance goes).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `storage-strategies`: the **Modification Tracking** requirement (and its three scenarios) is removed;
  the **TypesStoreAdapter** requirement is trimmed to drop the `ModCount` delegation clause and its
  `(0, false)` scenario. The remaining store, key-matching, and adapter Get/List behaviour is unchanged.

## Impact

- **pkg/k8s/store/memory.go**, **pkg/k8s/store/cached.go**: remove `modCount` field + increments + `ModCount()`.
- **pkg/stores/provider.go**: remove `ModCounter` interface + adapter delegation + assertion.
- **Tests**: delete `pkg/k8s/store/modcount_test.go`; drop `ModCount` fakes/assertions in the validator
  and discovery test files.
- **Docs**: `pkg/k8s/store/{CLAUDE,README}.md` modCount passages.
- **openspec/specs/storage-strategies/spec.md**: synced to drop the Modification Tracking requirement and
  trim TypesStoreAdapter (this change implements the removal, so the live spec is amended in the same MR).

## Non-goals

- Removing the per-render `List()` snapshot in `StoreWrapper`. That is the mechanism that actually keeps
  repeated enumeration cheap; it does not use `modCount` and stays.
- Pre-emptively designing a cross-render `List` cache. If one is needed later, re-add a counter then,
  guided by the deleted `1c7bf7c5` implementation, and re-amend this spec.
