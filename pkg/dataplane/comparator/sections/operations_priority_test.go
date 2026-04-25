// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package sections

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// IndexChildOp.Priority is load-bearing for parallel execution ordering of
// index-based child resources (ACLs, HTTP rules, TCP rules). The contract is:
//   - Creates/Updates: lower index runs first  → basePriority + index
//   - Deletes:         higher index runs first → basePriority + (999 - index)
//
// Both formulas live in a single 1000-wide sub-priority slot per base level
// (PriorityMultiplier=1000) so different sections never interleave even
// after the index adjustment. Pin the formula directly so a future refactor
// can't quietly change the ordering and break parallel rule lists.
func TestIndexChildOp_Priority(t *testing.T) {
	makeOp := func(opType OperationType, basePriority, index int) *IndexChildOp[string, string] {
		// Use string→string transform that just returns the model so the
		// generic type is unambiguous; transform is unused for Priority().
		return NewIndexChildOp[string, string](
			opType,
			"acl",
			basePriority,
			"frontend-x",
			index,
			"model",
			func(s string) string { return s },
			nil, // executeFn unused for Priority()
			func() string { return "test" },
		)
	}

	tests := []struct {
		name         string
		opType       OperationType
		basePriority int
		index        int
		want         int
	}{
		// Creates: basePriority * 1000 + index
		{name: "create at index 0 = base*1000", opType: OperationCreate, basePriority: 30, index: 0, want: 30000},
		{name: "create at index 1 = base*1000 + 1", opType: OperationCreate, basePriority: 30, index: 1, want: 30001},
		{name: "create at index 999 = base*1000 + 999", opType: OperationCreate, basePriority: 30, index: 999, want: 30999},
		// Updates follow the same formula as creates.
		{name: "update at index 0 = base*1000", opType: OperationUpdate, basePriority: 40, index: 0, want: 40000},
		{name: "update at index 5 = base*1000 + 5", opType: OperationUpdate, basePriority: 40, index: 5, want: 40005},
		// Deletes invert: basePriority * 1000 + (999 - index)
		{name: "delete at index 0 = base*1000 + 999", opType: OperationDelete, basePriority: 30, index: 0, want: 30999},
		{name: "delete at index 1 = base*1000 + 998", opType: OperationDelete, basePriority: 30, index: 1, want: 30998},
		{name: "delete at index 999 = base*1000", opType: OperationDelete, basePriority: 30, index: 999, want: 30000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := makeOp(tt.opType, tt.basePriority, tt.index)
			assert.Equal(t, tt.want, op.Priority())
		})
	}

	t.Run("creates of consecutive indexes are strictly increasing", func(t *testing.T) {
		// Pin the invariant the parallel scheduler relies on: across the
		// same base priority, lower-index creates always sort before
		// higher-index creates.
		prev := makeOp(OperationCreate, 50, 0).Priority()
		for i := 1; i < 10; i++ {
			cur := makeOp(OperationCreate, 50, i).Priority()
			assert.Greater(t, cur, prev, "create priorities must be strictly increasing in index")
			prev = cur
		}
	})

	t.Run("deletes of consecutive indexes are strictly DEcreasing", func(t *testing.T) {
		// Pin the inverted invariant for deletes: higher-index deletes
		// must sort before lower-index ones to prevent index shifts on the
		// API side from invalidating later delete targets.
		prev := makeOp(OperationDelete, 50, 0).Priority()
		for i := 1; i < 10; i++ {
			cur := makeOp(OperationDelete, 50, i).Priority()
			assert.Less(t, cur, prev, "delete priorities must be strictly decreasing in index")
			prev = cur
		}
	})

	t.Run("different base priorities never interleave even after index adjustment", func(t *testing.T) {
		// With PriorityMultiplier=1000 and indexes capped at 999, the
		// highest priority at base=30 (30999) is always less than the
		// lowest priority at base=31 (31000). Pin that boundary.
		highAt30 := makeOp(OperationCreate, 30, 999).Priority()
		lowAt31 := makeOp(OperationCreate, 31, 0).Priority()
		assert.Less(t, highAt30, lowAt31, "max index of base N must be less than min index of base N+1")
	})
}

// transformForExecute is the Execute helper every TopLevel/IndexChild op
// funnels through. Pin every branch:
//   - Delete returns TAPI's zero value with no error and never invokes the
//     transformer (deletes don't carry a payload).
//   - Create/Update with a non-zero transformed model returns it unchanged.
//   - Create/Update with a transform that produces TAPI's zero value
//     surfaces a wrapped error — that's how callers detect a failed
//     parser→API conversion.
func TestTransformForExecute(t *testing.T) {
	t.Run("delete returns zero value without invoking transformFn", func(t *testing.T) {
		called := false
		got, err := transformForExecute(OperationDelete, "backend", "model", func(string) *string {
			called = true
			s := "x"
			return &s
		})

		assert.NoError(t, err)
		assert.Nil(t, got, "delete must return TAPI's zero value (nil for *string)")
		assert.False(t, called, "delete must NOT invoke transformFn")
	})

	t.Run("create with non-zero transform returns the API model verbatim", func(t *testing.T) {
		s := "transformed"
		got, err := transformForExecute(OperationCreate, "backend", "model", func(string) *string {
			return &s
		})

		assert.NoError(t, err)
		assert.Equal(t, &s, got)
	})

	t.Run("update with non-zero transform returns the API model verbatim", func(t *testing.T) {
		s := "transformed"
		got, err := transformForExecute(OperationUpdate, "backend", "model", func(string) *string {
			return &s
		})

		assert.NoError(t, err)
		assert.Equal(t, &s, got)
	})

	t.Run("create with zero-value transform surfaces an error", func(t *testing.T) {
		got, err := transformForExecute(OperationCreate, "backend", "model", func(string) *string {
			return nil // zero value for *string
		})

		assert.Nil(t, got)
		if assert.Error(t, err) {
			// Error message must mention the section name so callers can
			// pinpoint which transform failed.
			assert.Contains(t, err.Error(), "backend")
		}
	})
}
