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

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// appendOperationsIfNotEmpty is the helper every comparator funnels its
// per-section operations through. Pin the (modified flag, dst slice)
// invariant so a future refactor can't desync them.
func TestAppendOperationsIfNotEmpty(t *testing.T) {
	t.Run("non-empty src appends and flips modified", func(t *testing.T) {
		dst := []Operation{newMockOp(sections.OperationCreate, "backend", 1)}
		src := []Operation{
			newMockOp(sections.OperationUpdate, "frontend", 2),
			newMockOp(sections.OperationDelete, "server", 3),
		}
		modified := false

		appendOperationsIfNotEmpty(&dst, src, &modified)

		assert.True(t, modified, "modified must flip when src is non-empty")
		assert.Len(t, dst, 3, "src ops must be appended to dst")
	})

	t.Run("empty src leaves both unchanged", func(t *testing.T) {
		original := []Operation{newMockOp(sections.OperationCreate, "backend", 1)}
		dst := append([]Operation(nil), original...)
		modified := false

		appendOperationsIfNotEmpty(&dst, nil, &modified)

		assert.False(t, modified, "modified must NOT flip when src is empty")
		assert.Equal(t, original, dst, "dst must be untouched")
	})

	t.Run("empty src does NOT clear an already-true modified flag", func(t *testing.T) {
		dst := []Operation{}
		modified := true

		appendOperationsIfNotEmpty(&dst, nil, &modified)

		assert.True(t, modified, "modified is sticky once set; empty src must not reset it")
	})

	t.Run("nil dst pointer-to-slice still gets the appends", func(t *testing.T) {
		var dst []Operation
		src := []Operation{newMockOp(sections.OperationCreate, "backend", 1)}
		modified := false

		appendOperationsIfNotEmpty(&dst, src, &modified)

		assert.True(t, modified)
		assert.Len(t, dst, 1)
	})
}

// updateSummaryFromOperations bumps the per-type counters on DiffSummary.
// Pin every operation-type branch and the "ignores unrecognized types"
// behaviour so a future enum addition can't silently inflate the wrong
// counter.
func TestUpdateSummaryFromOperations(t *testing.T) {
	tests := []struct {
		name string
		ops  []Operation
		want DiffSummary
	}{
		{
			name: "empty input leaves summary at zero",
			ops:  nil,
			want: DiffSummary{},
		},
		{
			name: "create / update / delete are counted independently",
			ops: []Operation{
				newMockOp(sections.OperationCreate, "backend", 1),
				newMockOp(sections.OperationCreate, "frontend", 1),
				newMockOp(sections.OperationUpdate, "server", 1),
				newMockOp(sections.OperationDelete, "acl", 1),
			},
			want: DiffSummary{
				TotalCreates: 2,
				TotalUpdates: 1,
				TotalDeletes: 1,
			},
		},
		{
			// OperationType is an int (iota); values outside the iota range
			// hit the default switch arm and are silently ignored. Pin that
			// behaviour so a future addition to the enum requires an explicit
			// case here too.
			name: "unknown operation type is silently ignored",
			ops: []Operation{
				newMockOp(sections.OperationType(99), "backend", 1),
				newMockOp(sections.OperationCreate, "frontend", 1),
			},
			want: DiffSummary{TotalCreates: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DiffSummary{}
			updateSummaryFromOperations(&got, tt.ops)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("counters accumulate across calls", func(t *testing.T) {
		summary := DiffSummary{TotalCreates: 5, TotalUpdates: 1}
		updateSummaryFromOperations(&summary, []Operation{
			newMockOp(sections.OperationCreate, "x", 1),
			newMockOp(sections.OperationDelete, "x", 1),
		})
		assert.Equal(t, 6, summary.TotalCreates)
		assert.Equal(t, 1, summary.TotalUpdates)
		assert.Equal(t, 1, summary.TotalDeletes)
	})
}
