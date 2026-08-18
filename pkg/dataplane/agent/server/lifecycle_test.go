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
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/haproxytest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/server"
)

func TestARestartBetweenAppliesKeepsTheBaseline(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)
	restarted := h.restart()

	state := restarted.state(false)
	assert.Equal(t, "plan-1", state.AppliedPlanID)
	assert.Equal(t, first.AppliedToken, state.AppliedToken)

	files := baseFiles("global\n  maxconn 900\n")
	m := buildManifest("plan-2", files)
	m.ExpectedPrevPlanID = state.AppliedPlanID
	m.ExpectedPrevToken = state.AppliedToken
	m.Mode = api.ModeReload
	assert.True(t, restarted.apply(&m, files).OK)
}

func TestATreeThatChangedWhileTheAgentWasAwayInvalidatesTheBaseline(t *testing.T) {
	h := newHarness(t)
	firstApply(t, h)
	h.stop()

	require.NoError(t, os.WriteFile(filepath.Join(h.baseDir, configPath), []byte("bootstrap\n"), 0o600))
	restarted := newHarness(t, withBaseDir(h.baseDir), withModel(h.model))

	assert.Empty(t, restarted.state(false).AppliedPlanID)
}

func TestAnInterruptedApplyReloadsWhatIsOnDisk(t *testing.T) {
	h := newHarness(t)
	firstApply(t, h)
	h.stop()

	// A crash in the op phase leaves the tree written but the runtime half done.
	statePath := filepath.Join(h.baseDir, ".haptic-agent.json")
	raw, err := os.ReadFile(statePath)
	require.NoError(t, err)
	patched := insertJSON(t, raw, `"phase":"written","in_flight_plan_id":"plan-2",`)
	require.NoError(t, os.WriteFile(statePath, patched, 0o600))

	var pidBefore int
	h.model.With(func(m *haproxytest.Model) { pidBefore = m.Pid })
	restarted := newHarness(t, withBaseDir(h.baseDir), withModel(h.model))

	assert.Empty(t, restarted.state(false).AppliedPlanID, "an interrupted apply leaves an unknown baseline")
	h.model.With(func(m *haproxytest.Model) {
		assert.Greater(t, m.Pid, pidBefore, "the recovery reload adopts whatever is on disk")
	})
}

func TestAForeignWorkerFallsBackToAReload(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)

	// The HAProxy container restarted: same sockets, a different process.
	h.model.With(func(m *haproxytest.Model) { m.Pid += 7 })

	files := baseFiles("global\n  maxconn 1000\n")
	m := buildManifest("plan-2", files)
	m.ExpectedPrevPlanID = first.AppliedPlanID
	m.ExpectedPrevToken = first.AppliedToken
	m.Ops = []api.Op{{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "b.example.com", Value: "be-a"}}

	result := h.apply(&m, files)
	require.True(t, result.OK, "%+v", result.Error)
	assert.Equal(t, api.ResultReload, result.Mode)
}

// Drift prevention polls /v1/state?verify=1; a restarted HAProxy container
// must show up there as the new worker and an unknown baseline, not only on
// the next apply.
func TestStateVerifyObservesAForeignWorker(t *testing.T) {
	h := newHarness(t)
	firstApply(t, h)
	before := h.state(false).HAProxy.WorkerPID

	h.model.With(func(m *haproxytest.Model) { m.Pid += 7 })

	assert.Equal(t, before, h.state(false).HAProxy.WorkerPID, "a plain GET reports the last observation")
	verified := h.state(true)
	assert.Equal(t, before+7, verified.HAProxy.WorkerPID)
	assert.Empty(t, verified.AppliedPlanID, "a foreign worker means the runtime baseline is gone")
}

func TestAManifestOverTheOpLimitIsRefused(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)

	files := baseFiles("global\n  maxconn 1100\n")
	m := buildManifest("plan-2", files)
	m.ExpectedPrevPlanID = first.AppliedPlanID
	m.ExpectedPrevToken = first.AppliedToken
	for i := 0; i <= api.MaxOpsPerApply; i++ {
		m.Ops = append(m.Ops, api.Op{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "k", Value: "v"})
	}

	status, raw := h.post(&m, files)
	require.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, string(raw), "op limit")
	assert.Equal(t, "global\n", h.read(configPath))
}

func TestGeneralOnItsOwnMountIsWrittenAndRolledBack(t *testing.T) {
	h := newHarness(t)
	files := []file{
		{Path: configPath, Content: "global\n", Reload: true},
		{Path: "general/503.http", Content: "HTTP/1.0 503\n", Kind: api.FileKindGeneral},
	}
	m := buildManifest("plan-1", files)
	m.Mode = api.ModeReload
	first := h.apply(&m, files)
	require.True(t, first.OK, "%+v", first.Error)
	assert.Equal(t, "HTTP/1.0 503\n", h.read("general/503.http"))

	h.model.With(func(mod *haproxytest.Model) { mod.ReloadFails = true })
	next := []file{
		{Path: configPath, Content: "nonsense\n", Reload: true},
		{Path: "general/503.http", Content: "HTTP/1.0 500\n", Kind: api.FileKindGeneral},
	}
	bad := buildManifest("plan-2", next)
	bad.Mode = api.ModeReload
	bad.ExpectedPrevPlanID = first.AppliedPlanID
	bad.ExpectedPrevToken = first.AppliedToken

	result := h.apply(&bad, next)
	require.False(t, result.OK)
	assert.Equal(t, "global\n", h.read(configPath))
	assert.Equal(t, "HTTP/1.0 503\n", h.read("general/503.http"), "the second mount rolls back with the first")
}

// insertJSON splices fields into the agent's state file without needing the
// unexported type the server writes.
func insertJSON(t *testing.T, raw []byte, fields string) []byte {
	t.Helper()
	require.Greater(t, len(raw), 1)
	return append([]byte("{"+fields), raw[1:]...)
}

func TestAReloadTimeoutOverTheAPILimitIsRefused(t *testing.T) {
	_, err := server.New(t.Context(), &server.Config{
		BaseDir:       t.TempDir(),
		ConfigFile:    configPath,
		StateFile:     ".haptic-agent.json",
		Listen:        "127.0.0.1:0",
		ReloadTimeout: server.DefaultReloadTimeout + time.Second,
		Username:      testUser,
		Password:      testPassword,
		Logger:        slog.New(slog.DiscardHandler),
	})
	require.ErrorContains(t, err, "--reload-timeout must be between 0 and 1m0s")
}
