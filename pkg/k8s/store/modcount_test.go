// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ModCount is the contract external caching layers (renderer cache,
// validators) rely on to detect store changes without polling. The
// invariants both MemoryStore and CachedStore must satisfy:
//
//   - the counter starts at 0
//   - the bool is always true (both stores support tracking)
//   - the counter increments on every successful mutation:
//     Add (new), Add (replace), Update, Delete, Clear
//   - the counter does NOT increment on read-only operations or on
//     mutations that fail validation (wrong key count, etc.)
//   - the counter is monotonically increasing
//
// MemoryStore and CachedStore have separate modCount fields and need
// independent verification — but they share the contract, so a single
// table-driven test against the Store interface is the natural shape.
func TestModCount_MemoryStore(t *testing.T) {
	store := NewMemoryStore(2)

	// Initial: 0, supported.
	got, supported := store.ModCount()
	assert.True(t, supported, "MemoryStore must report ModCount as supported")
	assert.Equal(t, uint64(0), got, "fresh MemoryStore must start at modCount=0")

	// Successful Add increments.
	require.NoError(t, store.Add(map[string]any{"id": "a"}, []string{"default", "a"}))
	got, _ = store.ModCount()
	assert.Equal(t, uint64(1), got, "Add of a new resource must increment ModCount")

	// Add of duplicate (same namespace/name) — counts as a replace; pin
	// that this is observable as another mutation rather than a no-op.
	require.NoError(t, store.Add(map[string]any{"id": "a", "v": 2}, []string{"default", "a"}))
	got, _ = store.ModCount()
	assert.Equal(t, uint64(2), got, "Add replacing an existing resource still increments ModCount")

	// Failed Add (wrong key count) must NOT increment.
	require.Error(t, store.Add(map[string]any{}, []string{"only-one-key"}))
	got, _ = store.ModCount()
	assert.Equal(t, uint64(2), got, "failed Add (wrong key count) must NOT increment ModCount")

	// Update on existing resource increments.
	require.NoError(t, store.Update(map[string]any{"id": "a", "v": 3}, []string{"default", "a"}))
	got, _ = store.ModCount()
	assert.Equal(t, uint64(3), got, "Update on existing resource must increment ModCount")

	// Delete increments.
	require.NoError(t, store.Delete("default", "a"))
	got, _ = store.ModCount()
	assert.Equal(t, uint64(4), got, "Delete must increment ModCount")

	// Read-only operations must NOT increment.
	_, _ = store.Get("anything")
	_, _ = store.List()
	_ = store.Size()
	got, _ = store.ModCount()
	assert.Equal(t, uint64(4), got, "Get/List/Size must NOT increment ModCount")

	// Clear increments (mutation, even if store was empty after Delete).
	require.NoError(t, store.Clear())
	got, _ = store.ModCount()
	assert.Equal(t, uint64(5), got, "Clear must increment ModCount")
}

// MemoryStore.Update has a non-obvious branch worth pinning: when called
// with a resource that doesn't exist yet, the implementation falls back
// to Add semantics. The modCount must still increment in that branch
// (otherwise upsert-style callers would lose the change signal).
func TestModCount_MemoryStore_UpdateUpsertIncrements(t *testing.T) {
	store := NewMemoryStore(2)

	got, _ := store.ModCount()
	assert.Equal(t, uint64(0), got)

	// Update on a never-added key must still bump (it inserts).
	require.NoError(t, store.Update(map[string]any{"id": "new"}, []string{"default", "new"}))
	got, _ = store.ModCount()
	assert.Equal(t, uint64(1), got, "Update on missing key (upsert) must increment ModCount")
}
