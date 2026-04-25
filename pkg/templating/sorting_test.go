// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package templating

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

// sortableItems is the multi-criteria, sort.Interface-conforming type
// underlying the {{ items | sort_by([...]) }} filter. Its sort_helpers
// primitives (evaluateExpression, getLength, compareValues) already
// have direct unit tests, but the top-level orchestration that ties
// them together — precomputeKeys parsing, descending flag handling,
// :exists modifier, ` | length` operator, multi-criteria tie-break
// chaining — has no end-to-end test.
//
// These integration-style tests pin the observable BEHAVIOUR of the
// full sort chain so a refactor of any single piece (key parsing,
// criterion-modifier handling, the Less / precomputeKeys split) can't
// silently change the sorted order users see in templates.
func TestSortableItems_AscendingByOneStringField(t *testing.T) {
	items := []any{
		map[string]any{"name": "charlie"},
		map[string]any{"name": "alpha"},
		map[string]any{"name": "bravo"},
	}

	sortItems(t, items, []string{"$.name"})

	assert.Equal(t, []any{
		map[string]any{"name": "alpha"},
		map[string]any{"name": "bravo"},
		map[string]any{"name": "charlie"},
	}, items, "default direction is ascending; missing :desc means ascending")
}

func TestSortableItems_DescModifierFlipsDirection(t *testing.T) {
	items := []any{
		map[string]any{"priority": 1},
		map[string]any{"priority": 10},
		map[string]any{"priority": 5},
	}

	sortItems(t, items, []string{"$.priority:desc"})

	// :desc must reverse comparison output: high priority first.
	// A refactor that read :desc but applied it backwards (or only at
	// precomputeKeys parsing without flipping Less) would silently
	// produce ascending output — pin the actual order users see.
	assert.Equal(t, []any{
		map[string]any{"priority": 10},
		map[string]any{"priority": 5},
		map[string]any{"priority": 1},
	}, items)
}

func TestSortableItems_ExistsModifierGroupsByPresence(t *testing.T) {
	// :exists collapses the value to a bool (present/absent). With
	// :desc, present (true) sorts BEFORE absent (false).
	items := []any{
		map[string]any{"name": "no-method"},                       // method missing
		map[string]any{"name": "with-method", "method": "GET"},    // method present
		map[string]any{"name": "with-method-2", "method": "POST"}, // method present
		map[string]any{"name": "no-method-2"},                     // method missing
	}

	sortItems(t, items, []string{"$.method:exists:desc"})

	// All "method present" items must come BEFORE "method missing"
	// items. The relative order within each bucket is not guaranteed
	// (sort.Sort is not stable) — only the partition is.
	for i, item := range items[:2] {
		m := item.(map[string]any)
		_, ok := m["method"]
		assert.True(t, ok, "first half (i=%d) must have method present (:exists:desc puts true first)", i)
	}
	for i, item := range items[2:] {
		m := item.(map[string]any)
		_, ok := m["method"]
		assert.False(t, ok, "second half (i=%d) must have method absent", i+2)
	}
}

func TestSortableItems_LengthOperatorOrdersBySize(t *testing.T) {
	// ` | length` rewrites the criterion key to the slice/map length.
	// With :desc, longest collections sort first.
	items := []any{
		map[string]any{"name": "two", "tags": []any{"a", "b"}},
		map[string]any{"name": "five", "tags": []any{"a", "b", "c", "d", "e"}},
		map[string]any{"name": "one", "tags": []any{"a"}},
	}

	sortItems(t, items, []string{"$.tags | length:desc"})

	assert.Equal(t, []any{
		map[string]any{"name": "five", "tags": []any{"a", "b", "c", "d", "e"}},
		map[string]any{"name": "two", "tags": []any{"a", "b"}},
		map[string]any{"name": "one", "tags": []any{"a"}},
	}, items, "longest tags first under | length:desc")
}

func TestSortableItems_MultiCriteriaTieBreak(t *testing.T) {
	// Two routes with the same priority should fall through to the
	// secondary criterion (name ascending). This is the contract
	// templates rely on for deterministic output: ties at criterion
	// N must consult criterion N+1, not just collapse to "equal".
	items := []any{
		map[string]any{"name": "delta", "priority": 5},
		map[string]any{"name": "bravo", "priority": 10},
		map[string]any{"name": "alpha", "priority": 5},
		map[string]any{"name": "charlie", "priority": 10},
	}

	sortItems(t, items, []string{"$.priority:desc", "$.name"})

	// Priority 10 group: bravo before charlie (alphabetical tiebreak)
	// Priority 5 group: alpha before delta (alphabetical tiebreak)
	assert.Equal(t, []any{
		map[string]any{"name": "bravo", "priority": 10},
		map[string]any{"name": "charlie", "priority": 10},
		map[string]any{"name": "alpha", "priority": 5},
		map[string]any{"name": "delta", "priority": 5},
	}, items)
}

func TestSortableItems_EmptyAndSingleItemAreNoOp(t *testing.T) {
	// Edge cases that callers exercise via templates rendering an
	// empty resource set — must not panic and must not reorder.
	t.Run("empty slice sorts cleanly", func(t *testing.T) {
		items := []any{}
		sortItems(t, items, []string{"$.priority:desc"})
		assert.Empty(t, items)
	})

	t.Run("single item is unchanged", func(t *testing.T) {
		items := []any{map[string]any{"name": "only"}}
		sortItems(t, items, []string{"$.name"})
		assert.Equal(t, []any{map[string]any{"name": "only"}}, items)
	})
}

func TestSortableItems_NoCriteriaIsNoOp(t *testing.T) {
	// With zero criteria, Less() returns false for every pair, which
	// in sort.Sort means "all elements are equal" — order is not
	// defined but the slice must remain the same length and contain
	// the same elements. Pin that the call doesn't panic and that
	// no elements are lost.
	items := []any{
		map[string]any{"name": "a"},
		map[string]any{"name": "b"},
		map[string]any{"name": "c"},
	}
	sortItems(t, items, []string{})
	assert.Len(t, items, 3, "no criteria must not drop or duplicate items")
}

// sortItems is a thin helper that wires items + criteria into a
// sortableItems and runs sort.Sort. Equivalent to the path the
// scriggo sort_by filter takes at template render time.
func sortItems(t *testing.T, items []any, criteria []string) {
	t.Helper()
	s := &sortableItems{
		items:    items,
		criteria: criteria,
	}
	s.precomputeKeys()
	sort.Sort(s)
}
