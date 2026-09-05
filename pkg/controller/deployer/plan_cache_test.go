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

package deployer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/planblob"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func planFor(id string) *renderplan.Plan {
	plan, _, _ := renderFor(id, "10.0.0.1", mapEntry)
	return plan
}

func TestPlanCache_ResolvesOnlyAnAckedPodScopedProof(t *testing.T) {
	cache := newPlanCache()
	plan := planFor("plan-1")
	state := &api.State{AppliedPlanID: plan.ID, AppliedPlanProof: "a:1"}

	assert.Nil(t, cache.Baseline("pod-a", state))
	require.True(t, cache.Bind("pod-a", plan.ID, "a:1", plan))
	assert.True(t, exactPlan(plan, cache.Baseline("pod-a", state)))
	assert.Nil(t, cache.Baseline("pod-b", state))
}

func TestPlanCache_ControllerRestartRejectsOpaqueBlob(t *testing.T) {
	plan := planFor("plan-1")
	blob, err := planblob.Encode(plan)
	require.NoError(t, err)
	state := &api.State{AppliedPlanID: plan.ID, AppliedPlanProof: "a:1", AppliedPlan: blob}

	assert.Nil(t, newPlanCache().Baseline("pod-a", state))
}

func TestPlanCache_SameIDDifferentExactPlansDoNotAlias(t *testing.T) {
	cache := newPlanCache()
	first := planFor("collision")
	second, _, _ := renderFor("collision", "10.0.0.2", mapEntry)
	require.True(t, cache.Bind("pod-a", first.ID, "a:1", first))
	require.True(t, cache.Bind("pod-b", second.ID, "a:1", second))

	assert.True(t, exactPlan(first, cache.Plan("pod-a", first.ID, "a:1")))
	assert.True(t, exactPlan(second, cache.Plan("pod-b", second.ID, "a:1")))
	assert.False(t, exactPlan(first, cache.Plan("pod-b", second.ID, "a:1")))
}

func TestPlanCache_ReusedAgentGenerationIsPoisoned(t *testing.T) {
	cache := newPlanCache()
	first := planFor("collision")
	second, _, _ := renderFor("collision", "10.0.0.2", mapEntry)
	require.True(t, cache.Bind("pod-a", first.ID, "a:1", first))

	assert.False(t, cache.Bind("pod-a", second.ID, "a:1", second))
	assert.Nil(t, cache.Plan("pod-a", second.ID, "a:1"))
	assert.Nil(t, cache.Plan("pod-a", first.ID, "a:1"))
}

func TestPlanCache_RetainsOnlyLiveRoleProofs(t *testing.T) {
	cache := newPlanCache()
	first := planFor("plan-1")
	second := planFor("plan-2")
	require.True(t, cache.Bind("pod-a", first.ID, "a:1", first))
	require.True(t, cache.Bind("pod-a", second.ID, "a:2", second))
	require.True(t, cache.Bind("pod-b", second.ID, "a:1", second))

	cache.Retain([]planCacheKey{{authority: "pod-a", proof: "a:1"}})

	assert.NotNil(t, cache.Plan("pod-a", first.ID, "a:1"))
	assert.Nil(t, cache.Plan("pod-a", second.ID, "a:2"))
	assert.Nil(t, cache.Plan("pod-b", second.ID, "a:1"))
}

// measuredState reports a pod holding exactly what the plan declares.
func measuredState(plan *renderplan.Plan, blob []byte) *api.State {
	files := make(map[string]api.FileAt, len(plan.Files))
	for i := range plan.Files {
		file := &plan.Files[i]
		files[file.Path] = api.FileAt{Digest: file.Digest, Size: file.Size}
	}
	return &api.State{
		AppliedPlanID:    plan.ID,
		AppliedPlanProof: "a:1",
		AppliedPlan:      blob,
		Files:            files,
	}
}

// A leader that never sent the plan adopts it once the pod's measured tree
// accounts for every file, which is what keeps a handover from reloading.
func TestPlanCache_AdoptsAPlanTheMeasuredTreeAccountsFor(t *testing.T) {
	plan := planFor("plan-1")
	blob, err := planblob.Encode(plan)
	require.NoError(t, err)

	adopted := newPlanCache().AdoptMeasured("pod-a", measuredState(plan, blob))

	require.NotNil(t, adopted)
	assert.Equal(t, plan.ID, adopted.ID)
}

// One file the measurement cannot account for is enough to refuse: ops composed
// against a guess would be applied to a pod running something else.
func TestPlanCache_MeasuredTreeThatMissesAFileIsNoBaseline(t *testing.T) {
	plan := planFor("plan-1")
	blob, err := planblob.Encode(plan)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Files)

	state := measuredState(plan, blob)
	delete(state.Files, plan.Files[0].Path)

	assert.Nil(t, newPlanCache().AdoptMeasured("pod-a", state))
}

// A digest that disagrees is the pod holding different bytes under the same
// path, which is what the measurement exists to catch.
func TestPlanCache_MeasuredDigestMismatchIsNoBaseline(t *testing.T) {
	plan := planFor("plan-1")
	blob, err := planblob.Encode(plan)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Files)

	state := measuredState(plan, blob)
	at := state.Files[plan.Files[0].Path]
	at.Digest = "sha256:something-else"
	state.Files[plan.Files[0].Path] = at

	assert.Nil(t, newPlanCache().AdoptMeasured("pod-a", state))
}

// Without the blob there is nothing to adopt, however well measured the tree is.
func TestPlanCache_MeasuredTreeWithoutABlobIsNoBaseline(t *testing.T) {
	assert.Nil(t, newPlanCache().AdoptMeasured("pod-a", measuredState(planFor("plan-1"), nil)))
}

// A refusal is remembered, so the same measurement is not decoded again on every
// discovery. The second call sees a state that would now adopt cleanly and must
// still refuse it.
func TestPlanCache_RefusedMeasurementIsNotReconsidered(t *testing.T) {
	plan := planFor("plan-1")
	blob, err := planblob.Encode(plan)
	require.NoError(t, err)
	require.NotEmpty(t, plan.Files)

	cache := newPlanCache()
	refused := measuredState(plan, blob)
	delete(refused.Files, plan.Files[0].Path)
	require.Nil(t, cache.AdoptMeasured("pod-a", refused))

	assert.Nil(t, cache.AdoptMeasured("pod-a", measuredState(plan, blob)))
}

// Two measurements race to adopt under one proof. The second finds the key taken
// and returns nil rather than replacing a plan an apply may already be composing
// against.
func TestPlanCache_MeasurementForATakenKeyIsNoBaseline(t *testing.T) {
	first, _, _ := renderFor("plan-1", "10.0.0.1", mapEntry)
	firstBlob, err := planblob.Encode(first)
	require.NoError(t, err)
	// A different address, so a different plan: the ID is derived from content.
	second, _, _ := renderFor("plan-2", "10.0.0.2", mapEntry)
	secondBlob, err := planblob.Encode(second)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)

	cache := newPlanCache()
	require.NotNil(t, cache.AdoptMeasured("pod-a", measuredState(first, firstBlob)))

	// Same authority and proof, so the same key, but a plan the cache never saw:
	// the ID lookup misses and the adopt falls through to the taken guard.
	assert.Nil(t, cache.AdoptMeasured("pod-a", measuredState(second, secondBlob)))
}

// A blob from a controller that wrote a different schema cannot be reasoned
// about, however well the tree measures.
func TestPlanCache_MeasuredPlanFromAnotherSchemaIsNoBaseline(t *testing.T) {
	plan := planFor("plan-1")
	plan.SchemaVersion = renderplan.SchemaVersion + 1
	blob, err := planblob.Encode(plan)
	require.NoError(t, err)

	assert.Nil(t, newPlanCache().AdoptMeasured("pod-a", measuredState(plan, blob)))
}
