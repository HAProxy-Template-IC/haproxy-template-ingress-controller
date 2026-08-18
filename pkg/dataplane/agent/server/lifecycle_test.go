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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

// The pacer is the one entry into the state machine that no request drives, so
// it has to wait for startup like every other one: firing before HAProxy is up
// rolls the tree back to the last known good set for no reason.
func TestAScheduledReloadWaitsForStartupToFinish(t *testing.T) {
	baseDir := t.TempDir()
	socketDir := t.TempDir()
	backup := filepath.Join(baseDir, ".haptic-lkg", "0-backup.bak")
	require.NoError(t, os.MkdirAll(filepath.Dir(backup), 0o755))
	require.NoError(t, os.WriteFile(backup, []byte("bootstrap\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, configPath), []byte("global\n"), 0o600))
	state := fmt.Sprintf(`{"generation":1,"applied_plan_id":"plan-1","running_plan_id":"plan-1",
		"lkg_plan_id":"plan-1","manifest_paths":["haproxy.cfg"],
		"journal":{"entries":[{"path":"haproxy.cfg","kind":"modified","backup":%q}]},
		"pending_reload_plan_id":"plan-2","reload_pending_at":"2020-01-01T00:00:00Z"}`, backup)
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, ".haptic-agent.json"), []byte(state), 0o600))

	// No listener under socketDir: HAProxy has not come up yet.
	agent, err := server.New(t.Context(), &server.Config{
		BaseDir:      baseDir,
		ConfigFile:   configPath,
		MasterSocket: filepath.Join(socketDir, "haproxy-master.sock"),
		WorkerSocket: filepath.Join(socketDir, "haproxy-worker.sock"),
		StateFile:    ".haptic-agent.json",
		Listen:       "127.0.0.1:0",
		Username:     testUser,
		Password:     testPassword,
		Logger:       slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	go func() { _ = agent.Start(t.Context()) }()

	require.Never(t, func() bool {
		raw, readErr := os.ReadFile(filepath.Join(baseDir, configPath))
		return readErr != nil || string(raw) != "global\n"
	}, time.Second, 20*time.Millisecond, "the pacer rolled the tree back before the agent was ready")
	assert.False(t, agent.Ready())
}

// A reload the master socket never answered is not HAProxy's verdict on the
// config, so it must not fence the repair path for the cooldown.
func TestATransportFailureIsNotRememberedAsKnownBad(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)
	h.model.StopMaster()

	files := baseFiles("global\n  maxconn 1200\n")
	m := buildManifest("plan-2", files)
	m.Mode = api.ModeReload
	m.ExpectedPrevPlanID = first.AppliedPlanID
	m.ExpectedPrevToken = first.AppliedToken
	result := h.apply(&m, files)

	require.False(t, result.OK)
	require.NotNil(t, result.Error)
	assert.NotEmpty(t, result.Error.Message, "the operator needs the reason the reload never happened")
	require.NotNil(t, result.Reload)
	assert.False(t, result.Reload.Performed, "HAProxy never saw this configuration")

	attempts := h.metric("haptic_agent_reloads_total", "failed")
	retry := buildManifest("plan-2", files)
	retry.Mode = api.ModeReload
	require.False(t, h.apply(&retry, files).OK)
	assert.Greater(t, h.metric("haptic_agent_reloads_total", "failed"), attempts,
		"the retry of a transport failure must reach HAProxy again")
}

// The read-back's own reload clears the backup journal on disk. A state file
// that still names those backups makes the next rollback delete files it
// cannot put back.
func TestADivergenceReloadPersistsTheClearedJournal(t *testing.T) {
	h := newHarness(t)
	first := firstApply(t, h)
	h.model.With(func(m *haproxytest.Model) {
		m.Reject = func(command string) (string, bool) {
			return "No such map file.", strings.HasPrefix(command, "show map maps/")
		}
	})

	files := baseFiles("global\n")
	files[1].Content = "example.com be-a\nb.example.com be-b\n"
	m := buildManifest("plan-2", files)
	m.ExpectedPrevPlanID = first.AppliedPlanID
	m.ExpectedPrevToken = first.AppliedToken
	m.Ops = []api.Op{{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "b.example.com", Value: "be-b"}}
	result := h.apply(&m, files)
	require.True(t, result.OK, "%+v", result.Error)
	require.Equal(t, api.ResultRuntime, result.Mode)

	require.Eventually(t, func() bool {
		return h.metric("haptic_runtime_map_divergence_total") == 1
	}, 10*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		journal, _ := h.persisted()["journal"].(map[string]any)
		return len(journal) == 0
	}, 10*time.Second, 20*time.Millisecond, "the reload cleared the journal; the state file must say so")
}

// insertJSON splices fields into the agent's state file without needing the
// unexported type the server writes.
func insertJSON(t *testing.T, raw []byte, fields string) []byte {
	t.Helper()
	require.Greater(t, len(raw), 1)
	return append([]byte("{"+fields), raw[1:]...)
}
