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

//go:build agentdocker

package agent

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
)

func TestBadConfigNACKsAndLeavesTheOldWorkerServing(t *testing.T) {
	e, s := converged(t)
	worker := e.workerPID()
	goodConfig := e.digest(configPath)
	goodPlan := s.applied

	s.set(configPath, brokenConfig)
	result := s.apply(s.next(api.ModeReload), s.allParts())
	require.False(t, result.OK, "HAProxy must refuse a config carrying %s", brokenDirective)
	require.NotNil(t, result.Error)
	assert.Contains(t, result.Error.Message, brokenDirective, "the NACK must carry HAProxy's own words")
	assert.Equal(t, goodPlan, result.LKGPlanID, "the last known good plan must not move")

	assert.Equal(t, worker, e.workerPID(), "the old worker must keep serving")
	assert.Equal(t, goodConfig, e.digest(configPath), "the disk must be back on the last known good set")
	e.waitForReady(http.StatusOK)

	fixed := renderedConfig + "\nbackend be-fixed from " + defaultsProfile +
		"\n    http-request return status 200 content-type text/plain string \"be-fixed\"\n"
	s.set(configPath, fixed)
	result = s.apply(s.next(api.ModeReload), s.allParts())
	require.True(t, result.OK, "the repaired config was rejected: %+v", result.Error)
	e.waitForReady(http.StatusOK)
	assert.Equal(t, fixed, e.read(configPath))
	assert.NotEqual(t, worker, e.workerPID())
}

func TestStaleBaselineIsRefusedWithoutWriting(t *testing.T) {
	e, s := converged(t)
	before := e.digest(noteMapPath)

	s.set(noteMapPath, "z.example.com must-not-land\n")
	stale := s.next(api.ModeReload)
	stale.ExpectedPrevPlanID = "plan-from-another-leader"

	err := s.applyExpectingRefusal(t, stale, s.allParts())
	var conflict *client.ConflictError
	require.ErrorAs(t, err, &conflict, "a baseline mismatch must be a 409 conflict")
	assert.Equal(t, s.applied, conflict.Conflict.AppliedPlanID)
	assert.NotEmpty(t, conflict.Conflict.Reason)
	assert.Equal(t, before, e.digest(noteMapPath), "a refused apply must never write")
}

func TestMissingPartsAreListedThenResent(t *testing.T) {
	e := newEnv(t)
	s := newSession(e)

	m := s.next(api.ModeReload)
	err := s.applyExpectingRefusal(t, m, nil)
	var missing *client.MissingError
	require.ErrorAs(t, err, &missing, "an apply without content the agent lacks must be a 409 missing")
	assert.Contains(t, missing.Missing, configPath)

	result := s.apply(m, s.allParts())
	require.True(t, result.OK, "the resend was rejected: %+v", result.Error)
	e.waitForReady(http.StatusOK)
	assert.Equal(t, renderedConfig, e.read(configPath))
}

func TestHAProxyRestartConvergesOnTheNextApply(t *testing.T) {
	e, s := converged(t)

	// The container comes back on the bootstrap config with a new worker, which
	// is what a kubelet restart of the haproxy container leaves behind.
	e.restartHAProxy()
	e.waitForReady(http.StatusServiceUnavailable)

	state, err := e.client.State(context.Background(), true)
	require.NoError(t, err)
	assert.Equal(t, e.workerPID(), state.HAProxy.WorkerPID, "the agent must observe the new worker")

	result := applyConverging(t, s)
	require.True(t, result.OK, "the recovery apply was rejected: %+v", result.Error)
	e.waitForReady(http.StatusOK)
	assert.Equal(t, renderedConfig, e.read(configPath))
	assert.Equal(t, result.PlanID, result.RunningPlanID)
}

// applyConverging sends the desired set and, if the agent refuses because its
// baseline moved, re-diffs from the state the 409 carries — the deployer's own
// recovery, in two lines.
func applyConverging(t *testing.T, s *session) *api.ApplyResult {
	t.Helper()
	m := s.next(api.ModeReload)
	result, err := s.env.client.Apply(context.Background(), m, s.allParts(), nil)
	if err == nil {
		s.absorb(result)
		return result
	}
	var conflict *client.ConflictError
	require.ErrorAs(t, err, &conflict, "the only refusal a restart may cause is a baseline conflict")
	s.applied = conflict.Conflict.AppliedPlanID
	s.token = conflict.Conflict.AppliedToken
	s.workerOps = conflict.Conflict.WorkerOpsPlanID
	return s.apply(s.next(api.ModeReload), s.allParts())
}

func TestRevertLKGRestoresTheLastKnownGoodSet(t *testing.T) {
	e, s := converged(t)
	goodPlan := s.applied
	goodMap := e.read(noteMapPath)
	worker := e.workerPID()

	s.set(noteMapPath, "a.example.com first value\nb.example.com runtime-only\n")
	runtime := s.next(api.ModeAuto)
	runtime.Ops = []api.Op{{Kind: api.OpMapSet, Path: noteMapPath, Key: "b.example.com", Value: "runtime-only"}}
	result := s.apply(runtime, s.allParts())
	require.True(t, result.OK, "the runtime apply was rejected: %+v", result.Error)
	require.Equal(t, goodPlan, result.LKGPlanID, "a runtime apply must not promote the last known good plan")
	require.NotEqual(t, goodMap, e.read(noteMapPath))

	result = s.apply(s.next(api.ModeRevertLKG), nil)
	require.True(t, result.OK, "revert_lkg was rejected: %+v", result.Error)
	assert.Equal(t, api.ResultReload, result.Mode)
	assert.Equal(t, goodPlan, result.RunningPlanID)
	e.waitForReady(http.StatusOK)
	assert.Equal(t, goodMap, e.read(noteMapPath), "the last known good file set must be back")
	assert.NotEqual(t, worker, e.workerPID(), "revert_lkg reloads onto the restored set")
}
