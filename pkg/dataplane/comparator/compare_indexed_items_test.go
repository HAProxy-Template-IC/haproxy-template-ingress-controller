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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// compareIndexedItems is the foundational POSITION-based diff helper
// that powers every indexed-section comparator: filters, HTTP checks,
// TCP checks, log targets, and QUIC initial rules. It is the
// position-based sibling of compareNamedMaps (which compares by
// name).
//
// The contract has FOUR rules driven by position (NOT by content):
//
//  1. Position only in desired (i >= len(current)) → create at i.
//  2. Position only in current (i >= len(desired)) → delete at i.
//  3. Position in both, equal() returns false      → update at i
//     with the DESIRED item (not current; that would mean writing
//     the old value back).
//  4. Position in both, equal() returns true       → no-op.
//
// EMISSION ORDER: updates and creates come out in ascending index
// order (the natural for-i loop), but deletes come out in DESCENDING
// index order, appended after all updates/creates. The descending
// order is load-bearing under sequential application: the dataplane
// API's underlying config-parser implements Delete(idx) by shifting
// every later element down a slot (see
// haproxytech/client-native/.../http-request_generated.go's
// `(*Requests).Delete`). Ascending-order deletes applied
// sequentially cascade-shift the remaining indices, so each
// subsequent Delete(N+1) targets a different rule than the
// comparator intended, eventually running off the end. Descending
// deletes shift only indices we've already removed (or are about to
// remove), so the operation stream is correct regardless of
// execution order.
//
// The "by position" vs "by name" distinction is load-bearing: items
// at the same index but with different content trigger UPDATE, NOT
// delete+create. Switching to delete+create would unnecessarily
// destroy and recreate every changed rule, breaking ordering and
// consuming extra HAProxy reload cycles.
//
// Operations carry their INDEX into the factory callback. A
// regression that swapped the index parameter (e.g. always passed
// 0) would cause every operation to target the wrong rule slot
// at the API layer — visible only at deploy time.
//
// Pin all four branches plus index propagation plus the
// updates-then-descending-deletes ordering in a table-driven test.
// Use the same markerOp pattern as compare_named_maps_test.go to
// avoid coupling to specific HAProxy model types.

// indexedMarker is markerOp's position-aware sibling. It records
// which factory fired (kind), with which value (drives equal()), at
// which index. The index assertion is the part that catches a
// regression in the "carry the position into the operation" contract.
type indexedMarker struct {
	kind  string // "create" | "delete" | "update"
	value string
	index int
}

var _ sections.Operation = (*indexedMarker)(nil)

func (m *indexedMarker) Type() sections.OperationType { return sections.OperationCreate }
func (m *indexedMarker) Section() string              { return "" }
func (m *indexedMarker) Priority() int                { return 0 }
func (m *indexedMarker) Parent() string               { return "" }
func (m *indexedMarker) Execute(_ context.Context, _ *client.DataplaneClient, _ string) error {
	return nil
}
func (m *indexedMarker) Describe() string { return m.kind + ":" + m.value }

// indexedFactories returns the three callback factories that
// compareIndexedItems uses. Each callback bundles the value and the
// index into a marker so the test can assert on both.
func indexedFactories() (create, remove, update func(item *string, i int) Operation) {
	create = func(v *string, i int) Operation {
		return &indexedMarker{kind: "create", value: *v, index: i}
	}
	remove = func(v *string, i int) Operation {
		return &indexedMarker{kind: "delete", value: *v, index: i}
	}
	update = func(v *string, i int) Operation {
		return &indexedMarker{kind: "update", value: *v, index: i}
	}
	return
}

// strSliceToPtrs converts a []string to []*string for compareIndexedItems' generic [T any] signature.
func strSliceToPtrs(s []string) []*string {
	out := make([]*string, len(s))
	for i := range s {
		v := s[i] // capture so each pointer is to a distinct value
		out[i] = &v
	}
	return out
}

// equalStringPtrs compares two *string by dereferenced value.
func equalStringPtrs(a, b *string) bool { return *a == *b }

func TestCompareIndexedItems(t *testing.T) {
	create, remove, update := indexedFactories()

	tests := []struct {
		name    string
		current []string
		desired []string
		// expected operations as "kind:value@index" strings, in
		// the order compareIndexedItems is documented to emit:
		// updates and creates in ascending index order from the
		// for-i loop, followed by deletes in DESCENDING index
		// order (see the function docstring for why — sequential
		// ascending-order deletes shift the indices of remaining
		// rules and so silently target the wrong rules under
		// per-parent serialisation).
		want []string
	}{
		{
			name:    "both empty: no ops",
			current: nil,
			desired: nil,
			want:    nil,
		},
		{
			name:    "current empty + desired non-empty: all creates with sequential ascending indices",
			current: nil,
			desired: []string{"a", "b", "c"},
			want:    []string{"create:a@0", "create:b@1", "create:c@2"},
		},
		{
			name:    "current non-empty + desired empty: all deletes in DESCENDING index order",
			current: []string{"x", "y"},
			desired: nil,
			want:    []string{"delete:y@1", "delete:x@0"},
		},
		{
			name:    "same length all equal: no ops",
			current: []string{"a", "b", "c"},
			desired: []string{"a", "b", "c"},
			want:    nil,
		},
		{
			name:    "same length all changed: updates carry DESIRED value at their index",
			current: []string{"a", "b", "c"},
			desired: []string{"A", "B", "C"},
			want:    []string{"update:A@0", "update:B@1", "update:C@2"},
		},
		{
			name:    "desired longer: shared prefix updates / no-ops, trailing indices become creates",
			current: []string{"a", "b"},
			desired: []string{"a", "B", "c", "d"},
			// i=0: equal → no-op; i=1: changed → update; i=2,3: only in desired → create.
			// The contract is INDEX-based: the same position with different content is UPDATE,
			// not delete+create — a refactor that emitted delete+create here would multiply
			// API calls and reload counts.
			want: []string{"update:B@1", "create:c@2", "create:d@3"},
		},
		{
			name:    "current longer: shared prefix updates / no-ops, trailing indices become deletes (DESCENDING)",
			current: []string{"a", "B", "c", "d"},
			desired: []string{"a", "b"},
			// i=0: equal → no-op; i=1: changed → update. Updates come first in
			// ascending order. Then deletes for i=2,3 are appended in descending
			// order: delete@3 before delete@2 so the parser's index-shift on
			// Delete only touches indices already removed.
			want: []string{"update:b@1", "delete:d@3", "delete:c@2"},
		},
		{
			name:    "single-item swap at index 0: must be UPDATE (positional), not delete+create",
			current: []string{"old"},
			desired: []string{"new"},
			want:    []string{"update:new@0"},
		},
		{
			name:    "many trailing deletes emit in DESCENDING order (regression: cascade index-shift bug)",
			current: []string{"a", "b", "c", "d", "e"},
			desired: []string{"a"},
			// Without the descending-emit fix this would be
			// ["delete:b@1", "delete:c@2", "delete:d@3", "delete:e@4"]. Applied
			// sequentially against the dataplane API's shifting Delete, each
			// successive op targets a stale index and either removes the wrong
			// rule or errors out-of-range. Descending order makes every Delete
			// touch a slot whose later contents are already gone.
			want: []string{"delete:e@4", "delete:d@3", "delete:c@2", "delete:b@1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := compareIndexedItems(
				strSliceToPtrs(tt.current),
				strSliceToPtrs(tt.desired),
				equalStringPtrs,
				create, remove, update,
			)

			require.Len(t, ops, len(tt.want),
				"compareIndexedItems must emit exactly the expected number of ops; "+
					"a missing branch would silently drop one or more transitions")

			for i, op := range ops {
				m, ok := op.(*indexedMarker)
				require.True(t, ok, "ops[%d] must be an *indexedMarker", i)
				got := m.kind + ":" + m.value + "@" + itoa(m.index)
				assert.Equal(t, tt.want[i], got,
					"ops[%d] mismatch — kind, value, OR index disagree. "+
						"The index is load-bearing: a regression that hardcoded i=0 in any factory "+
						"would silently target the wrong API slot.", i)
			}
		})
	}
}

// itoa avoids importing strconv just for this one place.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = '0' + byte(n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
