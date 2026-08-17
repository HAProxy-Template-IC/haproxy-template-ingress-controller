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

package cli_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/agenttest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/cli"
)

func newClient(t *testing.T) (*cli.Client, *agenttest.HAProxy) {
	t.Helper()
	model := agenttest.Start(t)
	client, err := cli.New(t.Context(), cli.Config{
		WorkerSocket: model.WorkerSocket(),
		MasterSocket: model.MasterSocket(),
		Logger:       slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	return client, model
}

func content(files map[string]string) cli.Content {
	return func(path string) ([]byte, error) {
		body, ok := files[path]
		if !ok {
			return nil, fmt.Errorf("no content for %s", path)
		}
		return []byte(body), nil
	}
}

func compileAll(t *testing.T, ops []api.Op, files map[string]string) []cli.Program {
	t.Helper()
	programs := make([]cli.Program, 0, len(ops))
	for i := range ops {
		program, err := cli.Compile(&ops[i], content(files))
		require.NoError(t, err)
		programs = append(programs, program)
	}
	return programs
}

func TestExecuteRunsTheOrderedCreateSequence(t *testing.T) {
	client, model := newClient(t)
	ops := []api.Op{
		{Kind: api.OpBackendAdd, Backend: "be-a", Profile: "prof", Mode: "http", GUID: "g1"},
		{Kind: api.OpServerAdd, Backend: "be-a", Server: "srv1", Address: "10.0.0.1", Port: 8080,
			Keywords: []api.KeywordArg{{Name: "check"}}},
		{Kind: api.OpServerEnable, Backend: "be-a", Server: "srv1", Health: true},
		{Kind: api.OpBackendPublish, Backend: "be-a"},
		{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "example.com", Value: "be-a"},
	}

	results, err := client.Execute(compileAll(t, ops, nil))
	require.NoError(t, err)
	require.Len(t, results, len(ops))
	for _, r := range results {
		assert.True(t, r.OK, "%s: %s", r.Kind, r.Output)
	}

	model.With(func(m *agenttest.Model) {
		backend := m.Backends["be-a"]
		require.NotNil(t, backend)
		assert.Equal(t, "prof", backend.Profile)
		assert.Equal(t, "http", backend.Mode)
		assert.Equal(t, "g1", backend.GUID)
		assert.True(t, backend.Published)
		require.Len(t, backend.Servers, 1)
		assert.True(t, backend.Servers[0].Enabled)
		assert.True(t, backend.Servers[0].Health)
	})
	assert.Equal(t, []agenttest.MapEntry{{Key: "example.com", Value: "be-a"}}, model.MapEntries("maps/host.map"))
}

func TestExecuteReportsANameCollision(t *testing.T) {
	client, model := newClient(t)
	add := []api.Op{{Kind: api.OpBackendAdd, Backend: "be-a", Profile: "prof", Mode: "http"}}
	_, err := client.Execute(compileAll(t, add, nil))
	require.NoError(t, err)

	results, err := client.Execute(compileAll(t, add, nil))
	require.ErrorIs(t, err, cli.ErrNameCollision)
	require.Len(t, results, 1)
	assert.False(t, results[0].OK)
	assert.Contains(t, results[0].Output, "already used by other proxy")
	model.With(func(m *agenttest.Model) { assert.Len(t, m.Backends, 1) })
}

func TestExecuteStopsAtTheFirstRejection(t *testing.T) {
	client, model := newClient(t)
	ops := []api.Op{
		{Kind: api.OpBackendAdd, Backend: "be-a", Profile: "prof", Mode: "http"},
		{Kind: api.OpServerAdd, Backend: "missing", Server: "srv1", Address: "10.0.0.1"},
		{Kind: api.OpBackendPublish, Backend: "be-a"},
	}
	results, err := client.Execute(compileAll(t, ops, nil))
	require.ErrorIs(t, err, cli.ErrRejected)
	assert.True(t, results[0].OK)
	assert.False(t, results[1].OK)
	assert.False(t, results[2].OK, "an op after the failure has an unknown outcome")
	assert.True(t, model.HasBackend("be-a"), "the op before the failure did run")
}

func TestExecuteBatchesCommandsWithinTheLineLimit(t *testing.T) {
	client, model := newClient(t)
	setup := []api.Op{{Kind: api.OpBackendAdd, Backend: "be-a", Profile: "prof", Mode: "http"}}
	_, err := client.Execute(compileAll(t, setup, nil))
	require.NoError(t, err)

	var ops []api.Op
	for i := 0; i < 400; i++ {
		ops = append(ops, api.Op{
			Kind: api.OpServerAdd, Backend: "be-a", Server: fmt.Sprintf("srv%03d", i),
			Address: "10.0.0.1", Port: 8080,
		})
	}
	_, err = client.Execute(compileAll(t, ops, nil))
	require.NoError(t, err)
	assert.Len(t, model.ServerNames("be-a"), 400)
}

func TestExecuteSwitchesAMapAtomically(t *testing.T) {
	client, model := newClient(t)
	seed := []api.Op{{Kind: api.OpMapAdd, Path: "m", Key: "old", Value: "1"}}
	_, err := client.Execute(compileAll(t, seed, nil))
	require.NoError(t, err)

	var body strings.Builder
	for i := 0; i < 1500; i++ {
		fmt.Fprintf(&body, "key-%04d value-%04d\n", i, i)
	}
	replace := []api.Op{{Kind: api.OpMapReplace, Path: "m"}}
	_, err = client.Execute(compileAll(t, replace, map[string]string{"m": body.String()}))
	require.NoError(t, err)

	entries := model.MapEntries("m")
	require.Len(t, entries, 1500)
	assert.Equal(t, agenttest.MapEntry{Key: "key-0000", Value: "value-0000"}, entries[0])
	assert.Equal(t, agenttest.MapEntry{Key: "key-1499", Value: "value-1499"}, entries[1499])
}

func TestExecuteRepeatsMapDelUntilTheKeyIsGone(t *testing.T) {
	client, model := newClient(t)
	seed := []api.Op{
		{Kind: api.OpMapAdd, Path: "m", Key: "dup", Value: "1"},
		{Kind: api.OpMapAdd, Path: "m", Key: "dup", Value: "2"},
		{Kind: api.OpMapAdd, Path: "m", Key: "keep", Value: "3"},
	}
	_, err := client.Execute(compileAll(t, seed, nil))
	require.NoError(t, err)

	_, err = client.Execute(compileAll(t, []api.Op{{Kind: api.OpMapDel, Path: "m", Key: "dup"}}, nil))
	require.NoError(t, err)
	assert.Equal(t, []agenttest.MapEntry{{Key: "keep", Value: "3"}}, model.MapEntries("m"))
}

func TestExecuteAbortsACertTransactionOnFailure(t *testing.T) {
	client, model := newClient(t)
	files := map[string]string{"certs/tls.crt": "-----BEGIN CERTIFICATE-----\nno key here\n"}

	_, err := client.Execute(compileAll(t, []api.Op{{Kind: api.OpCertSet, Path: "certs/tls.crt"}}, files))
	require.ErrorIs(t, err, cli.ErrRejected)

	var aborted bool
	for _, c := range model.Sent() {
		if c == "abort ssl cert certs/tls.crt" {
			aborted = true
		}
	}
	assert.True(t, aborted, "a failed commit must abort the transaction: %v", model.Sent())
	model.With(func(m *agenttest.Model) { assert.Empty(t, m.Certs["certs/tls.crt"]) })
}

func TestExecuteCommitsAValidCertificate(t *testing.T) {
	client, model := newClient(t)
	pem := "-----BEGIN PRIVATE KEY-----\nx\n-----BEGIN CERTIFICATE-----\ny\n"
	files := map[string]string{"certs/tls.crt": pem}

	_, err := client.Execute(compileAll(t, []api.Op{{Kind: api.OpCertNew, Path: "certs/tls.crt"}}, files))
	require.NoError(t, err)
	model.With(func(m *agenttest.Model) { assert.Equal(t, pem, m.Certs["certs/tls.crt"]) })
}

func TestInfoReadsTheWorkerIdentity(t *testing.T) {
	client, model := newClient(t)
	info, err := client.Info()
	require.NoError(t, err)
	model.With(func(m *agenttest.Model) { assert.Equal(t, m.Pid, info.WorkerPID) })
	assert.Equal(t, "3.4.3-1deb11u1", info.Version)
}

func TestReloadReportsFailureWithHAProxysWords(t *testing.T) {
	client, model := newClient(t)
	require.NoError(t, waitForWorker(client))

	model.With(func(m *agenttest.Model) {
		m.ReloadFails = true
		m.ReloadLog = "[ALERT] config : parsing [haproxy.cfg:12] : unknown keyword 'nonsense'."
	})
	logs, err := client.Reload()
	require.Error(t, err)
	assert.Contains(t, logs, "unknown keyword")

	var before int
	model.With(func(m *agenttest.Model) {
		m.ReloadFails = false
		m.ReloadLog = "Loading success."
		before = m.Pid
	})
	logs, err = client.Reload()
	require.NoError(t, err)
	assert.Contains(t, logs, "Loading success")
	model.With(func(m *agenttest.Model) { assert.Equal(t, before+1, m.Pid) })
}

func TestShowProcAnswersOnTheMasterSocket(t *testing.T) {
	client, _ := newClient(t)
	out, err := client.ShowProc()
	require.NoError(t, err)
	assert.Contains(t, out, "worker")
}

func TestInventoryListsWhatTheWorkerLoaded(t *testing.T) {
	client, _ := newClient(t)
	files := map[string]string{
		"certs/tls.crt": "-----BEGIN PRIVATE KEY-----\n",
		"certs/ca.crt":  "-----BEGIN CERTIFICATE-----\n",
	}
	ops := []api.Op{
		{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "a", Value: "b"},
		{Kind: api.OpCertNew, Path: "certs/tls.crt"},
		{Kind: api.OpCANew, Path: "certs/ca.crt"},
		{Kind: api.OpCRTListAdd, Path: "certs/list.txt", Cert: "certs/tls.crt"},
	}
	_, err := client.Execute(compileAll(t, ops, files))
	require.NoError(t, err)

	inventory, err := client.Inventory(7)
	require.NoError(t, err)
	assert.Equal(t, uint64(7), inventory.Generation)
	assert.Equal(t, []string{"maps/host.map"}, inventory.Maps)
	assert.Equal(t, []string{"certs/tls.crt"}, inventory.Certs)
	assert.Equal(t, []string{"certs/ca.crt"}, inventory.CAFiles)
	assert.Equal(t, []string{"certs/list.txt"}, inventory.CRTLists)
}

func TestReadBackSeesWhatTheOpsWrote(t *testing.T) {
	client, _ := newClient(t)
	ops := []api.Op{
		{Kind: api.OpBackendAdd, Backend: "be-a", Profile: "prof", Mode: "http"},
		{Kind: api.OpServerAdd, Backend: "be-a", Server: "srv1", Address: "10.0.0.1", Port: 80},
		{Kind: api.OpMapAdd, Path: "m", Key: "dup", Value: "one"},
		{Kind: api.OpMapAdd, Path: "m", Key: "dup", Value: "two"},
	}
	_, err := client.Execute(compileAll(t, ops, nil))
	require.NoError(t, err)

	entries, err := client.MapEntries("m")
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"dup": {"one", "two"}}, entries)

	names, err := client.ServerNames("be-a")
	require.NoError(t, err)
	assert.Equal(t, map[string]struct{}{"srv1": {}}, names)
}

func TestSplitSeparatesTheDeleteTail(t *testing.T) {
	ops := []api.Op{
		{Kind: api.OpBackendUnpublish, Backend: "be-a"},
		{Kind: api.OpServerDisable, Backend: "be-a", Server: "srv1"},
		{Kind: api.OpServerWaitRemovable, Backend: "be-a", Server: "srv1", TimeoutMs: 2000},
		{Kind: api.OpShutdownSessions, Backend: "be-a", Server: "srv1"},
		{Kind: api.OpServerDel, Backend: "be-a", Server: "srv1"},
		{Kind: api.OpBackendWaitRemovable, Backend: "be-a", TimeoutMs: 2000},
		{Kind: api.OpBackendDel, Backend: "be-a"},
	}
	inline, servers, backends := cli.Split(ops)

	kinds := make([]string, 0, len(inline))
	for i := range inline {
		kinds = append(kinds, inline[i].Kind)
	}
	assert.Equal(t, []string{api.OpBackendUnpublish, api.OpServerDisable}, kinds)
	assert.Equal(t, []cli.ServerRef{{Backend: "be-a", Server: "srv1"}}, servers)
	assert.Equal(t, []string{"be-a"}, backends)
}

func TestDeferralsRemoveServersAndBackendsOffTheApplyPath(t *testing.T) {
	client, model := newClient(t)
	setup := []api.Op{
		{Kind: api.OpBackendAdd, Backend: "be-a", Profile: "prof", Mode: "http"},
		{Kind: api.OpServerAdd, Backend: "be-a", Server: "srv1", Address: "10.0.0.1", Port: 80},
	}
	_, err := client.Execute(compileAll(t, setup, nil))
	require.NoError(t, err)

	deferrals := cli.NewDeferrals(client, slog.New(slog.DiscardHandler), nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = deferrals.Start(ctx) }()

	require.NoError(t, deferrals.Enqueue([]cli.ServerRef{{Backend: "be-a", Server: "srv1"}}, []string{"be-a"}))
	if !assert.Eventually(t, func() bool {
		return !model.HasBackend("be-a")
	}, 10*time.Second, 20*time.Millisecond) {
		t.Fatalf("sent: %v", model.Sent())
	}
	assert.Empty(t, deferrals.Pending().Servers)
}

func TestDeferralsShutDownSessionsWhenTheWaitExpires(t *testing.T) {
	client, model := newClient(t)
	setup := []api.Op{
		{Kind: api.OpBackendAdd, Backend: "be-a", Profile: "prof", Mode: "http"},
		{Kind: api.OpServerAdd, Backend: "be-a", Server: "srv1", Address: "10.0.0.1", Port: 80},
	}
	_, err := client.Execute(compileAll(t, setup, nil))
	require.NoError(t, err)
	model.With(func(m *agenttest.Model) { m.BlockedServers["be-a/srv1"] = true })

	deferrals := cli.NewDeferrals(client, slog.New(slog.DiscardHandler), nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = deferrals.Start(ctx) }()

	require.NoError(t, deferrals.Enqueue([]cli.ServerRef{{Backend: "be-a", Server: "srv1"}}, nil))
	if !assert.Eventually(t, func() bool {
		return len(model.ServerNames("be-a")) == 0
	}, 10*time.Second, 20*time.Millisecond) {
		t.Fatalf("sent: %v", model.Sent())
	}

	var shutdown bool
	for _, c := range model.Sent() {
		if c == "shutdown sessions server be-a/srv1" {
			shutdown = true
		}
	}
	assert.True(t, shutdown)
}

func TestDeferralsRefuseMoreThanTheCap(t *testing.T) {
	client, _ := newClient(t)
	deferrals := cli.NewDeferrals(client, slog.New(slog.DiscardHandler), nil)

	servers := make([]cli.ServerRef, api.MaxPendingServerDeletes+1)
	assert.ErrorIs(t, deferrals.Enqueue(servers, nil), cli.ErrDeferralOverflow)

	backends := make([]string, api.MaxPendingBackendDeletes+1)
	assert.ErrorIs(t, deferrals.Enqueue(nil, backends), cli.ErrDeferralOverflow)
}

// waitForWorker primes client-native's process-wide version cache, which its
// Reload() gate consults.
func waitForWorker(client *cli.Client) error {
	_, err := client.Info()
	return err
}
