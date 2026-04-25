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

// opPreposition returns "from" for delete and "in" for everything else. The
// describer functions interpolate the result directly into the user-facing
// operation description, so the contract is observable through every
// Describe* helper. Pin it directly so a future refactor can't quietly
// invert the mapping.
func TestOpPreposition(t *testing.T) {
	tests := []struct {
		name string
		op   OperationType
		want string
	}{
		{name: "create uses 'in'", op: OperationCreate, want: "in"},
		{name: "update uses 'in'", op: OperationUpdate, want: "in"},
		{name: "delete uses 'from'", op: OperationDelete, want: "from"},
		{name: "unknown op falls back to 'in'", op: OperationType(99), want: "in"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, opPreposition(tt.op))
		})
	}
}

// DescribeTopLevel formats single-section operations like
// "Create backend 'api'" or "Delete frontend 'http'". Pin the format
// for every op type so log scrapers stay stable.
func TestDescribeTopLevel(t *testing.T) {
	tests := []struct {
		name string
		op   OperationType
		want string
	}{
		{name: "create", op: OperationCreate, want: "Create backend 'api'"},
		{name: "update", op: OperationUpdate, want: "Update backend 'api'"},
		{name: "delete", op: OperationDelete, want: "Delete backend 'api'"},
		{name: "unknown op falls back to 'Process'", op: OperationType(99), want: "Process backend 'api'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DescribeTopLevel(tt.op, "backend", "api")())
		})
	}
}

// DescribeACL is a thin wrapper that includes the ACL name and uses the
// preposition to differentiate adds (in frontend) from removes (from
// frontend). Pin both branches.
func TestDescribeACL(t *testing.T) {
	tests := []struct {
		name string
		op   OperationType
		want string
	}{
		{name: "create uses 'in'", op: OperationCreate, want: "Create ACL 'is_api' in frontend 'http'"},
		{name: "update uses 'in'", op: OperationUpdate, want: "Update ACL 'is_api' in frontend 'http'"},
		{name: "delete uses 'from'", op: OperationDelete, want: "Delete ACL 'is_api' from frontend 'http'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DescribeACL(tt.op, "is_api", "frontend", "http")()
			assert.Equal(t, tt.want, got)
		})
	}
}

// DescribeNamedChild is shared by named-child and container-child operations.
// Pin the exact format and the per-op preposition so a future refactor can't
// silently change the user-facing string.
func TestDescribeNamedChild(t *testing.T) {
	tests := []struct {
		name string
		op   OperationType
		want string
	}{
		{name: "create uses 'in'", op: OperationCreate, want: "Create bind 'lb1' in frontend 'http'"},
		{name: "update uses 'in'", op: OperationUpdate, want: "Update bind 'lb1' in frontend 'http'"},
		{name: "delete uses 'from'", op: OperationDelete, want: "Delete bind 'lb1' from frontend 'http'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DescribeNamedChild(tt.op, "bind", "lb1", "frontend", "http")()
			assert.Equal(t, tt.want, got)
		})
	}
}

// DescribeTypedChild prefers a parenthesized identifier when present, else
// falls back to the supplied label. Pin both branches and the preposition
// flip on delete.
func TestDescribeTypedChild(t *testing.T) {
	tests := []struct {
		name       string
		op         OperationType
		identifier string
		fallback   string
		want       string
	}{
		{
			name:       "non-empty identifier wraps in parens and uses 'in' for create",
			op:         OperationCreate,
			identifier: "request",
			fallback:   "at index 0",
			want:       "Create http-rule (request) in backend 'api'",
		},
		{
			name:       "empty identifier falls back to the supplied label",
			op:         OperationUpdate,
			identifier: "",
			fallback:   "at index 3",
			want:       "Update http-rule at index 3 in backend 'api'",
		},
		{
			name:       "delete uses 'from' regardless of identifier presence",
			op:         OperationDelete,
			identifier: "request",
			fallback:   "at index 0",
			want:       "Delete http-rule (request) from backend 'api'",
		},
		{
			name:       "delete with empty identifier still uses 'from' and fallback",
			op:         OperationDelete,
			identifier: "",
			fallback:   "at index 0",
			want:       "Delete http-rule at index 0 from backend 'api'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DescribeTypedChild(tt.op, "http-rule", tt.identifier, tt.fallback, "backend", "api")()
			assert.Equal(t, tt.want, got)
		})
	}
}
