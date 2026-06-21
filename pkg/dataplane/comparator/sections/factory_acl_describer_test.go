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

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
)

// describeACLOp is a higher-order describer-factory used by
// NewIndexChildCRUDWithDescriber for the aclFrontendOps / aclBackendOps
// CRUD builders. It wraps DescribeACL into the
// (op, model, parentName, index) shape the describer slot expects.
//
// The two captured pieces — the parentType bound at factory time, and
// the model's ACLName at call time — surface in the user-facing
// description string. Pin both pieces so a future refactor can't
// silently lose either.
func TestDescribeACLOp(t *testing.T) {
	tests := []struct {
		name       string
		parentType string
		op         OperationType
		acl        *models.ACL
		parentName string
		want       string
	}{
		{
			name:       "frontend create with ACL name (preserves both captures)",
			parentType: "frontend",
			op:         OperationCreate,
			acl:        &models.ACL{ACLName: "is_api"},
			parentName: "http",
			want:       "Create ACL 'is_api' in frontend 'http'",
		},
		{
			name:       "backend update preserves the bound parentType",
			parentType: "backend",
			op:         OperationUpdate,
			acl:        &models.ACL{ACLName: "is_admin"},
			parentName: "api",
			want:       "Update ACL 'is_admin' in backend 'api'",
		},
		{
			name:       "delete uses 'from' preposition (DescribeACL contract)",
			parentType: "frontend",
			op:         OperationDelete,
			acl:        &models.ACL{ACLName: "is_api"},
			parentName: "http",
			want:       "Delete ACL 'is_api' from frontend 'http'",
		},
		{
			name:       "empty ACL name still renders without panic",
			parentType: "frontend",
			op:         OperationCreate,
			acl:        &models.ACL{ACLName: ""},
			parentName: "http",
			want:       "Create ACL '' in frontend 'http'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			describer := describeACLOp(tt.parentType)
			// The fourth parameter (index) is intentionally ignored by the
			// ACL describer — verify by passing a non-zero value and
			// confirming the output doesn't include it.
			got := describer(tt.op, tt.acl, tt.parentName, 42 /* index ignored */)()
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, "42", "describer must drop the index parameter")
		})
	}
}

// describeQUICInitialRule is the analogous describer-factory for
// quic_initial_rule sections. QUIC initial rules don't carry a model
// identifier, so it passes an empty identifier to DescribeTypedChild and
// relies on the "at index N" fallback — only the index matters.
func TestDescribeQUICInitialRule(t *testing.T) {
	tests := []struct {
		name       string
		parentType string
		op         OperationType
		index      int
		parentName string
		want       string
	}{
		{
			name:       "frontend create at index 0",
			parentType: "frontend",
			op:         OperationCreate,
			index:      0,
			parentName: "https",
			want:       "Create quic-initial-rule at index 0 in frontend 'https'",
		},
		{
			name:       "defaults update at non-zero index",
			parentType: "defaults",
			op:         OperationUpdate,
			index:      3,
			parentName: "common",
			want:       "Update quic-initial-rule at index 3 in defaults 'common'",
		},
		{
			name:       "delete uses 'from' preposition",
			parentType: "frontend",
			op:         OperationDelete,
			index:      1,
			parentName: "https",
			want:       "Delete quic-initial-rule at index 1 from frontend 'https'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			describer := describeQUICInitialRule(tt.parentType)
			// The describer takes a *models.QUICInitialRule pointer that
			// it intentionally ignores — pass nil to confirm.
			got := describer(tt.op, nil /* model intentionally unused */, tt.parentName, tt.index)()
			assert.Equal(t, tt.want, got)
		})
	}
}
