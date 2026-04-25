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
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// compareQUICInitialRules and compareAcmeProviders are version-gated
// section comparators (v3.1+ and v3.2+ respectively) that had NO
// direct test coverage. Both delegate to the shared diff helpers
// (compareIndexedItems / compareNamedSections) but each owns one
// load-bearing piece of glue:
//
//   - compareQUICInitialRules: the parentType branch that swaps
//     between Frontend and Defaults factories. Wiring the wrong
//     factory into one branch would silently route every QUIC rule
//     through the wrong API endpoint — visible only at deploy time
//     against a real DataPlane API.
//
//   - compareAcmeProviders: the equality function and name extractor
//     wired into compareNamedSections. A regression that swapped
//     create/delete or used the wrong Equal() method would silently
//     emit destructive operations against ACME accounts (cert
//     renewal disruption).
//
// Pin both with the same shape used by the existing compareLogProfiles
// and compareLogForwards tests in compare_config_test.go.

func TestCompareQUICInitialRules(t *testing.T) {
	comp := New()

	// Reusable small rules. Equal() compares Cond + CondTest + Type.
	ruleAccept := &models.QUICInitialRule{Type: "accept"}
	ruleReject := &models.QUICInitialRule{Type: "reject"}

	tests := []struct {
		name       string
		parentType string
		parentName string
		current    models.QUICInitialRules
		desired    models.QUICInitialRules
		wantTyp    sections.OperationType
		wantOps    int
	}{
		{
			name:       "frontend: add rule emits create",
			parentType: parentTypeFrontend,
			parentName: "https",
			current:    nil,
			desired:    models.QUICInitialRules{ruleAccept},
			wantTyp:    sections.OperationCreate,
			wantOps:    1,
		},
		{
			name:       "frontend: delete rule",
			parentType: parentTypeFrontend,
			parentName: "https",
			current:    models.QUICInitialRules{ruleAccept},
			desired:    nil,
			wantTyp:    sections.OperationDelete,
			wantOps:    1,
		},
		{
			name:       "frontend: change rule type at same index emits update",
			parentType: parentTypeFrontend,
			parentName: "https",
			current:    models.QUICInitialRules{ruleAccept},
			desired:    models.QUICInitialRules{ruleReject},
			wantTyp:    sections.OperationUpdate,
			wantOps:    1,
		},
		{
			name:       "defaults: add rule emits create (different parent type, different factory)",
			parentType: parentTypeDefaults,
			parentName: "common",
			current:    nil,
			desired:    models.QUICInitialRules{ruleAccept},
			wantTyp:    sections.OperationCreate,
			wantOps:    1,
		},
		{
			name:       "defaults: delete rule (verifies the parentType branch for delete factory too)",
			parentType: parentTypeDefaults,
			parentName: "common",
			current:    models.QUICInitialRules{ruleAccept},
			desired:    nil,
			wantTyp:    sections.OperationDelete,
			wantOps:    1,
		},
		{
			name:       "no changes when both empty",
			parentType: parentTypeFrontend,
			parentName: "https",
			current:    nil,
			desired:    nil,
			wantOps:    0,
		},
		{
			name:       "no changes when identical",
			parentType: parentTypeFrontend,
			parentName: "https",
			current:    models.QUICInitialRules{ruleAccept},
			desired:    models.QUICInitialRules{ruleAccept},
			wantOps:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := comp.compareQUICInitialRules(tt.parentType, tt.parentName, tt.current, tt.desired)
			require.Len(t, ops, tt.wantOps,
				"compareQUICInitialRules op count must match (parent=%s)", tt.parentType)
			if tt.wantOps > 0 {
				assert.Equal(t, tt.wantTyp, ops[0].Type(),
					"compareQUICInitialRules must wire the matching CRUD factory for each transition; "+
						"a wrong-factory regression would silently route rules through the wrong API endpoint")
			}
		})
	}
}

func TestCompareAcmeProviders(t *testing.T) {
	comp := New()

	tests := []struct {
		name    string
		current []*models.AcmeProvider
		desired []*models.AcmeProvider
		wantTyp sections.OperationType
		wantOps int
	}{
		{
			name:    "add provider",
			current: nil,
			desired: []*models.AcmeProvider{{Name: "letsencrypt", Directory: "https://acme-v02.example.com"}},
			wantTyp: sections.OperationCreate,
			wantOps: 1,
		},
		{
			name:    "delete provider",
			current: []*models.AcmeProvider{{Name: "letsencrypt", Directory: "https://acme-v02.example.com"}},
			desired: nil,
			wantTyp: sections.OperationDelete,
			wantOps: 1,
		},
		{
			name: "update provider (same name, different directory)",
			current: []*models.AcmeProvider{
				{Name: "letsencrypt", Directory: "https://acme-staging-v02.example.com"},
			},
			desired: []*models.AcmeProvider{
				{Name: "letsencrypt", Directory: "https://acme-v02.example.com"},
			},
			wantTyp: sections.OperationUpdate,
			wantOps: 1,
		},
		{
			name:    "no changes when both empty",
			current: nil,
			desired: nil,
			wantOps: 0,
		},
		{
			name: "no changes when identical",
			current: []*models.AcmeProvider{
				{Name: "letsencrypt", Directory: "https://acme-v02.example.com"},
			},
			desired: []*models.AcmeProvider{
				{Name: "letsencrypt", Directory: "https://acme-v02.example.com"},
			},
			wantOps: 0,
		},
		{
			name: "rename treated as delete + create (name is the identity, not a content field)",
			current: []*models.AcmeProvider{
				{Name: "letsencrypt", Directory: "https://acme-v02.example.com"},
			},
			desired: []*models.AcmeProvider{
				{Name: "buypass", Directory: "https://acme-v02.example.com"},
			},
			// Two ops: one delete (old name), one create (new name).
			// The exact order isn't asserted because named-section
			// comparators iterate maps with non-deterministic order.
			wantOps: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := comp.compareAcmeProviders(
				&parser.StructuredConfig{AcmeProviders: tt.current},
				&parser.StructuredConfig{AcmeProviders: tt.desired},
			)
			require.Len(t, ops, tt.wantOps)
			if tt.wantOps == 1 {
				assert.Equal(t, tt.wantTyp, ops[0].Type(),
					"compareAcmeProviders must wire the matching CRUD factory; "+
						"a swap of create/delete would emit destructive ops against ACME accounts (cert renewal disruption)")
			}
			if tt.wantOps == 2 {
				// Rename case: must contain exactly one create and one delete.
				kinds := map[sections.OperationType]int{}
				for _, op := range ops {
					kinds[op.Type()]++
				}
				assert.Equal(t, 1, kinds[sections.OperationCreate],
					"rename must produce exactly one create for the new name")
				assert.Equal(t, 1, kinds[sections.OperationDelete],
					"rename must produce exactly one delete for the old name")
				assert.Zero(t, kinds[sections.OperationUpdate],
					"rename must NOT emit an update — name is the identity, not a content field")
			}
		})
	}
}
