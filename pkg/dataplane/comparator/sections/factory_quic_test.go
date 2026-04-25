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
)

// QUIC initial rule factories (HAProxy DataPlane API v3.1+) are
// index-based child operations attached to either a frontend or a
// defaults section. They share an identical CRUD shape, so a single
// table-driven test pins type/section/effective-priority/description
// for every parent + op combination. Without this, a future refactor
// that swapped the underlying CRUD builder or rewired the parent type
// could change the section name or the description format silently.

func TestQUICInitialRuleFactoryFunctions(t *testing.T) {
	rule := &models.QUICInitialRule{}

	tests := []struct {
		name             string
		factory          func(string, *models.QUICInitialRule, int) Operation
		parentName       string
		index            int
		wantType         OperationType
		wantDescContains string
	}{
		// Frontend variants
		{
			name:             "NewQUICInitialRuleFrontendCreate",
			factory:          NewQUICInitialRuleFrontendCreate,
			parentName:       "https",
			index:            0,
			wantType:         OperationCreate,
			wantDescContains: "Create quic-initial-rule at index 0 in frontend 'https'",
		},
		{
			name:             "NewQUICInitialRuleFrontendUpdate",
			factory:          NewQUICInitialRuleFrontendUpdate,
			parentName:       "https",
			index:            2,
			wantType:         OperationUpdate,
			wantDescContains: "Update quic-initial-rule at index 2 in frontend 'https'",
		},
		{
			name:       "NewQUICInitialRuleFrontendDelete",
			factory:    NewQUICInitialRuleFrontendDelete,
			parentName: "https",
			index:      1,
			wantType:   OperationDelete,
			// Delete uses 'from' instead of 'in' (opPreposition contract).
			wantDescContains: "Delete quic-initial-rule at index 1 from frontend 'https'",
		},
		// Defaults variants
		{
			name:             "NewQUICInitialRuleDefaultsCreate",
			factory:          NewQUICInitialRuleDefaultsCreate,
			parentName:       "common",
			index:            0,
			wantType:         OperationCreate,
			wantDescContains: "Create quic-initial-rule at index 0 in defaults 'common'",
		},
		{
			name:             "NewQUICInitialRuleDefaultsUpdate",
			factory:          NewQUICInitialRuleDefaultsUpdate,
			parentName:       "common",
			index:            5,
			wantType:         OperationUpdate,
			wantDescContains: "Update quic-initial-rule at index 5 in defaults 'common'",
		},
		{
			name:             "NewQUICInitialRuleDefaultsDelete",
			factory:          NewQUICInitialRuleDefaultsDelete,
			parentName:       "common",
			index:            3,
			wantType:         OperationDelete,
			wantDescContains: "Delete quic-initial-rule at index 3 from defaults 'common'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(tt.parentName, rule, tt.index)
			// IndexChildOp.Priority adds the index inside the priority bucket
			// (creates/updates) or inverts (deletes). The shared
			// assertOperation helper compares against the effective priority,
			// so build it the same way IndexChildOp does.
			wantPrio := PriorityQUICInitialRule * PriorityMultiplier
			if tt.wantType == OperationDelete {
				wantPrio += 999 - tt.index
			} else {
				wantPrio += tt.index
			}
			assertOperation(t, op, tt.wantType, "quic_initial_rule", wantPrio, tt.wantDescContains)
		})
	}
}
