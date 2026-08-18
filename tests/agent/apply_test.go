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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
)

func TestFreshPodServesStateAndStaysOutOfService(t *testing.T) {
	e := newEnv(t)

	status, _, err := e.get(e.agentURL() + api.PathHealthz)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)

	state, err := e.client.State(context.Background(), true)
	require.NoError(t, err)
	assert.Equal(t, api.Version, state.APIVersion)
	assert.True(t, strings.HasPrefix(state.HAProxy.Version, haproxyVersion()),
		"agent reported HAProxy %q, expected the %s bracket", state.HAProxy.Version, haproxyVersion())
	assert.Equal(t, e.workerPID(), state.HAProxy.WorkerPID)
	assert.Empty(t, state.AppliedPlanID, "a fresh pod has no baseline")

	mismatch, missing := client.CheckSkew(state)
	assert.False(t, mismatch, "the agent reports API version %d", state.APIVersion)
	assert.Empty(t, missing, "the agent must execute every op kind the controller composes")

	// The bootstrap config keeps the pod out of the Service until the
	// controller's own configuration lands.
	e.waitForReady(http.StatusServiceUnavailable)
}

func TestFirstApplyReloadsOntoTheRenderedConfig(t *testing.T) {
	e := newEnv(t)
	s := newSession(e)
	e.waitForReady(http.StatusServiceUnavailable)
	bootstrapWorker := e.workerPID()

	result := s.apply(s.next(api.ModeReload), s.allParts())
	require.True(t, result.OK, "first apply was rejected: %+v", result.Error)
	assert.Equal(t, api.ResultReload, result.Mode)
	assert.Equal(t, result.PlanID, result.AppliedPlanID)
	assert.Equal(t, result.PlanID, result.RunningPlanID)
	assert.Equal(t, result.PlanID, result.LKGPlanID)
	require.NotNil(t, result.Reload)
	assert.True(t, result.Reload.Performed)
	assert.True(t, result.Reload.OK)

	e.waitForReady(http.StatusOK)
	assert.NotEqual(t, bootstrapWorker, e.workerPID(), "the reload must hand over to a new worker")
	assert.Equal(t, renderedConfig, e.read(configPath))
	assert.Equal(t, hostMapContent, e.read(hostMapPath))

	status, _, body := e.requestWithHost("b2.example.com", "/")
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "be-2", body, "the routing map from the same apply must be live")

	state, err := e.client.State(context.Background(), true)
	require.NoError(t, err)
	assert.Equal(t, result.PlanID, state.AppliedPlanID)
	assert.Len(t, state.Files, len(s.files))
}

func TestGeneralFilesLiveOnTheirOwnMountAndRollBackWithIt(t *testing.T) {
	e := newEnv(t)
	s := newSession(e)
	good := s.apply(s.next(api.ModeReload), s.allParts())
	require.True(t, good.OK, "first apply was rejected: %+v", good.Error)
	e.waitForReady(http.StatusOK)

	assert.Equal(t, generalFileContent, e.read(generalFilePath))
	require.Len(t, e.mountPoints(), 1, "general/ must sit on a mount of its own for the per-mount journal to matter")
	require.True(t, strings.HasSuffix(e.mountPoints()[0], "/general"), "the nested mount is general/: %v", e.mountPoints())

	// One apply touches both mounts and fails on the config. The rollback has
	// to restore the general mount too, which only a journal on that mount can.
	s.set(generalFilePath, generalFileContent+" (updated)")
	s.set(configPath, brokenConfig)
	result := s.apply(s.next(api.ModeReload), s.allParts())
	require.False(t, result.OK, "HAProxy must refuse %s", brokenDirective)

	e.waitForReady(http.StatusOK)
	assert.Equal(t, generalFileContent, e.read(generalFilePath), "the general mount was not rolled back")
	assert.Equal(t, renderedConfig, e.read(configPath))
	t.Logf("config mount after the abort: %s", e.listAll(""))
	t.Logf("general mount after the abort: %s", e.listAll("general"))
}
