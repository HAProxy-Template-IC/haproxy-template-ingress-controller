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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/agenttest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

func TestLocalAgentURL(t *testing.T) {
	tests := []struct {
		name   string
		listen string
		want   string
	}{
		{name: "a wildcard bind is reached on loopback", listen: ":5555", want: "http://127.0.0.1:5555"},
		{name: "an explicit wildcard too", listen: "0.0.0.0:5555", want: "http://127.0.0.1:5555"},
		{name: "an address is kept", listen: "10.0.0.1:5555", want: "http://10.0.0.1:5555"},
		{name: "a value with no port is passed through", listen: "agent", want: "http://agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, localAgentURL(tt.listen))
		})
	}
}

func TestFetchAgentStateNeedsTheCredentials(t *testing.T) {
	t.Setenv(agentUsernameEnv, "")
	t.Setenv(agentPasswordEnv, "")

	_, err := fetchAgentState(context.Background(), "http://127.0.0.1:5555")
	require.Error(t, err)
	assert.Contains(t, err.Error(), agentUsernameEnv)
	assert.Contains(t, err.Error(), "agent container")
}

func TestFetchAgentStateReadsTheFakeAgent(t *testing.T) {
	agent := agenttest.New(t)
	t.Setenv(agentUsernameEnv, agent.Username())
	t.Setenv(agentPasswordEnv, agent.Password())

	state, err := fetchAgentState(context.Background(), agent.URL())
	require.NoError(t, err)
	assert.Equal(t, api.Version, state.APIVersion)
	assert.Equal(t, "agenttest", state.AgentVersion)
	assert.Equal(t, "3.4.3", state.HAProxy.Version)
	assert.Equal(t, 1, agent.StateReads())
}

func TestFetchAgentStateRejectsWrongCredentials(t *testing.T) {
	agent := agenttest.New(t)
	t.Setenv(agentUsernameEnv, agent.Username())
	t.Setenv(agentPasswordEnv, "not-the-password")

	_, err := fetchAgentState(context.Background(), agent.URL())
	require.Error(t, err)
	assert.Contains(t, err.Error(), api.PathState)
}

// agentStateFixture is a pod mid-incident: its worker runs an older plan than
// it applied, a reload is pending, deletes are outstanding and the last apply
// was refused — every branch of the human output at once.
func agentStateFixture() *api.State {
	return &api.State{
		APIVersion:        api.Version,
		AgentVersion:      "0.3.0",
		PlanSchemaVersion: 1,
		AgentOps:          []string{api.OpMapSet},
		HAProxy:           api.HAProxyInfo{Version: "3.4.3", FullVersion: "3.4.3-1", WorkerPID: 42},
		Generation:        7,
		AppliedPlanID:     "plan-new",
		RunningPlanID:     "plan-old",
		WorkerOpsPlanID:   "plan-old",
		LKGPlanID:         "plan-old",
		AppliedToken:      api.Token{LeaderEpoch: 3, RenderSeq: 11},
		Files: map[string]api.FileAt{
			"haproxy.cfg":   {Digest: "aaaa", Size: 4096},
			"maps/host.map": {Digest: "bbbb", Size: 64},
		},
		Inventory:       api.Inventory{Generation: 6, Maps: []string{"maps/host.map"}, Certs: []string{"ssl/tls.pem"}},
		ReloadPendingAt: "2026-08-18T12:00:00Z",
		PendingDeletes:  api.PendingDeletes{Servers: []string{"be/s2"}, Backends: []string{"be_old"}},
		LastApply: &api.ApplyResult{
			PlanID:   "plan-new",
			OK:       false,
			Mode:     api.ResultRejected,
			At:       "2026-08-18T12:00:01Z",
			Error:    &api.ApplyError{Stage: "reload", Message: "config is invalid"},
			Reload:   &api.ReloadInfo{Performed: true, OK: false, WorkerPID: 42, TookMs: 210},
			Rollback: &api.RollbackInfo{Performed: true, Reloaded: true},
		},
	}
}

func TestPrintAgentStateHuman(t *testing.T) {
	restore := setAgentStateFlags(t)
	defer restore()

	var out bytes.Buffer
	require.NoError(t, printAgentState(&out, agentStateFixture()))
	printed := out.String()

	for _, want := range []string{
		"agent 0.3.0, api v1, plan schema 1",
		"haproxy 3.4.3, worker pid 42",
		"plans (generation 7, token 3/11)",
		"applied     plan-new",
		"running     plan-old",
		"worker ops  plan-old",
		"last good   plan-old",
		"files 2, reload pending 2026-08-18T12:00:00Z",
		"pending deletes: 1 servers, 1 backends",
		"  server be/s2",
		"  backend be_old",
		"inventory (generation 6): maps 1, certs 1, ca files 0, crl files 0, crt-lists 0",
		"last apply NACK: plan plan-new, mode rejected, 2026-08-18T12:00:01Z",
		"error at reload: config is invalid",
		"reload failed in 210ms, worker pid 42",
		"rolled back, reloaded true",
	} {
		assert.Contains(t, printed, want)
	}
	assert.NotContains(t, printed, "maps/host.map  64 bytes", "--files is what lists the tree")
}

func TestPrintAgentStateListsFiles(t *testing.T) {
	restore := setAgentStateFlags(t)
	defer restore()
	agentStateFiles = true

	var out bytes.Buffer
	require.NoError(t, printAgentState(&out, agentStateFixture()))

	assert.Contains(t, out.String(), "aaaa  haproxy.cfg  4096 bytes")
	assert.Contains(t, out.String(), "bbbb  maps/host.map  64 bytes")
	assert.Less(t, strings.Index(out.String(), "haproxy.cfg  4096"), strings.Index(out.String(), "maps/host.map  64"),
		"files are listed by path, so two reads of the same pod compare")
}

func TestPrintAgentStateWithoutAnApply(t *testing.T) {
	restore := setAgentStateFlags(t)
	defer restore()

	var out bytes.Buffer
	require.NoError(t, printAgentState(&out, &api.State{APIVersion: api.Version}))

	assert.Contains(t, out.String(), "last apply: none since this agent started")
	assert.Contains(t, out.String(), "haproxy -, worker pid 0")
}

func TestPrintAgentStateJSON(t *testing.T) {
	restore := setAgentStateFlags(t)
	defer restore()
	agentStateOutput = "json"

	var out bytes.Buffer
	require.NoError(t, printAgentState(&out, agentStateFixture()))

	var decoded api.State
	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
	assert.Equal(t, "plan-new", decoded.AppliedPlanID)
	assert.Equal(t, "plan-old", decoded.RunningPlanID)
	require.NotNil(t, decoded.LastApply)
	assert.Equal(t, "config is invalid", decoded.LastApply.Error.Message)
}

func TestPrintAgentStateRejectsAnUnknownFormat(t *testing.T) {
	restore := setAgentStateFlags(t)
	defer restore()
	agentStateOutput = "yaml"

	err := printAgentState(&bytes.Buffer{}, agentStateFixture())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "use human or json")
}

// setAgentStateFlags puts the subcommand's package-level flags back to their
// defaults for one test and restores whatever they held.
func setAgentStateFlags(t *testing.T) func() {
	t.Helper()
	previousOutput, previousFiles, previousVerify := agentStateOutput, agentStateFiles, agentStateVerify
	agentStateOutput, agentStateFiles, agentStateVerify = "human", false, false
	return func() {
		agentStateOutput, agentStateFiles, agentStateVerify = previousOutput, previousFiles, previousVerify
	}
}
