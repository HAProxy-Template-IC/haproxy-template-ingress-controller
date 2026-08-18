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

func TestPlanCache_ResolvesAPlanItHolds(t *testing.T) {
	cache := newPlanCache()
	plan := planFor("plan-1")
	cache.Put(plan)

	assert.Same(t, plan, cache.Baseline(&api.State{AppliedPlanID: "plan-1"}))
	assert.Nil(t, cache.Baseline(&api.State{AppliedPlanID: ""}), "a pod with no applied plan has no baseline")
}

// A controller that did not send the plan — a new leader, a restarted process —
// gets the baseline back from the blob the pod stored. Without this every
// leader change would reload the whole fleet.
func TestPlanCache_DecodesTheBlobThePodReports(t *testing.T) {
	plan := planFor("plan-1")
	blob, err := planblob.Encode(plan)
	require.NoError(t, err)

	cache := newPlanCache()
	decoded := cache.Baseline(&api.State{AppliedPlanID: "plan-1", AppliedPlan: blob})

	require.NotNil(t, decoded)
	assert.Equal(t, "plan-1", decoded.ID)
	assert.Equal(t, plan.Backends, decoded.Backends)
	assert.Same(t, decoded, cache.Plan("plan-1"), "a decoded plan is worth keeping")
}

// Anything the decode cannot vouch for is no baseline at all: a partial plan
// would diff into ops for a pod that runs something else.
func TestPlanCache_RefusesABlobItCannotVouchFor(t *testing.T) {
	foreign := planFor("plan-1")
	foreign.SchemaVersion = renderplan.SchemaVersion + 1
	foreignBlob, err := planblob.Encode(foreign)
	require.NoError(t, err)
	mislabelled, err := planblob.Encode(planFor("plan-other"))
	require.NoError(t, err)

	tests := map[string]api.State{
		"foreign schema":     {AppliedPlanID: "plan-1", AppliedPlan: foreignBlob},
		"another plan's id":  {AppliedPlanID: "plan-1", AppliedPlan: mislabelled},
		"not a plan at all":  {AppliedPlanID: "plan-1", AppliedPlan: []byte("not zstd")},
		"no blob to fall on": {AppliedPlanID: "plan-1"},
	}
	for name, state := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Nil(t, newPlanCache().Baseline(&state))
		})
	}
}

// Retain keeps what the fleet refers to plus the newest render, which bounds
// the cache at three ids per pod plus one.
func TestPlanCache_RetainsOnlyWhatIsReferenced(t *testing.T) {
	cache := newPlanCache()
	cache.Put(planFor("plan-1"))
	cache.Put(planFor("plan-2"))
	cache.Put(planFor("plan-3"))

	cache.Retain([]string{"plan-1"})

	assert.NotNil(t, cache.Plan("plan-1"), "a pod still refers to it")
	assert.Nil(t, cache.Plan("plan-2"))
	assert.NotNil(t, cache.Plan("plan-3"), "the newest render is always kept")
}

func TestPlanCache_EncodeDecodeRoundTrip(t *testing.T) {
	plan := planFor("plan-1")

	blob, err := planblob.Encode(plan)
	require.NoError(t, err)
	decoded, err := planblob.Decode(blob)

	require.NoError(t, err)
	assert.Equal(t, plan, decoded)
	assert.Less(t, len(blob), api.MaxPlanBlobBytes)
}
