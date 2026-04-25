// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package comparator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// compareNamedSections is the slice-input wrapper around
// compareNamedMaps that powers most named-section comparators
// (resolvers, log forwards, log profiles, traces, ACME providers,
// userlists, EE bot management profiles, etc.). Its add/delete/
// update/no-op behaviour is well covered through compare_enterprise_test.go
// and the generic compareNamedMaps tests in compare_named_maps_test.go.
//
// Two non-obvious branches in the slice-to-map conversion step are
// uncovered:
//
//  1. Items whose getName returns "" are SILENTLY SKIPPED. The
//     `if name := getName(item); name != ""` guard inside both
//     loops drops them from the working map. A regression that
//     dropped this guard (or flipped to keep empty-named items)
//     would either panic on map["" overwrite collisions or, worse,
//     emit operations targeting an unnamed section that the
//     DataPlane API would reject with a confusing error.
//
//  2. Duplicate names within the same slice: the LAST occurrence
//     wins because the map assignment overwrites earlier entries.
//     A real-world cause is a malformed config where two sections
//     accidentally share a name; the comparator should produce a
//     deterministic result (the last-wins semantics) so the
//     orchestrator either reconciles to the last definition or
//     fails the validation gate cleanly. A regression that
//     stopped on first occurrence (or panicked on duplicate)
//     would change the observable diff in subtle ways.
//
// Pin both behaviours directly. Reuses the markerOp /
// stringFactories / summary helpers defined in
// compare_named_maps_test.go (same package).

// item is a tiny struct so the slice has *T values that we can
// deref in the getName / equal callbacks. Mirrors how real
// callers pass *models.X slices.
type item struct {
	name  string
	value string
}

func TestCompareNamedSections_EmptyNamesAreSkipped(t *testing.T) {
	create, remove, update := stringFactories()

	current := []*item{
		{name: "real", value: "v1"},
		{name: "", value: "anonymous-1"}, // skipped
	}
	desired := []*item{
		{name: "real", value: "v1"},      // unchanged
		{name: "", value: "anonymous-2"}, // skipped (different value, but no name)
		{name: "added", value: "fresh"},  // create
	}

	ops := compareNamedSections(
		current, desired,
		func(it *item) string { return it.name },
		func(a, b *item) bool { return a.value == b.value },
		// Each factory wraps the value, NOT the name, so the test
		// can verify which item slipped through.
		func(it *item) Operation { return create(it.value) },
		func(it *item) Operation { return remove(it.value) },
		func(it *item) Operation { return update(it.value) },
	)

	// Expected: only the "added" item produces an op. Both
	// empty-name items are skipped on BOTH sides, and the "real"
	// items are equal so produce no op.
	assert.Equal(t, []string{"create:fresh"}, summary(ops),
		"items with empty names must be silently skipped on BOTH current and desired sides; "+
			"a regression that kept them would either panic on duplicate \"\" key collisions or "+
			"emit operations targeting an unnamed section that the DataPlane API rejects")
}

func TestCompareNamedSections_DuplicateNamesLastWins(t *testing.T) {
	create, remove, update := stringFactories()

	// Both sides have two "dupe" entries. The last in slice order
	// wins because the slice-to-map conversion overwrites earlier
	// values. equal() compares by value, so the test verifies which
	// pair (current-last, desired-last) actually drives the diff.
	current := []*item{
		{name: "dupe", value: "current-first"}, // overwritten
		{name: "dupe", value: "current-LAST"},  // wins
		{name: "single", value: "v1"},
	}
	desired := []*item{
		{name: "dupe", value: "desired-first"}, // overwritten
		{name: "dupe", value: "desired-LAST"},  // wins
		{name: "single", value: "v1"},          // unchanged
	}

	ops := compareNamedSections(
		current, desired,
		func(it *item) string { return it.name },
		func(a, b *item) bool { return a.value == b.value },
		func(it *item) Operation { return create(it.value) },
		func(it *item) Operation { return remove(it.value) },
		func(it *item) Operation { return update(it.value) },
	)

	// Expected: one update operation for "dupe", carrying
	// "desired-LAST" as the new value. The first "dupe" entries on
	// both sides are silently dropped by the map-overwrite. A
	// regression that panicked on duplicate keys, or used the FIRST
	// occurrence instead of last, would produce a different op.
	require.Len(t, ops, 1, "duplicate names must collapse to a single op (last-wins overwrite)")
	assert.Equal(t, []string{"update:desired-LAST"}, summary(ops),
		"the LAST occurrence of a duplicate name must drive the diff (map-overwrite semantics); "+
			"a refactor that took the first occurrence or panicked on duplicates would silently change behaviour")
}

func TestCompareNamedSections_NilSlicesAreEmpty(t *testing.T) {
	// Sanity: passing nil slices behaves the same as empty slices.
	// The slice-to-map conversion uses range, which is a no-op on
	// nil. A regression that dereferenced the slice header before
	// ranging would panic.
	create, remove, update := stringFactories()

	require.NotPanics(t, func() {
		ops := compareNamedSections[item](
			nil, nil,
			func(it *item) string { return it.name },
			func(a, b *item) bool { return a.value == b.value },
			func(it *item) Operation { return create(it.value) },
			func(it *item) Operation { return remove(it.value) },
			func(it *item) Operation { return update(it.value) },
		)
		assert.Empty(t, ops, "two nil slices must produce zero ops, not panic")
	})
}
