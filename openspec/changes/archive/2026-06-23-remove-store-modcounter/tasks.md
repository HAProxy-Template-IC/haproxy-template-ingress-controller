# Tasks

## 1. Remove the store-side counter

- [x] 1.1 `pkg/k8s/store/memory.go` — remove the `modCount uint64` field, every `s.modCount++` increment in Add/Update/Delete/Clear, and the `ModCount()` method.
- [x] 1.2 `pkg/k8s/store/cached.go` — remove the `modCount uint64` field, every `s.modCount++` increment, and the `ModCount()` method.

## 2. Remove the interface + adapter delegation

- [x] 2.1 `pkg/stores/provider.go` — remove the `ModCounter` interface, the `TypesStoreAdapter.ModCount()` delegation, and the `var _ ModCounter = (*TypesStoreAdapter)(nil)` assertion.

## 3. Remove tests

- [x] 3.1 Delete `pkg/k8s/store/modcount_test.go`.
- [x] 3.2 Drop the `ModCount` fakes/fields and the `TestTypesStoreAdapter_ModCount` test from the validator + discovery test files; keep the surrounding Get/List/error-passthrough coverage.

## 4. Docs

- [x] 4.1 `pkg/k8s/store/{CLAUDE,README}.md` — remove the `modCount` / `ModCounter` references; keep the zero-copy-reads explanation, drop the "read modCount to memoise List()" guidance (the interface no longer exists).

## 5. Spec

- [x] 5.1 Amend `openspec/specs/storage-strategies/spec.md`: remove the **Modification Tracking** requirement + its 3 scenarios; trim the **TypesStoreAdapter** requirement to drop the `ModCount` delegation clause and its `(0, false)` scenario.

## 6. Verify

- [x] 6.1 `go build ./...`, `go vet ./pkg/k8s/... ./pkg/stores/... ./pkg/controller/...`, `make test`, `make lint`, `make audit` all green; `grep -rn 'ModCount\|ModCounter\|modCount' pkg/ cmd/ --include='*.go'` returns nothing.
