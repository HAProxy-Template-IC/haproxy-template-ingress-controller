// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package comparator

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// compareNamedMaps is the foundational map-diff helper that powers
// every named-section comparator in this package: backends, frontends,
// resolvers (via compareNameserversWithIndex), security userlists,
// and several others. The contract is straightforward but has FOUR
// load-bearing rules and NO direct test coverage:
//
//  1. Names present in desired but missing from current → create.
//  2. Names present in current but missing from desired → delete.
//  3. Names present in both, equal() returns false      → update.
//  4. Names present in both, equal() returns true       → no-op.
//
// The function operates on Go maps which have NON-DETERMINISTIC
// iteration order — the OUTPUT order of operations is therefore
// unspecified, but the SET of operations must match exactly.
// Callers (e.g. compareBackends, compareNameserversWithIndex) sort
// the result downstream where order matters; here we only assert
// the operation set.
//
// A regression that, e.g.:
//   - swapped the equal-check polarity would emit updates for
//     unchanged sections (causing thrashing),
//   - swapped current/desired in either direction loop would emit
//     creates for deletes and vice versa (data destruction),
//   - skipped one of the three loops entirely would silently miss
//     deletes / creates / updates,
//
// would all pass the existing higher-level tests if the offending
// section happened to be unused in fixtures, but get caught here.

// markerOp is a synthetic Operation used to verify which factory
// callback fired and with which value. compareNamedMaps does not
// call any methods on the returned Operations beyond storing them
// in the result slice, so a minimal stub satisfying the interface
// is sufficient.
type markerOp struct {
	kind  string // "create" | "delete" | "update"
	value string // the value passed to the factory
}

var _ sections.Operation = (*markerOp)(nil)

func (m *markerOp) Type() sections.OperationType { return sections.OperationCreate } // unused
func (m *markerOp) Section() string              { return "" }
func (m *markerOp) Priority() int                { return 0 }
func (m *markerOp) Execute(_ context.Context, _ *client.DataplaneClient, _ string) error {
	return nil
}
func (m *markerOp) Describe() string { return m.kind + ":" + m.value }

// summary turns a slice of operations into a sorted "kind:value"
// string slice so map-iteration order doesn't make the assertions
// flaky.
func summary(ops []Operation) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		m, ok := op.(*markerOp)
		if !ok {
			continue
		}
		out = append(out, m.kind+":"+m.value)
	}
	sort.Strings(out)
	return out
}

// stringFactories returns the three callback factories that
// compareNamedMaps uses, each producing a *markerOp tagged with the
// kind so the test can verify which callback fired for which value.
func stringFactories() (create, remove, update func(string) Operation) {
	create = func(v string) Operation { return &markerOp{kind: "create", value: v} }
	remove = func(v string) Operation { return &markerOp{kind: "delete", value: v} }
	update = func(v string) Operation { return &markerOp{kind: "update", value: v} }
	return
}

// equalAlways and equalNever are the two extreme equal() implementations.
// Real callers pass model-specific equality checks; we only need to
// drive the equal() polarity to exercise the update branch.
func equalAlways(_, _ string) bool { return true }
func equalNever(_, _ string) bool  { return false }

func TestCompareNamedMaps_OnlyDesired_AllCreates(t *testing.T) {
	create, remove, update := stringFactories()

	ops := compareNamedMaps(
		map[string]string{},
		map[string]string{"a": "v1", "b": "v2"},
		equalNever, // irrelevant; no overlap
		create, remove, update,
	)

	assert.Equal(t, []string{"create:v1", "create:v2"}, summary(ops),
		"every name only in desired must produce a create; missing one would silently drop newly-added sections")
}

func TestCompareNamedMaps_OnlyCurrent_AllDeletes(t *testing.T) {
	create, remove, update := stringFactories()

	ops := compareNamedMaps(
		map[string]string{"a": "v1", "b": "v2"},
		map[string]string{},
		equalNever,
		create, remove, update,
	)

	assert.Equal(t, []string{"delete:v1", "delete:v2"}, summary(ops),
		"every name only in current must produce a delete; "+
			"missing one would leave orphaned sections in HAProxy that no longer exist in config")
}

func TestCompareNamedMaps_BothNilOrEmpty_NoOps(t *testing.T) {
	create, remove, update := stringFactories()

	tests := []struct {
		name             string
		current, desired map[string]string
	}{
		{name: "both nil", current: nil, desired: nil},
		{name: "current nil, desired empty", current: nil, desired: map[string]string{}},
		{name: "current empty, desired nil", current: map[string]string{}, desired: nil},
		{name: "both empty", current: map[string]string{}, desired: map[string]string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := compareNamedMaps(tt.current, tt.desired, equalNever, create, remove, update)
			assert.Empty(t, ops, "nil/empty inputs must produce no operations and not panic")
		})
	}
}

func TestCompareNamedMaps_BothPresentEqual_NoOp(t *testing.T) {
	create, remove, update := stringFactories()

	// Both maps share the same keys AND equal() reports them as
	// equal — this is the no-op path that prevents reconciliation
	// thrashing on unchanged sections.
	ops := compareNamedMaps(
		map[string]string{"a": "v1", "b": "v2"},
		map[string]string{"a": "v1", "b": "v2"},
		equalAlways,
		create, remove, update,
	)

	assert.Empty(t, ops,
		"equal() returning true must skip the update; "+
			"a regression that always fired update would cause reconciliation thrashing")
}

func TestCompareNamedMaps_BothPresentNotEqual_Updates(t *testing.T) {
	create, remove, update := stringFactories()

	// Same keys, equal() says "not equal" → updates for every shared key.
	ops := compareNamedMaps(
		map[string]string{"a": "v1", "b": "v2"},
		map[string]string{"a": "v1-new", "b": "v2-new"},
		equalNever,
		create, remove, update,
	)

	assert.Equal(t, []string{"update:v1-new", "update:v2-new"}, summary(ops),
		"same names with different values must emit updates carrying the DESIRED value, not the current")
}

func TestCompareNamedMaps_MixedAllBranches(t *testing.T) {
	// The realistic case: some names create-only, some delete-only,
	// some update, some no-op. Equal()-by-value-string drives the
	// update vs no-op decision per shared key.
	create, remove, update := stringFactories()

	ops := compareNamedMaps(
		map[string]string{
			"shared-equal":    "v1",
			"shared-changed":  "old",
			"only-in-current": "to-delete",
		},
		map[string]string{
			"shared-equal":    "v1",
			"shared-changed":  "new",
			"only-in-desired": "to-create",
		},
		// equal() returns true only when values match byte-for-byte.
		func(a, b string) bool { return a == b },
		create, remove, update,
	)

	require.Len(t, ops, 3,
		"realistic mix must produce exactly 3 operations: 1 create + 1 delete + 1 update; "+
			"the shared-equal entry must NOT contribute an op")

	assert.Equal(t,
		[]string{"create:to-create", "delete:to-delete", "update:new"},
		summary(ops),
		"each branch must fire exactly once with the correct factory and the correct value (DESIRED for create/update, CURRENT for delete)")
}
