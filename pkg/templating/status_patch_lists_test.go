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

package templating

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatusListOwnershipMerge(t *testing.T) {
	foreign := map[string]any{"writer": "other", "generation": int64(1)}
	current := map[string]any{"state": map[string]any{"entries": []any{
		foreign, map[string]any{"writer": "ours", "generation": int64(1)},
	}}}
	for _, entries := range [][]any{
		{map[string]any{"writer": "ours", "generation": int64(2)}},
		{},
	} {
		payload := map[string]any{"state": map[string]any{"entries": entries}}
		ownership, err := encodeStatusListOwnership(map[string]map[string]any{"deployed": payload}, []map[string]any{
			{"/state/entries": map[string]any{"writer": "ours"}},
		})
		require.NoError(t, err)
		merged, err := MergeStatusLists(payload, current, ownership)
		require.NoError(t, err)
		got, found, err := statusListAt(merged, "/state/entries")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, append(append([]any{}, entries...), foreign), got)
		require.Len(t, current["state"].(map[string]any)["entries"], 2)
		require.Equal(t, entries, payload["state"].(map[string]any)["entries"])
	}
}

func TestStatusListOwnershipValidation(t *testing.T) {
	variants := map[string]map[string]any{"rendered": {"entries": []any{map[string]any{"writer": "ours"}}}}
	for _, options := range []map[string]any{
		{"entries": map[string]any{"writer": "ours"}},
		{"/missing": map[string]any{"writer": "ours"}},
		{"/entries": map[string]any{}},
		{"/entries": map[string]any{"writer": "other"}},
		{"/entries~3": map[string]any{"writer": "ours"}},
		{"/entries": map[string]any{"writer": "ours"}, "/entries/child": map[string]any{"writer": "ours"}},
	} {
		_, err := encodeStatusListOwnership(variants, []map[string]any{options})
		require.Error(t, err)
	}
}

func TestStatusListOwnershipSurvivesProjectionAndSelection(t *testing.T) {
	ownership := `{"/entries":{"writer":"ours"}}`
	patch := StatusPatch{
		Namespace: "default", Name: "sample", APIVersion: "example.test/v1", Kind: "Widget",
		UID: "sample-uid", ResourceVersion: "1", ListOwnership: ownership,
		Variants: map[string]map[string]any{"rendered": {"entries": []any{map[string]any{"writer": "ours"}}}},
	}
	projection, err := NewStatusPatchProjection([]StatusPatch{patch})
	require.NoError(t, err)
	plan, err := NewStatusPatchProjectionPlanFromEntries([]StatusPatchProjectionPlanEntry{{Group: "status", Entry: "sample", Projection: projection}})
	require.NoError(t, err)
	collector := NewStatusPatchCollector()
	replay, err := plan.PrepareReplay()
	require.NoError(t, err)
	require.NoError(t, collector.ReplayProjectionPlan(replay))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)
	patches, err := snapshot.PatchesForPhase("rendered")
	require.NoError(t, err)
	require.Equal(t, []StatusPatch{patch}, patches)
	changed, err := snapshot.ChangedPatchesForPhase(nil, "rendered")
	require.NoError(t, err)
	require.Equal(t, patches, changed)
}

func TestStatusPatchOwnedListsRenderPaths(t *testing.T) {
	source := `{%%
statusPatch(item, map[string]any{
  "deployed": map[string]any{"entries": []any{map[string]any{"writer": "ours", "ready": true}}},
}, map[string]any{"/entries": map[string]any{"writer": "ours"}})
%%}`
	item := incrementalEffectTestItem("sample")
	engine, err := New(map[string]string{"component": source}, nil)
	require.NoError(t, err)
	collector := NewStatusPatchCollector()
	_, err = engine.Render(t.Context(), "component", map[string]any{"item": item, "statusPatchCollector": collector})
	require.NoError(t, err)
	patches, err := collector.Patches()
	require.NoError(t, err)
	require.Len(t, patches, 1)
	require.JSONEq(t, `{"/entries":{"writer":"ours"}}`, patches[0].ListOwnership)
	recorder := &incrementalEffectTestRecorder{}
	ctx := WithIncrementalStatusPatchRecorder(t.Context(), recorder)
	incrementalEngine := newIncrementalEffectTestEngine(t, map[string]string{"component": source})
	_, err = incrementalEngine.RenderIncrementalComponent(ctx, "component", incrementalEffectTestVars(item))
	require.NoError(t, err)
	incremental := recorder.patchSnapshot()
	require.Len(t, incremental, 1)
	require.Equal(t, patches[0].ListOwnership, incremental[0].ListOwnership)
	require.Equal(t, patches[0].Variants, incremental[0].Variants)
}

func TestStatusListOwnershipChangeSelectsPatch(t *testing.T) {
	patch := StatusPatch{
		Namespace: "default", Name: "sample", APIVersion: "example.test/v1", Kind: "Widget",
		UID: "uid", ResourceVersion: "1",
		Variants: map[string]map[string]any{"rendered": {"entries": []any{map[string]any{"writer": "ours", "ready": true}}}},
	}
	for _, planned := range []bool{false, true} {
		var previous *StatusPatchSnapshot
		for _, ownership := range []string{`{"/entries":{"writer":"ours"}}`, `{"/entries":{"writer":"ours","ready":true}}`} {
			patch.ListOwnership = ownership
			collector := NewStatusPatchCollector()
			if planned {
				projection, err := NewStatusPatchProjection([]StatusPatch{patch})
				require.NoError(t, err)
				plan, err := NewStatusPatchProjectionPlan().ReplaceGroup("status", projection)
				require.NoError(t, err)
				replay, err := plan.PrepareReplay()
				require.NoError(t, err)
				require.NoError(t, collector.ReplayProjectionPlan(replay))
			} else {
				require.NoError(t, collector.RegisterWithLineage(patch.Namespace, patch.Name, patch.APIVersion, patch.Kind,
					patch.UID, patch.ResourceVersion, patch.Variants, ownership))
			}
			snapshot, err := collector.Snapshot()
			require.NoError(t, err)
			changed, err := snapshot.ChangedPatchesForPhase(previous, "rendered")
			require.NoError(t, err)
			require.Len(t, changed, 1)
			require.JSONEq(t, ownership, changed[0].ListOwnership)
			previous = snapshot
		}
	}
}
