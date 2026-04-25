// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package indexer

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// navigateToField and deleteField are the two reflect-based primitives
// FieldFilter.Filter delegates to. The existing TestFieldFilter_Filter
// suite exercises the happy paths through Filter(), but the
// per-primitive error paths are silent failure modes worth pinning
// directly:
//
//  navigateToField:
//   * nil pointer deref → "nil pointer" error (defensive — without
//     this, a partially-constructed resource would panic mid-filter)
//   * map miss → "field not found" error (lets the parent removeField
//     short-circuit cleanly when an intermediate path segment is
//     absent — see "Field doesn't exist, nothing to remove" branch in
//     removeField, which intentionally swallows this error)
//   * struct miss → "field not found" error (same purpose)
//   * unsupported kind (slice, int, …) → "navigating into <kind>"
//     error so a misconfigured pattern surfaces clearly instead of
//     silently doing nothing
//
//  deleteField:
//   * nil pointer deref → silent no-op (NOT an error — matches the
//     "missing fields are not errors during filtering" contract from
//     removeField)
//   * map hit → key removed; absent key is silent
//   * struct hit → field zeroed (struct fields can't be deleted, only
//     zeroed)
//   * unsupported kind → error so misconfigured patterns surface
//
// A regression that returned errors instead of silent no-ops in
// deleteField (or vice versa) would change observable Filter behaviour
// for every caller — pin both directions.

// fieldNav is a tiny test-only struct with a mix of types so the
// exact-name and case-insensitive struct-field matches are both
// observable. Kept package-private to this test file.
type fieldNav struct {
	Name string
	Age  int
}

func TestFieldFilter_navigateToField(t *testing.T) {
	filter := &FieldFilter{}

	t.Run("map hit returns value", func(t *testing.T) {
		m := map[string]any{"k": "v"}
		got, err := filter.navigateToField(reflect.ValueOf(m), "k")
		require.NoError(t, err)
		assert.Equal(t, "v", got.Interface())
	})

	t.Run("map miss returns 'field not found' error", func(t *testing.T) {
		m := map[string]any{"k": "v"}
		_, err := filter.navigateToField(reflect.ValueOf(m), "missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "field not found",
			"this error must include 'field not found' so removeField can short-circuit "+
				"cleanly on absent intermediate path segments")
	})

	t.Run("struct exact-name hit returns value", func(t *testing.T) {
		s := fieldNav{Name: "n", Age: 7}
		got, err := filter.navigateToField(reflect.ValueOf(s), "Name")
		require.NoError(t, err)
		assert.Equal(t, "n", got.Interface())
	})

	t.Run("struct case-insensitive fallback hit returns value", func(t *testing.T) {
		// Documented in findStructField: matches are tried exact first,
		// then case-insensitive — Kubernetes JSON tags often use
		// camelCase that differs from Go's PascalCase field names.
		s := fieldNav{Name: "n", Age: 7}
		got, err := filter.navigateToField(reflect.ValueOf(s), "name")
		require.NoError(t, err)
		assert.Equal(t, "n", got.Interface(),
			"case-insensitive fallback is critical for navigating Kubernetes "+
				"objects whose JSON tags use camelCase but whose Go fields use "+
				"PascalCase — a regression that dropped this fallback would break "+
				"every JSONPath that targets a typed object's field by JSON name")
	})

	t.Run("struct miss returns 'field not found' error", func(t *testing.T) {
		s := fieldNav{Name: "n"}
		_, err := filter.navigateToField(reflect.ValueOf(s), "Missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "field not found")
	})

	t.Run("nil pointer returns 'nil pointer' error", func(t *testing.T) {
		// Defensive: a partially-constructed resource (intermediate
		// segment is nil) must surface as an error, not panic.
		var p *fieldNav
		_, err := filter.navigateToField(reflect.ValueOf(p), "Name")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil pointer",
			"navigating into a nil pointer must return a clear 'nil pointer' error "+
				"rather than panicking — partially-constructed resources are common "+
				"in fixture-based tests and during Add/Update races")
	})

	t.Run("slice (unsupported kind) returns 'navigating into <kind>' error", func(t *testing.T) {
		// A misconfigured JSONPath that tries to navigate INTO a slice
		// without an index must surface clearly, not be silently
		// swallowed (which would let the caller think the deletion
		// succeeded when nothing happened).
		s := []string{"a", "b"}
		_, err := filter.navigateToField(reflect.ValueOf(s), "anything")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "navigating into",
			"unsupported kinds must return an error containing 'navigating into' "+
				"so misconfigured patterns are visible rather than silently no-op")
		assert.Contains(t, err.Error(), "slice")
	})

	t.Run("int (unsupported kind) returns 'navigating into <kind>' error", func(t *testing.T) {
		_, err := filter.navigateToField(reflect.ValueOf(42), "anything")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "int")
	})
}

func TestFieldFilter_deleteField(t *testing.T) {
	filter := &FieldFilter{}

	t.Run("map: existing key is removed", func(t *testing.T) {
		m := map[string]any{"k": "v", "keep": "me"}
		err := filter.deleteField(reflect.ValueOf(m), "k")
		require.NoError(t, err)
		assert.NotContains(t, m, "k")
		assert.Contains(t, m, "keep", "siblings of the removed key must be untouched")
	})

	t.Run("map: missing key is a silent no-op", func(t *testing.T) {
		m := map[string]any{"keep": "me"}
		err := filter.deleteField(reflect.ValueOf(m), "absent")
		require.NoError(t, err,
			"deleting an absent key must NOT error — this matches the 'missing fields "+
				"are not errors during filtering' contract that removeField relies on")
		assert.Contains(t, m, "keep")
	})

	t.Run("struct: settable field is zeroed", func(t *testing.T) {
		// Need an addressable struct (via pointer) so Set works.
		// deleteField unwraps the pointer internally.
		s := &fieldNav{Name: "n", Age: 7}
		err := filter.deleteField(reflect.ValueOf(s), "Name")
		require.NoError(t, err)
		assert.Equal(t, "", s.Name,
			"struct fields can't be deleted, only zeroed — Name must be reset to its "+
				"zero value (empty string)")
		assert.Equal(t, 7, s.Age, "siblings of the zeroed field must be untouched")
	})

	t.Run("struct: missing field is a silent no-op", func(t *testing.T) {
		s := &fieldNav{Name: "n"}
		err := filter.deleteField(reflect.ValueOf(s), "Missing")
		require.NoError(t, err,
			"missing struct fields must NOT error — same contract as missing map keys")
		assert.Equal(t, "n", s.Name)
	})

	t.Run("nil pointer is a silent no-op", func(t *testing.T) {
		// The contract: deleteField on a nil pointer is silent.
		// removeField calls deleteField with the navigated parent — if
		// the parent navigation succeeded but the value is nil (rare
		// but possible with partially-constructed maps), we must NOT
		// error out and break the entire filter pass.
		var p *fieldNav
		err := filter.deleteField(reflect.ValueOf(p), "Name")
		assert.NoError(t, err,
			"nil pointer in deleteField must be silently swallowed; this differs "+
				"from navigateToField which errors — deleteField's role is 'remove if "+
				"present', and 'present in a nil parent' is trivially false")
	})

	t.Run("unsupported kind returns 'deleting field from <kind>' error", func(t *testing.T) {
		// Slice as parent — nothing to delete a NAMED field from.
		// This must surface as an error rather than silently doing
		// nothing, otherwise misconfigured JSONPaths would look like
		// they succeeded.
		s := []string{"a", "b"}
		err := filter.deleteField(reflect.ValueOf(s), "anything")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deleting field from",
			"unsupported parent kinds must return an error so misconfigured "+
				"patterns are visible rather than silently no-op")
		assert.Contains(t, err.Error(), "slice")
	})
}
