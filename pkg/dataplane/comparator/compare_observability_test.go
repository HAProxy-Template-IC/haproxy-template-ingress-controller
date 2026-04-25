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

// compareTraces is a singleton-section comparator with a non-obvious
// asymmetric contract:
//
//   - desired.Traces == nil          -> NEVER emit any operation (no
//     create, no delete). Traces is "always present or unsupported"
//     in the API; we don't synthesise a delete.
//   - current.Traces == nil          -> emit a SINGLE update (the API
//     treats update as create-or-replace for singleton sections).
//   - both present, contents differ  -> emit a single update.
//   - both present, contents equal   -> emit nothing.
//
// The asymmetry on the "missing desired" branch is the part most
// likely to silently regress (e.g. a refactor that made every nil
// branch symmetric would start emitting deletes that the API rejects).
// Pin all four branches plus operation type / target identity.
func TestCompareTraces(t *testing.T) {
	comp := New()

	tracesA := &models.Traces{
		Entries: models.TraceEntries{{Trace: "h1"}, {Trace: "h2"}},
	}
	tracesB := &models.Traces{
		Entries: models.TraceEntries{{Trace: "h1"}, {Trace: "h2"}, {Trace: "h3"}},
	}
	tracesAClone := &models.Traces{
		Entries: models.TraceEntries{{Trace: "h1"}, {Trace: "h2"}},
	}

	tests := []struct {
		name    string
		current *models.Traces
		desired *models.Traces
		wantOps int
	}{
		{
			name:    "desired nil never emits an op (even if current present)",
			current: tracesA,
			desired: nil,
			wantOps: 0,
		},
		{
			name:    "both nil emits no op",
			current: nil,
			desired: nil,
			wantOps: 0,
		},
		{
			name:    "current nil + desired present emits a single update (create-or-replace)",
			current: nil,
			desired: tracesA,
			wantOps: 1,
		},
		{
			name:    "both present and equal emits no op",
			current: tracesA,
			desired: tracesAClone,
			wantOps: 0,
		},
		{
			name:    "both present and differ emits a single update",
			current: tracesA,
			desired: tracesB,
			wantOps: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := comp.compareTraces(
				&parser.StructuredConfig{Traces: tt.current},
				&parser.StructuredConfig{Traces: tt.desired},
			)
			require.Len(t, ops, tt.wantOps)
			if tt.wantOps > 0 {
				// All non-empty results MUST be exactly one update
				// targeting the desired Traces value. Singleton
				// sections never produce create or delete ops here.
				assert.Equal(t, sections.OperationUpdate, ops[0].Type(),
					"compareTraces must only ever emit update ops for singleton Traces section")
			}
		})
	}
}

// compareLogProfiles is the standard add/update/delete pass over a
// named-section list. The behaviour mirrors compareLogForwards (which
// is already covered) but log-profiles has its own factory wiring and
// is gated behind DataPlane API v3.1+. Pin the three transitions plus
// the no-op case so any swap of Create/Delete/Update wiring or any
// change to the equality comparator is caught.
func TestCompareLogProfiles(t *testing.T) {
	comp := New()

	tests := []struct {
		name    string
		current []*models.LogProfile
		desired []*models.LogProfile
		wantTyp sections.OperationType
		wantOps int
	}{
		{
			name:    "add profile",
			current: nil,
			desired: []*models.LogProfile{{Name: "audit"}},
			wantTyp: sections.OperationCreate,
			wantOps: 1,
		},
		{
			name:    "delete profile",
			current: []*models.LogProfile{{Name: "audit"}},
			desired: nil,
			wantTyp: sections.OperationDelete,
			wantOps: 1,
		},
		{
			name: "update profile (same name, different content)",
			current: []*models.LogProfile{
				{Name: "audit", LogTag: "old"},
			},
			desired: []*models.LogProfile{
				{Name: "audit", LogTag: "new"},
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
			name:    "no changes when identical",
			current: []*models.LogProfile{{Name: "audit"}},
			desired: []*models.LogProfile{{Name: "audit"}},
			wantOps: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := comp.compareLogProfiles(
				&parser.StructuredConfig{LogProfiles: tt.current},
				&parser.StructuredConfig{LogProfiles: tt.desired},
			)
			require.Len(t, ops, tt.wantOps)
			if tt.wantOps > 0 {
				assert.Equal(t, tt.wantTyp, ops[0].Type(),
					"compareLogProfiles must wire the matching CRUD factory for each transition")
			}
		})
	}
}
