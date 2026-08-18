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
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// converged brings a fresh pod onto the rendered configuration so a test can
// start from a known running plan.
func converged(t *testing.T) (*env, *session) {
	t.Helper()
	e := newEnv(t)
	s := newSession(e)
	result := s.apply(s.next(api.ModeReload), s.allParts())
	require.True(t, result.OK, "first apply was rejected: %+v", result.Error)
	e.waitForReady(http.StatusOK)
	return e, s
}

// TestRuntimeNamesAreTheManifestPaths pins the convention the whole op surface
// rests on: under `default-path origin`, HAProxy names a map, a certificate and
// a crt-list by the literal base-relative string the config references, which is
// the manifest File.Path and therefore the Op.Path. No translation anywhere.
func TestRuntimeNamesAreTheManifestPaths(t *testing.T) {
	e, _ := converged(t)

	loaded := e.worker("show map")
	assert.Contains(t, loaded, "("+hostMapPath+")")
	assert.Contains(t, loaded, "("+noteMapPath+")")
	assert.NotContains(t, loaded, baseDir+"/"+hostMapPath,
		"an absolute runtime name would mean the agent has to translate paths")

	assert.Contains(t, e.worker("show ssl cert"), defaultCertPath)
	assert.Contains(t, e.worker("show ssl crt-list"), crtListPath)
}

func TestMapOpsRunAtRuntimeAndKeepEveryByte(t *testing.T) {
	e, s := converged(t)
	worker := e.workerPID()

	// Values with a space and a ';': the CLI's line form truncates at the space
	// and executes everything past the ';' as a second command, so only the
	// payload form (map_add) can store these — map_set is for line-safe values
	// and a changed unsafe value travels as map_del + map_add, exactly as
	// deployplan composes it.
	const changed = "changed; value with spaces"
	const added = "added; another value"
	const retouched = "first-value-retouched"
	s.set(noteMapPath, "a.example.com "+retouched+"\nb.example.com "+changed+"\nd.example.com "+added+"\n")

	m := s.next(api.ModeAuto)
	m.Ops = []api.Op{
		{Kind: api.OpMapSet, Path: noteMapPath, Key: "a.example.com", Value: retouched},
		{Kind: api.OpMapDel, Path: noteMapPath, Key: "b.example.com"},
		{Kind: api.OpMapAdd, Path: noteMapPath, Key: "b.example.com", Value: changed},
		{Kind: api.OpMapAdd, Path: noteMapPath, Key: "d.example.com", Value: added},
		{Kind: api.OpMapDel, Path: noteMapPath, Key: "c.example.com"},
	}
	result := s.apply(m, s.allParts())
	require.True(t, result.OK, "map apply was rejected: %+v", result.Error)
	assert.Equal(t, api.ResultRuntime, result.Mode)
	assert.Equal(t, worker, e.workerPID(), "a map-only apply must not reload")
	assert.Equal(t, result.PlanID, result.AppliedPlanID)

	entries := mapEntries(e.worker("show map " + noteMapPath))
	assert.Equal(t, map[string]string{
		"a.example.com": retouched,
		"b.example.com": changed,
		"d.example.com": added,
	}, entries)

	_, header, _ := e.requestWithHost("b.example.com", notePath)
	assert.Equal(t, changed, header.Get("x-note"), "the running worker must serve the new value")
	assert.Equal(t, s.files[noteMapPath], e.read(noteMapPath), "the file on disk must match the desired set")
}

// A versioned replace pushes the entries the plan declares, never the file's
// own bytes: HAProxy's payload parser has no comment syntax, so a '#' header
// would be stored as a key, and a blank line ends the default payload block.
func TestMapReplaceInstallsExactlyTheEntries(t *testing.T) {
	e, s := converged(t)
	worker := e.workerPID()

	// The shape every chart-rendered map has: a header, blank lines between
	// the per-library snippets, and a TAB-separated entry.
	s.set(noteMapPath, "# host to note mapping\n\n"+
		"a.example.com first value\n\n"+
		"# ingress/default\n"+
		"b.example.com replaced; value\n"+
		"c.example.com\tthird value\n")
	m := s.next(api.ModeAuto)
	m.Ops = []api.Op{{Kind: api.OpMapReplace, Path: noteMapPath}}
	result := s.apply(m, s.allParts())

	require.True(t, result.OK, "map_replace was rejected: %+v", result.Error)
	assert.Equal(t, api.ResultRuntime, result.Mode)
	assert.Equal(t, worker, e.workerPID(), "a map replace must not reload")
	assert.Equal(t, map[string]string{
		"a.example.com": "first value",
		"b.example.com": "replaced; value",
		"c.example.com": "third value",
	}, mapEntries(e.worker("show map "+noteMapPath)), "the running map must be the plan's entries")

	_, header, _ := e.requestWithHost("b.example.com", notePath)
	assert.Equal(t, "replaced; value", header.Get("x-note"), "the running worker must serve the new value")

	// The read-back compares that same file against `show map`; a divergence
	// would reload, so an unchanged worker pid is what proves they agree.
	state, err := e.client.State(context.Background(), false)
	require.NoError(t, err)
	assert.Equal(t, result.PlanID, state.AppliedPlanID)
	assert.Equal(t, worker, e.workerPID(), "the read-back found no divergence")
}

func TestServerOpsRunAtRuntime(t *testing.T) {
	e, s := converged(t)
	worker := e.workerPID()

	added := s.next(api.ModeAuto)
	added.Ops = []api.Op{
		{
			Kind: api.OpServerAdd, Backend: "be-1", Server: "srv2",
			Address: "127.0.0.1", Port: upstreamPort,
			Keywords: []api.KeywordArg{{Name: "check"}, {Name: "weight", Args: []string{"10"}}},
		},
		{Kind: api.OpServerEnable, Backend: "be-1", Server: "srv2", Health: true},
	}
	result := s.apply(added, nil)
	require.True(t, result.OK, "server_add was rejected: %+v", result.Error)
	assert.Equal(t, api.ResultRuntime, result.Mode)
	assert.Equal(t, worker, e.workerPID(), "adding a server must not reload")
	assert.Equal(t, "10", e.mustStatRow("be-1", "srv2")["weight"])

	weight := 5
	reweighted := s.next(api.ModeAuto)
	reweighted.Ops = []api.Op{{Kind: api.OpServerSetWeight, Backend: "be-1", Server: "srv2", Weight: &weight}}
	result = s.apply(reweighted, nil)
	require.True(t, result.OK, "server_set_weight was rejected: %+v", result.Error)
	assert.Equal(t, strconv.Itoa(weight), e.mustStatRow("be-1", "srv2")["weight"])

	drained := s.next(api.ModeAuto)
	drained.Ops = []api.Op{{Kind: api.OpServerSetState, Backend: "be-1", Server: "srv2", State: "maint"}}
	result = s.apply(drained, nil)
	require.True(t, result.OK, "server_set_state was rejected: %+v", result.Error)
	assert.Contains(t, e.mustStatRow("be-1", "srv2")["status"], "MAINT")

	// An idle keep-alive client holds a connection to the frontend while the
	// server is removed: `disable server` empties the idle pool, so the wait
	// returns instead of expiring.
	idle := keepAliveClient(t, e)
	defer idle.CloseIdleConnections()

	removed := s.next(api.ModeAuto)
	removed.Ops = []api.Op{
		{Kind: api.OpServerDisable, Backend: "be-1", Server: "srv2"},
		{Kind: api.OpServerWaitRemovable, Backend: "be-1", Server: "srv2", TimeoutMs: 3000},
		{Kind: api.OpServerDel, Backend: "be-1", Server: "srv2"},
	}
	result = s.apply(removed, nil)
	require.True(t, result.OK, "the delete sequence was rejected: %+v", result.Error)
	assert.Equal(t, worker, e.workerPID(), "the whole server lifecycle must stay reload-free")
	_, present := e.statRow("be-1", "srv2")
	assert.False(t, present, "srv2 is still in show stat")
}

// keepAliveClient leaves one pooled connection to the HTTP frontend open, the
// state a real client keeps between requests.
func keepAliveClient(t *testing.T, e *env) *http.Client {
	t.Helper()
	c := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{MaxIdleConnsPerHost: 2},
	}
	for range 4 {
		resp, err := c.Get(e.httpURL("/"))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
	return c
}

func TestDynamicBackendLifecycle(t *testing.T) {
	if !haproxyAtLeast("3.4") {
		t.Skipf("`add backend` arrived in HAProxy 3.4; this bracket runs %s", haproxyVersion())
	}
	e, s := converged(t)
	worker := e.workerPID()

	const host = "b3.example.com"
	s.set(hostMapPath, hostMapContent+host+" be-3\n")
	published := s.next(api.ModeAuto)
	published.Ops = []api.Op{
		{Kind: api.OpBackendAdd, Backend: "be-3", Profile: defaultsProfile, Mode: "http", GUID: "be:be-3"},
		{
			Kind: api.OpServerAdd, Backend: "be-3", Server: "srv1",
			Address: "127.0.0.1", Port: upstreamPort,
			Keywords: []api.KeywordArg{{Name: "check"}},
		},
		{Kind: api.OpServerEnable, Backend: "be-3", Server: "srv1", Health: true},
		{Kind: api.OpBackendPublish, Backend: "be-3"},
		{Kind: api.OpMapAdd, Path: hostMapPath, Key: host, Value: "be-3"},
	}
	result := s.apply(published, s.allParts())
	require.True(t, result.OK, "publishing a dynamic backend was rejected: %+v", result.Error)
	assert.Equal(t, api.ResultRuntime, result.Mode)
	assert.Equal(t, worker, e.workerPID())

	status, _, body := e.requestWithHost(host, "/")
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "upstream-ok", body, "traffic must reach the dynamic backend")

	s.set(hostMapPath, hostMapContent)
	retired := s.next(api.ModeAuto)
	retired.Ops = []api.Op{
		{Kind: api.OpMapDel, Path: hostMapPath, Key: host},
		{Kind: api.OpBackendUnpublish, Backend: "be-3"},
		{Kind: api.OpServerDisable, Backend: "be-3", Server: "srv1"},
		{Kind: api.OpServerWaitRemovable, Backend: "be-3", Server: "srv1", TimeoutMs: 3000},
		{Kind: api.OpServerDel, Backend: "be-3", Server: "srv1"},
		{Kind: api.OpBackendWaitRemovable, Backend: "be-3", TimeoutMs: 3000},
		{Kind: api.OpBackendDel, Backend: "be-3"},
	}
	result = s.apply(retired, s.allParts())
	require.True(t, result.OK, "retiring a dynamic backend was rejected: %+v", result.Error)
	assert.Equal(t, worker, e.workerPID())
	// The wait + del tail runs off the apply path (deferred deletes), so the
	// backend disappears shortly after the ACK, never inside it.
	waitFor(t, "the deferred delete of be-3", convergeBudget, func() error {
		if _, present := e.statRow("be-3", "BACKEND"); present {
			return errors.New("be-3 is still in show stat")
		}
		return nil
	})
	assert.Equal(t, worker, e.workerPID(), "deferred deletes must not reload")
	state, err := e.client.State(context.Background(), false)
	require.NoError(t, err)
	assert.Empty(t, state.PendingDeletes.Servers)
	assert.Empty(t, state.PendingDeletes.Backends)
}
