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

package server_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/haproxytest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/server"
)

func drainOverSocket(t *testing.T, socket string) server.DrainResult {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	resp, err := client.Get("http://drain" + api.PathDrain)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result server.DrainResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	return result
}

// The preStop hook's path: the counter of the probe frontend keeps moving
// while the traffic frontend is quiet, and the drain ends on the quiet period.
func TestDrainSocketEndsWhenOnlyProbesArrive(t *testing.T) {
	model := haproxytest.Start(t)
	socket := filepath.Join(t.TempDir(), "drain.sock")
	h := newHarness(t, withModel(model), withDrain(socket, 150*time.Millisecond, 2*time.Second))
	_ = h
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				model.With(func(m *haproxytest.Model) { m.ProbeConnections++ })
			}
		}
	}()
	defer close(stop)
	start := time.Now()
	result := drainOverSocket(t, socket)
	assert.Equal(t, server.DrainReasonQuiet, result.Reason)
	assert.GreaterOrEqual(t, time.Since(start), 150*time.Millisecond)
	assert.Less(t, time.Since(start), time.Second)
}

// The agent stopping (for whatever reason) while a hook is being served must
// not turn the drain into a cut-short 200: the drain finishes on its own terms.
func TestDrainSocketFinishesADrainInFlightWhenTheAgentStops(t *testing.T) {
	model := haproxytest.Start(t)
	socket := filepath.Join(t.TempDir(), "drain.sock")
	h := newHarness(t, withModel(model), withDrain(socket, 300*time.Millisecond, 2*time.Second))
	go func() {
		time.Sleep(50 * time.Millisecond)
		h.stopOnce()
	}()
	start := time.Now()
	result := drainOverSocket(t, socket)
	assert.Equal(t, server.DrainReasonQuiet, result.Reason)
	assert.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond)
}

func TestDrainSocketWaitsWhileTrafficStillArrives(t *testing.T) {
	model := haproxytest.Start(t)
	socket := filepath.Join(t.TempDir(), "drain.sock")
	_ = newHarness(t, withModel(model), withDrain(socket, 100*time.Millisecond, 300*time.Millisecond))
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				model.With(func(m *haproxytest.Model) { m.TrafficConnections++ })
			}
		}
	}()
	defer close(stop)
	start := time.Now()
	result := drainOverSocket(t, socket)
	assert.Equal(t, server.DrainReasonBound, result.Reason)
	assert.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond)
}
