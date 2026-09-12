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

package server_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/haproxytest"
)

func TestRuntimeAndFileOnlyApplyRequireExactWorkerBaseline(t *testing.T) {
	for _, mode := range []string{"runtime", "file-only"} {
		for _, mismatch := range []string{"plan", "proof"} {
			t.Run(mode+"/"+mismatch, func(t *testing.T) {
				h := newHarness(t)
				first := firstApply(t, h)
				before := h.state(false)
				next := baseFiles("global\n")
				next[1].Content += "new.example.com be-new\n"
				manifest := buildManifest("plan-2", next)
				manifest.IdentityVersion = api.ExactIdentityVersion
				manifest.ExpectedPrevPlanID = first.AppliedPlanID
				manifest.ExpectedPrevPlanProof = first.AppliedPlanProof
				manifest.ExpectedPrevToken = first.AppliedToken
				manifest.ExpectedWorkerOpsPlanID = first.WorkerOpsPlanID
				manifest.ExpectedWorkerOpsPlanProof = first.WorkerOpsPlanProof
				if mismatch == "plan" {
					manifest.ExpectedWorkerOpsPlanID = "worker-before-reload"
				} else {
					manifest.ExpectedWorkerOpsPlanProof = "proof-before-reload"
				}
				if mode == "runtime" {
					manifest.Ops = []api.Op{{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "new.example.com", Value: "be-new"}}
				}
				status, body := h.postRaw(&manifest, next)
				require.Equal(t, http.StatusConflict, status, string(body))
				var conflict api.Conflict
				require.NoError(t, json.Unmarshal(body, &conflict))
				require.Equal(t, "worker_ops_mismatch", conflict.Reason)
				after := h.state(false)
				slices.Sort(before.AgentOps)
				slices.Sort(after.AgentOps)
				require.Equal(t, before, after)
				require.Equal(t, baseFiles("global\n")[1].Content, h.read("maps/host.map"))
			})
		}
	}
}

func TestFileOnlyApplyAcrossScheduledReloadRequiresRediff(t *testing.T) {
	h := newHarness(t, withReloadInterval(2*time.Second))
	first := firstApply(t, h)
	const mapPath = "maps/host.map"
	h.model.With(func(model *haproxytest.Model) {
		model.OnReload = func(next *haproxytest.Model) {
			next.Maps[mapPath] = []haproxytest.MapEntry{{Key: "example.com", Value: "be-a"}}
		}
	})
	files := baseFiles("global\n  maxconn 800\n")
	second := buildManifest("plan-2", files)
	second.Mode = api.ModeReload
	second.ExpectedPrevPlanID = first.AppliedPlanID
	second.ExpectedPrevToken = first.AppliedToken
	scheduled := h.apply(&second, files)
	require.Equal(t, api.ResultScheduled, scheduled.Mode)
	before := h.state(false)
	require.NotContains(t, before.Inventory.Maps, "maps/host.map")

	next := baseFiles(files[0].Content)
	next[1].Content += "new.example.com be-new\n"
	manifest := buildManifest("plan-3", next)
	manifest.ExpectedPrevPlanID = before.AppliedPlanID
	manifest.ExpectedPrevPlanProof = before.AppliedPlanProof
	manifest.ExpectedPrevToken = before.AppliedToken
	manifest.ExpectedWorkerOpsPlanID = before.WorkerOpsPlanID
	manifest.ExpectedWorkerOpsPlanProof = before.WorkerOpsPlanProof
	require.Eventually(t, func() bool {
		return h.state(false).RunningPlanID == second.PlanID
	}, 10*time.Second, 20*time.Millisecond)
	after := h.state(false)
	require.Equal(t, before.AppliedPlanProof, after.AppliedPlanProof)
	require.NotEqual(t, before.WorkerOpsPlanProof, after.WorkerOpsPlanProof)
	require.Contains(t, after.Inventory.Maps, "maps/host.map")

	status, body := h.postRaw(&manifest, next)
	require.Equal(t, http.StatusConflict, status, string(body))
	var conflict api.Conflict
	require.NoError(t, json.Unmarshal(body, &conflict))
	require.Equal(t, "worker_ops_mismatch", conflict.Reason)
	require.Equal(t, files[1].Content, h.read("maps/host.map"))
	require.Equal(t, []haproxytest.MapEntry{{Key: "example.com", Value: "be-a"}}, h.model.MapEntries(mapPath))

	manifest.ExpectedWorkerOpsPlanID = after.WorkerOpsPlanID
	manifest.ExpectedWorkerOpsPlanProof = after.WorkerOpsPlanProof
	manifest.Ops = []api.Op{{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "new.example.com", Value: "be-new"}}
	status, body = h.postRaw(&manifest, next)
	require.Equal(t, http.StatusOK, status, string(body))
	var result api.ApplyResult
	require.NoError(t, json.Unmarshal(body, &result))
	require.True(t, result.OK, string(body))
	require.Equal(t, api.ResultRuntime, result.Mode)
	require.Equal(t, after.HAProxy, h.state(false).HAProxy)
	require.Equal(t, next[1].Content, h.read("maps/host.map"))
	require.Equal(t, []haproxytest.MapEntry{
		{Key: "example.com", Value: "be-a"},
		{Key: "new.example.com", Value: "be-new"},
	}, h.model.MapEntries(mapPath))
}

func TestAutomaticApplyWithoutWorkerProofReloads(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)
	files := baseFiles("global\n")
	manifest := buildManifest("plan-2", files)
	manifest.ExpectedPrevPlanID = first.AppliedPlanID
	manifest.ExpectedPrevPlanProof = first.AppliedPlanProof
	manifest.ExpectedPrevToken = first.AppliedToken
	manifest.Ops = []api.Op{{Kind: api.OpBackendAdd, Backend: "must-not-run", Profile: "prof", Mode: "http"}}
	status, body := h.postRaw(&manifest, files)
	require.Equal(t, http.StatusOK, status, string(body))
	var result api.ApplyResult
	require.NoError(t, json.Unmarshal(body, &result))
	require.True(t, result.OK, string(body))
	require.Equal(t, api.ResultReload, result.Mode)
	require.False(t, h.model.HasBackend("must-not-run"))
	require.NotEqual(t, first.HAProxy.WorkerPID, result.HAProxy.WorkerPID)
}

func TestAutomaticInPlaceApplyWithoutWorkerProofIsRejected(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)
	before := h.state(false)
	files := baseFiles("global\n  maxconn 800\n")
	manifest := buildManifest("plan-2", files)
	manifest.ExpectedPrevPlanID = first.AppliedPlanID
	manifest.ExpectedPrevPlanProof = first.AppliedPlanProof
	manifest.ExpectedPrevToken = first.AppliedToken
	manifest.ExpectedWorkerOpsPlanID = first.WorkerOpsPlanID
	manifest.WorkerOpsPlanID = "worker-after-in-place"
	manifest.InPlaceOps = []api.Op{{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "new.example.com", Value: "be-new"}}
	status, body := h.postRaw(&manifest, files)
	require.Equal(t, http.StatusBadRequest, status, string(body))
	require.Contains(t, string(body), "in-place ops need an exact expected worker plan proof")
	after := h.state(false)
	slices.Sort(before.AgentOps)
	slices.Sort(after.AgentOps)
	require.Equal(t, before, after)
	require.Equal(t, "global\n", h.read(configPath))
	require.Equal(t, baseFiles("global\n")[1].Content, h.read("maps/host.map"))
}
