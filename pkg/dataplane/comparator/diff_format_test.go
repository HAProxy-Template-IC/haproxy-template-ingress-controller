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

// formatNamedChanges is the shared "added/modified/deleted: a, b, c" line
// builder behind every formatFrontendChanges / formatBackendChanges output.
// Pin the per-branch contract (empty slices skip the line; non-empty slices
// produce the exact "- <Label> <kind>: <comma list>" string) so DiffSummary
// strings stay stable for log scrapers.
func TestFormatNamedChanges(t *testing.T) {
	tests := []struct {
		name     string
		label    string
		added    []string
		modified []string
		deleted  []string
		want     []string
	}{
		{
			name:  "all empty produces no lines",
			label: "Frontends",
			want:  nil,
		},
		{
			name:  "only added",
			label: "Frontends",
			added: []string{"f1"},
			want: []string{
				"- Frontends added: f1",
			},
		},
		{
			name:     "only modified",
			label:    "Backends",
			modified: []string{"b1", "b2"},
			want: []string{
				"- Backends modified: b1, b2",
			},
		},
		{
			name:    "only deleted",
			label:   "Backends",
			deleted: []string{"b1"},
			want: []string{
				"- Backends deleted: b1",
			},
		},
		{
			name:     "added + modified + deleted preserves order",
			label:    "Backends",
			added:    []string{"new"},
			modified: []string{"m1", "m2"},
			deleted:  []string{"old"},
			want: []string{
				"- Backends added: new",
				"- Backends modified: m1, m2",
				"- Backends deleted: old",
			},
		},
		{
			name:    "added + deleted (no modified) skips the modified line",
			label:   "Frontends",
			added:   []string{"a"},
			deleted: []string{"d"},
			want: []string{
				"- Frontends added: a",
				"- Frontends deleted: d",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatNamedChanges(tt.label, tt.added, tt.modified, tt.deleted)
			assert.Equal(t, tt.want, got)
		})
	}
}

// formatBackendDiffFields groups modified backends by their *set* of differing
// fields to keep DiffSummary output compact for large repeated diffs (e.g.
// "every backend differs in [GUID]"). The grouping uses sorted field names
// as the key, and the output is sorted alphabetically for deterministic logs.
func TestFormatBackendDiffFields(t *testing.T) {
	t.Run("nil/empty map yields nil", func(t *testing.T) {
		s := &DiffSummary{}
		assert.Nil(t, s.formatBackendDiffFields())

		s.BackendDiffFields = map[string][]string{}
		assert.Nil(t, s.formatBackendDiffFields())
	})

	t.Run("backends with the same field-set are grouped into one line", func(t *testing.T) {
		s := &DiffSummary{
			BackendDiffFields: map[string][]string{
				"b1": {"GUID"},
				"b2": {"GUID"},
				"b3": {"GUID"},
			},
		}
		got := s.formatBackendDiffFields()
		assert.Equal(t, []string{
			"- Backend diff fields: [GUID] (3 backends)",
		}, got)
	})

	t.Run("differing field-sets produce separate lines, sorted alphabetically", func(t *testing.T) {
		s := &DiffSummary{
			BackendDiffFields: map[string][]string{
				"b1": {"GUID"},
				"b2": {"Mode", "GUID"}, // unsorted input
				"b3": {"GUID", "Mode"}, // same set, different order — must group with b2
				"b4": {"Balance"},
			},
		}
		got := s.formatBackendDiffFields()
		// Sorted alphabetically by the formatted line. Keys after sorting fields:
		//   "Balance"  -> 1 backend
		//   "GUID"     -> 1 backend (b1)
		//   "GUID, Mode" -> 2 backends (b2 + b3)
		assert.Equal(t, []string{
			"- Backend diff fields: [Balance] (1 backends)",
			"- Backend diff fields: [GUID, Mode] (2 backends)",
			"- Backend diff fields: [GUID] (1 backends)",
		}, got)
	})
}

// formatServerMapChanges emits one line per change-kind with backends sorted
// alphabetically and "<backend>: <count>" pairs. The count is derived from the
// length of the per-backend server slice, NOT from the slice contents — pin
// that so a future refactor can't accidentally start emitting server names.
func TestFormatServerMapChanges(t *testing.T) {
	s := &DiffSummary{}

	tests := []struct {
		name       string
		changes    map[string][]string
		changeType string
		want       string
	}{
		{
			name:       "empty map yields header with no entries",
			changes:    map[string][]string{},
			changeType: "added",
			want:       "- Servers added: ",
		},
		{
			name: "single backend with three servers",
			changes: map[string][]string{
				"backend-a": {"s1", "s2", "s3"},
			},
			changeType: "modified",
			want:       "- Servers modified: backend-a: 3",
		},
		{
			name: "multiple backends sorted alphabetically",
			changes: map[string][]string{
				"zebra":  {"s1"},
				"alpha":  {"s1", "s2"},
				"middle": {"s1", "s2", "s3"},
			},
			changeType: "deleted",
			want:       "- Servers deleted: alpha: 2, middle: 3, zebra: 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.formatServerMapChanges(tt.changes, tt.changeType)
			assert.Equal(t, tt.want, got)
		})
	}
}

// formatOtherChanges builds the "Other changes: section: count, ..." line
// with sections sorted alphabetically. Pin the empty-skip and sort behaviour.
func TestFormatOtherChanges(t *testing.T) {
	t.Run("nil/empty map yields no lines", func(t *testing.T) {
		s := &DiffSummary{}
		assert.Nil(t, s.formatOtherChanges())

		s.OtherChanges = map[string]int{}
		assert.Nil(t, s.formatOtherChanges())
	})

	t.Run("entries sorted alphabetically and joined", func(t *testing.T) {
		s := &DiffSummary{
			OtherChanges: map[string]int{
				"acme":     2,
				"resolver": 1,
				"cache":    3,
			},
		}
		got := s.formatOtherChanges()
		assert.Equal(t, []string{"- Other changes: acme: 2, cache: 3, resolver: 1"}, got)
	})
}
