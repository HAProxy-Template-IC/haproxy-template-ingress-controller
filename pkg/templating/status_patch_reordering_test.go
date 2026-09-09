// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package templating

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChangedPatchesDetectProjectionReordering(t *testing.T) {
	first := selectionProjection(t, selectionPatch("route", "rendered", "first"))
	last := selectionProjection(t, selectionPatch("route", "rendered", "last"))
	plan, err := NewStatusPatchProjectionPlan().ReplaceEntry("routes", "001", first)
	require.NoError(t, err)
	plan, err = plan.ReplaceEntry("routes", "002", last)
	require.NoError(t, err)
	previous := selectionSnapshot(t, plan)
	reordered, err := plan.ReplaceEntry("routes", "001", last)
	require.NoError(t, err)
	reordered, err = reordered.ReplaceEntry("routes", "002", first)
	require.NoError(t, err)
	current := selectionSnapshot(t, reordered)
	changed, err := current.ChangedPatchesForPhase(previous, "rendered")
	require.NoError(t, err)
	full, err := current.PatchesForPhase("rendered")
	require.NoError(t, err)
	require.Len(t, full, 1)
	assert.Equal(t, "first", full[0].Variants["rendered"]["owner"])
	assert.Equal(t, "last", full[0].SourceTemplate)
	assert.Equal(t, full, changed)
}
