// Copyright 2025 Philipp Hossner
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

package deployplan_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// TestInPlaceOnlyWhileAReloadIsPending: without a pending reload the ops carry
// everything, so nothing is duplicated into the in-place set.
func TestInPlaceOnlyWhileAReloadIsPending(t *testing.T) {
	prev := basePlan(withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))))
	next := basePlan(withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.2", 8080))))

	got := deployplan.Diff(next, on34(prev))

	assert.Equal(t, deployplan.VerdictRuntime, got.Verdict)
	assert.Empty(t, got.InPlace)
}

func TestInPlaceIsComputedAgainstTheWorkerPlan(t *testing.T) {
	applied := basePlan(withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))))
	worker := basePlan(withBackend(dynBackend("be-a", weighted(10))))
	next := basePlan(withBackend(dynBackend("be-a", weighted(30))))

	base := on34(applied)
	base.WorkerOps = worker
	base.ReloadPending = true

	got := deployplan.Diff(next, base)

	require.Len(t, got.InPlace, 1)
	assert.Equal(t, api.OpServerSetWeight, got.InPlace[0].Kind)
	assert.Equal(t, 30, *got.InPlace[0].Weight)
}

// TestInPlaceExcludesWhatOutlivesTheReload: deletes and backend lifecycle ops
// are not safe to run against a worker that is about to be replaced.
func TestInPlaceExcludesDeletesAndBackendOps(t *testing.T) {
	worker := basePlan(
		withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080), srv("SRV_2", "10.0.0.2", 8080))),
		withBackend(dynBackend("be-old", srv("SRV_1", "10.0.0.3", 8080))),
	)
	next := basePlan(
		withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))),
		withBackend(dynBackend("be-new", srv("SRV_1", "10.0.0.4", 8080))),
	)
	base := on34(worker)
	base.ReloadPending = true

	got := deployplan.Diff(next, base)

	assert.Equal(t, []string{api.OpServerDisable}, kinds(got.InPlace))
	assert.Equal(t, "SRV_2", got.InPlace[0].Server)
}

func TestInPlaceMapAndCertOps(t *testing.T) {
	worker := basePlan(
		withMap(renderplan.Map{Path: routeMap, Entries: []renderplan.Entry{
			entry("a.example.com", "be-a"), entry("gone.example.com", "be-gone"), entry("multi", "one"),
		}}),
		withFile(renderplan.File{Path: certPath, Kind: renderplan.FileKindCert, Digest: "before"}),
	)
	next := basePlan(
		withMap(renderplan.Map{Path: routeMap, Entries: []renderplan.Entry{
			entry("a.example.com", "be-z"), entry("multi", "one"), entry("multi", "two"),
		}}),
		withFile(renderplan.File{Path: certPath, Kind: renderplan.FileKindCert, Digest: "after"}),
	)
	base := withMapsLoaded(on34(worker), routeMap)
	base.Inventory.Certs = []string{certPath}
	base.ReloadPending = true

	got := deployplan.Diff(next, base)

	assert.Equal(t, []string{api.OpMapSet, api.OpMapDel, api.OpCertSet}, kinds(got.InPlace))
	assert.Equal(t, "a.example.com", got.InPlace[0].Key)
	assert.Equal(t, "gone.example.com", got.InPlace[1].Key)
}

func TestInPlaceHonoursTheAgentOpSet(t *testing.T) {
	worker := basePlan(withBackend(dynBackend("be-a", weighted(10))))
	next := basePlan(withBackend(dynBackend("be-a", weighted(30))))
	base := &deployplan.Baseline{
		Applied:       worker,
		Caps:          deployplan.CapsFor("3.4.3", []string{api.OpServerSetAddr}),
		ReloadPending: true,
	}

	got := deployplan.Diff(next, base)

	assert.Empty(t, got.InPlace)
}

func TestInPlaceFallsBackToTheAppliedPlan(t *testing.T) {
	applied := basePlan(withBackend(dynBackend("be-a", weighted(10))))
	next := basePlan(withBackend(dynBackend("be-a", weighted(30))))
	base := on34(applied)
	base.ReloadPending = true

	got := deployplan.Diff(next, base)

	require.Len(t, got.InPlace, 1)
	assert.Equal(t, api.OpServerSetWeight, got.InPlace[0].Kind)
}

// weighted is the server the in-place cases move, at the weight they want.
func weighted(weight int) renderplan.Server {
	server := srv("SRV_1", "10.0.0.1", 8080)
	server.Weight = ptr(weight)
	return server
}
