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
)

// allInserts and allDeletes are the empty-side fast paths used by
// diffIndexedRules when one of the input slices is empty. They're tested
// indirectly through the public diff tests, but a direct test pins the
// exact diffEntry shape (Op, OldIndex/NewIndex sentinel = -1, Value
// passthrough) that buildEditScript and downstream callers rely on.

func TestAllInserts(t *testing.T) {
	tests := []struct {
		name string
		dst  []string
		want []diffEntry[string]
	}{
		{
			name: "empty input yields empty (non-nil) slice",
			dst:  []string{},
			want: []diffEntry[string]{},
		},
		{
			name: "single element marks Op=Insert with NewIndex=0 and OldIndex=-1",
			dst:  []string{"a"},
			want: []diffEntry[string]{
				{Op: diffInsert, OldIndex: -1, NewIndex: 0, Value: "a"},
			},
		},
		{
			name: "multiple elements get sequential NewIndex and shared OldIndex=-1",
			dst:  []string{"a", "b", "c"},
			want: []diffEntry[string]{
				{Op: diffInsert, OldIndex: -1, NewIndex: 0, Value: "a"},
				{Op: diffInsert, OldIndex: -1, NewIndex: 1, Value: "b"},
				{Op: diffInsert, OldIndex: -1, NewIndex: 2, Value: "c"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allInserts(tt.dst)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAllDeletes(t *testing.T) {
	tests := []struct {
		name string
		src  []int
		want []diffEntry[int]
	}{
		{
			name: "empty input yields empty (non-nil) slice",
			src:  []int{},
			want: []diffEntry[int]{},
		},
		{
			name: "single element marks Op=Delete with OldIndex=0 and NewIndex=-1",
			src:  []int{42},
			want: []diffEntry[int]{
				{Op: diffDelete, OldIndex: 0, NewIndex: -1, Value: 42},
			},
		},
		{
			name: "multiple elements get sequential OldIndex and shared NewIndex=-1",
			src:  []int{10, 20, 30},
			want: []diffEntry[int]{
				{Op: diffDelete, OldIndex: 0, NewIndex: -1, Value: 10},
				{Op: diffDelete, OldIndex: 1, NewIndex: -1, Value: 20},
				{Op: diffDelete, OldIndex: 2, NewIndex: -1, Value: 30},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allDeletes(tt.src)
			assert.Equal(t, tt.want, got)
		})
	}
}

// diffIndexedRules wires allInserts/allDeletes for the empty-side cases.
// Pin that wiring so a future refactor can't accidentally swap them or
// drop the empty-empty short-circuit.
func TestDiffIndexedRules_EmptyShortCircuits(t *testing.T) {
	eq := func(a, b string) bool { return a == b }

	t.Run("empty src + empty dst returns nil", func(t *testing.T) {
		got := diffIndexedRules[string](nil, nil, eq)
		assert.Nil(t, got, "both empty must short-circuit to nil")
	})

	t.Run("empty src + non-empty dst delegates to allInserts", func(t *testing.T) {
		got := diffIndexedRules[string](nil, []string{"a", "b"}, eq)
		assert.Equal(t, []diffEntry[string]{
			{Op: diffInsert, OldIndex: -1, NewIndex: 0, Value: "a"},
			{Op: diffInsert, OldIndex: -1, NewIndex: 1, Value: "b"},
		}, got)
	})

	t.Run("non-empty src + empty dst delegates to allDeletes", func(t *testing.T) {
		got := diffIndexedRules[string]([]string{"x", "y"}, nil, eq)
		assert.Equal(t, []diffEntry[string]{
			{Op: diffDelete, OldIndex: 0, NewIndex: -1, Value: "x"},
			{Op: diffDelete, OldIndex: 1, NewIndex: -1, Value: "y"},
		}, got)
	})
}
