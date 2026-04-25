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

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
)

// indexACLsByName builds the lookup that compareACLs uses to detect
// added/deleted/modified ACLs by name. The "skip nameless ACLs" rule is
// load-bearing — without it the index would alias every nameless entry
// onto a single empty-string key and break the diff.
func TestIndexACLsByName(t *testing.T) {
	tests := []struct {
		name string
		acls models.Acls
		want map[string]int
	}{
		{
			name: "empty input yields empty index",
			acls: models.Acls{},
			want: map[string]int{},
		},
		{
			name: "single named ACL maps to index 0",
			acls: models.Acls{
				{ACLName: "is_api", Criterion: "path_beg", Value: "/api"},
			},
			want: map[string]int{"is_api": 0},
		},
		{
			name: "multiple named ACLs preserve their slice positions",
			acls: models.Acls{
				{ACLName: "first"},
				{ACLName: "second"},
				{ACLName: "third"},
			},
			want: map[string]int{"first": 0, "second": 1, "third": 2},
		},
		{
			name: "nameless ACLs are skipped (the 'can't address them via API anyway' rule)",
			acls: models.Acls{
				{ACLName: ""},
				{ACLName: "named"},
				{ACLName: ""},
			},
			want: map[string]int{"named": 1},
		},
		{
			name: "duplicate names keep the LAST seen index (map overwrite semantics)",
			acls: models.Acls{
				{ACLName: "dup"},
				{ACLName: "other"},
				{ACLName: "dup"},
			},
			want: map[string]int{"dup": 2, "other": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := indexACLsByName(tt.acls)
			assert.Equal(t, tt.want, got)
		})
	}
}

// compareEditedItems wires diffIndexedRules + collapseEdits to the per-section
// (create, remove, update) op factories. Pin every dispatch path:
// - editInsert -> create(new, NewIndex)
// - editDelete -> remove(old, OldIndex)
// - editUpdate -> update(new, OldIndex)  ← position of the OLD item
// The OldIndex on update is load-bearing (matches what the DataPlane API
// expects when replacing in place), so a future refactor can't silently
// switch to NewIndex without breaking ordered rule lists.
func TestCompareEditedItems(t *testing.T) {
	type call struct {
		kind  string // "create" | "remove" | "update"
		value string
		index int
	}

	tests := []struct {
		name    string
		current []string
		desired []string
		// Expected dispatch order. Each entry encodes the factory name, the
		// value passed to it, and the index passed to it.
		want []call
	}{
		{
			name:    "no diff produces no operations",
			current: []string{"a", "b"},
			desired: []string{"a", "b"},
			want:    nil,
		},
		{
			name:    "pure insert dispatches create with NewIndex",
			current: nil,
			desired: []string{"x", "y"},
			want: []call{
				{kind: "create", value: "x", index: 0},
				{kind: "create", value: "y", index: 1},
			},
		},
		{
			name:    "pure delete dispatches remove with OldIndex",
			current: []string{"x", "y"},
			desired: nil,
			want: []call{
				{kind: "remove", value: "x", index: 0},
				{kind: "remove", value: "y", index: 1},
			},
		},
		{
			name:    "in-place replacement collapses to update at OLD index",
			current: []string{"a", "old", "c"},
			desired: []string{"a", "new", "c"},
			want: []call{
				{kind: "update", value: "new", index: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			rec := func(kind string) func(string, int) Operation {
				return func(v string, i int) Operation {
					calls = append(calls, call{kind: kind, value: v, index: i})
					return &mockOperation{desc: kind + ":" + v, priority: i, section: "test"}
				}
			}

			ops := compareEditedItems(
				tt.current, tt.desired,
				func(a, b string) bool { return a == b },
				rec("create"),
				rec("remove"),
				rec("update"),
			)

			assert.Equal(t, tt.want, calls, "factory dispatch order/values/indices")
			assert.Len(t, ops, len(tt.want), "number of returned operations matches factory invocations")
		})
	}
}
