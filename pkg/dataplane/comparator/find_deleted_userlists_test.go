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
)

// findDeletedUserlists is the per-userlist deletion-detection
// helper called from compareUserlists. Coverage was 80% — the
// "userlist deleted" branch was effectively covered via the
// higher-level Compare test, but the table-driven unit tests
// below pin the exact contract so a future refactor of the diff
// emission can't silently:
//
//   - Skip emitting deletes (would leave stale userlists in
//     HAProxy → security regression: revoked credentials still
//     accepted).
//
//   - Over-emit deletes (would delete still-needed userlists →
//     authentication drops to all clients).
//
// The two contracts pinned are an O-equivalence pair around the
// emit/skip decision keyed on map presence in `desired`:
//
//  1. A userlist present in current but absent in desired emits
//     EXACTLY ONE delete operation per userlist, identified by
//     the original *Userlist pointer (preserves Name + AdminUsers
//     for the dataplane API call).
//
//  2. A userlist present in BOTH current and desired emits NO
//     delete (keep it; later phases handle in-place modifications).

func TestFindDeletedUserlists_TableDriven(t *testing.T) {
	ulA := &models.Userlist{UserlistBase: models.UserlistBase{Name: "list-a"}}
	ulB := &models.Userlist{UserlistBase: models.UserlistBase{Name: "list-b"}}
	ulC := &models.Userlist{UserlistBase: models.UserlistBase{Name: "list-c"}}

	tests := []struct {
		name        string
		current     map[string]*models.Userlist
		desired     map[string]*models.Userlist
		wantDeleted []string // names of userlists expected in delete operations
	}{
		{
			name:        "empty current → no deletes",
			current:     map[string]*models.Userlist{},
			desired:     map[string]*models.Userlist{"list-a": ulA},
			wantDeleted: nil,
		},
		{
			name:        "all current present in desired → no deletes",
			current:     map[string]*models.Userlist{"list-a": ulA, "list-b": ulB},
			desired:     map[string]*models.Userlist{"list-a": ulA, "list-b": ulB, "list-c": ulC},
			wantDeleted: nil,
		},
		{
			name:        "single current absent from desired → one delete",
			current:     map[string]*models.Userlist{"list-a": ulA},
			desired:     map[string]*models.Userlist{},
			wantDeleted: []string{"list-a"},
		},
		{
			name:        "mixed: keep some, delete others",
			current:     map[string]*models.Userlist{"list-a": ulA, "list-b": ulB, "list-c": ulC},
			desired:     map[string]*models.Userlist{"list-b": ulB},
			wantDeleted: []string{"list-a", "list-c"},
		},
		{
			name:        "all current absent from desired → all deleted",
			current:     map[string]*models.Userlist{"list-a": ulA, "list-b": ulB},
			desired:     map[string]*models.Userlist{},
			wantDeleted: []string{"list-a", "list-b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := findDeletedUserlists(tt.current, tt.desired)

			gotNames := extractDeletedNames(t, ops)

			for _, op := range ops {
				assert.Equal(t, sections.OperationDelete, op.Type(),
					"every operation MUST have Type=Delete — a regression that "+
						"emitted Create or Update here would silently route the "+
						"call to the wrong dataplane API endpoint")
			}

			// Use ElementsMatch because map iteration order is
			// non-deterministic — the contract is the SET of
			// deleted userlists, not the order.
			assert.ElementsMatch(t, tt.wantDeleted, gotNames,
				"deleted userlist set MUST exactly match — under-deletion leaves "+
					"stale userlists in HAProxy (revoked credentials still accepted), "+
					"over-deletion drops authentication for still-active users")
		})
	}
}

// extractDeletedNames pulls userlist names out of the Describe()
// strings emitted by NewUserlistDelete. The format is documented
// by sections.DescribeTopLevel as: "<Verb> <Section> '<Name>'".
// We match the single-quoted name. Keeps the test independent of
// the concrete delete-op struct type (which is generic and
// unexported through the factory API).
func extractDeletedNames(t *testing.T, ops []sections.Operation) []string {
	t.Helper()
	names := make([]string, 0, len(ops))
	for _, op := range ops {
		desc := op.Describe()
		first := -1
		last := -1
		for i, b := range desc {
			if b == '\'' {
				if first == -1 {
					first = i
				} else {
					last = i
				}
			}
		}
		require.True(t, first >= 0 && last > first,
			"Describe output %q must contain a single-quoted name (DescribeTopLevel format)", desc)
		names = append(names, desc[first+1:last])
	}
	return names
}
