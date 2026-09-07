// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderplan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func requireSnapshotJSONMatchesLegacy(t *testing.T, snapshot *Snapshot) {
	t.Helper()
	legacy, err := snapshot.LegacyCopy()
	require.NoError(t, err)
	want, err := json.Marshal(legacy)
	require.NoError(t, err)
	var got bytes.Buffer
	require.NoError(t, snapshot.WriteJSON(&got))
	require.Equal(t, string(want), got.String())
}

// TestSnapshotJSONMatchesEncodingJSON pins WriteJSON to encoding/json over a
// freshly built snapshot, a shared successor, and a chain of document
// transitions whose map entries are spliced from their predecessors.
func TestSnapshotJSONMatchesEncodingJSON(t *testing.T) {
	authority := NewAuthority()
	first := mustPlanSnapshot(t, authority, snapshotPlanFixture(3), nil)
	requireSnapshotJSONMatchesLegacy(t, first)

	successorPlan := snapshotPlanFixture(4)
	delete(successorPlan.CRTLists, "crt-list-000000")
	successor := mustPlanSnapshot(t, authority, successorPlan, first)
	requireSnapshotJSONMatchesLegacy(t, successor)

	empty := mustPlanSnapshot(t, authority, &Plan{SchemaVersion: SchemaVersion}, nil)
	requireSnapshotJSONMatchesLegacy(t, empty)

	entries := func(count, salt int) []Entry {
		list := make([]Entry, 0, count)
		for i := range count {
			list = append(list, Entry{Key: fmt.Sprintf("host-%d.example.com", i), Value: fmt.Sprintf("be-%d", (i+salt)%7)})
		}
		return list
	}
	basePlan, baseDocument, _ := documentTransitionFixture(t, []string{"one\n", "two\n"})
	basePlan.Maps["maps/host.map"] = Map{Path: "host.map", Ordered: true, Entries: entries(40, 0)}
	basePlan.Maps["maps/path.map"] = Map{Path: "path.map", Entries: entries(5, 1)}
	base, _, err := ReconcileSnapshotWithConfigDocument(authority, nil, basePlan, baseDocument)
	require.NoError(t, err)
	requireSnapshotJSONMatchesLegacy(t, base)

	current := base
	for step := range 6 {
		texts := []string{"one\n", fmt.Sprintf("step %d\n", step)}
		nextPlan, nextDocument, _ := documentTransitionFixture(t, texts)
		nextPlan.Maps["maps/host.map"] = Map{Path: "host.map", Ordered: true, Entries: entries(40+step, step)}
		nextPlan.Maps["maps/path.map"] = Map{Path: "path.map", Entries: entries(5, 1)}
		if step%2 == 1 {
			nextPlan.Maps["maps/extra.map"] = Map{Path: "extra.map", Entries: entries(step, step)}
		}
		next, _, err := ReconcileSnapshotWithConfigDocument(authority, current, nextPlan, nextDocument)
		require.NoError(t, err)
		requireSnapshotJSONMatchesLegacy(t, next)
		current = next
	}
}
