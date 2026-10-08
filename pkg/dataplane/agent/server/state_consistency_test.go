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

package server

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/haproxytest"
)

func TestVerifiedStateSerializesWorkerObservation(t *testing.T) {
	model, agent := newWorkerObservationServer(t)
	agent.state.AppliedPlanID = "confirmed"
	interleaved := make(chan bool, 1)
	model.With(func(m *haproxytest.Model) {
		m.Reject = func(command string) (string, bool) {
			if !strings.HasSuffix(command, "show info float") {
				return "", false
			}
			if !agent.workerObservation.TryLock() {
				interleaved <- false
				return "", false
			}
			defer agent.workerObservation.Unlock()
			interleaved <- true
			return "", false
		}
	})
	state, err := agent.stateResponse(true, false)
	require.NoError(t, err)
	select {
	case allowed := <-interleaved:
		assert.False(t, allowed, "a concurrent reload must not replace the worker between verification and adoption")
	default:
		t.Fatal("verified state did not observe the worker")
	}
	assert.Equal(t, "confirmed", state.AppliedPlanID)
}

func TestReloadSerializesWorkerObservation(t *testing.T) {
	model, agent := newWorkerObservationServer(t)
	interleaved := make(chan bool, 1)
	model.With(func(m *haproxytest.Model) {
		m.OnReload = func(*haproxytest.Model) {
			allowed := agent.workerObservation.TryLock()
			if allowed {
				agent.workerObservation.Unlock()
			}
			interleaved <- allowed
		}
	})
	run := &applyRun{server: agent}
	require.NoError(t, run.performReload("reloaded", "proof"))
	select {
	case allowed := <-interleaved:
		assert.False(t, allowed, "verified state must not adopt a worker until reload adoption finishes")
	default:
		t.Fatal("reload did not replace the worker")
	}
	assert.Equal(t, "reloaded", agent.state.RunningPlanID)
}

func newWorkerObservationServer(t *testing.T) (*haproxytest.HAProxy, *Server) {
	t.Helper()
	model := haproxytest.Start(t)
	agent, err := New(t.Context(), &Config{
		BaseDir: t.TempDir(), ConfigFile: "haproxy.cfg", StateFile: ".haptic-agent.json",
		MasterSocket: model.MasterSocket(), WorkerSocket: model.WorkerSocket(),
		Logger: slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	worker, err := agent.runtime.Info()
	require.NoError(t, err)
	agent.adoptWorker(worker)
	return model, agent
}

func TestApplyCommitPreservesConcurrentWorkerInvalidation(t *testing.T) {
	agent := newUninitializedStateServer(t)
	agent.state.AppliedPlanID = "old"
	run := &applyRun{
		server: agent, manifest: &api.Manifest{PlanID: "new"},
		invalidations: agent.invalidationCount(), appliedProof: "proof",
	}
	agent.invalidateBaseline()
	agent.mu.Lock()
	agent.commitLocked(run)
	agent.mu.Unlock()
	assert.Empty(t, agent.state.AppliedPlanID)
	assert.Empty(t, agent.state.AppliedPlanProof)
	assert.Empty(t, agent.state.WorkerOpsPlanID)
}
