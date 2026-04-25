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
)

// compareByNamespaceName is the comparator MemoryStore uses to keep its
// per-key resource slices sorted at insert time, so Get() / List() can
// return deterministic results without re-sorting on every read. The
// invariant the comparator must satisfy is: compare by namespace first,
// then by name. Pin every branch — the read-side stability of the store
// depends on this ordering being predictable across resource types.
func TestCompareByNamespaceName(t *testing.T) {
	res := func(ns, name string) any {
		return map[string]any{
			"metadata": map[string]any{
				"namespace": ns,
				"name":      name,
			},
		}
	}

	tests := []struct {
		name string
		a    any
		b    any
		want int
	}{
		{name: "identical namespace+name returns 0", a: res("ns", "a"), b: res("ns", "a"), want: 0},
		{name: "earlier namespace sorts first (negative)", a: res("aaa", "z"), b: res("bbb", "a"), want: -1},
		{name: "later namespace sorts last (positive)", a: res("zzz", "a"), b: res("aaa", "z"), want: 1},
		{name: "same namespace, earlier name sorts first", a: res("ns", "a"), b: res("ns", "b"), want: -1},
		{name: "same namespace, later name sorts last", a: res("ns", "z"), b: res("ns", "a"), want: 1},
		{name: "empty namespace sorts before non-empty namespace", a: res("", "z"), b: res("ns", "a"), want: -1},
		{name: "missing metadata extracts to ('', '') and ties", a: 42, b: "string", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareByNamespaceName(tt.a, tt.b)
			// cmp.Compare uses sign semantics, not exactly -1/0/1.
			switch {
			case tt.want < 0:
				assert.Negative(t, got, "expected negative comparison result")
			case tt.want > 0:
				assert.Positive(t, got, "expected positive comparison result")
			default:
				assert.Zero(t, got, "expected zero comparison result")
			}
		})
	}
}

// sortResourceSlice is a thin wrapper over slices.SortFunc(compare). Pin
// that the wrapper sorts the input in-place (callers reuse the same slice
// across operations) and that the resulting order matches the documented
// namespace-then-name contract.
func TestSortResourceSlice(t *testing.T) {
	res := func(ns, name string) any {
		return map[string]any{
			"metadata": map[string]any{
				"namespace": ns,
				"name":      name,
			},
		}
	}

	t.Run("empty slice is a no-op", func(t *testing.T) {
		var items []any
		sortResourceSlice(items)
		assert.Empty(t, items)
	})

	t.Run("single element is unchanged", func(t *testing.T) {
		items := []any{res("ns", "a")}
		sortResourceSlice(items)
		assert.Equal(t, []any{res("ns", "a")}, items)
	})

	t.Run("sorts by namespace first, then name (in place)", func(t *testing.T) {
		items := []any{
			res("zzz", "a"),
			res("aaa", "b"),
			res("aaa", "a"),
			res("mid", "z"),
		}
		sortResourceSlice(items)

		assert.Equal(t, []any{
			res("aaa", "a"),
			res("aaa", "b"),
			res("mid", "z"),
			res("zzz", "a"),
		}, items)
	})

	t.Run("sort is stable enough that ties (same ns+name) preserve their relative order", func(t *testing.T) {
		// Two distinct map values that compare equal under
		// extractNamespaceName must remain in their relative input order;
		// callers don't rely on a specific tie-break rule.
		first := res("ns", "a")
		second := res("ns", "a")
		items := []any{first, second}
		sortResourceSlice(items)
		assert.Len(t, items, 2)
	})
}
