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

package agenttest_test

import (
	"context"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/agenttest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func newClient(t *testing.T, agent *agenttest.Agent) *client.Client {
	t.Helper()
	c, err := client.New(&client.Config{
		BaseURL:             agent.URL(),
		Username:            agent.Username(),
		Password:            agent.Password(),
		Timeout:             5 * time.Second,
		PerPodApplyTimeout:  5 * time.Second,
		ConnectRetryBackoff: time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

// build turns path→content into a manifest plus the matching parts, deriving
// each file's kind from its directory the way the render's file set does.
func build(planID, mode string, token api.Token, files map[string]string) (manifest *api.Manifest, parts map[string]io.Reader) {
	m := &api.Manifest{PlanID: planID, PlanSchemaVersion: 1, Token: token, Mode: mode}
	parts = map[string]io.Reader{}
	for _, path := range slices.Sorted(maps.Keys(files)) {
		content := files[path]
		m.Files = append(m.Files, api.File{
			Path:           path,
			Digest:         renderplan.DigestString(content),
			Size:           int64(len(content)),
			Kind:           kindOf(path),
			ReloadOnChange: strings.HasSuffix(path, ".cfg"),
		})
		parts[path] = strings.NewReader(content)
	}
	return m, parts
}

func kindOf(path string) string {
	switch {
	case strings.HasPrefix(path, "maps/"):
		return api.FileKindMap
	case strings.HasPrefix(path, "ssl/"):
		return api.FileKindCert
	case strings.HasPrefix(path, "general/"):
		return api.FileKindGeneral
	default:
		return api.FileKindConfig
	}
}

// seed brings the fake to a known baseline: one reloaded plan holding both
// files, which every follow-up apply fences against.
func seed(t *testing.T, c *client.Client) *api.ApplyResult {
	t.Helper()
	m, parts := build("plan-1", api.ModeReload, api.Token{LeaderEpoch: 1, RenderSeq: 1}, map[string]string{
		"haproxy.cfg":   "global\n",
		"maps/host.map": "example.com be-1\n",
	})
	result, err := c.Apply(context.Background(), m, parts, nil)
	require.NoError(t, err)
	require.True(t, result.OK)
	return result
}

func TestFirstApplyReloadsAndBecomesTheBaseline(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t)
	c := newClient(t, agent)

	before := agent.State()
	result := seed(t, c)

	assert.Equal(t, api.ResultReload, result.Mode)
	assert.Equal(t, "plan-1", result.AppliedPlanID)
	assert.Equal(t, "plan-1", result.RunningPlanID)
	assert.Equal(t, "plan-1", result.LKGPlanID)
	require.NotNil(t, result.Reload)
	assert.True(t, result.Reload.Performed)
	assert.Greater(t, result.HAProxy.WorkerPID, before.HAProxy.WorkerPID)

	state := agent.State()
	assert.Equal(t, uint64(1), state.Generation)
	assert.Len(t, state.Files, 2)
	assert.Equal(t, []string{"maps/host.map"}, state.Inventory.Maps)
}

func TestMissingPartsThenResend(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t)
	c := newClient(t, agent)

	m, parts := build("plan-1", api.ModeReload, api.Token{LeaderEpoch: 1, RenderSeq: 1}, map[string]string{
		"haproxy.cfg":   "global\n",
		"maps/host.map": "example.com be-1\n",
	})
	_, err := c.Apply(context.Background(), m, nil, nil)
	var missing *client.MissingError
	require.ErrorAs(t, err, &missing)
	assert.ElementsMatch(t, []string{"haproxy.cfg", "maps/host.map"}, missing.Missing)
	assert.Zero(t, agent.State().Generation, "a missing-parts answer must not write")

	result, err := c.Apply(context.Background(), m, parts, nil)
	require.NoError(t, err)
	assert.True(t, result.OK)

	// The agent now holds both digests, so an identical follow-up needs no parts.
	next, _ := build("plan-2", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 2}, map[string]string{
		"haproxy.cfg":   "global\n",
		"maps/host.map": "example.com be-1\n",
	})
	next.ExpectedPrevPlanID = "plan-1"
	next.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 1}
	result, err = c.Apply(context.Background(), next, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, api.ResultNoop, result.Mode)
}

func TestFencing(t *testing.T) {
	t.Parallel()
	baseline := api.Token{LeaderEpoch: 1, RenderSeq: 1}

	tests := []struct {
		name        string
		mutate      func(*api.Manifest)
		wantReason  string
		wantApplied string
	}{
		{name: "matching baseline", mutate: func(*api.Manifest) {}, wantApplied: "plan-2"},
		{name: "newer leader epoch", mutate: func(m *api.Manifest) {
			m.Token = api.Token{LeaderEpoch: 2, RenderSeq: 1}
		}, wantApplied: "plan-2"},
		{name: "stale plan id", mutate: func(m *api.Manifest) {
			m.ExpectedPrevPlanID = "plan-0"
		}, wantReason: "prev_mismatch"},
		{name: "stale render seq", mutate: func(m *api.Manifest) {
			m.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 99}
		}, wantReason: "prev_mismatch"},
		{name: "older leader epoch", mutate: func(m *api.Manifest) {
			m.Token = api.Token{LeaderEpoch: 0, RenderSeq: 5}
			m.ExpectedPrevToken = api.Token{LeaderEpoch: 0, RenderSeq: 4}
		}, wantReason: "stale_epoch"},
		{name: "a revert is fenced by the epoch alone", mutate: func(m *api.Manifest) {
			m.Mode = api.ModeRevertLKG
			m.ExpectedPrevPlanID = "plan-from-another-life"
		}, wantApplied: "plan-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			agent := agenttest.New(t)
			c := newClient(t, agent)
			seed(t, c)

			m, parts := build("plan-2", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 2}, map[string]string{
				"haproxy.cfg":   "global\n  nbthread 2\n",
				"maps/host.map": "example.com be-1\n",
			})
			m.ExpectedPrevPlanID = "plan-1"
			m.ExpectedPrevToken = baseline
			m.ExpectedWorkerOpsPlanID = "plan-1"
			tt.mutate(m)

			_, err := c.Apply(context.Background(), m, parts, nil)
			if tt.wantReason == "" {
				require.NoError(t, err)
				assert.Equal(t, tt.wantApplied, agent.State().AppliedPlanID)
				return
			}
			var conflict *client.ConflictError
			require.ErrorAs(t, err, &conflict)
			assert.Equal(t, tt.wantReason, conflict.Conflict.Reason)
			assert.Equal(t, "plan-1", conflict.Conflict.AppliedPlanID)

			state := agent.State()
			assert.Equal(t, "plan-1", state.AppliedPlanID, "a conflict must never write")
			assert.Equal(t, uint64(1), state.Generation)
		})
	}
}

func TestUnknownBaselineIsItsOwnConflictReason(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t)
	c := newClient(t, agent)

	m, parts := build("plan-2", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 2}, map[string]string{
		"haproxy.cfg": "global\n",
	})
	m.ExpectedPrevPlanID = "plan-1"
	_, err := c.Apply(context.Background(), m, parts, nil)
	var conflict *client.ConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, "unknown_baseline", conflict.Conflict.Reason)
}

func TestRuntimeApplyRunsOpsWithoutAReload(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t)
	c := newClient(t, agent)
	seeded := seed(t, c)

	m, parts := build("plan-2", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 2}, map[string]string{
		"haproxy.cfg":   "global\n",
		"maps/host.map": "example.com be-2\n",
	})
	m.ExpectedPrevPlanID = "plan-1"
	m.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 1}
	m.Ops = []api.Op{{Kind: api.OpMapSet, Path: "maps/host.map", Key: "example.com", Value: "be-2"}}

	result, err := c.Apply(context.Background(), m, parts, nil)
	require.NoError(t, err)
	assert.Equal(t, api.ResultRuntime, result.Mode)
	assert.Equal(t, "plan-2", result.AppliedPlanID)
	assert.Equal(t, "plan-1", result.RunningPlanID, "a runtime apply never advances the running plan")
	assert.Equal(t, seeded.HAProxy.WorkerPID, result.HAProxy.WorkerPID, "no reload, so no new worker")
	require.Len(t, result.OpResults, 1)
	assert.Equal(t, api.OpMapSet, result.OpResults[0].Kind)

	applies := agent.Applies()
	require.Len(t, applies, 2)
	assert.Equal(t, "example.com be-2\n", string(applies[1].Parts["maps/host.map"]))
}

func TestFileOnlyApplyWhenNothingRuns(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t)
	c := newClient(t, agent)
	seed(t, c)

	m, parts := build("plan-2", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 2}, map[string]string{
		"haproxy.cfg":      "global\n",
		"maps/host.map":    "example.com be-1\n",
		"general/50x.http": "HTTP/1.1 503\n",
	})
	m.ExpectedPrevPlanID = "plan-1"
	m.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 1}

	result, err := c.Apply(context.Background(), m, parts, nil)
	require.NoError(t, err)
	assert.Equal(t, api.ResultFileOnly, result.Mode)
	assert.Len(t, agent.State().Files, 3)
}

func TestPendingReloadCoalescesAndRunsOnlyInPlaceOps(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t)
	c := newClient(t, agent)
	seeded := seed(t, c)
	agent.SetReloadPending(true)

	m, parts := build("plan-2", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 2}, map[string]string{
		"haproxy.cfg":   "global\n  nbthread 4\n",
		"maps/host.map": "example.com be-1\n",
	})
	m.ExpectedPrevPlanID = "plan-1"
	m.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 1}
	m.ExpectedWorkerOpsPlanID = "plan-1"
	m.Ops = []api.Op{{Kind: api.OpBackendAdd, Backend: "be-2", Profile: "haptic-base", Mode: "http"}}
	m.InPlaceOps = []api.Op{{Kind: api.OpServerSetWeight, Backend: "be-1", Server: "srv1"}}

	result, err := c.Apply(context.Background(), m, parts, nil)
	require.NoError(t, err)
	assert.Equal(t, api.ResultScheduled, result.Mode)
	assert.Equal(t, "plan-2", result.WorkerOpsPlanID)
	assert.Equal(t, "plan-1", result.RunningPlanID)
	assert.Equal(t, seeded.HAProxy.WorkerPID, result.HAProxy.WorkerPID)
	require.Len(t, result.OpResults, 1)
	assert.Equal(t, api.OpServerSetWeight, result.OpResults[0].Kind, "the structural ops wait for the reload")

	applies := agent.Applies()
	require.Len(t, applies, 2)
	assert.Len(t, applies[1].Manifest.InPlaceOps, 1)
}

// TestInPlaceOpsOnAStaleWorkerBaselineAreNotAConflict pins the shape the real
// agent answers with: the files land, the apply is scheduled, and the error
// invalidates the pod instead of coming back as a 409 the caller would retry.
func TestInPlaceOpsOnAStaleWorkerBaselineAreNotAConflict(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t)
	c := newClient(t, agent)
	seed(t, c)
	agent.SetReloadPending(true)

	m, parts := build("plan-2", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 2}, map[string]string{
		"haproxy.cfg":   "global\n  nbthread 4\n",
		"maps/host.map": "example.com be-1\n",
	})
	m.ExpectedPrevPlanID = "plan-1"
	m.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 1}
	m.ExpectedWorkerOpsPlanID = "plan-from-another-life"
	m.InPlaceOps = []api.Op{{Kind: api.OpMapSet, Path: "maps/host.map", Key: "a", Value: "b"}}

	result, err := c.Apply(context.Background(), m, parts, nil)
	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Equal(t, api.ResultScheduled, result.Mode)
	require.NotNil(t, result.Error)
	assert.Equal(t, "in_place", result.Error.Stage)
	assert.Empty(t, result.OpResults, "the batch never reached the worker")

	state := agent.State()
	assert.Empty(t, state.AppliedPlanID, "the next apply must be full state plus a reload")
	assert.Empty(t, state.WorkerOpsPlanID)
	assert.Equal(t, "global\n  nbthread 4\n", string(agent.Applies()[1].Parts["haproxy.cfg"]))
	assert.Equal(t, renderplan.DigestString("global\n  nbthread 4\n"), state.Files["haproxy.cfg"].Digest)
}

// TestScheduledApplyWithoutInPlaceOpsKeepsTheWorkerBaseline pins that nothing
// but an executed batch moves the worker-ops plan id, which is what the next
// in-place apply is fenced against.
func TestScheduledApplyWithoutInPlaceOpsKeepsTheWorkerBaseline(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t)
	c := newClient(t, agent)
	seed(t, c)
	agent.SetReloadPending(true)

	m, parts := build("plan-2", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 2}, map[string]string{
		"haproxy.cfg":   "global\n  nbthread 4\n",
		"maps/host.map": "example.com be-1\n",
	})
	m.ExpectedPrevPlanID = "plan-1"
	m.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 1}

	result, err := c.Apply(context.Background(), m, parts, nil)
	require.NoError(t, err)
	assert.Equal(t, api.ResultScheduled, result.Mode)
	assert.Equal(t, "plan-2", result.AppliedPlanID)
	assert.Equal(t, "plan-1", result.WorkerOpsPlanID, "no in-place op ran, so the worker is where it was")
	require.NotNil(t, result.Reload)
	assert.Equal(t, agent.State().ReloadPendingAt, result.Reload.ScheduledAt)
}

// TestMissingPartsAreResolvedByPath pins that holding a digest under one path
// does not satisfy another: the agent stores files by path, so the deployer's
// resend loop has to run for a new path with familiar bytes.
func TestMissingPartsAreResolvedByPath(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t)
	c := newClient(t, agent)
	seed(t, c)

	m, _ := build("plan-2", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 2}, map[string]string{
		"haproxy.cfg":    "global\n",
		"maps/host.map":  "example.com be-1\n",
		"maps/other.map": "example.com be-1\n",
	})
	m.ExpectedPrevPlanID = "plan-1"
	m.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 1}

	_, err := c.Apply(context.Background(), m, nil, nil)
	var missing *client.MissingError
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, []string{"maps/other.map"}, missing.Missing)
}

func TestRejectedOpNACKsAndInvalidatesTheBaseline(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t)
	c := newClient(t, agent)
	seed(t, c)
	agent.RejectOp(api.OpServerAdd)

	m, parts := build("plan-2", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 2}, map[string]string{
		"haproxy.cfg":   "global\n",
		"maps/host.map": "example.com be-1\n",
	})
	m.ExpectedPrevPlanID = "plan-1"
	m.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 1}
	m.Ops = []api.Op{{Kind: api.OpServerAdd, Backend: "be-1", Server: "srv2", Address: "10.0.0.2", Port: 8080}}

	result, err := c.Apply(context.Background(), m, parts, nil)
	require.NoError(t, err, "a NACK is an answer, not a transport error")
	assert.False(t, result.OK)
	assert.Equal(t, api.ResultRejected, result.Mode)
	require.NotNil(t, result.Error)
	assert.Equal(t, "ops", result.Error.Stage)
	assert.Contains(t, result.Error.Message, api.OpServerAdd)
	assert.Empty(t, agent.State().AppliedPlanID, "the next apply must be full state plus a reload")
}

func TestRevertLKGRestoresTheLastKnownGoodSet(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t)
	c := newClient(t, agent)
	seed(t, c)

	m, parts := build("plan-2", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 2}, map[string]string{
		"haproxy.cfg":   "global\n",
		"maps/host.map": "example.com be-2\n",
	})
	m.ExpectedPrevPlanID = "plan-1"
	m.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 1}
	m.Ops = []api.Op{{Kind: api.OpMapSet, Path: "maps/host.map", Key: "example.com", Value: "be-2"}}
	_, err := c.Apply(context.Background(), m, parts, nil)
	require.NoError(t, err)

	revert := &api.Manifest{
		PlanID:             "plan-3",
		PlanSchemaVersion:  1,
		Token:              api.Token{LeaderEpoch: 1, RenderSeq: 3},
		ExpectedPrevPlanID: "plan-2",
		ExpectedPrevToken:  api.Token{LeaderEpoch: 1, RenderSeq: 2},
		Mode:               api.ModeRevertLKG,
	}
	result, err := c.Apply(context.Background(), revert, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, api.ResultReload, result.Mode)
	assert.Equal(t, "plan-1", result.AppliedPlanID)
	assert.Equal(t, "plan-1", result.RunningPlanID)

	state := agent.State()
	assert.Equal(t, renderplan.DigestString("example.com be-1\n"), state.Files["maps/host.map"].Digest)
}

func TestLKGPromotionFollowsTheValidatedPlan(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t)
	c := newClient(t, agent)
	seed(t, c)

	m, parts := build("plan-2", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 2}, map[string]string{
		"haproxy.cfg":   "global\n",
		"maps/host.map": "example.com be-2\n",
	})
	m.ExpectedPrevPlanID = "plan-1"
	m.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 1}
	m.Ops = []api.Op{{Kind: api.OpMapSet, Path: "maps/host.map", Key: "example.com", Value: "be-2"}}
	_, err := c.Apply(context.Background(), m, parts, nil)
	require.NoError(t, err)
	assert.Equal(t, "plan-1", agent.State().LKGPlanID)

	noop := &api.Manifest{
		PlanID:             "plan-2",
		PlanSchemaVersion:  1,
		Token:              api.Token{LeaderEpoch: 1, RenderSeq: 3},
		ExpectedPrevPlanID: "plan-2",
		ExpectedPrevToken:  api.Token{LeaderEpoch: 1, RenderSeq: 2},
		ValidatedPlanID:    "plan-2",
		Mode:               api.ModeAuto,
		Files:              m.Files,
	}
	_, err = c.Apply(context.Background(), noop, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "plan-2", agent.State().LKGPlanID)
}

func TestPartDigestMismatchIsRefused(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t)
	c := newClient(t, agent)

	m, _ := build("plan-1", api.ModeReload, api.Token{LeaderEpoch: 1, RenderSeq: 1}, map[string]string{
		"haproxy.cfg": "global\n",
	})
	// Same length, different bytes: the client's own size check must not fire
	// before the fake gets to verify the digest.
	result, err := c.Apply(context.Background(), m,
		map[string]io.Reader{"haproxy.cfg": strings.NewReader("globaX\n")}, nil)
	require.NoError(t, err)
	assert.False(t, result.OK)
	require.NotNil(t, result.Error)
	assert.Equal(t, "verify", result.Error.Stage)
	assert.Zero(t, agent.State().Generation)
}

func TestWrongCredentialsAreRefused(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t, agenttest.WithCredentials("admin", "correct"))
	c, err := client.New(&client.Config{BaseURL: agent.URL(), Username: "admin", Password: "wrong"})
	require.NoError(t, err)
	t.Cleanup(c.Close)

	_, err = c.State(context.Background(), false)
	var httpErr *client.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, 401, httpErr.Status)
}

func TestReportedContractDrivesTheSkewCheck(t *testing.T) {
	t.Parallel()
	full := agenttest.New(t)
	state, err := newClient(t, full).State(context.Background(), false)
	require.NoError(t, err)
	mismatch, missing := client.CheckSkew(state)
	assert.False(t, mismatch)
	assert.Empty(t, missing)

	partial := agenttest.New(t, agenttest.WithAgentOps(api.OpMapSet, api.OpMapDel))
	state, err = newClient(t, partial).State(context.Background(), false)
	require.NoError(t, err)
	mismatch, missing = client.CheckSkew(state)
	assert.False(t, mismatch)
	assert.Contains(t, missing, api.OpServerAdd)
}

func TestInventoryOptionIsReported(t *testing.T) {
	t.Parallel()
	agent := agenttest.New(t, agenttest.WithInventory(&api.Inventory{Generation: 7, Maps: []string{"maps/host.map"}}),
		agenttest.WithHAProxyInfo(api.HAProxyInfo{Version: "3.0.26", WorkerPID: 42}))

	state, err := newClient(t, agent).State(context.Background(), true)
	require.NoError(t, err)
	assert.Equal(t, uint64(7), state.Inventory.Generation)
	assert.Equal(t, "3.0.26", state.HAProxy.Version)
	assert.Equal(t, 42, state.HAProxy.WorkerPID)
}
