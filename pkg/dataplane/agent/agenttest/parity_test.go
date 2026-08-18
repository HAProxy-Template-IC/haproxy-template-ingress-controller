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
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/agenttest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/haproxytest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/server"
)

// The plan the seeded worker starts from, shared by every scenario.
var seedFiles = map[string]string{
	"haproxy.cfg":   "global\n",
	"maps/host.map": "example.com be-1\n",
}

// observation is what one apply told the caller. Worker pids, timestamps and
// on-disk state are per-implementation and stay out of it; status, mode and the
// four plan ids are the contract a deployer is written against.
type observation struct {
	Step            string
	Status          int
	OK              bool
	Mode            string
	ErrorStage      string
	ConflictReason  string
	Missing         []string
	OpsRan          bool
	AppliedPlanID   string
	RunningPlanID   string
	WorkerOpsPlanID string
	LKGPlanID       string
}

// parityAgent is one implementation behind the client both ends share.
type parityAgent struct {
	client *client.Client
	// pendReload leaves a reload waiting, which each end reaches differently:
	// the fake is told, the real agent's pacing window does it after its first
	// reload.
	pendReload func()
	seen       []observation
}

func (p *parityAgent) apply(t *testing.T, step string, m *api.Manifest, parts map[string]io.Reader) {
	t.Helper()
	result, err := p.client.Apply(t.Context(), m, parts, nil)
	seen := observation{Step: step, Status: http.StatusOK}
	var conflict *client.ConflictError
	var missing *client.MissingError
	switch {
	case errors.As(err, &conflict):
		seen.Status = http.StatusConflict
		seen.ConflictReason = conflict.Conflict.Reason
		seen.AppliedPlanID = conflict.Conflict.AppliedPlanID
		seen.RunningPlanID = conflict.Conflict.RunningPlanID
		seen.WorkerOpsPlanID = conflict.Conflict.WorkerOpsPlanID
		seen.LKGPlanID = conflict.Conflict.LKGPlanID
	case errors.As(err, &missing):
		seen.Status = http.StatusConflict
		seen.Missing = missing.Missing
	default:
		require.NoError(t, err, step)
		seen.OK, seen.Mode = result.OK, result.Mode
		seen.OpsRan = len(result.OpResults) > 0
		seen.AppliedPlanID = result.AppliedPlanID
		seen.RunningPlanID = result.RunningPlanID
		seen.WorkerOpsPlanID = result.WorkerOpsPlanID
		seen.LKGPlanID = result.LKGPlanID
		if result.Error != nil {
			seen.ErrorStage = result.Error.Stage
		}
	}
	p.seen = append(p.seen, seen)
}

// TestFakeAndRealAgentAnswerAlike runs one manifest sequence through the
// in-process fake and the real agent and requires the same answers. The fake is
// the seam the deployer is developed against, so a divergence here is a deployer
// written for a contract production does not have.
func TestFakeAndRealAgentAnswerAlike(t *testing.T) {
	scenarios := []struct {
		name string
		run  func(t *testing.T, p *parityAgent)
	}{
		{name: "first apply, runtime ops, a stale baseline and a missing part", run: lifecycleScenario},
		{name: "a pending reload coalesces and only in-place ops run", run: pendingReloadScenario},
		{name: "a revert restores the last known good set", run: revertScenario},
	}

	for _, sc := range scenarios {
		// Serial on purpose: two concurrent server.New calls race inside
		// client-native's runtime version cache (vendor/.../runtime_client.go).
		t.Run(sc.name, func(t *testing.T) {
			fake := newFakeParityAgent(t)
			sc.run(t, fake)
			production := newRealParityAgent(t)
			sc.run(t, production)

			assert.Equal(t, fake.seen, production.seen)
		})
	}
}

func lifecycleScenario(t *testing.T, p *parityAgent) {
	t.Helper()
	// An unknown baseline reloads even when the manifest asks for ops.
	first, parts := build("plan-1", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 1}, seedFiles)
	first.Ops = []api.Op{{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "example.com", Value: "be-1"}}
	p.apply(t, "first apply", first, parts)

	routed := map[string]string{"haproxy.cfg": "global\n", "maps/host.map": "example.com be-1\nnew.example.com be-2\n"}
	runtime, parts := build("plan-2", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 2}, routed)
	runtime.ExpectedPrevPlanID = "plan-1"
	runtime.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 1}
	runtime.Ops = []api.Op{{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "new.example.com", Value: "be-2"}}
	p.apply(t, "runtime ops", runtime, parts)

	stale, parts := build("plan-3", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 3}, routed)
	stale.ExpectedPrevPlanID = "plan-1"
	stale.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 1}
	p.apply(t, "a stale baseline", stale, parts)

	grown := map[string]string{
		"haproxy.cfg":    "global\n",
		"maps/host.map":  "example.com be-1\nnew.example.com be-2\n",
		"maps/other.map": "example.com be-1\n",
	}
	added, parts := build("plan-3", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 3}, grown)
	added.ExpectedPrevPlanID = "plan-2"
	added.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 2}
	// A path whose bytes the agent already holds under another name is still
	// missing: the agent stores files by path.
	delete(parts, "maps/other.map")
	p.apply(t, "a part the agent does not hold", added, parts)
}

func pendingReloadScenario(t *testing.T, p *parityAgent) {
	t.Helper()
	first, parts := build("plan-1", api.ModeReload, api.Token{LeaderEpoch: 1, RenderSeq: 1}, seedFiles)
	p.apply(t, "first apply", first, parts)
	p.pendReload()

	tuned := map[string]string{"haproxy.cfg": "global\n  nbthread 4\n", "maps/host.map": "example.com be-1\n"}
	paced, parts := build("plan-2", api.ModeReload, api.Token{LeaderEpoch: 1, RenderSeq: 2}, tuned)
	paced.ExpectedPrevPlanID = "plan-1"
	paced.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 1}
	p.apply(t, "a second reload is paced", paced, parts)

	inPlace, parts := build("plan-3", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 3}, tuned)
	inPlace.ExpectedPrevPlanID = "plan-2"
	inPlace.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 2}
	inPlace.ExpectedWorkerOpsPlanID = "plan-1"
	inPlace.InPlaceOps = []api.Op{{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "new.example.com", Value: "be-2"}}
	p.apply(t, "in-place ops while the reload waits", inPlace, parts)

	stale, parts := build("plan-4", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 4}, tuned)
	stale.ExpectedPrevPlanID = "plan-3"
	stale.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 3}
	stale.ExpectedWorkerOpsPlanID = "plan-from-another-life"
	stale.InPlaceOps = []api.Op{{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "third.example.com", Value: "be-3"}}
	p.apply(t, "in-place ops on a stale worker baseline", stale, parts)
}

func revertScenario(t *testing.T, p *parityAgent) {
	t.Helper()
	first, parts := build("plan-1", api.ModeReload, api.Token{LeaderEpoch: 1, RenderSeq: 1}, seedFiles)
	p.apply(t, "first apply", first, parts)

	routed := map[string]string{"haproxy.cfg": "global\n", "maps/host.map": "example.com be-1\nnew.example.com be-2\n"}
	runtime, parts := build("plan-2", api.ModeAuto, api.Token{LeaderEpoch: 1, RenderSeq: 2}, routed)
	runtime.ExpectedPrevPlanID = "plan-1"
	runtime.ExpectedPrevToken = api.Token{LeaderEpoch: 1, RenderSeq: 1}
	runtime.Ops = []api.Op{{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "new.example.com", Value: "be-2"}}
	p.apply(t, "runtime ops", runtime, parts)

	revert := &api.Manifest{
		PlanID:            "plan-3",
		PlanSchemaVersion: 1,
		Token:             api.Token{LeaderEpoch: 1, RenderSeq: 3},
		// A revert targets the LKG by definition, so its baseline is not fenced.
		ExpectedPrevPlanID: "plan-from-another-life",
		Mode:               api.ModeRevertLKG,
	}
	p.apply(t, "revert to the last known good set", revert, nil)
}

func newFakeParityAgent(t *testing.T) *parityAgent {
	t.Helper()
	agent := agenttest.New(t)
	return &parityAgent{
		client:     newClient(t, agent),
		pendReload: func() { agent.SetReloadPending(true) },
	}
}

// newRealParityAgent runs the production agent against the HAProxy model. Its
// reload interval makes every reload after the first a paced one, which is how
// the real end reaches the state the fake is simply told to be in.
func newRealParityAgent(t *testing.T) *parityAgent {
	t.Helper()
	model := haproxytest.Start(t)
	model.With(func(m *haproxytest.Model) {
		m.Maps["maps/host.map"] = []haproxytest.MapEntry{{Key: "example.com", Value: "be-1"}}
	})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	agent, err := server.New(ctx, &server.Config{
		BaseDir:           t.TempDir(),
		ConfigFile:        "haproxy.cfg",
		MasterSocket:      model.MasterSocket(),
		WorkerSocket:      model.WorkerSocket(),
		StateFile:         ".haptic-agent.json",
		Listen:            "127.0.0.1:0",
		ReloadIntervalMin: time.Minute,
		Username:          agenttest.DefaultUsername,
		Password:          agenttest.DefaultPassword,
		AgentVersion:      "parity",
		Logger:            slog.New(slog.DiscardHandler),
		Registry:          prometheus.NewRegistry(),
	})
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- agent.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the real agent did not shut down")
		}
	})
	require.Eventually(t, agent.Ready, 10*time.Second, 10*time.Millisecond)

	c, err := client.New(&client.Config{
		BaseURL:             "http://" + agent.Addr(),
		Username:            agenttest.DefaultUsername,
		Password:            agenttest.DefaultPassword,
		Timeout:             30 * time.Second,
		PerPodApplyTimeout:  30 * time.Second,
		ConnectRetryBackoff: time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return &parityAgent{client: c, pendReload: func() {}}
}
