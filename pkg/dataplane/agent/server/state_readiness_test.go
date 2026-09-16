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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

type observedStateContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *observedStateContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestStateWaitsForInitializedBaseline(t *testing.T) {
	agent := newUninitializedStateServer(t)
	ctx := &observedStateContext{Context: t.Context(), waiting: make(chan struct{})}
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, api.PathState+"?plan=0", http.NoBody)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		agent.handleState(response, request)
		close(done)
	}()
	select {
	case <-ctx.waiting:
	case <-time.After(time.Second):
		t.Fatal("state handler did not wait for initialization")
	}
	select {
	case <-done:
		t.Fatal("state handler published an uninitialized baseline")
	default:
	}
	agent.mu.Lock()
	agent.state.Generation = 42
	agent.mu.Unlock()
	close(agent.initialized)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("state handler did not resume after initialization")
	}
	require.Equal(t, http.StatusOK, response.Code)
	var state api.State
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &state))
	assert.EqualValues(t, 42, state.Generation)
}

func TestStateWaitRespectsCancellation(t *testing.T) {
	agent := newUninitializedStateServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, api.PathState, http.NoBody)
	response := httptest.NewRecorder()
	agent.handleState(response, request)
	assert.Empty(t, response.Body.String())
	assert.False(t, agent.Ready())
}

func newUninitializedStateServer(t *testing.T) *Server {
	t.Helper()
	base := t.TempDir()
	agent, err := New(t.Context(), &Config{
		BaseDir:      base,
		ConfigFile:   "haproxy.cfg",
		StateFile:    ".haptic-agent.json",
		MasterSocket: filepath.Join(base, "master.sock"),
		WorkerSocket: filepath.Join(base, "worker.sock"),
	})
	require.NoError(t, err)
	return agent
}
