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

package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/haproxytest"
)

func baseFiles(config string) []file {
	return []file{
		{Path: configPath, Content: config, Reload: true},
		{Path: "maps/host.map", Content: "example.com be-a\n"},
	}
}

// firstApply is the fresh-pod case: the baseline is unknown, so the whole set
// is written and reloaded.
func firstApply(t *testing.T, h *harness) api.ApplyResult {
	t.Helper()
	files := baseFiles("global\n")
	m := buildManifest("plan-1", files)
	m.Mode = api.ModeReload
	result := h.apply(&m, files)
	require.True(t, result.OK, "%+v", result.Error)
	return result
}

func TestFirstApplyWritesTheTreeAndReloads(t *testing.T) {
	h := newHarness(t)
	result := firstApply(t, h)

	assert.Equal(t, api.ResultReload, result.Mode)
	assert.Equal(t, "plan-1", result.AppliedPlanID)
	assert.Equal(t, "plan-1", result.RunningPlanID)
	assert.Equal(t, "plan-1", result.LKGPlanID)
	require.NotNil(t, result.Reload)
	assert.True(t, result.Reload.Performed)
	assert.Equal(t, "global\n", h.read(configPath))
	assert.Equal(t, "example.com be-a\n", h.read("maps/host.map"))

	state := h.state(false)
	assert.Equal(t, uint64(1), state.Generation)
	assert.Equal(t, api.Version, state.APIVersion)
	assert.Contains(t, state.AgentOps, api.OpBackendAdd)
	assert.Len(t, state.Files, 2)
	assert.Zero(t, h.metric("haptic_agent_invariant_violations_total"))
}

func TestRuntimeApplyRunsOpsWithoutReloading(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)

	files := baseFiles("global\n")
	files[1].Content = "example.com be-a\nnew.example.com be-b\n"
	m := buildManifest("plan-2", files)
	m.ExpectedPrevPlanID = first.AppliedPlanID
	m.ExpectedPrevToken = first.AppliedToken
	m.Ops = []api.Op{
		{Kind: api.OpBackendAdd, Backend: "be-b", Profile: "prof", Mode: "http"},
		{Kind: api.OpServerAdd, Backend: "be-b", Server: "srv1", Address: "10.0.0.9", Port: 80},
		{Kind: api.OpServerEnable, Backend: "be-b", Server: "srv1"},
		{Kind: api.OpBackendPublish, Backend: "be-b"},
		{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "new.example.com", Value: "be-b"},
	}
	result := h.apply(&m, files)

	require.True(t, result.OK, "%+v", result.Error)
	assert.Equal(t, api.ResultRuntime, result.Mode)
	assert.Nil(t, result.Reload)
	assert.Equal(t, "plan-1", result.RunningPlanID, "a runtime apply does not advance the running plan")
	assert.True(t, h.model.HasBackend("be-b"))
	assert.Equal(t, uint64(2), h.state(false).Generation)
	assert.Equal(t, 1.0, h.metric("haptic_agent_apply_total", api.ResultRuntime))
	assert.Zero(t, h.metric("haptic_agent_invariant_violations_total"))
}

func TestFencingRefusesAndNeverWrites(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*api.Manifest)
		reason string
	}{
		{
			name:   "previous plan does not match",
			mutate: func(m *api.Manifest) { m.ExpectedPrevPlanID = "plan-elsewhere" },
			reason: "prev_mismatch",
		},
		{
			name:   "previous token does not match",
			mutate: func(m *api.Manifest) { m.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 99} },
			reason: "prev_mismatch",
		},
		{
			name: "a former leader is still dispatching",
			mutate: func(m *api.Manifest) {
				m.Token = api.Token{LeaderEpoch: 0, RenderSeq: 1}
			},
			reason: "stale_epoch",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			files := baseFiles("global\n")
			m := buildManifest("plan-1", files)
			m.Mode = api.ModeReload
			m.Token = api.Token{LeaderEpoch: 4, RenderSeq: 1}
			require.True(t, h.apply(&m, files).OK)

			next := baseFiles("global\n  maxconn 100\n")
			nextManifest := buildManifest("plan-2", next)
			nextManifest.ExpectedPrevPlanID = "plan-1"
			nextManifest.ExpectedPrevToken = api.Token{LeaderEpoch: 4, RenderSeq: 1}
			nextManifest.Token = api.Token{LeaderEpoch: 4, RenderSeq: 2}
			tc.mutate(&nextManifest)

			status, raw := h.post(&nextManifest, next)
			require.Equal(t, http.StatusConflict, status, string(raw))
			conflict := api.Conflict{}
			require.NoError(t, json.Unmarshal(raw, &conflict))
			assert.Equal(t, tc.reason, conflict.Reason)
			assert.Equal(t, "plan-1", conflict.AppliedPlanID)
			assert.Equal(t, "global\n", h.read(configPath), "a refused apply must not write")
		})
	}
}

func TestUnknownBaselineIsItsOwnConflictReason(t *testing.T) {
	h := newHarness(t)
	files := baseFiles("global\n")
	m := buildManifest("plan-1", files)
	m.ExpectedPrevPlanID = "plan-0"

	status, raw := h.post(&m, files)
	require.Equal(t, http.StatusConflict, status)
	conflict := api.Conflict{}
	require.NoError(t, json.Unmarshal(raw, &conflict))
	assert.Equal(t, "unknown_baseline", conflict.Reason)
	assert.False(t, h.exists(configPath))
}

func TestMissingPartsAreNamed(t *testing.T) {
	h := newHarness(t)
	files := baseFiles("global\n")
	m := buildManifest("plan-1", files)
	m.Mode = api.ModeReload

	status, raw := h.post(&m, files, "maps/host.map")
	require.Equal(t, http.StatusConflict, status)
	missing := api.Missing{}
	require.NoError(t, json.Unmarshal(raw, &missing))
	assert.Equal(t, []string{"maps/host.map"}, missing.Missing)
	assert.False(t, h.exists(configPath), "no part lands while one is missing")
}

func TestUnchangedFilesNeedNoParts(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)

	files := baseFiles("global\n")
	m := buildManifest("plan-2", files)
	m.ExpectedPrevPlanID = first.AppliedPlanID
	m.ExpectedPrevToken = first.AppliedToken

	status, raw := h.post(&m, files, configPath, "maps/host.map")
	require.Equal(t, http.StatusOK, status, string(raw))
	result := api.ApplyResult{}
	require.NoError(t, json.Unmarshal(raw, &result))
	assert.Equal(t, api.ResultNoop, result.Mode)
	assert.Equal(t, "plan-2", result.AppliedPlanID)
}

func TestAPartThatDoesNotMatchItsDigestIsRefused(t *testing.T) {
	h := newHarness(t)
	files := baseFiles("global\n")
	m := buildManifest("plan-1", files)
	m.Mode = api.ModeReload
	files[0].Content = "global\n  tampered\n"

	status, raw := h.post(&m, files)
	require.Equal(t, http.StatusBadRequest, status, string(raw))
	assert.Contains(t, string(raw), "manifest digest")
	assert.False(t, h.exists(configPath))
}

func TestAbsenceDeletesAnOwnedPath(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)

	files := []file{{Path: configPath, Content: "global\n", Reload: true}}
	m := buildManifest("plan-2", files)
	m.ExpectedPrevPlanID = first.AppliedPlanID
	m.ExpectedPrevToken = first.AppliedToken

	result := h.apply(&m, files)
	require.True(t, result.OK)
	assert.False(t, h.exists("maps/host.map"))
	assert.Equal(t, map[string]string{configPath: "global\n"}, h.tree())
}

func TestUnknownOpFallsBackToAReload(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)

	files := baseFiles("global\n  maxconn 200\n")
	m := buildManifest("plan-2", files)
	m.ExpectedPrevPlanID = first.AppliedPlanID
	m.ExpectedPrevToken = first.AppliedToken
	m.Ops = []api.Op{{Kind: "backend_teleport", Backend: "be-a"}}

	result := h.apply(&m, files)
	require.True(t, result.OK, "%+v", result.Error)
	assert.Equal(t, api.ResultReload, result.Mode)
	assert.Equal(t, "plan-2", result.RunningPlanID)
	assert.Equal(t, 1.0, h.metric("haptic_agent_invariant_violations_total", "ops_executable"))
}

func TestARejectedOpReloadsTheDesiredSet(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)

	files := baseFiles("global\n  maxconn 300\n")
	m := buildManifest("plan-2", files)
	m.ExpectedPrevPlanID = first.AppliedPlanID
	m.ExpectedPrevToken = first.AppliedToken
	m.Ops = []api.Op{{Kind: api.OpServerAdd, Backend: "absent", Server: "srv1", Address: "10.0.0.1"}}

	result := h.apply(&m, files)
	require.True(t, result.OK, "%+v", result.Error)
	assert.Equal(t, api.ResultReload, result.Mode)
	assert.Equal(t, "global\n  maxconn 300\n", h.read(configPath))
	assert.Equal(t, 1.0, h.metric("haptic_agent_op_errors_total", api.OpServerAdd))
}

func TestAFailedReloadRestoresTheLastKnownGoodSet(t *testing.T) {
	h := newHarness(t)
	firstApply(t, h)

	h.model.With(func(m *haproxytest.Model) {
		m.ReloadFails = true
		m.ReloadLog = "[ALERT] config : parsing [haproxy.cfg:1] : unknown keyword 'nonsense'."
	})
	files := baseFiles("nonsense\n")
	m := buildManifest("plan-bad", files)
	m.Mode = api.ModeReload
	m.ExpectedPrevPlanID = "plan-1"

	result := h.apply(&m, files)
	require.False(t, result.OK)
	assert.Equal(t, api.ResultRejected, result.Mode)
	require.NotNil(t, result.Error)
	assert.Contains(t, result.Error.Message, "unknown keyword")
	require.NotNil(t, result.Rollback)
	assert.True(t, result.Rollback.Performed)
	assert.Equal(t, "global\n", h.read(configPath), "the tree is back on the last known good set")
	assert.Empty(t, result.AppliedPlanID, "a NACK invalidates the baseline")
	assert.Equal(t, 1.0, h.metric("haptic_agent_rollbacks_total"))
}

func TestTheSameRejectedManifestDoesNoWorkInsideTheCooldown(t *testing.T) {
	h := newHarness(t)
	firstApply(t, h)
	h.model.With(func(m *haproxytest.Model) { m.ReloadFails = true })

	files := baseFiles("nonsense\n")
	m := buildManifest("plan-bad", files)
	m.Mode = api.ModeReload
	m.ExpectedPrevPlanID = "plan-1"
	require.False(t, h.apply(&m, files).OK)
	reloadsAfterFirst := h.metric("haptic_agent_reloads_total", "failed")

	m.ExpectedPrevPlanID = ""
	result := h.apply(&m, files)
	assert.False(t, result.OK)
	assert.Equal(t, reloadsAfterFirst, h.metric("haptic_agent_reloads_total", "failed"),
		"a known-bad manifest must not reach HAProxy again")
	assert.Equal(t, "global\n", h.read(configPath))
}

func TestRevertLKGRestoresAndReloads(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)

	files := baseFiles("global\n  maxconn 400\n")
	m := buildManifest("plan-2", files)
	m.ExpectedPrevPlanID = first.AppliedPlanID
	m.ExpectedPrevToken = first.AppliedToken
	m.Mode = api.ModeReload
	require.True(t, h.apply(&m, files).OK)
	require.Equal(t, "global\n  maxconn 400\n", h.read(configPath))

	revert := buildManifest("plan-2", nil)
	revert.Mode = api.ModeRevertLKG
	result := h.apply(&revert, nil)

	require.True(t, result.OK, "%+v", result.Error)
	require.NotNil(t, result.Rollback)
	assert.True(t, result.Rollback.Performed)
	assert.True(t, result.Rollback.Reloaded)
}

func TestLKGPromotionClearsTheJournal(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)

	files := baseFiles("global\n  maxconn 500\n")
	m := buildManifest("plan-2", files)
	m.ExpectedPrevPlanID = first.AppliedPlanID
	m.ExpectedPrevToken = first.AppliedToken
	m.Ops = []api.Op{{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "example.com", Value: "be-a"}}
	second := h.apply(&m, files)
	require.True(t, second.OK, "%+v", second.Error)
	require.Equal(t, "plan-1", second.LKGPlanID, "a runtime apply does not promote by itself")

	noop := buildManifest("plan-3", files)
	noop.ExpectedPrevPlanID = second.AppliedPlanID
	noop.ExpectedPrevToken = second.AppliedToken
	noop.ValidatedPlanID = "plan-2"
	promoted := h.apply(&noop, files)

	require.True(t, promoted.OK, "%+v", promoted.Error)
	assert.Equal(t, "plan-2", promoted.LKGPlanID)
	assert.Zero(t, h.metric("haptic_agent_invariant_violations_total"))
}

func TestAScheduledReloadCoalescesAndRunsInPlaceOps(t *testing.T) {
	h := newHarness(t, withReloadInterval(time.Hour))
	files := baseFiles("global\n")
	m := buildManifest("plan-1", files)
	m.Mode = api.ModeReload
	first := h.apply(&m, files)
	require.True(t, first.OK)

	next := baseFiles("global\n  maxconn 600\n")
	second := buildManifest("plan-2", next)
	second.Mode = api.ModeReload
	second.ExpectedPrevPlanID = first.AppliedPlanID
	second.ExpectedPrevToken = first.AppliedToken
	scheduled := h.apply(&second, next)
	require.True(t, scheduled.OK, "%+v", scheduled.Error)
	require.Equal(t, api.ResultScheduled, scheduled.Mode)
	require.NotNil(t, scheduled.Reload)
	assert.NotEmpty(t, scheduled.Reload.ScheduledAt)
	assert.NotEmpty(t, h.state(false).ReloadPendingAt)

	third := buildManifest("plan-3", next)
	third.ExpectedPrevPlanID = scheduled.AppliedPlanID
	third.ExpectedPrevToken = scheduled.AppliedToken
	third.ExpectedWorkerOpsPlanID = scheduled.WorkerOpsPlanID
	third.InPlaceOps = []api.Op{{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "example.com", Value: "be-c"}}
	coalesced := h.apply(&third, next)

	require.True(t, coalesced.OK, "%+v", coalesced.Error)
	assert.Equal(t, api.ResultScheduled, coalesced.Mode)
	assert.Equal(t, "plan-3", coalesced.WorkerOpsPlanID)
	assert.Equal(t, "global\n  maxconn 600\n", h.read(configPath), "the files land even while a reload waits")
}

func TestAnInPlaceOpOnAStaleWorkerBaselineInvalidatesThePod(t *testing.T) {
	h := newHarness(t, withReloadInterval(time.Hour))
	files := baseFiles("global\n")
	m := buildManifest("plan-1", files)
	m.Mode = api.ModeReload
	first := h.apply(&m, files)

	next := baseFiles("global\n  maxconn 700\n")
	second := buildManifest("plan-2", next)
	second.Mode = api.ModeReload
	second.ExpectedPrevPlanID = first.AppliedPlanID
	second.ExpectedPrevToken = first.AppliedToken
	scheduled := h.apply(&second, next)
	require.Equal(t, api.ResultScheduled, scheduled.Mode)

	third := buildManifest("plan-3", next)
	third.ExpectedPrevPlanID = scheduled.AppliedPlanID
	third.ExpectedPrevToken = scheduled.AppliedToken
	third.ExpectedWorkerOpsPlanID = "plan-from-another-life"
	third.InPlaceOps = []api.Op{{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "example.com", Value: "be-c"}}
	result := h.apply(&third, next)

	require.NotNil(t, result.Error)
	assert.Equal(t, "in_place", result.Error.Stage)
	assert.Empty(t, h.state(false).AppliedPlanID, "the pod's baseline is invalidated, not silently reused")
}

func TestTheScheduledReloadFiresWhenTheWindowPasses(t *testing.T) {
	h := newHarness(t, withReloadInterval(300*time.Millisecond))
	files := baseFiles("global\n")
	m := buildManifest("plan-1", files)
	m.Mode = api.ModeReload
	first := h.apply(&m, files)

	next := baseFiles("global\n  maxconn 800\n")
	second := buildManifest("plan-2", next)
	second.Mode = api.ModeReload
	second.ExpectedPrevPlanID = first.AppliedPlanID
	second.ExpectedPrevToken = first.AppliedToken
	require.Equal(t, api.ResultScheduled, h.apply(&second, next).Mode)

	require.Eventually(t, func() bool {
		return h.state(false).RunningPlanID == "plan-2"
	}, 10*time.Second, 20*time.Millisecond)
	assert.Empty(t, h.state(false).ReloadPendingAt)
}

// The controller polls last_apply for a scheduled reload's verdict and takes
// its applied plan as the next baseline, so a failed one must report the
// invalidated baseline, not the one from before the reload.
func TestAFailedScheduledReloadReportsTheInvalidatedBaseline(t *testing.T) {
	h := newHarness(t, withReloadInterval(300*time.Millisecond))
	files := baseFiles("global\n")
	m := buildManifest("plan-1", files)
	m.Mode = api.ModeReload
	first := h.apply(&m, files)

	h.model.With(func(model *haproxytest.Model) { model.ReloadFails = true })
	next := baseFiles("global\n  broken\n")
	second := buildManifest("plan-2", next)
	second.Mode = api.ModeReload
	second.ExpectedPrevPlanID = first.AppliedPlanID
	second.ExpectedPrevToken = first.AppliedToken
	require.Equal(t, api.ResultScheduled, h.apply(&second, next).Mode)

	var last *api.ApplyResult
	require.Eventually(t, func() bool {
		last = h.state(false).LastApply
		return last != nil && last.PlanID == "plan-2" && last.Mode != api.ResultScheduled
	}, 10*time.Second, 20*time.Millisecond)
	assert.False(t, last.OK)
	assert.Empty(t, last.AppliedPlanID, "the NACK must carry the baseline the next apply has to expect")
	assert.Empty(t, h.state(false).AppliedPlanID)
	assert.Equal(t, "plan-1", last.LKGPlanID)
}

func TestStateVerifyObservesTheTree(t *testing.T) {
	h := newHarness(t)
	firstApply(t, h)

	require.NoError(t, writeFile(h, "maps/host.map", "tampered by someone else\n"))
	assert.NotEqual(t, h.state(false).Files["maps/host.map"].Digest, h.state(true).Files["maps/host.map"].Digest)
}

func TestAuthenticationIsRequiredForTheAPIButNotTheProbes(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{api.PathHealthz, api.PathReadyz} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, h.url+path, http.NoBody)
		require.NoError(t, err)
		response, err := h.client.Do(request)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		assert.Equal(t, http.StatusOK, response.StatusCode, path)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, h.url+api.PathState, http.NoBody)
	require.NoError(t, err)
	response, err := h.client.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)

	request.SetBasicAuth(testUser, "wrong")
	response, err = h.client.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
}
